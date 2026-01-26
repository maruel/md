#!/bin/bash
# Patch /etc/skel/.bashrc for non-interactive SSH commands (runs as root).
set -euo pipefail
echo "- $0"

# Comment out the early return for non-interactive shells so the full bashrc
# runs for SSH commands like "md run". This ensures PATH and aliases are set.
#
# This has the drawback of bash invocation being slower but this unblocks
# weird issues like having tools working in an ssh session.
sed -i '/^# If not running interactively/,/^esac$/s/^/#/' /etc/skel/.bashrc

# Add sourcing of ~/.config/bash.d/*.sh for modular bashrc extensions.
sed -i '/^#esac$/a\
\
# Source user bash extensions from ~/.config/bash.d/\
if [ -d "$HOME/.config/bash.d" ]; then\
  for i in "$HOME/.config/bash.d"/*.sh; do\
    [ -r "$i" ] \&\& . "$i"\
  done\
  unset i\
fi' /etc/skel/.bashrc
