# Agent development guide

A file to [guide coding agents](https://agents.md/).

## Requirements

- Make sure the code passes shellcheck after every change. Then format with `shfmt -l -w $script_name`
- Update this file (AGENTS.md) everytime you make a change that affects this project's requirements.
- Update rsc/home/user/AGENTS.md everytime you make a change that affects the agent inside the container.
- When adding a new setup script in `rsc/root/setup/` or `rsc/home/user/setup/`, add a corresponding `RUN` command to `rsc/Dockerfile.base` to execute it during the build.
- No tests should be written for any changes made to the codebase.
- For Python code changes, ensure code passes `pylint` and `ruff` checks as defined in `.github/workflows/docker-build.yml`
- When adding new tools to the system, they must also be added to `rsc/home/user/setup/generate_version_report.sh` to ensure they appear in version reports. The script generates `/home/user/tool_versions.md` which is used in release notes and build reports

## Adding a New Tool Checklist

When installing a new tool in the container, ensure you update:

1. Create setup script in `rsc/root/setup/` or `rsc/home/user/setup/` (with appropriate numbering)
2. Add `RUN measure_exec.sh` command to `rsc/Dockerfile.base`
3. Add entry to "Installed Tools" section in this AGENTS.md
4. Add version check to `rsc/home/user/setup/generate_version_report.sh`
5. Update `rsc/home/user/AGENTS.md` with any relevant changes
6. Run `shellcheck` and `shfmt` on any shell scripts

## Installed Tools

- Google Chrome (amd64 only, installed via extrepo during image build in rsc/root/setup/5_chrome.sh)
- Chromium Browser (arm64 fallback, installed via apt in rsc/root/setup/1_packages.sh)
- chromium-sandbox (installed via apt in rsc/root/setup/1_packages.sh)
- Chrome DevTools MCP (installed via npm in rsc/home/user/setup/2_nodejs.sh)
- tokei (installed via apt in rsc/root/setup/1_packages.sh)
- golangci-lint (installed via curl in rsc/home/user/setup/1_go.sh)
- strace (installed via apt in rsc/root/setup/1_packages.sh)
- lldb (installed via apt in rsc/root/setup/1_packages.sh, enables rust-lldb)
- delve/dlv (installed via go install in rsc/home/user/setup/1_go.sh)
- Tailscale (installed via extrepo in rsc/root/setup/7_tailscale.sh)

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

## For End Users: Remote GUI Access

The container runs Xvnc (TigerVNC) + XFCE4 on port 5901 accessible via any VNC client. Xvnc runs as root (unkillable by user), while the XFCE session runs as user:
- **Xvnc** (root): Combined X server + VNC server on :1, port 5901
- **XFCE4** (user): Desktop session, auto-restarts if killed

## Directory Layout (rsc/)

The `rsc/` directory contains Docker build context and system configuration:

- `rsc/Dockerfile` and `rsc/Dockerfile.base` - Docker build files
- `rsc/etc/` - System-level configuration files (copied to `/etc/` in container)
- `rsc/root/` - Root-context setup and utilities
  - `rsc/root/setup/` - Root-level installation scripts (numbered 1+)
  - `rsc/root/start.sh` - Container entrypoint
- `rsc/home/user/` - User-context setup (copied as user to `/home/user/`)
  - `rsc/home/user/.config/bash.d/` - Modular bash extensions sourced by `.bashrc`
  - `rsc/home/user/setup/` - User-level installation scripts (numbered 1+)
  - `rsc/home/user/AGENTS.md` - Agent documentation inside container (keep in sync)
