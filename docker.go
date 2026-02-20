// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
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
		_, _ = io.WriteString(h, rel)
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		_, _ = h.Write(data)
	}
	return nil
}

func dockerInspectFormat(ctx context.Context, name, format string) (string, error) {
	return runCmd(ctx, "", []string{"docker", "image", "inspect", name, "--format", format}, true)
}

func getImageVersionLabel(ctx context.Context, imageName string) string {
	out, err := dockerInspectFormat(ctx, imageName, `{{index .Config.Labels "org.opencontainers.image.version"}}`)
	if err != nil || out == "" || out == "<no value>" {
		return ""
	}
	return out
}

// getRemoteConfigDigest queries the registry for the config digest of the
// given image for the specified architecture without downloading layers.
func getRemoteConfigDigest(ctx context.Context, image, arch string) (string, error) {
	out, err := runCmd(ctx, "", []string{"docker", "manifest", "inspect", "-v", image}, true)
	if err != nil {
		return "", err
	}
	type configRef struct {
		Digest string `json:"digest"`
	}
	type manifestContent struct {
		Config configRef `json:"config"`
	}
	type manifestEntry struct {
		Descriptor struct {
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"Descriptor"`
		SchemaV2Manifest *manifestContent `json:"SchemaV2Manifest,omitempty"`
		OCIManifest      *manifestContent `json:"OCIManifest,omitempty"`
	}
	// Handle both v2 and OCI manifests.
	configDigest := func(e *manifestEntry) string {
		if e.SchemaV2Manifest != nil {
			return e.SchemaV2Manifest.Config.Digest
		}
		if e.OCIManifest != nil {
			return e.OCIManifest.Config.Digest
		}
		return ""
	}
	var entries []manifestEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		var single manifestEntry
		if err2 := json.Unmarshal([]byte(out), &single); err2 != nil {
			return "", fmt.Errorf("parsing manifest inspect output: %w", err)
		}
		entries = []manifestEntry{single}
	}
	for i := range entries {
		p := entries[i].Descriptor.Platform
		if p.Architecture == arch && p.OS == "linux" {
			if d := configDigest(&entries[i]); d != "" {
				return d, nil
			}
		}
	}
	if len(entries) == 1 {
		if d := configDigest(&entries[0]); d != "" {
			return d, nil
		}
	}
	return "", fmt.Errorf("no manifest for linux/%s in %s", arch, image)
}

// buildCustomizedImage builds the per-user Docker image. keysDir is the
// directory containing SSH host keys and authorized_keys, supplied to Docker
// as a named build context "md-keys".
func buildCustomizedImage(ctx context.Context, w io.Writer, buildCtxDir, keysDir, imageName, baseImage string, quiet bool) error {
	arch := runtime.GOARCH
	// Local-only images (no "/" or ":" in name) are never pulled from a registry.
	isLocal := !strings.Contains(baseImage, "/") && !strings.Contains(baseImage, ":")
	if isLocal {
		if _, err := runCmd(ctx, "", []string{"docker", "image", "inspect", "--format", "{{.Id}}", baseImage}, true); err != nil {
			return fmt.Errorf("local image %s not found; build it first with 'md build-image'", baseImage)
		}
		if !quiet {
			_, _ = fmt.Fprintf(w, "- Using local base image %s.\n", baseImage)
		}
	} else {
		// Check if local image is already up to date with remote.
		needsPull := true
		if localID, err := runCmd(ctx, "", []string{"docker", "image", "inspect", "--format", "{{.Id}}", baseImage}, true); err == nil && localID != "" {
			if remoteDigest, err := getRemoteConfigDigest(ctx, baseImage, arch); err == nil && remoteDigest == localID {
				needsPull = false
			}
		}
		if needsPull {
			if !quiet {
				_, _ = fmt.Fprintf(w, "- Pulling base image %s ...\n", baseImage)
			}
			args := []string{"docker", "pull", "--platform", "linux/" + arch, baseImage}
			if _, err := runCmd(ctx, "", args, quiet); err != nil {
				return fmt.Errorf("pulling base image: %w", err)
			}
			if !quiet {
				if v := getImageVersionLabel(ctx, baseImage); strings.HasPrefix(v, "v") {
					_, _ = fmt.Fprintf(w, "  Version: %s\n", v)
				}
			}
		} else if !quiet {
			_, _ = fmt.Fprintf(w, "- Base image %s is up to date.\n", baseImage)
		}
	}

	// Get base image digest.
	baseDigest, err := runCmd(ctx, "", []string{"docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", baseImage}, true)
	if err != nil {
		baseDigest, _ = runCmd(ctx, "", []string{"docker", "image", "inspect", "--format", "{{.Id}}", baseImage}, true)
	}

	// Compute context SHA from both the build context and keys directory.
	contextSHA, err := contextSHAHash(buildCtxDir, keysDir)
	if err != nil {
		return fmt.Errorf("computing context SHA: %w", err)
	}

	// Check if current image already matches.
	currentDigest, err := runCmd(ctx, "", []string{"docker", "image", "inspect", imageName, "--format", `{{index .Config.Labels "md.base_digest"}}`}, true)
	if err == nil && currentDigest != "<no value>" {
		currentContext, _ := runCmd(ctx, "", []string{"docker", "image", "inspect", imageName, "--format", `{{index .Config.Labels "md.context_sha"}}`}, true)
		if currentDigest == baseDigest && currentContext == contextSHA {
			if !quiet {
				_, _ = fmt.Fprintf(w, "- Docker image %s already matches %s (%s), skipping rebuild.\n", imageName, baseImage, baseDigest)
			}
			return nil
		}
	}

	if !quiet {
		_, _ = fmt.Fprintf(w, "- Building Docker image %s from %s ...\n", imageName, baseImage)
	}
	buildCmd := []string{
		"docker", "build",
		"--build-context", "md-keys=" + keysDir,
		"--platform", "linux/" + arch,
		"--build-arg", "BASE_IMAGE=" + baseImage,
		"--build-arg", "BASE_IMAGE_DIGEST=" + baseDigest,
		"--build-arg", "CONTEXT_SHA=" + contextSHA,
		"-t", imageName,
	}
	if quiet {
		buildCmd = append(buildCmd[:2], append([]string{"-q"}, buildCmd[2:]...)...)
	}
	buildCmd = append(buildCmd, buildCtxDir)
	if _, err := runCmd(ctx, "", buildCmd, false); err != nil {
		return fmt.Errorf("building Docker image: %w", err)
	}
	return nil
}

// runContainer starts the Docker container and sets up SSH access.
// tailscaleEphemeral indicates the Tailscale node was auto-keyed and should be
// treated as ephemeral (cleaned up on kill without API deletion).
func runContainer(ctx context.Context, c *Container, opts *StartOpts, tailscaleEphemeral bool) (*StartResult, error) {
	var dockerArgs []string
	dockerArgs = append(dockerArgs, "docker", "run", "-d",
		"--name", c.Name, "--hostname", c.Name,
		"-p", "127.0.0.1:0:22")

	if opts.Display {
		dockerArgs = append(dockerArgs, "-p", "127.0.0.1:0:5901", "-e", "MD_DISPLAY=1")
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
		"--security-opt", "seccomp=unconfined",
		"--security-opt", "apparmor=unconfined")

	// Tailscale.
	if opts.Tailscale {
		dockerArgs = append(dockerArgs,
			"--cap-add=NET_ADMIN", "--cap-add=NET_RAW", "--cap-add=MKNOD",
			"-e", "MD_TAILSCALE=1")
		if opts.TailscaleAuthKey != "" {
			dockerArgs = append(dockerArgs, "-e", "TAILSCALE_AUTHKEY="+opts.TailscaleAuthKey)
		}
		if tailscaleEphemeral {
			dockerArgs = append(dockerArgs, "-e", "MD_TAILSCALE_EPHEMERAL=1")
		}
	}

	// USB passthrough (Linux only; Docker Desktop on macOS/Windows runs in a
	// VM that cannot access host USB devices). Use a bind mount + cgroup
	// rule so that devices plugged in after container start are visible.
	if opts.USB {
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("--usb requires Linux; Docker Desktop on %s cannot pass through host USB devices", runtime.GOOS)
		}
		dockerArgs = append(dockerArgs,
			"-v", "/dev/bus/usb:/dev/bus/usb",
			"--device-cgroup-rule=c 189:* rwm")
	}

	// Agent config mounts.
	home := c.Home
	xdgConfig := c.XDGConfigHome
	xdgData := c.XDGDataHome
	xdgState := c.XDGStateHome
	for _, p := range agentConfig.HomePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(home, p)+":/home/user/"+p)
	}
	for _, p := range agentConfig.XDGConfigPaths {
		ro := ""
		if p == "md" {
			ro = ":ro"
		}
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgConfig, p)+":/home/user/.config/"+p+ro)
	}
	for _, p := range agentConfig.LocalSharePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgData, p)+":/home/user/.local/share/"+p)
	}
	for _, p := range agentConfig.LocalStatePaths {
		dockerArgs = append(dockerArgs, "-v", filepath.Join(xdgState, p)+":/home/user/.local/state/"+p)
	}

	// Set md metadata labels.
	// TODO: This will need a bit more work when we support multiple repos.
	dockerArgs = append(dockerArgs,
		"--label", "md.git_root="+c.GitRoot,
		"--label", "md.repo_name="+c.RepoName,
		"--label", "md.branch="+c.Branch)
	if opts.Display {
		dockerArgs = append(dockerArgs, "--label", "md.display=1")
	}
	if opts.Tailscale {
		dockerArgs = append(dockerArgs, "--label", "md.tailscale=1")
		if tailscaleEphemeral {
			dockerArgs = append(dockerArgs, "--label", "md.tailscale_ephemeral=1")
		}
	}
	if opts.USB {
		dockerArgs = append(dockerArgs, "--label", "md.usb=1")
	}
	for _, l := range opts.Labels {
		dockerArgs = append(dockerArgs, "--label", l)
	}
	dockerArgs = append(dockerArgs, c.ImageName)

	if opts.Quiet {
		if _, err := runCmd(ctx, "", dockerArgs, true); err != nil {
			return nil, fmt.Errorf("starting container: %w", err)
		}
	} else {
		_, _ = fmt.Fprintf(c.W, "- Starting container %s ... ", c.Name)
		if _, err := runCmd(ctx, "", dockerArgs, false); err != nil {
			_, _ = fmt.Fprintln(c.W)
			return nil, fmt.Errorf("starting container: %w", err)
		}
	}

	result := &StartResult{}

	// Get SSH port and creation time.
	port, err := runCmd(ctx, "", []string{"docker", "inspect", "--format", `{{(index .NetworkSettings.Ports "22/tcp" 0).HostPort}}`, c.Name}, true)
	if err != nil {
		return nil, fmt.Errorf("getting SSH port: %w", err)
	}
	result.SSHPort = port
	if !opts.Quiet {
		_, _ = fmt.Fprintf(c.W, "- Found ssh port %s\n", port)
	}
	createdStr, err := runCmd(ctx, "", []string{"docker", "inspect", "--format", "{{.Created}}", c.Name}, true)
	if err != nil {
		return nil, fmt.Errorf("getting container creation time: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parsing container creation time %q: %w", createdStr, err)
	}
	c.CreatedAt = created

	// Get VNC port if display enabled.
	if opts.Display {
		vncPort, _ := runCmd(ctx, "", []string{"docker", "inspect", "--format", `{{(index .NetworkSettings.Ports "5901/tcp" 0).HostPort}}`, c.Name}, true)
		result.VNCPort = vncPort
		if vncPort != "" && !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Found VNC port %s (display :1)\n", vncPort)
		}
	}

	// Write SSH config.
	sshConfigDir := filepath.Join(home, ".ssh", "config.d")
	if err := os.MkdirAll(sshConfigDir, 0o700); err != nil {
		return nil, err
	}
	knownHostsPath := filepath.Join(sshConfigDir, c.Name+".known_hosts")
	hostPubKey, err := os.ReadFile(c.HostKeyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("reading host public key: %w", err)
	}
	if err := writeSSHConfig(sshConfigDir, c.Name, port, c.UserKeyPath, knownHostsPath); err != nil {
		return nil, err
	}
	if err := writeKnownHosts(knownHostsPath, port, strings.TrimSpace(string(hostPubKey))); err != nil {
		return nil, err
	}

	// Set up git remote.
	if !opts.Quiet {
		_, _ = fmt.Fprintln(c.W, "- git clone into container ...")
	}
	_, _ = runCmd(ctx, c.GitRoot, []string{"git", "remote", "rm", c.Name}, true)
	if _, err := runCmd(ctx, c.GitRoot, []string{"git", "remote", "add", c.Name, "user@" + c.Name + ":/home/user/src/" + c.RepoName}, false); err != nil {
		return nil, fmt.Errorf("adding git remote: %w", err)
	}

	// Wait for SSH.
	start := time.Now()
	var lastOutput string
	for {
		out, err := runCmd(ctx, "", []string{"ssh", "-o", "ConnectTimeout=2", c.Name, "exit"}, true)
		if err == nil {
			break
		}
		lastOutput = out
		time.Sleep(100 * time.Millisecond)
		if time.Since(start) > 10*time.Second {
			return nil, fmt.Errorf("timed out waiting for container SSH: %s", lastOutput)
		}
	}

	repo := shellQuote(c.RepoName)
	branch := shellQuote(c.Branch)

	// Initialize git repo in container.
	if _, err := runCmd(ctx, "", []string{"ssh", c.Name, "git init -q ~/src/" + repo}, false); err != nil {
		return nil, err
	}
	if _, err := runCmd(ctx, c.GitRoot, []string{"git", "push", "-q", "--tags", c.Name, c.Branch + ":refs/heads/" + c.Branch}, false); err != nil {
		return nil, err
	}
	if _, err := runCmd(ctx, "", []string{"ssh", c.Name, "cd ~/src/" + repo + " && git switch -q " + branch + " && git branch -f base " + branch + " && git switch -q base && git switch -q " + branch}, false); err != nil {
		return nil, err
	}

	// Set up origin remote in container using HTTPS.
	remote, _ := GitDefaultRemote(ctx, c.GitRoot)
	originURL, err := runCmd(ctx, c.GitRoot, []string{"git", "remote", "get-url", remote}, true)
	if err == nil && originURL != "" {
		httpsURL := convertGitURLToHTTPS(originURL)
		_, _ = runCmd(ctx, "", []string{"ssh", c.Name, "cd ~/src/" + repo + " && git remote add origin " + shellQuote(httpsURL)}, true)
		if !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Set container origin to %s\n", httpsURL)
		}
	}

	// Push the default branch (e.g. main) so agents can diff against it.
	if err := c.SyncDefaultBranch(ctx); err != nil {
		return nil, err
	}

	// Copy .env if present.
	if _, err := os.Stat(".env"); err == nil {
		if !opts.Quiet {
			_, _ = fmt.Fprintln(c.W, "- sending .env into container ...")
		}
		_, _ = runCmd(ctx, "", []string{"scp", ".env", c.Name + ":/home/user/.env"}, false)
	}

	// Wait for Tailscale auth URL if needed.
	if opts.Tailscale && opts.TailscaleAuthKey == "" {
		cmd := exec.CommandContext(ctx, "ssh", c.Name, "tail -f /tmp/tailscale_auth_url")
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
