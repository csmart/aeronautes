#!/usr/bin/env bash
# Release smoke (§65.6 item 8): launch the real binary headless against
# the in-repo mock cluster and drive bookmark → connect → browse.
# Mirror and benchmark are deferred in a v1 build (ADR 0052); copy was
# returned on 31 October 2026 (decision 12), and bulk delete on 1 November
# 2026 (decision 14).
# Upload returned to the release on 16 October 2026 but, like download, needs
# a native picker this harness cannot operate, so neither is driven here. Screenshots
# and logs go to smoke-artifacts/. The §57.4 smoke against real SwiftStack
# infrastructure remains a manual operator step.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/app-process.sh
. scripts/app-process.sh

ART="smoke-artifacts"
rm -rf "$ART" && mkdir -p "$ART"
say() { printf '\nsmoke: %s\n' "$*"; }
fail() { printf '\nsmoke: FAIL: %s\n' "$*"; exit 1; }

for tool in Xvfb xdotool import curl python3; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
[ -x bin/aeronautes ] || fail "bin/aeronautes missing — run 'make build' first"

# Before anything is launched: this driver's process handling is what makes
# every later "it started" and "it is gone" mean something.
say "checking the driver's own process handling"
./scripts/app-process-selfcheck.sh >"$ART/app-process-selfcheck.log" 2>&1 \
  || { cat "$ART/app-process-selfcheck.log" >&2; fail "the driver's process handling is broken"; }

say "building mockswift"
export PATH="$HOME/go/bin:$PATH"
go build -o "$ART/mockswift" ./cmd/mockswift

STATE_DIR=$(mktemp -d)
cleanup() { rm -rf "$STATE_DIR"; }
# Installed before the first launch: every process this driver starts is a group
# the library owns, and the trap stops all of them however this driver ends —
# including when it is interrupted, since bash runs an EXIT trap even when a
# signal kills the shell.
app_install_traps cleanup

# The cluster and the display are launched and stopped exactly like the app.
# They are single processes today, but owning them the same way is what makes a
# leftover impossible to miss: a surviving mock holds port 8899 and a surviving
# Xvfb holds display :98, so the next run would fail for the wrong reason.
say "starting mock cluster"
app_start "$ART/mockswift.log" "$ART/mockswift" -addr 127.0.0.1:8899 -fixtures=false
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

say "starting Xvfb"
app_start "$ART/xvfb.log" Xvfb :98 -screen 0 1280x800x24
sleep 1

# Some containers have no session bus, so the webview aborts before any window
# exists; SMOKE_APP_WRAPPER="dbus-run-session --" gives such a host a private
# one. WebKit's own sandbox is turned off by exporting
# WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1, which this script does NOT set:
# the caller does, and CI already does exactly that because its runner denies
# the user namespaces bubblewrap needs. The artifact records the effective value
# either way, because that variable weakens the webview's confinement and a
# reader of these artifacts should not have to guess whether it was on.
read -r -a APP_WRAPPER <<<"${SMOKE_APP_WRAPPER:-}"
APP_ENV=(AERONAUTES_STATE_DIR="$STATE_DIR" DISPLAY=:98 WAYLAND_DISPLAY=)
printf 'wrapper=%s WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=%s\n' \
  "${SMOKE_APP_WRAPPER:-<none>}" "${WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS:-<unset>}" \
  >"$ART/launch-environment.txt"

# start_app <log> [extra env assignments...] — the real binary, as shipped, in
# its own owned group. Sets APP_LAST_GROUP; never call it in a subshell.
start_app() {
  local log="$1"; shift
  app_start "$log" env "${APP_ENV[@]}" "$@" "${APP_WRAPPER[@]}" ./bin/aeronautes
}

