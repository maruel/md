// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caic-xyz/md/gitutil"
	"github.com/maruel/genai"
	"github.com/maruel/genai/providers"
	"golang.org/x/term"
)

// runCmd executes a command and returns (stdout, error).
// If capture is true, stdout/stderr are captured; otherwise they go to os.Stdout/os.Stderr.
// If dir is non-empty, the command runs in that directory.
func runCmd(ctx context.Context, dir string, args []string, capture bool) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LANG=C")
	if capture {
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return "", cmd.Run()
}

// DefaultBaseImage is the base image used when none is specified.
const DefaultBaseImage = "ghcr.io/caic-xyz/md"

// Repo describes a git repository to push into a container.
// It is mounted at /home/user/src/<basename>.
type Repo struct {
	// GitRoot is the absolute path to the git repository root on the host.
	GitRoot string `json:"git_root"`
	// Branch is the git branch to push into the container.
	Branch string `json:"branch"`
}

// StartOpts configures container startup.
type StartOpts struct {
	// BaseImage is the full Docker image reference (e.g.
	// "ghcr.io/caic-xyz/md:v0.7.1" or "myregistry/custom:tag"). When empty,
	// DefaultBaseImage is used.
	BaseImage string
	// Display enables X11/VNC virtual display (port 5901).
	Display bool
	// Tailscale enables Tailscale networking inside the container.
	//
	// It is recommended to set Client.TailscaleAPIKey to enable ephemeral nodes. If Client.TailscaleAPIKey is
	// not set, the node will not be ephemeral. Instead, an authentication URL will be printed back by md.
	Tailscale bool
	// TailscaleAuthKey is a pre-authorized Tailscale auth key.
	//
	// When empty and Tailscale is true, Client.TailscaleAPIKey is used to generate an authentication key.
	//
	// The tailnet policy must allow "tag:md".
	//
	// https://tailscale.com/docs/features/access-control/auth-keys
	TailscaleAuthKey string
	// USB enables USB device passthrough (Linux only).
	USB bool
	// Caches lists host directories to COPY into the image at build time.
	// Use well-known names from [WellKnownCaches] or construct [CacheMount]
	// values directly. Paths that do not exist on the host are silently skipped.
	Caches []CacheMount
	// Labels are additional Docker labels (key=value) applied to the container.
	Labels []string
	// Quiet suppresses informational output during startup.
	Quiet bool
	// AgentPaths specifies which agent config directories to mount. Pass one
	// entry per harness using values from [HarnessMounts]. Always-mounted
	// directories (~/.config/agents, ~/.config/md) are added automatically.
	// Nil or empty mounts only those shared directories.
	AgentPaths []AgentPaths
}

// Container holds state for a single container instance.
//
// Fields marked with a label are persisted as Docker container labels
// and restored by [unmarshalContainer] when listing containers.
type Container struct {
	*Client
	// W is the writer for progress output. Defaults to Client.W; set directly
	// to redirect output for a specific container without affecting others.
	W io.Writer
	// Repos are the git repositories in this container. Repos[0] is the
	// primary; the rest are pushed alongside it at /home/user/src/<basename>.
	// Label: md.repos (base64-encoded JSON)
	Repos []Repo
	// Name is the Docker container name (e.g. "md-myrepo-main").
	Name string
	// State is the Docker container state (e.g. "running", "exited").
	State string
	// CreatedAt is when the container was created.
	CreatedAt time.Time
	// Display indicates the container was started with X11/VNC enabled.
	// Label: md.display
	Display bool
	// Tailscale indicates the container was started with Tailscale networking.
	// Label: md.tailscale
	Tailscale bool
	// USB indicates the container was started with USB passthrough.
	// Label: md.usb
	USB bool

	// SSHPort is the host port mapped to the container's SSH port.
	// Set by Launch; available immediately after Launch returns.
	SSHPort int32
	// VNCPort is the host port mapped to the container's VNC port, if display is enabled.
	// Set by Launch; available immediately after Launch returns. Zero if display is disabled.
	VNCPort int32

	// DefaultRemote is the host's default git remote (resolved lazily).
	DefaultRemote string
	// DefaultBranch is the default branch for DefaultRemote (resolved lazily).
	DefaultBranch string

	// tailscaleEphemeral is set by Launch and consumed by Connect.
	tailscaleEphemeral bool
}

