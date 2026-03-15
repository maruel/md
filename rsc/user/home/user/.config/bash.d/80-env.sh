# shellcheck disable=SC2148
# Source user environment files (API keys, etc.) for all shells.
if [ -f "${HOME}/.env" ]; then
	set -a
	# shellcheck source=/dev/null
	. "${HOME}/.env"
	set +a
fi

if [ -f "${HOME}/.config/md/env" ]; then
	set -a
	# shellcheck source=/dev/null
	. "${HOME}/.config/md/env"
	set +a
fi