# log_saw <file> <pattern> <seconds>: wait for a line to appear in a log. The
# app's own log is the truth for a startup assertion, and waiting on the line
# is also the synchronisation: several writers share that file, so a line's
# position in it proves nothing about what has been written yet.
log_saw() {
  for _ in $(seq 1 "$3"); do
    grep -qF "$2" "$1" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

# file_saw <path> <seconds>: the app's own state database appearing under the
# case's state directory proves a real process started AND used that directory.
file_saw() {
  for _ in $(seq 1 "$2"); do
    [ -e "$1" ] && return 0
    sleep 1
  done
  return 1
}

# wait_window <seconds>: a visible window means startup ran all the way past
# the point where the switch is read, which is what makes a NEGATIVE assertion
# about the log sound. Nothing in a shipped build's log says "started".
wait_window() {
  for _ in $(seq 1 "$1"); do
    DISPLAY=:98 xdotool search --onlyvisible --name "Aeronautes" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

# wait_no_window <seconds>: X keeps a killed app's window listed for a moment,
# and a stale id fails SetInputFocus with BadWindow. The journey must start
# from a display with no Aeronautes window at all.
wait_no_window() {
  for _ in $(seq 1 "$1"); do
    DISPLAY=:98 xdotool search --onlyvisible --name "Aeronautes" >/dev/null 2>&1 || return 0
    sleep 1
  done
  return 1
}

# ADR 0082's product-level proof: the switch is read from the environment at
# startup by a REAL process, and what the operator is told about it is the
# startup log. A test binary lifts the capability gate, so only a launched
# binary can show this. Each case launches, proves startup from that process's
# own state database and window, asserts the lines it expects, and is confirmed
# stopped before the next case starts.
say "capability switch at startup"
switch_case() {
  local label="$1" spec="$2"; shift 2
  local log="$ART/switch-$label.log" state
  # One Aeronautes window on the display at a time, so the window this case
  # waits for is its own.
  wait_no_window 30 || fail "a window from an earlier case never went away"
  state=$(mktemp -d "$PWD/$ART/state-$label.XXXXXX")
  start_app "$log" AERONAUTES_ENABLE="$spec" AERONAUTES_STATE_DIR="$state"
  local group="$APP_LAST_GROUP"
  printf 'case=%s spec=%q state=%s group=%s\n' "$label" "$spec" "$state" "$group" \
    >>"$ART/switch-launches.txt"
  local failure=""
  file_saw "$state/state.sqlite" 60 \
    || failure="the app never opened a state database under $state"
  if [ -z "$failure" ]; then
    wait_window 60 || failure="the app never showed a window"
  fi
  if [ -z "$failure" ]; then
    local want
    for want in "$@"; do
      log_saw "$log" "$want" 20 || failure="no log line: $want"
    done
  fi
  # Confirmed gone before the next case: it holds a window, a state lock and a
  # log this harness is about to read again.
  app_stop "$group" 20 || fail "the app with AERONAUTES_ENABLE=$spec could not be stopped"
  if [ -n "$failure" ]; then
    printf '\n--- %s (%s bytes) ---\n' "$log" "$(wc -c <"$log")" >&2
    cat "$log" >&2
    fail "AERONAUTES_ENABLE=$spec: $failure (see $log)"
  fi
}
switch_case unset ""
if grep -q "AERONAUTES_ENABLE" "$ART/switch-unset.log"; then
  fail "an unset switch logged something about capabilities (see $ART/switch-unset.log)"
fi
# Copy was returned to the release on 31 October 2026 (ADR 0052 decision 12);
# its token no longer names a capability, so the switch says it was ignored
# rather than silently accepting a token that now does nothing.
switch_case copy "copy" \
  "AERONAUTES_ENABLE: ignored, not a capability: copy"
# Object deletion was returned to the release on 29 October 2026 (ADR 0052
# decision 11); its token no longer names a capability, so the switch says it
# was ignored rather than silently accepting a token that now does nothing.
switch_case object-delete "object-delete" \
  "AERONAUTES_ENABLE: ignored, not a capability: object-delete"
# Marker and manifest deletion was RETURNED on 1 November 2026 (ADR 0052
# decision 13), so its token no longer names a capability and the switch says
# it was ignored, as for the earlier returns.
switch_case marker-manifest-delete "marker-manifest-delete" \
  "AERONAUTES_ENABLE: ignored, not a capability: marker-manifest-delete"
# Bulk deletion was RETURNED on 1 November 2026 (ADR 0052 decision 14), so its
# token no longer names a capability and the switch says it was ignored, as
# for the earlier returns.
switch_case bulk-delete "bulk-delete" \
  "AERONAUTES_ENABLE: ignored, not a capability: bulk-delete"
switch_case unrecognised "benchmark, not-a-capability" \
  "AERONAUTES_ENABLE: offering unproven capabilities in this process: Benchmark" \
  "AERONAUTES_ENABLE: ignored, not a capability: not-a-capability"
# Mirror has no token deliberately (ADR 0052 decision 5, ADR 0082 decision 3),
# and "upload" stopped naming one when the operator returned that capability
# to the release (ADR 0052 decision 9), so both are ignored and SAID to be
# ignored. The two arrive on one line, in sorted order.
switch_case ignored "mirror, upload" \
  "AERONAUTES_ENABLE: ignored, not a capability: mirror, upload"

say "starting the app for the journey"
wait_no_window 30 || fail "a window from the switch cases never went away"
start_app "$ART/app.log"
APP_GROUP="$APP_LAST_GROUP"
printf 'case=journey state=%s group=%s\n' "$STATE_DIR" "$APP_GROUP" \
  >>"$ART/switch-launches.txt"

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
app_group_alive "$APP_GROUP" || fail "app exited at launch (see $ART/app.log)"

say "bookmark + connect"
connected=""
for attempt in 1 2 3; do
  X windowfocus --sync "$WIN" 2>/dev/null || true
  X key Escape; sleep 1                 # clear any half-open state
  X mousemove 42 150 click 1; sleep 2   # below the empty-list status
  # The dialog's focus trap puts focus on the Display-name field at
  # mount — type straight in, no pixel-hunting for the field.
  X type --delay 15 "Smoke"
  # The first Tab reaches the auth-type selector, which defaults to TempAuth
  # v1; a second Tab steps past it to the auth URL, so this still fills a v1
  # bookmark (the selector was added with the Keystone v3 auth type).
  X key Tab; X key Tab; X type --delay 10 "http://127.0.0.1:8899/auth/v1.0"
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
n=$(curl -s "$U/smoke-src?format=json" -H "X-Auth-Token: $TOK" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))")
[ "$n" = "3" ] || fail "smoke-src listing has $n of 3 objects"

say "PASS — browse landed; copy is deferred (ADR 0052). artifacts in $ART/"
