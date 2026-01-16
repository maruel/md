#!/bin/bash
# Install Google Chrome via extrepo (runs as root).
# Only supports amd64 as of 2026-01-16 (arm64 version not available).
set -euo pipefail
echo "- $0"

# Check architecture
if [[ "$(dpkg --print-architecture)" != "amd64" ]]; then
	echo "Skipping: Google Chrome only available for amd64"
	exit 0
fi

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

sed -i 's/^# - /- /g' /etc/extrepo/config.yaml
extrepo enable google_chrome
apt-get update -qq >/dev/null
apt-get install -qq -y --no-install-recommends google-chrome-stable >/dev/null
