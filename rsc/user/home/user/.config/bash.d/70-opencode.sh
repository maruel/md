# shellcheck disable=SC2148
# Add OpenCode paths.
if [ -d "${HOME}/.opencode/bin" ]; then
	PATH="${HOME}/.opencode/bin:${PATH}"
fi
export PATH
