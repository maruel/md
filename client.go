// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// Package md manages isolated Docker development containers for AI coding
// agents.
//
// It provides programmatic access to create, manage, and tear down containers
// with SSH access. Containers optionally receive a full git clone of one or
// more repositories; repo-less containers are also supported for general
// agent workloads.
package md

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client holds global MD tool state (paths, image config, SSH keys).
type Client struct {
	// W is the writer for progress and status messages.
	W io.Writer

	// Paths.
	Home          string
	XDGConfigHome string
	XDGDataHome   string
	XDGStateHome  string

	// SSH key paths.
	HostKeyPath string // ~/.config/md/ssh_host_ed25519_key (generated)
	UserKeyPath string // ~/.ssh/md

	// Container runtime.
	Runtime string // "docker" or "podman"; auto-detected by New().

	// ControlMaster enables SSH ControlMaster connection multiplexing.
	// When true, SSH connections are shared via a persistent socket,
	// reducing connection overhead. Disabled by default because stale
	// sockets can cause connectivity issues that are hard to diagnose.
	ControlMaster bool

	// Tokens.
	GithubToken string // GitHub API token for Docker build secrets.
	// TailscaleAPIKey is the Tailscale API key for auth key generation and device deletion.
	//
	// It is necessary to setup ephemeral nodes. The key must be rotated every 90 days.
	//
	// See https://tailscale.com/docs/reference/tailscale-api and
	// https://tailscale.com/docs/features/ephemeral-nodes
	TailscaleAPIKey string

	// keysDir is the directory containing SSH host keys and authorized_keys
	// (~/.config/md/), used as a named Docker build context.
	keysDir string
	// sshArgs is the base SSH command, set by New(). It includes
	// "-o Include=~/.ssh/config.d/*.conf" when the user's ~/.ssh/config
	// lacks the Include directive.
	sshArgs []string

	// DigestCacheTTL controls how long remote image digest lookups are cached.
	// When zero, caching is disabled and the registry is queried on every start.
	DigestCacheTTL time.Duration

	// buildMu serializes image build operations (BuildImage, Warmup, and the
	// build step inside Launch) so concurrent callers don't race on the same
	// image tag.
	buildMu sync.Mutex

	// mu protects digestCache and imageBuildCache.
	mu sync.Mutex
	// digestCache caches remote image digest queries to avoid repeated
	// registry network round-trips. Entries expire after DigestCacheTTL.
	digestCache map[string]remoteDigestEntry
	// imageBuildCache stores the last imageBuildNeeded result so that
	// back-to-back checks (e.g. Warmup then Launch) skip redundant
	// docker inspect calls. Protected by mu; invalidated on successful build.
	imageBuildCache *imageBuildCacheEntry
}

// New creates a Client with global MD tool config and initialises SSH
// infrastructure (keys, authorized_keys, config.d include).
func New() (*Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	xdgConfigHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	c := &Client{
		W:              os.Stdout,
		Home:           home,
		XDGConfigHome:  xdgConfigHome,
		XDGDataHome:    envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")),
		XDGStateHome:   envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state")),
		HostKeyPath:    filepath.Join(xdgConfigHome, "md", "ssh_host_ed25519_key"),
		UserKeyPath:    filepath.Join(home, ".ssh", "md"),
		Runtime:        detectRuntime(),
		DigestCacheTTL: 12 * time.Hour,
		digestCache:    make(map[string]remoteDigestEntry),
	}
	c.keysDir = filepath.Join(c.XDGConfigHome, "md")
	if err := c.setupSSH(); err != nil {
		return nil, err
	}
	return c, nil
}

