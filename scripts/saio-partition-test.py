#!/usr/bin/env python3
"""Check that splitting the Swift suite cannot hide missing work."""

import copy
import hashlib
import importlib.util
import json
import os
import re
import shutil
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location(
    "saio_partition", Path(__file__).with_name("saio-partition.py")
)
PARTITION = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PARTITION)
SYSTEM = "github.com/csmart/aeronautes/internal/systemtest"
IDENTITY = {"commit": "a" * 40, "go_version": "go1.27.1", "run_id": "123", "run_attempt": "1"}


def write_json(path, value):
    path.write_text(json.dumps(value) + "\n")


class PartitionTests(unittest.TestCase):
    def prepare(self, root, lane):
        root.mkdir()
        names = [*PARTITION.HEAVY, "TestOrdinary", "TestAddedLater"]
        (root / "all-packages.txt").write_text(SYSTEM + "\n")
        listing = [{"Action": "start", "Package": SYSTEM}]
        listing += [{"Action": "output", "Package": SYSTEM, "Output": name + "\n"} for name in names]
        listing += [{"Action": "pass", "Package": SYSTEM}]
        (root / "test-list.jsonl").write_text("".join(json.dumps(e) + "\n" for e in listing))
        PARTITION.prepare(root, lane, IDENTITY)
        return json.loads((root / "plan.json").read_text())

    def complete(self, root, lane):
        plan = self.prepare(root, lane)
        events = [{"Action": "start", "Package": SYSTEM}]
        for package, name in plan["selected"]:
            events.extend([{"Action": "run", "Package": package, "Test": name},
                           {"Action": "pass", "Package": package, "Test": name}])
        events.append({"Action": "pass", "Package": SYSTEM})
        (root / "events.jsonl").write_text("".join(json.dumps(e) + "\n" for e in events))
        report = control_report([CONTROL_CELL] if lane == "main" else (),
                                witness="TestOrdinary")
        write_json(root / "scenario-coverage.json", report)
        (root / "cover.out").write_text(
            "mode: atomic\ngithub.com/csmart/aeronautes/internal/example.go:1.1,1.2 1 1\n"
        )
        PARTITION.check(root, 0, 0)

    def test_closed_heavy_list_has_a_complete_derived_complement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            main = self.prepare(root / "main", "main")
            heavy = self.prepare(root / "heavy", "heavy")
            left, right = set(map(tuple, main["selected"])), set(map(tuple, heavy["selected"]))
            self.assertFalse(left & right)
            self.assertEqual(left | right, set(map(tuple, main["all_tests"])))
            self.assertIn((SYSTEM, "TestAddedLater"), left)
            self.assertEqual({name for package, name in right}, set(PARTITION.HEAVY))

    def test_heavy_names_are_unique(self):
        # A repeated name would silently shrink the selection, because the plan
        # builds a set from the tuple. With fifteen entries a duplicate is easy
        # to introduce and invisible in the resulting selection.
        self.assertEqual(len(PARTITION.HEAVY), len(set(PARTITION.HEAVY)))

    def test_pattern_matches_the_heavy_names_and_nothing_that_extends_them(self):
        # The lane selection is a regular expression handed to -run and -skip, so
        # an unanchored one would carry a sibling whose name merely starts with a
        # heavy name into the wrong lane. The longer the list, the likelier that
        # collision, so pin the anchoring rather than the current names.
        pattern = re.compile(PARTITION.pattern_text())
        for name in PARTITION.HEAVY:
            self.assertRegex(name, pattern)
            for affix in ("Mock", "Two", "Extra"):
                self.assertNotRegex(name + affix, pattern)
                self.assertNotRegex(affix + name, pattern)
        self.assertNotRegex("TestOrdinary", pattern)

    def test_missing_or_duplicate_listing_is_refused(self):
        for change in ("missing", "duplicate", "incomplete"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "main"
                self.prepare(root, "main")
                listing = [json.loads(line) for line in (root / "test-list.jsonl").read_text().splitlines()]
                if change == "missing":
                    listing = [e for e in listing if e.get("Output") != PARTITION.HEAVY[0] + "\n"]
                elif change == "duplicate":
                    listing.insert(1, copy.deepcopy(listing[1]))
                else:
                    listing.pop()
                (root / "test-list.jsonl").write_text("".join(json.dumps(e) + "\n" for e in listing))
                with self.assertRaises(ValueError):
                    PARTITION.prepare(root, "main", IDENTITY)

    def test_missing_overlap_failed_and_skipped_execution_is_refused(self):
        for change in ("missing", "duplicate", "wrong lane", "failed", "skipped", "truncated"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "heavy"
                self.complete(root, "heavy")
                events = [json.loads(line) for line in (root / "events.jsonl").read_text().splitlines()]
                if change == "missing":
                    events = [e for e in events if e.get("Test") != PARTITION.HEAVY[0]]
                elif change == "duplicate":
                    events.insert(3, copy.deepcopy(events[2]))
                elif change == "wrong lane":
                    events += [{"Action": "pass", "Package": SYSTEM, "Test": "TestOrdinary"}]
                elif change in ("failed", "skipped"):
                    events[2]["Action"] = "fail" if change == "failed" else "skip"
                else:
                    events.pop()
                (root / "events.jsonl").write_text("".join(json.dumps(e) + "\n" for e in events))
                with self.assertRaises(ValueError):
                    PARTITION.check(root, 0, 0)

    def test_real_producer_and_artifact_write_exits_are_required(self):
        for test_exit, tee_exit in ((1, 0), (0, 1), (124, 0)):
            with self.subTest(exits=(test_exit, tee_exit)), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "main"
                self.complete(root, "main")
                with self.assertRaises(ValueError):
                    PARTITION.check(root, test_exit, tee_exit)

    def test_heavy_credit_or_invalid_scenario_report_is_refused(self):
        for field, value in (("totalCovered", 1), ("valid", False), ("streamComplete", False)):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "heavy"
                self.complete(root, "heavy")
                report = json.loads((root / "scenario-coverage.json").read_text())
                report[field] = value
                write_json(root / "scenario-coverage.json", report)
                with self.assertRaises(ValueError):
                    PARTITION.check(root, 0, 0)

    def test_join_refuses_absent_failed_changed_or_foreign_artifacts(self):
        for change in ("absent directory", "missing receipt", "failed", "changed artifact",
                       "commit", "go_version", "run_id", "run_attempt", "overlapping selection"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                self.complete(root / "main", "main")
                self.complete(root / "heavy", "heavy")
                baseline = root / "baseline.control.json"
                write_json(baseline, control_baseline())
                heavy = root / "heavy"
                if change == "absent directory":
                    heavy = root / "absent"
                elif change == "missing receipt":
                    (heavy / "receipt.json").unlink()
                elif change == "failed":
                    write_json(heavy / "exits.json", {"test": 1, "tee": 0})
                elif change == "changed artifact":
                    (heavy / "cover.out").write_text("mode: atomic\n")
                else:
                    plan = json.loads((heavy / "plan.json").read_text())
                    if change == "overlapping selection":
                        plan["selected"].append([SYSTEM, "TestOrdinary"])
                    else:
                        plan["identity"][change] = "foreign"
                    write_json(heavy / "plan.json", plan)
                    if change != "overlapping selection":
                        PARTITION.check(heavy, 0, 0)
                with patch.object(PARTITION, "check_scenario_preservation") as compare:
                    with self.assertRaises((ValueError, OSError)):
                        PARTITION.join(root / "main", heavy, root / "joined", IDENTITY, baseline)
                    compare.assert_not_called()
                self.assertFalse((root / "joined" / "cover.out").exists())
                self.assertFalse((root / "joined" / "scenario-preservation.json").exists())

    def test_join_keeps_main_credits_and_both_raw_reports(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.complete(root / "main", "main")
            self.complete(root / "heavy", "heavy")
            actions = [{"action": f"SyntheticService.Action{n:02}", "kind": "destructive",
                        "required": ["happy", "refusal", "conflict"]} for n in range(39)]
            cells = [(row["action"], scenario) for n, row in enumerate(actions)
                     for scenario in row["required"][:3 if n == 0 else 2 if n == 1 else 1]]
            for lane in ("main", "heavy"):
                report = control_report(cells if lane == "main" else (), actions,
                                        witness="TestOrdinary")
                write_json(root / lane / "scenario-coverage.json", report)
                PARTITION.check(root / lane, 0, 0)
            baseline = root / "baseline.control.json"
            write_json(baseline, control_baseline(actions, cells))
            PARTITION.join(root / "main", root / "heavy", root / "joined", IDENTITY, baseline)
            report = json.loads((root / "joined" / "scenario-coverage.json").read_text())
            self.assertEqual((report["totalCovered"], report["totalRequired"], report["actionsWithGaps"]),
                             (42, 117, 38))
            for lane, name in (("main", "scenario-coverage.json"),
                               ("heavy", "scenario-coverage-heavy.json")):
                self.assertEqual((root / "joined" / name).read_bytes(),
                                 (root / lane / "scenario-coverage.json").read_bytes())
            combined = (root / "joined" / "cover.out").read_text()
            self.assertEqual(combined.count("mode: atomic"), 1)
            self.assertEqual(len(combined.splitlines()), 3)

    def test_join_comparison_follows_every_output_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.complete(root / "main", "main")
            self.complete(root / "heavy", "heavy")
            baseline = root / "baseline.control.json"
            write_json(baseline, control_baseline())
            output = root / "joined"
            compare = PARTITION.check_scenario_preservation

            def checked_compare(*args):
                for name in ("cover.out", "partition.json", "scenario-coverage.json",
                             "scenario-coverage-heavy.json"):
                    self.assertTrue((output / name).is_file(), "comparison preceded " + name)
                return compare(*args)

            with patch.object(PARTITION, "check_scenario_preservation", side_effect=checked_compare) as call:
                PARTITION.join(root / "main", root / "heavy", output, IDENTITY, baseline)
                call.assert_called_once()


CONTROL_ACTIONS = [
    {"action": "SyntheticService.Read", "kind": "read", "required": ["happy"]},
    {"action": "SyntheticService.Write", "kind": "mutate", "required": ["happy", "refusal"]},
]
CONTROL_CELL = ("SyntheticService.Read", "happy")


def control_fingerprint(actions):
    text = "".join(f'{row["action"]}|{row["kind"]}|{",".join(row["required"])}\n'
                   for row in sorted(actions, key=lambda row: row["action"]))
    return hashlib.sha256(text.encode()).hexdigest()[:16]


def control_report(cells=(), actions=None, identity=IDENTITY, witness="TestSyntheticControl"):
    """Synthetic component controls, never baseline admission evidence."""
    rows = copy.deepcopy(CONTROL_ACTIONS if actions is None else actions)
    for row in rows:
        covered = [scenario for scenario in row["required"] if (row["action"], scenario) in cells]
        row["covered"] = [{"scenario": scenario, "tests": [SYSTEM + "\t" + witness],
                           "evidence": ["synthetic control only"]} for scenario in covered] or None
        row["gaps"] = [scenario for scenario in row["required"] if scenario not in covered] or None
    gaps = sum(bool(row["gaps"]) for row in rows)
    return {"schemaVersion": 3, "valid": True, "processSucceeded": True,
            "streamComplete": True, "contractValid": True, "coverageComplete": gaps == 0,
            "commitSha": identity["commit"], "goVersion": identity["go_version"],
            "saioConfigured": False, "contractHash": control_fingerprint(rows),
            "totalActions": len(rows), "totalRequired": sum(len(row["required"]) for row in rows),
            "totalCovered": sum(len(row["covered"] or []) for row in rows),
            "actionsWithGaps": gaps, "nonJSONLines": 0, "actions": rows}


def control_baseline(actions=None, cells=None):
    actions = CONTROL_ACTIONS if actions is None else actions
    cells = [CONTROL_CELL] if cells is None else cells
    return {"schemaVersion": 1, "reportSchemaVersion": 3,
            "identity": {**IDENTITY, "commit": "b" * 40, "run_id": "100"},
            "contractHash": control_fingerprint(actions),
            "actions": copy.deepcopy(actions),
            "protected": [{"action": action, "scenario": scenario} for action, scenario in sorted(cells)],
            "evidence": {"reference": "synthetic control only; not admitted evidence",
                         "mainReportSHA256": "1" * 64, "heavyReportSHA256": "2" * 64,
                         "mainReceiptSHA256": "3" * 64, "heavyReceiptSHA256": "4" * 64}}


class ScenarioPreservationTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="scenario-control-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.main = self.root / "main.json"
        self.heavy = self.root / "heavy.json"
        self.baseline = self.root / "baseline.control.json"
        self.result = self.root / "preservation.json"
        self.reset_controls()

    def reset_controls(self):
        write_json(self.main, control_report([CONTROL_CELL]))
        write_json(self.heavy, control_report())
        write_json(self.baseline, control_baseline())

    def compare(self, identity=IDENTITY):
        return PARTITION.check_scenario_preservation(
            self.main, self.heavy, self.baseline, identity, self.result)

    def refused(self):
        before = {path: path.read_bytes() for path in (self.main, self.heavy, self.baseline)
                  if path.is_file()}
        with self.assertRaisesRegex(ValueError, "scenario preservation"):
            self.compare()
        result = json.loads(self.result.read_text())
        self.assertIs(result["preserved"], False)
        for path, data in before.items():
            self.assertEqual(path.read_bytes(), data, "comparison changed a raw input")
        return result

    def test_valid_incomplete_control_accepts_older_baseline_and_null_lists(self):
        result = self.compare()
        self.assertIs(result["preserved"], True)
        self.assertEqual(result["identity"], IDENTITY)
        self.assertEqual(result["baselineSHA256"], hashlib.sha256(self.baseline.read_bytes()).hexdigest())
        self.assertEqual(result["requiredDrift"], [])
        self.assertEqual(result["missingProtected"], [])
        self.assertEqual(result["errors"], [])
        self.assertEqual(json.loads(self.result.read_text()), result)
        self.assertIs(json.loads(self.main.read_text())["coverageComplete"], False)

    def test_lost_cell_fails_despite_unchanged_valid_incomplete_flags(self):
        write_json(self.main, control_report())
        result = self.refused()
        self.assertEqual(result["missingProtected"], [{"action": CONTROL_CELL[0], "scenario": "happy"}])
        self.assertEqual(result["requiredDrift"], [])
        self.assertEqual(result["errors"], [])
        self.assertIs(json.loads(self.main.read_text())["valid"], True)

    def test_equal_coverage_total_cannot_replace_protected_identity(self):
        write_json(self.main, control_report([("SyntheticService.Write", "happy")]))
        result = self.refused()
        self.assertEqual(json.loads(self.main.read_text())["totalCovered"], 1)
        self.assertEqual(result["missingProtected"], [{"action": CONTROL_CELL[0], "scenario": "happy"}])

    def test_additional_credits_and_changed_witness_do_not_change_baseline(self):
        report = control_report([CONTROL_CELL, ("SyntheticService.Write", "refusal")])
        report["actions"][0]["covered"][0].update(tests=[SYSTEM + "\tTestReplacement"], evidence=[""])
        write_json(self.main, report)
        before = self.baseline.read_bytes()
        self.assertIs(self.compare()["preserved"], True)
        self.assertEqual(self.baseline.read_bytes(), before)

    def test_drift_retains_missing_cells_even_when_required_identity_disappears(self):
        actions = [CONTROL_ACTIONS[1]]
        write_json(self.main, control_report(actions=actions))
        write_json(self.heavy, control_report(actions=actions))
        result = self.refused()
        self.assertEqual(result["requiredDrift"], [{"action": CONTROL_CELL[0],
                                                  "baseline": {"kind": "read", "required": ["happy"]},
                                                  "current": None}])
        self.assertEqual(result["missingProtected"], [{"action": CONTROL_CELL[0], "scenario": "happy"}])
        self.assertEqual(result["errors"], [])

    def test_new_required_cell_and_kind_change_are_drift(self):
        for change in ("added", "kind", "order"):
            with self.subTest(change=change):
                self.reset_controls()
                actions = copy.deepcopy(CONTROL_ACTIONS)
                if change == "added":
                    actions.append({"action": "SyntheticService.New", "kind": "read", "required": ["happy"]})
                elif change == "kind":
                    actions[0]["kind"] = "session"
                else:
                    actions[1]["required"].reverse()
                write_json(self.main, control_report([CONTROL_CELL], actions))
                write_json(self.heavy, control_report(actions=actions))
                result = self.refused()
                self.assertEqual(len(result["requiredDrift"]), 1)
                self.assertEqual(result["missingProtected"], [])

    def test_row_credit_and_witness_order_do_not_change_diagnostics(self):
        report = control_report([CONTROL_CELL, ("SyntheticService.Write", "happy"),
                                 ("SyntheticService.Write", "refusal")])
        report["actions"][0]["covered"][0].update(
            tests=[SYSTEM + "\tTestSecond", SYSTEM + "\tTestFirst"], evidence=["two", "one"])
        write_json(self.main, report)
        self.compare()
        before = self.result.read_bytes()
        report["actions"].reverse()
        report["actions"][0]["covered"].reverse()
        credit = report["actions"][1]["covered"][0]
        credit["tests"].reverse()
        credit["evidence"].reverse()
        write_json(self.main, report)
        self.compare()
        self.assertEqual(self.result.read_bytes(), before)

    def test_schema_flags_counts_and_error_contradictions_are_refused(self):
        bad = {"schemaVersion": 4, "valid": 1, "processSucceeded": False,
               "streamComplete": False, "contractValid": False, "coverageComplete": True,
               "totalActions": 1, "totalRequired": 2, "totalCovered": True,
               "actionsWithGaps": 0, "nonJSONLines": 1, "saioConfigured": "false",
               "protocolError": "error", "violations": ["bad"], "violationsTruncated": 1,
               "missingPackages": [SYSTEM], "failedPackages": [SYSTEM],
               "unexpectedPackages": [SYSTEM], "unrecognised": 1}
        for lane in (self.main, self.heavy):
            for field, value in bad.items():
                with self.subTest(lane=lane.name, field=field):
                    self.reset_controls()
                    report = json.loads(lane.read_text())
                    report[field] = value
                    write_json(lane, report)
                    self.assertTrue(self.refused()["errors"])

    def test_count_fields_reject_boolean_float_negative_and_missing_values(self):
        for field in ("totalActions", "totalRequired", "totalCovered", "actionsWithGaps", "nonJSONLines"):
            for value in (False, 0.0, -1, None):
                with self.subTest(field=field, value=value):
                    self.reset_controls()
                    report = control_report([CONTROL_CELL])
                    if value is None:
                        report.pop(field)
                    else:
                        report[field] = value
                    write_json(self.main, report)
                    self.assertTrue(self.refused()["errors"])

    def test_current_source_toolchain_and_fingerprint_must_match(self):
        for lane in (self.main, self.heavy):
            for field, value in (("commitSha", "c" * 40), ("goVersion", "go1.26.0"),
                                 ("contractHash", "0" * 16)):
                with self.subTest(lane=lane.name, field=field):
                    self.reset_controls()
                    report = json.loads(lane.read_text())
                    report[field] = value
                    write_json(lane, report)
                    self.assertTrue(self.refused()["errors"])

    def test_invalid_rows_sets_and_witnesses_are_refused(self):
        changes = {
            "empty inventory": lambda report: report.update(actions=[]),
            "duplicate action": lambda report: report["actions"].append(copy.deepcopy(report["actions"][0])),
            "unknown kind": lambda report: report["actions"][0].update(kind="transfer"),
            "bad identity": lambda report: report["actions"][0].update(action="bad name"),
            "duplicate requirement": lambda report: report["actions"][0].update(required=["happy", "happy"]),
            "unknown scenario": lambda report: report["actions"][0].update(required=["success"]),
            "missing gaps": lambda report: report["actions"][1].pop("gaps"),
            "wrong gaps": lambda report: report["actions"][1].update(gaps=[]),
            "duplicate credit": lambda report: report["actions"][0]["covered"].append(
                copy.deepcopy(report["actions"][0]["covered"][0])),
            "unrequired credit": lambda report: report["actions"][0]["covered"][0].update(scenario="refusal"),
            "empty witnesses": lambda report: report["actions"][0]["covered"][0].update(tests=[], evidence=[]),
            "duplicate witness": lambda report: report["actions"][0]["covered"][0].update(
                tests=[SYSTEM + "\tTestA"] * 2, evidence=["a", "b"]),
            "subtest witness": lambda report: report["actions"][0]["covered"][0].update(tests=[SYSTEM + "\tTestA/sub"]),
            "missing evidence": lambda report: report["actions"][0]["covered"][0].update(evidence=[]),
            "nonstring evidence": lambda report: report["actions"][0]["covered"][0].update(evidence=[False]),
        }
        for name, change in changes.items():
            with self.subTest(case=name):
                self.reset_controls()
                report = control_report([CONTROL_CELL])
                change(report)
                write_json(self.main, report)
                self.assertTrue(self.refused()["errors"])

    def test_baseline_metadata_and_protected_set_are_required(self):
        changes = {
            "schema": lambda value: value.update(schemaVersion=True),
            "report schema": lambda value: value.update(reportSchemaVersion=2),
            "missing provenance": lambda value: value.pop("evidence"),
            "missing digest": lambda value: value["evidence"].pop("mainReceiptSHA256"),
            "invalid digest": lambda value: value["evidence"].update(mainReportSHA256="short"),
            "empty reference": lambda value: value["evidence"].update(reference=""),
            "source identity": lambda value: value["identity"].update(commit="short"),
            "missing attempt": lambda value: value["identity"].pop("run_attempt"),
            "zero run": lambda value: value["identity"].update(run_id="0"),
            "empty protected": lambda value: value.update(protected=[]),
            "duplicate protected": lambda value: value["protected"].append(copy.deepcopy(value["protected"][0])),
            "unknown protected": lambda value: value["protected"][0].update(scenario="refusal"),
            "false fingerprint": lambda value: value.update(contractHash="0" * 16),
        }
        for name, change in changes.items():
            with self.subTest(case=name):
                self.reset_controls()
                baseline = control_baseline()
                change(baseline)
                write_json(self.baseline, baseline)
                self.assertTrue(self.refused()["errors"])

    def test_duplicate_json_keys_truncation_and_missing_input_leave_failure_artifact(self):
        for path in (self.main, self.heavy, self.baseline):
            for change in ("duplicate", "nested duplicate", "truncated", "trailing", "null", "missing"):
                with self.subTest(path=path.name, change=change):
                    self.reset_controls()
                    if change == "duplicate":
                        path.write_text('{"schemaVersion": 3, ' + path.read_text()[1:])
                    elif change == "nested duplicate":
                        path.write_text(path.read_text().replace('"kind": "read"',
                                                                '"kind": "read", "kind": "read"'))
                    elif change == "truncated":
                        path.write_text(path.read_text()[:-3])
                    elif change == "trailing":
                        path.write_text(path.read_text() + '{}')
                    elif change == "null":
                        path.write_text('null')
                    else:
                        path.unlink()
                    result = self.refused()
                    self.assertTrue(result["errors"])
                    self.assertIsNone(result["missingProtected"])

    def test_heavy_credits_and_different_lane_contracts_are_refused(self):
        write_json(self.heavy, control_report([CONTROL_CELL]))
        self.assertTrue(self.refused()["errors"])
        changed = copy.deepcopy(CONTROL_ACTIONS)
        changed[0]["kind"] = "session"
        write_json(self.heavy, control_report(actions=changed))
        self.assertTrue(self.refused()["errors"])

    def test_missing_candidate_identity_and_unwritable_result_cannot_succeed(self):
        for field in IDENTITY:
            with self.subTest(field=field):
                identity = dict(IDENTITY)
                identity.pop(field)
                with self.assertRaises(ValueError):
                    self.compare(identity)
                self.assertIs(json.loads(self.result.read_text())["preserved"], False)
        self.result.unlink()
        self.result.mkdir()
        with self.assertRaises(OSError):
            self.compare()

    def test_result_cannot_overwrite_any_raw_input_or_its_hardlink(self):
        for path in (self.main, self.heavy, self.baseline):
            with self.subTest(path=path.name):
                before = path.read_bytes()
                for result in (path, self.root / "input-alias.json"):
                    if result != path:
                        os.link(path, result)
                    with self.assertRaisesRegex(ValueError, "must not replace"):
                        PARTITION.check_scenario_preservation(
                            self.main, self.heavy, self.baseline, IDENTITY, result)
                    self.assertEqual(path.read_bytes(), before)
                    if result != path:
                        result.unlink()

    def test_join_refuses_loss_and_retains_valid_incomplete_reports(self):
        fixture = PartitionTests()
        fixture.complete(self.root / "main", "main")
        fixture.complete(self.root / "heavy", "heavy")
        for lane in ("main", "heavy"):
            write_json(self.root / lane / "scenario-coverage.json", control_report())
            PARTITION.check(self.root / lane, 0, 0)
        with self.assertRaisesRegex(ValueError, "scenario preservation"):
            PARTITION.join(self.root / "main", self.root / "heavy", self.root / "joined", IDENTITY,
                           self.baseline)
        report = json.loads((self.root / "joined" / "scenario-coverage.json").read_text())
        self.assertIs(report["valid"], True)
        self.assertEqual(report["totalCovered"], 0)
        self.assertIs(report["coverageComplete"], False)
        self.assertEqual(json.loads((self.root / "joined" / "scenario-preservation.json").read_text())[
            "missingProtected"], [{"action": CONTROL_CELL[0], "scenario": "happy"}])
        with self.assertRaisesRegex(ValueError, "scenario preservation"):
            PARTITION.check_scenario_preservation(
                self.root / "main" / "scenario-coverage.json",
                self.root / "heavy" / "scenario-coverage.json", self.baseline, IDENTITY, self.result)
        self.assertEqual(json.loads(self.result.read_text())["missingProtected"],
                         [{"action": CONTROL_CELL[0], "scenario": "happy"}])


REPO = Path(__file__).resolve().parent.parent
JOIN_STEP = "Validate both selections and combine coverage"
UPLOAD_STEP = "Upload combined coverage and scenario reports"
UPLOAD_PIN = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"


def workflow_job(text, name):
    matches = re.findall(r"^  " + re.escape(name) + r":\n(.*?)(?=^  [a-z_]+:|\Z)",
                         text, re.MULTILINE | re.DOTALL)
    if len(matches) != 1:
        raise AssertionError("expected one workflow job: " + name)
    return matches[0]


def workflow_step(job, name):
    matches = re.findall(r"^      - name: " + re.escape(name) + r"\n((?:^        .*\n|^\n)*)",
                         job, re.MULTILINE)
    if len(matches) != 1:
        raise AssertionError("expected one workflow step: " + name)
    return matches[0]


def workflow_join_script(text):
    step = workflow_step(workflow_job(text, "saio"), JOIN_STEP)
    if not step.startswith("        run: |\n"):
        raise AssertionError("join must be an ordinary hard-failing run step")
    lines = step.splitlines()[1:]
    if not lines or any(not line.startswith("          ") for line in lines):
        raise AssertionError("ambiguous join shell block")
    return "\n".join(line[10:] for line in lines) + "\n"


class JoinCLITests(unittest.TestCase):
    """Real CLI/shell processes over synthetic inputs, never admission evidence."""

    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="scenario-join-control-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.env = os.environ.copy()
        self.env.update(GOTOOLCHAIN="local", GOENV="off", GOWORK="off", GOPROXY="off",
                        GOSUMDB="off", GOFLAGS="-mod=readonly", GIT_CONFIG_NOSYSTEM="1",
                        GIT_CONFIG_GLOBAL=os.devnull)
        for name in ("scripts/saio-partition.py", "scripts/ci-gate.sh", ".github/workflows/ci.yml"):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(REPO / name, target)
        (self.root / "go.mod").write_text("module github.com/csmart/aeronautes\n\ngo 1.22.0\n")
        (self.root / ".gitignore").write_text(
            "/*\n!/.gitignore\n!/.github/\n!/scripts/\n!/go.mod\n!/internal/\n")
        (self.root / "internal").mkdir()
        (self.root / "internal/example.go").write_text(
            "package example\n\nfunc Value() int {\n    return 1\n}\n")
        self.baseline = self.root / "internal/scenariocontract/preservation-baseline.json"
        self.baseline.parent.mkdir()
        write_json(self.baseline, control_baseline())
        self.process(["git", "init", "--quiet"], success=True)
        self.commit_source()
        self.write_lanes()

    def process(self, argv, success=False, env=None):
        result = subprocess.run(argv, cwd=self.root, env=self.env if env is None else env,
                                text=True, capture_output=True, timeout=60)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        return result

    def commit_source(self):
        self.process(["git", "add", "--all", "--", ".gitignore", "scripts", ".github", "go.mod", "internal"],
                     success=True)
        self.process(["git", "-c", "user.name=Scenario Control", "-c", "user.email=control@example.invalid",
                      "-c", "commit.gpgsign=false", "commit", "--quiet", "-m",
                      "synthetic preservation control"], success=True)
        self.identity = {"commit": self.process(["git", "rev-parse", "HEAD"], True).stdout.strip(),
                         "go_version": self.process(["go", "env", "GOVERSION"], True).stdout.strip(),
                         "run_id": "123", "run_attempt": "1"}
        self.assertEqual(self.process(["git", "status", "--porcelain"], True).stdout, "")

    def write_lanes(self, cells=(CONTROL_CELL,), actions=None):
        for lane in ("main", "heavy"):
            root = self.root / ("saio-" + lane)
            if root.exists():
                shutil.rmtree(root)
            PartitionTests().complete(root, lane)
            PARTITION.prepare(root, lane, self.identity)
            write_json(root / "scenario-coverage.json",
                       control_report(cells if lane == "main" else (), actions,
                                      self.identity, "TestOrdinary"))
            (root / "cover.out").write_text(
                "mode: atomic\ngithub.com/csmart/aeronautes/internal/example.go:3.18,5.2 1 1\n")
            PARTITION.check(root, 0, 0)

    def raw_inputs(self):
        paths = [self.baseline] + [self.root / ("saio-" + lane) / name
                                  for lane in ("main", "heavy") for name in (*PARTITION.ARTIFACTS, "receipt.json")]
        return {path: path.read_bytes() for path in paths if path.is_file()}

    def cli(self, output, baseline=None, include_baseline=True):
        argv = [sys.executable, "scripts/saio-partition.py", "join", "saio-main", "saio-heavy", output]
        for name, value in self.identity.items():
            argv += ["--" + name.replace("_", "-"), value]
        if include_baseline:
            argv += ["--baseline", str(self.baseline if baseline is None else baseline)]
        before = self.raw_inputs()
        result = self.process(argv)
        self.assertEqual(self.raw_inputs(), before, "CLI changed raw inputs")
        return result

    def diagnostic(self, output):
        path = self.root / output / "scenario-preservation.json"
        self.assertTrue(path.is_file(), "preservation diagnostic was not retained")
        return json.loads(path.read_text())

    def test_baseline_argument_is_required_only_for_join(self):
        result = self.cli("missing-argument", include_baseline=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("--baseline", result.stderr)
        self.assertFalse((self.root / "missing-argument").exists())
        result = self.process([sys.executable, "scripts/saio-partition.py", "pattern"], True)
        self.assertEqual(result.stdout.strip(), PARTITION.pattern_text())

    def test_cli_preserves_or_refuses_with_retained_raw_reports(self):
        self.assertEqual(self.cli("retained").returncode, 0)
        result = self.diagnostic("retained")
        self.assertTrue(result["preserved"])
        self.assertEqual(result["identity"], self.identity)
        self.assertEqual(result["baselineSHA256"], hashlib.sha256(self.baseline.read_bytes()).hexdigest())
        self.write_lanes(cells=())
        failed = self.cli("lost")
        self.assertEqual(failed.returncode, 1)
        self.assertIn("scenario preservation", failed.stderr)
        result = self.diagnostic("lost")
        self.assertFalse(result["preserved"])
        self.assertEqual(result["missingProtected"], [{"action": CONTROL_CELL[0], "scenario": "happy"}])
        self.assertEqual((result["requiredDrift"], result["errors"]), ([], []))
        for output in ("retained", "lost"):
            report = json.loads((self.root / output / "scenario-coverage.json").read_text())
            self.assertTrue(report["valid"])
            self.assertFalse(report["coverageComplete"])
            self.assertTrue((self.root / output / "cover.out").is_file())
            self.assertEqual(json.loads((self.root / output / "partition.json").read_text())["identity"],
                             self.identity)
        for lane, name in (("main", "scenario-coverage.json"), ("heavy", "scenario-coverage-heavy.json")):
            self.assertEqual((self.root / "lost" / name).read_bytes(),
                             (self.root / ("saio-" + lane) / "scenario-coverage.json").read_bytes())

    def test_cli_missing_invalid_baseline_and_output_refuse(self):
        invalid = self.root / "invalid.control.json"
        invalid.write_text("{}\n")
        for name, baseline in (("absent-baseline", self.root / "absent.json"), ("invalid-baseline", invalid)):
            with self.subTest(case=name):
                self.assertEqual(self.cli(name, baseline).returncode, 1)
                result = self.diagnostic(name)
                self.assertFalse(result["preserved"])
                self.assertTrue(result["errors"][0].startswith("baseline:"))
                self.assertIsNone(result["missingProtected"])
        (self.root / "unwritable").write_text("owned output obstruction\n")
        self.assertEqual(self.cli("unwritable").returncode, 1)
        self.assertEqual((self.root / "unwritable").read_text(), "owned output obstruction\n")

    def test_cli_prerequisites_precede_comparison(self):
        cases = {"receipt": "missing or changed", "test": "both the test producer",
                 "tee": "both the test producer", "run": "incompatible source",
                 "source": "coverage source is absent", "heavy": "heavy scenario credits"}
        for case, expected in cases.items():
            with self.subTest(case=case):
                self.write_lanes()
                root = self.root / "saio-heavy"
                if case == "receipt":
                    (root / "events.jsonl").write_text("changed\n")
                elif case in ("test", "tee"):
                    write_json(root / "exits.json", {"test": int(case == "test"), "tee": int(case == "tee")})
                elif case == "run":
                    plan = json.loads((root / "plan.json").read_text())
                    plan["identity"]["run_attempt"] = "2"
                    write_json(root / "plan.json", plan)
                elif case == "source":
                    (root / "cover.out").write_text((root / "cover.out").read_text().replace("example.go", "absent.go"))
                else:
                    write_json(root / "scenario-coverage.json", control_report([CONTROL_CELL], identity=self.identity))
                if case != "receipt":
                    write_json(root / "receipt.json", {"sha256": PARTITION.hashes(root)})
                result = self.cli(case, self.root / "absent-baseline.json")
                self.assertEqual(result.returncode, 1)
                self.assertIn(expected, result.stderr)
                self.assertFalse((self.root / case / "scenario-preservation.json").exists())
                self.assertFalse((self.root / case / "cover.out").exists())

    def test_workflow_failure_retains_exit_and_blocks_required_gate(self):
        self.write_lanes(cells=())
        text = (self.root / ".github/workflows/ci.yml").read_text()
        script = workflow_join_script(text)
        env = {**self.env, "GITHUB_SHA": self.identity["commit"],
               "GITHUB_RUN_ID": self.identity["run_id"], "GITHUB_RUN_ATTEMPT": self.identity["run_attempt"]}
        before = self.raw_inputs()
        result = self.process(["bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script], env=env)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertTrue((self.root / "saio-combined/join-exit.txt").is_file(), "join exit was not retained")
        self.assertEqual((self.root / "saio-combined/join-exit.txt").read_text(), "1\n")
        self.assertFalse(self.diagnostic("saio-combined")["preserved"])
        self.assertFalse((self.root / "coverage-pct.txt").exists())
        self.assertEqual(self.raw_inputs(), before)
        gate = self.process(["bash", "scripts/ci-gate.sh", "success", "success",
                             "success" if result.returncode == 0 else "failure", "success"])
        self.assertEqual(gate.returncode, 1)
        self.assertIn("result: failure", gate.stderr)

    def test_workflow_success_and_early_failure_have_honest_artifacts(self):
        script = workflow_join_script((self.root / ".github/workflows/ci.yml").read_text())
        env = {**self.env, "GITHUB_SHA": self.identity["commit"],
               "GITHUB_RUN_ID": self.identity["run_id"], "GITHUB_RUN_ATTEMPT": self.identity["run_attempt"]}
        argv = ["bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script]
        self.process(argv, True, env)
        self.assertTrue((self.root / "saio-combined/join-exit.txt").is_file(), "join exit was not retained")
        self.assertEqual((self.root / "saio-combined/join-exit.txt").read_text(), "0\n")
        self.assertTrue(self.diagnostic("saio-combined")["preserved"])
        self.assertTrue((self.root / "coverage-pct.txt").is_file())
        shutil.move(self.root / "saio-combined", self.root / "retained-success")
        (self.root / "saio-heavy/receipt.json").unlink()
        result = self.process(argv, env=env)
        self.assertEqual(result.returncode, 1)
        self.assertTrue((self.root / "saio-combined/join-exit.txt").is_file(), "join exit was not retained")
        self.assertEqual((self.root / "saio-combined/join-exit.txt").read_text(), "1\n")
        self.assertFalse((self.root / "saio-combined/scenario-preservation.json").exists())
        self.assertFalse((self.root / "saio-combined/cover.out").exists())

    def test_drift_fixture_retains_failure_before_replacement(self):
        cells = [CONTROL_CELL, ("SyntheticService.Write", "happy")]
        write_json(self.baseline, control_baseline(cells=cells))
        self.commit_source()
        changed = [*CONTROL_ACTIONS, {"action": "SyntheticService.New", "kind": "read", "required": ["happy"]}]
        self.write_lanes(cells, changed)
        original_identity = dict(self.identity)
        self.assertEqual(self.cli("expected-drift").returncode, 1)
        self.assertEqual(len(self.diagnostic("expected-drift")["requiredDrift"]), 1)
        self.assertEqual(self.diagnostic("expected-drift")["missingProtected"], [])
        retained = {path: path.read_bytes() for path in (self.root / "expected-drift").iterdir()}
        self.write_lanes([CONTROL_CELL], changed)
        self.assertEqual(self.cli("unrelated-loss").returncode, 1)
        self.assertEqual(self.diagnostic("unrelated-loss")["missingProtected"],
                         [{"action": "SyntheticService.Write", "scenario": "happy"}])
        write_json(self.baseline, control_baseline(changed, cells))
        self.commit_source()
        self.write_lanes(cells, changed)
        self.assertNotEqual(self.identity["commit"], original_identity["commit"])
        self.assertEqual(self.cli("replacement-control").returncode, 0)
        self.assertTrue(self.diagnostic("replacement-control")["preserved"])
        self.assertEqual({path: path.read_bytes() for path in retained}, retained)


class WorkflowPreservationTests(unittest.TestCase):
    def test_failed_join_upload_and_required_result_stay_connected(self):
        text = (REPO / ".github/workflows/ci.yml").read_text()
        job = workflow_job(text, "saio")
        self.assertIn("    if: ${{ always() }}\n    needs: saio_tests\n", job)
        prerequisite = workflow_step(job, "Require both Swift fixtures to succeed")
        self.assertIn("PRODUCER_RESULT: ${{ needs.saio_tests.result }}", prerequisite)
        self.assertIn('run: test "$PRODUCER_RESULT" = success', prerequisite)
        script = workflow_join_script(text)
        self.assertIn("--baseline internal/scenariocontract/preservation-baseline.json", script)
        upload = workflow_step(job, UPLOAD_STEP)
        for line in ("        if: always()\n", "        uses: " + UPLOAD_PIN + " # v7\n",
                     "          name: scenario-coverage\n", "          path: saio-combined/\n",
                     "          if-no-files-found: error\n"):
            self.assertIn(line, upload)
        self.assertNotIn("continue-on-error", upload)
        required = workflow_job(text, "required")
        self.assertIn("    if: ${{ always() }}\n    needs: [go, frontend, saio, smoke]\n", required)
        gate = workflow_step(required, "Require every test job to succeed")
        self.assertIn("          SAIO_RESULT: ${{ needs.saio.result }}\n", gate)
        self.assertIn('        run: ./scripts/ci-gate.sh "$GO_RESULT" "$FRONTEND_RESULT" "$SAIO_RESULT" "$SMOKE_RESULT"\n', gate)
        self.assertNotIn("continue-on-error", gate)


HEADER = 'mode: atomic\n'
FILE = 'github.com/csmart/aeronautes/internal/probe/probe.go'
OTHER = 'github.com/csmart/aeronautes/internal/cold/cold.go'
MAXIMUM = (1 << 63) - 1


def profile(*records):
    return HEADER + ''.join(record + '\n' for record in records)


class CombineProfilesTests(unittest.TestCase):
    def setUp(self):
        self.record = f'{FILE}:3.2,5.3 2 0'
        self.valid = profile(self.record)

    def test_preserves_bodies_zeros_and_main_only_file(self):
        main = profile(self.record, f'{OTHER}:8.1,9.2 7 0')
        heavy = profile(f'{FILE}:3.2,5.3 2 4')
        self.assertEqual(PARTITION.combine_profiles(main, heavy), main + heavy[len(HEADER):])

    def test_repeated_matching_blocks_and_order_are_accepted(self):
        other = f'{FILE}:8.1,9.2 1 0'
        main = profile(self.record, other, self.record)
        heavy = profile(other, f'{FILE}:3.2,5.3 2 4', other)
        combined = PARTITION.combine_profiles(main, heavy)
        self.assertEqual(combined, main + heavy[len(HEADER):])
        self.assertEqual(combined.count(self.record + '\n'), 2)

    def test_zero_width_and_zero_statement_block(self):
        empty = profile(f'{FILE}:3.2,3.2 0 0')
        self.assertEqual(PARTITION.combine_profiles(empty, empty), empty + empty[len(HEADER):])

    def test_invalid_profiles_are_rejected_in_either_lane(self):
        invalid = {
            'empty': '', 'header_only': HEADER, 'missing_header': self.record + '\n',
            'count_mode': self.valid.replace('atomic', 'count'),
            'set_mode': self.valid.replace('atomic', 'set'),
            'incomplete': self.valid[:-1], 'blank_record': self.valid + '\n',
            'second_header': self.valid + HEADER, 'crlf': self.valid.replace('\n', '\r\n'),
            'absolute_path': self.valid.replace(FILE, '/tmp/probe.go'),
            'outside_internal': self.valid.replace('/internal/', '/external/'),
            'parent_component': self.valid.replace('/probe/probe.go', '/../probe.go'),
            'dot_component': self.valid.replace('/probe/probe.go', '/./probe.go'),
            'empty_component': self.valid.replace('/probe/probe.go', '//probe.go'),
            'backslash': self.valid.replace('/probe/probe.go', '/probe\\probe.go'),
            'not_go': self.valid.replace('.go:', '.txt:'),
            'filename_space': self.valid.replace('probe.go:', 'bad name.go:'),
            'negative_count': self.valid.replace(' 2 0', ' 2 -1'),
            'negative_statements': self.valid.replace(' 2 0', ' -1 0'),
            'extra_field': self.valid.replace(' 2 0', ' 2 0 4'),
            'zero_line': self.valid.replace(':3.2,', ':0.2,'),
            'zero_column': self.valid.replace(':3.2,', ':3.0,'),
            'reversed_lines': self.valid.replace('3.2,5.3', '5.2,3.3'),
            'reversed_columns': self.valid.replace('3.2,5.3', '3.3,3.2'),
            'unicode_digit': self.valid.replace(' 2 0', ' 2 ٠'),
            'overflow_field': self.valid.replace(' 2 0', f' 2 {MAXIMUM + 1}'),
            'overflow_statements': self.valid.replace(' 2 0', f' {MAXIMUM + 1} 0'),
            'overflow_position': self.valid.replace('3.2,5.3', f'3.2,{MAXIMUM + 1}.3'),
        }
        for name, invalid_profile in invalid.items():
            for lane in ('main', 'heavy'):
                with self.subTest(case=name, lane=lane):
                    args = (invalid_profile, self.valid) if lane == 'main' else (self.valid, invalid_profile)
                    with self.assertRaises(ValueError):
                        PARTITION.combine_profiles(*args)

    def test_shared_structure_must_match(self):
        for name, heavy in {
            'different_file': profile(self.record.replace(FILE, OTHER)),
            'changed_statements': profile(self.record.replace(' 2 0', ' 3 0')),
            'changed_positions': profile(self.record.replace('5.3', '5.4')),
            'missing_block': profile(self.record),
            'extra_block': profile(self.record, f'{FILE}:9.1,9.2 0 0'),
        }.items():
            main = self.valid if name != 'missing_block' else profile(self.record, f'{FILE}:7.1,7.2 1 0')
            with self.subTest(case=name), self.assertRaises(ValueError):
                PARTITION.combine_profiles(main, heavy)

    def test_conflicting_duplicate_statements_are_rejected(self):
        conflict = profile(self.record, self.record.replace(' 2 0', ' 3 0'))
        for args in ((conflict, self.valid), (self.valid, conflict)):
            with self.subTest(args=args), self.assertRaises(ValueError):
                PARTITION.combine_profiles(*args)

    def test_counter_sums_are_checked_within_and_across_lanes(self):
        largest = self.record.replace(' 2 0', f' 2 {MAXIMUM}')
        one = self.record.replace(' 2 0', ' 2 1')
        self.assertEqual(PARTITION.combine_profiles(profile(largest), self.valid), profile(largest, self.record))
        for main, heavy in ((profile(largest, one), self.valid),
                            (self.valid, profile(largest, one)), (profile(largest), profile(one))):
            with self.subTest(main=main, heavy=heavy), self.assertRaises(ValueError):
                PARTITION.combine_profiles(main, heavy)


class GoCoverageReaderTests(unittest.TestCase):
    def test_exclusive_branches_keep_the_zero_denominator(self):
        go = shutil.which('go')
        self.assertIsNotNone(go, 'Go is required for the coverage-reader control')
        with tempfile.TemporaryDirectory(prefix='coverage-reader-') as directory:
            root = Path(directory)
            (root / 'go.mod').write_text('module github.com/csmart/aeronautes\n\ngo 1.22.0\n')
            package = root / 'internal/probe'
            package.mkdir(parents=True)
            source = '''package probe

func Choose(left bool) int {
    if left {
        return 1
    }
    return 2
}

func Unused() int {
    return 3
}
'''
            (package / 'probe.go').write_text(source)
            (package / 'probe_test.go').write_text('''package probe
import "testing"
func TestLeft(t *testing.T) { if Choose(true) != 1 { t.Fatal("left") } }
func TestRight(t *testing.T) { if Choose(false) != 2 { t.Fatal("right") } }
''')
            env = os.environ.copy()
            env.update(GOTOOLCHAIN='local', GOENV='off', GOWORK='off', GOPROXY='off',
                       GOSUMDB='off', GOFLAGS='-mod=readonly')

            def run_go(*arguments):
                argv = [go, *arguments]
                result = subprocess.run(argv, cwd=root, env=env, text=True,
                                        capture_output=True, timeout=60)
                self.assertEqual(result.returncode, 0, result.stderr)
                return result.stdout

            run_go('version')
            for name, selector in (('main', '^TestLeft$'), ('heavy', '^TestRight$')):
                run_go('test', '-race', '-count=1', '-covermode=atomic', '-coverpkg=./internal/...',
                       '-coverprofile=' + name + '.out', '-run=' + selector, './internal/probe')
            main = (root / 'main.out').read_text()
            heavy = (root / 'heavy.out').read_text()
            combined = PARTITION.combine_profiles(main, heavy)
            (root / 'combined.out').write_text(combined)
            for name in ('main', 'heavy'):
                report = run_go('tool', 'cover', '-func=' + name + '.out')
                self.assertRegex(report, r'total:\s+\(statements\)\s+50\.0%')
            report = run_go('tool', 'cover', '-func=combined.out')
            self.assertRegex(report, r'Choose\s+100\.0%')
            self.assertRegex(report, r'Unused\s+0\.0%')
            self.assertRegex(report, r'total:\s+\(statements\)\s+75\.0%')



if __name__ == "__main__":
    unittest.main()
