#!/bin/bash
# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.
#
# md (my devenv): sets up a local dev environment with a local git clone for quick iteration.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOST_KEY_PATH="$SCRIPT_DIR/rsc/etc/ssh/ssh_host_ed25519_key"
HOST_KEY_PUB_PATH="$HOST_KEY_PATH.pub"
USER_AUTH_KEYS="$SCRIPT_DIR/rsc/home/user/.ssh/authorized_keys"

mkdir -p \
	"$HOME/.amp" \
	"$HOME/.codex" \
	"$HOME/.claude" \
	"$HOME/.gemini" \
	"$HOME/.qwen" \
	"$HOME/.config/amp" \
	"$HOME/.config/goose" \
	"$HOME/.local/share/amp" \
	"$HOME/.local/share/goose" \
	"$(dirname "$HOST_KEY_PATH")" \
	"$(dirname "$USER_AUTH_KEYS")"

if [ -d "$HOME/.ssh" ]; then
	chmod 700 "$HOME/.ssh"
else
	mkdir -m 700 "$HOME/.ssh"
fi
mkdir -p "$HOME/.ssh/config.d"

usage() {
	cat <<'EOF'
usage: ./md <command>

Commands:
  start  Pull latest base image, rebuild if needed, start container, open shell.
  push   Force-push current repo state into the running container.
  pull   Pull changes from the container back to the local repo.
  kill   Remove ssh config/remote and stop/remove the container.
EOF
	exit 1
}

require_no_args() {
	if [ "$#" -ne 0 ]; then
		echo "Error: '$CMD' does not accept additional arguments." >&2
		usage
	fi
}

container_exists() {
	docker inspect "$CONTAINER_NAME" >/dev/null 2>&1
}

GIT_CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$GIT_CURRENT_BRANCH" ]; then
	echo "Check out a named branch" >&2
	exit 1
fi
GIT_ROOT_DIR=$(git rev-parse --show-toplevel)
cd "$GIT_ROOT_DIR"
GIT_USER_NAME="$(git config --get user.name)"
GIT_USER_EMAIL="$(git config --get user.email)"
REPO_NAME=$(basename "$GIT_ROOT_DIR")
CONTAINER_NAME="md-$REPO_NAME-$GIT_CURRENT_BRANCH"
IMAGE_NAME=md
BASE_IMAGE="ghcr.io/maruel/md:latest"

MD_USER_KEY="$HOME/.ssh/md-$REPO_NAME"

ensure_ed25519_key() {
	local path="$1"
	local comment="$2"
	if [ ! -f "$path" ]; then
		echo "- Generating $comment at $path ..."
		ssh-keygen -q -t ed25519 -N '' -C "$comment" -f "$path"
	fi
	if [ ! -f "$path.pub" ]; then
		ssh-keygen -y -f "$path" > "$path.pub"
	fi
}

ensure_ed25519_key "$MD_USER_KEY" "md-user"
ensure_ed25519_key "$HOST_KEY_PATH" "md-host"

if [ "$#" -eq 0 ]; then
	usage
fi
CMD="$1"
shift

######

build() (
	cd "$SCRIPT_DIR/rsc"

	cp "$MD_USER_KEY.pub" "$USER_AUTH_KEYS"
	chmod 600 "$USER_AUTH_KEYS"

	echo "- Pulling base image ${BASE_IMAGE} ..."
	docker pull "${BASE_IMAGE}"

	BASE_DIGEST="$(docker image inspect --format '{{index .RepoDigests 0}}' "${BASE_IMAGE}" 2>/dev/null || true)"
	if [ -z "${BASE_DIGEST}" ]; then
		BASE_DIGEST="$(docker image inspect --format '{{.Id}}' "${BASE_IMAGE}")"
	fi

	CONTEXT_SHA="$(tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - . | sha256sum | cut -d' ' -f1)"
	CURRENT_DIGEST=""
	CURRENT_CONTEXT=""
	if docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
		CURRENT_DIGEST="$(docker image inspect "$IMAGE_NAME" --format '{{ index .Config.Labels "md.base_digest" }}')"
		CURRENT_CONTEXT="$(docker image inspect "$IMAGE_NAME" --format '{{ index .Config.Labels "md.context_sha" }}')"
		if [ "${CURRENT_DIGEST}" = "<no value>" ]; then
			CURRENT_DIGEST=""
		fi
		if [ "${CURRENT_CONTEXT}" = "<no value>" ]; then
			CURRENT_CONTEXT=""
		fi
	fi
	if [[ "${CURRENT_DIGEST}" == "${BASE_DIGEST}" && "${CURRENT_CONTEXT}" == "${CONTEXT_SHA}" ]]; then
		echo "- Docker image $IMAGE_NAME already matches ${BASE_IMAGE} (${BASE_DIGEST}), skipping rebuild."
		return
	fi

	echo "- Building Docker image $IMAGE_NAME ..."
	docker build \
		--build-arg BASE_IMAGE="${BASE_IMAGE}" \
		--build-arg BASE_IMAGE_DIGEST="${BASE_DIGEST}" \
		--build-arg CONTEXT_SHA="${CONTEXT_SHA}" \
		-t "$IMAGE_NAME" .
)

