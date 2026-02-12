// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// Package md manages isolated Docker development containers for AI coding
// agents.
//
// It provides programmatic access to create, manage, and tear down containers
// that each get a full git clone of your repository with SSH access.
package md

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

	// Docker.
	ImageName   string
	BaseImage   string
	TagExplicit bool

	// Tokens.
	GithubToken     string // GitHub API token for Docker build secrets.
	TailscaleAPIKey string // Tailscale API key for auth key generation and device deletion.

	// keysDir is the directory containing SSH host keys and authorized_keys
	// (~/.config/md/), used as a named Docker build context.
	keysDir string
}

// New creates a Client with global MD tool config.
// tag may be empty for "latest".
func New(tag string) (*Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	c := &Client{
		W:             os.Stdout,
		Home:          home,
		XDGConfigHome: envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")),
		XDGDataHome:   envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")),
		XDGStateHome:  envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state")),
		HostKeyPath:   filepath.Join(home, ".config", "md", "ssh_host_ed25519_key"),
		UserKeyPath:   filepath.Join(home, ".ssh", "md"),
		ImageName:     "md",
		TagExplicit:   tag != "",
	}
	if c.TagExplicit {
		c.BaseImage = "ghcr.io/maruel/md:" + tag
	} else {
		c.BaseImage = "ghcr.io/maruel/md:latest"
	}
	c.keysDir = filepath.Join(c.XDGConfigHome, "md")
	return c, nil
}

// Container returns a Container handle for the given git root and branch.
//
// It doesn't start it, it is just a reference.
func (c *Client) Container(gitRoot, branch string) *Container {
	repoName := filepath.Base(gitRoot)
	return &Container{
		Client:   c,
		GitRoot:  gitRoot,
		RepoName: repoName,
		Branch:   branch,
		Name:     containerName(repoName, branch),
	}
}

// Prepare ensures all directories and keys exist.
func (c *Client) Prepare() error {
	dirs := make([]string, 0, 2+len(agentConfig.HomePaths)+len(agentConfig.XDGConfigPaths)+len(agentConfig.LocalSharePaths)+len(agentConfig.LocalStatePaths))
	dirs = append(dirs,
		filepath.Dir(c.HostKeyPath),
		filepath.Join(c.Home, ".ssh", "config.d"),
	)
	for _, p := range agentConfig.HomePaths {
		dirs = append(dirs, filepath.Join(c.Home, p))
	}
	for _, p := range agentConfig.XDGConfigPaths {
		dirs = append(dirs, filepath.Join(c.XDGConfigHome, p))
	}
	for _, p := range agentConfig.LocalSharePaths {
		dirs = append(dirs, filepath.Join(c.XDGDataHome, p))
	}
	for _, p := range agentConfig.LocalStatePaths {
		dirs = append(dirs, filepath.Join(c.XDGStateHome, p))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}

	// Ensure ~/.claude.json symlink.
	claudeJSON := filepath.Join(c.Home, ".claude.json")
	target := filepath.Join(c.Home, ".claude", "claude.json")
	if fi, err := os.Lstat(claudeJSON); err != nil {
		// Doesn't exist, create symlink.
		if err := os.Symlink(target, claudeJSON); err != nil {
			return fmt.Errorf("creating claude.json symlink: %w", err)
		}
	} else if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("file %s exists but is not a symlink", claudeJSON)
	}

	if err := ensureEd25519Key(c.W, c.UserKeyPath, "md-user"); err != nil {
		return err
	}
	if err := ensureEd25519Key(c.W, c.HostKeyPath, "md-host"); err != nil {
		return err
	}

	// Write authorized_keys from user public key.
	pubKey, err := os.ReadFile(c.UserKeyPath + ".pub")
	if err != nil {
		return err
	}
	userAuthKeysPath := filepath.Join(c.keysDir, "authorized_keys")
	if err := os.WriteFile(userAuthKeysPath, pubKey, 0o600); err != nil {
		return err
	}
	return nil
}

// List returns running md containers sorted by name.
func (c *Client) List(ctx context.Context) ([]*Container, error) {
	out, err := runCmd(ctx, "", []string{"docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"}, true)
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
			containers = append(containers, &ct)
		}
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	return containers, nil
}

// BuildBase builds the base Docker image locally.
func (c *Client) BuildBase(ctx context.Context, serialSetup bool) (retErr error) {
	arch, err := hostArch()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.W, "- Building base Docker image from rsc/Dockerfile.base ...")

	// Extract the embedded rsc/ to a temp dir for building.
	buildCtx, err := prepareBuildContext()
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()

	cmd := []string{
		"docker", "build",
		"--platform", "linux/" + arch,
		"-f", filepath.Join(buildCtx, "Dockerfile.base"),
		"-t", "md-base",
	}
	if serialSetup {
		cmd = append(cmd, "--build-arg", "MD_SERIAL_SETUP=1")
	}
	if c.GithubToken != "" {
		cmd = append(cmd, "--secret", "id=github_token,env=GITHUB_TOKEN")
	} else {
		_, _ = fmt.Fprintln(c.W, "WARNING: GITHUB_TOKEN not found. Some tools (neovim, rust-analyzer, etc) might fail to install or hit rate limits.")
		_, _ = fmt.Fprintln(c.W, "Please set GITHUB_TOKEN to avoid issues:")
		_, _ = fmt.Fprintln(c.W, "  https://github.com/settings/personal-access-tokens/new?name=md-build-base&description=Token%20to%20help%20generating%20local%20docker%20images%20for%20https://github.com/maruel/md")
		_, _ = fmt.Fprintln(c.W, "  export GITHUB_TOKEN=...")
	}
	cmd = append(cmd, buildCtx)
	if _, err := runCmd(ctx, "", cmd, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.W, "- Base image built as 'md-base'.")
	return nil
}

//

// agentConfig defines the host paths that get mounted into containers.
var agentConfig = struct {
	// HomePaths are mounted as-is under /home/user/.
	HomePaths []string
	// XDGConfigPaths are mounted under /home/user/.config/.
	XDGConfigPaths []string
	// LocalSharePaths are mounted under /home/user/.local/share/.
	LocalSharePaths []string
	// LocalStatePaths are mounted under /home/user/.local/state/.
	LocalStatePaths []string
}{
	HomePaths: []string{
		".amp",
		".android",
		".codex",
		".claude",
		".gemini",
		".kimi",
		".pi",
		".qwen",
	},
	XDGConfigPaths: []string{
		"agents",
		"amp",
		"goose",
		"md",
		"opencode",
	},
	LocalSharePaths: []string{
		"amp",
		"goose",
		"opencode",
	},
	LocalStatePaths: []string{
		"opencode",
	},
}

var (
	reInvalid        = regexp.MustCompile(`[/@#:~]+`)
	reStripRemaining = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	reCollapse       = regexp.MustCompile(`[-_.]{2,}`)
	reGitAt          = regexp.MustCompile(`^git@([^:]+):(.+)$`)
	reSSHGit         = regexp.MustCompile(`^ssh://git@([^/]+)/(.+)$`)
	reGitProto       = regexp.MustCompile(`^git://([^/]+)/(.+)$`)
)

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
