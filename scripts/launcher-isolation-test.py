#!/usr/bin/env python3
"""Exercise launch environments without opening a window or operator state."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent


def command(source, prefix):
    lines = source.splitlines()
    start = next(i for i, line in enumerate(lines) if line.startswith(prefix))
    end = start
    while lines[end].endswith("\\") or (
            prefix.endswith("=(") and not lines[end].endswith(")")):
        end += 1
    return "\n".join(lines[start:end + 1])


class LauncherIsolation(unittest.TestCase):
    def test_browser_child_does_not_inherit_saio_credentials(self):
        source = (SCRIPTS / "uijourneys.sh").read_text()
        launch = command(source, 'AERONAUTES_UI=')
        variables = ["AERONAUTES_SAIO_AUTH_URL", "AERONAUTES_SAIO_USER",
                     "AERONAUTES_SAIO_KEY", "AERONAUTES_SAIO_USER2", "AERONAUTES_SAIO_KEY2"]
        for selected in (True, False):
            with self.subTest(selected=selected), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                browser = root / "uijourneys/node_modules/.bin/playwright"
                browser.parent.mkdir(parents=True)
                # Observe only variable presence; even a failing check need not
                # print a credential inherited from the parent environment.
                browser.write_text(
                    "#!/usr/bin/env python3\nimport json,os\n"
                    f"print(json.dumps({{k: k in os.environ for k in {variables!r}}}"
                    " | {'selected': os.environ.get('AERONAUTES_SAIO') == '1'}))\n"
                )
                browser.chmod(0o700)
                env = dict(os.environ, **dict.fromkeys(variables, "fixture-only"))
                env["AERONAUTES_SAIO_AUTH_URL"] = "http://fixture.invalid/auth/v1.0" if selected else ""
                program = '''
set -euo pipefail
ui=127.0.0.1:1; controller=127.0.0.1:2; token=fixture-only
mock_port=3; private=unused; CLOUDS=
''' + launch
                result = subprocess.run(["bash", "-c", program], cwd=root, env=env,
                                        text=True, capture_output=True, timeout=10, check=True)
                observed = json.loads(result.stdout)
                for variable in variables:
                    self.assertFalse(observed[variable], f"browser inherited {variable}")
                self.assertEqual(observed["selected"], selected)

    def test_keyring_waits_for_its_session_bus(self):
        source = (SCRIPTS / "smoke.sh").read_text()
        start = source.index('app_start "$ART/dbus.log"')
        startup = source[start:source.index("\nkeyring_call()", start)]
        for available in (True, False):
            with self.subTest(available=available), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "home").mkdir()
                # A live process can still refuse the first RPC. The keyring
                # stand-in rejects a start before any successful bus response.
                probe = root / "gdbus"
                probe.write_text('''#!/bin/sh
if [ "$BUS_AVAILABLE" = no ]; then exit 1; fi
if [ ! -e "$BUS_FIXTURE/attempted" ]; then
    touch "$BUS_FIXTURE/attempted"
    exit 1
fi
touch "$BUS_FIXTURE/ready"
''')
                probe.chmod(0o700)
                program = '''
set -euo pipefail
STATE_DIR="$1"; ART="$STATE_DIR"; SESSION_ENV=()
fail() { exit 1; }
sleep() { :; }
app_start() {
    if [ "$1" = "$ART/keyring.log" ]; then
        test -e "$STATE_DIR/ready" || exit 93
        touch "$STATE_DIR/keyring-started"
    fi
}
''' + startup
                env = dict(os.environ, BUS_FIXTURE=str(root),
                           BUS_AVAILABLE="yes" if available else "no",
                           PATH=f"{root}:{os.environ['PATH']}")
                result = subprocess.run(["bash", "-c", program, "startup", str(root)],
                                        env=env, text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 0 if available else 1,
                                 result.stdout + result.stderr)
                self.assertEqual((root / "keyring-started").exists(), available)

    def test_ci_refuses_cloud_credentials_before_building(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            npm = root / "npm"
            npm.write_text("#!/bin/sh\nexit 92\n")
            npm.chmod(0o700)
            env = dict(os.environ, CI="true", CLOUDS=str(root / "clouds.yaml"),
                       XDG_CACHE_HOME=str(root / "cache"),
                       PATH=f"{root}:{os.environ['PATH']}")
            result = subprocess.run(["bash", str(SCRIPTS / "uijourneys.sh")],
                                    env=env, text=True, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("real-cloud UI journeys run locally only", result.stderr)
            self.assertNotIn(env["CLOUDS"], result.stdout + result.stderr)
            self.assertFalse((root / "cache").exists())

    def test_socket_check_requires_successful_observation(self):
        source = (SCRIPTS / "webview-spike.sh").read_text()
        guard = source[source.index("PIDS=$(pgrep"):]
        for name, pids, process_status, sockets, socket_status, accepted in (
            ("no listeners", "123\n456", 0, "", 0, True),
            ("other process", "123\n456", 0, 'users:(("other",pid=789,fd=3))', 0, True),
            ("owned listener", "123\n456", 0, 'users:(("helper",pid=456,fd=3))', 0, False),
            ("socket tool failed", "123", 0, "", 127, False),
            ("partial socket result", "123", 0, "partial output", 1, False),
            ("process tool failed", "", 1, "", 0, False),
            ("empty process set", "", 0, "", 0, False),
        ):
            with self.subTest(case=name):
                env = dict(os.environ, PROBE_PIDS=pids, PROCESS_STATUS=str(process_status),
                           PROBE_SOCKETS=sockets, SOCKET_STATUS=str(socket_status))
                result = subprocess.run(["bash", "-c", """
