#!/bin/bash
set -eu

# Create kvm group if it doesn't exist
if ! getent group kvm >/dev/null; then
	groupadd -r kvm
fi

# Add user to kvm group
if getent passwd user >/dev/null 2>&1; then
	usermod -a -G kvm user
fi
