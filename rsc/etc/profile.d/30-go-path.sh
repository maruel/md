# shellcheck disable=SC2148
# Add Go toolchain paths only when the directories are present.
if [ -d "${HOME}/.local/go/bin" ]; then
	PATH="${HOME}/.local/go/bin:${PATH}"
fi
if [ -d "${HOME}/go/bin" ]; then
	PATH="${HOME}/go/bin:${PATH}"
fi
export PATH