// setupSSH ensures SSH keys, authorized_keys, and ~/.ssh/config.d exist.
// Called once by New(); idempotent.
func (c *Client) setupSSH() error {
	for _, d := range []string{
		filepath.Dir(c.HostKeyPath), // ~/.config/md/
		filepath.Join(c.Home, ".ssh", "config.d"),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	sshDir := filepath.Join(c.Home, ".ssh")
	missing, err := ensureSSHConfigInclude(c.W, sshDir)
	if err != nil {
		return err
	}
	c.sshArgs = []string{"ssh"}
	if missing {
		c.sshArgs = append(c.sshArgs, "-o", "Include="+filepath.Join(sshDir, "config.d", "*.conf"))
	}
	if err := ensureEd25519Key(c.W, c.UserKeyPath, "md-user"); err != nil {
		return err
	}
	if err := ensureEd25519Key(c.W, c.HostKeyPath, "md-host"); err != nil {
		return err
	}
	pubKey, err := os.ReadFile(c.UserKeyPath + ".pub")
	if err != nil {
		return err
	}
	authKeysPath := filepath.Join(c.keysDir, "authorized_keys")
	if existing, _ := os.ReadFile(authKeysPath); bytes.Equal(existing, pubKey) {
		return nil
	}
	return os.WriteFile(authKeysPath, pubKey, 0o600) //nolint:gosec // path is constructed from trusted config dir
}

// detectRuntime returns the container runtime to use.
// Checks for docker, then podman in PATH.
func detectRuntime() string {
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	return "docker"
}

// Container returns a Container handle for the given repos.
// The first repo is the primary; the rest are pushed alongside it at
// /home/user/src/<basename> inside the container. When called with no repos,
// the container has no associated git repository and a name is generated
// automatically.
//
// It doesn't start it, it is just a reference.
func (c *Client) Container(repos ...Repo) *Container {
	if len(repos) == 0 {
		var buf [4]byte
		_, _ = rand.Read(buf[:])
		return &Container{
			Client: c,
			W:      c.W,
			Name:   fmt.Sprintf("md-agent-%x", buf),
		}
	}
	primary := repos[0]
	repoName := strings.TrimSuffix(filepath.Base(primary.GitRoot), ".git")
	return &Container{
		Client: c,
		W:      c.W,
		Repos:  repos,
		Name:   containerName(repoName, primary.Branch),
	}
}

// SSHCommand returns the base SSH command args. Extra arguments (flags,
// hostname, command) should be appended by the caller. The returned slice is a
// fresh copy safe to modify.
func (c *Client) SSHCommand(extraArgs ...string) []string {
	args := make([]string, len(c.sshArgs), len(c.sshArgs)+len(extraArgs))
	copy(args, c.sshArgs)
	return append(args, extraArgs...)
}

// SCPCommand returns the base SCP command args with the same Include
// workaround as [SSHCommand]. Extra arguments should be appended by the caller.
func (c *Client) SCPCommand(extraArgs ...string) []string {
	// sshArgs[0] is "ssh"; skip it, copy only the -o flags.
	args := make([]string, 1, 1+len(c.sshArgs)-1+len(extraArgs))
	args[0] = "scp"
	args = append(args, c.sshArgs[1:]...)
	return append(args, extraArgs...)
}

// List returns running md containers sorted by name.
func (c *Client) List(ctx context.Context) ([]*Container, error) {
	out, err := runCmd(ctx, "", []string{c.Runtime, "ps", "--all", "--no-trunc", "--format", "{{json .}}"}, true)
	if err != nil {
		return nil, err
	}
	var containers []*Container
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		ct, err := unmarshalContainer([]byte(line))
		if err != nil {
			continue
		}
		if strings.HasPrefix(ct.Name, "md-") {
			ct.Client = c
			ct.W = c.W
			containers = append(containers, &ct)
		}
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	return containers, nil
}

// BuildImage builds the base Docker image locally.
func (c *Client) BuildImage(ctx context.Context, serialSetup bool) (retErr error) {
	c.buildMu.Lock()
	defer c.buildMu.Unlock()
	arch := runtime.GOARCH
	_, _ = fmt.Fprintln(c.W, "- Building base Docker image from rsc/Dockerfile.base ...")

	// Extract the embedded rsc/ to a temp dir for building.
	buildCtx, err := prepareBuildContext()
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()

	cmd := []string{
		c.Runtime, "build",
		"--platform", "linux/" + arch,
		"-f", filepath.Join(buildCtx, "Dockerfile.base"),
		"-t", "md-local",
	}
	if serialSetup {
		cmd = append(cmd, "--build-arg", "MD_SERIAL_SETUP=1")
	}
	if c.GithubToken != "" {
		cmd = append(cmd, "--secret", "id=github_token,env=GITHUB_TOKEN")
	} else {
		_, _ = fmt.Fprintln(c.W, "WARNING: GITHUB_TOKEN not found. Some tools (neovim, rust-analyzer, etc) might fail to install or hit rate limits.")
		_, _ = fmt.Fprintln(c.W, "Please set GITHUB_TOKEN to avoid issues:")
		_, _ = fmt.Fprintln(c.W, "  https://github.com/settings/personal-access-tokens/new?name=md-build-image&description=Token%20to%20help%20generating%20local%20docker%20images%20for%20https://github.com/caic-xyz/md")
		_, _ = fmt.Fprintln(c.W, "  export GITHUB_TOKEN=...")
	}
	cmd = append(cmd, buildCtx)
	if _, err := runCmd(ctx, "", cmd, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.W, "- Base image built as 'md-local'.")
	c.invalidateImageBuildCache()
	// Clean up BuildKit cache (--mount=type=cache volumes from Dockerfile.base).
	// These are only useful during the build itself; pruning avoids leaving
	// orphaned resources on disk.
	if _, err := runCmd(ctx, "", []string{c.Runtime, "builder", "prune", "-f"}, true); err != nil {
		_, _ = fmt.Fprintf(c.W, "- Warning: pruning build cache: %v\n", err)
	}
	return nil
}

// WarmupOpts configures base image warmup.
type WarmupOpts struct {
	// BaseImage is the full Docker image reference. When empty,
	// DefaultBaseImage+":latest" is used.
	BaseImage string
	// Caches lists host directories to COPY into the image at build time.
	Caches []CacheMount
	// Quiet suppresses informational output.
	Quiet bool
}

// Warmup ensures the base image is pulled and the user image is built,
// without starting a container. Returns true if a build was performed.
func (c *Client) Warmup(ctx context.Context, opts *WarmupOpts) (bool, error) {
	c.buildMu.Lock()
	defer c.buildMu.Unlock()
	baseImage := opts.BaseImage
	if baseImage == "" {
		baseImage = DefaultBaseImage + ":latest"
	}
	imageName := userImageName(baseImage, activeCacheKey(opts.Caches, c.Home))
	if !c.imageBuildNeeded(ctx, c.Runtime, imageName, baseImage, c.keysDir, c.Home, opts.Caches) {
		if !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Docker image %s is up to date, skipping build.\n", imageName)
		}
		return false, nil
	}
	if !opts.Quiet && len(opts.Caches) > 0 {
		printCacheInfo(c.W, opts.Caches, c.Home)
	}
	buildCtx, err := prepareBuildContext()
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(buildCtx) }()
	if err := buildCustomizedImage(ctx, c.Runtime, c.W, buildCtx, c.keysDir, imageName, baseImage, c.Home, opts.Caches, agentContainerPaths(), opts.Quiet); err != nil {
		return false, err
	}
	c.invalidateImageBuildCache()
	if _, err := runCmd(ctx, "", []string{c.Runtime, "builder", "prune", "-f"}, true); err != nil {
		_, _ = fmt.Fprintf(c.W, "- Warning: pruning build cache: %v\n", err)
	}
	return true, nil
}

