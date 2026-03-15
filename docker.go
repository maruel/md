// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

//go:embed all:rsc
var rscFS embed.FS

// prepareBuildContext writes the embedded rsc/ tree to a temp directory.
// Returns the temp dir path (caller must clean up).
func prepareBuildContext() (dir string, retErr error) {
	tmp, err := os.MkdirTemp("", "md-build-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, os.RemoveAll(tmp))
		}
	}()
	// Walk the embedded filesystem and write all files.
	err = fs.WalkDir(rscFS, "rsc", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the leading "rsc/" prefix so we get a clean build context.
		rel := strings.TrimPrefix(path, "rsc/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := rscFS.ReadFile(path)
		if err != nil {
			return err
		}
		// Preserve executable bits for shell scripts.
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, "xstartup") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return "", fmt.Errorf("extracting build context: %w", err)
	}
	return tmp, nil
}

// contextSHAHash computes a deterministic SHA-256 hash over the build context
// directory and the SSH key files. It walks files in sorted order and hashes
// each relative path and its contents.
func contextSHAHash(buildCtxDir, keysDir string) (string, error) {
	h := sha256.New()
	// Hash all files in buildCtxDir.
	if err := hashDir(h, buildCtxDir); err != nil {
		return "", err
	}
	// Hash specific key files from keysDir.
	for _, name := range []string{"ssh_host_ed25519_key", "ssh_host_ed25519_key.pub", "authorized_keys"} {
		data, err := os.ReadFile(filepath.Join(keysDir, name))
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, name)
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDir walks dir in sorted order, hashing each file's relative path and
// contents into h.
func hashDir(h io.Writer, dir string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, rel := range paths {
		// Use forward slashes so the hash matches embeddedContextSHA on all
		// platforms (fs.WalkDir on embed.FS always uses forward slashes).
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		_, _ = h.Write(data)
	}
	return nil
}

func dockerInspectFormat(ctx context.Context, rt, name, format string) (string, error) {
	return runCmd(ctx, "", []string{rt, "image", "inspect", name, "--format", format}, true)
}

func getImageVersionLabel(ctx context.Context, rt, imageName string) string {
	out, err := dockerInspectFormat(ctx, rt, imageName, `{{index .Config.Labels "org.opencontainers.image.version"}}`)
	if err != nil || out == "" || out == "<no value>" {
		return ""
	}
	return out
}

// getRemoteManifestDigest queries the registry for the per-architecture
// manifest digest without downloading layers.
//
// For a multi-arch image the digest hierarchy is:
//
//	Image Index (manifest list)         sha256:AAA
//	  └── Per-platform Manifest (amd64) sha256:BBB  ← manifest digest
//	        ├── Config                  sha256:CCC  ← docker's {{.Id}}
//	        └── Layers ...
//
// We compare manifest digests (sha256:BBB): this is what "docker pull" prints,
// what {{index .RepoDigests 0}} stores as "repo@sha256:BBB", and what
// "manifest inspect" returns in manifests[].digest. Any change to layers,
// config, or manifest metadata produces a different manifest digest, making it
// a reliable staleness signal.
//
// Both Docker schema v2 manifest lists and OCI image indexes share the same
// "manifests[].{digest, platform}" JSON structure, so one parser covers both
// runtimes and both formats.
func getRemoteManifestDigest(ctx context.Context, rt, image, arch string) (string, error) {
	out, err := runCmd(ctx, "", []string{rt, "manifest", "inspect", image}, true)
	if err != nil {
		return "", err
	}
	var index manifestIndex
	if err := json.Unmarshal([]byte(out), &index); err != nil {
		return "", fmt.Errorf("parsing manifest inspect output: %w", err)
	}
	for _, m := range index.Manifests {
		if m.Platform.Architecture == arch && m.Platform.OS == "linux" && m.Digest != "" {
			return m.Digest, nil
		}
	}
	if len(index.Manifests) == 1 && index.Manifests[0].Digest != "" {
		return index.Manifests[0].Digest, nil
	}
	return "", fmt.Errorf("no manifest for linux/%s in %s", arch, image)
}

// repoDigestOnly extracts the digest portion from a repo digest reference
// (e.g. "ghcr.io/foo/bar@sha256:abc" → "sha256:abc"). Returns the input
// unchanged if it contains no "@".
func repoDigestOnly(repoDigest string) string {
	if _, after, found := strings.Cut(repoDigest, "@"); found {
		return after
	}
	return repoDigest
}

// embeddedContextSHA computes a deterministic SHA-256 hash over the embedded
// build context (rsc/) and SSH key files without extracting to disk. It
// produces the same result as contextSHAHash on the extracted build context.
func embeddedContextSHA(keysDir string) (string, error) {
	h := sha256.New()
	var paths []string
	if err := fs.WalkDir(rscFS, "rsc", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if rel := strings.TrimPrefix(path, "rsc/"); rel != "" {
				paths = append(paths, rel)
			}
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, rel := range paths {
		_, _ = io.WriteString(h, rel)
		data, err := rscFS.ReadFile("rsc/" + rel)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(data)
	}
	for _, name := range []string{"ssh_host_ed25519_key", "ssh_host_ed25519_key.pub", "authorized_keys"} {
		data, err := os.ReadFile(filepath.Join(keysDir, name))
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, name)
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type remoteDigestEntry struct {
	digest  string
	err     error
	expires time.Time
}

type manifestPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type manifestEntry struct {
	Digest   string           `json:"digest"`
	Platform manifestPlatform `json:"platform"`
}

type manifestIndex struct {
	Manifests []manifestEntry `json:"manifests"`
}

type activeCM struct {
	cm       CacheMount
	hostPath string
}

type cacheInfoStat struct {
	name     string
	hostPath string
	files    int64
	bytes    int64
	missing  bool
}

// imageBuildCacheEntry caches the result of imageBuildNeeded so that
// back-to-back calls with the same inputs skip docker inspect exec calls.
type imageBuildCacheEntry struct {
	baseImage  string
	contextSHA string
	cacheKey   string
	needed     bool
}

// cachedRemoteManifestDigest returns the remote per-architecture manifest digest.
// When Client.DigestCacheTTL is non-zero, results are cached for that duration
// to skip repeated registry round-trips. When zero, the registry is always queried.
func (c *Client) cachedRemoteManifestDigest(ctx context.Context, rt, image, arch string) (string, error) {
	if c.DigestCacheTTL == 0 {
		return getRemoteManifestDigest(ctx, rt, image, arch)
	}
	key := rt + "\x00" + image + "\x00" + arch
	c.mu.Lock()
	if e, ok := c.digestCache[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		return e.digest, e.err
	}
	c.mu.Unlock()
	digest, err := getRemoteManifestDigest(ctx, rt, image, arch)
	c.mu.Lock()
	c.digestCache[key] = remoteDigestEntry{digest: digest, err: err, expires: time.Now().Add(c.DigestCacheTTL)}
	c.mu.Unlock()
	return digest, err
}

// activeCacheKey filters caches to those whose host directories exist and
// returns the cache spec key for the active set.
func activeCacheKey(caches []CacheMount, home string) string {
	var active []CacheMount
	for _, cm := range caches {
		if _, err := os.Stat(resolveHostPath(cm.HostPath, home)); err == nil {
			active = append(active, cm)
		}
	}
	return cacheSpecKey(active)
}

// userImageName returns the Docker image name for a given base image and
// active cache configuration. The name includes a content hash so that
// different base images or cache sets produce distinct images without
// clobbering each other.
func userImageName(baseImage, cacheKey string) string {
	h := sha256.Sum256([]byte(baseImage + "\x00" + cacheKey))
	return "md-user-" + hex.EncodeToString(h[:16])
}

// cacheSpecKey returns a short hash over the requested cache names and
// container paths. Returns empty string when caches is nil or empty.
// Only the spec (name + path) is hashed, not the cache contents.
func cacheSpecKey(caches []CacheMount) string {
	if len(caches) == 0 {
		return ""
	}
	specs := make([]string, len(caches))
	for i, c := range caches {
		specs[i] = c.Name + ":" + c.ContainerPath
	}
	sort.Strings(specs)
	h := sha256.Sum256([]byte(strings.Join(specs, ",")))
	return hex.EncodeToString(h[:8])
}

// imageBuildNeeded reports whether the customized Docker image needs to be
// rebuilt. It checks the base image digest, build context SHA, and cache spec
// key against labels on the existing image. For remote base images it also
// verifies the local copy matches the registry.
// home is used to resolve "~/" in cache HostPaths so only caches whose host
// directory currently exists are compared (matching what appendCacheLayers
// would actually inject).
func (c *Client) imageBuildNeeded(ctx context.Context, rt, imageName, baseImage, keysDir, home string, caches []CacheMount) bool {
	// Compute cheap inputs first so we can check the cache.
	contextSHA, err := embeddedContextSHA(keysDir)
	if err != nil {
		return true
	}
	var activeCaches []CacheMount
	for _, cm := range caches {
		if _, err := os.Stat(resolveHostPath(cm.HostPath, home)); err == nil {
			activeCaches = append(activeCaches, cm)
		}
	}
	activeKey := cacheSpecKey(activeCaches)

	// Check cached result from a previous call with the same inputs.
	c.mu.Lock()
	if e := c.imageBuildCache; e != nil && e.baseImage == baseImage && e.contextSHA == contextSHA && e.cacheKey == activeKey {
		needed := e.needed
		c.mu.Unlock()
		return needed
	}
	c.mu.Unlock()

	needed := c.imageBuildNeededSlow(ctx, rt, imageName, baseImage, contextSHA, activeKey)

	c.mu.Lock()
	c.imageBuildCache = &imageBuildCacheEntry{
		baseImage:  baseImage,
		contextSHA: contextSHA,
		cacheKey:   activeKey,
		needed:     needed,
	}
	c.mu.Unlock()
	return needed
}

// invalidateImageBuildCache clears the cached imageBuildNeeded result.
// Must be called after a successful image build so the next check re-evaluates.
func (c *Client) invalidateImageBuildCache() {
	c.mu.Lock()
	c.imageBuildCache = nil
	c.mu.Unlock()
}

// imageBuildNeededSlow performs the full check with docker inspect calls.
func (c *Client) imageBuildNeededSlow(ctx context.Context, rt, imageName, baseImage, contextSHA, activeKey string) bool {
	// Quick check: does the customized image have labels at all?
	currentDigest, err := dockerInspectFormat(ctx, rt, imageName, `{{index .Config.Labels "md.base_digest"}}`)
	if err != nil || currentDigest == "" || currentDigest == "<no value>" {
		return true
	}
	currentContext, err := dockerInspectFormat(ctx, rt, imageName, `{{index .Config.Labels "md.context_sha"}}`)
	if err != nil || currentContext == "" || currentContext == "<no value>" {
		return true
	}

	// Get the base image digest.
	var baseDigest string
	if d, err := dockerInspectFormat(ctx, rt, baseImage, "{{index .RepoDigests 0}}"); err == nil && d != "" {
		baseDigest = d
	} else if id, err := dockerInspectFormat(ctx, rt, baseImage, "{{.Id}}"); err == nil {
		baseDigest = id
	} else {
		return true
	}
	if currentDigest != baseDigest {
		return true
	}

	// For remote images, verify the local base is up to date with the registry.
	// Errors are intentionally ignored: a registry failure is not a reason to rebuild;
	// the base digest label comparison above already catches locally-pulled updates.
	isLocal := !strings.Contains(baseImage, "/")
	if !isLocal {
		repoDigest, _ := dockerInspectFormat(ctx, rt, baseImage, "{{index .RepoDigests 0}}")
		localRef := repoDigestOnly(repoDigest)
		remoteDigest, err := c.cachedRemoteManifestDigest(ctx, rt, baseImage, runtime.GOARCH)
		if err == nil && remoteDigest != localRef {
			return true
		}
	}

	if currentContext != contextSHA {
		return true
	}

	currentKey, err := dockerInspectFormat(ctx, rt, imageName, `{{index .Config.Labels "md.cache_key"}}`)
	if err != nil || currentKey == "<no value>" {
		currentKey = ""
	}
	if activeKey != currentKey {
		return true
	}

	return false
}

// appendCacheLayers appends a RUN mkdir and COPY instructions to the Dockerfile
// at dockerfilePath, returns --build-context args for docker build and the
// cache spec key for the caches that were actually injected (may differ from
// the requested set when some host paths do not exist).
// mountPaths lists container-side -v mount targets whose leaf dirs must be
// pre-created. For caches only the intermediary ancestors are created; COPY
// --chown handles the leaf. Caches whose host path does not exist are silently
// skipped. "~/" in HostPath is resolved using home.
func appendCacheLayers(dockerfilePath string, caches []CacheMount, home string, mountPaths []string) (extraArgs []string, activeKey string, err error) {
	var active []activeCM
	for _, cm := range caches {
		hostPath := resolveHostPath(cm.HostPath, home)
		if _, err := os.Stat(hostPath); err != nil {
			continue
		}
		active = append(active, activeCM{cm, hostPath})
	}

	// activeKey reflects only the caches actually injected, not all requested.
	// This ensures imageBuildNeeded detects when a previously-skipped cache dir
	// appears on the host and triggers a rebuild.
	activeMounts := make([]CacheMount, len(active))
	for i, a := range active {
		activeMounts[i] = a.cm
	}
	activeKey = cacheSpecKey(activeMounts)

	// Collect directories to pre-create:
	// - For cache COPY destinations: intermediaries only; COPY --chown creates
	//   and owns the leaf.
	// - For runtime -v mount targets: the full path (leaf included), since no
	//   COPY will create it. mkdir -p also covers any intermediaries.
	const base = "/home/user"
	seen := map[string]bool{}
	for _, a := range active {
		for dir := path.Dir(a.cm.ContainerPath); dir != base && dir != "." && dir != "/"; dir = path.Dir(dir) {
			seen[dir] = true
		}
	}
	for _, p := range mountPaths {
		seen[p] = true
	}

	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	extraArgs = make([]string, 0, 2*len(active))
	var copies strings.Builder
	for _, a := range active {
		fmt.Fprintf(&copies, "COPY --chown=user:user --from=%s / %s/\n", a.cm.Name, a.cm.ContainerPath)
		extraArgs = append(extraArgs, "--build-context", a.cm.Name+"="+a.hostPath)
	}

	if len(dirs) == 0 && copies.Len() == 0 {
		return nil, activeKey, nil
	}
	f, err := os.OpenFile(dockerfilePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil, activeKey, err
	}
	var snippet strings.Builder
	snippet.WriteString("\n# Pre-create mount targets and inject build-time cache layers.\n")
	if len(dirs) > 0 {
		joined := strings.Join(dirs, " ")
		fmt.Fprintf(&snippet, "RUN mkdir -p %s && chown user:user %s\n", joined, joined)
	}
	snippet.WriteString(copies.String())
	_, err = fmt.Fprint(f, snippet.String())
	return extraArgs, activeKey, errors.Join(err, f.Close())
}

// buildCustomizedImage builds the per-user Docker image. keysDir is the
// directory containing SSH host keys and authorized_keys, supplied to Docker
// as a named build context "md-keys". home is the user's home directory used
// to resolve "~/" in cache HostPaths. mountPaths lists container-side -v mount
// targets to pre-create with user ownership.
func buildCustomizedImage(ctx context.Context, rt string, w io.Writer, buildCtxDir, keysDir, imageName, baseImage, home string, caches []CacheMount, mountPaths []string, quiet bool) error {
	arch := runtime.GOARCH
	// Local-only images (no "/" in name) are never pulled from a registry.
	// A tag (":latest") does not imply a registry; only a "/" does.
	isLocal := !strings.Contains(baseImage, "/")
	if isLocal {
		if _, err := runCmd(ctx, "", []string{rt, "image", "inspect", "--format", "{{.Id}}", baseImage}, true); err != nil {
			return fmt.Errorf("local image %s not found; build it first with 'md build-image'", baseImage)
		}
		if !quiet {
			_, _ = fmt.Fprintf(w, "- Using local base image %s.\n", baseImage)
		}
	} else {
		// Check if local image is already up to date with remote.
		needsPull := true
		if repoDigest, err := runCmd(ctx, "", []string{rt, "image", "inspect", "--format", "{{index .RepoDigests 0}}", baseImage}, true); err == nil && repoDigest != "" {
			localRef := repoDigestOnly(repoDigest)
			if remoteDigest, err := getRemoteManifestDigest(ctx, rt, baseImage, arch); err == nil && remoteDigest == localRef {
				needsPull = false
			}
		}
		if needsPull {
			if !quiet {
				_, _ = fmt.Fprintf(w, "- Pulling base image %s ...\n", baseImage)
			}
			args := []string{rt, "pull", "--platform", "linux/" + arch, baseImage}
			if _, err := runCmd(ctx, "", args, quiet); err != nil {
				return fmt.Errorf("pulling base image: %w", err)
			}
			if !quiet {
				if v := getImageVersionLabel(ctx, rt, baseImage); strings.HasPrefix(v, "v") {
					_, _ = fmt.Fprintf(w, "  Version: %s\n", v)
				}
			}
		} else if !quiet {
			_, _ = fmt.Fprintf(w, "- Base image %s is up to date.\n", baseImage)
		}
	}

	// Get base image digest. For locally-built images (no registry), RepoDigests
	// is empty; fall back to the image ID so the label is never stored as "".
	baseDigest, err := runCmd(ctx, "", []string{rt, "image", "inspect", "--format", "{{index .RepoDigests 0}}", baseImage}, true)
	if err != nil || baseDigest == "" {
		baseDigest, _ = runCmd(ctx, "", []string{rt, "image", "inspect", "--format", "{{.Id}}", baseImage}, true)
	}

	// Compute context SHA from both the build context and keys directory.
	contextSHA, err := contextSHAHash(buildCtxDir, keysDir)
	if err != nil {
		return fmt.Errorf("computing context SHA: %w", err)
	}

	// Inject COPY layers into the extracted Dockerfile.
	// activeKey reflects only the caches that were actually found on the host;
	// use it (not cacheSpecKey(caches)) so the label accurately represents what
	// was baked in.
	var cacheArgs []string
	var activeKey string
	cacheArgs, activeKey, err = appendCacheLayers(filepath.Join(buildCtxDir, "Dockerfile"), caches, home, mountPaths)
	if err != nil {
		return fmt.Errorf("appending cache layers: %w", err)
	}

	if !quiet {
		_, _ = fmt.Fprintf(w, "- Building container image %s from %s ...\n", imageName, baseImage)
	}
	buildCmd := []string{
		rt, "build",
		"--build-context", "md-keys=" + keysDir,
		"-f", filepath.Join(buildCtxDir, "Dockerfile"),
		"--platform", "linux/" + arch,
		"--build-arg", "BASE_IMAGE=" + baseImage,
		"--build-arg", "BASE_IMAGE_DIGEST=" + baseDigest,
		"--build-arg", "CONTEXT_SHA=" + contextSHA,
		"--build-arg", "CACHE_KEY=" + activeKey,
		"-t", imageName,
	}
	if rt == "podman" {
		// SHELL instruction is Docker-format-only; use --format docker to avoid
		// "SHELL is not supported for OCI image format" warnings.
		buildCmd = append(buildCmd, "--format", "docker")
	}
	if quiet {
		buildCmd = append(buildCmd[:2], append([]string{"-q"}, buildCmd[2:]...)...)
	}
	buildCmd = append(buildCmd, cacheArgs...)
	buildCmd = append(buildCmd, buildCtxDir)
	if _, err := runCmd(ctx, "", buildCmd, false); err != nil {
		return fmt.Errorf("building container image: %w", err)
	}
	return nil
}

// dirStats returns the number of regular files and total byte size under dir.
// Unreadable entries are silently skipped.
func dirStats(dir string) (files, n int64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				files++
				n += info.Size()
			}
		}
		return nil
	})
	return files, n
}