func (c *Container) primary() Repo {
	return c.Repos[0]
}

func (c *Container) repoName() string {
	return strings.TrimSuffix(filepath.Base(c.Repos[0].GitRoot), ".git")
}

// StartResult contains Tailscale information from Connect. Port information
// is available on Container directly (SSHPort, VNCPort) after Launch returns.
type StartResult struct {
	// TailscaleFQDN is the Tailscale FQDN assigned to the container, if any.
	TailscaleFQDN string
	// TailscaleAuthURL is the Tailscale auth URL when no pre-auth key was provided.
	TailscaleAuthURL string
}

// prepare creates harness-specific config directories on the host so they can
// be bind-mounted into the container. Always-mounted directories
// (~/.config/agents, ~/.config/md) are created regardless.
func (c *Container) prepare(paths []AgentPaths) error {
	combined := mergePaths(paths)
	dirs := make([]string, 0, len(combined.HomePaths)+len(combined.XDGConfigPaths)+len(combined.LocalSharePaths)+len(combined.LocalStatePaths))
	for _, p := range combined.HomePaths {
		dirs = append(dirs, filepath.Join(c.Home, p))
	}
	for _, p := range combined.XDGConfigPaths {
		dirs = append(dirs, filepath.Join(c.XDGConfigHome, p))
	}
	for _, p := range combined.LocalSharePaths {
		dirs = append(dirs, filepath.Join(c.XDGDataHome, p))
	}
	for _, p := range combined.LocalStatePaths {
		dirs = append(dirs, filepath.Join(c.XDGStateHome, p))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	// Ensure ~/.claude.json symlink when ~/.claude is being prepared.
	for _, p := range combined.HomePaths {
		if p == ".claude" {
			claudeJSON := filepath.Join(c.Home, ".claude.json")
			target := filepath.Join(c.Home, ".claude", "claude.json")
			if fi, err := os.Lstat(claudeJSON); err != nil {
				if err := os.Symlink(target, claudeJSON); err != nil {
					return fmt.Errorf("creating claude.json symlink: %w", err)
				}
			} else if fi.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("file %s exists but is not a symlink", claudeJSON)
			}
			break
		}
	}
	return nil
}

// Launch prepares the image and starts the Docker container. It does NOT
// wait for SSH to become ready — call Connect to complete startup once the
// container's repos have their branches set (e.g. after concurrent branch
// allocation).
func (c *Container) Launch(ctx context.Context, opts *StartOpts) (retErr error) {
	if err := c.prepare(opts.AgentPaths); err != nil {
		return err
	}
	// Check if container already exists.
	if _, err := runCmd(ctx, "", []string{c.Runtime, "inspect", c.Name}, true); err == nil {
		return fmt.Errorf("container %s already exists. SSH in with 'ssh %s' or clean it up via 'md kill' first",
			c.Name, c.Name)
	}

	// Generate Tailscale auth key if needed.
	if opts.Tailscale && opts.TailscaleAuthKey == "" {
		key, err := generateTailscaleAuthKey(c.TailscaleAPIKey)
		if err != nil {
			if !opts.Quiet {
				_, _ = fmt.Fprintf(c.W, "- Could not generate Tailscale auth key (%v), will use browser auth\n", err)
			}
		} else {
			opts.TailscaleAuthKey = key
			c.tailscaleEphemeral = true
		}
	}

	baseImage := opts.BaseImage
	if baseImage == "" {
		baseImage = DefaultBaseImage + ":latest"
	}
	if !c.imageBuildNeeded(ctx, c.Runtime, c.ImageName, baseImage, c.keysDir, c.Home, opts.Caches) {
		if !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Docker image %s is up to date, skipping build.\n", c.ImageName)
		}
	} else {
		if !opts.Quiet && len(opts.Caches) > 0 {
			printCacheInfo(c.W, opts.Caches, c.Home)
		}
		buildCtx, err := prepareBuildContext()
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()
		if err := buildCustomizedImage(ctx, c.Runtime, c.W, buildCtx, c.keysDir, c.ImageName, baseImage, c.Home, opts.Caches, agentContainerPaths(), opts.Quiet); err != nil {
			return err
		}
	}
	return launchContainer(ctx, c, opts)
}

