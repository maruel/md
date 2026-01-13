#!/usr/bin/env bash
set -eu

# Validate required environment variables
if [ -z "$GITHUB_REPOSITORY_OWNER" ]; then
	echo "Error: GITHUB_REPOSITORY_OWNER is not set."
	exit 1
fi

if [ -z "$GITHUB_REPOSITORY" ]; then
	echo "Error: GITHUB_REPOSITORY is not set."
	exit 1
fi

if [ -z "$GITHUB_TOKEN" ]; then
	echo "Error: GITHUB_TOKEN is not set."
	exit 1
fi

# Ensure jq is installed
if ! command -v jq &>/dev/null; then
	echo "Error: jq is required but not installed."
	exit 1
fi

OWNER="$GITHUB_REPOSITORY_OWNER"
# GITHUB_REPOSITORY comes as "owner/repo", we need just "repo"
REPO="${GITHUB_REPOSITORY#*/}"

echo "Starting cleanup for image package: $OWNER/$REPO"

CUTOFF_DATE=$(date -d '30 days ago' +%s)
echo "Cutoff date: $(date -d @"$CUTOFF_DATE")"

# Get all package versions with pagination
PAGE=1
while true; do
	echo "Processing page $PAGE..."

	RESPONSE=$(curl -s -H "Accept: application/vnd.github.v3+json" \
		-H "Authorization: token ${GITHUB_TOKEN}" \
		"https://api.github.com/users/$OWNER/packages/container/$REPO/versions?per_page=100&page=$PAGE")

	# Check if response is valid JSON and has items
	LENGTH=$(echo "$RESPONSE" | jq length 2>/dev/null)

	# If jq failed or length is empty/0, we are done
	if [ -z "$LENGTH" ] || [ "$LENGTH" -eq 0 ]; then
		echo "No more versions found on page $PAGE."
		break
	fi

	# Parse versions
	# We use a temporary file or process substitution to avoid pipe subshell issues if we were setting variables,
	# but here we just read.
	VERSIONS=$(echo "$RESPONSE" | jq -r '.[] | "\(.id)|\(.name)|\(.created_at)" ')

	while IFS='|' read -r VERSION_ID TAG CREATED_AT; do
		[ -z "$VERSION_ID" ] && continue

		# Skip special tags
		if [ "$TAG" = "latest" ]; then
			echo "Skipping tag: latest"
			continue
		fi

		# Skip semantic version tags (v1, v1.2, v1.2.3, etc)
		if [[ "$TAG" =~ ^v[0-9]+ ]]; then
			echo "Skipping tag: $TAG"
			continue
		fi

		CREATED=$(date -d "$CREATED_AT" +%s)

		if [ "$CREATED" -lt "$CUTOFF_DATE" ]; then
			echo "Deleting image: $TAG (created: $(date -d @"$CREATED"))"

			DELETE_STATUS=$(curl -s -o /dev/null -w "% {http_code}" -X DELETE \
				-H "Authorization: token ${GITHUB_TOKEN}" \
				"https://api.github.com/users/$OWNER/packages/container/$REPO/versions/$VERSION_ID")

			if [ "$DELETE_STATUS" -eq 204 ]; then
				echo "Deleted successfully"
			else
				echo "Failed to delete $TAG (HTTP Status: $DELETE_STATUS)"
			fi
		else
			echo "Keeping image: $TAG (created: $(date -d @"$CREATED"))"
		fi
	done <<<"$VERSIONS"

	PAGE=$((PAGE + 1))
done

echo "Cleanup complete."
