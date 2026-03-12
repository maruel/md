// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// md (my devenv) manages isolated Docker development containers for AI coding
// agents.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caic-xyz/md"
	"github.com/caic-xyz/md/gitutil"
	"github.com/maruel/genai"
	"github.com/maruel/genai/providers"
	"golang.org/x/sync/errgroup"
)

// runtimeOverride is set by --runtime and applied in newClient/cmdList.
var runtimeOverride string

// controlMasterEnabled is set by --control-master and applied in newClient.
var controlMasterEnabled bool

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

// addVerboseFlag registers -v/-verbose on fs and returns the bool pointer.
func addVerboseFlag(fs *flag.FlagSet) *bool {
	v := fs.Bool("verbose", false, "Enable debug logging")
	fs.BoolVar(v, "v", false, "Enable debug logging")
	return v
}

// initLogging configures the default slog handler based on the verbose flag.
func initLogging(verbose bool) {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func mainImpl() error {
	// Pre-parse to support flags before the subcommand (e.g. "md -v start").
	pre := flag.NewFlagSet("md", flag.ContinueOnError)
	preVerbose := addVerboseFlag(pre)
	preRuntime := pre.String("runtime", "", "Container runtime: docker or podman (default: auto-detect)")
	preControlMaster := pre.Bool("control-master", false, "Enable SSH ControlMaster connection multiplexing")
	// Ignore errors: unknown flags here are subcommand flags, parsed later.
	_ = pre.Parse(os.Args[1:])
	initLogging(*preVerbose)
	runtimeOverride = *preRuntime
	controlMasterEnabled = *preControlMaster
	remaining := pre.Args()

	if len(remaining) == 0 {
		usage()
		return errors.New("no command specified")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := remaining[0]
	args := remaining[1:]
	switch cmd {
	case "start":
		return cmdStart(ctx, args)
	case "run":
		return cmdRun(ctx, args)
	case "list":
		return cmdList(ctx, args)
	case "ssh":
		return cmdSSH(args)
	case "purge", "kill":
		return cmdPurge(ctx, args)
	case "stop":
		return cmdStop(ctx, args)
	case "push":
		return cmdPush(ctx, args)
	case "pull":
		return cmdPull(ctx, args)
	case "diff":
		return cmdDiff(ctx, args)
	case "vnc":
		return cmdVNC(ctx, args)
	case "build-image":
		return cmdBuildImage(ctx, args)
	case "version":
		return cmdVersion(args)
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
	fmt.Fprintln(os.Stderr, "Global flags:")
	fmt.Fprintln(os.Stderr, "  -v, -verbose       Enable debug logging")
	fmt.Fprintln(os.Stderr, "  --runtime <name>   Container runtime: docker or podman (default: auto-detect)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  start       Pull base image, rebuild if needed, start container, open shell")
	fmt.Fprintln(os.Stderr, "  run <cmd>   Start a temporary container, run a command, then clean up")
	fmt.Fprintln(os.Stderr, "  list        List running md containers")
	fmt.Fprintln(os.Stderr, "  stop        Stop the container (preserves filesystem for later revival)")
	fmt.Fprintln(os.Stderr, "  purge       Stop and remove the container permanently")
	fmt.Fprintln(os.Stderr, "  push        Force-push current repo state into the running container")
	fmt.Fprintln(os.Stderr, "  pull        Pull changes from container back to local branch")
	fmt.Fprintln(os.Stderr, "  diff        Show differences between base and current changes")
	fmt.Fprintln(os.Stderr, "  vnc         Open VNC connection to the container")
	fmt.Fprintln(os.Stderr, "  build-image Build the base Docker image locally")
	fmt.Fprintln(os.Stderr, "  version     Print version information")
}

func newClient() (*md.Client, error) {
	c, err := md.New()
	if err != nil {
		return nil, err
	}
	if runtimeOverride != "" {
		c.Runtime = runtimeOverride
	}
	c.ControlMaster = controlMasterEnabled
	c.GithubToken = os.Getenv("GITHUB_TOKEN")
	if c.GithubToken == "" {
		if _, err2 := exec.LookPath("gh"); err2 == nil {
			if out, err2 := exec.Command("gh", "auth", "token").Output(); err2 == nil {
				c.GithubToken = strings.TrimSpace(string(out))
			} else {
				fmt.Fprintf(os.Stderr, "gh auth token: %v\n", err2)
			}
		}
	}
	c.TailscaleAPIKey = os.Getenv("TAILSCALE_API_KEY")
	return c, nil
}

// containerFlags holds the common flags for commands that target a container.
type containerFlags struct {
	image  *string
	tag    *string
	branch *string
	repo   *string
}

// addContainerFlags registers -b/-branch and -repo on the given FlagSet.
// When image is true, --image and --tag are also registered.
func addContainerFlags(fs *flag.FlagSet, image bool) *containerFlags {
	cf := &containerFlags{}
	if image {
		cf.image = fs.String("image", "", "Full base Docker image (default: "+md.DefaultBaseImage+":latest)")
		cf.tag = fs.String("tag", "", "Tag for the default base image ("+md.DefaultBaseImage+":<tag>)")
	}
	cf.branch = fs.String("branch", "", "Branch to use (default: current branch)")
	fs.StringVar(cf.branch, "b", "", "Branch to use (default: current branch)")
	cf.repo = fs.String("repo", "", "Path to git repository (default: current directory)")
	fs.StringVar(cf.repo, "r", "", "Path to git repository (default: current directory)")
	return cf
}

// baseImage returns the resolved base image from --image and --tag flags.
// --image takes precedence; --tag expands to DefaultBaseImage+":<tag>".
// Returns empty string when neither is set (caller should use DefaultBaseImage).
func (cf *containerFlags) baseImage() (string, error) {
	hasImage := cf.image != nil && *cf.image != ""
	hasTag := cf.tag != nil && *cf.tag != ""
	if hasImage && hasTag {
		return "", errors.New("--image and --tag are mutually exclusive")
	}
	if hasImage {
		return *cf.image, nil
	}
	if hasTag {
		return md.DefaultBaseImage + ":" + *cf.tag, nil
	}
	return "", nil
}

// findContainerAndRepo searches all containers for one that contains the
// repo identified by cf (defaults to cwd). Returns the container and the
// index of the matched repo within it. If cf.branch is set, it is used to
// disambiguate when multiple containers share the same git root.
func findContainerAndRepo(ctx context.Context, cf *containerFlags) (*md.Container, int, error) {
	c, err := newClient()
	if err != nil {
		return nil, 0, err
	}
	searchPath := ""
	if cf.repo != nil && *cf.repo != "" {
		searchPath = *cf.repo
	} else {
		searchPath, err = os.Getwd()
		if err != nil {
			return nil, 0, err
		}
	}
	gitRoot, err := gitutil.RootDir(ctx, searchPath)
	if err != nil {
		return nil, 0, fmt.Errorf("not in a git repository: %w", err)
	}
	branch := ""
	if cf.branch != nil {
		branch = *cf.branch
	}
	// If no branch was specified, use the current local branch as the default
	// disambiguator so that two containers on different branches of the same
	// repo are resolved automatically.
	if branch == "" {
		branch, _ = gitutil.RunGit(ctx, gitRoot, "branch", "--show-current")
	}
	containers, err := c.List(ctx)
	if err != nil {
		return nil, 0, err
	}
	var matched []*md.Container
	var matchedIdx []int
	for _, ct := range containers {
		for i, repo := range ct.Repos {
			if repo.GitRoot == gitRoot && (branch == "" || repo.Branch == branch) {
				matched = append(matched, ct)
				matchedIdx = append(matchedIdx, i)
				break
			}
		}
	}
	switch len(matched) {
	case 0:
		return nil, 0, fmt.Errorf("no container found for %s", gitRoot)
	case 1:
		return matched[0], matchedIdx[0], nil
	default:
		names := make([]string, len(matched))
		for i, ct := range matched {
			names[i] = ct.Name
		}
		return nil, 0, fmt.Errorf("multiple containers match %s: %s; use -branch to disambiguate", gitRoot, strings.Join(names, ", "))
	}
}

// newContainer resolves a Container from flags. extraRepoSpecs holds
// additional "path[:branch]" strings (e.g. from -extra-repo in cmdStart).
func newContainer(ctx context.Context, cf *containerFlags, extraRepoSpecs []string) (*md.Container, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}
	// Resolve primary repo.
	var repos []md.Repo
	primaryPath := ""
	if cf.repo != nil && *cf.repo != "" {
		primaryPath = *cf.repo
	} else {
		primaryPath, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	gitRoot, gitErr := gitutil.RootDir(ctx, primaryPath)
	if gitErr == nil {
		if err := os.Chdir(gitRoot); err != nil {
			return nil, err
		}
		var branch string
		if cf.branch != nil && *cf.branch != "" {
			branch = *cf.branch
		} else {
			branch, err = gitutil.CurrentBranch(ctx, gitRoot)
			if err != nil {
				return nil, err
			}
		}
		repos = append(repos, md.Repo{GitRoot: gitRoot, Branch: branch})
	} else if cf.repo != nil && *cf.repo != "" {
		// Explicit -repo that isn't a git root is an error.
		return nil, fmt.Errorf("repo %s: %w", primaryPath, gitErr)
	}
	// Not in a git repo and no explicit -repo: create a no-repo container.
	// Resolve extra repos.
	for _, spec := range extraRepoSpecs {
		path, branch, _ := strings.Cut(spec, ":")
		gitRoot, err := gitutil.RootDir(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("extra repo %s: %w", path, err)
		}
		if branch == "" {
			branch, err = gitutil.CurrentBranch(ctx, gitRoot)
			if err != nil {
				return nil, fmt.Errorf("extra repo %s: %w", path, err)
			}
		}
		repos = append(repos, md.Repo{GitRoot: gitRoot, Branch: branch})
	}
	if len(repos) > 1000 {
		return nil, fmt.Errorf("too many repositories: %d (max 1000)", len(repos))
	}
	return c.Container(repos...), nil
}

func cmdStart(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	display := fs.Bool("display", false, "Enable X11/VNC display")
	fs.BoolVar(display, "d", false, "Enable X11/VNC display")
	tailscale := fs.Bool("tailscale", false, "Enable Tailscale networking")
	usb := fs.Bool("usb", false, "Pass through USB devices (/dev/bus/usb)")
	cf := addContainerFlags(fs, true)
	extraRepos := &stringSlice{}
	fs.Var(extraRepos, "extra-repo", "Additional git repository path[:branch] to map; may be repeated")
	fs.Var(extraRepos, "e", "Additional git repository path[:branch] to map; may be repeated")
	noSSH := fs.Bool("no-ssh", false, "Don't SSH into the container after starting")
	quiet := fs.Bool("q", false, "Suppress informational messages")
	labels := &stringSlice{}
	fs.Var(labels, "label", "Set Docker container label (key=value); can be repeated")
	fs.Var(labels, "l", "Set Docker container label (key=value); can be repeated")
	cacheSpecs := &stringSlice{}
	fs.Var(cacheSpecs, "cache", "Add a cache: well-known name ("+wellKnownCacheList()+") or host:container[:ro]; may be repeated")
	noCacheSpecs := &stringSlice{}
	fs.Var(noCacheSpecs, "no-cache", "Exclude a default well-known cache by name; may be repeated")
	noCaches := fs.Bool("no-caches", false, "Disable all default caches")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)

	ct, err := newContainer(ctx, cf, extraRepos.values)
	if err != nil {
		return err
	}
	baseImage, err := cf.baseImage()
	if err != nil {
		return err
	}
	caches, err := resolveCaches(cacheSpecs.values, noCacheSpecs.values, *noCaches)
	if err != nil {
		return err
	}
	opts := md.StartOpts{
		BaseImage:        baseImage,
		Display:          *display,
		Tailscale:        *tailscale,
		USB:              *usb,
		TailscaleAuthKey: os.Getenv("TAILSCALE_AUTHKEY"),
		Caches:           caches,
		Labels:           labels.values,
		Quiet:            *quiet,
		AgentPaths:       slices.Collect(maps.Values(md.HarnessMounts)),
	}
	if err := ct.Launch(ctx, &opts); err != nil {
		return err
	}
	result, err := ct.Connect(ctx, &opts)
	if err != nil {
		return err
	}
	if !*quiet {
		printStartSummary(ct, result)
	}
	if !*noSSH {
		sshArgs := ct.SSHCommand(ct.Name)
		cmd := exec.CommandContext(ctx, sshArgs[0], sshArgs[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return nil
}

func printStartSummary(ct *md.Container, r *md.StartResult) {
	fmt.Println("- Cool facts:")
	fmt.Println("  > Remote access:")
	fmt.Printf("  >  SSH: `ssh %s`\n", ct.Name)
	if ct.VNCPort != 0 {
		fmt.Printf("  >  VNC: connect to localhost:%d with a VNC client or: `md vnc`\n", ct.VNCPort)
	} else {
		fmt.Println("  >  Next time pass --display to have a virtual display")
	}
	if r.TailscaleFQDN != "" {
		fmt.Printf("  >  Tailscale FQDN: %s\n", r.TailscaleFQDN)
	}
	if r.TailscaleAuthURL != "" {
		fmt.Printf("  >  Tailscale auth: %s\n", r.TailscaleAuthURL)
	}
	if len(ct.Repos) > 0 {
		fmt.Printf("  > Host branch '%s' is mapped in the container as 'base'\n", ct.Repos[0].Branch)
		fmt.Println("  > See changes (in container): `git diff base`")
		fmt.Println("  > See changes    (on host)  : `md diff`")
	}
	fmt.Println("  > Stop container (on host)  : `md stop`")
	fmt.Println("  > Purge container (on host) : `md purge`")
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, true)
	cacheSpecs := &stringSlice{}
	fs.Var(cacheSpecs, "cache", "Add a cache: well-known name ("+wellKnownCacheList()+") or host:container[:ro]; may be repeated")
	noCacheSpecs := &stringSlice{}
	fs.Var(noCacheSpecs, "no-cache", "Exclude a default well-known cache by name; may be repeated")
	noCaches := fs.Bool("no-caches", false, "Disable all default caches")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	extra := fs.Args()
	if len(extra) == 0 {
		return errors.New("no command specified")
	}
	ct, err := newContainer(ctx, cf, nil)
	if err != nil {
		return err
	}
	baseImage, err := cf.baseImage()
	if err != nil {
		return err
	}
	caches, err := resolveCaches(cacheSpecs.values, noCacheSpecs.values, *noCaches)
	if err != nil {
		return err
	}
	exitCode, err := ct.Run(ctx, baseImage, extra, caches)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return &exitCodeError{code: exitCode}
	}
	return nil
}

// containerListEntry is the JSON representation of a container in `md list --json`.
type containerListEntry struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Uptime    string `json:"uptime"`
	Display   bool   `json:"display,omitempty"`
	Tailscale bool   `json:"tailscale,omitempty"`
	FQDN      string `json:"fqdn,omitempty"`
	USB       bool   `json:"usb,omitempty"`
}

func cmdList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	jsonOut := fs.Bool("json", false, "Output in JSON format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	c, err := md.New()
	if err != nil {
		return err
	}
	if runtimeOverride != "" {
		c.Runtime = runtimeOverride
	}
	containers, err := c.List(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		entries := make([]containerListEntry, len(containers))
		for i, ct := range containers {
			entries[i] = containerListEntry{
				Name:      ct.Name,
				State:     ct.State,
				Uptime:    time.Since(ct.CreatedAt).Truncate(time.Second).String(),
				Display:   ct.Display,
				Tailscale: ct.Tailscale,
				USB:       ct.USB,
			}
			if ct.Tailscale {
				entries[i].FQDN = ct.TailscaleFQDN(ctx)
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(containers) == 0 {
		fmt.Println("No running md containers")
		return nil
	}
	fmt.Printf("%-30s %-10s %-12s %s\n", "Container", "Status", "Uptime", "Features")
	fmt.Println(strings.Repeat("-", 80))
	for _, ct := range containers {
		var features []string
		if ct.Display {
			features = append(features, "display")
		}
		if ct.Tailscale {
			if fqdn := ct.TailscaleFQDN(ctx); fqdn != "" {
				features = append(features, "tailscale:"+fqdn)
			} else {
				features = append(features, "tailscale")
			}
		}
		if ct.USB {
			features = append(features, "usb")
		}
		fmt.Printf("%-30s %-10s %-12s %s\n", ct.Name, ct.State, time.Since(ct.CreatedAt).Truncate(time.Second), strings.Join(features, ","))
	}
	return nil
}

func cmdSSH(args []string) error {
	if err := noArgs("ssh", args); err != nil {
		return err
	}
	return errors.New("use 'ssh md-<repo>-<branch>' directly")
}

func cmdStop(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	if name := fs.Arg(0); name != "" {
		c, err := newClient()
		if err != nil {
			return err
		}
		containers, err := c.List(ctx)
		if err != nil {
			return err
		}
		for _, ct := range containers {
			if ct.Name == name {
				return ct.Stop(ctx)
			}
		}
		return fmt.Errorf("no container named %s", name)
	}
	ct, _, err := findContainerAndRepo(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Stop(ctx)
}

func cmdPurge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("purge", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	// A bare container name may be passed as a positional argument for
	// repo-less containers, which have no git root to search by.
	if name := fs.Arg(0); name != "" {
		c, err := newClient()
		if err != nil {
			return err
		}
		containers, err := c.List(ctx)
		if err != nil {
			return err
		}
		for _, ct := range containers {
			if ct.Name == name {
				return ct.Purge(ctx)
			}
		}
		return fmt.Errorf("no container named %s", name)
	}
	ct, _, err := findContainerAndRepo(ctx, cf)
	if err != nil {
		return err
	}
	return ct.Purge(ctx)
}

func cmdPush(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	all := fs.Bool("all", false, "Operate on all repos, not just the current one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	ct, repoIdx, err := findContainerAndRepo(ctx, cf)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	printBackup := func(i int, backup string) {
		repoName := ct.Repos[i].Name()
		mu.Lock()
		fmt.Printf("- %s: previous state saved as git branch: %s\n", repoName, backup)
		mu.Unlock()
	}
	if !*all {
		backup, err := ct.Push(ctx, repoIdx)
		if err != nil {
			return err
		}
		printBackup(repoIdx, backup)
		return nil
	}
	eg, ctx2 := errgroup.WithContext(ctx)
	for i := range ct.Repos {
		eg.Go(func() error {
			backup, err := ct.Push(ctx2, i)
			if err != nil {
				return err
			}
			if backup != "" {
				printBackup(i, backup)
			}
			return nil
		})
	}
	return eg.Wait()
}

func cmdPull(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	all := fs.Bool("all", false, "Operate on all repos, not just the current one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	ct, repoIdx, err := findContainerAndRepo(ctx, cf)
	if err != nil {
		return err
	}
	var p genai.Provider
	if providerName := os.Getenv("ASK_PROVIDER"); providerName != "" {
		var err error
		p, err = newProvider(ctx, providerName, os.Getenv("ASK_MODEL"))
		if err != nil {
			slog.WarnContext(ctx, "failed to initialize provider", "err", err)
		}
	}
	if !*all {
		return ct.Pull(ctx, repoIdx, p)
	}
	eg, ctx2 := errgroup.WithContext(ctx)
	for i := range ct.Repos {
		eg.Go(func() error {
			return ct.Pull(ctx2, i, p)
		})
	}
	return eg.Wait()
}

func cmdDiff(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	all := fs.Bool("all", false, "Operate on all repos, not just the current one")
	// Separate md-own flags from git passthrough args.
	// Flags defined on fs go to mdArgs; everything else (e.g. --stat,
	// --name-only) is forwarded to git diff. "--" explicitly ends md flag
	// parsing; everything after goes to git.
	var mdArgs, gitArgs []string
	for i := 0; i < len(args); i++ {
		if a := args[i]; a != "--" && strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			// Handle -flag=value form.
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if f := fs.Lookup(name); f != nil {
				mdArgs = append(mdArgs, a)
				// Consume the next arg as value for non-bool flags without inline =.
				type isBool interface{ IsBoolFlag() bool }
				if _, isBoolFlag := f.Value.(isBool); !isBoolFlag && !strings.Contains(a, "=") {
					i++
					if i < len(args) {
						mdArgs = append(mdArgs, args[i])
					}
				}
				continue
			}
		}
		gitArgs = append(gitArgs, args[i:]...)
		break
	}
	if err := fs.Parse(mdArgs); err != nil {
		return err
	}
	initLogging(*verbose)
	ct, repoIdx, err := findContainerAndRepo(ctx, cf)
	if err != nil {
		return err
	}
	indices := []int{repoIdx}
	if *all {
		indices = make([]int, len(ct.Repos))
		for i := range ct.Repos {
			indices[i] = i
		}
	}
	for _, i := range indices {
		if *all && len(ct.Repos) > 1 {
			fmt.Printf("=== %s ===\n", filepath.Base(ct.Repos[i].GitRoot))
		}
		if err := ct.Diff(ctx, i, os.Stdout, os.Stderr, gitArgs); err != nil {
			return err
		}
	}
	return nil
}

func cmdVNC(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vnc", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	cf := addContainerFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	ct, err := newContainer(ctx, cf, nil)
	if err != nil {
		return err
	}
	vncPort, err := ct.GetHostPort(ctx, "5901/tcp")
	if err != nil {
		return err
	}
	if vncPort == 0 {
		return fmt.Errorf("VNC port not found for %s. Did you start it with --display?\nTo enable display, run:\n  md purge\n  md start --display", ct.Name)
	}
	vncURL := fmt.Sprintf("vnc://127.0.0.1:%d", vncPort)
	fmt.Printf("VNC connection: %s\n", vncURL)

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", vncURL).Run()
	case "linux":
		if err := exec.Command("xdg-open", vncURL).Run(); err == nil {
			return nil
		}
		if err := exec.Command("vncviewer", fmt.Sprintf("127.0.0.1:%d", vncPort)).Run(); err == nil {
			return nil
		}
		fmt.Println("\nNo VNC client found. Connect manually:")
		fmt.Println("  Address: 127.0.0.1")
		fmt.Printf("  Port: %d\n", vncPort)
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

func cmdBuildImage(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build-image", flag.ExitOnError)
	verbose := addVerboseFlag(fs)
	serialSetup := fs.Bool("serial-setup", false, "Run setup steps serially instead of in parallel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	initLogging(*verbose)
	c, err := newClient()
	if err != nil {
		return err
	}
	return c.BuildImage(ctx, *serialSetup)
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("version: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		_, err := fmt.Println("md (unknown version; no build info)")
		return err
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	version := info.Main.Version
	if version == "" || version == "(devel)" {
		// No module version stamped; build from VCS info.
		rev := settings["vcs.revision"]
		if rev == "" {
			_, err := fmt.Println("md (unknown version; no VCS info)")
			return err
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		version = rev
		if settings["vcs.modified"] == "true" {
			version += "-dirty"
		}
		if t := settings["vcs.time"]; t != "" {
			version += " " + t
		}
	}
	_, err := fmt.Printf("md %s\n", version)
	return err
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

// wellKnownCacheList returns a sorted comma-separated list of well-known cache
// names for use in flag help text.
func wellKnownCacheList() string {
	names := make([]string, 0, len(md.WellKnownCaches))
	for k := range md.WellKnownCaches {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// resolveCaches builds the list of CacheMounts to bake into the image.
//
// By default all well-known caches are included (sorted by name).
// excluded names remove specific well-known caches from that default set.
// noAll disables all defaults; only caches from customSpecs are included.
// customSpecs accepts well-known names (to re-add an excluded cache when used
// with noAll) or "host:container[:ro]" custom paths.
func resolveCaches(customSpecs, excluded []string, noAll bool) ([]md.CacheMount, error) {
	result := make([]md.CacheMount, 0)

	if !noAll {
		// Validate excluded names before building the result.
		for _, n := range excluded {
			if _, ok := md.WellKnownCaches[n]; !ok {
				return nil, fmt.Errorf("unknown --no-cache name %q; valid names: %s", n, wellKnownCacheList())
			}
		}
		excl := make(map[string]struct{}, len(excluded))
		for _, n := range excluded {
			excl[n] = struct{}{}
		}
		names := make([]string, 0, len(md.WellKnownCaches))
		for k := range md.WellKnownCaches {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, excluded := excl[name]; !excluded {
				result = append(result, md.WellKnownCaches[name]...)
			}
		}
	}

	// Track mount names already present to avoid duplicates from --cache.
	seen := make(map[string]struct{}, len(result))
	for _, m := range result {
		seen[m.Name] = struct{}{}
	}

	// Process --cache specs: well-known names or custom host:container[:ro].
	for _, spec := range customSpecs {
		if mounts, ok := md.WellKnownCaches[spec]; ok {
			for _, m := range mounts {
				if _, ok := seen[m.Name]; !ok {
					result = append(result, m)
					seen[m.Name] = struct{}{}
				}
			}
			continue
		}
		// Custom spec: host:container or host:container:ro.
		parts := strings.SplitN(spec, ":", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid --cache %q: use a well-known name (%s) or host:container[:ro]",
				spec, wellKnownCacheList())
		}
		cm := md.CacheMount{
			Name:          filepath.Base(parts[0]),
			HostPath:      parts[0],
			ContainerPath: parts[1],
		}
		if len(parts) == 3 {
			if parts[2] != "ro" {
				return nil, fmt.Errorf("invalid --cache %q: only ':ro' modifier is supported", spec)
			}
			cm.ReadOnly = true
		}
		result = append(result, cm)
	}
	return result, nil
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
