#!/usr/bin/env python3
"""Keep the two Swift CI selections complete and their evidence compatible."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


SYSTEM = "github.com/csmart/aeronautes/internal/systemtest"
# The heavy fixture carries the slowest real-Swift groups so the main lane keeps
# margin against its package budget. The names are a fixed selection, not a rule
# the schedule re-evaluates: they were chosen once from the measured 76c30e90 run
# as the systemtest tests that earn no scenario credit and took at least sixty-five
# seconds. A heavy test must earn no scenario credit, because join() refuses a
# heavy report with any.
HEAVY = (
    "TestCopySegmentChangeDetectionRealSwift",
    "TestUploadItemRecordsRealSwift",
    "TestDownloadRecordsRealSwift",
    "TestUploadRecoveryRealSwift",
    "TestCopyItemRecordsRealSwift",
    "TestDownloadDestinationRealSwift",
    "TestDownloadRootGuardRealSwift",
    "TestDownloadNameFormRealSwift",
    "TestCopyCompletionRealSwift",
    "TestCopyInterruptionRealSwift",
    "TestDownloadRecordRefusalRealSwift",
    "TestLocalCompletionRealSwift",
    "TestCopyUnchangedRecordsRealSwift",
    "TestDownloadNameEncodingRealSwift",
    "TestCopyStagedContentRealSwift",
)
ARTIFACTS = (
    "all-packages.txt", "test-list.jsonl", "plan.json", "expected-packages.txt",
    "events.jsonl", "exits.json", "scenario-coverage.json", "cover.out",
)


def pattern_text():
    """Anchored alternation selecting exactly the heavy tests.

    Both lanes derive their -run and -skip argument from this, so it must not
    match a name that merely extends a heavy one.
    """
    return "^(" + "|".join(HEAVY) + ")$"


def read_json(path):
    return json.loads(path.read_text())


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def events(path, expected):
    states = {}
    with path.open() as stream:
        for number, line in enumerate(stream, 1):
            if not line.endswith("\n"):
                raise ValueError(f"{path}:{number}: incomplete event")
            event = json.loads(line)
            if not isinstance(event, dict):
                raise ValueError(f"{path}:{number}: event must be an object")
            package, action = event.get("Package"), event.get("Action")
            if package not in expected:
                raise ValueError(f"{path}:{number}: unexpected package {package}")
            if action == "start":
                if package in states:
                    raise ValueError(f"{package}: duplicate package start")
                states[package] = "active"
            elif states.get(package) != "active":
                raise ValueError(f"{package}: event outside package lifetime")
            if not event.get("Test") and action in ("pass", "skip", "fail"):
                if action == "fail":
                    raise ValueError(f"{package}: failed package")
                states[package] = "complete"
            yield event
    if set(states) != set(expected) or any(s != "complete" for s in states.values()):
        raise ValueError(f"{path}: missing or incomplete package")


def make_plan(root, lane, identity):
    if lane not in ("main", "heavy"):
        raise ValueError("unknown Swift test selection")
    packages = (root / "all-packages.txt").read_text().splitlines()
    if not packages or len(packages) != len(set(packages)) or SYSTEM not in packages:
        raise ValueError("missing or duplicate internal packages")
    tests = set()
    for event in events(root / "test-list.jsonl", packages):
        if event.get("Test"):
            raise ValueError("test listing executed a test")
        if event["Action"] != "output":
            continue
        for name in event.get("Output", "").splitlines():
            if re.fullmatch(r"(?:Test|Fuzz|Example)\w*", name):
                key = (event["Package"], name)
                if key in tests:
                    raise ValueError(f"duplicate listed test {key}")
                tests.add(key)
    heavy = {(SYSTEM, name) for name in HEAVY}
    if not heavy <= tests:
        raise ValueError("a named heavy test is missing from the full inventory")
    selected = heavy if lane == "heavy" else tests - heavy
    return {"lane": lane, "identity": identity, "all_packages": sorted(packages),
            "all_tests": sorted(map(list, tests)), "selected": sorted(map(list, selected)),
            "expected_packages": [SYSTEM] if lane == "heavy" else sorted(packages)}


def prepare(root, lane, identity):
    plan = make_plan(root, lane, identity)
    write_json(root / "plan.json", plan)
    (root / "expected-packages.txt").write_text("\n".join(plan["expected_packages"]) + "\n")


def validate(root):
    plan = read_json(root / "plan.json")
    if plan != make_plan(root, plan["lane"], plan["identity"]):
        raise ValueError("saved selection differs from the full inventory")
    if (root / "expected-packages.txt").read_text().splitlines() != plan["expected_packages"]:
        raise ValueError("scenario package selection differs from the plan")
    if read_json(root / "exits.json") != {"test": 0, "tee": 0}:
        raise ValueError("both the test producer and event writer must succeed")
    selected = set(map(tuple, plan["selected"]))
    started, outcomes = set(), {}
    for event in events(root / "events.jsonl", plan["expected_packages"]):
        name, action = event.get("Test"), event["Action"]
        if not name:
            continue
        package = event["Package"]
        if (package, name.split("/", 1)[0]) not in selected:
            raise ValueError(f"{package}/{name}: test outside selected lane")
        key = (package, name)
        if action == "run":
            if key in started:
                raise ValueError(f"{key}: duplicate test start")
            started.add(key)
        elif action in ("pass", "skip", "fail"):
            if key not in started or key in outcomes:
                raise ValueError(f"{key}: missing start or duplicate terminal event")
            if action == "fail" or (plan["lane"] == "heavy" and action != "pass"):
                raise ValueError(f"{key}: unsuccessful selected test")
            outcomes[key] = action
    if started != outcomes.keys() or not selected <= outcomes.keys():
        raise ValueError("selected or started test is missing its outcome")
    report = read_json(root / "scenario-coverage.json")
    if report.get("schemaVersion") != 3 or any(report.get(field) is not True for field in (
            "valid", "processSucceeded", "streamComplete", "contractValid")):
        raise ValueError("scenario reader did not validate this producer")
    if plan["lane"] == "heavy" and report.get("totalCovered") != 0:
        raise ValueError("heavy scenario credits need deliberate combined-report handling")
    return plan, report, outcomes


def hashes(root):
    return {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in ARTIFACTS}


def check(root, test_exit, tee_exit):
    (root / "receipt.json").unlink(missing_ok=True)
    write_json(root / "exits.json", {"test": test_exit, "tee": tee_exit})
    validate(root)
    write_json(root / "receipt.json", {"sha256": hashes(root)})


def combine_profiles(main_text: str, heavy_text: str) -> str:
    """Validate atomic lane profiles and preserve both bodies for Go's reader."""
    header = 'mode: atomic\n'
    maximum = (1 << 63) - 1
    record = re.compile(
        r'(github\.com/csmart/aeronautes/internal/'
        r'(?:[A-Za-z0-9_-]+/)*[A-Za-z0-9_-]+\.go):'
        r'([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+) ([0-9]+) ([0-9]+)'
    )
    structures = []
    counts = {}
    for label, text in (('main', main_text), ('heavy', heavy_text)):
        if not isinstance(text, str) or not text.startswith(header) or not text.endswith('\n'):
            raise ValueError(f'{label}: complete atomic profile required')
        lines = text[len(header):-1].split('\n')
        files = {}
        for number, line in enumerate(lines, 2):
            match = record.fullmatch(line)
            if match is None:
                raise ValueError(f'{label}:{number}: invalid internal Go coverage record')
            name = match[1]
            start_line, start_col, end_line, end_col, statements, count = (
                int(value) for value in match.groups()[1:]
            )
            position = (start_line, start_col, end_line, end_col)
            if any(value > maximum for value in (*position, statements, count)):
                raise ValueError(f'{label}:{number}: value exceeds Go int64')
            if min(position) < 1 or (start_line, start_col) > (end_line, end_col):
                raise ValueError(f'{label}:{number}: invalid source positions')
            blocks = files.setdefault(name, {})
            if position in blocks and blocks[position] != statements:
                raise ValueError(f'{label}:{number}: conflicting statement count')
            blocks[position] = statements
            key = (name, position)
            counts[key] = counts.get(key, 0) + count
            if counts[key] > maximum:
                raise ValueError(f'{label}:{number}: combined counter exceeds Go int64')
        structures.append(files)

    main_files, heavy_files = structures
    if not heavy_files.keys() <= main_files.keys():
        raise ValueError('heavy coverage contains a file absent from main')
    for name, blocks in heavy_files.items():
        if blocks != main_files[name]:
            raise ValueError(f'{name}: lane block structures differ')
    # Go merges matching blocks; retaining both bodies also retains all zero units.
    return main_text + heavy_text[len(header):]


