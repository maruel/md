#!/bin/bash
# Run user setup scripts in parallel to improve build time.
set -euo pipefail

# Set GITHUB_TOKEN for all scripts if the secret is available
if [ -f /run/secrets/github_token ]; then
	token="$(cat /run/secrets/github_token)"
	if [ -n "$token" ]; then
		export GITHUB_TOKEN="$token"
	fi
fi

SCRIPTS=(
	'Go' /home/user/setup/1_go.sh
	'Node.js' /home/user/setup/2_nodejs.sh
	'Bun' /home/user/setup/3_bun.sh
	'Android SDK' /home/user/setup/4_android.sh
	'Rust' /home/user/setup/5_rust.sh
	'Python' /home/user/setup/6_python.sh
	'LLM Tools' /home/user/setup/7_llm_tools.sh
)

if [ "${MD_SERIAL_SETUP:-0}" = "1" ]; then
	echo "- $0: Starting serial setup (MD_SERIAL_SETUP=1)..."
	for ((i = 0; i < ${#SCRIPTS[@]}; i += 2)); do
		measure_exec.sh "${SCRIPTS[i]}" "${SCRIPTS[i + 1]}"
	done
else
	echo "- $0: Starting parallel setup..."
	pids=()
	pid_names=()
	for ((i = 0; i < ${#SCRIPTS[@]}; i += 2)); do
		measure_exec.sh "${SCRIPTS[i]}" "${SCRIPTS[i + 1]}" &
		pids+=($!)
		pid_names+=("${SCRIPTS[i]}")
	done

	failed_names=()
	for ((i = 0; i < ${#pids[@]}; i++)); do
		wait "${pids[i]}" || failed_names+=("${pid_names[i]}")
	done

	if [ "${#failed_names[@]}" -ne 0 ]; then
		echo "Error: Setup script(s) failed: ${failed_names[*]}" >&2
		exit 1
	fi
fi

# Remove lines that installers (nvm, bun, opencode) appended to .bashrc.
# These are now handled by ~/.config/bash.d/ scripts sourced via /etc/bash_env.
measure_exec.sh "Bashrc Cleanup" /home/user/setup/bashrc_cleanup.sh

echo "- $0: All setup scripts completed successfully."
