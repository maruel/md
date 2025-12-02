#!/bin/bash
# Install Android SDK tools, emulator, and system images (runs as user).
set -euo pipefail

ANDROID_SDK_ROOT="$HOME/.local/share/android-sdk"
mkdir -p "$ANDROID_SDK_ROOT"
cd "$ANDROID_SDK_ROOT"

# Detect architecture and set appropriate system image ABI
ARCH=$(uname -m)
case "$ARCH" in
x86_64)
	SYS_IMAGE_ABI="x86_64"
	;;
aarch64)
	SYS_IMAGE_ABI="arm64-v8a"
	;;
*)
	echo "Unsupported architecture: $ARCH" >&2
	exit 1
	;;
esac

# Get the latest commandlinetools download URL dynamically
# linux/arm64 is still not supported; see https://issuetracker.google.com/issues/227219818
SDK_URL=$(curl -s "https://developer.android.com/studio" | grep -o 'https://dl\.google\.com/android/repository/commandlinetools-linux-[0-9]*_latest\.zip' | head -n 1)
if [ -z "$SDK_URL" ]; then
	echo "Failed to determine SDK URL" >&2
	exit 1
fi
wget -q "$SDK_URL" -O /tmp/cmdline-tools.zip
unzip -q /tmp/cmdline-tools.zip -d /tmp
rm /tmp/cmdline-tools.zip

# Move cmdline-tools to proper location (sdkmanager expects this structure)
mkdir -p "$ANDROID_SDK_ROOT/cmdline-tools"
mv /tmp/cmdline-tools "$ANDROID_SDK_ROOT/cmdline-tools/latest"
chmod 755 "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin"/*
SDKMANAGER="$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager"

# Accept all licenses
yes | "$SDKMANAGER" --licenses >/dev/null 2>&1 || true

# Install required SDK components
"$SDKMANAGER" \
	"build-tools;36.0.0" \
	"cmdline-tools;latest" \
	"emulator" \
	"platform-tools" \
	"platforms;android-36" \
	"system-images;android-36;google_apis;${SYS_IMAGE_ABI}"
