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
cargo install cargo-edit cargo-update

# Install asciinema from source at the moment since it's not the right package;
cargo install --locked --quiet --git https://github.com/asciinema/asciinema asciinema
