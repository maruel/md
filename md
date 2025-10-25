#!/bin/bash
# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.
#
# md (my devenv): sets up a local dev environment with a local git clone for quick iteration.
#
# Assumptions:
# - only tested on ubuntu
# - ssh-keygen is available locally
# - nvim is configured
#
# A simplified version of https://github.com/boldsoftware/sketch/blob/main/loop/server/local_ssh.md but
# without the certification verification done properly yet.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOST_KEY_PATH="$SCRIPT_DIR/rsc/etc/ssh/ssh_host_ed25519_key"
HOST_KEY_PUB_PATH="$HOST_KEY_PATH.pub"
USER_AUTH_KEYS="$SCRIPT_DIR/rsc/home/user/.ssh/authorized_keys"

if [ ! -d $HOME/.amp ]; then
	mkdir $HOME/.amp
fi
if [ ! -d $HOME/.codex ]; then
	mkdir $HOME/.codex
fi
if [ ! -d $HOME/.claude ]; then
	mkdir $HOME/.claude
fi
if [ ! -d $HOME/.gemini ]; then
	mkdir $HOME/.gemini
fi
if [ ! -d $HOME/.qwen ]; then
	mkdir $HOME/.qwen
fi
if [ ! -d $HOME/.config/amp ]; then
	mkdir $HOME/.config/amp
fi
if [ ! -d $HOME/.config/goose ]; then
	mkdir $HOME/.config/goose
fi
if [ ! -d $HOME/.local/share/amp ]; then
	mkdir $HOME/.local/share/amp
fi
if [ ! -d $HOME/.local/share/goose ]; then
	mkdir $HOME/.local/share/goose
fi
if [ ! -d "$HOME/.ssh" ]; then
	mkdir -m 700 "$HOME/.ssh"
fi
if [ ! -d "$HOME/.ssh/config.d" ]; then
	mkdir "$HOME/.ssh/config.d"
fi
mkdir -p "$(dirname "$HOST_KEY_PATH")"
mkdir -p "$(dirname "$USER_AUTH_KEYS")"
GIT_CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$GIT_CURRENT_BRANCH" ]; then
	echo "Check out a named branch" >&2
	exit 1
fi
GIT_ROOT_DIR=$(git rev-parse --show-toplevel)
GIT_USER_NAME="$(git config --get user.name)"
GIT_USER_EMAIL="$(git config --get user.email)"
REPO_NAME=$(basename "$GIT_ROOT_DIR")
CONTAINER_NAME="md-$REPO_NAME-$GIT_CURRENT_BRANCH"
IMAGE_NAME=md

MD_USER_KEY="$HOME/.ssh/md-$REPO_NAME"
if [ ! -f "$MD_USER_KEY" ]; then
	echo "- Generating md user SSH key at $MD_USER_KEY ..."
	ssh-keygen -q -t ed25519 -N '' -C "md-user" -f "$MD_USER_KEY"
fi
if [ ! -f "$MD_USER_KEY.pub" ]; then
	ssh-keygen -y -f "$MD_USER_KEY" > "$MD_USER_KEY.pub"
fi
if [ ! -f "$HOST_KEY_PATH" ]; then
	echo "- Generating md host SSH key at $HOST_KEY_PATH ..."
	ssh-keygen -q -t ed25519 -N '' -C "md-host" -f "$HOST_KEY_PATH"
fi
if [ ! -f "$HOST_KEY_PUB_PATH" ]; then
	ssh-keygen -y -f "$HOST_KEY_PATH" > "$HOST_KEY_PUB_PATH"
fi

if [ $# -ne 0 ]; then
    echo "Error: No arguments are supported" >&2
    exit 1
fi

######

function build {
	ROOT="$SCRIPT_DIR"
	cd $ROOT/rsc

	echo "- Building Docker image ${IMAGE_NAME}.base ..."
	cp "$MD_USER_KEY.pub" "$USER_AUTH_KEYS"
	chmod 600 "$USER_AUTH_KEYS"
	docker build -t ${IMAGE_NAME}.base -f Dockerfile.base .

	echo "- Building Docker image $IMAGE_NAME ..."
	# We could use ARGS ENV_FILE in there but the image would become specific to this repository.
	docker build -t $IMAGE_NAME .
	cd -
}

function run {
	docker rm -f $CONTAINER_NAME &>/dev/null || true
	echo "- Starting container $CONTAINER_NAME ..."
	# Port 3000 is mapped.
	# -p 127.0.0.1:3000:3000
	docker run -d \
	  --name $CONTAINER_NAME \
	  -p 127.0.0.1:0:22 \
	  -v ~/.amp/:/home/user/.amp/ \
	  -v ~/.codex/:/home/user/.codex/ \
	  -v ~/.claude/:/home/user/.claude/ \
	  -v ~/.gemini/:/home/user/.gemini/ \
	  -v ~/.qwen/:/home/user/.qwen/ \
	  -v ~/.config/amp/:/home/user/.config/amp/ \
	  -v ~/.config/goose/:/home/user/.config/goose/ \
	  -v ~/.local/share/amp/:/home/user/.local/share/amp/ \
	  -v ~/.local/share/goose/:/home/user/.local/share/goose/ \
	  $IMAGE_NAME

	PORT_NUMBER=$(docker inspect --format "{{(index .NetworkSettings.Ports \"22/tcp\" 0).HostPort}}" $CONTAINER_NAME)
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
	#echo "  HostbasedAuthentication yes" >> "$HOST_CONF"
	#echo "  EnableSSHKeysign yes" >> "$HOST_CONF"

	echo "- git clone into container ..."
	git remote rm $CONTAINER_NAME || true
	git remote add $CONTAINER_NAME user@$CONTAINER_NAME:/app || true
	# We need to loop until the container is up and running. It's quite fast but can take a few hundreds of
	# ms.
	while ! ssh $CONTAINER_NAME exit &>/dev/null; do
		sleep 0.1
	done
	git fetch $CONTAINER_NAME
	git push -q $CONTAINER_NAME HEAD:$GIT_CURRENT_BRANCH
	ssh $CONTAINER_NAME "cd /app && git checkout -q $GIT_CURRENT_BRANCH"
	# Set up base branch to match the original state for easy diffing
	ssh $CONTAINER_NAME "cd /app && git branch -f base $GIT_CURRENT_BRANCH && git checkout base && git checkout $GIT_CURRENT_BRANCH"
		if [ -f .env ]; then
		echo "- sending .env into container ..."
		scp .env $CONTAINER_NAME:/home/user/.env
	fi

	echo ""
	echo "Base branch '$GIT_CURRENT_BRANCH' has been set up in the container as 'base' for easy diffing."
	echo "Inside the container, you can use 'git diff base' to see your changes."
	echo ""
	echo "When done:"
	echo "  docker rm -f $CONTAINER_NAME"
}

build
run

ssh $CONTAINER_NAME