set -euo pipefail
APP_GROUP=123
pgrep() { printf '%s' "$PROBE_PIDS"; return "$PROCESS_STATUS"; }
ss() { printf '%s' "$PROBE_SOCKETS"; return "$SOCKET_STATUS"; }
""" + guard], env=env, text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode == 0, accepted, result.stdout + result.stderr)
                self.assertEqual("PASS:" in result.stdout, accepted)

    def test_application_paths_belong_to_the_run(self):
        for launcher in ("uijourneys", "webview-spike", "smoke"):
            with self.subTest(launcher=launcher), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                private = root / "run"
                private.mkdir()
                executable = private / "aeronautes-uijourney"
                executable.write_text(
                    "#!/usr/bin/env python3\nimport json,os\n"
                    "print(json.dumps({k: os.environ.get(k) for k in "
                    "['AERONAUTES_STATE_DIR', 'AERONAUTES_LOG_FILE', 'HOME', "
                    "'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_CACHE_HOME']}))\n"
                )
                executable.chmod(0o700)
                # Preserve env on either side of the wrapper without opening a bus.
                wrapper = private / "dbus-run-session"
                wrapper.write_text('#!/bin/sh\nif [ "$1" = -- ]; then shift; fi\nexec "$@"\n')
                wrapper.chmod(0o700)
                source = (SCRIPTS / f"{launcher}.sh").read_text()
                if launcher == "uijourneys":
                    start = source.index("start_app() {")
                    end = source.index("\n}", start) + 2
                    launch = source[start:end] + '\nstart_app ""\ncat "$private/app.log"'
                elif launcher == "webview-spike":
                    launch = command(source, 'app_start "$APP_LOG" env')
                else:
                    start = source.index("start_app() {")
                    end = source.index("\n}", start) + 2
                    launch = (command(source, "SESSION_ENV=(") + "\n"
                              + command(source, "APP_ENV=(") + "\n"
                              + source[start:end] + '\nstart_app "$APP_LOG"')
                # app_start owns processes, not paths; intercept only that
                # external launch to observe the environment of its child.
                program = """
set -euo pipefail
private="$1"; state="$private/state"; STATE_DIR="$state"; ART="$private"
APP_LOG="$private/stdout"; XVFB_DISPLAY=:97; BIN=unused
APP_WRAPPER=(dbus-run-session)
app_start() {
    shift
    local -a args=("$@")
    args[${#args[@]}-1]="$FIXTURE"
    "${args[@]}"
}
""" + launch
                env = dict(os.environ, FIXTURE=str(executable),
                           PATH=f"{private}:{os.environ['PATH']}",
                           AERONAUTES_STATE_DIR=str(root / "outside-state"),
                           AERONAUTES_LOG_FILE=str(root / "outside.log"))
                result = subprocess.run(["bash", "-c", program, "launcher", str(private)],
                                        cwd="/", env=env, text=True, capture_output=True,
                                        timeout=10, check=True)
                observed = json.loads(result.stdout)
                for name, value in observed.items():
                    if launcher == "webview-spike" and not name.startswith("AERONAUTES_"):
                        continue
                    with self.subTest(variable=name):
                        if name == "AERONAUTES_LOG_FILE" or (
                                launcher == "uijourneys" and name == "AERONAUTES_STATE_DIR"):
                            # Overrides bypass private XDG paths or logging settings.
                            self.assertIsNone(value, f"{launcher}: inherited {name}")
                        else:
                            self.assertTrue(value, f"{launcher}: missing {name}")
                            self.assertTrue(Path(value).resolve().is_relative_to(private),
                                            f"{launcher}: {name} escaped the run: {value}")


if __name__ == "__main__":
    unittest.main()
