# Agent Development Guide

For agents running in this container environment.

## Preinstalled Tools

For a complete and up-to-date list of tool versions, see `/var/log/tool_versions.md`.

Notable executables available in the container:

- **Core utilities**: bash, git, curl, wget, rsync, jq, grep, ripgrep, less, file, find, xargs, sed, awk, bubblewrap, xvfb
- **Editors**: nvim (vi, vim, vimdiff)
- **Compression**: brotli, zstd, unzip
- **Development**: build-essential, git, actionlint, shellcheck, shfmt, cmake, make, gcc, g++, cargo-binstall, pylint, ruff, uv, asciinema
- **Languages**: node (v24), npm, go, python3, java, typescript, eslint, tsx, rust (cargo, rustc), bun
- **AI Tools**: claude, gemini, codex, qwen-code, opencode, amp
- **Virtualization**: qemu-kvm, libvirt-clients, podman
- **Media**: ffmpeg, imagemagick
- **Containers**: podman
- **Android**: android-sdk, gradle, java, adb
- **Database**: sqlite3
- **Network**: curl, wget, net-tools, iproute2
- **Debugging**: gdb, strace, lsof
