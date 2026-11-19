#!/usr/bin/env bash
# UI journeys (ADR 0112): build the application in Wails server mode with the
# uijourney stand-ins, run it on loopback with its state in a fresh directory,
# and drive the real frontend from Chromium with Playwright, against the mock
# and, when CLOUDS names a clouds.yaml, the two real clouds. Local only.
#
# Everything that could name a provider, a host or an account goes to one
# private directory under the user cache directory; the output names only
# each journey, its outcome and that directory.
#
# Arguments are passed to Playwright, so one journey can be run, or watched:
#   scripts/uijourneys.sh --project=mock tests/slash.spec.ts
#   scripts/uijourneys.sh --headed
set -euo pipefail
cd "$(dirname "$0")/.."

private="${XDG_CACHE_HOME:-$HOME/.cache}/aeronautes-journeys/$(date -u +%Y%m%dT%H%M%SZ)-ui"
mkdir -p "$private"
chmod 700 "$private" "$(dirname "$private")"
state="$private/state"
mkdir -p "$state/home" "$state/config" "$state/data" "$state/state" "$state/cache"

pids=()
cleanup() {
	# The application's restart loop stops at this mark, and then the
	# application it last started is stopped with the rest.
	touch "$private/stopping"
	for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
	if [ -f "$private/app.pid" ]; then kill "$(cat "$private/app.pid")" 2>/dev/null || true; fi
	wait 2>/dev/null || true
}
trap cleanup EXIT

# The build's output can name paths and hosts, so it goes to the private
# directory. A step that fails there would otherwise end the run with nothing
# said, so the run says that it failed and where its output is.
build_failed() {
	echo "the build failed; see $private/build.log"
	exit 1
}

echo "building the frontend and the UI journey build"
{
	npm --prefix frontend ci --no-audit --no-fund &&
		npm --prefix frontend run build &&
		go build -tags server,uijourney -o "$private/aeronautes-uijourney" . &&
		go build -o "$private/mockswift" ./cmd/mockswift &&
		go test -tags server,uijourney -run 'TestJourneyControl|TestServeOptions' -count=1 .
} >"$private/build.log" 2>&1 || build_failed

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
mock_port=$(free_port)
"$private/mockswift" -addr "127.0.0.1:$mock_port" >"$private/mockswift.log" 2>&1 &
pids+=($!)

# The application's output is its own log, which can name hosts, so it goes
# to the private directory; its first line is the one the runner reads.
# A journey can quit the application as closing its window does (test plan
# step 9), and marks that it did; only then is it started again over the
# same state, on the addresses and token of its first start. An exit nobody
# asked for is not restarted, so a crash still fails the journeys after it.
start_app() {
	env -u WAILS_SERVER_HOST -u WAILS_SERVER_PORT \
		-u AERONAUTES_STATE_DIR -u AERONAUTES_LOG_FILE \
		HOME="$state/home" XDG_CONFIG_HOME="$state/config" XDG_DATA_HOME="$state/data" \
		XDG_STATE_HOME="$state/state" XDG_CACHE_HOME="$state/cache" \
		ORACLE_CLOUDS="${CLOUDS:-}" AERONAUTES_UIJOURNEY_LISTEN="$1" \
		"$private/aeronautes-uijourney" >>"$private/app.log" 2>&1 &
	echo $! >"$private/app.pid"
	wait "$!" || true
}
(
	start_app ""
	while [ -f "$private/restart" ] && [ ! -f "$private/stopping" ]; do
		rm -f "$private/restart"
		start_app "$(grep -m1 '^uijourney ' "$private/app.log")"
	done
) &
pids+=($!)
for _ in $(seq 100); do
	grep -q '^uijourney ' "$private/app.log" 2>/dev/null && break
	sleep 0.1
done
startup=$(grep -m1 '^uijourney ' "$private/app.log") || { echo "the UI journey build did not start; see $private"; exit 1; }
ui=${startup#*ui=}; ui=${ui%% *}
controller=${startup#*control=}; controller=${controller%% *}
token=${startup#*token=}

npm --prefix uijourneys ci --no-audit --no-fund >>"$private/build.log" 2>&1 || build_failed
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
uijourneys/node_modules/.bin/playwright install chromium >>"$private/build.log" 2>&1 || build_failed

AERONAUTES_UI="http://$ui" AERONAUTES_CONTROL="http://$controller" AERONAUTES_CONTROL_TOKEN="$token" \
	AERONAUTES_MOCK="http://127.0.0.1:$mock_port/auth/v1.0" AERONAUTES_CLOUDS="${CLOUDS:+1}" \
	AERONAUTES_PRIVATE="$private" \
	uijourneys/node_modules/.bin/playwright test --config uijourneys/playwright.config.ts "$@"
