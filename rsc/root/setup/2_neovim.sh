#!/bin/bash
# Install the latest stable Neovim build and wire common aliases.
set -euo pipefail

ARCH="$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/')"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

CURL_ARGS=(-fsSL)
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
	CURL_ARGS+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

curl "${CURL_ARGS[@]}" -o "${TMPDIR}/nvim.tar.gz" "https://github.com/neovim/neovim/releases/latest/download/nvim-linux-${ARCH}.tar.gz"
mkdir -p /opt
tar xzf "${TMPDIR}/nvim.tar.gz" -C /opt --strip-components=1
ln -sf /opt/bin/nvim /usr/local/bin/nvim
ln -sf /opt/bin/nvim /usr/local/bin/vim
ln -sf /opt/bin/nvim /usr/local/bin/vi
