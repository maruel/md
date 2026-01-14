# Source: https://github.com/maruel/md

# codex is stupid and will always use a login bash shell.
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"

alias amp="\$(which amp) --dangerously-allow-all"
alias claude="\$(which claude) --dangerously-skip-permissions --allow-dangerously-skip-permissions"
alias codex="\$(which codex) --dangerously-bypass-approvals-and-sandbox"
alias gemini="\$(which gemini) --yolo"
alias qwen="\$(which qwen) --yolo"

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
