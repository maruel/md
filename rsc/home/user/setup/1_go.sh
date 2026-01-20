#!/bin/bash
# Install the latest Go toolchain reported by go.dev and set up go tools.
set -euo pipefail
echo "- $0"

ARCH="$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/')"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

GO_VERSION="$(curl -fsSL https://go.dev/VERSION?m=text | head -n1 | tr -d '\r')"
if [[ -z "${GO_VERSION}" ]]; then
	echo "Failed to resolve Go version" >&2
	exit 1
fi

# Install Go to user's home directory
curl -fsSL -o "${TMPDIR}/go.tar.gz" "https://go.dev/dl/${GO_VERSION}.linux-${ARCH}.tar.gz"
rm -rf "$HOME/.local/go"
mkdir -p "$HOME/.local"
tar -C "$HOME/.local" -xzf "${TMPDIR}/go.tar.gz" --transform='s,^go,go,'

# Update PATH for this session
export PATH="$HOME/.local/go/bin:$PATH"

# Set up go tools
mkdir -p "$HOME/go/bin"
go install github.com/maruel/ask@latest
go install github.com/mikefarah/yq/v4@latest
go install github.com/rhysd/actionlint/cmd/actionlint@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install golang.org/x/tools/cmd/goimports@latest
go install golang.org/x/tools/gopls@latest
go install mvdan.cc/sh/v3/cmd/shfmt@latest

curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$HOME/go/bin"

go clean -cache -testcache -modcache
