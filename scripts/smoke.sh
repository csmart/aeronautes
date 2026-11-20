#!/usr/bin/env bash
# Release smoke (§65.6 item 8): launch the real binary headless against
# the in-repo mock cluster and drive bookmark → connect → browse.
# Mirror and benchmark are deferred in a v1 build (ADR 0052); copy was
# returned on 31 October 2026 (decision 12), and bulk delete on 1 November
# 2026 (decision 14).
# Upload returned to the release on 16 October 2026 but, like download, needs
# a native picker this harness cannot operate, so neither is driven here. Screenshots
# and logs go to smoke-artifacts/. The §57.4 representative-cloud journeys
# run locally with test accounts as separate release evidence.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/app-process.sh
. scripts/app-process.sh

ART="smoke-artifacts"
rm -rf "$ART" && mkdir -p "$ART"
say() { printf '\nsmoke: %s\n' "$*"; }
fail() { printf '\nsmoke: FAIL: %s\n' "$*"; exit 1; }

for tool in Xvfb xdotool xclip import curl python3 cc dbus-daemon gdbus gnome-keyring-daemon; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
[ -x bin/aeronautes ] || fail "bin/aeronautes missing — run 'make build' first"

# Ubuntu's xdotool has no windowquit; windowclose destroys the window.
# Send the window manager's graceful request, only to a child of our app.
cc -Wall -Wextra -Werror -x c -o "$ART/request-close" - -lX11 <<'C'
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <stdlib.h>
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    Display *display = XOpenDisplay(NULL);
    if (!display) return 2;
    Window window = strtoul(argv[1], NULL, 0), parent = 0;
    if (!XGetTransientForHint(display, window, &parent) || parent != strtoul(argv[2], NULL, 0)) {
        XCloseDisplay(display);
        return 3;
    }
    XEvent event = {0};
    event.xclient.type = ClientMessage;
    event.xclient.window = window;
    event.xclient.message_type = XInternAtom(display, "WM_PROTOCOLS", False);
    event.xclient.format = 32;
    event.xclient.data.l[0] = XInternAtom(display, "WM_DELETE_WINDOW", False);
    int sent = XSendEvent(display, window, False, NoEventMask, &event);
    XSync(display, False);
    XCloseDisplay(display);
    return sent ? 0 : 1;
}
C

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

# Neither the app nor its secret service may use the caller's desktop state.
# Keep build caches outside this environment, and the keyring outside artifacts.
mkdir -p "$STATE_DIR"/{home,config,data,state,cache,runtime}
chmod 700 "$STATE_DIR/runtime"
SESSION_ENV=(HOME="$STATE_DIR/home" XDG_CONFIG_HOME="$STATE_DIR/config"
  XDG_DATA_HOME="$STATE_DIR/data" XDG_STATE_HOME="$STATE_DIR/state"
  XDG_CACHE_HOME="$STATE_DIR/cache" XDG_RUNTIME_DIR="$STATE_DIR/runtime"
  DBUS_SESSION_BUS_ADDRESS="unix:path=$STATE_DIR/session-bus" GNOME_KEYRING_CONTROL=)
say "starting a private session bus and GNOME keyring"
app_start "$ART/dbus.log" env "${SESSION_ENV[@]}" \
  dbus-daemon --session --nofork --address="unix:path=$STATE_DIR/session-bus"
# Owning the process group does not mean the bus accepts clients yet.
# The keyring does not reconnect if its first connection beats the socket.
bus_ready=""
for _ in $(seq 1 10); do
  if timeout 5 env "${SESSION_ENV[@]}" gdbus call --session --dest org.freedesktop.DBus \
      --object-path /org/freedesktop/DBus --method org.freedesktop.DBus.ListNames >/dev/null 2>&1; then
    bus_ready=yes; break
  fi
  sleep 1
done
[ -n "$bus_ready" ] || fail "the private session bus did not become ready"
printf '%s' 'owned-native-keyring-fixture' > "$STATE_DIR/home/unlock-password"
# Background commands otherwise lose stdin; attach it in the child that execs
# the daemon so it creates an unlocked login collection without a desktop prompt.
# shellcheck disable=SC2016
app_start "$ART/keyring.log" env "${SESSION_ENV[@]}" bash -c \
  'exec gnome-keyring-daemon --foreground --unlock --components=secrets --control-directory "$XDG_RUNTIME_DIR/keyring" < "$HOME/unlock-password"'
