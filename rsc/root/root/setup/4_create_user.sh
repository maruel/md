#!/bin/bash
# Create the unprivileged user account.
set -euo pipefail
echo "- $0"

# Fix the image's first regular Linux account at the conventional 1000:1000;
# rootless Podman maps any host UID/GID to this container identity.
groupadd --gid 1000 user
useradd --uid 1000 --gid 1000 -ms /bin/bash user
