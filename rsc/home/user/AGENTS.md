# Agent Development Guide

For agents running in this container environment.

## Preinstalled Tools

For a complete and up-to-date list of tool versions, see `~/tool_versions.md`.

Notable executables available in the container:

- **Core utilities**: bash, git, curl, wget, rsync, jq, grep, ripgrep, less, file, find, xargs, sed, awk, bubblewrap, xvfb, tokei
- **Editors**: nvim (vi, vim, vimdiff)
- **Browsers**: google-chrome (amd64), chromium (arm64 fallback), chrome-devtools-mcp
- **Compression**: brotli, zstd, unzip
- **Development**: build-essential, git, actionlint, shellcheck, shfmt, golangci-lint, cmake, make, gcc, g++, cargo-binstall, pylint, ruff, uv, asciinema
- **Languages**: node (v24), npm, go, python3, java, typescript, eslint, tsx, rust (cargo, rustc), bun
- AI Tools: claude, gemini, codex, qwen-code, opencode, amp
- Virtualization: qemu-kvm, libvirt-clients
- Media: ffmpeg, imagemagick
- Android: android-sdk, gradle, java, adb
- **Database**: sqlite3
- **Network**: curl, wget, net-tools, iproute2, tailscale (when enabled via `md start --tailscale`)
- **Debugging**: strace, lsof, dlv (Go), lldb/rust-lldb (Rust)

## Browser & GUI Environment

- **Browsers**: Google Chrome and Chromium are installed as standard packages.
  - **Sandboxing**: They require the container to be run with `seccomp=unconfined` and `apparmor=unconfined` (handled automatically by the `md` launcher).
  - **Headless**: Can be run headlessly without X. A DBus session is automatically configured for the user.
- **GUI**: XFCE4/TigerVNC is available but only starts if the container is launched with `--display`.
