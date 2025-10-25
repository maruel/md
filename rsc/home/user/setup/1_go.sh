#!/bin/bash
# Set up go.
set -eu

if ! which go &> /dev/null; then
	PATH=$PATH:/usr/local/go/bin
fi

mkdir -p $HOME/go/bin
go install golang.org/x/tools/cmd/goimports@latest
go install golang.org/x/tools/gopls@latest
go clean -cache -testcache -modcache
