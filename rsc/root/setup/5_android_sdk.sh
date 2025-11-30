#!/bin/bash
# Install Android SDK and gradle (runs as root).
set -euo pipefail

SDK_URL=$(curl -s "https://developer.android.com/studio" | grep -o 'https://dl\.google\.com/android/repository/commandlinetools-linux-[0-9]*_latest\.zip' | head -n 1)

ANDROID_SDK_ROOT=/opt/android-sdk
mkdir -p "$ANDROID_SDK_ROOT"
cd "$ANDROID_SDK_ROOT"

# Download and extract command-line tools
wget -q "$SDK_URL" -O /tmp/cmdline-tools.zip
unzip -q /tmp/cmdline-tools.zip -d /tmp
rm /tmp/cmdline-tools.zip

# Move cmdline-tools to proper location (sdkmanager expects this structure)
mkdir -p "$ANDROID_SDK_ROOT/cmdline-tools"
mv /tmp/cmdline-tools "$ANDROID_SDK_ROOT/cmdline-tools/latest"

chmod 755 "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin"/*

# Accept all licenses
yes | "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager" --licenses >/dev/null 2>&1 || true

# Install required SDK components
"$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager" \
	"platform-tools" \
	"platforms;android-34" \
	"build-tools;34.0.0" \
	"cmdline-tools;latest"

# Symlink command-line tools to PATH
ln -sf "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin"/* /usr/local/bin/
