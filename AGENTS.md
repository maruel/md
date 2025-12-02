# Agent development guide

A file to [guide coding agents](https://agents.md/).

## Requirements

- Make sure the code passes shellcheck after every change. Then format with `shfmt -l -w $script_name`
- Update this file (AGENTS.md) everytime you make a change that affects the agent. This may include adding new
  entries, sections or removing old ones.

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