// FormatBytes formats n bytes as a human-readable string (e.g. "1.2 GB").
func FormatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// formatCount formats n with comma thousands separators (e.g. 1234567 → "1,234,567").
func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := len(s) % 3
	var b strings.Builder
	b.Grow(len(s) + len(s)/3)
	if start > 0 {
		b.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// resolveHostPath expands a leading "~/" to home; absolute paths are returned
// unchanged.
func resolveHostPath(p, home string) string {
	if strings.HasPrefix(p, "~/") {
		return path.Join(home, p[2:])
	}
	return p
}

// printCacheInfo walks each cache directory in parallel and writes one summary
// line per cache to w. "~/" in HostPath is resolved using home. Caches whose
// host path does not exist are reported as "not found".
func printCacheInfo(w io.Writer, caches []CacheMount, home string) {
	stats := make([]cacheInfoStat, len(caches))
	var wg sync.WaitGroup
	for i, c := range caches {
		wg.Add(1)
		go func(i int, c CacheMount) {
			defer wg.Done()
			hostPath := resolveHostPath(c.HostPath, home)
			stats[i].name = c.Name
			stats[i].hostPath = hostPath
			if _, err := os.Stat(hostPath); err != nil {
				stats[i].missing = true
				return
			}
			stats[i].files, stats[i].bytes = dirStats(hostPath)
		}(i, c)
	}
	wg.Wait()
	for _, s := range stats {
		if s.missing {
			_, _ = fmt.Fprintf(w, "- Cache %s: %s not found, skipping\n", s.name, s.hostPath)
			continue
		}
		_, _ = fmt.Fprintf(w, "- Cache %s (%s): %s files, %s\n",
			s.name, s.hostPath, formatCount(s.files), FormatBytes(s.bytes))
	}
}

// launchContainer starts the Docker container, queries mapped ports, writes
// SSH config, and sets up host-side git remotes. It does NOT wait for SSH.
// Port and creation-time results are stored directly on c (launchSSHPort,
// launchVNCPort, CreatedAt) so that connectContainer can complete startup.
func launchContainer(ctx context.Context, c *Container, opts *StartOpts, imageName string) error {
	rt := c.Runtime
	var dockerArgs []string
	dockerArgs = append(dockerArgs, rt, "run", "-d",
		"--name", c.Name, "--hostname", c.Name,
		"-p", "127.0.0.1::22")

	if opts.Display {
		dockerArgs = append(dockerArgs, "-p", "127.0.0.1::5901", "-e", "MD_DISPLAY=1")
	}

	if kvmAvailable() {
		dockerArgs = append(dockerArgs, "--device=/dev/kvm")
	}
	// Localtime.
	if runtime.GOOS == "linux" {
		dockerArgs = append(dockerArgs, "-v", "/etc/localtime:/etc/localtime:ro")
	}
	// Sandbox capabilities.
	dockerArgs = append(dockerArgs,
		"--cap-add=SYS_PTRACE",
		"--security-opt", "seccomp=unconfined")
	// AppArmor is Docker/Linux-specific; podman uses SELinux and does not
	// support the apparmor security option — passing it can hang on kernel
	// security filesystem access inside a container.
	if rt != "podman" {
		dockerArgs = append(dockerArgs, "--security-opt", "apparmor=unconfined")
	}

	// Rootless podman: --userns=keep-id maps host UID to same UID inside the
	// container so bind-mounted configs are writable. --user 0:0 keeps
	// start.sh running as root for privileged setup (groupmod, sshd, dbus).
	// Rootless Docker is handled inside start.sh via /proc/self/uid_map
	// detection since Docker lacks --userns=keep-id.
	if isRootlessPodman(rt) {
		dockerArgs = append(dockerArgs, "--userns=keep-id", "--user", "0:0")
	}

	// Tailscale.
	if opts.Tailscale {
		dockerArgs = append(dockerArgs,
			"--cap-add=NET_ADMIN", "--cap-add=NET_RAW", "--cap-add=MKNOD",
			"-e", "MD_TAILSCALE=1")
		if opts.TailscaleAuthKey != "" {
			dockerArgs = append(dockerArgs, "-e", "TAILSCALE_AUTHKEY="+opts.TailscaleAuthKey)
		}
		if c.tailscaleEphemeral {
			dockerArgs = append(dockerArgs, "-e", "MD_TAILSCALE_EPHEMERAL=1")
		}
	}

	// USB passthrough (Linux only; Docker Desktop on macOS/Windows runs in a
	// VM that cannot access host USB devices). Use a bind mount + cgroup
	// rule so that devices plugged in after container start are visible.
	if opts.USB {
		if runtime.GOOS != "linux" {
			return fmt.Errorf("--usb requires Linux; Docker Desktop on %s cannot pass through host USB devices", runtime.GOOS)
		}
		dockerArgs = append(dockerArgs,
			"-v", "/dev/bus/usb:/dev/bus/usb",
			"--device-cgroup-rule=c 189:* rwm")
	}

	// Agent config mounts: always-mounted paths plus caller-specified harness paths.
	combined := mergePaths(opts.AgentPaths)
	home := c.Home
	xdgConfig := c.XDGConfigHome
	xdgData := c.XDGDataHome
	xdgState := c.XDGStateHome
	for _, p := range combined.HomePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(home, p)+":/home/user/"+p)
	}
	for _, p := range combined.XDGConfigPaths {
		ro := ""
		if p == "md" {
			ro = ":ro"
		}
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgConfig, p)+":/home/user/.config/"+p+ro)
	}
	for _, p := range combined.LocalSharePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgData, p)+":/home/user/.local/share/"+p)
	}
	for _, p := range combined.LocalStatePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgState, p)+":/home/user/.local/state/"+p)
	}

	// Set md metadata labels.
	if reposJSON, err := json.Marshal(c.Repos); err == nil {
		// Base64-encode so commas in JSON don't corrupt the comma-separated
		// label parsing in unmarshalContainer.
		dockerArgs = append(dockerArgs, "--label", "md.repos="+base64.StdEncoding.EncodeToString(reposJSON))
	}
	if opts.Display {
		dockerArgs = append(dockerArgs, "--label", "md.display=1")
	}
	if opts.Tailscale {
		dockerArgs = append(dockerArgs, "--label", "md.tailscale=1")
		if c.tailscaleEphemeral {
			dockerArgs = append(dockerArgs, "--label", "md.tailscale_ephemeral=1")
		}
	}
	if opts.USB {
		dockerArgs = append(dockerArgs, "--label", "md.usb=1")
	}
	for _, l := range opts.Labels {
		dockerArgs = append(dockerArgs, "--label", l)
	}
	dockerArgs = append(dockerArgs, imageName)

	if opts.Quiet {
		if _, err := runCmd(ctx, "", dockerArgs, true); err != nil {
			return fmt.Errorf("starting container: %w", err)
		}
	} else {
		_, _ = fmt.Fprintf(c.W, "- Starting container %s ... ", c.Name)
		if _, err := runCmd(ctx, "", dockerArgs, false); err != nil {
			_, _ = fmt.Fprintln(c.W)
			return fmt.Errorf("starting container: %w", err)
		}
	}

	// Get SSH port and creation time.
	port, err := getHostPort(ctx, rt, c.Name, "22/tcp")
	if err != nil {
		return fmt.Errorf("getting SSH port: %w", err)
	}
	c.SSHPort = port
	if !opts.Quiet {
		_, _ = fmt.Fprintf(c.W, "- Found ssh port %d\n", port)
	}
	createdStr, err := runCmd(ctx, "", []string{rt, "inspect", "--format", "{{.Created}}", c.Name}, true)
	if err != nil {
		return fmt.Errorf("getting container creation time: %w", err)
	}
	created, err := parseCreatedAt(createdStr)
	if err != nil {
		return fmt.Errorf("parsing container creation time %q: %w", createdStr, err)
	}
	c.CreatedAt = created

	// Get VNC port if display enabled.
	if opts.Display {
		vncPort, _ := getHostPort(ctx, rt, c.Name, "5901/tcp")
		c.VNCPort = vncPort
		if vncPort != 0 && !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Found VNC port %d (display :1)\n", vncPort)
		}
	}

	// Write SSH config.
	sshConfigDir := filepath.Join(home, ".ssh", "config.d")
	if err := os.MkdirAll(sshConfigDir, 0o700); err != nil {
		return err
	}
	knownHostsPath := filepath.Join(sshConfigDir, c.Name+".known_hosts")
	hostPubKey, err := os.ReadFile(c.HostKeyPath + ".pub")
	if err != nil {
		return fmt.Errorf("reading host public key: %w", err)
	}
	if err := writeSSHConfig(sshConfigDir, c.Name, port, c.UserKeyPath, knownHostsPath, c.ControlMaster); err != nil {
		return err
	}
	if err := writeKnownHosts(knownHostsPath, port, strings.TrimSpace(string(hostPubKey))); err != nil {
		return err
	}

	// Set up git remotes for all repos before waiting for SSH, so they are
	// ready to push as soon as the connection is established.
	if len(c.Repos) > 0 {
		if !opts.Quiet {
			_, _ = fmt.Fprintln(c.W, "- git clone into container ...")
		}
		for _, r := range c.Repos {
			rName := r.Name()
			_, _ = runCmd(ctx, r.GitRoot, []string{"git", "remote", "rm", c.Name}, true)
			if _, err := runCmd(ctx, r.GitRoot, []string{"git", "remote", "add", c.Name, "user@" + c.Name + ":/home/user/src/" + rName}, false); err != nil {
				return fmt.Errorf("adding git remote for %s: %w", rName, err)
			}
		}
	}
	return nil
}

