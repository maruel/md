// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// Package gitutil provides git utility functions for repository introspection,
// branch management, and pushing.
package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// RunGit executes a git command in dir and returns captured stdout.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

// RootDir returns the git repository root for the given working directory.
func RootDir(ctx context.Context, wd string) (string, error) {
	out, err := RunGit(ctx, wd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git checkout directory: %s: %w", wd, err)
	}
	return out, nil
}

// CurrentBranch returns the current branch name for the given working
// directory.
func CurrentBranch(ctx context.Context, wd string) (string, error) {
	out, err := RunGit(ctx, wd, "branch", "--show-current")
	if err != nil || out == "" {
		return "", errors.New("check out a named branch")
	}
	return out, nil
}

// DefaultRemote returns the default remote for the given working directory.
// If there is exactly one remote, it is returned. Otherwise "origin" is used.
func DefaultRemote(ctx context.Context, wd string) (string, error) {
	out, err := RunGit(ctx, wd, "remote")
	if err != nil || out == "" {
		return "", errors.New("no git remotes configured")
	}
	lines := strings.Split(out, "\n")
	if len(lines) == 1 {
		return lines[0], nil
	}
	if slices.Contains(lines, "origin") {
		return "origin", nil
	}
	return "", fmt.Errorf("multiple remotes and no %q", "origin")
}

// DefaultBranch returns the default branch name (e.g. "main" or "master")
// for the given remote in the given working directory.
func DefaultBranch(ctx context.Context, wd, remote string) (string, error) {
	prefix := "refs/remotes/" + remote + "/"
	// Try symbolic-ref first (works when <remote>/HEAD is set).
	if out, err := RunGit(ctx, wd, "symbolic-ref", prefix+"HEAD"); err == nil {
		if _, name, ok := strings.Cut(out, prefix); ok && name != "" {
			return name, nil
		}
	}
	// Fall back to checking common names.
	for _, name := range []string{"main", "master"} {
		if _, err := RunGit(ctx, wd, "rev-parse", "--verify", prefix+name); err == nil {
			return name, nil
		}
	}
	return "", errors.New("could not determine default branch")
}

// MergeBase returns the merge-base between HEAD and baseRef, falling back to
// baseRef itself if merge-base fails (e.g. unrelated histories).
func MergeBase(ctx context.Context, dir, baseRef string) string {
	if mb, err := RunGit(ctx, dir, "merge-base", "HEAD", baseRef); err == nil && mb != "" {
		return mb
	}
	return baseRef
}

// Fetch fetches the latest refs from origin.
func Fetch(ctx context.Context, dir string) error {
	slog.Info("git fetch", "dir", dir)
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch origin: %w: %s", err, stderr.String())
	}
	return nil
}

// CreateBranch creates a new branch from startPoint and checks it out.
func CreateBranch(ctx context.Context, dir, name, startPoint string) error {
	slog.Info("git create branch", "branch", name, "startPoint", startPoint)
	cmd := exec.CommandContext(ctx, "git", "checkout", "-b", name, startPoint)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git checkout -b %s: %w: %s", name, err, stderr.String())
	}
	return nil
}

// CheckoutBranch switches to an existing branch.
func CheckoutBranch(ctx context.Context, dir, name string) error {
	slog.Info("git checkout", "branch", name)
	cmd := exec.CommandContext(ctx, "git", "checkout", name)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", name, err, stderr.String())
	}
	return nil
}

// RemoteOriginURL returns the URL of the "origin" remote, or "" if
// unavailable.
func RemoteOriginURL(ctx context.Context, dir string) string {
	out, err := RunGit(ctx, dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return out
}

// RemoteToHTTPS converts a git remote URL to an HTTPS browse URL.
// SSH (git@host:owner/repo.git), ssh:// and https:// with .git suffix are
// normalised. Unrecognised formats are returned as-is.
func RemoteToHTTPS(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// git@host:owner/repo.git → https://host/owner/repo
	if after, ok := strings.CutPrefix(raw, "git@"); ok {
		if i := strings.IndexByte(after, ':'); i > 0 {
			host := after[:i]
			path := strings.TrimSuffix(after[i+1:], ".git")
			return "https://" + host + "/" + path
		}
	}
	// ssh://git@host/owner/repo.git → https://host/owner/repo
	if after, ok := strings.CutPrefix(raw, "ssh://"); ok {
		// Strip user@ if present.
		if i := strings.IndexByte(after, '@'); i >= 0 {
			after = after[i+1:]
		}
		return "https://" + strings.TrimSuffix(after, ".git")
	}
	// https://host/owner/repo.git → strip .git
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return strings.TrimSuffix(raw, ".git")
	}
	return raw
}

