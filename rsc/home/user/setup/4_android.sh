#!/bin/bash
# Install Android SDK tools, emulator, and system images on x64 (runs as user).
set -euo pipefail
echo "- $0"

# Detect architecture and set appropriate system image ABI.
ARCH=$(uname -m)
# linux/arm64 is still not supported; see https://issuetracker.google.com/issues/227219818
if [ "$ARCH" == "aarch64" ]; then
	echo "  Skipping Android SDK installation on $ARCH"
	exit 0
fi

ANDROID_SDK_ROOT="$HOME/.local/share/android-sdk"
mkdir -p "$ANDROID_SDK_ROOT"
cd "$ANDROID_SDK_ROOT"

# Get the latest commandlinetools download URL dynamically
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

echo "Determining latest SDK versions..."
AVAILABLE_PACKAGES=$("$SDKMANAGER" --list | grep '^[[:space:]]' | awk '{print $1}')

LATEST_BUILD_TOOLS=$(echo "$AVAILABLE_PACKAGES" | grep -E '^build-tools;[0-9.]+$' | sort -V | tail -n 1)
LATEST_PLATFORM=$(echo "$AVAILABLE_PACKAGES" | grep -E '^platforms;android-[0-9]+$' | sort -V | tail -n 1)

if [ -z "$LATEST_BUILD_TOOLS" ] || [ -z "$LATEST_PLATFORM" ]; then
	echo "Error: Could not determine latest Android SDK versions."
	exit 1
fi

echo "Selected Build Tools: $LATEST_BUILD_TOOLS"
echo "Selected Platform: $LATEST_PLATFORM"

# shellcheck disable=SC2034
PLATFORM_VERSION=${LATEST_PLATFORM#platforms;}

# Install required SDK components (emulator and system-images not available on arm64)
SDK_PACKAGES=(
	"$LATEST_BUILD_TOOLS"
	"platform-tools"
	"$LATEST_PLATFORM"
)

# Emulator and system images take 3GiB which is a bit too large to always include in the base image.
# if [ "$ARCH" != "aarch64" ]; then
#   case "$ARCH" in
#   x86_64)
#   	SYS_IMAGE_ABI="x86_64"
#   	;;
#   aarch64)
#   	SYS_IMAGE_ABI="arm64-v8a"
#   	;;
#   *)
#   	echo "Unsupported architecture: $ARCH" >&2
#   	exit 1
#   	;;
#   esac
#	  SDK_PACKAGES+=(
#	 	  "system-images;${PLATFORM_VERSION};google_apis;${SYS_IMAGE_ABI}"
# 	  "emulator"
#   )
# fi

"$SDKMANAGER" "${SDK_PACKAGES[@]}"
