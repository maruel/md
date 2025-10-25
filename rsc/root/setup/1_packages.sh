#!/bin/bash
# Install core system packages (runs as root).
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

apt-get clean
rm -rf /var/lib/apt/lists/*
