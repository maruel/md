// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runCmd executes a command and returns (stdout, error).
// If capture is true, stdout/stderr are captured; otherwise they go to os.Stdout/os.Stderr.
func runCmd(args []string, capture bool) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	if capture {
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return "", cmd.Run()
}

// GitRootDir returns the git repository root for the given working directory.
func GitRootDir(wd string) (string, error) {
	out, err := runCmdDir(wd, []string{"git", "rev-parse", "--show-toplevel"}, true)
	if err != nil {
		return "", fmt.Errorf("not a git checkout directory: %s: %w", wd, err)
	}
	return out, nil
}

// GitCurrentBranch returns the current branch name for the given working directory.
func GitCurrentBranch(wd string) (string, error) {
	out, err := runCmdDir(wd, []string{"git", "branch", "--show-current"}, true)
	if err != nil || out == "" {
		return "", errors.New("check out a named branch")
	}
	return out, nil
}

// runCmdDir is like runCmd but runs the command in the given directory.
func runCmdDir(dir string, args []string, capture bool) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if capture {
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return "", cmd.Run()
}