// Connect waits for SSH, pushes repos into the container, and completes
// startup. Must be called after Launch. Container.Repos must have
// branches set before this call.
func (c *Container) Connect(ctx context.Context, opts *StartOpts) (*StartResult, error) {
	result, err := connectContainer(ctx, c, opts)
	if err != nil {
		return nil, err
	}
	if opts.Tailscale {
		c.Tailscale = true
		c.State = "running"
		result.TailscaleFQDN = c.TailscaleFQDN(ctx)
	}
	return result, nil
}

// Run starts a temporary container, runs a command, then cleans up.
// baseImage is the full Docker image reference; if empty, DefaultBaseImage is
// used. caches lists host directories to COPY into the image (same semantics
// as StartOpts.Caches); nil means no caches.
func (c *Container) Run(ctx context.Context, baseImage string, command []string, caches []CacheMount) (_ int, retErr error) {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	var tmpRepos []Repo
	var tmpName string
	if len(c.Repos) > 0 {
		tmpRepos = c.Repos[:1]
		tmpName = fmt.Sprintf("md-%s-run-%x", sanitizeDockerName(c.repoName()), buf)
	} else {
		tmpName = fmt.Sprintf("md-run-%x", buf)
	}
	tmp := &Container{
		Client: c.Client,
		W:      c.W,
		Repos:  tmpRepos,
		Name:   tmpName,
	}

	if baseImage == "" {
		baseImage = DefaultBaseImage + ":latest"
	}
	if c.imageBuildNeeded(ctx, c.Runtime, c.ImageName, baseImage, c.keysDir, c.Home, caches) {
		buildCtx, err := prepareBuildContext()
		if err != nil {
			return 1, err
		}
		defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()
		if err := buildCustomizedImage(ctx, c.Runtime, c.W, buildCtx, c.keysDir, c.ImageName, baseImage, c.Home, caches, agentContainerPaths(), true); err != nil {
			return 1, err
		}
	}
	opts := StartOpts{Quiet: true}
	if err := launchContainer(ctx, tmp, &opts); err != nil {
		tmp.cleanup(ctx)
		return 1, err
	}
	if _, err := connectContainer(ctx, tmp, &opts); err != nil {
		tmp.cleanup(ctx)
		return 1, err
	}

	cmdStr := strings.Join(command, " ")
	var sshCmd string
	if len(c.Repos) > 0 {
		sshCmd = "cd ~/src/" + shellQuote(c.repoName()) + " && " + cmdStr
	} else {
		sshCmd = cmdStr
	}
	_, err := runCmd(ctx, "", c.SSHCommand(tmp.Name, sshCmd), false)
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	tmp.cleanup(ctx)
	return exitCode, nil
}

// Kill stops and removes the container.
func (c *Container) Kill(ctx context.Context) error {
	rt := c.Runtime
	_, containerErr := runCmd(ctx, "", []string{rt, "inspect", c.Name}, true)
	containerExists := containerErr == nil
	var anyRemoteExists bool
	for _, repo := range c.Repos {
		if _, err := gitutil.RunGit(ctx, repo.GitRoot, "remote", "get-url", c.Name); err == nil {
			anyRemoteExists = true
			break
		}
	}
	sshConfigDir := filepath.Join(c.Home, ".ssh", "config.d")
	sshConf := filepath.Join(sshConfigDir, c.Name+".conf")
	sshKnown := filepath.Join(sshConfigDir, c.Name+".known_hosts")
	_, sshConfErr := os.Stat(sshConf)
	_, sshKnownErr := os.Stat(sshKnown)
	sshExists := sshConfErr == nil || sshKnownErr == nil

	if !containerExists && !anyRemoteExists && !sshExists {
		return fmt.Errorf("%s not found", c.Name)
	}

	// Clean up non-ephemeral Tailscale node.
	if containerExists {
		if !c.Tailscale {
			tsLabel, _ := runCmd(ctx, "", []string{rt, "inspect", "--format", `{{index .Config.Labels "md.tailscale"}}`, c.Name}, true)
			c.Tailscale = tsLabel == "1"
		}
		if c.Tailscale {
			ephLabel, _ := runCmd(ctx, "", []string{rt, "inspect", "--format", `{{index .Config.Labels "md.tailscale_ephemeral"}}`, c.Name}, true)
			if ephLabel != "1" {
				statusJSON, err := runCmd(ctx, "", []string{rt, "exec", c.Name, "tailscale", "status", "--json"}, true)
				if err == nil {
					var status tailscaleStatus
					if json.Unmarshal([]byte(statusJSON), &status) == nil && status.Self.ID != "" {
						_, _ = fmt.Fprintln(c.W, "- Removing Tailscale node from tailnet...")
						deleteTailscaleDevice(c.TailscaleAPIKey, status.Self.ID)
					}
				}
			}
		}
	}

	_ = os.Remove(sshConf)
	_ = os.Remove(sshKnown)

	var retErr error
	for _, repo := range c.Repos {
		if _, err := gitutil.RunGit(ctx, repo.GitRoot, "remote", "get-url", c.Name); err == nil {
			if _, err := gitutil.RunGit(ctx, repo.GitRoot, "remote", "remove", c.Name); err != nil {
				retErr = errors.Join(retErr, err)
			}
		}
	}
	if containerExists {
		if _, err := runCmd(ctx, "", []string{rt, "rm", "-f", "-v", c.Name}, true); err != nil {
			retErr = err
		}
	}
	_, _ = fmt.Fprintf(c.W, "Removed %s\n", c.Name)
	return retErr
}

