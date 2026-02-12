// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// md (my devenv) manages isolated Docker development containers for AI coding
// agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/maruel/md"
)

func main() {
	if err := mainImpl(); err != nil {
		var ec *exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		fmt.Fprintf(os.Stderr, "md: %v\n", err)
		os.Exit(1)
	}
}

func mainImpl() error {
	if len(os.Args) < 2 {
		usage()
		return errors.New("no command specified")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "start":
		return cmdStart(ctx, args)
	case "run":
		return cmdRun(ctx, args)
	case "list":
		return cmdList(ctx, args)
	case "ssh":
		return cmdSSH(args)
	case "kill":
		return cmdKill(ctx, args)
	case "push":
		return cmdPush(ctx, args)
	case "pull":
		return cmdPull(ctx, args)
	case "diff":
		return cmdDiff(ctx, args)
	case "vnc":
		return cmdVNC(ctx, args)
	case "build-base":
		return cmdBuildBase(ctx, args)
	case "help", "-h", "-help", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "md (my devenv): local development environment with git clone")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  start       Pull base image, rebuild if needed, start container, open shell")
	fmt.Fprintln(os.Stderr, "  run <cmd>   Start a temporary container, run a command, then clean up")
	fmt.Fprintln(os.Stderr, "  list        List running md containers")
	fmt.Fprintln(os.Stderr, "  kill        Stop and remove the container")
	fmt.Fprintln(os.Stderr, "  push        Force-push current repo state into the running container")
	fmt.Fprintln(os.Stderr, "  pull        Pull changes from container back to local branch")
	fmt.Fprintln(os.Stderr, "  diff        Show differences between base and current changes")
	fmt.Fprintln(os.Stderr, "  vnc         Open VNC connection to the container")
	fmt.Fprintln(os.Stderr, "  build-base  Build the base Docker image locally")
}

func newClient(tagFlag *string) (*md.Client, error) {
	var tag string
	if tagFlag != nil {
		tag = *tagFlag
	}
	c, err := md.New(tag)
	if err != nil {
		return nil, err
	}
	if err := c.Prepare(); err != nil {
		return nil, err
	}
	return c, nil
}

// containerFlags holds the common flags for commands that target a container.
type containerFlags struct {
	tag    *string
	branch *string
	repo   *string
}

// addContainerFlags registers -b/-branch and -repo on the given FlagSet.
func addContainerFlags(fs *flag.FlagSet, tag bool) *containerFlags {
	cf := &containerFlags{}
	if tag {
		cf.tag = fs.String("tag", "", "Tag for the base image")
	}
	cf.branch = fs.String("branch", "", "Branch to use (default: current branch)")
	fs.StringVar(cf.branch, "b", "", "Branch to use (default: current branch)")
	cf.repo = fs.String("repo", "", "Path to git repository (default: current directory)")
	fs.StringVar(cf.repo, "r", "", "Path to git repository (default: current directory)")
	return cf
}

