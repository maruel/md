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

# Start dbus service (required for XFCE4)
if [ -n "${MD_DISPLAY:-}" ]; then
	if [ -x /etc/init.d/dbus ]; then
		echo "[start.sh] Starting dbus service..."
		/etc/init.d/dbus start
	fi

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
				} >/etc/profile.d/vnc-display.sh
				chmod 644 /etc/profile.d/vnc-display.sh
			fi
		fi
	else
		echo "[start.sh] vncserver not found, skipping VNC startup"
	fi
fi

# Start SSH server (after VNC so DISPLAY is available)
service ssh start

sleep infinity
