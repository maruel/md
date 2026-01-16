#!/bin/bash
# Install Google Chrome (amd64) or Chromium (other architectures).
# Google Chrome only available for amd64 as of 2026-01-16.
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

ARCH="$(dpkg --print-architecture)"

if [[ "$ARCH" == "amd64" ]]; then
	# Install Google Chrome on amd64
	extrepo enable google_chrome
	apt-get update -qq >/dev/null
	apt-get install -qq -y --no-install-recommends google-chrome-stable >/dev/null
else
	# Install Chromium on non-amd64 architectures
	apt-get install -qq -y --no-install-recommends chromium-browser >/dev/null
fi