// Push force-pushes local state into the container.
func (c *Container) Push(ctx context.Context) error {
	if len(c.Repos) == 0 {
		return errors.New("container has no repos")
	}
	if err := c.checkContainerState(ctx); err != nil {
		return err
	}
	if err := c.SyncDefaultBranch(ctx); err != nil {
		return err
	}
	// Refuse if there are pending local changes on the branch being pushed.
	currentBranch, _ := gitutil.RunGit(ctx, c.primary().GitRoot, "branch", "--show-current")
	if currentBranch == c.primary().Branch {
		if _, err := gitutil.RunGit(ctx, c.primary().GitRoot, "diff", "--quiet", "--exit-code"); err != nil {
			return errors.New("there are pending changes locally. Please commit or stash them before pushing")
		}
	}
	repo := shellQuote(c.repoName())
	branch := shellQuote(c.primary().Branch)
	// Commit any pending changes in the container.
	_, _ = runCmd(ctx, "", c.SSHCommand(c.Name, "cd ~/src/"+repo+" && git add . && (git diff --quiet HEAD -- . || git commit -q -m 'Backup before push')"), true)
	containerCommit, _ := runCmd(ctx, "", c.SSHCommand(c.Name, "cd ~/src/"+repo+" && git rev-parse HEAD"), true)
	backupBranch := "backup-" + time.Now().Format("20060102-150405")
	_, _ = runCmd(ctx, "", c.SSHCommand(c.Name, "cd ~/src/"+repo+" && git branch -f "+backupBranch+" "+containerCommit), true)
	_, _ = fmt.Fprintf(c.W, "- Previous state saved as git branch: %s\n", backupBranch)
	if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "push", "-q", "-f", "--tags", c.Name, c.primary().Branch + ":base"}, false); err != nil {
		return err
	}
	if _, err := runCmd(ctx, "", c.SSHCommand(c.Name, "cd ~/src/"+repo+" && git switch -q -C "+branch+" base"), false); err != nil {
		return err
	}
	// Update the local remote-tracking ref so it reflects the pushed state.
	if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "update-ref", "refs/remotes/" + c.Name + "/" + c.primary().Branch, c.primary().Branch}, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.W, "- Container updated.")
	return nil
}

