# shellcheck disable=SC2148
# Common shell defaults.

export SHELL="${SHELL:-/bin/bash}"
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$HOME/.local/bin:$PATH"
export EDITOR=nvim

alias ll='ls --color=auto -la'
alias vimdiff='nvim -d'

if [ -n "$MD_REPO_DIR" ] && [ -d "$HOME/src/$MD_REPO_DIR" ]; then
	# shellcheck disable=SC2164
	cd "$HOME/src/$MD_REPO_DIR"
fi