keyring_call() {
  local method="$1"; shift
  timeout 5 env "${SESSION_ENV[@]}" gdbus call --session --dest org.freedesktop.secrets \
    --object-path /org/freedesktop/secrets --method "org.freedesktop.Secret.Service.$method" "$@"
}
keyring_ready=""
for _ in $(seq 1 10); do
  if timeout 5 env "${SESSION_ENV[@]}" gdbus call --session --dest org.freedesktop.DBus \
      --object-path /org/freedesktop/DBus --method org.freedesktop.DBus.NameHasOwner org.freedesktop.secrets \
      | grep -q '(true,)' \
    && keyring_call ReadAlias default | grep -q "'/org/freedesktop/secrets/collection/login'"; then
    keyring_ready=yes; break
  fi
  sleep 1
done
[ -n "$keyring_ready" ] || fail "the private persistent keyring did not open"
# Search returns paths, never values. Count locked items too: a locked key
# still exists and cannot pass the deletion assertion.
keyring_count_is() {
  keyring_call SearchItems "{'service': 'aeronautes'}" \
    | python3 -c 'import re,sys; sys.exit(len(re.findall(r"/org/freedesktop/secrets/collection/[^\x27 ]+", sys.stdin.read())) != int(sys.argv[1]))' "$1"
}
keyring_count_is 0 || fail "the private keyring is not empty"

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

# The wrapper remains available for host launch requirements; the private
# desktop environment is applied after it. WebKit's sandbox is turned off by
# exporting
# WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1, which this script does NOT set:
# the caller does, and CI already does exactly that because its runner denies
# the user namespaces bubblewrap needs. The artifact records the effective value
# either way, because that variable weakens the webview's confinement and a
# reader of these artifacts should not have to guess whether it was on.
read -r -a APP_WRAPPER <<<"${SMOKE_APP_WRAPPER:-}"
APP_ENV=("${SESSION_ENV[@]}" AERONAUTES_STATE_DIR="$STATE_DIR" DISPLAY=:98 WAYLAND_DISPLAY=)
printf 'wrapper=%s WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=%s\n' \
  "${SMOKE_APP_WRAPPER:-<none>}" "${WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS:-<unset>}" \
  >"$ART/launch-environment.txt"

# start_app <log> [extra env assignments...] — the real binary, as shipped, in
# its own owned group. Sets APP_LAST_GROUP; never call it in a subshell.
start_app() {
  local log="$1"; shift
  app_start "$log" "${APP_WRAPPER[@]}" env -u AERONAUTES_LOG_FILE "${APP_ENV[@]}" "$@" ./bin/aeronautes
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
  start_app "$log" AERONAUTES_ENABLE="$spec" AERONAUTES_STATE_DIR="$state" AERONAUTES_LOG_FILE="$state/session.log"
  local group="$APP_LAST_GROUP"
  printf 'case=%s spec=%q state=%s group=%s\n' "$label" "$spec" "$state" "$group" \
    >>"$ART/switch-launches.txt"
  local failure=""
  file_saw "$state/aeronautes.sqlite" 60 \
    || failure="the app never opened its database under $state"
  if [ -z "$failure" ]; then
    wait_window 60 || failure="the app never showed a window"
  fi
  if [ -z "$failure" ]; then
    local want
    for want in "$@"; do
      log_saw "$log" "$want" 20 || failure="no log line: $want"
      log_saw "$state/session.log" "$want" 5 || failure="no configured-file log line: $want"
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
start_app "$ART/app.log" AERONAUTES_LOG_FILE="$STATE_DIR/native.log"
APP_GROUP="$APP_LAST_GROUP"
printf 'case=journey state=%s group=%s\n' "$STATE_DIR" "$APP_GROUP" \
  >>"$ART/switch-launches.txt"

X() { DISPLAY=:98 xdotool "$@"; }
shot() { DISPLAY=:98 import -window root "$ART/$1.png"; }
# The browser build has no clipboard. Read the native selection through
# a second client instead of accepting the page's "Copied" acknowledgement.
clipboard_is() {
  local value
  for _ in $(seq 1 10); do
    if value=$(timeout 2 env DISPLAY=:98 xclip -selection clipboard -out 2>/dev/null) && [ "$value" = "$1" ]; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}
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
keyring_count_is 1 || fail "the bookmark key was not saved in the private keyring"
sleep 1
shot 01-connected

say "open smoke-src"
opened=""
for attempt in 1 2 3; do
  # The second container row (smoke-out sorts first), below the column's
  # filter field.
  X mousemove 160 195 click 1
  if wire_saw "GET /v1/AUTH_test/smoke-src" 8; then opened=yes; break; fi
  say "container open attempt $attempt saw no listing; retrying"
done
[ -n "$opened" ] || fail "smoke-src never opened"
sleep 1
shot 02-browse
n=$(curl -s "$U/smoke-src?format=json" -H "X-Auth-Token: $TOK" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))")
[ "$n" = "3" ] || fail "smoke-src listing has $n of 3 objects"

