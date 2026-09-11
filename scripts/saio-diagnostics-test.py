#!/usr/bin/env python3
"""Exercise diagnostic collection using real, disposable child processes."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("saio-diagnostics.py")
sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("saio_diagnostics", SCRIPT)
DIAGNOSTICS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DIAGNOSTICS)


class DiagnosticsTests(unittest.TestCase):
    def error_log_probe(self, directory):
        probes = dict(DIAGNOSTICS.PROBES)
        self.assertIn("swift-error-log", probes)
        argv = probes["swift-error-log"][3:]
        argv[-1] = str(directory)
        return argv

    def test_rotated_errors_survive_later_traffic(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "@400000006aa3b88b155140a4.s").write_text(
                "HTTP 503 tx-earlier AUTH_tk0123456789\nX-Auth-Key: secret-key\n")
            (root / "@400000006aa3b88b155140a5.u").write_text("unfinished rotation tx-second\n")
            (root / "current").write_text("later cleanup\n" * 10001 + "tx-current\n")
            (root / "state").write_text("excluded state contents\n")
            result = DIAGNOSTICS.probe(self.error_log_probe(root))
        self.assertEqual(result["exit"], 0)
        self.assertFalse(result["timed_out"])
        self.assertFalse(result["output_truncated"])
        for retained in ("HTTP 503 tx-earlier", "unfinished rotation tx-second", "tx-current"):
            self.assertIn(retained, result["output"])
        for excluded in ("AUTH_tk0123456789", "secret-key", "excluded state contents"):
            self.assertNotIn(excluded, result["output"])
        self.assertLess(result["output"].index("tx-earlier"), result["output"].index("tx-current"))

    def test_error_log_without_rotations_is_successful(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "current").write_text("current error tx-only\n")
            result = DIAGNOSTICS.probe(self.error_log_probe(root))
        self.assertEqual(result["exit"], 0)
        self.assertEqual(result["output"], "current error tx-only\n")

    def test_error_output_limit_keeps_the_earlier_rotated_error(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "@400000006aa3b88b155140a4.s").write_text(
                "HTTP 503 tx-earlier AUTH_tk0123456789\n")
            (root / "current").write_text(("later traffic " + "x" * 1000 + "\n") * 6000)
            with patch.object(DIAGNOSTICS, "PROBES", (("swift-error-log", self.error_log_probe(root)),)):
                output = root / "observations"
                DIAGNOSTICS.collect(output)
            result = json.loads((output / "swift-error-log.json").read_text())
        self.assertEqual(result["exit"], 0)
        self.assertTrue(result["output_truncated"])
        self.assertLessEqual(len(result["output"].encode()), DIAGNOSTICS.OUTPUT_LIMIT)
        self.assertTrue("HTTP 503 tx-earlier" in result["output"], "earlier rotated error was dropped")
        self.assertNotIn("AUTH_tk0123456789", result["output"])

    def test_blocked_reader_exits_before_probe_timeout(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            os.mkfifo(root / "current")
            argv = self.error_log_probe(root)
            if argv[0] == "timeout":
                argv[2:4] = ["0.1", "0.1"]
            result = DIAGNOSTICS.probe(argv, timeout=2)
        self.assertFalse(result["timed_out"])
        self.assertIsNotNone(result["exit"])
        self.assertNotEqual(result["exit"], 0)

    def test_missing_current_error_log_is_visible_and_later_probe_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "@400000006aa3b88b155140a4.s").write_text("retained error tx-before\n")
            probes = (("swift-error-log", self.error_log_probe(root)),
                      ("next", [sys.executable, "-c", "print('later probe completed')"]))
            with patch.object(DIAGNOSTICS, "PROBES", probes):
                output = root / "observations"
                DIAGNOSTICS.collect(output)
            failed = json.loads((output / "swift-error-log.json").read_text())
            later = json.loads((output / "next.json").read_text())
        self.assertNotEqual(failed["exit"], 0)
        self.assertIn("retained error tx-before", failed["output"])
        self.assertIn("current", failed["output"])
        self.assertIn("No such file", failed["output"])
        self.assertEqual(later["exit"], 0)
        self.assertEqual(later["output"], "later probe completed\n")

    def test_success_keeps_output_and_command_status(self):
        argv = [sys.executable, "-c", "import sys; print('out'); print('err', file=sys.stderr)"]
        result = DIAGNOSTICS.probe(argv)
        self.assertEqual(result["argv"], argv)
        self.assertEqual(result["exit"], 0)
        self.assertFalse(result["timed_out"])
        self.assertFalse(result["output_truncated"])
        self.assertIn("out\n", result["output"])
        self.assertIn("err\n", result["output"])
        self.assertTrue(result["started"].endswith("+00:00"))

    def test_failed_command_does_not_hide_later_probes(self):
        probes = (
            ("missing-container", [sys.executable, "-c",
                                   "import sys; print('No such container: saio'); sys.exit(1)"]),
            ("missing-command", ["/aeronautes-diagnostic-command-does-not-exist"]),
            ("next", [sys.executable, "-c", "print('later probe completed')"]),
        )
        with tempfile.TemporaryDirectory() as directory, patch.object(DIAGNOSTICS, "PROBES", probes):
            root = Path(directory)
            DIAGNOSTICS.collect(root)
            records = {p.stem: json.loads(p.read_text()) for p in root.glob("*.json")}
        self.assertEqual(set(records), {"missing-container", "missing-command", "next"})
        self.assertEqual(records["missing-container"]["exit"], 1)
        self.assertIn("No such container", records["missing-container"]["output"])
        self.assertIsNone(records["missing-command"]["exit"])
        self.assertIn("No such file", records["missing-command"]["error"])
        self.assertEqual(records["next"]["exit"], 0)
        self.assertEqual(records["next"]["output"], "later probe completed\n")

    def test_timeout_retains_partial_output(self):
        started = time.monotonic()
        result = DIAGNOSTICS.probe(
            [sys.executable, "-c", "import time; print('before timeout', flush=True); time.sleep(60)"],
            timeout=0.5,
        )
        self.assertLess(time.monotonic() - started, 5)
        self.assertIsNone(result["exit"])
        self.assertTrue(result["timed_out"])
        self.assertEqual(result["output"], "before timeout\n")

    def test_timeout_does_not_hide_later_probes(self):
        slow = [sys.executable, "-c", "import time; time.sleep(60)"]
        probes = (("slow", slow), ("next", [sys.executable, "-c", "print('after timeout')"]))
        original_probe = DIAGNOSTICS.probe
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(DIAGNOSTICS, "PROBES", probes), \
                patch.object(DIAGNOSTICS, "probe", side_effect=lambda argv, **kwargs: original_probe(
                    argv, timeout=0.5 if argv == slow else DIAGNOSTICS.PROBE_TIMEOUT, **kwargs)):
            root = Path(directory)
            DIAGNOSTICS.collect(root)
            self.assertTrue(json.loads((root / "slow.json").read_text())["timed_out"])
            later = json.loads((root / "next.json").read_text())
            self.assertEqual(later["exit"], 0)
            self.assertEqual(later["output"], "after timeout\n")

    def test_authentication_is_removed_before_truncation(self):
        output = (b"HTTP 503 tx-request-kept\nAUTH_tk0123456789 secret-token-field\n"
                  b"X-Auth-Token: header-secret\nauthorization: Basic secret-credentials\n"
                  b"x-storage-pass: password\nCookie: cookie-secret\n"
                  b"\xff non-utf8 diagnostic\n")
        text = DIAGNOSTICS.redact(output)
        for secret in ("AUTH_tk0123456789", "header-secret", "secret-credentials", "password", "cookie-secret"):
            self.assertNotIn(secret, text)
        self.assertIn("HTTP 503 tx-request-kept", text)
        self.assertIn("non-utf8 diagnostic", text)
        with patch.object(DIAGNOSTICS, "OUTPUT_LIMIT", 12):
            result = DIAGNOSTICS.probe([sys.executable, "-c", "print('AUTH_tk0123456789')"])
        self.assertTrue(result["output_truncated"])
        self.assertNotIn("0123456789", result["output"])
        self.assertLessEqual(len(result["output"].encode("utf-8")), 12)

    def test_output_limit_preserves_valid_utf8(self):
        with patch.object(DIAGNOSTICS, "OUTPUT_LIMIT", 4):
            result = DIAGNOSTICS.probe([sys.executable, "-c", "print('é' * 10, end='')"])
        self.assertTrue(result["output_truncated"])
        self.assertEqual(result["output"], "éé")
        with patch.object(DIAGNOSTICS, "OUTPUT_LIMIT", 3):
            result = DIAGNOSTICS.probe([sys.executable, "-c", "print('é' * 10, end='')"])
        self.assertEqual(result["output"], "é")

    def test_output_write_failure_is_not_success(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            blocked = root / "blocked"
            blocked.write_text("existing file")
            with self.assertRaises(FileExistsError):
                DIAGNOSTICS.collect(blocked)

    def test_cli_requires_output_directory(self):
        result = subprocess.run([sys.executable, str(SCRIPT)], capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usage:", result.stderr)


if __name__ == "__main__":
    unittest.main()
