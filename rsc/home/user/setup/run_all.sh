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

echo "- $0: Starting parallel setup..."

pids=()

measure_exec.sh 'Go' /home/user/setup/1_go.sh &
pids+=($!)

measure_exec.sh 'Node.js' /home/user/setup/2_nodejs.sh &
pids+=($!)

measure_exec.sh 'Bun' /home/user/setup/3_bun.sh &
pids+=($!)

measure_exec.sh 'Android SDK' /home/user/setup/4_android.sh &
pids+=($!)

measure_exec.sh 'Rust' /home/user/setup/5_rust.sh &
pids+=($!)

measure_exec.sh 'Python' /home/user/setup/6_python.sh &
pids+=($!)

measure_exec.sh 'LLM Tools' /home/user/setup/7_llm_tools.sh &
pids+=($!)

FAILED=0
for pid in "${pids[@]}"; do
    wait "$pid" || FAILED=1
done

if [ "$FAILED" -ne 0 ]; then
    echo "Error: One or more setup scripts failed." >&2
    exit 1
fi

echo "- $0: All setup scripts completed successfully."
