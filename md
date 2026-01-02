#!/usr/bin/env python3
# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by the Apache v2 license that can be found in the
# LICENSE file.

import argparse
import inspect
import json
import os
import shlex
import shutil
import subprocess
import sys
import time
import traceback
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = str(Path(__file__).parent.resolve())


def run_cmd(cmd, check=True, **kwargs):
    """Execute shell command, return (stdout, returncode) tuple."""
    if kwargs.get("capture_output", False) or "stdout" in kwargs:
        kwargs.setdefault("text", True)
    result = subprocess.run(cmd, check=check, **kwargs)
    return (result.stdout.strip() if result.stdout else "", result.returncode)


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


def build(script_dir, user_auth_keys, md_user_key, image_name, base_image):
    """Build Docker image."""
    rsc_dir = f"{script_dir}/rsc"

    with open(md_user_key + ".pub", encoding="utf-8") as f:
        pub_key = f.read()
    with open(user_auth_keys, "w", encoding="utf-8") as f:
        f.write(pub_key)
    os.chmod(user_auth_keys, 0o600)

    local_base = get_image_created_time("md-base")
    if local_base:
        remote_base = get_image_created_time(base_image)
        if not remote_base:
            print(f"- Remote {base_image} image not found, using local build instead")
            base_image = "md-base"
        elif date_to_epoch(local_base) > date_to_epoch(remote_base):
            print(f"- Local md-base image is newer, using local build instead of {base_image}")
            base_image = "md-base"

    if base_image != "md-base":
        print(f"- Pulling base image {base_image} ...")
        _, returncode = run_cmd(["docker", "pull", base_image], check=False)
        if returncode:
            print("- Pulling base image {base_image} failed, this is likely because the GitHub Actions ", file=sys.stderr)
            print("  workflow to build the image failed. Sorry about that!", file=sys.stderr)
            if not local_base:
                print("  Try building one locally with 'md build-base' for now.", file=sys.stderr)
                return None
            # Unlikely?
            print("  Fallingback to local 'md-base'", file=sys.stderr)
            base_image = "md-base"

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
    run_cmd(["docker", "build", "--build-arg", f"BASE_IMAGE={base_image}", "--build-arg", f"BASE_IMAGE_DIGEST={base_digest}", "--build-arg", f"CONTEXT_SHA={context_sha}", "-t", image_name, rsc_dir])
    return base_image


def run_container(container_name, image_name, md_user_key, host_key_pub_path, git_current_branch):
    """Start Docker container."""
    print(f"- Starting container {container_name} ...")

    kvm_args = ["--device=/dev/kvm"] if os.path.exists("/dev/kvm") and os.access("/dev/kvm", os.W_OK) else []
    localtime_args = ["-v", "/etc/localtime:/etc/localtime:ro"] if sys.platform == "linux" else []

    home = Path.home()
    xdg_config = os.environ.get("XDG_CONFIG_HOME", str(home / ".config"))
    mounts = [
        "-v",
        f"{home}/.amp:/home/user/.amp",
        "-v",
        f"{home}/.android:/home/user/.android",
        "-v",
        f"{home}/.codex:/home/user/.codex",
        "-v",
        f"{home}/.claude:/home/user/.claude",
        "-v",
        f"{home}/.gemini:/home/user/.gemini",
        "-v",
        f"{home}/.letta:/home/user/.letta",
        "-v",
        f"{home}/.opencode:/home/user/.opencode",
        "-v",
        f"{home}/.qwen:/home/user/.qwen",
        "-v",
        f"{xdg_config}/amp:/home/user/.config/amp",
        "-v",
        f"{xdg_config}/goose:/home/user/.config/goose",
        "-v",
        f"{xdg_config}/opencode:/home/user/.config/opencode",
        "-v",
        f"{xdg_config}/md:/home/user/.config/md:ro",
        "-v",
        f"{home}/.local/share/amp:/home/user/.local/share/amp",
        "-v",
        f"{home}/.local/share/goose:/home/user/.local/share/goose",
    ]

    docker_cmd = ["docker", "run", "-d", "--name", container_name, "-p", "127.0.0.1:0:22"] + kvm_args + localtime_args + mounts + [image_name]
    run_cmd(docker_cmd, check=False)

    port, _ = run_cmd(["docker", "inspect", "--format", '{{(index .NetworkSettings.Ports "22/tcp" 0).HostPort}}', container_name], capture_output=True)
    print(f"- Found ssh port {port}")

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
    run_cmd(["git", "remote", "add", container_name, f"user@{container_name}:/app"])

    while True:
        try:
            _, returncode = run_cmd(["ssh", container_name, "exit"], capture_output=True, timeout=1, check=False)
            if returncode == 0:
                break
        except subprocess.TimeoutExpired:
            pass
        time.sleep(0.1)

    run_cmd(["git", "fetch", container_name])
    run_cmd(["git", "push", "-q", container_name, f"HEAD:refs/heads/{git_current_branch}"])
    run_cmd(["ssh", container_name, f"cd /app && git switch -q {git_current_branch}"])
    run_cmd(["ssh", container_name, f"cd /app && git branch -f base {git_current_branch} && git switch -q base && git switch -q {git_current_branch}"])

    if Path(".env").exists():
        print("- sending .env into container ...")
        run_cmd(["scp", ".env", f"{container_name}:/home/user/.env"])

    print(f"\nBase branch '{git_current_branch}' has been set up in the container as 'base' for easy diffing.")
    print("Inside the container, you can use 'git diff base' to see your changes.\n")
    print("When done:")
    print(f"  docker rm -f {container_name}")


