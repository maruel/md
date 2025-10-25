#!/bin/bash
# Install the configured Go toolchain version for all users.
set -euo pipefail

: "${GO_VERSION:?GO_VERSION must be set}"

ARCH="$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/')"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

curl -sSL -o "${TMPDIR}/go.tar.gz" "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz"
rm -rf /usr/local/go
tar -C /usr/local -xzf "${TMPDIR}/go.tar.gz"