// PushRef pushes a local ref to the origin remote as the given branch.
// ref can be a remote-tracking ref (e.g. "container/branch"), a branch
// name, or any valid git ref. When force is true, --force is passed.
func PushRef(ctx context.Context, dir, ref, branch string, force bool) error {
	slog.Info("git push", "ref", ref, "branch", branch, "force", force)
	args := []string{"push"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "origin", ref+":refs/heads/"+branch)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push origin %s:%s: %w: %s", ref, branch, err, stderr.String())
	}
	return nil
}

// SquashOnto creates a single squash commit of sourceRef's tree on top of
// origin/<targetBranch> and pushes it. Uses plumbing commands only — no
// working-tree checkout needed. The push is non-force so it fails with a
// non-fast-forward error if origin/<targetBranch> has advanced since fetch.
func SquashOnto(ctx context.Context, dir, sourceRef, targetBranch, message string) error {
	slog.Info("squash onto", "sourceRef", sourceRef, "targetBranch", targetBranch)

	// 1. Fetch so origin/<targetBranch> is up to date.
	if err := Fetch(ctx, dir); err != nil {
		return err
	}

	// 2. Create the squash commit: sourceRef's tree, parented on origin/<targetBranch>.
	target := "origin/" + targetBranch
	commitTreeArgs := []string{"commit-tree", "-p", target, "-m", message, sourceRef + "^{tree}"}
	cmd := exec.CommandContext(ctx, "git", commitTreeArgs...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git commit-tree: %w: %s", err, stderr.String())
	}
	newCommit := strings.TrimSpace(string(out))

	// 3. Push the new commit to origin/<targetBranch> (non-force).
	return PushRef(ctx, dir, newCommit, targetBranch, false)
}

// RevParse resolves a git ref to its full SHA-1 hash.
func RevParse(ctx context.Context, dir, ref string) (string, error) {
	return RunGit(ctx, dir, "rev-parse", "--verify", ref)
}

// IsReachable reports whether commit is an ancestor of (or equal to) any ref
// in refs/heads/ or refs/remotes/origin/. Container remote-tracking refs
// (refs/remotes/<container>/*) are excluded by construction.
func IsReachable(ctx context.Context, dir, commit string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "for-each-ref",
		"--contains", commit,
		"--format=%(refname)",
		"refs/heads/",
		"refs/remotes/origin/",
	)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git for-each-ref --contains %s: %w: %s", commit, err, stderr.String())
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// CreateBranchAt creates a local branch pointing at commit without checking it
// out. This does not touch the working tree or index.
func CreateBranchAt(ctx context.Context, dir, name, commit string) error {
	slog.Info("git create branch at", "branch", name, "commit", commit)
	cmd := exec.CommandContext(ctx, "git", "branch", name, commit)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git branch %s %s: %w: %s", name, commit, err, stderr.String())
	}
	return nil
}

// DiscoverRepos recursively walks root up to maxDepth levels, returning
// absolute paths of directories containing a .git subdirectory. Hidden
// directories (prefix ".") are skipped. Recursion stops once .git is found.
func DiscoverRepos(root string, maxDepth int) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var repos []string
	err = discoverRepos(root, maxDepth, &repos)
	return repos, err
}

func discoverRepos(dir string, depth int, repos *[]string) error {
	if depth < 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	// Check if this directory contains .git.
	for _, e := range entries {
		if e.Name() == ".git" {
			*repos = append(*repos, dir)
			return nil // Don't recurse into repos.
		}
	}
	// Recurse into subdirectories.
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := discoverRepos(filepath.Join(dir, e.Name()), depth-1, repos); err != nil {
			// Skip directories we can't read.
			continue
		}
	}
	return nil
}
