# Agent development guide

A file to [guide coding agents](https://agents.md/).

## Requirements

- Make sure the code passes shellcheck after every change. Then format with `shfmt -l -w $script_name`
- Update this file (AGENTS.md) everytime you make a change that affects this project's requirements.
- Update rsc/home/user/AGENTS.md everytime you make a change that affects the agent inside the container.
- When adding a new setup script in `rsc/root/setup/` or `rsc/home/user/setup/`, add a corresponding `RUN` command to `rsc/Dockerfile.base` to execute it during the build.
- No tests should be written for any changes made to the codebase.
- For Python code changes, ensure code passes `pylint` and `ruff` checks as defined in `.github/workflows/docker-build.yml`
- When adding new tools to the system, they must also be added to `rsc/home/user/setup/generate_version_report.sh` to ensure they appear in version reports. The script generates `/var/log/tool_versions.md` which is used in release notes and build reports

## Container Remote GUI Access

The container runs a VNC server (TigerVNC + XFCE4) on port 5901 accessible via any VNC client.

## Directory Layout (rsc/)

The `rsc/` directory contains Docker build context and system configuration:

- `rsc/Dockerfile` and `rsc/Dockerfile.base` - Docker build files
- `rsc/etc/` - System-level configuration files (copied to `/etc/` in container)
  - `rsc/etc/profile.d/` - Shell environment scripts sourced by login shells
- `rsc/root/` - Root-context setup and utilities
  - `rsc/root/setup/` - Root-level installation scripts (numbered 1+)
  - `rsc/root/start.sh` - Container entrypoint
- `rsc/home/user/` - User-context setup (copied as user to `/home/user/`)
  - `rsc/home/user/setup/` - User-level installation scripts (numbered 1+)
  - `rsc/home/user/AGENTS.md` - Agent documentation inside container (keep in sync)