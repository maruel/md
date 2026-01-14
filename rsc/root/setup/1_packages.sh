#!/bin/bash
# Install core system packages (runs as root).
set -euo pipefail
echo "- $0"

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

echo "- apt-get update"
apt-get update -qq >/dev/null
echo "- apt-get upgrade"
apt-get upgrade -qq -y >/dev/null
echo "- apt-get install"
apt-get install -qq -y --no-install-recommends \
	bash-completion \
	brotli \
	bubblewrap \
	build-essential \
	ca-certificates \
	clang \
	cmake \
	cpu-checker \
	curl \
	ffmpeg \
	file \
	git \
	gpg \
	gradle \
	imagemagick \
	iproute2 \
	jq \
	kmod \
	less \
	libgl1 \
	librsvg2-bin \
	libssl-dev \
	libvirt-clients \
	libvirt-daemon \
	libvirt-daemon-system \
	locales \
	lsof \
	net-tools \
	openjdk-21-jdk-headless \
	openssh-server \
	pkg-config \
	podman \
	python-is-python3 \
	python3 \
	python3-venv \
	python3-yaml \
	qemu-kvm \
	qemu-system-arm \
	qemu-system-x86 \
	qemu-utils \
	ripgrep \
	rsync \
	shared-mime-info \
	shellcheck \
	sqlite3 \
	unzip \
	wget \
	xvfb \
	zstd >/dev/null

if ! grep -q '^en_US.UTF-8 UTF-8' /etc/locale.gen; then
	sed -i 's/^# *en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen || echo 'en_US.UTF-8 UTF-8' >>/etc/locale.gen
fi
locale-gen en_US.UTF-8
update-locale LANG=en_US.UTF-8 LC_CTYPE=en_US.UTF-8

apt-get clean
rm -rf /var/lib/apt/lists/*