run() {
	echo "- Starting container $CONTAINER_NAME ..."
	# Port 3000 is mapped.
	# -p 127.0.0.1:3000:3000
	docker run -d \
	  --name "$CONTAINER_NAME" \
	  -p 127.0.0.1:0:22 \
	  -v "$HOME/.amp:/home/user/.amp" \
	  -v "$HOME/.codex:/home/user/.codex" \
	  -v "$HOME/.claude:/home/user/.claude" \
	  -v "$HOME/.gemini:/home/user/.gemini" \
	  -v "$HOME/.qwen:/home/user/.qwen" \
	  -v "$HOME/.config/amp:/home/user/.config/amp" \
	  -v "$HOME/.config/goose:/home/user/.config/goose" \
	  -v "$HOME/.local/share/amp:/home/user/.local/share/amp" \
	  -v "$HOME/.local/share/goose:/home/user/.local/share/goose" \
	  "$IMAGE_NAME"

	PORT_NUMBER=$(docker inspect --format "{{(index .NetworkSettings.Ports \"22/tcp\" 0).HostPort}}" "$CONTAINER_NAME")
	echo "- Found ssh port $PORT_NUMBER"
	local HOST_CONF="$HOME/.ssh/config.d/$CONTAINER_NAME.conf"
	local HOST_KNOWN_HOSTS="$HOME/.ssh/config.d/$CONTAINER_NAME.known_hosts"
	echo "Host $CONTAINER_NAME" > "$HOST_CONF"
	echo "  HostName 127.0.0.1" >> "$HOST_CONF"
	echo "  Port $PORT_NUMBER" >> "$HOST_CONF"
	echo "  User user" >> "$HOST_CONF"
	echo "  IdentityFile $MD_USER_KEY" >> "$HOST_CONF"
	echo "  IdentitiesOnly yes" >> "$HOST_CONF"
	echo "  UserKnownHostsFile $HOST_KNOWN_HOSTS" >> "$HOST_CONF"
	echo "  StrictHostKeyChecking yes" >> "$HOST_CONF"
	local HOST_PUBLIC_KEY
	HOST_PUBLIC_KEY=$(cat "$HOST_KEY_PUB_PATH")
	echo "[127.0.0.1]:$PORT_NUMBER $HOST_PUBLIC_KEY" > "$HOST_KNOWN_HOSTS"

	echo "- git clone into container ..."
	git remote rm "$CONTAINER_NAME" || true
	git remote add "$CONTAINER_NAME" "user@$CONTAINER_NAME:/app" || true
	while ! ssh "$CONTAINER_NAME" exit &>/dev/null; do
		sleep 0.1
	done
	git fetch "$CONTAINER_NAME"
	git push -q "$CONTAINER_NAME" "HEAD:$GIT_CURRENT_BRANCH"
	ssh "$CONTAINER_NAME" "cd /app && git checkout -q $GIT_CURRENT_BRANCH"
	ssh "$CONTAINER_NAME" "cd /app && git branch -f base $GIT_CURRENT_BRANCH && git checkout base && git checkout $GIT_CURRENT_BRANCH"
	if [ -f .env ]; then
		echo "- sending .env into container ..."
		scp .env "$CONTAINER_NAME:/home/user/.env"
	fi

	echo ""
	echo "Base branch '$GIT_CURRENT_BRANCH' has been set up in the container as 'base' for easy diffing."
	echo "Inside the container, you can use 'git diff base' to see your changes."
	echo ""
	echo "When done:"
	echo "  docker rm -f $CONTAINER_NAME"
}

push_changes() {
	local branch container_commit backup_branch
	branch="$(git rev-parse --abbrev-ref HEAD)"
	container_commit="$(ssh "$CONTAINER_NAME" "cd /app && git rev-parse HEAD")"
	backup_branch="backup-$(date +%Y%m%d-%H%M%S)"
	ssh "$CONTAINER_NAME" "cd /app && git branch -f $backup_branch $container_commit"
	git push -f "$CONTAINER_NAME"
	ssh "$CONTAINER_NAME" "cd /app && git reset --hard && git checkout $branch && git branch -f base $branch"
	echo "- Container updated (previous state saved as $backup_branch)."
}

kill_env() {
	local host_conf="$HOME/.ssh/config.d/$CONTAINER_NAME.conf"
	local host_known_hosts="$HOME/.ssh/config.d/$CONTAINER_NAME.known_hosts"
	rm -f "$host_conf" "$host_known_hosts"
	git remote remove "$CONTAINER_NAME" 2>/dev/null || true
	if docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1; then
		echo "Removed $CONTAINER_NAME"
	else
		echo "$CONTAINER_NAME not running"
	fi
}

pull_changes() {
	ssh "$CONTAINER_NAME" "cd /app && git add . && git commit -a -q -m 'Pull from md' || true"
	local remote_branch
	remote_branch="$(ssh "$CONTAINER_NAME" "cd /app && git rev-parse --abbrev-ref HEAD")"
	git pull -q "$CONTAINER_NAME" "$remote_branch:"
	ssh "$CONTAINER_NAME" "cd /app && git branch -f base $remote_branch"
}

case "$CMD" in
	start)
		require_no_args "$@"
		if container_exists; then
			echo "Container $CONTAINER_NAME already exists. SSH in with 'ssh $CONTAINER_NAME' or clean it up via './md kill' first." >&2
			exit 1
		fi
		build
		run
		ssh "$CONTAINER_NAME"
		;;
	push)
		require_no_args "$@"
		push_changes
		;;
	pull)
		require_no_args "$@"
		pull_changes
		;;
	kill)
		require_no_args "$@"
		kill_env
		;;
	*)
		usage
		;;
esac
