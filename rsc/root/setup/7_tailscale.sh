#!/bin/bash
# Install Tailscale for optional VPN/networking support.
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

extrepo enable tailscale
apt-get update -qq >/dev/null
apt-get install -qq -y --no-install-recommends tailscale >/dev/null
