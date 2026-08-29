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
cleanup() {
	[ -n "${APP_PID:-}" ] && kill "$APP_PID" 2>/dev/null || true
	kill "$XVFB_PID" 2>/dev/null || true
}
trap cleanup EXIT
sleep 1

# Run detached from Wayland so the spike exercises the Xvfb display; use a
# private session bus when available so WebKit's portal chatter is isolated.
if command -v dbus-run-session >/dev/null; then
	env DISPLAY="$XVFB_DISPLAY" WAYLAND_DISPLAY="" dbus-run-session -- "$BIN" &
else
	env DISPLAY="$XVFB_DISPLAY" WAYLAND_DISPLAY="" "$BIN" &
fi
APP_PID=$!
sleep 5

kill -0 "$APP_PID" 2>/dev/null || {
	echo "FAIL: shell exited within 5s of launch"
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
