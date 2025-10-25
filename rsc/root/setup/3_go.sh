#!/bin/bash
# Install the latest Go toolchain reported by go.dev.
set -euo pipefail

ARCH="$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/')"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

GO_VERSION="$(curl -sSL https://go.dev/VERSION?m=text | head -n1 | tr -d '\r')"
if [[ -z "${GO_VERSION}" ]]; then
	echo "Failed to resolve Go version" >&2
	exit 1
fi

curl -sSL -o "${TMPDIR}/go.tar.gz" "https://go.dev/dl/${GO_VERSION}.linux-${ARCH}.tar.gz"
rm -rf /usr/local/go
tar -C /usr/local -xzf "${TMPDIR}/go.tar.gz"