// Fetch commits any uncommitted changes in the container and fetches them
// locally, updating the remote-tracking ref without integrating.
//
// provider and model control AI commit message generation. See https://github.com/maruel/genai for valid
// names. If provider is empty, a default message is used.
func (c *Container) Fetch(ctx context.Context, provider, model string) error {
	if len(c.Repos) == 0 {
		return errors.New("container has no repos")
	}
	if err := c.checkContainerState(ctx); err != nil {
		return err
	}
	if err := c.SyncDefaultBranch(ctx); err != nil {
		return err
	}
	repo := shellQuote(c.repoName())
	// Check if there are uncommitted changes in the container.
	if _, err := runCmd(ctx, "", c.SSHCommand(c.Name, "cd ~/src/"+repo+" && git add . && git diff --quiet HEAD -- ."), true); err != nil {
		commitMsg := "Pull from md"
		if provider != "" {
			if p, err := newProvider(ctx, provider, model); err != nil {
				slog.WarnContext(ctx, "failed to initialize provider", "err", err)
			} else {
				metadata := c.gatherGitMetadata(ctx, c.Name, repo)
				diff := c.gatherGitDiff(ctx, c.Name, repo)
				if msg, err := gitutil.GenerateCommitMsg(ctx, p, metadata, diff, nil); err != nil {
					slog.WarnContext(ctx, "failed to generate commit message", "err", err)
				} else if msg != "" {
					commitMsg = msg
				}
			}
		}
		gitUserName, _ := gitutil.RunGit(ctx, c.primary().GitRoot, "config", "user.name")
		gitUserEmail, _ := gitutil.RunGit(ctx, c.primary().GitRoot, "config", "user.email")
		gitAuthor := shellQuote(gitUserName + " <" + gitUserEmail + ">")
		commitCmd := "cd ~/src/" + repo + " && echo " + shellQuote(commitMsg) + " | git commit -a -q --author " + gitAuthor + " -F -"
		if _, err := runCmd(ctx, "", c.SSHCommand(c.Name, commitCmd), false); err != nil {
			return fmt.Errorf("committing in container: %w", err)
		}
	}
	if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "fetch", "-q", c.Name, c.primary().Branch}, false); err != nil {
		return err
	}
	for _, repo := range c.Repos[1:] {
		repoName := shellQuote(filepath.Base(repo.GitRoot))
		// Commit any pending changes in the container for this repo (best-effort).
		commitCmd := "cd ~/src/" + repoName + " && git add . && git diff --quiet HEAD -- . || git commit -q -m 'Pull from md'"
		_, _ = runCmd(ctx, "", c.SSHCommand(c.Name, commitCmd), true)
		// Fetch from container to host.
		if _, err := runCmd(ctx, repo.GitRoot, []string{"git", "fetch", "-q", c.Name, repo.Branch}, false); err != nil {
			return fmt.Errorf("fetch extra repo %s: %w", filepath.Base(repo.GitRoot), err)
		}
	}
	return nil
}

// Pull fetches changes from the container and integrates them into the local branch.
//
// provider and model control AI commit message generation. See https://github.com/maruel/genai for valid
// names. If provider is empty, a default message is used.
func (c *Container) Pull(ctx context.Context, provider, model string) error {
	if err := c.Fetch(ctx, provider, model); err != nil {
		return err
	}
	remoteRef := c.Name + "/" + c.primary().Branch
	currentBranch, _ := gitutil.RunGit(ctx, c.primary().GitRoot, "branch", "--show-current")
	if currentBranch == c.primary().Branch {
		// Already on the branch, rebase locally.
		if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "rebase", "-q", remoteRef}, false); err != nil {
			return err
		}
	} else if _, err := gitutil.RunGit(ctx, c.primary().GitRoot, "merge-base", "--is-ancestor", c.primary().Branch, remoteRef); err == nil {
		// Fast-forward: update ref without checkout.
		if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "update-ref", "refs/heads/" + c.primary().Branch, remoteRef}, false); err != nil {
			return err
		}
	} else {
		// Not a fast-forward. Checkout the branch, rebase, then checkout back.
		origRef := currentBranch
		if origRef == "" {
			origRef, _ = gitutil.RunGit(ctx, c.primary().GitRoot, "rev-parse", "HEAD")
		}
		if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "checkout", "-q", c.primary().Branch}, false); err != nil {
			return err
		}
		if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "rebase", "-q", remoteRef}, false); err != nil {
			_, _ = runCmd(ctx, c.primary().GitRoot, []string{"git", "checkout", "-q", origRef}, false)
			return err
		}
		if _, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "checkout", "-q", origRef}, false); err != nil {
			return err
		}
	}
	_, err := runCmd(ctx, c.primary().GitRoot, []string{"git", "push", "-q", "-f", c.Name, c.primary().Branch + ":base"}, false)
	return err
}

