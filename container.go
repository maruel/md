// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maruel/genai"
	"github.com/maruel/genai/providers"
	"golang.org/x/term"
)

// Container holds state for a single container instance.
type Container struct {
	*Client
	GitRoot   string
	RepoName  string
	Branch    string
	Name      string
	State     string
	CreatedAt time.Time
}

// Start creates and starts a container.
func (c *Container) Start(ctx context.Context, opts *StartOpts) (retErr error) {
	// Check if container already exists.
	if _, err := runCmd(ctx, []string{"docker", "inspect", c.Name}, true); err == nil {
		return fmt.Errorf("container %s already exists. SSH in with 'ssh %s' or clean it up via 'md kill' first",
			c.Name, c.Name)
	}

	// Generate Tailscale auth key if needed.
	if opts.Tailscale && opts.TailscaleAuthKey == "" {
		if key := os.Getenv("TAILSCALE_AUTHKEY"); key != "" {
			opts.TailscaleAuthKey = key
		} else {
			key, err := generateTailscaleAuthKey()
			if err != nil {
				_, _ = fmt.Fprintf(c.W, "- Could not generate Tailscale auth key (%v), will use browser auth\n", err)
			} else {
				opts.TailscaleAuthKey = key
				opts.TailscaleEphemeral = true
			}
		}
	}

	buildCtx, err := prepareBuildContext()
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()

	if err := buildCustomizedImage(ctx, c.W, buildCtx, c.keysDir, c.ImageName, c.BaseImage, c.TagExplicit, false); err != nil {
		return err
	}
	if err := runContainer(ctx, c, opts); err != nil {
		return err
	}
	if !opts.NoSSH {
		cmd := exec.CommandContext(ctx, "ssh", c.Name)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return nil
}

// Run starts a temporary container, runs a command, then cleans up.
func (c *Container) Run(ctx context.Context, command []string) (_ int, retErr error) {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	tmp := &Container{
		Client:   c.Client,
		GitRoot:  c.GitRoot,
		RepoName: c.RepoName,
		Branch:   c.Branch,
		Name:     fmt.Sprintf("md-%s-run-%x", sanitizeDockerName(c.RepoName), buf),
	}

	buildCtx, err := prepareBuildContext()
	if err != nil {
		return 1, err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(buildCtx)) }()

	if err := buildCustomizedImage(ctx, c.W, buildCtx, c.keysDir, c.ImageName, c.BaseImage, c.TagExplicit, true); err != nil {
		return 1, err
	}
	opts := StartOpts{NoSSH: true, Quiet: true}
	if err := runContainer(ctx, tmp, &opts); err != nil {
		tmp.cleanup(ctx)
		return 1, err
	}

	cmdStr := strings.Join(command, " ")
	_, err = runCmd(ctx, []string{"ssh", tmp.Name, "cd ./" + shellQuote(c.RepoName) + " && " + cmdStr}, false)
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
	_, containerErr := runCmd(ctx, []string{"docker", "inspect", c.Name}, true)
	containerExists := containerErr == nil
	_, remoteErr := runCmd(ctx, []string{"git", "remote", "get-url", c.Name}, true)
	remoteExists := remoteErr == nil
	sshConfigDir := filepath.Join(c.Home, ".ssh", "config.d")
	sshConf := filepath.Join(sshConfigDir, c.Name+".conf")
	sshKnown := filepath.Join(sshConfigDir, c.Name+".known_hosts")
	_, sshConfErr := os.Stat(sshConf)
	_, sshKnownErr := os.Stat(sshKnown)
	sshExists := sshConfErr == nil || sshKnownErr == nil

	if !containerExists && !remoteExists && !sshExists {
		return fmt.Errorf("%s not found", c.Name)
	}

	// Clean up non-ephemeral Tailscale node.
	if containerExists {
		envOut, err := runCmd(ctx, []string{"docker", "inspect", "--format", `{{range .Config.Env}}{{println .}}{{end}}`, c.Name}, true)
		if err == nil && strings.Contains(envOut, "MD_TAILSCALE=1") && !strings.Contains(envOut, "MD_TAILSCALE_EPHEMERAL=1") {
			statusJSON, err := runCmd(ctx, []string{"docker", "exec", c.Name, "tailscale", "status", "--json"}, true)
			if err == nil {
				var status struct {
					Self struct {
						ID string `json:"ID"`
					} `json:"Self"`
				}
				if json.Unmarshal([]byte(statusJSON), &status) == nil && status.Self.ID != "" {
					_, _ = fmt.Fprintln(c.W, "- Removing Tailscale node from tailnet...")
					deleteTailscaleDevice(status.Self.ID)
				}
			}
		}
	}

	_ = os.Remove(sshConf)
	_ = os.Remove(sshKnown)

	var retErr error
	if remoteExists {
		if _, err := runCmd(ctx, []string{"git", "remote", "remove", c.Name}, true); err != nil {
			retErr = err
		}
	}
	if containerExists {
		if _, err := runCmd(ctx, []string{"docker", "rm", "-f", "-v", c.Name}, true); err != nil {
			retErr = err
		}
	}
	_, _ = fmt.Fprintf(c.W, "Removed %s\n", c.Name)
	return retErr
}

