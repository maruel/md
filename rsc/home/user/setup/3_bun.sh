#!/bin/bash
# Install Bun (as user)
set -euo pipefail
echo "- $0"

cd "$HOME"

curl -fsSL https://bun.sh/install | bash
