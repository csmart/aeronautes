#!/usr/bin/env python3
"""Check the pinned fixture's logging correction and startup refusals."""

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("saio_start", Path(__file__).with_name("saio-start.py"))
START = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(START)


class StartupTests(unittest.TestCase):
    def test_only_the_original_error_selector_changes(self):
        before = b"# keep these bytes\r\n-\n+\\..err:\n+\\.crit:\nT\n/log/errors\n"
        expected = b"# keep these bytes\r\n-\n+\\.err:\n+\\.crit:\nT\n/log/errors\n"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rules"
            path.write_bytes(before)
            START.configure_error_log(path)
            self.assertEqual(path.read_bytes(), expected)

    def test_corrected_rule_is_accepted_without_a_write(self):
        before = b"-\n+\\.err:\nT\n/log/errors\n"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rules"
            path.write_bytes(before)
            with patch.object(Path, "write_bytes", side_effect=AssertionError("unexpected write")):
                START.configure_error_log(path)
            self.assertEqual(path.read_bytes(), before)

    def test_unexpected_rules_are_refused_without_writes(self):
        original = b"+\\..err:\n"
        corrected = b"+\\.err:\n"
        cases = {
            "missing": b"-\n+\\.crit:\n",
            "duplicate original": original * 2,
            "duplicate corrected": corrected * 2,
            "both forms": original + corrected,
            "comment": b"# " + original,
            "embedded": b"prefix" + original,
            "different line ending": original.replace(b"\n", b"\r\n"),
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rules"
            for name, before in cases.items():
                with self.subTest(name=name):
                    path.write_bytes(before)
                    with self.assertRaisesRegex(ValueError, "expected one original or corrected"):
                        START.configure_error_log(path)
                    self.assertEqual(path.read_bytes(), before)

    def test_missing_rule_file_is_an_error(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(FileNotFoundError):
                START.configure_error_log(Path(directory) / "missing")

    def test_configuration_failure_prevents_image_initialization(self):
        for failure in (ValueError("unexpected rules"), OSError("cannot write rules")):
            with self.subTest(error=type(failure).__name__):
                with patch.object(START, "configure_error_log", side_effect=failure), \
                        patch.object(START.os, "execv") as initialize:
                    with self.assertRaises(type(failure)):
                        START.start()
                    initialize.assert_not_called()

    def test_success_runs_the_original_image_entrypoint_after_configuration(self):
        calls = []
        with patch.object(START, "configure_error_log", side_effect=lambda path: calls.append(("configure", path))), \
                patch.object(START.os, "execv", side_effect=lambda *args: calls.append(("initialize", *args))):
            START.start()
        self.assertEqual(calls, [
            ("configure", Path("/etc/socklog.rules/~-errors")),
            ("initialize", "/init", ["/init"]),
        ])


if __name__ == "__main__":
    unittest.main()