// Push force-pushes local state into the container.
func (c *Container) Push(ctx context.Context) error {
	if err := c.checkContainerState(ctx); err != nil {
		return err
	}
	// Refuse if there are pending local changes on the branch being pushed.
	currentBranch, _ := runCmd(ctx, []string{"git", "branch", "--show-current"}, true)
	if currentBranch == c.Branch {
		if _, err := runCmd(ctx, []string{"git", "diff", "--quiet", "--exit-code"}, true); err != nil {
			return errors.New("there are pending changes locally. Please commit or stash them before pushing")
		}
	}
	repo := shellQuote(c.RepoName)
	branch := shellQuote(c.Branch)
	// Commit any pending changes in the container.
	_, _ = runCmd(ctx, []string{"ssh", c.Name, "cd ./" + repo + " && git add . && (git diff --quiet HEAD -- . || git commit -q -m 'Backup before push')"}, true)
	containerCommit, _ := runCmd(ctx, []string{"ssh", c.Name, "cd ./" + repo + " && git rev-parse HEAD"}, true)
	backupBranch := "backup-" + time.Now().Format("20060102-150405")
	_, _ = runCmd(ctx, []string{"ssh", c.Name, "cd ./" + repo + " && git branch -f " + backupBranch + " " + containerCommit}, true)
	_, _ = fmt.Fprintf(c.W, "- Previous state saved as git branch: %s\n", backupBranch)
	if _, err := runCmd(ctx, []string{"git", "push", "-q", "-f", "--tags", c.Name, c.Branch + ":base"}, false); err != nil {
		return err
	}
	if _, err := runCmd(ctx, []string{"ssh", c.Name, "cd ./" + repo + " && git switch -q -C " + branch + " base"}, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.W, "- Container updated.")
	return nil
}

// Pull pulls changes from the container back to the local repo.
func (c *Container) Pull(ctx context.Context) error {
	if err := c.checkContainerState(ctx); err != nil {
		return err
	}
	repo := shellQuote(c.RepoName)
	// Check if there are uncommitted changes in the container.
	if _, err := runCmd(ctx, []string{"ssh", c.Name, "cd ./" + repo + " && git add . && git diff --quiet HEAD -- ."}, true); err != nil {
		commitMsg := "Pull from md"
		if provider := os.Getenv("ASK_PROVIDER"); provider != "" {
			metadata := gatherGitMetadata(ctx, c.Name, repo)
			diff := gatherGitDiff(ctx, c.Name, repo)
			if msg, err := generateCommitMsg(ctx, provider, metadata, diff); err == nil && msg != "" {
				commitMsg = msg
			}
		}
		gitUserName, _ := runCmd(ctx, []string{"git", "config", "user.name"}, true)
		gitUserEmail, _ := runCmd(ctx, []string{"git", "config", "user.email"}, true)
		gitAuthor := shellQuote(gitUserName + " <" + gitUserEmail + ">")
		commitCmd := "cd ./" + repo + " && echo " + shellQuote(commitMsg) + " | git commit -a -q --author " + gitAuthor + " -F -"
		if _, err := runCmd(ctx, []string{"ssh", c.Name, commitCmd}, false); err != nil {
			return fmt.Errorf("committing in container: %w", err)
		}
	}
	if _, err := runCmd(ctx, []string{"git", "fetch", "-q", c.Name, c.Branch}, false); err != nil {
		return err
	}
	currentBranch, _ := runCmd(ctx, []string{"git", "branch", "--show-current"}, true)
	if currentBranch == c.Branch {
		// Already on the branch, rebase locally.
		if _, err := runCmd(ctx, []string{"git", "rebase", "-q", "FETCH_HEAD"}, false); err != nil {
			return err
		}
	} else if _, err := runCmd(ctx, []string{"git", "merge-base", "--is-ancestor", c.Branch, "FETCH_HEAD"}, true); err == nil {
		// Fast-forward: update ref without checkout.
		if _, err := runCmd(ctx, []string{"git", "update-ref", "refs/heads/" + c.Branch, "FETCH_HEAD"}, false); err != nil {
			return err
		}
	} else {
		// Not a fast-forward. Checkout the branch, rebase, then checkout back.
		origRef := currentBranch
		if origRef == "" {
			origRef, _ = runCmd(ctx, []string{"git", "rev-parse", "HEAD"}, true)
		}
		if _, err := runCmd(ctx, []string{"git", "checkout", "-q", c.Branch}, false); err != nil {
			return err
		}
		if _, err := runCmd(ctx, []string{"git", "rebase", "-q", "FETCH_HEAD"}, false); err != nil {
			_, _ = runCmd(ctx, []string{"git", "checkout", "-q", origRef}, false)
			return err
		}
		if _, err := runCmd(ctx, []string{"git", "checkout", "-q", origRef}, false); err != nil {
			return err
		}
	}
	_, err := runCmd(ctx, []string{"git", "push", "-q", "-f", c.Name, c.Branch + ":base"}, false)
	return err
}