// PruneImages removes md-user-* images that are not used by any container.
// Returns the list of removed image names.
func (c *Client) PruneImages(ctx context.Context) ([]string, error) {
	// List all md-user-* images.
	out, err := runCmd(ctx, "", []string{
		c.Runtime, "images", "--format", "{{.Repository}}", "--filter", "reference=md-user-*",
	}, true)
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	allImages := make(map[string]struct{})
	for name := range strings.SplitSeq(out, "\n") {
		if name != "" {
			allImages[name] = struct{}{}
		}
	}

	// Find images used by running md containers.
	containerOut, err := runCmd(ctx, "", []string{
		c.Runtime, "ps", "-a", "--filter", "name=^md-", "--format", "{{.Image}}",
	}, true)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	inUse := make(map[string]struct{})
	if containerOut != "" {
		for img := range strings.SplitSeq(containerOut, "\n") {
			if img != "" {
				inUse[img] = struct{}{}
			}
		}
	}

	// Remove unused images.
	var removed []string
	for img := range allImages {
		if _, used := inUse[img]; used {
			continue
		}
		if _, err := runCmd(ctx, "", []string{c.Runtime, "rmi", img}, true); err != nil {
			_, _ = fmt.Fprintf(c.W, "- Warning: failed to remove %s: %v\n", img, err)
			continue
		}
		removed = append(removed, img)
	}
	sort.Strings(removed)

	// Clean up BuildKit build cache.
	if _, err := runCmd(ctx, "", []string{c.Runtime, "builder", "prune", "-f"}, true); err != nil {
		_, _ = fmt.Fprintf(c.W, "- Warning: pruning build cache: %v\n", err)
	}
	return removed, nil
}

// gatherGitMetadata runs SSH commands to collect branch, stat, and log from
// the container. This data is always small.
func (c *Client) gatherGitMetadata(ctx context.Context, containerName, repo string) string {
	cmd := "cd ~/src/" + repo + " && echo '=== Branch ===' && git rev-parse --abbrev-ref HEAD && echo && echo '=== Files Changed ===' && git diff --stat --cached base -- . && echo && echo '=== Recent Commits ===' && git log -5 base -- ."
	out, _ := runCmd(ctx, "", c.SSHCommand(containerName, cmd), true)
	return out
}

