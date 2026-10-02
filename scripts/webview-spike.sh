#!/usr/bin/env bash
# Phase-0 webview spike (DESIGN.md §6.6, §55, §60): build the production
# binary and verify the release-hardening properties on the artifact itself:
#
#   1. the shell starts and stays alive on the Tier-1 Linux stack
#      (GTK4/WebKitGTK 6.0), headless under Xvfb;
#   2. the process tree exposes NO listening TCP/UDP socket — the Go<->JS
#      bindings transport must be in-process, never a reachable localhost
#      HTTP/WebSocket server another local process could drive.
#
# DevTools are handled at build time: -tags production compiles the wails
# DevTools capability out, and DevToolsEnabled stays false in main.go.
# Re-run this on the shipped binary as part of Phase 7 release checks.
set -euo pipefail
cd "$(dirname "$0")/.."

command -v wails3 >/dev/null || {
	echo "FAIL: wails3 CLI not on PATH (go install github.com/wailsapp/wails/v3/cmd/wails3@<pinned>)"
	exit 1
}

wails3 task build

BIN=bin/aeronautes
[ -x "$BIN" ] || {
	echo "FAIL: $BIN was not built"
	exit 1
}

command -v Xvfb >/dev/null || {
	echo "SKIP: Xvfb not installed; headless runtime checks not run"
	exit 0
}

XVFB_DISPLAY=":97"
Xvfb "$XVFB_DISPLAY" -screen 0 1280x800x24 &
XVFB_PID=$!
APP_LOG=$(mktemp -t aeronautes-spike.XXXXXX)
cleanup() {
	if [ -n "${APP_PID:-}" ]; then kill "$APP_PID" 2>/dev/null || true; fi
	# dbus-run-session's TERM handling can orphan the app; sweep it.
	pkill -f "$PWD/bin/aeronautes" 2>/dev/null || true
	kill "$XVFB_PID" 2>/dev/null || true
	rm -f "$APP_LOG"
}
trap cleanup EXIT

# Wait for the display socket instead of hoping — a stale lock or slow
# start otherwise turns into a misleading "Failed to open display".
for _ in $(seq 1 50); do
	[ -S "/tmp/.X11-unix/X${XVFB_DISPLAY#:}" ] && break
	kill -0 "$XVFB_PID" 2>/dev/null || {
		echo "FAIL: Xvfb did not start (stale /tmp/.X${XVFB_DISPLAY#:}-lock?)"
		exit 1
	}
	sleep 0.2
done

# Run detached from Wayland so the spike exercises the Xvfb display, and
# under a PRIVATE session bus: WebKitGTK's bubblewrap sandbox aborts hard
# without a usable bus, and with DISPLAY set but no bus address the
# process would X11-autolaunch a dbus-daemon that inherits our stdio and
# outlives the app (both observed). dbus-run-session reaps its daemon on
# exit; the empty private bus has no Secret Service, so the app falls back
# to the session secret store — the honest headless behaviour.
command -v dbus-run-session >/dev/null || {
	echo "FAIL: dbus-run-session not installed (dbus-tools); required for a leak-free headless run"
	exit 1
}
# The app's stdio goes to a scratch log, never to this script's pipeline:
# any straggling child inheriting our stdout would otherwise hold a
# caller's pipe open long after the spike is done.
env DISPLAY="$XVFB_DISPLAY" WAYLAND_DISPLAY="" dbus-run-session -- "$PWD/$BIN" \
	>"$APP_LOG" 2>&1 &
APP_PID=$!
sleep 5

kill -0 "$APP_PID" 2>/dev/null || {
	echo "FAIL: shell exited within 5s of launch; last app output:"
	tail -5 "$APP_LOG"
	exit 1
}

# Collect the shell process plus WebKit helper processes and look for any
# listening socket owned by them.
PIDS=$(pgrep -f "bin/aeronautes|WebKitWebProcess|WebKitNetworkProcess" | paste -sd'|' -)
LISTENERS=$(ss -tulnp 2>/dev/null | grep -E "pid=(${PIDS})," || true)
if [ -n "$LISTENERS" ]; then
	echo "FAIL: production shell exposes listening sockets (§55):"
	echo "$LISTENERS"
	exit 1
fi

echo "PASS: production shell runs headless on GTK4/WebKitGTK 6.0 with no listening sockets"