say "copy an object name to the native clipboard"
X mousemove 420 230 click 3
sleep 1
X key --delay 100 Down Down Return
clipboard_is "alpha.txt" || fail "Copy name did not reach the native clipboard"
wire_saw 'HEAD /v1/AUTH_test/smoke-src/alpha.txt$' 5 || fail "the selected object's details were not read"
shot 03-object-clipboard

# The menu returns focus to the list; tab through the two object actions,
# Size and Content type to copy ETag without depending on text wrapping.
X key --delay 100 Tab Tab Tab Tab Tab Return
etag=$(python3 -c 'import hashlib; print(hashlib.md5(b"payload-alpha.txt", usedforsecurity=False).hexdigest())')
clipboard_is "$etag" || fail "Copy ETag did not copy the known payload's checksum"
shot 04-etag-clipboard

say "copy the log path from native settings"
X mousemove 24 669 click 1
sleep 1
X key Tab
X type --delay 30 "Keep a log file"
X key --delay 100 Tab Tab Return
clipboard_is "$STATE_DIR/native.log" || fail "Settings did not copy the active log file's path"
# A healthy session can be silent; the startup cases above separately prove
# that a configured file receives the diagnostics the app actually emits.
[ -f "$STATE_DIR/native.log" ] || fail "the named native log file was not opened"
shot 05-log-path-clipboard
X key Escape

say "refuse a native question with Escape and a window-close request"
NATIVE_PID=$(X getwindowpid "$WIN")
if DISPLAY=:98 "$ART/request-close" "$WIN" "$WIN"; then
  fail "the close helper accepted the main window as its own dialog"
fi
question_window() {
  local found
  for _ in $(seq 1 30); do
    found=$(X search --onlyvisible --pid "$NATIVE_PID" | awk -v main="$WIN" '$0 != main' || true)
    if [[ "$found" =~ ^[0-9]+$ ]]; then printf '%s\n' "$found"; return 0; fi
    sleep 0.2
  done
  return 1
}
X mousemove 160 195 click 1
sleep 1
X mousemove 1010 85 click 1
sleep 1
X key --delay 100 Tab space
# Tab through the offered fields to the acknowledgement. This scrolls the
# narrow inspector naturally without depending on the warning's wrapping.
X key --delay 100 Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab space Tab Tab Return
QUESTION=$(question_window) || { shot question-missing; fail "the native public-read question did not open"; }
posts_before=$(grep -c 'POST /v1/AUTH_test/smoke-src$' "$ART/mockswift.log" || true)
for dismissal in escape close; do
  sleep 0.3
  shot "question-$dismissal"
  if [ "$dismissal" = escape ]; then
    X windowfocus --sync "$QUESTION"
    X key Escape
  else
    DISPLAY=:98 "$ART/request-close" "$QUESTION" "$WIN"
  fi
  closed=""
  for _ in $(seq 1 30); do
    if ! X search --onlyvisible --pid "$NATIVE_PID" | grep -qx "$QUESTION"; then closed=yes; break; fi
    sleep 0.2
  done
  [ -n "$closed" ] || fail "native $dismissal left the question open"
  sleep 1
  # Opening it again proves the previous answer returned and Save is usable.
  # Cycle focus back into the webview after GTK's modal surface loses it.
  X windowfocus --sync "$WIN"
  X key --delay 100 Tab shift+Tab Return
  QUESTION=$(question_window) || { shot question-not-returned; fail "the native $dismissal did not return to a usable editor"; }
  posts_after=$(grep -c 'POST /v1/AUTH_test/smoke-src$' "$ART/mockswift.log" || true)
  [ "$posts_after" = "$posts_before" ] || fail "native $dismissal sent a container write"
  printf 'dismissal=%s posts_before=%s posts_after=%s\n' "$dismissal" "$posts_before" "$posts_after" \
    >> "$ART/native-refusals.txt"
  curl -fsSI "$U/smoke-src" -H "X-Auth-Token: $TOK" > "$ART/question-$dismissal.headers"
  python3 - "$ART/question-$dismissal.headers" <<'PY' || fail "native $dismissal made the container public"