// gatherGitDiff runs SSH to get the full patience diff from the container.
func (c *Client) gatherGitDiff(ctx context.Context, containerName, repo string) string {
	cmd := "cd ~/src/" + repo + " && git diff --patience -U10 --cached base -- ."
	out, _ := runCmd(ctx, "", c.SSHCommand(containerName, cmd), true)
	return out
}

// Harness identifies an agent harness whose config directories are mounted
// into a container.
type Harness string

// Known agent harnesses.
const (
	HarnessAmp      Harness = "amp"
	HarnessAndroid  Harness = "android"
	HarnessClaude   Harness = "claude"
	HarnessCodex    Harness = "codex"
	HarnessGemini   Harness = "gemini"
	HarnessGoose    Harness = "goose"
	HarnessKilo     Harness = "kilo"
	HarnessKimi     Harness = "kimi"
	HarnessOpencode Harness = "opencode"
	HarnessPi       Harness = "pi"
	HarnessQwen     Harness = "qwen"
)

// AgentPaths groups the relative host directory paths for one or more agent
// harnesses. Paths under HomePaths are relative to $HOME, XDGConfigPaths to
// $XDG_CONFIG_HOME (~/.config), LocalSharePaths to $XDG_DATA_HOME
// (~/.local/share), and LocalStatePaths to $XDG_STATE_HOME (~/.local/state).
type AgentPaths struct {
	HomePaths       []string
	XDGConfigPaths  []string
	LocalSharePaths []string
	LocalStatePaths []string
}

// HarnessMounts maps each known harness to its path configuration.
var HarnessMounts = map[Harness]AgentPaths{
	HarnessAmp:      {HomePaths: []string{".amp"}, XDGConfigPaths: []string{"amp"}, LocalSharePaths: []string{"amp"}},
	HarnessAndroid:  {HomePaths: []string{".android"}},
	HarnessClaude:   {HomePaths: []string{".claude"}},
	HarnessCodex:    {HomePaths: []string{".codex"}},
	HarnessGemini:   {HomePaths: []string{".gemini"}},
	HarnessGoose:    {XDGConfigPaths: []string{"goose"}, LocalSharePaths: []string{"goose"}},
	HarnessKilo:     {HomePaths: []string{".kilocode"}},
	HarnessKimi:     {HomePaths: []string{".kimi"}},
	HarnessOpencode: {XDGConfigPaths: []string{"opencode"}, LocalSharePaths: []string{"opencode"}, LocalStatePaths: []string{"opencode"}},
	HarnessPi:       {HomePaths: []string{".pi"}},
	HarnessQwen:     {HomePaths: []string{".qwen"}},
}

// CacheMount defines a host directory to bind-mount as a build cache inside
// the container. Well-known caches are defined in [WellKnownCaches]; custom
// mounts can be constructed directly.
type CacheMount struct {
	// Name is a human-readable identifier shown in progress output (e.g. "go-mod").
	Name string
	// HostPath is the absolute path on the host. In [WellKnownCaches] entries
	// "~/" is used as a placeholder; call [CachesForHome] to resolve it.
	HostPath string
	// ContainerPath is the absolute path inside the container.
	ContainerPath string
	// ReadOnly mounts the directory read-only inside the container.
	ReadOnly bool
}

