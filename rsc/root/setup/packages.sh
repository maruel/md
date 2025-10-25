#!/bin/bash
# Install core system packages, Firefox, and Geckodriver (runs as root).
set -euo pipefail

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

apt-get update -q
apt-get upgrade -q -y
apt-get install -q -y --no-install-recommends \
	bash-completion \
	brotli \
	bubblewrap \
	build-essential \
	ca-certificates \
	curl \
	ffmpeg \
	file \
	git \
	gpg \
	iproute2 \
	jq \
	less \
	lsof \
	net-tools \
	openssh-server \
	podman \
	python3 \
	python3-venv \
	ripgrep \
	rsync \
	shared-mime-info \
	sqlite3 \
	wget \
	xvfb \
	zstd

# Install Firefox from Mozilla's repository.
install -d -m 0755 /etc/apt/keyrings
curl -sSL -o /etc/apt/keyrings/packages.mozilla.org.asc https://packages.mozilla.org/apt/repo-signing-key.gpg
cat >/etc/apt/sources.list.d/mozilla.list <<'EOF'
deb [signed-by=/etc/apt/keyrings/packages.mozilla.org.asc] https://packages.mozilla.org/apt mozilla main
EOF
cat >/etc/apt/preferences.d/mozilla <<'EOF'
Package: *
Pin: origin packages.mozilla.org
Pin-Priority: 1000
EOF

apt-get update -q
apt-get install -q -y --no-install-recommends firefox

# Install the latest Geckodriver release.
GECKODRIVER_URL="$(curl -sSL https://api.github.com/repos/mozilla/geckodriver/releases/latest | jq -r '.assets[] | select(.name | contains("linux64")) | .browser_download_url' | grep -v '\.asc$')"
curl -sSL "${GECKODRIVER_URL}" | tar xz -C /usr/local/bin
chmod +x /usr/local/bin/geckodriver

apt-get clean
rm -rf /var/lib/apt/lists/*