// Diff writes the diff between base and current for Repos[repoIdx] to stdout/stderr.
// When stdout is a terminal, a TTY is allocated so git's pager and colors work.
func (c *Container) Diff(ctx context.Context, repoIdx int, stdout, stderr io.Writer, extraArgs []string) error {
	if len(c.Repos) == 0 {
		return errors.New("container has no repos")
	}
	if repoIdx < 0 || repoIdx >= len(c.Repos) {
		return fmt.Errorf("repo index %d out of range [0, %d)", repoIdx, len(c.Repos))
	}
	if err := c.checkContainerState(ctx); err != nil {
		return err
	}
	if err := c.SyncDefaultBranch(ctx); err != nil {
		return err
	}
	quotedArgs := make([]string, len(extraArgs))
	for i, a := range extraArgs {
		quotedArgs[i] = shellQuote(a)
	}
	repo := c.Repos[repoIdx]
	repoName := shellQuote(strings.TrimSuffix(filepath.Base(repo.GitRoot), ".git"))
	sshArgs := c.SSHCommand("-q")
	cmd := exec.CommandContext(ctx, sshArgs[0])
	if f, ok := stdout.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		sshArgs = append(sshArgs, "-t")
		cmd.Stdin = os.Stdin
	}
	sshArgs = append(sshArgs, c.Name, "cd ~/src/"+repoName+" && git add . && git diff base "+strings.Join(quotedArgs, " ")+" -- .")
	cmd.Path, _ = exec.LookPath(sshArgs[0])
	cmd.Args = sshArgs
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// ContainerStats holds runtime resource usage for a container.
type ContainerStats struct {
	// CPUPerc is the CPU usage as a percentage (e.g. 1.23).
	CPUPerc float64 `json:"cpu_perc"`
	// MemUsed is memory currently used in bytes.
	MemUsed uint64 `json:"mem_used"`
	// MemLimit is the memory limit in bytes.
	MemLimit uint64 `json:"mem_limit"`
	// MemPerc is the memory usage as a percentage (e.g. 2.0).
	MemPerc float64 `json:"mem_perc"`
	// PIDs is the number of running processes.
	PIDs int `json:"pids"`
}

