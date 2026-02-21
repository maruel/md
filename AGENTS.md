# Agent development guide

A file to [guide coding agents](https://agents.md/).

## Requirements

- Make sure the code passes shellcheck after every change. Then format with `shfmt -l -w $script_name`
- Update this file (AGENTS.md) everytime you make a change that affects this project's requirements.
- Update rsc/home/user/AGENTS.md everytime you make a change that affects the agent inside the container.
- When adding a new setup script in `rsc/root/setup/` or `rsc/home/user/setup/`, add a corresponding `RUN` command to `rsc/Dockerfile.base` to execute it during the build.
- No tests should be written for Python or shell script changes.
- **NEVER run `go build ./cmd/md/` without `-o`** — the repo root contains a Python script named `md` and `go build` will overwrite it. Always use `go build -o /tmp/md-test ./cmd/md/` or similar.
- For Go code changes, ensure code passes `go test ./...`, `go vet ./...`, and `golangci-lint run ./...`.
- For Python code changes, ensure code passes `pylint` and `ruff` checks as defined in `.github/workflows/docker-build.yml`
- When adding new tools to the system, they must also be added to `rsc/home/user/setup/generate_version_report.sh` to ensure they appear in version reports. The script generates `/home/user/src/tool_versions.md` which is used in release notes and build reports

## Adding a New Tool Checklist

When installing a new tool in the container, ensure you update:

1. Create setup script in `rsc/root/setup/` or `rsc/home/user/setup/` (with appropriate numbering)
2. Add `RUN measure_exec.sh` command to `rsc/Dockerfile.base`
3. Add entry to "Installed Tools" section in this AGENTS.md
4. Add version check to `rsc/home/user/setup/generate_version_report.sh`
6. Update `rsc/home/user/src/AGENTS.md` "Preinstalled Tools" section to reflect the change
7. If the tool needs PATH setup, add a `bash.d` script (see [Shell Environment](#shell-environment-bash_env))
8. Run `shellcheck` and `shfmt` on any shell scripts

## Shell Environment (BASH_ENV)

The container uses `BASH_ENV=/etc/bash_env` to ensure PATH and environment variables are available in **all** bash invocations — interactive, non-interactive, login, and non-login. This solves the classic problem where `ssh host command` runs a non-interactive non-login shell that skips `.bashrc`'s interactive guard.

### How it works

1. **`/etc/bash_env`** — sourced by bash for non-interactive shells via the `BASH_ENV` env var (set in Dockerfile). It sources all `~/.config/bash.d/*.sh` scripts. Has a double-source guard.
2. **`/etc/bash.bashrc`** — system-wide bashrc, sources `/etc/bash_env` before the interactive guard. No patching of `/etc/skel/.bashrc` is needed.
3. **`~/.config/bash.d/*.sh`** — modular scripts for PATH and environment:
   - `10-git.sh` — git completions and prompt (self-guards for interactive only)
   - `20-rust-path.sh` — `~/.cargo/bin`
   - `30-go-path.sh` — `~/.local/go/bin`, `~/go/bin`
   - `40-android.sh` — Android SDK paths
   - `50-nvm.sh` — nvm-managed node PATH (loads nvm function only in interactive shells)
   - `60-bun.sh` — `~/.bun/bin`
   - `70-opencode.sh` — `~/.opencode/bin`
   - `80-env.sh` — sources `~/.env` and `~/.config/md/env` (API keys, etc.)
   - `90-shell.sh` — `~/.local/bin`, pnpm, editor, aliases

### Adding a tool that modifies PATH

When installing a tool whose installer appends PATH lines to `.bashrc` (like nvm, bun, opencode):

1. Create a `~/.config/bash.d/NN-toolname.sh` script that adds the tool's bin directory to PATH
2. If the installer supports `PROFILE=/dev/null` (like nvm), use it to prevent writing to `.bashrc`
3. Otherwise, add a cleanup pattern to `rsc/home/user/setup/bashrc_cleanup.sh` to remove the appended lines
4. Interactive-only features (completions, shell functions) should be guarded with `case $- in *i*) ... ;; esac`

## Chrome/Chromium Configuration

Initial preferences are configured via:
- `/opt/google/chrome/initial_preferences` - Chrome
- `/etc/chromium/initial_preferences` - Chromium

Reference for all available preference names. The file is large so first download it and then take a look:
https://chromium.googlesource.com/chromium/src/+/refs/heads/main/chrome/common/pref_names.h?format=TEXT

## Runtime Requirements

- **Chrome Sandbox**: To run Chrome/Chromium with the sandbox enabled, the container must be launched with `--security-opt seccomp=unconfined` and `--security-opt apparmor=unconfined`. The `md` script handles this automatically.
- **Debugging Tools**: strace requires `--cap-add=SYS_PTRACE`. The `md` script handles this automatically.
- **Tailscale**: Requires `--cap-add=NET_ADMIN`, `--cap-add=NET_RAW`, and `--cap-add=MKNOD`. The TUN device is created inside the container's namespace. The `md` script handles this automatically when `--tailscale` is passed to `md start`.
- **USB Passthrough**: Requires `--device=/dev/bus/usb` to expose host USB devices (e.g. for ADB). The `md` script handles this automatically when `--usb` is passed to `md start`.

## For End Users: Remote GUI Access

The container runs Xvnc (TigerVNC) + XFCE4 on port 5901 accessible via any VNC client. Xvnc runs as root (unkillable by user), while the XFCE session runs as user:
- **Xvnc** (root): Combined X server + VNC server on :1, port 5901
- **XFCE4** (user): Desktop session, auto-restarts if killed

## Directory Layout (rsc/)

The `rsc/` directory contains Docker build context and system configuration:

- `rsc/Dockerfile` and `rsc/Dockerfile.base` - Docker build files
- `rsc/etc/`, `rsc/opt/`, `rsc/home/` - Mirrored into the container as-is (`COPY etc/ /etc/`, etc.). Place static files here instead of generating them in setup scripts.
  - `rsc/etc/bash_env` - Environment bootstrap sourced by BASH_ENV (see Shell Environment below)
  - `rsc/etc/bash.bashrc` - System-wide bashrc, sources bash_env for interactive shells
- `rsc/root/` - Root-context setup and utilities
  - `rsc/root/setup/` - Root-level installation scripts (numbered 1+)
  - `rsc/root/start.sh` - Container entrypoint
- `rsc/home/user/` - User-context setup (copied as user to `/home/user/`)
  - `rsc/home/user/.config/bash.d/` - Modular bash extensions sourced via `/etc/bash_env` (see Shell Environment below)
  - `rsc/home/user/setup/` - User-level installation scripts (numbered 1+)
  - `rsc/home/user/src/AGENTS.md` - Agent documentation inside container (keep in sync)
