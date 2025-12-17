#!/bin/bash
# Install Claude Code, Gemini CLI, codex, goose (as user)
set -euo pipefail
echo "- $0"

cd $HOME

if ! which nvm &>/dev/null; then
	. ~/.nvm/nvm.sh
fi

npm install --silent --no-fund -g \
	@google/gemini-cli \
	@openai/codex \
	@qwen-code/qwen-code@latest \
	@sourcegraph/amp \
	opencode-ai \
	vscode-langservers-extracted

# This is SO annoying. What were they thinking?
ln -s "$HOME/.claude/claude.json" "$HOME/.claude.json"
curl -fsSL https://claude.ai/install.sh | bash

# curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | bash
