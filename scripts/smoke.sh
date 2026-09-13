#!/usr/bin/env bash
# Release smoke (§65.6 item 8): launch the real binary headless against
# the in-repo mock cluster, drive bookmark → connect → browse → copy,
# and assert the copy actually landed server-side. Screenshots and logs
# go to smoke-artifacts/. The §57.4 smoke against real SwiftStack
# infrastructure remains a manual operator step.
set -euo pipefail
cd "$(dirname "$0")/.."

ART="smoke-artifacts"
rm -rf "$ART" && mkdir -p "$ART"
say() { printf '\nsmoke: %s\n' "$*"; }
fail() { printf '\nsmoke: FAIL: %s\n' "$*"; exit 1; }

for tool in Xvfb xdotool import curl python3; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
[ -x bin/aeronautes ] || fail "bin/aeronautes missing — run 'make build' first"

TRANSFERS=1
if ! rclone version 2>/dev/null | head -1 | grep -q "v1.74.3"; then
  say "SKIPPING the transfer step: pinned rclone v1.74.3 not on PATH"
  TRANSFERS=0
fi

say "building mockswift"
export PATH="$HOME/go/bin:$PATH"
go build -o "$ART/mockswift" ./cmd/mockswift

say "starting mock cluster"
"$ART/mockswift" -addr 127.0.0.1:8899 -fixtures=false >"$ART/mockswift.log" 2>&1 &
MOCK_PID=$!
STATE_DIR=$(mktemp -d)
cleanup() {
  [ -n "${APP_PID:-}" ] && kill "$APP_PID" >/dev/null 2>&1 || true
  kill "$MOCK_PID" "${XVFB_PID:-}" >/dev/null 2>&1 || true
  rm -rf "$STATE_DIR"
}
trap cleanup EXIT
sleep 1

TOK=$(curl -si http://127.0.0.1:8899/auth/v1.0 -H "X-Auth-User: test:tester" -H "X-Auth-Key: testing" \
  | grep -i '^x-auth-token:' | tr -d '\r' | awk '{print $2}')
[ -n "$TOK" ] || fail "mock auth"
U="http://127.0.0.1:8899/v1/AUTH_test"
curl -sf -X PUT "$U/smoke-out" -H "X-Auth-Token: $TOK" -o /dev/null
curl -sf -X PUT "$U/smoke-src" -H "X-Auth-Token: $TOK" -o /dev/null
for name in alpha.txt bravo.txt charlie.txt; do
  curl -sf -X PUT "$U/smoke-src/$name" -H "X-Auth-Token: $TOK" --data-binary "payload-$name" -o /dev/null
done

say "starting Xvfb + app"
Xvfb :98 -screen 0 1280x800x24 >"$ART/xvfb.log" 2>&1 &
XVFB_PID=$!
sleep 1
AERONAUTES_STATE_DIR="$STATE_DIR" DISPLAY=:98 WAYLAND_DISPLAY= ./bin/aeronautes >"$ART/app.log" 2>&1 &
APP_PID=$!

X() { DISPLAY=:98 xdotool "$@"; }
shot() { DISPLAY=:98 import -window root "$ART/$1.png"; }
# wire_saw <pattern> <seconds>: the mock's request log is the truth —
# a UI step only counts when its wire effect appears.
wire_saw() {
  for _ in $(seq 1 "$2"); do
    grep -q "$1" "$ART/mockswift.log" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

say "waiting for the window"
WIN=$(timeout 30 env DISPLAY=:98 xdotool search --sync --onlyvisible --name "Aeronautes" | head -1)   || fail "app window never appeared (see $ART/app.log)"
# No window manager under Xvfb: X keyboard focus is nowhere until set,
# so keystrokes can miss the app entirely. Pin it explicitly.
X windowactivate --sync "$WIN" 2>/dev/null || X windowfocus --sync "$WIN"
sleep 2
kill -0 "$APP_PID" || fail "app exited at launch (see $ART/app.log)"

say "bookmark + connect"
connected=""
for attempt in 1 2 3; do
  X windowfocus --sync "$WIN" 2>/dev/null || true
  X key Escape; sleep 1                 # clear any half-open state
  X mousemove 42 150 click 1; sleep 2   # below the empty-list status
  # The dialog's focus trap puts focus on the Display-name field at
  # mount — type straight in, no pixel-hunting for the field.
  X type --delay 15 "Smoke"
  X key Tab; X type --delay 10 "http://127.0.0.1:8899/auth/v1.0"
  X key Tab; X type --delay 15 "test:tester"
  X key Tab; X type --delay 15 "testing"
  X key Return
  # Save & connect lists containers: the account GET is the proof.
  if wire_saw 'GET /v1/AUTH_test$' 10; then
    connected=yes; break
  fi
  say "connect attempt $attempt did not reach the cluster; retrying"
  shot "retry-connect-$attempt"
done
[ -n "$connected" ] || fail "never connected after 3 attempts"
sleep 1
shot 01-connected

say "open smoke-src"
opened=""
for attempt in 1 2 3; do
  X mousemove 160 160 click 1           # second container row (smoke-out sorts first)
  if wire_saw "GET /v1/AUTH_test/smoke-src" 8; then opened=yes; break; fi
  say "container open attempt $attempt saw no listing; retrying"
done
[ -n "$opened" ] || fail "smoke-src never opened"
sleep 1
shot 02-browse

if [ "$TRANSFERS" = 1 ]; then
  say "copy smoke-src -> smoke-out through the real engine"
  started=""
  for attempt in 1 2; do
    X mousemove 872 82 click 1; sleep 2          # Copy to…
    # The focus trap lands on the Copy tab; tab order is deterministic:
    # Mirror, close, destination select, container input.
    X key Tab Tab Tab Tab
    X type --delay 15 "smoke-out"; sleep 1
    X key Tab Tab Tab                            # prefix, settings summary, Preview
    X key Return; sleep 3                        # Preview
    shot 03-preview
    # Preview disables its button while planning, so native focus can
    # leave it. Start directly in this fixed viewport instead of tabbing
    # from a control that may no longer own focus.
    X mousemove 945 608 click 1; sleep 2          # Start copy
    shot 04-started
    X key Escape; sleep 1
    # The engine's first write is the proof the job started.
    if wire_saw "PUT /v1/AUTH_test/smoke-out/" 15; then started=yes; break; fi
    say "copy attempt $attempt never reached the cluster; retrying dialog"
    X key Escape; sleep 1
  done
  [ -n "$started" ] || fail "copy never started after 2 dialog attempts"

  say "waiting for the copy to land server-side"
  landed=""
  for _ in $(seq 1 30); do
    n=$(curl -s "$U/smoke-out?format=json" -H "X-Auth-Token: $TOK" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))")
    if [ "$n" = "3" ]; then landed=yes; break; fi
    sleep 1
  done
  [ -n "$landed" ] || fail "copy never landed (smoke-out has $n of 3 objects)"
  say "copy landed: 3/3 objects"
  X mousemove 100 756 click 1; sleep 2
  shot 05-drawer
else
  say "transfer step SKIPPED (no pinned rclone)"
fi

say "PASS — artifacts in $ART/"
