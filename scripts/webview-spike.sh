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
# shellcheck source=scripts/app-process.sh
. scripts/app-process.sh

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

# A host whose container cannot run WebKit's own sandbox — a device set bwrap
# cannot bind-mount, for instance — must export
# WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 before running this, or the webview
# aborts within a second of launch and this script reports that as a failed
# shell. This script does NOT set it: disabling that sandbox weakens the
# webview's confinement, so it stays the caller's decision, exactly as in
# scripts/smoke.sh. What is being checked here is unaffected either way — the
# binary's own listening sockets, which the sandbox does not change.

XVFB_DISPLAY=":97"
APP_LOG=$(mktemp -t aeronautes-spike.XXXXXX)
XVFB_LOG=$(mktemp -t aeronautes-spike-xvfb.XXXXXX)
remove_logs() { rm -f "$APP_LOG" "$XVFB_LOG"; }
# Everything this script starts is a process group the library owns, and the
# trap stops all of them however this script ends — installed before the first
# launch. This replaces a kill by pid plus a pkill over a command-line pattern:
# the pattern was there because dbus-run-session does not pass a signal to its
# child, which is precisely what signalling the GROUP does cover, and a pattern
# matches any command line holding that path, including a caller's own.
app_install_traps remove_logs

app_start "$XVFB_LOG" Xvfb "$XVFB_DISPLAY" -screen 0 1280x800x24
XVFB_GROUP="$APP_LAST_GROUP"

# Wait for the display socket instead of hoping — a stale lock or slow
# start otherwise turns into a misleading "Failed to open display".
for _ in $(seq 1 50); do
	[ -S "/tmp/.X11-unix/X${XVFB_DISPLAY#:}" ] && break
	app_leader_running "$XVFB_GROUP" || {
		echo "FAIL: Xvfb did not start (stale /tmp/.X${XVFB_DISPLAY#:}-lock?)"
		cat "$XVFB_LOG"
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
app_start "$APP_LOG" env DISPLAY="$XVFB_DISPLAY" WAYLAND_DISPLAY="" \
	dbus-run-session -- "$PWD/$BIN"
APP_GROUP="$APP_LAST_GROUP"
sleep 5

# The LEADER, not the group: the leader of this group is the bus wrapper, which
# exits when the application exits, so its lifetime is the application's. Asking
# the group instead would accept any surviving member — a quiet helper left
# behind by an application that has already died — and this assertion would then
# pass for a dead shell, with no listening socket to contradict it. That
# regression was found in review (db:780) and is held by a case below the script.
app_leader_running "$APP_GROUP" || {
	echo "FAIL: shell exited within 5s of launch; last app output:"
	tail -5 "$APP_LOG"
	exit 1
}

# Collect this launch's own processes — the shell, the bus wrapper and the
# WebKit helpers are all in the group it owns — and look for any listening
# socket belonging to them. The group is more precise than a name pattern: it
# cannot pick up another run's processes, and it cannot miss a helper whose name
# changes.
PIDS=$(pgrep -g "$APP_GROUP" | paste -sd'|' -)
LISTENERS=$(ss -tulnp 2>/dev/null | grep -E "pid=(${PIDS})," || true)
if [ -n "$LISTENERS" ]; then
	echo "FAIL: production shell exposes listening sockets (§55):"
	echo "$LISTENERS"
	exit 1
fi

echo "PASS: production shell runs headless on GTK4/WebKitGTK 6.0 with no listening sockets"
