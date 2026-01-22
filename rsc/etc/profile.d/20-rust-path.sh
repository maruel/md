# shellcheck disable=SC2148
# Add Rust toolchain paths only when the directories are present.
if [ -d "${HOME}/.cargo/bin" ]; then
	PATH="${HOME}/.cargo/bin:${PATH}"
fi
export PATH