def join(main, heavy, output, identity):
    plans, reports, counts = [], [], []
    for lane, root in (("main", main), ("heavy", heavy)):
        if read_json(root / "receipt.json") != {"sha256": hashes(root)}:
            raise ValueError(f"{lane}: missing or changed producer artifacts")
        plan, report, outcomes = validate(root)
        if plan["lane"] != lane or plan["identity"] != identity:
            raise ValueError(f"{lane}: incompatible source, toolchain or workflow run")
        plans.append(plan)
        reports.append(report)
        counts.append({"lane": lane, "pass": list(outcomes.values()).count("pass"),
                       "skip": list(outcomes.values()).count("skip")})
    if any(plans[0][key] != plans[1][key] for key in ("all_tests", "all_packages")):
        raise ValueError("producer inventories differ")
    if not reports[0].get("contractHash") or reports[0]["contractHash"] != reports[1].get("contractHash"):
        raise ValueError("producer scenario contracts differ")
    combined = combine_profiles((main / "cover.out").read_text(), (heavy / "cover.out").read_text())
    output.mkdir(parents=True)
    (output / "cover.out").write_text(combined)
    (output / "scenario-coverage.json").write_bytes((main / "scenario-coverage.json").read_bytes())
    (output / "scenario-coverage-heavy.json").write_bytes((heavy / "scenario-coverage.json").read_bytes())
    write_json(output / "partition.json", {"identity": identity, "outcomes": counts})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("pattern")
    planning = commands.add_parser("prepare")
    planning.add_argument("lane", choices=("main", "heavy"))
    planning.add_argument("root", type=Path)
    checking = commands.add_parser("check")
    checking.add_argument("root", type=Path)
    checking.add_argument("test_exit", type=int)
    checking.add_argument("tee_exit", type=int)
    joining = commands.add_parser("join")
    for name in ("main", "heavy", "output"):
        joining.add_argument(name, type=Path)
    for command in (planning, joining):
        for name in ("commit", "go-version", "run-id", "run-attempt"):
            command.add_argument("--" + name, required=True)
    args = parser.parse_args()
    if args.command == "pattern":
        print(pattern_text())
    elif args.command == "check":
        check(args.root, args.test_exit, args.tee_exit)
    else:
        identity = {name: getattr(args, name) for name in ("commit", "go_version", "run_id", "run_attempt")}
        actual = subprocess.run(["git", "rev-parse", "HEAD"], check=True, text=True,
                                capture_output=True, timeout=10).stdout.strip()
        subprocess.run(["git", "diff", "--quiet", "HEAD"], check=True, timeout=10)
        version = subprocess.run(["go", "env", "GOVERSION"], check=True, text=True,
                                 capture_output=True, timeout=30).stdout.strip()
        if identity["commit"] != actual or identity["go_version"] != version:
            raise ValueError("identity differs from the current checkout or Go toolchain")
        if args.command == "prepare":
            prepare(args.root, args.lane, identity)
        else:
            for root in (args.main, args.heavy):
                for line in (root / "cover.out").read_text().splitlines()[1:]:
                    name = line.split(":", 1)[0]
                    source = Path(name.removeprefix("github.com/csmart/aeronautes/"))
                    if not source.is_file():
                        raise ValueError(f"coverage source is absent from checkout: {name}")
            join(args.main, args.heavy, args.output, identity)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        sys.exit(f"Swift test partition refused: {error}")
