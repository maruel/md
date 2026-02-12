#!/bin/bash
# Install nvm, node.js, npm, typescript, eslint, MCP servers, and global LLM packages (as user)
set -euo pipefail
echo "- $0"

cd "$HOME"

# 1. Setup Node.js via NVM
if ! which nvm &>/dev/null; then
	# TODO: Update from time to time.
	curl -sSL -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
	# shellcheck disable=SC1090
	. ~/.nvm/nvm.sh
fi

if ! which node &>/dev/null; then
	# Lock to v24 as @sourcegraph/amp requires it as of 2025-10-15. Switch back to "node" to use latest.
	nvm install --no-progress v24
fi

corepack enable pnpm
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$PATH"

# 2. Install Global Node Packages
pnpm add -g \
	@google/gemini-cli \
	@kilocode/cli \
	@mariozechner/pi-coding-agent \
	@openai/codex \
	@qwen-code/qwen-code \
	chrome-devtools-mcp \
	eslint \
	prettier \
	tsx \
	typescript \
	typescript-eslint \
	vscode-langservers-extracted
