# shellcheck shell=bash
# Source: https://github.com/maruel/md

# codex is stupid and will always use a login bash shell.
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"

alias amp="$(command -v amp 2>/dev/null || echo amp) --dangerously-allow-all"
alias claude="$(command -v claude 2>/dev/null || echo claude) --dangerously-skip-permissions --allow-dangerously-skip-permissions --permission-mode dontAsk"
alias codex="$(command -v codex 2>/dev/null || echo codex) --dangerously-bypass-approvals-and-sandbox"
alias gemini="$(command -v gemini 2>/dev/null || echo gemini) --yolo"
alias qwen="$(command -v qwen 2>/dev/null || echo qwen) --yolo"

if [ -f "$HOME/.env" ]; then
	set -a
	# shellcheck source=/dev/null
	source "$HOME/.env"
	set +a
fi

if [ -f "$HOME/.config/md/env" ]; then
	set -a
	# shellcheck source=/dev/null
	source "$HOME/.config/md/env"
	set +a
fi
