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
    while lines[end].endswith("\\"):
        end += 1
    return "\n".join(lines[start:end + 1])


class LauncherIsolation(unittest.TestCase):
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
                    launch = (command(source, "APP_ENV=(") + "\n"
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
    local -a args=()
    while [ "$1" != dbus-run-session ]; do args+=("$1"); shift; done
    "${args[@]}" "$FIXTURE"
}
""" + launch
                env = dict(os.environ, FIXTURE=str(executable),
                           AERONAUTES_STATE_DIR=str(root / "outside-state"),
                           AERONAUTES_LOG_FILE=str(root / "outside.log"))
                result = subprocess.run(["bash", "-c", program, "launcher", str(private)],
                                        cwd="/", env=env, text=True, capture_output=True,
                                        timeout=10, check=True)
                observed = json.loads(result.stdout)
                for name, value in observed.items():
                    if launcher != "uijourneys" and not name.startswith("AERONAUTES_"):
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
