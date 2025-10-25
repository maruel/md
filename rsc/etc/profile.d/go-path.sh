#!/bin/bash
# Add Go toolchain paths only when the directories are present.
if [ -d /usr/local/go/bin ]; then
	PATH="/usr/local/go/bin:${PATH}"
fi
if [ -d "${HOME}/go/bin" ]; then
	PATH="${HOME}/go/bin:${PATH}"
fi
export PATH