// Stats returns the current CPU and memory usage for the container.
func (c *Container) Stats(ctx context.Context) (*ContainerStats, error) {
	out, err := runCmd(ctx, "", []string{
		c.Runtime, "stats", "--no-stream", "--no-trunc",
		"--format", "{{json .}}", c.Name,
	}, true)
	if err != nil {
		return nil, fmt.Errorf("container %s is not running", c.Name)
	}
	var raw struct {
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
		MemPerc  string `json:"MemPerc"`
		PIDs     string `json:"PIDs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing stats: %w", err)
	}
	cpuPerc, err := parsePercent(raw.CPUPerc)
	if err != nil {
		return nil, fmt.Errorf("parsing CPU%%: %w", err)
	}
	memPerc, err := parsePercent(raw.MemPerc)
	if err != nil {
		return nil, fmt.Errorf("parsing mem%%: %w", err)
	}
	memUsed, memLimit, err := parseMemUsage(raw.MemUsage)
	if err != nil {
		return nil, fmt.Errorf("parsing mem usage: %w", err)
	}
	pids, err := strconv.Atoi(raw.PIDs)
	if err != nil {
		return nil, fmt.Errorf("parsing PIDs: %w", err)
	}
	return &ContainerStats{
		CPUPerc:  cpuPerc,
		MemUsed:  memUsed,
		MemLimit: memLimit,
		MemPerc:  memPerc,
		PIDs:     pids,
	}, nil
}

// parsePercent parses a percentage string like "1.23%" into 1.23.
func parsePercent(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	return strconv.ParseFloat(s, 64)
}

// parseMemUsage parses "150MiB / 7.5GiB" into (used, limit) in bytes.
func parseMemUsage(s string) (uint64, uint64, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected 'used / limit', got %q", s)
	}
	used, err := parseByteSize(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	limit, err := parseByteSize(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// byteUnits maps suffixes used by docker/podman stats to multipliers.
var byteUnits = []struct {
	suffix string
	mult   uint64
}{
	{"KiB", 1 << 10},
	{"MiB", 1 << 20},
	{"GiB", 1 << 30},
	{"TiB", 1 << 40},
	{"kB", 1000},
	{"MB", 1000 * 1000},
	{"GB", 1000 * 1000 * 1000},
	{"TB", 1000 * 1000 * 1000 * 1000},
	{"B", 1},
}

// parseByteSize parses a size string like "150MiB" or "7.5GiB" into bytes.
func parseByteSize(s string) (uint64, error) {
	for _, u := range byteUnits {
		if numStr, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
			if err != nil {
				return 0, fmt.Errorf("parsing %q: %w", s, err)
			}
			return uint64(f * float64(u.mult)), nil
		}
	}
	return 0, fmt.Errorf("unknown unit in %q", s)
}

// GetHostPort returns the host port mapped to a container port (e.g.
// "5901/tcp"). Returns 0 if the port is not mapped.
func (c *Container) GetHostPort(ctx context.Context, containerPort string) (int32, error) {
	rt := c.Runtime
	if _, err := runCmd(ctx, "", []string{rt, "inspect", c.Name}, true); err != nil {
		return 0, fmt.Errorf("container %s is not running", c.Name)
	}
	return getHostPort(ctx, rt, c.Name, containerPort)
}

// getHostPort extracts the host port for containerPort from a running
// container. It uses JSON output instead of Go templates to work around
// Docker 27's "index of untyped nil" bug when port bindings are nil.
func getHostPort(ctx context.Context, rt, container, containerPort string) (int32, error) {
	raw, err := runCmd(ctx, "", []string{rt, "inspect", "--format", "{{json .NetworkSettings.Ports}}", container}, true)
	if err != nil {
		return 0, err
	}
	var ports map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}
	if err := json.Unmarshal([]byte(raw), &ports); err != nil {
		return 0, fmt.Errorf("parsing port map: %w", err)
	}
	bindings := ports[containerPort]
	if len(bindings) == 0 {
		return 0, nil
	}
	port, err := strconv.ParseInt(bindings[0].HostPort, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parsing host port %q: %w", bindings[0].HostPort, err)
	}
	return int32(port), nil
}

// tailscaleStatus is the subset of `tailscale status --json` we care about.
type tailscaleStatus struct {
	Self struct {
		ID      string `json:"ID"`
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

// TailscaleFQDN returns the Tailscale FQDN for the container, or "" if unavailable.
func (c *Container) TailscaleFQDN(ctx context.Context) string {
	if !c.Tailscale || c.State != "running" {
		return ""
	}
	statusJSON, err := runCmd(ctx, "", []string{c.Runtime, "exec", c.Name, "tailscale", "status", "--json"}, true)
	if err != nil {
		return ""
	}
	var status tailscaleStatus
	if json.Unmarshal([]byte(statusJSON), &status) != nil {
		return ""
	}
	return strings.TrimRight(status.Self.DNSName, ".")
}

// containerJSON is the raw Docker ps JSON structure.
type containerJSON struct {
	Names     string `json:"Names"`
	State     string `json:"State"`
	CreatedAt string `json:"CreatedAt"`
	Labels    string `json:"Labels"`
}

// parseCreatedAt parses a container creation timestamp. Docker uses
// "2006-01-02 15:04:05 -0700 MST"; Podman uses ISO 8601 (RFC 3339).
func parseCreatedAt(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05 -0700 MST",           // Docker
		time.RFC3339Nano,                          // Podman
		time.RFC3339,                              // Podman (no fractional seconds)
		"2006-01-02 15:04:05.999999999 -0700 MST", // Docker with nanoseconds
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse CreatedAt %q", s)
}

// unmarshalContainer parses docker/podman ps JSON output, converting the
// CreatedAt timestamp string into a time.Time and extracting md.* labels.
// The returned Container has a nil Client; callers must set it.
func unmarshalContainer(data []byte) (Container, error) {
	var raw containerJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Container{}, err
	}
	ct := Container{
		Name:  raw.Names,
		State: raw.State,
	}
	if raw.CreatedAt != "" {
		t, err := parseCreatedAt(raw.CreatedAt)
		if err != nil {
			return Container{}, err
		}
		ct.CreatedAt = t
	}
	// Docker ps outputs labels as comma-separated key=value pairs.
	for kv := range strings.SplitSeq(raw.Labels, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "md.repos":
			if data, err := base64.StdEncoding.DecodeString(v); err == nil {
				_ = json.Unmarshal(data, &ct.Repos)
			}
		case "md.display":
			ct.Display = v == "1"
		case "md.tailscale":
			ct.Tailscale = v == "1"
		case "md.usb":
			ct.USB = v == "1"
		}
	}
	return ct, nil
}

// resolveDefaults populates DefaultRemote and DefaultBranch if not already set.
func (c *Container) resolveDefaults(ctx context.Context) error {
	if len(c.Repos) == 0 {
		return errors.New("container has no repos")
	}
	if c.DefaultRemote == "" {
		remote, err := gitutil.DefaultRemote(ctx, c.primary().GitRoot)
		if err != nil {
			return err
		}
		c.DefaultRemote = remote
	}
	if c.DefaultBranch == "" {
		branch, err := gitutil.DefaultBranch(ctx, c.primary().GitRoot, c.DefaultRemote)
		if err != nil {
			return err
		}
		c.DefaultBranch = branch
	}
	return nil
}

// SyncDefaultBranch force-pushes the host's default branch (e.g. origin/main)
// into the container so agents can diff against it.
func (c *Container) SyncDefaultBranch(ctx context.Context) error {
	if len(c.Repos) == 0 {
		return errors.New("container has no repos")
	}
	if err := c.resolveDefaults(ctx); err != nil {
		return fmt.Errorf("sync default branch: %w", err)
	}
	// If the container's working branch is the default branch, it's already
	// synced as "base".
	if c.DefaultBranch == c.primary().Branch {
		return nil
	}
	if _, err := gitutil.RunGit(ctx, c.primary().GitRoot, "push", "-q", "-f", c.Name, "refs/remotes/"+c.DefaultRemote+"/"+c.DefaultBranch+":refs/heads/"+c.DefaultBranch); err != nil {
		return fmt.Errorf("sync default branch %q: %w", c.DefaultBranch, err)
	}
	return nil
}

func (c *Container) checkContainerState(ctx context.Context) error {
	_, containerErr := runCmd(ctx, "", []string{c.Runtime, "inspect", c.Name}, true)
	containerExists := containerErr == nil
	var remoteExists bool
	if len(c.Repos) > 0 {
		_, remoteErr := gitutil.RunGit(ctx, c.primary().GitRoot, "remote", "get-url", c.Name)
		remoteExists = remoteErr == nil
	}
	sshConfigDir := filepath.Join(c.Home, ".ssh", "config.d")
	_, sshErr := os.Stat(filepath.Join(sshConfigDir, c.Name+".conf"))
	sshExists := sshErr == nil

	if !containerExists && !remoteExists && !sshExists {
		if len(c.Repos) > 0 {
			return fmt.Errorf("no container running for branch '%s'.\nStart a container with: md start", c.primary().Branch)
		}
		return fmt.Errorf("container %s not found.\nStart a container with: md start", c.Name)
	}
	var issues []string
	if !containerExists {
		issues = append(issues, "Docker container is not running")
	}
	if len(c.Repos) > 0 && !remoteExists {
		issues = append(issues, "Git remote is missing")
	}
	if !sshExists {
		issues = append(issues, "SSH config is missing")
	}
	if len(issues) > 0 {
		return fmt.Errorf("inconsistent state detected for %s:\n  - %s\nConsider running 'md kill' to clean up, then 'md start' to restart",
			c.Name, strings.Join(issues, "\n  - "))
	}
	return nil
}

func (c *Container) cleanup(ctx context.Context) {
	removeSSHConfig(filepath.Join(c.Home, ".ssh", "config.d"), c.Name)
	if len(c.Repos) > 0 {
		_, _ = gitutil.RunGit(ctx, c.primary().GitRoot, "remote", "remove", c.Name)
		for _, repo := range c.Repos[1:] {
			_, _ = gitutil.RunGit(ctx, repo.GitRoot, "remote", "remove", c.Name)
		}
	}
	_, _ = runCmd(ctx, "", []string{c.Runtime, "rm", "-f", "-v", c.Name}, true)
}

// newProvider creates a genai.Provider for the given provider name and model.
func newProvider(ctx context.Context, provider, model string) (genai.Provider, error) {
	cfg, ok := providers.All[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	m := genai.ProviderOptionModel(model)
	if m == "" {
		m = genai.ModelCheap
	}
	return cfg.Factory(ctx, m)
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

// shellQuote returns a shell-escaped version of s, safe for embedding in a
// single-quoted shell string.  Equivalent to Python's shlex.quote.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// If the string contains only safe characters, return it as-is.
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') &&
			c != '@' && c != '%' && c != '+' && c != '=' && c != ':' && c != ',' && c != '.' &&
			c != '/' && c != '-' && c != '_' {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}
