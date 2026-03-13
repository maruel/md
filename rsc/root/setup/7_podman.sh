#!/bin/bash
# Configure Podman for rootless container-in-container support.
set -euo pipefail
echo "- $0"

# Configure subuid/subgid for rootless Podman (user UID 1000).
usermod --add-subuids 100000-165535 user
usermod --add-subgids 100000-165535 user
