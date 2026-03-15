# Agent development guide

A file to [guide coding agents](https://agents.md/).

## Requirements

- Make sure the code passes shellcheck after every change. Then format with `shfmt -l -w $script_name`
- Update this file (AGENTS.md) everytime you make a change that affects this project's requirements.
- Update rsc/user/home/user/AGENTS.md everytime you make a change that affects the agent inside the container.
- When adding a new setup script in `rsc/user/root/setup/` or `rsc/user/home/user/setup/`, add a corresponding `RUN` command to `rsc/user/Dockerfile` to execute it during the build.
- No tests should be written for Python or shell script changes.
- **NEVER run `go build ./cmd/md/` without `-o`** — the repo root contains a Python script named `md` and `go build` will overwrite it. Always use `go build -o /tmp/md-test ./cmd/md/` or similar.
- For Go code changes, ensure code passes `go test ./...`, `go vet ./...`, and `golangci-lint run ./...`.
- For Python code changes, ensure code passes `pylint` and `ruff` checks as defined in `.github/workflows/docker-build.yml`
- When adding new tools to the system, they must also be added to `rsc/user/home/user/setup/generate_version_report.sh` to ensure they appear in version reports. The script generates `/home/user/src/tool_versions.md` which is used in release notes and build reports

## md Tool: Image Build and Cache Injection

### Image hierarchy

- **`md-local`** — base image built locally from `rsc/user/Dockerfile` via `md build-image`. Tagged as `md-local`. Used as base when no `--image`/`--tag` flag is given and the user prefers a local build.
- **`ghcr.io/maruel/md:latest`** (default) or any `--image`/`--tag` variant — remote base image.
- **`md-user-<hash>`** — customized per-user image built from `rsc/specialized/Dockerfile` on top of the chosen base. Built automatically by `md start` and `md run` when needed. The image name includes a 32-hex-char hash of (base image, active cache key) so that different base images or cache sets get distinct images without clobbering each other. Computed by `userImageName()` in `docker.go`.

### When the user image is rebuilt

`imageBuildNeeded` (`docker.go`) returns `true` (triggering a rebuild) when any of the following change:
1. `md.base_digest` label missing/empty, or differs from the current base image digest.
2. For remote base images: registry has a newer version than the local copy.
3. `md.context_sha` label differs from the SHA of the embedded `rsc/user/` build context + SSH keys.
4. `md.cache_key` label differs from `cacheSpecKey` of the **active** caches (those whose host directories currently exist).

### Cache injection

`md start` and `md run` bake host cache directories into the user image at build time via `COPY --from=<name>` in the Dockerfile. This avoids slow cold-start downloads inside the container.

**Default behaviour**: all `WellKnownCaches` entries are included. Caches whose host directory does not exist are silently skipped (no rebuild triggered for missing dirs).

**CLI flags** (on both `md start` and `md run`):
- `--no-cache <name>` — exclude a specific well-known cache (repeatable).
- `--no-caches` — disable all default caches; use `--cache` to add back specific ones.
- `--cache <spec>` — add a well-known name (re-adds when used with `--no-caches`) or a custom `host:container[:ro]` path.

**Well-known cache names** (defined in `WellKnownCaches`, `client.go`): bun, cargo, go-mod, gradle, maven, npm, pip, pnpm, uv.

**Adding a new well-known cache**: add an entry to `WellKnownCaches` in `client.go`. No other changes needed — it is automatically picked up by `resolveCaches`, `appendCacheLayers`, and the flag help text.

### Key labels on user image

| Label | Value |
|---|---|
| `md.base_image` | Base image reference used at build time |
| `md.base_digest` | Digest (or image ID for local images) of the base |
| `md.context_sha` | SHA-256 of `rsc/user/` build context + SSH keys |
| `md.cache_key` | 8-byte hex hash of the **active** (injected) cache names+paths |

## Adding a New Tool Checklist

When installing a new tool in the container, ensure you update:

1. Create setup script in `rsc/user/root/setup/` or `rsc/user/home/user/setup/` (with appropriate numbering)
2. Add `RUN measure_exec.sh` command to `rsc/user/Dockerfile`
3. Add entry to "Installed Tools" section in this AGENTS.md
4. Add version check to `rsc/user/home/user/setup/generate_version_report.sh`
6. Update `rsc/user/home/user/src/AGENTS.md` "Preinstalled Tools" section to reflect the change
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
3. Otherwise, add a cleanup pattern to `rsc/user/home/user/setup/bashrc_cleanup.sh` to remove the appended lines
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
- **Nested Containers (rootless Podman inside md)**: Supported on **rootful Docker/Podman hosts** with `kernel.unprivileged_userns_clone=1` (default on most modern distros) — no extra flags needed. Rootless Docker/Podman hosts are not supported: `newuidmap` fails with EPERM because the container itself already runs inside a user namespace, and `start.sh` logs a warning at startup.

## For End Users: Remote GUI Access

The container runs Xvnc (TigerVNC) + XFCE4 on port 5901 accessible via any VNC client. Xvnc runs as root (unkillable by user), while the XFCE session runs as user:
- **Xvnc** (root): Combined X server + VNC server on :1, port 5901
- **XFCE4** (user): Desktop session, auto-restarts if killed

## Directory Layout (rsc/)

The `rsc/` directory is split into two build contexts:

- `rsc/specialized/Dockerfile` - Per-user image build file (SSH key specialization on top of the base)

The `rsc/user/` directory contains the base image build context:

- `rsc/user/Dockerfile` - Base image build file
- `rsc/user/etc/`, `rsc/user/opt/`, `rsc/user/home/` - Mirrored into the container as-is (`COPY etc/ /etc/`, etc.). Place static files here instead of generating them in setup scripts.
  - `rsc/user/etc/bash_env` - Environment bootstrap sourced by BASH_ENV (see Shell Environment below)
  - `rsc/user/etc/bash.bashrc` - System-wide bashrc, sources bash_env for interactive shells
- `rsc/user/root/` - Root-context setup and utilities
  - `rsc/user/root/setup/` - Root-level installation scripts (numbered 1+)
  - `rsc/user/root/start.sh` - Container entrypoint
- `rsc/user/home/user/` - User-context setup (copied as user to `/home/user/`)
  - `rsc/user/home/user/.config/bash.d/` - Modular bash extensions sourced via `/etc/bash_env` (see Shell Environment below)
  - `rsc/user/home/user/setup/` - User-level installation scripts (numbered 1+)
  - `rsc/user/home/user/src/AGENTS.md` - Agent documentation inside container (keep in sync)