func newContainer(ctx context.Context, cf *containerFlags) (*md.Container, error) {
	c, err := newClient(cf.tag)
	if err != nil {
		return nil, err
	}
	repo := ""
	if cf.repo != nil && *cf.repo != "" {
		repo = *cf.repo
	} else {
		repo, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	gitRoot, err := md.GitRootDir(ctx, repo)
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(gitRoot); err != nil {
		return nil, err
	}
	var branch string
	if cf.branch != nil && *cf.branch != "" {
		branch = *cf.branch
	} else {
		branch, err = md.GitCurrentBranch(ctx, gitRoot)
		if err != nil {
			return nil, err
		}
	}
	return c.Container(gitRoot, branch), nil
}

func cmdStart(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	display := fs.Bool("display", false, "Enable X11/VNC display")
	fs.BoolVar(display, "d", false, "Enable X11/VNC display")
	tailscale := fs.Bool("tailscale", false, "Enable Tailscale networking")
	cf := addContainerFlags(fs, true)
	noSSH := fs.Bool("no-ssh", false, "Don't SSH into the container after starting")
	labels := &stringSlice{}
	fs.Var(labels, "label", "Set Docker container label (key=value); can be repeated")
	fs.Var(labels, "l", "Set Docker container label (key=value); can be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	opts := md.StartOpts{
		Display:   *display,
		Tailscale: *tailscale,
		Labels:    labels.values,
		NoSSH:     *noSSH,
	}
	if err := ct.Start(ctx, &opts); err != nil {
		return err
	}
	return nil
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cf := addContainerFlags(fs, true)
	if err := fs.Parse(args); err != nil {
		return err
	}
	extra := fs.Args()
	if len(extra) == 0 {
		return errors.New("no command specified")
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	exitCode, err := ct.Run(ctx, extra)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return &exitCodeError{code: exitCode}
	}
	return nil
}

func cmdList(ctx context.Context, args []string) error {
	if err := noArgs("list", args); err != nil {
		return err
	}
	c, err := md.New("")
	if err != nil {
		return err
	}
	containers, err := c.List(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		fmt.Println("No running md containers")
		return nil
	}
	fmt.Printf("%-50s %-15s %-20s\n", "Container", "Status", "Uptime")
	fmt.Println(strings.Repeat("-", 85))
	for _, ct := range containers {
		fmt.Printf("%-50s %-15s %-20s\n", ct.Name, ct.State, time.Since(ct.CreatedAt).Truncate(time.Second))
	}
	return nil
}

func cmdSSH(args []string) error {
	if err := noArgs("ssh", args); err != nil {
		return err
	}
	return errors.New("use 'ssh md-<repo>-<branch>' directly")
}

func cmdKill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("kill", flag.ExitOnError)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Kill(ctx)
}

func cmdPush(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Push(ctx)
}

func cmdPull(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Pull(ctx)
}

func cmdDiff(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Diff(ctx, os.Stdout, os.Stderr, fs.Args())
}

func cmdVNC(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vnc", flag.ExitOnError)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ct, err := newContainer(ctx, cf)
	if err != nil {
		return err
	}
	vncPort, err := ct.GetHostPort(ctx, "5901/tcp")
	if err != nil {
		return err
	}
	if vncPort == "" {
		return fmt.Errorf("VNC port not found for %s. Did you start it with --display?\nTo enable display, run:\n  md kill\n  md start --display", ct.Name)
	}
	vncURL := "vnc://127.0.0.1:" + vncPort
	fmt.Printf("VNC connection: %s\n", vncURL)

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", vncURL).Run()
	case "linux":
		if err := exec.Command("xdg-open", vncURL).Run(); err == nil {
			return nil
		}
		if err := exec.Command("vncviewer", "127.0.0.1:"+vncPort).Run(); err == nil {
			return nil
		}
		fmt.Println("\nNo VNC client found. Connect manually:")
		fmt.Println("  Address: 127.0.0.1")
		fmt.Printf("  Port: %s\n", vncPort)
		fmt.Println("\nInstall a VNC client:")
		fmt.Println("  Ubuntu/Debian: sudo apt install tigervnc-viewer")
		fmt.Println("  Fedora/RHEL: sudo dnf install tigervnc")
		fmt.Println("  Or use any remote desktop client (Remmina, RealVNC, TigerVNC, etc.)")
		return nil
	case "windows":
		return exec.Command("cmd", "/c", "start", vncURL).Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func cmdBuildBase(ctx context.Context, args []string) error {
	if err := noArgs("build-base", args); err != nil {
		return err
	}
	c, err := newClient(nil)
	if err != nil {
		return err
	}
	return c.BuildBase(ctx)
}

func noArgs(cmd string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s: unexpected arguments: %s", cmd, strings.Join(args, " "))
	}
	return nil
}

// exitCodeError is returned when a subcommand needs to exit with a specific
// non-zero code without printing an error message.
type exitCodeError struct {
	code int
}

func (e *exitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

// stringSlice implements flag.Value for repeatable string flags.
type stringSlice struct {
	values []string
}

func (s *stringSlice) String() string {
	return strings.Join(s.values, ", ")
}

func (s *stringSlice) Set(v string) error {
	s.values = append(s.values, v)
	return nil
}
