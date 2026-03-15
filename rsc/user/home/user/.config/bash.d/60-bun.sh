# shellcheck disable=SC2148
# Add Bun paths.
if [ -d "${HOME}/.bun/bin" ]; then
	PATH="${HOME}/.bun/bin:${PATH}"
fi
export PATH
