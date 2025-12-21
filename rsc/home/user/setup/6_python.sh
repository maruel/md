#!/bin/bash
# Install Python development tools (runs as user)
set -euo pipefail
echo "- $0"

# Install uv package manager
curl -LsSf https://astral.sh/uv/install.sh | sh -s -- --quiet

# shellcheck disable=SC1091
. "$HOME/.local/bin/env" 2>/dev/null || true

# Install Python development tools
uv tool install --quiet pylint
uv tool install --quiet ruff
