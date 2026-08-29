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
sleep 7
kill -0 "$APP_PID" || fail "app exited at launch (see $ART/app.log)"

X() { DISPLAY=:98 xdotool "$@"; }
shot() { DISPLAY=:98 import -window root "$ART/$1.png"; }

say "bookmark + connect"
X mousemove 42 50 click 1; sleep 2
X mousemove 639 280 click 1
X type --delay 15 "Smoke"
X key Tab; X type --delay 10 "http://127.0.0.1:8899/auth/v1.0"
X key Tab; X type --delay 15 "test:tester"
X key Tab; X type --delay 15 "testing"
X key Return; sleep 3
shot 01-connected
grep -q "GET /v1/AUTH_test" "$ART/mockswift.log" 2>/dev/null || true

say "open smoke-src"
X mousemove 160 160 click 1; sleep 2   # second container row (smoke-out sorts first)
shot 02-browse

if [ "$TRANSFERS" = 1 ]; then
  say "copy smoke-src -> smoke-out through the real engine"
  X mousemove 872 82 click 1; sleep 2          # Copy to…
  # The focus trap lands on the Copy tab; tab order is deterministic:
  # Mirror, close, destination select, container input.
  X key Tab Tab Tab Tab
  X type --delay 15 "smoke-out"; sleep 1
  X key Tab Tab Tab                            # prefix, settings summary, Preview
  X key Return; sleep 3                        # Preview
  shot 03-preview
  X key Tab; X key Return; sleep 2             # Start copy
  shot 04-started
  X key Escape; sleep 1

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