// connectContainer waits for SSH, pushes repos into the container, and
// handles .env and Tailscale auth. Must be called after launchContainer.
// The task branch and default branch are pushed in parallel to reduce latency.
// waitForTCP polls until a TCP connection to addr succeeds or the deadline is
// exceeded. ECONNREFUSED returns immediately from the kernel so no sleep is
// needed — this detects readiness within microseconds of the service binding.
func waitForTCP(ctx context.Context, addr string, deadline time.Time) error {
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for TCP %s", addr)
		}
	}
}

func connectContainer(ctx context.Context, c *Container, opts *StartOpts) (*StartResult, error) {
	result := &StartResult{}

	// Phase 1: wait for TCP port to accept connections.
	addr := fmt.Sprintf("localhost:%d", c.SSHPort)
	deadline := time.Now().Add(30 * time.Second)
	if err := waitForTCP(ctx, addr, deadline); err != nil {
		return nil, err
	}

	// Send .env into the container via ssh+stdin — this is the first SSH
	// operation and doubles as the handshake readiness check. Using ssh
	// instead of scp gives reliable exit code 255 on connection errors.
	// If no .env exists locally the container still gets an empty file.
	var envContent []byte
	if data, err := os.ReadFile(".env"); err == nil {
		envContent = data
		if !opts.Quiet {
			_, _ = fmt.Fprintln(c.W, "- sending .env into container ...")
		}
	}
	if len(opts.ExtraEnv) > 0 {
		if len(envContent) > 0 && envContent[len(envContent)-1] != '\n' {
			envContent = append(envContent, '\n')
		}
		for _, kv := range opts.ExtraEnv {
			envContent = append(envContent, []byte(kv+"\n")...)
		}
		if !opts.Quiet {
			_, _ = fmt.Fprintln(c.W, "- injecting extra env vars into container ...")
		}
	}
	sshEnvArgs := c.SSHCommand(c.Name, "cat > /home/user/.env")
	for {
		cmd := exec.CommandContext(ctx, sshEnvArgs[0], sshEnvArgs[1:]...)
		cmd.Stdin = bytes.NewReader(envContent)
		out, err := cmd.CombinedOutput()
		if err == nil {
			break
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 255 || time.Now().After(deadline) {
			return nil, fmt.Errorf("copying .env: %w\n%s", err, out)
		}
	}

	// Push all repos into the container in parallel. Each repo pushes to a
	// distinct path (~/src/<name>) so there are no cross-repo conflicts.
	if len(c.Repos) > 0 {
		eg, egCtx := errgroup.WithContext(ctx)
		for repoIdx := range c.Repos {
			eg.Go(func() error {
				rName := c.Repos[repoIdx].Name()
				rRepo := shellQuote(rName)
				rBranch := shellQuote(c.Repos[repoIdx].Branch)

				if _, err := runCmd(egCtx, "", c.SSHCommand(c.Name, "git init -q ~/src/"+rRepo), false); err != nil {
					return fmt.Errorf("init repo %s in container: %w", rName, err)
				}

				// Push task branch and sync default branch in parallel — different refs.
				inner, innerCtx := errgroup.WithContext(egCtx)
				inner.Go(func() error {
					if _, err := runCmd(innerCtx, c.Repos[repoIdx].GitRoot, []string{
						"git", "push", "-q", c.Name,
						c.Repos[repoIdx].Branch + ":refs/heads/base",
					}, false); err != nil {
						return fmt.Errorf("push repo %s: %w", rName, err)
					}
					_, err := runCmd(innerCtx, "", c.SSHCommand(c.Name,
						"cd ~/src/"+rRepo+
							" && git branch "+rBranch+" base"+
							" && git switch -q "+rBranch), false)
					return err
				})
				inner.Go(func() error {
					if err := c.Repos[repoIdx].resolveDefaults(innerCtx); err != nil {
						return fmt.Errorf("resolve defaults for %s: %w", rName, err)
					}
					return c.SyncDefaultBranch(innerCtx, repoIdx)
				})
				if err := inner.Wait(); err != nil {
					return err
				}

				if err := c.pushSubmodules(egCtx, "/home/user/src/"+rName, c.Repos[repoIdx].GitRoot, opts.Quiet); err != nil {
					return fmt.Errorf("push submodules for %s: %w", rName, err)
				}

				// resolveDefaults ran above, so DefaultRemote is set.
				originURL, err := runCmd(egCtx, c.Repos[repoIdx].GitRoot, []string{"git", "remote", "get-url", c.Repos[repoIdx].DefaultRemote}, true)
				if err == nil && originURL != "" {
					httpsURL := convertGitURLToHTTPS(originURL)
					_, _ = runCmd(egCtx, "", c.SSHCommand(c.Name, "cd ~/src/"+rRepo+" && git remote add origin "+shellQuote(httpsURL)), true)
					if !opts.Quiet {
						_, _ = fmt.Fprintf(c.W, "- Set %s origin to %s\n", rName, httpsURL)
					}
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
	}

	// Wait for Tailscale auth URL if needed.
	if opts.Tailscale && opts.TailscaleAuthKey == "" {
		tailArgs := c.SSHCommand(c.Name, "tail -f /tmp/tailscale_auth_url")
		cmd := exec.CommandContext(ctx, tailArgs[0], tailArgs[1:]...)
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			if err := cmd.Start(); err == nil {
				buf := make([]byte, 4096)
				n, _ := stdout.Read(buf)
				line := string(buf[:n])
				if strings.Contains(line, "https://") {
					result.TailscaleAuthURL = strings.TrimSpace(line)
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}

	return result, nil
}

// convertGitURLToHTTPS converts a git URL to HTTPS format.
//
// Supports git@host:path, ssh://git@host/path, git://host/path, and
// https:// (returned unchanged).
func convertGitURLToHTTPS(url string) string {
	url = strings.TrimSpace(url)
	if strings.HasPrefix(url, "https://") {
		return url
	}
	// Matches git@host:user/repo.git
	if m := reGitAt.FindStringSubmatch(url); m != nil {
		return fmt.Sprintf("https://%s/%s", m[1], m[2])
	}
	// Matches ssh://git@host/user/repo.git
	if m := reSSHGit.FindStringSubmatch(url); m != nil {
		return fmt.Sprintf("https://%s/%s", m[1], m[2])
	}
	// Matches git://host/user/repo.git
	if m := reGitProto.FindStringSubmatch(url); m != nil {
		return fmt.Sprintf("https://%s/%s", m[1], m[2])
	}
	return url
}
