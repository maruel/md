#!/bin/bash
# Generate version report for installed tools
# Should be run as 'user' to access user-installed tools (go, node, rust, etc.)

# Load user environment if possible
export HOME="/home/user"
# Source bashrc to get PATHs if set there
if [ -f "$HOME/.bashrc" ]; then
	# shellcheck disable=SC1091
	source "$HOME/.bashrc" 2>/dev/null || true
fi
# Explicitly source known config files if bashrc didn't catch them (non-interactive shell issues)
if [ -f "$HOME/.nvm/nvm.sh" ]; then
	# shellcheck disable=SC1091
	source "$HOME/.nvm/nvm.sh"
fi
if [ -f "$HOME/.cargo/env" ]; then
	# shellcheck disable=SC1091
	source "$HOME/.cargo/env"
fi
if [ -d "$HOME/.local/go/bin" ]; then
	export PATH="$HOME/.local/go/bin:$PATH"
fi
if [ -d "$HOME/.local/bin" ]; then
	export PATH="$HOME/.local/bin:$PATH"
fi
if [ -d "$HOME/.local/share/android-sdk/platform-tools" ]; then
	export PATH="$HOME/.local/share/android-sdk/platform-tools:$PATH"
fi
if [ -d "$HOME/.bun/bin" ]; then
	export PATH="$HOME/.bun/bin:$PATH"
fi

OUTPUT_FILE="/var/log/tool_versions.md"

{
	echo "# Image Tool Versions"
	echo "Generated on $(date)"
	echo ""
	echo "| Tool | Version |"
	echo "| :--- | :--- |"

	check_version() {
		local name=$1
		local cmd=$2
		local version_flag=${3:---version}

		if command -v "$cmd" >/dev/null 2>&1; then
			local version
			# specific handling for some tools that output to stderr or have weird formats
			version=$("$cmd" "$version_flag" 2>&1 | head -n 1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
			# Escape pipe symbols for markdown table
			version=${version//|/\\|}
			echo "| $name | $version |"
		else
			echo "| $name | Not found |"
		fi
	}

	# OS Info
	if [ -f /etc/os-release ]; then
		OS=$(grep PRETTY_NAME /etc/os-release | cut -d= -f2 | tr -d '"')
		echo "| OS | $OS |"
	fi

	# Languages
	check_version "Go" "go" "version"
	check_version "Python" "python3" "--version"
	check_version "Node.js" "node" "--version"
	check_version "Rust" "rustc" "--version"
	check_version "Java" "java" "-version"
	check_version "TypeScript" "tsc" "--version"
	check_version "Bun" "bun" "--version"

	# Build Tools
	check_version "Git" "git" "--version"
	check_version "Make" "make" "--version"
	check_version "CMake" "cmake" "--version"
	check_version "GCC" "gcc" "--version"
	check_version "G++" "g++" "--version"

	# Utilities
	check_version "ShellCheck" "shellcheck" "--version"
	check_version "jq" "jq" "--version"
	check_version "actionlint" "actionlint" "--version"
	check_version "curl" "curl" "--version"
	check_version "SQLite" "sqlite3" "--version"
	check_version "asciinema" "asciinema" "--version"

	# Editors / Tools
	check_version "Neovim" "nvim" "--version"
	# check_version "Firefox" "firefox" "--version"
	# check_version "Geckodriver" "geckodriver" "--version"

	# Python Tools
	check_version "uv" "uv" "--version"
	check_version "Pylint" "pylint" "--version"
	check_version "Ruff" "ruff" "--version"

	# Android
	check_version "ADB" "adb" "version"
	ANDROID_SDK_ROOT="$HOME/.local/share/android-sdk"
	if [ -d "$ANDROID_SDK_ROOT/build-tools" ]; then
		# shellcheck disable=SC2012
		VERSION=$(ls -1 "$ANDROID_SDK_ROOT/build-tools" 2>/dev/null | sort -V | tail -n 1)
		if [ -n "$VERSION" ]; then
			echo "| Android Build-Tools | $VERSION |"
		fi
	fi
	if [ -d "$ANDROID_SDK_ROOT/platforms" ]; then
		# shellcheck disable=SC2012
		VERSION=$(ls -1 "$ANDROID_SDK_ROOT/platforms" 2>/dev/null | sort -V | tail -n 1)
		if [ -n "$VERSION" ]; then
			echo "| Android Platform | $VERSION |"
		fi
	fi

	# AI Tools
	check_version "Claude CLI" "claude" "--version"
	check_version "Gemini CLI" "gemini" "--version"
	check_version "Codex" "codex" "--version"
	check_version "Qwen Code" "qwen" "--version"
	check_version "OpenCode" "opencode" "--version"
	check_version "Amp" "amp" "--version"
	check_version "Letta Code" "letta" "--version"
	check_version "ESLint" "eslint" "--version"
	check_version "tsx" "tsx" "--version"

} >"$OUTPUT_FILE"

echo "Report generated at $OUTPUT_FILE"
