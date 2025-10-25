#!/bin/bash
# Enable git completion when bash-completion provides it.
if [ -f /usr/share/bash-completion/completions/git ]; then
	. /usr/share/bash-completion/completions/git
fi
