# shellcheck disable=SC2148
# Common shell defaults for interactive sessions.

export SHELL="${SHELL:-/bin/bash}"
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$HOME/.local/bin:$PATH"
export EDITOR=nvim

if command -v dircolors >/dev/null 2>&1; then
	eval "$(dircolors)"
fi

case $- in
*i*)
	export LS_OPTIONS="--color=auto"
	alias ls='ls $LS_OPTIONS'
	alias ll='ls $LS_OPTIONS -la'
	alias vimdiff='nvim -d'
	if [ -n "$MD_REPO_DIR" ] && [ -d "./$MD_REPO_DIR" ]; then
		# shellcheck disable=SC2164
		cd "./$MD_REPO_DIR"
	fi
	;;
*)
	: # Non-interactive shell; skip prompt niceties.
	;;
esac
