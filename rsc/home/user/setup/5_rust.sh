#!/bin/bash
# Install the latest Rust toolchain via rustup.
set -euo pipefail
echo "- $0"

# Install rustup
curl -fsSL https://sh.rustup.rs | sh -s -- -y --no-modify-path

# Update PATH for this session
export PATH="$HOME/.cargo/bin:$PATH"

# Install additional rust tools
rustup component add clippy rust-analyzer rustfmt

# Install cargo-binstall for fast binary installations
curl -L --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/cargo-bins/cargo-binstall/main/install-from-binstall-release.sh | bash

# Install tools using binstall to avoid compiling from source
cargo binstall -y cargo-edit cargo-update asciinema
