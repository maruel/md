#!/bin/bash
# Install Claude Code, Gemini CLI, codex, goose (as user)
set -euo pipefail
echo "- $0"

cd "$HOME"

if ! which nvm &>/dev/null; then
	# shellcheck disable=SC1090
	. ~/.nvm/nvm.sh
fi

npm install --silent --no-fund -g \
	@google/gemini-cli \
	@letta-ai/letta-code \
	@openai/codex \
	@qwen-code/qwen-code@latest \
	vscode-langservers-extracted

# Install OpenCode and Amp
curl -fsSL https://opencode.ai/install | bash
curl -fsSL https://ampcode.com/install.sh | bash

# This is SO annoying. What were they thinking?
ln -s "$HOME/.claude/claude.json" "$HOME/.claude.json"
curl -fsSL https://claude.ai/install.sh | bash
rm "$HOME/.claude.json"
ln -s "$HOME/.claude/claude.json" "$HOME/.claude.json"

# curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | bash
