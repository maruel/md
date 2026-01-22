#!/usr/bin/env python3
# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by the Apache v2 license that can be found in the
# LICENSE file.

import argparse
import inspect
import json
import os
import platform
import re
import shlex
import shutil
import subprocess
import sys
import time
import traceback
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = str(Path(__file__).parent.resolve())

# Global constant for agent configuration paths
AGENT_CONFIG = {
    # Home directory paths (mounted as-is)
    "home_paths": [
        ".amp",
        ".android",
        ".codex",
        ".claude",
        ".gemini",
        ".pi",
        ".qwen",
    ],
    # XDG config paths (mounted to .config/)
    "xdg_config_paths": [
        "agents",
        "amp",
        "goose",
        "md",
        "opencode",
    ],
    # Local share paths (mounted to .local/share/)
    "local_share_paths": [
        "amp",
        "goose",
        "opencode",
    ],
    # Local state paths (mounted to .local/state/)
    "local_state_paths": [
        "opencode",
    ],
}


def argument(*name_or_flags, **kwargs):
    """Decorator to add arguments to a command."""

    def _decorator(func):
        if not hasattr(func, "arguments"):
            func.arguments = []
        func.arguments.append((name_or_flags, kwargs))
        return func

    return _decorator


def accepts_extra_args(func):
    """Decorator to mark a command as accepting extra arguments."""
    func.accepts_extra_args = True
    return func


def run_cmd(cmd, check=True, **kwargs):
    """Execute shell command, return (stdout, returncode) tuple."""
    if kwargs.get("capture_output", False) or "stdout" in kwargs:
        kwargs.setdefault("text", True)
    result = subprocess.run(cmd, check=check, **kwargs)
    return (result.stdout.strip() if result.stdout else "", result.returncode)


def convert_git_url_to_https(url):
    """Convert a git URL to HTTPS format.

    Supports:
    - git@github.com:user/repo.git -> https://github.com/user/repo.git
    - ssh://git@github.com/user/repo.git -> https://github.com/user/repo.git
    - https://github.com/user/repo.git -> https://github.com/user/repo.git (unchanged)
    """
    url = url.strip()
    # Already HTTPS
    if url.startswith("https://"):
        return url
    # SSH format: git@host:user/repo.git
    match = re.match(r"^git@([^:]+):(.+)$", url)
    if match:
        host, path = match.groups()
        return f"https://{host}/{path}"
    # SSH URL format: ssh://git@host/user/repo.git
    match = re.match(r"^ssh://git@([^/]+)/(.+)$", url)
    if match:
        host, path = match.groups()
        return f"https://{host}/{path}"
    # Git protocol: git://host/user/repo.git
    match = re.match(r"^git://([^/]+)/(.+)$", url)
    if match:
        host, path = match.groups()
        return f"https://{host}/{path}"
    # Unknown format, return as-is
    return url


