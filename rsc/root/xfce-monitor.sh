#!/bin/bash
# Monitor XFCE session, restart if it dies
# Runs as root - unkillable by user

set -eu

DISPLAY=":1"
LOGFILE="/var/log/display-server.log"

log() {
	echo "[xfce-monitor] $*" | tee -a "$LOGFILE"
}

start_xfce() {
	su - user -c "DISPLAY=$DISPLAY startxfce4" </dev/null &
	sleep 1
	pgrep -u user -x xfce4-session
}

while true; do
	pid=$(pgrep -u user -x xfce4-session || start_xfce)
	log "Watching XFCE (pid $pid)"
	tail --pid="$pid" -f /dev/null 2>/dev/null || true
	log "XFCE died"
done
