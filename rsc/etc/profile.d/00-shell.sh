# Common shell defaults for interactive sessions.

export SHELL="${SHELL:-/bin/bash}"

if command -v dircolors >/dev/null 2>&1; then
	eval "$(dircolors)"
fi

case $- in
*i*)
	export LS_OPTIONS="--color=auto"
	alias ls='ls $LS_OPTIONS'
	alias ll='ls $LS_OPTIONS -la'
	alias vimdiff='nvim -d'
	if [ -d /app ]; then
		cd /app
	fi
	;;
*)
	: # Non-interactive shell; skip prompt niceties.
	;;
esac