// Diff writes the diff between base and current in the container.
// When stdout is a terminal, a TTY is allocated so git's pager and colors work.
func (c *Container) Diff(ctx context.Context, stdout, stderr io.Writer, extraArgs []string) error {
	quotedArgs := make([]string, len(extraArgs))
	for i, a := range extraArgs {
		quotedArgs[i] = shellQuote(a)
	}
	sshArgs := []string{"ssh", "-q"}
	cmd := exec.CommandContext(ctx, "ssh") // args set below
	if f, ok := stdout.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		sshArgs = append(sshArgs, "-t")
		cmd.Stdin = os.Stdin
	}
	sshArgs = append(sshArgs, c.Name, "cd ./"+shellQuote(c.RepoName)+" && git add . && git diff base "+strings.Join(quotedArgs, " ")+" -- .")
	cmd.Path, _ = exec.LookPath(sshArgs[0])
	cmd.Args = sshArgs
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// GetHostPort returns the host port mapped to a container port (e.g.
// "5901/tcp"). Returns empty string if the port is not mapped.
func (c *Container) GetHostPort(ctx context.Context, containerPort string) (string, error) {
	if _, err := runCmd(ctx, []string{"docker", "inspect", c.Name}, true); err != nil {
		return "", fmt.Errorf("container %s is not running", c.Name)
	}
	port, err := runCmd(ctx, []string{"docker", "inspect", "--format", `{{(index .NetworkSettings.Ports "` + containerPort + `" 0).HostPort}}`, c.Name}, true)
	if err != nil {
		return "", err
	}
	if port == "" {
		return "", nil
	}
	return port, nil
}

// containerJSON is the raw Docker ps JSON structure.
type containerJSON struct {
	Names     string `json:"Names"`
	State     string `json:"State"`
	CreatedAt string `json:"CreatedAt"`
	Labels    string `json:"Labels"`
}

// unmarshalContainer parses docker ps JSON output, converting the CreatedAt
// timestamp string into a time.Time and extracting md.* labels.
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
		// Docker uses "2006-01-02 15:04:05 -0700 MST" format.
		t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", raw.CreatedAt)
		if err != nil {
			return Container{}, fmt.Errorf("parsing CreatedAt %q: %w", raw.CreatedAt, err)
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
		case "md.git_root":
			ct.GitRoot = v
		case "md.repo_name":
			ct.RepoName = v
		case "md.branch":
			ct.Branch = v
		}
	}
	return ct, nil
}

func (c *Container) checkContainerState(ctx context.Context) error {
	_, containerErr := runCmd(ctx, []string{"docker", "inspect", c.Name}, true)
	containerExists := containerErr == nil
	_, remoteErr := runCmd(ctx, []string{"git", "remote", "get-url", c.Name}, true)
	remoteExists := remoteErr == nil
	sshConfigDir := filepath.Join(c.Home, ".ssh", "config.d")
	_, sshErr := os.Stat(filepath.Join(sshConfigDir, c.Name+".conf"))
	sshExists := sshErr == nil

	if !containerExists && !remoteExists && !sshExists {
		return fmt.Errorf("no container running for branch '%s'.\nStart a container with: md start", c.Branch)
	}
	if !containerExists || !remoteExists || !sshExists {
		var issues []string
		if !containerExists {
			issues = append(issues, "Docker container is not running")
		}
		if !remoteExists {
			issues = append(issues, "Git remote is missing")
		}
		if !sshExists {
			issues = append(issues, "SSH config is missing")
		}
		return fmt.Errorf("inconsistent state detected for %s:\n  - %s\nConsider running 'md kill' to clean up, then 'md start' to restart",
			c.Name, strings.Join(issues, "\n  - "))
	}
	return nil
}

func (c *Container) cleanup(ctx context.Context) {
	removeSSHConfig(filepath.Join(c.Home, ".ssh", "config.d"), c.Name)
	_, _ = runCmd(ctx, []string{"git", "remote", "remove", c.Name}, true)
	_, _ = runCmd(ctx, []string{"docker", "rm", "-f", "-v", c.Name}, true)
}

// genCommitMsg generates a commit message using the genai library.
func genCommitMsg(ctx context.Context, provider, prompt string) (string, error) {
	cfg, ok := providers.All[provider]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", provider)
	}
	model := genai.ProviderOptionModel(os.Getenv("ASK_MODEL"))
	if model == "" {
		model = genai.ModelGood
	}
	p, err := cfg.Factory(ctx, model)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := p.GenSync(ctx, genai.Messages{genai.NewTextMessage(prompt)}, &genai.GenOptionText{MaxTokens: 1024})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.String()), nil
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
