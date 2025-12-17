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
	"$HOME/.android" \
	"$HOME/.codex" \
	"$HOME/.claude" \
	"$HOME/.gemini" \
	"$HOME/.opencode" \
	"$HOME/.qwen" \
	"${XDG_CONFIG_HOME:-$HOME/.config}/amp" \
	"${XDG_CONFIG_HOME:-$HOME/.config}/goose" \
	"${XDG_CONFIG_HOME:-$HOME/.config}/opencode" \
	"${XDG_CONFIG_HOME:-$HOME/.config}/md" \
	"$HOME/.local/share/amp" \
	"$HOME/.local/share/goose" \
	"$(dirname "$HOST_KEY_PATH")" \
	"$(dirname "$USER_AUTH_KEYS")"
if [ ! -f "${XDG_CONFIG_HOME:-$HOME/.config}/md/env" ]; then
	touch "${XDG_CONFIG_HOME:-$HOME/.config}/md/env"
fi
if [ ! -f "$HOME/.claude.json" ]; then
	# This is SO annoying. What were they thinking?
	ln -s "$HOME/.claude/claude.json" "$HOME/.claude.json"
elif [ ! -L "$HOME/.claude.json" ]; then
	echo "File $HOME/.claude.json exists but is not a symlink"
	echo "It's problematic because your authentication will not be synchronized. Blame Anthropic to not putting files at the right place."
	echo "Run:"
	echo "  mv $HOME/.claude.json $HOME/.claude/claude.json"
	echo "  ln -s $HOME/.claude/claude.json $HOME/.claude.json"
	exit 1
fi

if [ -d "$HOME/.ssh" ]; then
	chmod 700 "$HOME/.ssh"
else
	mkdir -m 700 "$HOME/.ssh"
fi
mkdir -p "$HOME/.ssh/config.d"

usage() {
	cat <<'EOF'
usage: md <command>

Commands:
  start       Pull latest base image, rebuild if needed, start container, open shell.
  push        Force-push current repo state into the running container.
  pull        Pull changes from the container back to the local repo.
  diff        Show differences between base branch and current changes in container.
  kill        Remove ssh config/remote and stop/remove the container.
  build-base  Build the base Docker image locally from rsc/Dockerfile.base.
EOF
	exit 1
}

require_no_args() {
	if [ "$#" -ne 0 ]; then
		echo "Error: '$CMD' does not accept additional arguments." >&2
		usage
	fi
}

GIT_CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$GIT_CURRENT_BRANCH" ]; then
	echo "Check out a named branch" >&2
	exit 1
fi
GIT_ROOT_DIR=$(git rev-parse --show-toplevel)
cd "$GIT_ROOT_DIR"
REPO_NAME=$(basename "$GIT_ROOT_DIR")
CONTAINER_NAME="md-$REPO_NAME-$GIT_CURRENT_BRANCH"
IMAGE_NAME=md
BASE_IMAGE="ghcr.io/maruel/md:latest"
# For now, we use the same key for all containers. Let's update if there's any security value in doing
# different.
MD_USER_KEY="$HOME/.ssh/md"

