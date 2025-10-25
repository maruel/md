#!/bin/bash
# Install Firefox and the latest Geckodriver release from GitHub (runs as root).
set -euo pipefail

export DEBIAN_FRONTEND="${DEBIAN_FRONTEND:-noninteractive}"

# Install Firefox from Mozilla's repository.
install -d -m 0755 /etc/apt/keyrings
curl -sSL -o /etc/apt/keyrings/packages.mozilla.org.asc https://packages.mozilla.org/apt/repo-signing-key.gpg
cat >/etc/apt/sources.list.d/mozilla.list <<'EOF'
deb [signed-by=/etc/apt/keyrings/packages.mozilla.org.asc] https://packages.mozilla.org/apt mozilla main
EOF
cat >/etc/apt/preferences.d/mozilla <<'EOF'
Package: *
Pin: origin packages.mozilla.org
Pin-Priority: 1000
EOF

apt-get update -q
apt-get install -q -y --no-install-recommends firefox

API_URL="https://api.github.com/repos/mozilla/geckodriver/releases/latest"
AUTH_HEADER=()
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
	AUTH_HEADER=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

GECKODRIVER_URL="$(
	curl -fsSL "${AUTH_HEADER[@]}" -H 'Accept: application/vnd.github+json' "${API_URL}" |
		jq -r '.assets[]? | select(.name | test("linux64\\.tar\\.gz$")) | .browser_download_url' |
		head -n1
)"
if [[ -z "${GECKODRIVER_URL}" ]]; then
	echo "Failed to determine Geckodriver download URL from ${API_URL}" >&2
	exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

curl -fsSL "${GECKODRIVER_URL}" -o "${TMP_DIR}/geckodriver.tar.gz"
tar -xzf "${TMP_DIR}/geckodriver.tar.gz" -C "${TMP_DIR}"
install -m 0755 "${TMP_DIR}/geckodriver" /usr/local/bin/geckodriver

apt-get clean
rm -rf /var/lib/apt/lists/*
