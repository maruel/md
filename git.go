// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// runCmd executes a command and returns (stdout, error).
// If capture is true, stdout/stderr are captured; otherwise they go to os.Stdout/os.Stderr.
// If dir is non-empty, the command runs in that directory.
func runCmd(ctx context.Context, dir string, args []string, capture bool) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	if capture {
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return "", cmd.Run()
}

// GitRootDir returns the git repository root for the given working directory.
func GitRootDir(ctx context.Context, wd string) (string, error) {
	out, err := runCmd(ctx, wd, []string{"git", "rev-parse", "--show-toplevel"}, true)
	if err != nil {
		return "", fmt.Errorf("not a git checkout directory: %s: %w", wd, err)
	}
	return out, nil
}

// GitCurrentBranch returns the current branch name for the given working directory.
func GitCurrentBranch(ctx context.Context, wd string) (string, error) {
	out, err := runCmd(ctx, wd, []string{"git", "branch", "--show-current"}, true)
	if err != nil || out == "" {
		return "", errors.New("check out a named branch")
	}
	return out, nil
}

// GitDefaultRemote returns the default remote for the given working directory.
// If there is exactly one remote, it is returned. Otherwise "origin" is used.
func GitDefaultRemote(ctx context.Context, wd string) (string, error) {
	out, err := runCmd(ctx, wd, []string{"git", "remote"}, true)
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

// GitDefaultBranch returns the default branch name (e.g. "main" or "master")
// for the given remote in the given working directory.
func GitDefaultBranch(ctx context.Context, wd, remote string) (string, error) {
	prefix := "refs/remotes/" + remote + "/"
	// Try symbolic-ref first (works when <remote>/HEAD is set).
	if out, err := runCmd(ctx, wd, []string{"git", "symbolic-ref", prefix + "HEAD"}, true); err == nil {
		if _, name, ok := strings.Cut(out, prefix); ok && name != "" {
			return name, nil
		}
	}
	// Fall back to checking common names.
	for _, name := range []string{"main", "master"} {
		if _, err := runCmd(ctx, wd, []string{"git", "rev-parse", "--verify", prefix + name}, true); err == nil {
			return name, nil
		}
	}
	return "", errors.New("could not determine default branch")
}
