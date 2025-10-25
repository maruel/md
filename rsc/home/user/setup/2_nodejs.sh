#!/bin/bash
# Install nvm, node.js, npm, typescript, eslint (as user)

set -eu
cd $HOME

if ! which nvm &> /dev/null; then
	# TODO: Update from time to time.
	curl -sSL -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
	. ~/.nvm/nvm.sh
fi

if ! which node &> /dev/null; then
	# Lock to v24 as @sourcegraph/amp requires it as of 2025-10-15. Switch back to "node" to use latest.
	nvm install --no-progress v24
fi

npm install --silent --no-fund -g eslint tsx typescript typescript-eslint
