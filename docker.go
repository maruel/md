// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
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

// hostArch returns the Docker platform architecture string.
func hostArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unknown architecture: %s", runtime.GOARCH)
	}
}

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

func getImageCreatedTime(ctx context.Context, imageName string) string {
	out, _ := dockerInspectFormat(ctx, imageName, "{{.Created}}")
	return out
}

func getImageVersionLabel(ctx context.Context, imageName string) string {
	out, err := dockerInspectFormat(ctx, imageName, `{{index .Config.Labels "org.opencontainers.image.version"}}`)
	if err != nil || out == "" || out == "<no value>" {
		return ""
	}
	return out
}

// dateToEpoch converts Docker's date string to a Unix timestamp.
func dateToEpoch(dateStr string) int64 {
	// Try parsing various Docker date formats.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	} {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// buildCustomizedImage builds the per-user Docker image. keysDir is the
// directory containing SSH host keys and authorized_keys, supplied to Docker
// as a named build context "md-keys".
func buildCustomizedImage(ctx context.Context, w io.Writer, buildCtxDir, keysDir, imageName, baseImage string, quiet bool) error {
	arch, err := hostArch()
	if err != nil {
		return err
	}
	// Check if local md-base is newer than remote.
	if baseImage == DefaultBaseImage {
		localBase := getImageCreatedTime(ctx, "md-base")
		if localBase != "" {
			remoteBase := getImageCreatedTime(ctx, baseImage)
			if remoteBase == "" {
				if !quiet {
					_, _ = fmt.Fprintf(w, "- Remote %s image not found, using local build instead\n", baseImage)
				}
				baseImage = "md-base"
			} else if dateToEpoch(localBase) > dateToEpoch(remoteBase) {
				if !quiet {
					_, _ = fmt.Fprintf(w, "- Local md-base image is newer, using local build instead of %s\n", baseImage)
					_, _ = fmt.Fprintln(w, "  Run 'docker image rm md-base' to delete the local image.")
				}
				baseImage = "md-base"
			}
		}
	}
	if baseImage != "md-base" {
		if !quiet {
			_, _ = fmt.Fprintf(w, "- Pulling base image %s ...\n", baseImage)
		}
		args := []string{"docker", "pull", "--platform", "linux/" + arch, baseImage}
		if _, err := runCmd(ctx, "", args, quiet); err != nil {
			return fmt.Errorf("pulling base image: %w", err)
		}
		if !quiet && !strings.Contains(baseImage, ":") {
			if v := getImageVersionLabel(ctx, baseImage); strings.HasPrefix(v, "v") {
				_, _ = fmt.Fprintf(w, "  Version: %s\n", v)
			}
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
		_, _ = fmt.Fprintf(w, "- Building Docker image %s ...\n", imageName)
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

// DefaultBaseImage is the base image used when none is specified.
const DefaultBaseImage = "ghcr.io/maruel/md"

// StartOpts configures container startup.
type StartOpts struct {
	// BaseImage is the full Docker image reference (e.g.
	// "ghcr.io/maruel/md:v1.0" or "myregistry/custom:tag"). When empty,
	// DefaultBaseImage is used.
	BaseImage          string
	Display            bool
	Tailscale          bool
	TailscaleAuthKey   string
	TailscaleEphemeral bool
	Labels             []string
	NoSSH              bool
	Quiet              bool
}

// runContainer starts the Docker container and sets up SSH access.
func runContainer(ctx context.Context, c *Container, opts *StartOpts) error {
	arch, err := hostArch()
	if err != nil {
		return err
	}
	_ = arch // Used for documentation; platform already set in image.

	var dockerArgs []string
	dockerArgs = append(dockerArgs, "docker", "run", "-d",
		"--name", c.Name, "--hostname", c.Name,
		"-p", "127.0.0.1:0:22")

	if opts.Display {
		dockerArgs = append(dockerArgs, "-p", "127.0.0.1:0:5901", "-e", "MD_DISPLAY=1")
	}
	dockerArgs = append(dockerArgs, "-e", "MD_REPO_DIR="+c.RepoName)

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
		if opts.TailscaleEphemeral {
			dockerArgs = append(dockerArgs, "-e", "MD_TAILSCALE_EPHEMERAL=1")
		}
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
	dockerArgs = append(dockerArgs,
		"--label", "md.git_root="+c.GitRoot,
		"--label", "md.repo_name="+c.RepoName,
		"--label", "md.branch="+c.Branch)
	for _, l := range opts.Labels {
		dockerArgs = append(dockerArgs, "--label", l)
	}
	dockerArgs = append(dockerArgs, c.ImageName)

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
	port, err := runCmd(ctx, "", []string{"docker", "inspect", "--format", `{{(index .NetworkSettings.Ports "22/tcp" 0).HostPort}}`, c.Name}, true)
	if err != nil {
		return fmt.Errorf("getting SSH port: %w", err)
	}
	if !opts.Quiet {
		_, _ = fmt.Fprintf(c.W, "- Found ssh port %s\n", port)
	}
	createdStr, err := runCmd(ctx, "", []string{"docker", "inspect", "--format", "{{.Created}}", c.Name}, true)
	if err != nil {
		return fmt.Errorf("getting container creation time: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return fmt.Errorf("parsing container creation time %q: %w", createdStr, err)
	}
	c.CreatedAt = created

	// Get VNC port if display enabled.
	var vncPort string
	if opts.Display {
		vncPort, _ = runCmd(ctx, "", []string{"docker", "inspect", "--format", `{{(index .NetworkSettings.Ports "5901/tcp" 0).HostPort}}`, c.Name}, true)
		if vncPort != "" && !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Found VNC port %s (display :1)\n", vncPort)
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
	if err := writeSSHConfig(sshConfigDir, c.Name, port, c.UserKeyPath, knownHostsPath); err != nil {
		return err
	}
	if err := writeKnownHosts(knownHostsPath, port, strings.TrimSpace(string(hostPubKey))); err != nil {
		return err
	}

	// Set up git remote.
	if !opts.Quiet {
		_, _ = fmt.Fprintln(c.W, "- git clone into container ...")
	}
	_, _ = runCmd(ctx, c.GitRoot, []string{"git", "remote", "rm", c.Name}, true)
	if _, err := runCmd(ctx, c.GitRoot, []string{"git", "remote", "add", c.Name, "user@" + c.Name + ":/home/user/src/" + c.RepoName}, false); err != nil {
		return fmt.Errorf("adding git remote: %w", err)
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
			return fmt.Errorf("timed out waiting for container SSH: %s", lastOutput)
		}
	}

	repo := shellQuote(c.RepoName)
	branch := shellQuote(c.Branch)

	// Initialize git repo in container.
	if _, err := runCmd(ctx, "", []string{"ssh", c.Name, "git init -q ~/src/" + repo}, false); err != nil {
		return err
	}
	/*
		if _, err := runCmd(ctx, c.GitRoot, []string{"git", "fetch", c.Name}, false); err != nil {
			return err
		}
	*/
	if _, err := runCmd(ctx, c.GitRoot, []string{"git", "push", "-q", "--tags", c.Name, c.Branch + ":refs/heads/" + c.Branch}, false); err != nil {
		return err
	}
	if _, err := runCmd(ctx, "", []string{"ssh", c.Name, "cd ~/src/" + repo + " && git switch -q " + branch + " && git branch -f base " + branch + " && git switch -q base && git switch -q " + branch}, false); err != nil {
		return err
	}

	// Set up origin remote in container using HTTPS.
	originURL, err := runCmd(ctx, c.GitRoot, []string{"git", "remote", "get-url", "origin"}, true)
	if err == nil && originURL != "" {
		httpsURL := convertGitURLToHTTPS(originURL)
		_, _ = runCmd(ctx, "", []string{"ssh", c.Name, "cd ~/src/" + repo + " && git remote add origin " + shellQuote(httpsURL)}, true)
		if !opts.Quiet {
			_, _ = fmt.Fprintf(c.W, "- Set container origin to %s\n", httpsURL)
		}
	}

	// Copy .env if present.
	if _, err := os.Stat(".env"); err == nil {
		if !opts.Quiet {
			_, _ = fmt.Fprintln(c.W, "- sending .env into container ...")
		}
		_, _ = runCmd(ctx, "", []string{"scp", ".env", c.Name + ":/home/user/.env"}, false)
	}

	// Wait for Tailscale auth URL if needed.
	var tailscaleURL string
	if opts.Tailscale && opts.TailscaleAuthKey == "" {
		cmd := exec.CommandContext(ctx, "ssh", c.Name, "tail -f /tmp/tailscale_auth_url")
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			if err := cmd.Start(); err == nil {
				buf := make([]byte, 4096)
				n, _ := stdout.Read(buf)
				line := string(buf[:n])
				if strings.Contains(line, "https://") {
					tailscaleURL = strings.TrimSpace(line)
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}

	// Print summary.
	if !opts.Quiet {
		_, _ = fmt.Fprintln(c.W, "- Cool facts:")
		_, _ = fmt.Fprintln(c.W, "  > Remote access:")
		_, _ = fmt.Fprintf(c.W, "  >  SSH: `ssh %s`\n", c.Name)
		if vncPort != "" {
			_, _ = fmt.Fprintf(c.W, "  >  VNC: connect to localhost:%s with a VNC client or: `md vnc`\n", vncPort)
		} else {
			_, _ = fmt.Fprintln(c.W, "  >  Next time pass --display to have a virtual display")
		}
		if tailscaleURL != "" {
			_, _ = fmt.Fprintf(c.W, "  >  Tailscale: %s\n", tailscaleURL)
		}
		_, _ = fmt.Fprintf(c.W, "  > Host branch '%s' is mapped in the container as 'base'\n", c.Branch)
		_, _ = fmt.Fprintln(c.W, "  > See changes (in container): `git diff base`")
		_, _ = fmt.Fprintln(c.W, "  > See changes    (on host)  : `md diff`")
		_, _ = fmt.Fprintln(c.W, "  > Kill container (on host)  : `md kill`")
	}
	return nil
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