// WellKnownCaches is the set of predefined build-tool caches, keyed by short
// name. Each name may expand to multiple [CacheMount]s (e.g. "cargo" covers
// both the registry index and git sources). HostPath values use "~/" as a
// placeholder; call [CachesForHome] to get fully resolved paths.
// WellKnownCaches is the set of predefined build-tool caches, keyed by short
// name. Each name may expand to multiple [CacheMount]s (e.g. "cargo" covers
// both the registry index and git sources). HostPath values use "~/" as a
// prefix that [Container.Launch] resolves to the user's home directory at
// runtime; custom absolute paths are also accepted.
var WellKnownCaches = map[string][]CacheMount{
	"bun": {
		{Name: "bun", HostPath: "~/.bun/install/cache", ContainerPath: "/home/user/.bun/install/cache"},
	},
	"cargo": {
		{Name: "cargo-registry", HostPath: "~/.cargo/registry", ContainerPath: "/home/user/.cargo/registry"},
		{Name: "cargo-git", HostPath: "~/.cargo/git", ContainerPath: "/home/user/.cargo/git"},
	},
	// "go-build": {
	// 	{Name: "go-build", HostPath: "~/.cache/go-build", ContainerPath: "/home/user/.cache/go-build"},
	// },
	"go-mod": {
		{Name: "go-mod", HostPath: "~/go/pkg/mod", ContainerPath: "/home/user/go/pkg/mod"},
	},
	"gradle": {
		{Name: "gradle-caches", HostPath: "~/.gradle/caches", ContainerPath: "/home/user/.gradle/caches"},
		{Name: "gradle-wrapper", HostPath: "~/.gradle/wrapper/dists", ContainerPath: "/home/user/.gradle/wrapper/dists"},
	},
	"maven": {
		{Name: "maven", HostPath: "~/.m2/repository", ContainerPath: "/home/user/.m2/repository"},
	},
	"npm": {
		{Name: "npm", HostPath: "~/.npm", ContainerPath: "/home/user/.npm"},
	},
	"pip": {
		{Name: "pip", HostPath: "~/.cache/pip", ContainerPath: "/home/user/.cache/pip"},
	},
	"pnpm": {
		{Name: "pnpm", HostPath: "~/.local/share/pnpm/store", ContainerPath: "/home/user/.local/share/pnpm/store"},
	},
	"uv": {
		{Name: "uv", HostPath: "~/.cache/uv", ContainerPath: "/home/user/.cache/uv"},
	},
}

//

var (
	reInvalid        = regexp.MustCompile(`[/@#:~]+`)
	reStripRemaining = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	reCollapse       = regexp.MustCompile(`[-_.]{2,}`)
	reGitAt          = regexp.MustCompile(`^git@([^:]+):(.+)$`)
	reSSHGit         = regexp.MustCompile(`^ssh://git@([^/]+)/(.+)$`)
	reGitProto       = regexp.MustCompile(`^git://([^/]+)/(.+)$`)
)

// alwaysPaths are merged into every container's mount set automatically.
// Callers do not need to include these; Client methods add them internally.
var alwaysPaths = AgentPaths{
	XDGConfigPaths: []string{"agents", "md"},
}

// mergePaths concatenates a slice of AgentPaths into one, prepending alwaysPaths.
func mergePaths(paths []AgentPaths) AgentPaths {
	result := alwaysPaths
	for _, p := range paths {
		result.HomePaths = append(result.HomePaths, p.HomePaths...)
		result.XDGConfigPaths = append(result.XDGConfigPaths, p.XDGConfigPaths...)
		result.LocalSharePaths = append(result.LocalSharePaths, p.LocalSharePaths...)
		result.LocalStatePaths = append(result.LocalStatePaths, p.LocalStatePaths...)
	}
	return result
}

// agentContainerPaths returns the container-side mount target paths for all
// agent config mounts. These are the -v targets that must be pre-created with
// user ownership in the Docker image before docker run creates them as root.
func agentContainerPaths() []string {
	all := alwaysPaths
	for _, p := range HarnessMounts {
		all.HomePaths = append(all.HomePaths, p.HomePaths...)
		all.XDGConfigPaths = append(all.XDGConfigPaths, p.XDGConfigPaths...)
		all.LocalSharePaths = append(all.LocalSharePaths, p.LocalSharePaths...)
		all.LocalStatePaths = append(all.LocalStatePaths, p.LocalStatePaths...)
	}
	paths := make([]string, 0, len(all.HomePaths)+len(all.XDGConfigPaths)+len(all.LocalSharePaths)+len(all.LocalStatePaths))
	for _, p := range all.HomePaths {
		paths = append(paths, "/home/user/"+p)
	}
	for _, p := range all.XDGConfigPaths {
		paths = append(paths, "/home/user/.config/"+p)
	}
	for _, p := range all.LocalSharePaths {
		paths = append(paths, "/home/user/.local/share/"+p)
	}
	for _, p := range all.LocalStatePaths {
		paths = append(paths, "/home/user/.local/state/"+p)
	}
	return paths
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// sanitizeDockerName sanitizes a string for use in a Docker container name.
//
// Docker container names must match [a-zA-Z0-9][a-zA-Z0-9_.-].
func sanitizeDockerName(name string) string {
	s := reInvalid.ReplaceAllString(name, "-")
	s = reStripRemaining.ReplaceAllString(s, "")
	s = reCollapse.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_.")
	if s == "" {
		return "unnamed"
	}
	return s
}

// containerName returns the container name for a repo and branch.
func containerName(repoName, branchName string) string {
	return "md-" + sanitizeDockerName(repoName) + "-" + sanitizeDockerName(branchName)
}
