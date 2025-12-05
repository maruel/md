#!/bin/bash
set -eu

# If /dev/kvm exists, update the kvm group GID to match the host
if [ -e /dev/kvm ]; then
	host_kvm_gid=$(stat -c %g /dev/kvm)
	current_kvm_gid=$(getent group kvm | cut -d: -f3)
	if [ "$host_kvm_gid" != "$current_kvm_gid" ]; then
		groupmod -g "$host_kvm_gid" kvm
	fi
fi

service ssh start
sleep infinity
