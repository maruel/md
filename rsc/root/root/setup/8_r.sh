#!/bin/bash
# Install R (runs as root).
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

apt-get install -qq -y --no-install-recommends r-base >/dev/null
