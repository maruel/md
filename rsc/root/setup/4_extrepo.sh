#!/bin/bash
# Install extrepo packages: Google Chrome (amd64 only), GitHub CLI, Tailscale.
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

ARCH="$(dpkg --print-architecture)"
PACKAGES=(gh tailscale)

if [[ "$ARCH" == "amd64" ]]; then
	# Google Chrome only available for amd64 as of 2026-01-16.
	extrepo enable google_chrome
	PACKAGES+=(google-chrome-stable)
fi
extrepo enable github-cli
extrepo enable tailscale
apt-get update -qq >/dev/null
apt-get install -qq -y --no-install-recommends "${PACKAGES[@]}" >/dev/null