import sys
from pathlib import Path
for line in Path(sys.argv[1]).read_text().splitlines():
    name, separator, value = line.partition(":")
    if separator and name.lower() == "x-container-read" and value.strip():
        sys.exit(1)
PY
done
X windowfocus --sync "$QUESTION"
X key Return
sleep 1
X windowfocus --sync "$WIN"

say "refresh containers when native focus returns"
X windowfocus --sync 0
reads_before=$(grep -c 'GET /v1/AUTH_test$' "$ART/mockswift.log")
curl -sf -X PUT "$U/smoke-return" -H "X-Auth-Token: $TOK" -o /dev/null
# The production shell uses the real clock: crossing its 30-second boundary
# and restoring X input focus must reach the webview's focus handler.
sleep 31
[ "$(grep -c 'GET /v1/AUTH_test$' "$ART/mockswift.log")" = "$reads_before" ] \
  || fail "the account was read before native focus returned"
X windowfocus --sync "$WIN"
refreshed=""
for _ in $(seq 1 10); do
  if [ "$(grep -c 'GET /v1/AUTH_test$' "$ART/mockswift.log")" -gt "$reads_before" ]; then
    refreshed=yes; break
  fi
  sleep 1
done
[ -n "$refreshed" ] || fail "returning native focus did not refresh the container list"
sleep 1
# The new container sorts between smoke-out and smoke-src. Opening it proves
# the refreshed result reached the column, beyond observing a background GET.
X mousemove 160 195 click 1
wire_saw 'GET /v1/AUTH_test/smoke-return' 10 || fail "the refreshed container was not shown"
shot 06-focus-return

say "reconnect after restart using the saved key"
app_stop "$APP_GROUP" 20 || fail "the journey app did not stop"
wait_no_window 30 || fail "the stopped journey window remains"
reads_before=$(grep -c 'GET /v1/AUTH_test$' "$ART/mockswift.log")
start_app "$ART/restarted-app.log" AERONAUTES_LOG_FILE="$STATE_DIR/native.log"
APP_GROUP="$APP_LAST_GROUP"
WIN=$(timeout 30 env DISPLAY=:98 xdotool search --sync --onlyvisible --name "Aeronautes" | head -1) \
  || fail "the restarted app window never appeared"
X windowfocus --sync "$WIN"
sleep 2
X mousemove 42 75 click 1
reconnected=""
for _ in $(seq 1 10); do
  if [ "$(grep -c 'GET /v1/AUTH_test$' "$ART/mockswift.log")" -gt "$reads_before" ]; then
    reconnected=yes; break
  fi
  sleep 1
done
[ -n "$reconnected" ] || fail "the restarted app did not reconnect with its saved key"
keyring_count_is 1 || fail "restart changed the saved key count"
shot 07-keyring-reconnect
X key --delay 100 Tab Return
sleep 1
shot 08-keyring-editor

say "delete the bookmark and its actual keyring item"
X mousemove 470 684 click 1
sleep 1
X click 1
cleared=""
for _ in $(seq 1 10); do
  if keyring_count_is 0 && timeout 5 python3 - "$STATE_DIR/aeronautes.sqlite" <<'PY'
import pathlib, sqlite3, sys
with sqlite3.connect(pathlib.Path(sys.argv[1]).as_uri() + "?mode=ro", uri=True, timeout=3) as db:
    counts = db.execute("SELECT (SELECT COUNT(*) FROM bookmarks), (SELECT COUNT(*) FROM credential_work)").fetchone()
sys.exit(counts != (0, 0))
PY
  then
    cleared=yes; break
  fi
  sleep 1
done
[ -n "$cleared" ] || fail "bookmark deletion left its keyring item or recorded cleanup work"
shot 09-keyring-deleted

say "PASS — browse, native refusals, clipboard, focus refresh and GNOME keyring lifecycle landed; returned capabilities' tokens are reported as ignored (ADR 0052). artifacts in $ART/"
