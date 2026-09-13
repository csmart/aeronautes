#!/usr/bin/env python3
"""Check that splitting the Swift suite cannot hide missing work."""

import copy
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
        report = {"schemaVersion": 3, "valid": True, "processSucceeded": True,
                  "streamComplete": True, "contractValid": True,
                  "contractHash": "same-contract", "totalCovered": 0}
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
                with self.assertRaises((ValueError, OSError)):
                    PARTITION.join(root / "main", heavy, root / "joined", IDENTITY)
                self.assertFalse((root / "joined" / "cover.out").exists())

    def test_join_keeps_main_credits_and_both_raw_reports(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.complete(root / "main", "main")
            self.complete(root / "heavy", "heavy")
            report = json.loads((root / "main" / "scenario-coverage.json").read_text())
            report.update(totalCovered=42, totalRequired=117, actionsWithGaps=38)
            write_json(root / "main" / "scenario-coverage.json", report)
            PARTITION.check(root / "main", 0, 0)
            PARTITION.join(root / "main", root / "heavy", root / "joined", IDENTITY)
            for lane, name in (("main", "scenario-coverage.json"),
                               ("heavy", "scenario-coverage-heavy.json")):
                self.assertEqual((root / "joined" / name).read_bytes(),
                                 (root / lane / "scenario-coverage.json").read_bytes())
            combined = (root / "joined" / "cover.out").read_text()
            self.assertEqual(combined.count("mode: atomic"), 1)
            self.assertEqual(len(combined.splitlines()), 3)


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
