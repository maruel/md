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

OUTPUT_FILE="/home/user/tool_versions.md"

{
	echo "# Image Tool Versions"
	echo "Generated on $(date)"
	echo ""

	check_version() {
		local name=$1
		local cmd=$2
		local version_flag=${3:---version}

		if command -v "$cmd" >/dev/null 2>&1; then
			echo "### $name"
			echo '```'
			# specific handling for some tools that output to stderr or have weird formats
			output=$("$cmd" "$version_flag" 2>&1)
			# trim whitespace
			echo "$output" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
			echo '```'
			echo ""
		else
			echo "### $name"
			echo "Not found"
			echo ""
		fi
	}

	# OS Info
	if [ -f /etc/os-release ]; then
		echo "### OS"
		echo '```'
		grep PRETTY_NAME /etc/os-release | cut -d= -f2 | tr -d '"'
		echo '```'
		echo ""
	fi

	# Languages
	if command -v go >/dev/null; then
		check_version "Go" "go" "version"
	fi

	if command -v python3 >/dev/null; then
		check_version "Python" "python3" "--version"
	fi

	if command -v node >/dev/null; then
		check_version "Node.js" "node" "--version"
	fi

	if command -v rustc >/dev/null; then
		check_version "Rust" "rustc" "--version"
	fi

	# Tools
	if command -v nvim >/dev/null; then
		check_version "Neovim" "nvim" "--version"
	fi

	if command -v firefox >/dev/null; then
		check_version "Firefox" "firefox" "--version"
	fi

	if command -v geckodriver >/dev/null; then
		check_version "Geckodriver" "geckodriver" "--version"
	fi

	# AI Tools
	if command -v claude >/dev/null; then
		check_version "Claude CLI" "claude" "--version"
	fi

	if command -v gemini >/dev/null; then
		check_version "Gemini CLI" "gemini" "--version"
	fi

} >"$OUTPUT_FILE"

echo "Report generated at $OUTPUT_FILE"
