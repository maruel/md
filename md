#!/usr/bin/env python3
# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by the Apache v2 license that can be found in the
# LICENSE file.

import argparse
import inspect
import os
import shlex
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = str(Path(__file__).parent.resolve())


def run_cmd(cmd, check=False, **kwargs):
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
    stdout, _ = run_cmd(["docker", "image", "inspect", image_name, "--format", "{{.Created}}"], capture_output=True)
    return stdout


def date_to_epoch(date_str):
    """Convert date string to epoch."""
    try:
        return str(int(datetime.fromisoformat(date_str.replace("Z", "+00:00")).timestamp()))
    except ValueError:
        return "0"


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
        if remote_base:
            local_epoch = date_to_epoch(local_base)
            remote_epoch = date_to_epoch(remote_base)
            if int(local_epoch) > int(remote_epoch):
                print(f"- Local md-base image is newer, using local build instead of {base_image}")
                base_image = "md-base"

    if base_image != "md-base":
        print(f"- Pulling base image {base_image} ...")
        run_cmd(["docker", "pull", base_image])

    stdout, returncode = run_cmd(["docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", base_image], capture_output=True)
    if returncode == 0:
        base_digest = stdout
    else:
        base_digest, _ = run_cmd(["docker", "image", "inspect", "--format", "{{.Id}}", base_image], capture_output=True)

    context_sha, _ = run_cmd(["tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - -C " + shlex.quote(rsc_dir) + " . | sha256sum | cut -d' ' -f1"], shell=True, capture_output=True)

    current_digest = ""
    current_context = ""
    stdout, returncode = run_cmd(["docker", "image", "inspect", image_name, "--format", '{{index .Config.Labels "md.base_digest"}}'], capture_output=True)
    if returncode == 0:
        current_digest = stdout if stdout != "<no value>" else ""
        stdout, _ = run_cmd(["docker", "image", "inspect", image_name, "--format", '{{index .Config.Labels "md.context_sha"}}'], capture_output=True)
        current_context = stdout if stdout != "<no value>" else ""

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
    run_cmd(["git", "remote", "rm", container_name], check=False)
    run_cmd(["git", "remote", "add", container_name, f"user@{container_name}:/app"], check=False)

    while True:
        try:
            _, returncode = run_cmd(["ssh", container_name, "exit"], capture_output=True, timeout=1)
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

    _, returncode = run_cmd(["docker", "inspect", args.container_name], capture_output=True)
    if returncode == 0:
        print(f"Container {args.container_name} already exists. SSH in with 'ssh {args.container_name}' or clean it up via 'md kill' first.", file=sys.stderr)
        sys.exit(1)
    build(SCRIPT_DIR, str(user_auth_keys), md_user_key, image_name, base_image)
    run_container(args.container_name, image_name, md_user_key, host_key_pub_path, args.git_current_branch)
    run_cmd(["ssh", args.container_name])


def cmd_build_base(args):  # pylint: disable=unused-argument
    """Build the base Docker image locally from rsc/Dockerfile.base."""
    print("- Building base Docker image from rsc/Dockerfile.base ...")
    run_cmd(["docker", "build", "-f", f"{SCRIPT_DIR}/rsc/Dockerfile.base", "-t", "md-base", f"{SCRIPT_DIR}/rsc"])
    print("- Base image built as 'md-base'.")
    return 0


def cmd_push(args):
    """Force-push current repo state into the running container."""
    branch, _ = run_cmd(["git", "rev-parse", "--abbrev-ref", "HEAD"], capture_output=True)
    container_commit, _ = run_cmd(["ssh", args.container_name, "cd /app && git rev-parse HEAD"], capture_output=True)
    backup_branch = f"backup-{datetime.now().strftime('%Y%m%d-%H%M%S')}"
    run_cmd(["ssh", args.container_name, f"cd /app && git branch -f {backup_branch} {container_commit}"])
    run_cmd(["git", "push", "-q", "-f", args.container_name])
    run_cmd(["ssh", args.container_name, f"cd /app && git reset -q --hard && git switch -q {branch} && git branch -q -f base {branch}"])
    print(f"- Container updated (previous state saved as {backup_branch}).")
    return 0


def cmd_pull(args):
    """Pull changes from the container back to the local repo."""
    git_user_name, _ = run_cmd(["git", "config", "user.name"], capture_output=True)
    git_user_email, _ = run_cmd(["git", "config", "user.email"], capture_output=True)
    git_author = f"{git_user_name} <{git_user_email}>"

    remote_branch, _ = run_cmd(["ssh", args.container_name, "cd /app && git add . && git rev-parse --abbrev-ref HEAD"], capture_output=True)
    commit_msg = "Pull from md"

    if os.environ.get("ASK_PROVIDER") and os.system("which ask >/dev/null 2>&1") == 0:
        prompt = "Analyze the git context below (branch, file changes, recent commits, and diff). Write a commit message explaining what changed and why. It should be one line, or summary + details if the change is very complex. Match the style of recent commits. No emojis."
        remote_cmd = "cd /app && echo '=== Branch ===' && git rev-parse --abbrev-ref HEAD && echo && echo '=== Files Changed ===' && git diff --stat --cached base -- . && echo && echo '=== Recent Commits ===' && git log -5 base -- && echo && echo '=== Changes ===' && git diff --patience -U10 --cached base -- . ':!*.yaml'"
        git_context, _ = run_cmd(["ssh", args.container_name, remote_cmd], capture_output=True)
        commit_msg, _ = run_cmd(["ask", "-q", prompt], input=git_context, capture_output=True)

    commit_cmd = f"cd /app && echo {shlex.quote(commit_msg)} | git commit -a -q --author {shlex.quote(git_author)} -F -"
    run_cmd(["ssh", args.container_name, commit_cmd])
    run_cmd(["git", "pull", "--rebase", "-q", args.container_name, remote_branch])
    run_cmd(["ssh", args.container_name, f"cd /app && git branch -f base {remote_branch}"])
    return 0


def cmd_diff(args):
    """Show differences between base branch and current changes in container."""
    run_cmd(["ssh", "-q", "-t", args.container_name, "cd /app && git add . && git diff base -- ."])
    return 0


def cmd_kill(args):
    """Remove ssh config/remote and stop/remove the container."""
    ssh_config_dir = Path.home() / ".ssh" / "config.d"
    (ssh_config_dir / f"{args.container_name}.conf").unlink(missing_ok=True)
    (ssh_config_dir / f"{args.container_name}.known_hosts").unlink(missing_ok=True)
    run_cmd(["git", "remote", "remove", args.container_name])
    _, returncode = run_cmd(["docker", "rm", "-f", args.container_name])
    if returncode == 0:
        print(f"Removed {args.container_name}")
    else:
        print(f"{args.container_name} not running")
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
    container_name = f"md-{Path(git_root_dir).name}-{git_current_branch}"
    parser = argparse.ArgumentParser(description="md (my devenv): local development environment with git clone")
    subparsers = parser.add_subparsers(dest="cmd")
    for name, func in inspect.getmembers(sys.modules[__name__], inspect.isfunction):
        if name.startswith("cmd_"):
            cmd_name = name[4:].replace("_", "-")
            subparsers.add_parser(cmd_name, help=func.__doc__.strip() if func.__doc__ else "").set_defaults(func=func)
    args = parser.parse_args()
    if not args.cmd:
        parser.print_help()
        return 0
    args.container_name = container_name
    args.git_current_branch = git_current_branch
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