ensure_ed25519_key() {
	local path="$1"
	local comment="$2"
	if [ ! -f "$path" ]; then
		echo "- Generating $comment at $path ..."
		ssh-keygen -q -t ed25519 -N '' -C "$comment" -f "$path"
	fi
	if [ ! -f "$path.pub" ]; then
		ssh-keygen -y -f "$path" >"$path.pub"
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

build_base() (
	echo "- Building base Docker image from rsc/Dockerfile.base ..."
	docker build \
		-f "$SCRIPT_DIR/rsc/Dockerfile.base" \
		-t md-base \
		"$SCRIPT_DIR/rsc"
	echo "- Base image built as 'md-base'."
)

build() (
	cd "$SCRIPT_DIR/rsc"

	cp "$MD_USER_KEY.pub" "$USER_AUTH_KEYS"
	chmod 600 "$USER_AUTH_KEYS"

	# Check if local md-base image is more recent than remote
	LOCAL_BASE_CREATED=$(docker image inspect md-base --format '{{.Created}}' 2>/dev/null || echo "")
	if [ -n "$LOCAL_BASE_CREATED" ]; then
		REMOTE_BASE_CREATED=$(docker image inspect "${BASE_IMAGE}" --format '{{.Created}}' 2>/dev/null || echo "")
		if [ -n "$REMOTE_BASE_CREATED" ]; then
			LOCAL_EPOCH=$(date -d "$LOCAL_BASE_CREATED" +%s 2>/dev/null || echo 0)
			REMOTE_EPOCH=$(date -d "$REMOTE_BASE_CREATED" +%s 2>/dev/null || echo 0)
			if [ "$LOCAL_EPOCH" -gt "$REMOTE_EPOCH" ]; then
				echo "- Local md-base image is newer, using local build instead of ${BASE_IMAGE}"
				BASE_IMAGE="md-base"
			fi
		fi
	fi
	if [ "$BASE_IMAGE" != "md-base" ]; then
		echo "- Pulling base image ${BASE_IMAGE} ..."
		docker pull "${BASE_IMAGE}"
	fi

	#if [ -d "$HOME/go/pkg" ]; then
	#	docker volume create go_pkg_cache
	#	# Should be the same as rsc/Dockerfile.base
	#	BASE_IMG=docker.io/debian:stable-slim
	#	docker run --rm \
	#		-v $HOME/go/pkg:/host_pkg:ro \
	#		-v go_pkg_cache:/container_pkg \
	#		$BASE_IMG sh -c "cp -a /host_pkg/. /container_pkg/"
	#fi

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
	local KVM_DEVICE=""
	if [ -e /dev/kvm ] && [ -w /dev/kvm ]; then
		KVM_DEVICE="--device=/dev/kvm"
	fi
	local CLAUDE_JSON_MOUNT=""
	docker run -d \
		--name "$CONTAINER_NAME" \
		-p 127.0.0.1:0:22 \
		${KVM_DEVICE:+"$KVM_DEVICE"} \
		${CLAUDE_JSON_MOUNT:+"$CLAUDE_JSON_MOUNT"} \
		-v "$HOME/.amp:/home/user/.amp" \
		-v "$HOME/.android:/home/user/.android" \
		-v "$HOME/.codex:/home/user/.codex" \
		-v "$HOME/.claude:/home/user/.claude" \
		-v "$HOME/.gemini:/home/user/.gemini" \
		-v "$HOME/.opencode:/home/user/.opencode" \
		-v "$HOME/.qwen:/home/user/.qwen" \
		-v "${XDG_CONFIG_HOME:-$HOME/.config}/amp:/home/user/.config/amp" \
		-v "${XDG_CONFIG_HOME:-$HOME/.config}/goose:/home/user/.config/goose" \
		-v "${XDG_CONFIG_HOME:-$HOME/.config}/opencode:/home/user/.config/opencode" \
		-v "${XDG_CONFIG_HOME:-$HOME/.config}/md:/home/user/.config/md:ro" \
		-v "$HOME/.local/share/amp:/home/user/.local/share/amp" \
		-v "$HOME/.local/share/goose:/home/user/.local/share/goose" \
		"$IMAGE_NAME"

	PORT_NUMBER=$(docker inspect --format "{{(index .NetworkSettings.Ports \"22/tcp\" 0).HostPort}}" "$CONTAINER_NAME")
	echo "- Found ssh port $PORT_NUMBER"
	local HOST_CONF="$HOME/.ssh/config.d/$CONTAINER_NAME.conf"
	local HOST_KNOWN_HOSTS="$HOME/.ssh/config.d/$CONTAINER_NAME.known_hosts"
	{
		echo "Host $CONTAINER_NAME"
		echo "  HostName 127.0.0.1"
		echo "  Port $PORT_NUMBER"
		echo "  User user"
		echo "  IdentityFile $MD_USER_KEY"
		echo "  IdentitiesOnly yes"
		echo "  UserKnownHostsFile $HOST_KNOWN_HOSTS"
		echo "  StrictHostKeyChecking yes"
	} >"$HOST_CONF"
	local HOST_PUBLIC_KEY
	HOST_PUBLIC_KEY=$(cat "$HOST_KEY_PUB_PATH")
	echo "[127.0.0.1]:$PORT_NUMBER $HOST_PUBLIC_KEY" >"$HOST_KNOWN_HOSTS"

	echo "- git clone into container ..."
	git remote rm "$CONTAINER_NAME" || true
	git remote add "$CONTAINER_NAME" "user@$CONTAINER_NAME:/app" || true
	while ! ssh "$CONTAINER_NAME" exit &>/dev/null; do
		sleep 0.1
	done
	git fetch "$CONTAINER_NAME"
	git push -q "$CONTAINER_NAME" "HEAD:refs/heads/$GIT_CURRENT_BRANCH"
	ssh "$CONTAINER_NAME" 'cd /app && git switch -q '"'$GIT_CURRENT_BRANCH'"
	ssh "$CONTAINER_NAME" 'cd /app && git branch -f base '"'$GIT_CURRENT_BRANCH'"' && git switch -q base && git switch -q '"'$GIT_CURRENT_BRANCH'"
	if [ -f .env ]; then
		echo "- sending .env into container ..."
		scp .env "$CONTAINER_NAME:/home/user/.env"
	fi

	# TODO: This would require a go clean -modcache then a go get -t ./... to be efficient.
	#if [ -d "$HOME/go/pkg" ]; then
	#	# Surprisingly, compression helps.
	#	rsync -az --ignore-existing --info=progress2 $HOME/go/pkg/ $CONTAINER_NAME:/home/user/go/pkg
	#fi

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
	ssh "$CONTAINER_NAME" 'cd /app && git branch -f '"'$backup_branch'"' '"'$container_commit'"
	git push -q -f "$CONTAINER_NAME"
	ssh "$CONTAINER_NAME" 'cd /app && git reset -q --hard && git switch -q '"'$branch'"' && git branch -q -f base '"'$branch'"
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
	local remote_branch
	remote_branch="$(ssh "$CONTAINER_NAME" "cd /app && git add . && git rev-parse --abbrev-ref HEAD")"
	# shellcheck disable=SC2034
	local commit_msg="Pull from md"
	if [ -n "${ASK_PROVIDER:-}" ] && which ask >/dev/null 2>&1; then
		local diff_output
		diff_output="$(ssh "$CONTAINER_NAME" "cd /app && echo '=== Branch ===' && git rev-parse --abbrev-ref HEAD && echo && echo '=== Files Changed ===' && git diff --stat --cached base -- && echo && echo '=== Recent Commits ===' && git log -5 base -- && echo && echo '=== Changes ===' && git diff --patience -U10 --cached base --")"
		local prompt="Analyze the git context below (branch, file changes, recent commits, and diff). Write a commit message explaining what changed and why. It should be one line, or summary + details if the change is very complex. Match the style of recent commits. No emojis."
		# shellcheck disable=SC2034
		commit_msg="$(ask -q -provider "$ASK_PROVIDER" "$prompt" "$diff_output")"
		echo ""
	fi
	echo "$commit_msg" | ssh "$CONTAINER_NAME" 'cd /app && git commit -a -q -F -' || true
	if git pull --rebase -q "$CONTAINER_NAME" "$remote_branch"; then
		git commit --amend --no-edit --reset-author
	fi
	ssh "$CONTAINER_NAME" 'cd /app && git branch -f base '"'$remote_branch'"
}

diff_changes() {
	ssh -q -t "$CONTAINER_NAME" "cd /app && git add . && git diff base -- ."
}

case "$CMD" in
start)
	require_no_args "$@"
	if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
		echo "Container $CONTAINER_NAME already exists. SSH in with 'ssh $CONTAINER_NAME' or clean it up via 'md kill' first." >&2
		exit 1
	fi
	build
	run
	ssh "$CONTAINER_NAME"
	;;
build-base)
	require_no_args "$@"
	build_base
	;;
push)
	require_no_args "$@"
	push_changes
	;;
pull)
	require_no_args "$@"
	pull_changes
	;;
diff)
	require_no_args "$@"
	diff_changes
	;;
kill)
	require_no_args "$@"
	kill_env
	;;
*)
	usage
	;;
esac
