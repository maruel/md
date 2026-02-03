#!/bin/bash
set -eu

# Generate dynamic motd with hostname
cat >/etc/motd <<EOF
Connected to $(hostname)
Don't forget to tell your agent to read ~/tool_versions.md
EOF

# Export MD_REPO_DIR to profile.d so SSH sessions can access it
if [ -n "${MD_REPO_DIR:-}" ]; then
	echo "export MD_REPO_DIR='$MD_REPO_DIR'" >/etc/profile.d/00-md-repo-dir.sh
	chmod 644 /etc/profile.d/00-md-repo-dir.sh
fi

# If /dev/kvm exists, update the kvm group GID to match the host
if [ -e /dev/kvm ]; then
	host_kvm_gid=$(stat -c %g /dev/kvm)
	current_kvm_gid=$(getent group kvm | cut -d: -f3)
	if [ "$host_kvm_gid" != "$current_kvm_gid" ]; then
		groupmod -g "$host_kvm_gid" kvm
	fi
fi

# Start dbus service and ensure user has a DBus session available
echo "[start.sh] Starting dbus service..."
/etc/init.d/dbus start
echo "[start.sh] Setting up persistent DBus session for user..."
session_file="/home/user/.dbus-session-env"
su - user -c "dbus-launch --sh-syntax > '$session_file'"
chown user:user "$session_file"
cat <<EOF >/etc/profile.d/50-dbus-session.sh
if [ -f "$session_file" ]; then
    . "$session_file"
    export DBUS_SESSION_BUS_ADDRESS
fi
EOF

# Start XFCE4 and VNC
if [ -n "${MD_DISPLAY:-}" ]; then
	# Start VNC server in background as user (before SSH to avoid race condition)
	if command -v vncserver &>/dev/null; then
		echo "[start.sh] Starting VNC server as user..."
		# Clean up migration conflict and prepare log file
		mkdir -p /home/user/.config/tigervnc
		chown user:user /home/user/.config/tigervnc
		touch /var/log/vncserver.log
		chmod 666 /var/log/vncserver.log
		su - user -c "vncserver --I-KNOW-THIS-IS-INSECURE 2>&1 | tee /var/log/vncserver.log" </dev/null &
		echo "[start.sh] VNC server started in background"
		sleep 2
		if [ -f /var/log/vncserver.log ]; then
			display=$(grep -oE ':[0-9]+' /var/log/vncserver.log | head -n1)
			if [ -n "$display" ]; then
				echo "[start.sh] Detected VNC display: $display"
				{
					echo "# VNC Display - set by container startup"
					echo "export DISPLAY=$display"
				} >/etc/profile.d/60-vnc-display.sh
				chmod 644 /etc/profile.d/60-vnc-display.sh
			fi
		fi
	else
		echo "[start.sh] vncserver not found, skipping VNC startup"
	fi
else
	echo "[start.sh] MD_DISPLAY not set, skipping X/VNC startup"
fi

# Start Tailscale if enabled
if [ -n "${MD_TAILSCALE:-}" ]; then
	echo "[start.sh] Starting Tailscale..."
	# Create TUN device inside container's namespace (don't use host's /dev/net/tun)
	mkdir -p /dev/net
	mknod /dev/net/tun c 10 200 2>/dev/null || true
	chmod 600 /dev/net/tun
	tailscaled --state=/var/lib/tailscale/tailscaled.state &
	# Wait for tailscaled to be ready
	for _ in $(seq 1 30); do
		if tailscale status >/dev/null 2>&1; then
			break
		fi
		sleep 0.1
	done
	ts_args="--hostname=$(hostname) --ssh"
	if [ -n "${TAILSCALE_AUTHKEY:-}" ]; then
		ts_args="$ts_args --authkey=$TAILSCALE_AUTHKEY"
		# shellcheck disable=SC2086
		tailscale up $ts_args
		# Update MOTD with Tailscale FQDN
		ts_fqdn=$(tailscale status --json | jq -r '.Self.DNSName // empty' | sed 's/\.$//')
		if [ -n "$ts_fqdn" ]; then
			cat >/etc/motd <<-EOF
				Connected to $ts_fqdn
				Don't forget to tell your agent to read ~/tool_versions.md
			EOF
			echo "[start.sh] Tailscale connected: $ts_fqdn"
		fi
	else
		# Capture auth URL for the host to display (MOTD not updated without authkey)
		# shellcheck disable=SC2086
		tailscale up $ts_args 2>&1 | tee /tmp/tailscale_auth_url &
	fi
fi

# Start SSH server (after VNC so DISPLAY is available)
service ssh start

sleep infinity
