#!/bin/bash
# Run user setup scripts in parallel to improve build time.
# Groups dependent scripts (Node -> LLM) and handles independent ones concurrently.
set -euo pipefail

echo "- $0: Starting parallel setup..."

# Array to store background process IDs
pids=()

# 1. Go (Independent)
measure_exec.sh 'Go' /home/user/setup/1_go.sh &
pids+=($!)

# 2. Node.js -> LLM Tools (Sequential dependency)
(
    if measure_exec.sh 'Node.js' /home/user/setup/2_nodejs.sh; then
        measure_exec.sh 'LLM Tools' /home/user/setup/3_llm.sh
    else
        exit 1
    fi
) &
pids+=($!)

# 3. Android SDK (Independent)
measure_exec.sh 'Android SDK' /home/user/setup/4_android.sh &
pids+=($!)

# 4. Rust (Independent, requires secret)
(
    # Read secret if available, usually mounted at /run/secrets/github_token
    export GITHUB_TOKEN="$(cat /run/secrets/github_token 2>/dev/null || true)"
    measure_exec.sh "Rust" /home/user/setup/5_rust.sh
) &
pids+=($!)

# 5. Python (Independent)
measure_exec.sh 'Python' /home/user/setup/6_python.sh &
pids+=($!)

# Wait for all background processes and check exit status
FAILED=0
for pid in "${pids[@]}"; do
    wait "$pid" || FAILED=1
done

if [ "$FAILED" -ne 0 ]; then
    echo "Error: One or more setup scripts failed." >&2
    exit 1
fi

echo "- $0: All setup scripts completed successfully."
