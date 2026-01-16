#!/bin/bash
# Install Google Chrome via extrepo (runs as root).
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

sed -i 's/^# - /- /g' /etc/extrepo/config.yaml
extrepo enable google_chrome
apt-get update -qq >/dev/null
apt-get install -qq -y --no-install-recommends google-chrome-stable >/dev/null
