#!/bin/bash
# Install core system packages (runs as root).
set -euo pipefail

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

apt-get update -qq
apt-get upgrade -qq -y
apt-get install -qq -y --no-install-recommends \
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
	locales \
	net-tools \
	openssh-server \
	podman \
	python3 \
	python3-venv \
	ripgrep \
	rsync \
	shared-mime-info \
	shellcheck \
	sqlite3 \
	wget \
	xvfb \
	zstd

if ! grep -q '^en_US.UTF-8 UTF-8' /etc/locale.gen; then
	sed -i 's/^# *en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen || echo 'en_US.UTF-8 UTF-8' >> /etc/locale.gen
fi
locale-gen en_US.UTF-8
update-locale LANG=en_US.UTF-8 LC_CTYPE=en_US.UTF-8

apt-get clean
rm -rf /var/lib/apt/lists/*
