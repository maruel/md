#!/bin/bash
# Start Xvnc and XFCE - runs synchronously during container startup

set -eu

DISPLAY=":1"
LOGFILE="/var/log/display-server.log"
DISPLAY_FILE="/etc/profile.d/60-vnc-display.sh"

log() {
	echo "[vnc-start] $*" | tee -a "$LOGFILE"
}

# Clean up any stale X locks/sockets
rm -f /tmp/.X1-lock /tmp/.X11-unix/X1 2>/dev/null || true

# Prepare log file
: >"$LOGFILE"
chmod 666 "$LOGFILE"

# Start Xvnc
log "Starting Xvnc on $DISPLAY (port 5901)..."
Xvnc "$DISPLAY" -geometry 1920x1080 -depth 24 -SecurityTypes None -rfbport 5901 &
sleep 1

# Write DISPLAY to profile.d
log "Writing DISPLAY=$DISPLAY to $DISPLAY_FILE"
{
	echo "# Display - set by container startup"
	echo "export DISPLAY=$DISPLAY"
} >"$DISPLAY_FILE"
chmod 644 "$DISPLAY_FILE"

# Start XFCE
log "Starting XFCE session as user..."
su - user -c "DISPLAY=$DISPLAY startxfce4" </dev/null &

log "VNC startup complete, starting monitors"
/root/xvnc-monitor.sh &
/root/xfce-monitor.sh &
