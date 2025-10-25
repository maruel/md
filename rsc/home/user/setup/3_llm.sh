#!/bin/bash
# Install Claude Code, Gemini CLI, codex, goose (as user)

set -eu
cd $HOME

if ! which nvm &> /dev/null; then
	. ~/.nvm/nvm.sh
fi

npm install --silent --no-fund -g \
	@anthropic-ai/claude-code \
	@google/gemini-cli \
	@openai/codex \
	@qwen-code/qwen-code@latest \
	@sourcegraph/amp \
	vscode-langservers-extracted

# curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | bash