def ensure_ed25519_key(path, comment):
    """Generate SSH key if missing."""
    path_obj = Path(path)
    if not path_obj.exists():
        print(f"- Generating {comment} at {path} ...")
        run_cmd(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", path])
    if not Path(f"{path}.pub").exists():
        with open(f"{path}.pub", "w", encoding="utf-8") as f:
            stdout, _ = run_cmd(["ssh-keygen", "-y", "-f", path], capture_output=True)
            f.write(stdout + "\n")


def get_image_created_time(image_name):
    """Get image creation time."""
    stdout, _ = run_cmd(["docker", "image", "inspect", image_name, "--format", "{{.Created}}"], capture_output=True, check=False)
    return stdout


def date_to_epoch(date_str):
    """Convert date string to epoch."""
    try:
        # Stay compatible with python3.10 until I upgrade away from Ubuntu 22.04 lol
        s = date_str.replace("Z", "+00:00")
        # Trim the nanoseconds
        if "." in s:
            parts = s.split(".", 1)
            # Split - or +
            for c in "+-":
                if c in parts[1]:
                    s = parts[0] + c + parts[1].split(c, 1)[-1]
                    break
        return datetime.fromisoformat(s).timestamp()
    except ValueError:
        return 0


def build_customized_image(script_dir, user_auth_keys, md_user_key, image_name, base_image, tag_explicitly_provided=False):
    """Build the user customized Docker image."""
    rsc_dir = f"{script_dir}/rsc"
    machine = platform.machine().lower()
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64", "amd64": "amd64"}.get(machine)
    if not host_arch:
        print(f"- Unknown architecture: {machine}", file=sys.stderr)
        return None

    with open(md_user_key + ".pub", encoding="utf-8") as f:
        pub_key = f.read()
    with open(user_auth_keys, "w", encoding="utf-8") as f:
        f.write(pub_key)
    os.chmod(user_auth_keys, 0o600)

    # Only check for md-base if --tag was not explicitly provided (i.e., using default "latest")
    if not tag_explicitly_provided and base_image == "ghcr.io/maruel/md:latest":
        local_base = get_image_created_time("md-base")
        if local_base:
            remote_base = get_image_created_time(base_image)
            if not remote_base:
                print(f"- Remote {base_image} image not found, using local build instead")
                base_image = "md-base"
            elif date_to_epoch(local_base) > date_to_epoch(remote_base):
                print(f"- Local md-base image is newer, using local build instead of {base_image}")
                print("  Run 'docker image rm md-base' to delete the local image.")
                base_image = "md-base"

    if base_image != "md-base":
        print(f"- Pulling base image {base_image} ...")
        run_cmd(["docker", "pull", "--platform", f"linux/{host_arch}", base_image])

    stdout, returncode = run_cmd(["docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", base_image], capture_output=True, check=False)
    if returncode == 0:
        base_digest = stdout
    else:
        base_digest, _ = run_cmd(["docker", "image", "inspect", "--format", "{{.Id}}", base_image], capture_output=True)

    context_sha, _ = run_cmd(["tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - -C " + shlex.quote(rsc_dir) + " . | sha256sum | cut -d' ' -f1"], shell=True, capture_output=True)

    current_digest = ""
    current_context = ""
    stdout, returncode = run_cmd(["docker", "image", "inspect", image_name, "--format", '{{index .Config.Labels "md.base_digest"}}'], capture_output=True, check=False)
    if returncode == 0:
        current_digest = stdout if stdout != "<no value>" else ""
        stdout, returncode = run_cmd(["docker", "image", "inspect", image_name, "--format", '{{index .Config.Labels "md.context_sha"}}'], capture_output=True, check=False)
        if returncode == 0:
            current_context = stdout

    if current_digest == base_digest and current_context == context_sha:
        print(f"- Docker image {image_name} already matches {base_image} ({base_digest}), skipping rebuild.")
        return base_image

    print(f"- Building Docker image {image_name} ...")
    run_cmd(
        [
            "docker",
            "build",
            "--platform",
            f"linux/{host_arch}",
            "--build-arg",
            f"BASE_IMAGE={base_image}",
            "--build-arg",
            f"BASE_IMAGE_DIGEST={base_digest}",
            "--build-arg",
            f"CONTEXT_SHA={context_sha}",
            "-t",
            image_name,
            rsc_dir,
        ]
    )
    return base_image


def run_container(container_name, image_name, md_user_key, host_key_pub_path, git_current_branch, display, repo_name):
    """Start Docker container."""
    print(f"- Starting container {container_name} ...")

    repo_name_q = shlex.quote(repo_name)
    kvm_args = ["--device=/dev/kvm"] if os.path.exists("/dev/kvm") and os.access("/dev/kvm", os.W_OK) else []
    localtime_args = ["-v", "/etc/localtime:/etc/localtime:ro"] if sys.platform == "linux" else []
    display_args = ["-p", "127.0.0.1:0:5901", "-e", "MD_DISPLAY=1"] if display else []
    repo_dir_args = ["-e", f"MD_REPO_DIR={repo_name_q}"]
    # Grant just enough rights for Chrome sandbox and debugging tools to work.
    # seccomp=unconfined: Allow CLONE_NEWUSER for user namespaces.
    # apparmor=unconfined: Allow unprivileged user namespaces (Ubuntu 24.04+).
    # SYS_PTRACE: Allow strace and other debugging tools.
    sandbox_args = ["--cap-add=SYS_PTRACE", "--security-opt", "seccomp=unconfined", "--security-opt", "apparmor=unconfined"]

    home = Path.home()
    # Use XDG environment variables with proper fallbacks
    xdg_config_home = os.environ.get("XDG_CONFIG_HOME", str(home / ".config"))
    xdg_data_home = os.environ.get("XDG_DATA_HOME", str(home / ".local" / "share"))
    xdg_state_home = os.environ.get("XDG_STATE_HOME", str(home / ".local" / "state"))

    # Build mounts from agent configuration
    mounts = []
    for agent_path in AGENT_CONFIG["home_paths"]:
        mounts.extend(["-v", f"{home}/{agent_path}:/home/user/{agent_path}"])
    for config_path in AGENT_CONFIG["xdg_config_paths"]:
        read_only = ":ro" if config_path == "md" else ""
        mounts.extend(["-v", f"{xdg_config_home}/{config_path}:/home/user/.config/{config_path}{read_only}"])
    for share_path in AGENT_CONFIG["local_share_paths"]:
        mounts.extend(["-v", f"{xdg_data_home}/{share_path}:/home/user/.local/share/{share_path}"])
    for state_path in AGENT_CONFIG["local_state_paths"]:
        mounts.extend(["-v", f"{xdg_state_home}/{state_path}:/home/user/.local/state/{state_path}"])

    docker_cmd = ["docker", "run", "-d", "--name", container_name, "--hostname", container_name, "-p", "127.0.0.1:0:22"] + display_args + repo_dir_args + kvm_args + localtime_args + sandbox_args + mounts + [image_name]
    run_cmd(docker_cmd, check=False)

    port, _ = run_cmd(["docker", "inspect", "--format", '{{(index .NetworkSettings.Ports "22/tcp" 0).HostPort}}', container_name], capture_output=True)
    print(f"- Found ssh port {port}")

    # Old images may not have VNC yet.
    vnc_port = None
    if display:
        vnc_port, _ = run_cmd(["docker", "inspect", "--format", '{{(index .NetworkSettings.Ports "5901/tcp" 0).HostPort}}', container_name], capture_output=True, check=False)
        if vnc_port:
            print(f"- Found VNC port {vnc_port} (display :1)")

    ssh_config_dir = home / ".ssh" / "config.d"
    ssh_config_dir.mkdir(parents=True, exist_ok=True)

    host_conf = ssh_config_dir / f"{container_name}.conf"
    host_known_hosts = ssh_config_dir / f"{container_name}.known_hosts"

    with open(host_conf, "w", encoding="utf-8") as f:
        f.write(
            f"Host {container_name}\n  HostName 127.0.0.1\n  Port {port}\n  User user\n  IdentityFile {md_user_key}\n  IdentitiesOnly yes\n  UserKnownHostsFile {host_known_hosts}\n  StrictHostKeyChecking yes\n"
        )

    with open(host_key_pub_path, encoding="utf-8") as f:
        host_pub_key = f.read().strip()
    with open(host_known_hosts, "w", encoding="utf-8") as f:
        f.write(f"[127.0.0.1]:{port} {host_pub_key}\n")

    print("- git clone into container ...")
    run_cmd(["git", "remote", "rm", container_name], check=False, capture_output=True)
    run_cmd(["git", "remote", "add", container_name, f"user@{container_name}:./{repo_name_q}"])

    start = time.time()
    while True:
        try:
            output, returncode = run_cmd(["ssh", "-o", "ConnectTimeout=2", container_name, "exit"],
                                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                         timeout=10, check=False)
            if returncode == 0:
                break
        except subprocess.TimeoutExpired:
            pass
        time.sleep(0.1)
        if time.time() - start > 10:
            print("Timed out waiting for container to start", file=sys.stderr)
            print(output, file=sys.stderr)
            return 1

    # Initialize git repo in the container (done here instead of Dockerfile since repo_name is dynamic)
    run_cmd(["ssh", container_name, f"mkdir -p ./{repo_name_q} && git init -q ./{repo_name_q}"])
    run_cmd(["git", "fetch", container_name])
    run_cmd(["git", "push", "-q", container_name, f"HEAD:refs/heads/{git_current_branch}"])
    run_cmd(["ssh", container_name, f"cd ./{repo_name_q} && git switch -q {git_current_branch}"])
    run_cmd(["ssh", container_name, f"cd ./{repo_name_q} && git branch -f base {git_current_branch} && git switch -q base && git switch -q {git_current_branch}"])

    # Set up origin remote in the container pointing to the original project using HTTPS.
    # This enables claude --teleport functionality which requires HTTPS access.
    origin_url, returncode = run_cmd(["git", "remote", "get-url", "origin"], capture_output=True, check=False)
    if returncode == 0 and origin_url:
        https_url = convert_git_url_to_https(origin_url)
        run_cmd(["ssh", container_name, f"cd ./{repo_name_q} && git remote add origin {shlex.quote(https_url)}"])
        print(f"- Set container origin to {https_url}")

    if Path(".env").exists():
        print("- sending .env into container ...")
        run_cmd(["scp", ".env", f"{container_name}:/home/user/.env"])

    print(f"\nBase branch '{git_current_branch}' has been set up in the container as 'base' for easy diffing.")
    print("Inside the container, you can use 'git diff base' to see your changes.\n")
    print("Remote access:")
    print(f"  SSH: ssh {container_name}")
    if vnc_port:
        print(f"  VNC: connect to localhost:{vnc_port} with a VNC client\n")
    print(f"When done while on branch {git_current_branch}:")
    print("  md kill")
    return 0


@argument("--display", "-d", action="store_true", help="Enable X11/VNC display")
@argument("--tag", default=None, help="Tag for the base image (ghcr.io/maruel/md:<tag>); use 'edge' for the latest from CI")
def cmd_start(args):
    """Pull base image with specified tag, rebuild if needed, start container, open shell."""
    host_key_path = Path(SCRIPT_DIR) / "rsc" / "etc" / "ssh" / "ssh_host_ed25519_key"
    host_key_pub_path = str(host_key_path) + ".pub"
    user_auth_keys = Path(SCRIPT_DIR) / "rsc" / "home" / "user" / ".ssh" / "authorized_keys"
    home = Path.home()
    image_name = "md"

    # Determine the tag to use
    # If --tag was not provided, use "latest" and check for md-base
    # If --tag was provided, use the specified value and don't check for md-base
    tag_explicitly_provided = args.tag is not None
    tag_to_use = args.tag if tag_explicitly_provided else "latest"

    base_image = f"ghcr.io/maruel/md:{tag_to_use}"
    md_user_key = str(home / ".ssh" / "md")

    # Use XDG environment variables with proper fallbacks
    xdg_config_home = os.environ.get("XDG_CONFIG_HOME", str(home / ".config"))
    xdg_data_home = os.environ.get("XDG_DATA_HOME", str(home / ".local" / "share"))
    xdg_state_home = os.environ.get("XDG_STATE_HOME", str(home / ".local" / "state"))

    # Build paths from agent configuration
    paths = []
    paths.extend(home / path for path in AGENT_CONFIG["home_paths"])
    paths.extend(os.path.join(xdg_config_home, path) for path in AGENT_CONFIG["xdg_config_paths"])
    paths.extend(os.path.join(xdg_data_home, path) for path in AGENT_CONFIG["local_share_paths"])
    paths.extend(os.path.join(xdg_state_home, path) for path in AGENT_CONFIG["local_state_paths"])
    # Additional paths
    paths.extend(
        [
            host_key_path.parent,
            user_auth_keys.parent,
            home / ".ssh" / "config.d",
        ]
    )
    for p in paths:
        Path(p).mkdir(parents=True, exist_ok=True)

    claude_json = home / ".claude.json"
    if not claude_json.exists():
        claude_dir_json = home / ".claude" / "claude.json"
        claude_json.symlink_to(claude_dir_json)
    elif not claude_json.is_symlink():
        print(f"File {claude_json} exists but is not a symlink", file=sys.stderr)
        sys.exit(1)

    ensure_ed25519_key(str(home / ".ssh" / "md"), "md-user")
    ensure_ed25519_key(str(host_key_path), "md-host")

    _, returncode = run_cmd(["docker", "inspect", args.container_name], capture_output=True, check=False)
    if returncode == 0:
        print(f"Container {args.container_name} already exists. SSH in with 'ssh {args.container_name}' or clean it up via 'md kill' first.", file=sys.stderr)
        sys.exit(1)
    if not build_customized_image(SCRIPT_DIR, str(user_auth_keys), md_user_key, image_name, base_image, tag_explicitly_provided):
        return 1
    result = run_container(args.container_name, image_name, md_user_key, host_key_pub_path, args.git_current_branch, args.display, args.repo_name)
    if result != 0:
        return 1
    run_cmd(["ssh", args.container_name])
    return 0


def cmd_build_base(args):  # pylint: disable=unused-argument
    """Build the base Docker image locally from rsc/Dockerfile.base."""
    machine = platform.machine().lower()
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64", "amd64": "amd64"}.get(machine)
    if not host_arch:
        print(f"- Unknown architecture: {machine}", file=sys.stderr)
        return 1
    print("- Building base Docker image from rsc/Dockerfile.base ...")
    cmd = [
        "docker",
        "build",
        "--platform",
        f"linux/{host_arch}",
        "-f",
        f"{SCRIPT_DIR}/rsc/Dockerfile.base",
        "-t",
        "md-base",
    ]
    if os.environ.get("GITHUB_TOKEN"):
        cmd.extend(["--secret", "id=github_token,env=GITHUB_TOKEN"])
    else:
        print("WARNING: GITHUB_TOKEN not found. Some tools (neovim, rust-analyzer, etc) might fail to install or hit rate limits.", file=sys.stderr)
        print("Please set GITHUB_TOKEN to avoid issues:", file=sys.stderr)
        print("  https://github.com/settings/personal-access-tokens/new?name=md-build-base&description=Token%20to%20help%20generating%20local%20docker%20images%20for%20https://github.com/maruel/md", file=sys.stderr)
        print("  export GITHUB_TOKEN=...", file=sys.stderr)

    cmd.append(f"{SCRIPT_DIR}/rsc")
    run_cmd(cmd)
    print("- Base image built as 'md-base'.")
    return 0


def cmd_push(args):
    """Force-push current repo state into the running container."""
    # Refuse if there's any pending changes locally.
    _, returncode = run_cmd(["git", "diff", "--quiet", "--exit-code"], check=False)
    if returncode:
        print("- There are pending changes locally. Please commit or stash them before pushing.", file=sys.stderr)
        return 1
    # If there are any pending changes inside the container, commit them so they are saved in the backup branch.
    repo_name = shlex.quote(args.repo_name)
    run_cmd(["ssh", args.container_name, f"cd ./{repo_name} && git add . && (git diff --quiet HEAD -- . || git commit -q -m 'Backup before push')"], check=False)
    container_commit, _ = run_cmd(["ssh", args.container_name, f"cd ./{repo_name} && git rev-parse HEAD"], capture_output=True)
    backup_branch = f"backup-{datetime.now().strftime('%Y%m%d-%H%M%S')}"
    run_cmd(["ssh", args.container_name, f"cd ./{repo_name} && git branch -f {backup_branch} {container_commit}"])
    print(f"- Previous state saved as git branch: {backup_branch}")
    # Update base first.
    run_cmd(["git", "push", "-q", "-f", args.container_name, f"{args.git_current_branch}:base"])
    # Recreate the branch from base.
    run_cmd(["ssh", args.container_name, f"cd ./{repo_name} && git switch -q -C {args.git_current_branch} base"])
    print("- Container updated.")
    return 0


def cmd_pull(args):
    """Pull changes from the container back to the local repo."""
    repo_name = shlex.quote(args.repo_name)
    # Add any untracked files and identify if a commit is needed.
    _, returncode = run_cmd(["ssh", args.container_name, f"cd ./{repo_name} && git add . && git diff --quiet HEAD -- ."], capture_output=True, check=False)
    if returncode != 0:
        commit_msg = "Pull from md"
        if os.environ.get("ASK_PROVIDER") and shutil.which("ask"):
            # Generate a commit message based on the pending changes.
            prompt = "Analyze the git context below (branch, file changes, recent commits, and diff). Write a commit message explaining what changed and why. It should be one line, or summary + details if the change is very complex. Match the style of recent commits. No emojis."
            remote_cmd = f"cd ./{repo_name} && echo '=== Branch ===' && git rev-parse --abbrev-ref HEAD && echo && echo '=== Files Changed ===' && git diff --stat --cached base -- . && echo && echo '=== Recent Commits ===' && git log -5 base -- && echo && echo '=== Changes ===' && git diff --patience -U10 --cached base -- . ':!*.yaml'"
            git_context, _ = run_cmd(["ssh", args.container_name, remote_cmd], capture_output=True)
            try:
                msg, rc = run_cmd(["ask", "-q", prompt], input=git_context, capture_output=True, timeout=10, check=False)
                if rc == 0 and msg:
                    commit_msg = msg
            except subprocess.TimeoutExpired:
                pass
        git_user_name, _ = run_cmd(["git", "config", "user.name"], capture_output=True)
        git_user_email, _ = run_cmd(["git", "config", "user.email"], capture_output=True)
        git_author = f"{git_user_name} <{git_user_email}>"
        commit_cmd = f"cd ./{repo_name} && echo {shlex.quote(commit_msg)} | git commit -a -q --author {shlex.quote(git_author)} -F -"
        run_cmd(["ssh", args.container_name, commit_cmd])

    # Pull changes from the container. It's possible that the container is ahead of the local repo.
    run_cmd(["git", "pull", "--rebase", "-q", args.container_name, args.git_current_branch])
    # Update the base branch to match the current branch.
    run_cmd(["git", "push", "-q", "-f", args.container_name, f"{args.git_current_branch}:base"])
    return 0


@accepts_extra_args
def cmd_diff(args):
    """Show differences between base and current changes. Extra args passed to git diff."""
    extra = " ".join(shlex.quote(a) for a in args.extra) if args.extra else ""
    repo_name = shlex.quote(args.repo_name)
    run_cmd(["ssh", "-q", "-t", args.container_name, f"cd ./{repo_name} && git add . && git diff base {extra} -- ."])
    return 0


def cmd_vnc(args):
    """Open VNC connection to the container."""
    _, returncode = run_cmd(["docker", "inspect", args.container_name], capture_output=True, check=False)
    if returncode != 0:
        print(f"Container {args.container_name} is not running", file=sys.stderr)
        return 1

    vnc_port, _ = run_cmd(["docker", "inspect", "--format", '{{(index .NetworkSettings.Ports "5901/tcp" 0).HostPort}}', args.container_name], capture_output=True, check=False)
    if not vnc_port:
        print(f"VNC port not found for {args.container_name}. Did you start it with --display?", file=sys.stderr)
        print("To enable display, run:\n  md kill\n  md start --display", file=sys.stderr)
        return 1

    vnc_url = f"vnc://127.0.0.1:{vnc_port}"
    print(f"VNC connection: {vnc_url}")

    if sys.platform == "darwin":
        _, returncode = run_cmd(["open", vnc_url], check=False)
    elif sys.platform == "linux":
        _, returncode = run_cmd(["xdg-open", vnc_url], check=False)
        if returncode != 0:
            # Try direct VNC viewer as fallback
            _, returncode = run_cmd(["vncviewer", f"127.0.0.1:{vnc_port}"], check=False)
            if returncode != 0:
                print("\nNo VNC client found. Connect manually:")
                print("  Address: 127.0.0.1")
                print(f"  Port: {vnc_port}")
                print("\nInstall a VNC client:")
                print("  Ubuntu/Debian: sudo apt install tigervnc-viewer")
                print("  Fedora/RHEL: sudo dnf install tigervnc")
                print("  Or use any remote desktop client (Remmina, RealVNC, TigerVNC, etc.)")
                returncode = 0
    elif sys.platform == "win32":
        _, returncode = run_cmd(["start", vnc_url], shell=True, check=False)
    else:
        print(f"Unsupported platform: {sys.platform}", file=sys.stderr)
        return 1

    return returncode


def cmd_list(args):  # pylint: disable=unused-argument
    """List running md containers with their uptime."""
    containers, returncode = run_cmd(["docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"], capture_output=True, check=False)
    if returncode or not containers.strip():
        print("No running containers")
        return returncode
    containers = (json.loads(line) for line in containers.split("\n") if line)
    containers = [line for line in containers if line["Names"].startswith("md-")]
    if not containers:
        print("No running md containers")
        return 0
    print(f"{'Container':<50} {'Status':<15} {'Uptime':<20}")
    print("-" * 85)
    for data in sorted(containers, key=lambda c: c["Names"]):
        print(f"{data['Names']:<50} {data['State']:<15} {data['RunningFor']:<20}")
    return 0


def cmd_kill(args):
    """Remove ssh config/remote and stop/remove the container."""
    ssh_config_dir = Path.home() / ".ssh" / "config.d"
    (ssh_config_dir / f"{args.container_name}.conf").unlink(missing_ok=True)
    (ssh_config_dir / f"{args.container_name}.known_hosts").unlink(missing_ok=True)
    run_cmd(["git", "remote", "remove", args.container_name], check=False, capture_output=True)
    stdout, returncode = run_cmd(["docker", "rm", "-f", "-v", args.container_name], check=False, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if returncode or "Error" in stdout:
        print(f"{args.container_name} not running")
        return 1
    print(f"Removed {args.container_name}")
    return 0


def main():
    """Main entry point."""
    git_root_dir, returncode = run_cmd(["git", "rev-parse", "--show-toplevel"], capture_output=True, check=False)
    if returncode:
        print(f"Not a git checkout directory: {os.getcwd()}", file=sys.stderr)
        return 1
    os.chdir(git_root_dir)
    git_current_branch, returncode = run_cmd(["git", "branch", "--show-current"], capture_output=True, check=False)
    if returncode or not git_current_branch:
        print("Check out a named branch", file=sys.stderr)
        return 1
    parser = argparse.ArgumentParser(description="md (my devenv): local development environment with git clone")
    subparsers = parser.add_subparsers(dest="cmd")
    for name, func in inspect.getmembers(sys.modules[__name__], inspect.isfunction):
        if name.startswith("cmd_"):
            epilog = "Extra arguments are passed through." if getattr(func, "accepts_extra_args", False) else None
            desc = func.__doc__.splitlines()[0]
            sub_parser = subparsers.add_parser(name[4:].replace("_", "-"), help=desc, description=desc, epilog=epilog)
            sub_parser.set_defaults(func=func)
            if hasattr(func, "arguments"):
                for args_tuple, kwargs_dict in reversed(func.arguments):
                    sub_parser.add_argument(*args_tuple, **kwargs_dict)
    args, unknown = parser.parse_known_args()
    if not args.cmd:
        parser.print_help()
        return 2
    # Pass unknown args to subcommands that accept extra arguments.
    if getattr(args.func, "accepts_extra_args", False):
        args.extra = unknown
    elif unknown:
        parser.error(f"unrecognized arguments: {' '.join(unknown)}")
    repo_name = Path(git_root_dir).name
    args.container_name = f"md-{repo_name}-{git_current_branch}"
    args.git_current_branch = git_current_branch
    args.repo_name = repo_name
    try:
        return args.func(args)
    except subprocess.CalledProcessError as e:
        # Find the frame that called run_git_command
        tb = traceback.extract_tb(e.__traceback__)
        frame = None
        for i, f in enumerate(tb):
            if f.name == "run_git_command" and i > 0:
                frame = tb[i - 1]
                break
        if frame:
            print(f"Error in {frame.name}() at line {frame.lineno}:", file=sys.stderr)
        print(f"Command failed: {' '.join(e.cmd)}", file=sys.stderr)
        if e.stdout:
            print(f"stdout: {e.stdout}", file=sys.stderr)
        if e.stderr:
            print(f"stderr: {e.stderr}", file=sys.stderr)
        return e.returncode


if __name__ == "__main__":
    sys.exit(main())