def cmd_start(args):
    """Pull latest base image, rebuild if needed, start container, open shell."""
    host_key_path = Path(SCRIPT_DIR) / "rsc" / "etc" / "ssh" / "ssh_host_ed25519_key"
    host_key_pub_path = str(host_key_path) + ".pub"
    user_auth_keys = Path(SCRIPT_DIR) / "rsc" / "home" / "user" / ".ssh" / "authorized_keys"
    home = Path.home()
    image_name = "md"
    base_image = "ghcr.io/maruel/md:latest"
    md_user_key = str(home / ".ssh" / "md")

    paths = (
        home / ".amp",
        home / ".android",
        home / ".codex",
        home / ".claude",
        home / ".gemini",
        home / ".letta",
        home / ".opencode",
        home / ".qwen",
        home / ".config" / "amp",
        home / ".config" / "goose",
        home / ".config" / "opencode",
        home / ".config" / "md",
        home / ".local" / "share" / "amp",
        home / ".local" / "share" / "goose",
        host_key_path.parent,
        user_auth_keys.parent,
        home / ".ssh" / "config.d",
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
    if not build(SCRIPT_DIR, str(user_auth_keys), md_user_key, image_name, base_image):
        return 1
    run_container(args.container_name, image_name, md_user_key, host_key_pub_path, args.git_current_branch)
    run_cmd(["ssh", args.container_name])
    return 0


def cmd_build_base(args):  # pylint: disable=unused-argument
    """Build the base Docker image locally from rsc/Dockerfile.base."""
    print("- Building base Docker image from rsc/Dockerfile.base ...")
    run_cmd(["docker", "build", "-f", f"{SCRIPT_DIR}/rsc/Dockerfile.base", "-t", "md-base", f"{SCRIPT_DIR}/rsc"])
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
    run_cmd(["ssh", args.container_name, "cd /app && git add . && git commit -q -m 'Backup before push'"], check=False)
    container_commit, _ = run_cmd(["ssh", args.container_name, "cd /app && git rev-parse HEAD"], capture_output=True)
    backup_branch = f"backup-{datetime.now().strftime('%Y%m%d-%H%M%S')}"
    run_cmd(["ssh", args.container_name, f"cd /app && git branch -f {backup_branch} {container_commit}"])
    print(f"- Previous state saved as git branch: {backup_branch}")
    # Update base first.
    run_cmd(["git", "push", "-q", "-f", args.container_name, f"{args.git_current_branch}:base"])
    # Recreate the branch from base.
    run_cmd(["ssh", args.container_name, f"cd /app && git switch -q -C {args.git_current_branch} base"])
    print("- Container updated.")
    return 0


def cmd_pull(args):
    """Pull changes from the container back to the local repo."""
    # Add any untracked files and identify if a commit is needed.
    _, returncode = run_cmd(["ssh", args.container_name, "cd /app && git add . && git diff --quiet HEAD -- ."], capture_output=True, check=False)
    if returncode != 0:
        commit_msg = "Pull from md"
        if os.environ.get("ASK_PROVIDER") and shutil.which("ask"):
            # Generate a commit message based on the pending changes.
            prompt = "Analyze the git context below (branch, file changes, recent commits, and diff). Write a commit message explaining what changed and why. It should be one line, or summary + details if the change is very complex. Match the style of recent commits. No emojis."
            remote_cmd = "cd /app && echo '=== Branch ===' && git rev-parse --abbrev-ref HEAD && echo && echo '=== Files Changed ===' && git diff --stat --cached base -- . && echo && echo '=== Recent Commits ===' && git log -5 base -- && echo && echo '=== Changes ===' && git diff --patience -U10 --cached base -- . ':!*.yaml'"
            git_context, _ = run_cmd(["ssh", args.container_name, remote_cmd], capture_output=True)
            try:
                commit_msg, _ = run_cmd(["ask", "-q", prompt], input=git_context, capture_output=True, timeout=10)
            except subprocess.TimeoutExpired:
                pass
        git_user_name, _ = run_cmd(["git", "config", "user.name"], capture_output=True)
        git_user_email, _ = run_cmd(["git", "config", "user.email"], capture_output=True)
        git_author = f"{git_user_name} <{git_user_email}>"
        commit_cmd = f"cd /app && echo {shlex.quote(commit_msg)} | git commit -a -q --author {shlex.quote(git_author)} -F -"
        run_cmd(["ssh", args.container_name, commit_cmd])

    # Pull changes from the container. It's possible that the container is ahead of the local repo.
    run_cmd(["git", "pull", "--rebase", "-q", args.container_name, args.git_current_branch])
    # Update the base branch to match the current branch.
    run_cmd(["git", "push", "-q", "-f", args.container_name, f"{args.git_current_branch}:base"])
    return 0


def cmd_diff(args):
    """Show differences between base branch and current changes in container."""
    run_cmd(["ssh", "-q", "-t", args.container_name, "cd /app && git add . && git diff base -- ."])
    return 0


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
            subparsers.add_parser(name[4:].replace("_", "-"), help=func.__doc__.splitlines()[0]).set_defaults(func=func)
    args = parser.parse_args()
    if not args.cmd:
        parser.print_help()
        return 2
    args.container_name = f"md-{Path(git_root_dir).name}-{git_current_branch}"
    args.git_current_branch = git_current_branch
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
