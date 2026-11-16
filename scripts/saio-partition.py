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


def _scenario_fields(value, required, optional=()):
    if (not isinstance(value, dict) or not set(required) <= value.keys()
            or not value.keys() <= set(required) | set(optional)):
        raise ValueError("missing or unexpected scenario fields")


def _scenario_identity(identity):
    _scenario_fields(identity, ("commit", "go_version", "run_id", "run_attempt"))
    patterns = {"commit": r"[0-9a-f]{40}",
                "go_version": r"go[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:(?:beta|rc)[0-9]+)?",
                "run_id": r"[1-9][0-9]*", "run_attempt": r"[1-9][0-9]*"}
    for field, pattern in patterns.items():
        if not isinstance(identity[field], str) or re.fullmatch(pattern, identity[field]) is None:
            raise ValueError(f"invalid {field} identity")


def _scenario_names(value, nullable=False):
    if value is None and nullable:
        return []
    known = {"happy", "refusal", "conflict", "cancel", "partial", "retry"}
    if (not isinstance(value, list) or any(not isinstance(item, str) or item not in known for item in value)
            or len(set(value)) != len(value)):
        raise ValueError("invalid or duplicate scenario names")
    return value


def _scenario_inventory(rows, coverage=False):
    if not isinstance(rows, list) or not rows:
        raise ValueError("nonempty scenario inventory required")
    inventory = {}
    for row in rows:
        fields = ("action", "kind", "required", "covered", "gaps") if coverage else ("action", "kind", "required")
        _scenario_fields(row, fields)
        action, kind = row["action"], row["kind"]
        if (not isinstance(action, str)
                or re.fullmatch(r"[A-Za-z][A-Za-z0-9_]*\.[A-Za-z][A-Za-z0-9_]*", action) is None):
            raise ValueError("invalid action identity")
        if action in inventory:
            raise ValueError("duplicate action identity")
        if not isinstance(kind, str) or kind not in {"read", "session", "mutate", "destructive", "job"}:
            raise ValueError("unknown scenario kind")
        required = _scenario_names(row["required"])
        if not required:
            raise ValueError("empty required scenario set")
        inventory[action] = {"kind": kind, "required": required}
    text = "".join(f'{action}|{row["kind"]}|{",".join(row["required"])}\n'
                   for action, row in sorted(inventory.items()))
    return inventory, hashlib.sha256(text.encode()).hexdigest()[:16]


def _scenario_report(report, identity):
    _scenario_fields(report, (
        "schemaVersion", "valid", "processSucceeded", "streamComplete", "contractValid",
        "coverageComplete", "commitSha", "goVersion", "saioConfigured", "contractHash",
        "totalActions", "totalRequired", "totalCovered", "actionsWithGaps", "nonJSONLines", "actions",
    ), ("protocolError", "missingPackages", "failedPackages", "unexpectedPackages",
        "violations", "violationsTruncated"))
    if type(report["schemaVersion"]) is not int or report["schemaVersion"] != 3:
        raise ValueError("unsupported scenario report schema")
    for field in ("valid", "processSucceeded", "streamComplete", "contractValid"):
        if report[field] is not True:
            raise ValueError(f"scenario report {field} is not true")
    if report["commitSha"] != identity["commit"] or report["goVersion"] != identity["go_version"]:
        raise ValueError("scenario report source or toolchain differs from producer identity")
    if type(report["saioConfigured"]) is not bool:
        raise ValueError("invalid saioConfigured observation")
    if report.get("protocolError", "") != "":
        raise ValueError("scenario report has a protocolError")
    for field in ("missingPackages", "failedPackages", "unexpectedPackages", "violations"):
        value = report.get(field, [])
        if value is not None and (not isinstance(value, list) or value):
            raise ValueError(f"scenario report has invalid {field}")
    for field in ("nonJSONLines", "violationsTruncated"):
        value = report.get(field, 0)
        if type(value) is not int or value != 0:
            raise ValueError(f"scenario report has invalid {field}")
    inventory, fingerprint = _scenario_inventory(report["actions"], coverage=True)
    if report["contractHash"] != fingerprint:
        raise ValueError("scenario report contract fingerprint differs from its rows")
    covered, actions_with_gaps = set(), 0
    for row in report["actions"]:
        credits = row["covered"]
        if credits is None:
            credits = []
        if not isinstance(credits, list):
            raise ValueError("invalid covered scenario list")
        have = set()
        for credit in credits:
            _scenario_fields(credit, ("scenario", "tests", "evidence"))
            scenario = credit["scenario"]
            if not isinstance(scenario, str) or scenario not in row["required"] or scenario in have:
                raise ValueError("duplicate or unrequired scenario credit")
            tests, evidence = credit["tests"], credit["evidence"]
            if (not isinstance(tests, list) or not tests or any(not isinstance(test, str) for test in tests)
                    or len(set(tests)) != len(tests)):
                raise ValueError("missing or duplicate scenario witnesses")
            if (not isinstance(evidence, list) or len(evidence) != len(tests)
                    or any(not isinstance(item, str) for item in evidence)):
                raise ValueError("scenario evidence must match its witnesses")
            for test in tests:
                parts = test.split("\t")
                if (len(parts) != 2 or any(not part or any(char.isspace() for char in part) for part in parts)
                        or "/" in parts[1]):
                    raise ValueError("scenario witness must name a package and top-level test")
            have.add(scenario)
            covered.add((row["action"], scenario))
        gaps = _scenario_names(row["gaps"], nullable=True)
        if set(gaps) != set(row["required"]) - have:
            raise ValueError("scenario gaps differ from required minus covered")
        actions_with_gaps += bool(gaps)
    counts = {"totalActions": len(inventory), "totalRequired": sum(len(row["required"]) for row in inventory.values()),
              "totalCovered": len(covered), "actionsWithGaps": actions_with_gaps}
    for field, expected in counts.items():
        if type(report[field]) is not int or report[field] != expected:
            raise ValueError(f"scenario report {field} differs from its rows")
    if type(report["coverageComplete"]) is not bool or report["coverageComplete"] != (actions_with_gaps == 0):
        raise ValueError("scenario coverageComplete differs from its gaps")
    return inventory, covered


def _scenario_baseline(baseline):
    _scenario_fields(baseline, ("schemaVersion", "reportSchemaVersion", "identity", "contractHash",
                                "actions", "protected", "evidence"))
    for field, expected in (("schemaVersion", 1), ("reportSchemaVersion", 3)):
        if type(baseline[field]) is not int or baseline[field] != expected:
            raise ValueError(f"unsupported baseline {field}")
    _scenario_identity(baseline["identity"])
    evidence = baseline["evidence"]
    digests = ("mainReportSHA256", "heavyReportSHA256", "mainReceiptSHA256", "heavyReceiptSHA256")
    _scenario_fields(evidence, ("reference", *digests))
    if not isinstance(evidence["reference"], str) or not evidence["reference"].strip():
        raise ValueError("baseline requires a retained evidence reference")
    for field in digests:
        if not isinstance(evidence[field], str) or re.fullmatch(r"[0-9a-f]{64}", evidence[field]) is None:
            raise ValueError(f"invalid baseline {field}")
    inventory, fingerprint = _scenario_inventory(baseline["actions"])
    if baseline["contractHash"] != fingerprint:
        raise ValueError("baseline contract fingerprint differs from its rows")
    if not isinstance(baseline["protected"], list) or not baseline["protected"]:
        raise ValueError("nonempty protected scenario set required")
    protected = set()
    for cell in baseline["protected"]:
        _scenario_fields(cell, ("action", "scenario"))
        action, scenario = cell["action"], cell["scenario"]
        if (not isinstance(action, str) or not isinstance(scenario, str) or action not in inventory
                or scenario not in inventory[action]["required"]):
            raise ValueError("protected scenario is outside the baseline contract")
        pair = (action, scenario)
        if pair in protected:
            raise ValueError("duplicate protected scenario")
        protected.add(pair)
    return inventory, protected


def _scenario_json(data):
    def unique_fields(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("duplicate scenario JSON key")
            value[key] = item
        return value

    return json.loads(data, object_pairs_hook=unique_fields)


def check_scenario_preservation(main_report, heavy_report, baseline_path, identity, output):
    """Require ADR0069 preservation after the caller validates its producers.

    Producer receipts, exits, selection and run/attempt identity must be checked
    first. Baseline evidence admission remains independent review.
    """
    inputs = (main_report, heavy_report, baseline_path)
    if (output.resolve() in {path.resolve() for path in inputs}
            or any(output.exists() and path.exists() and output.samefile(path) for path in inputs)):
        raise ValueError("scenario preservation output must not replace a raw input")
    result = {"schemaVersion": 1, "identity": None, "baselineSHA256": None,
              "preserved": False, "requiredDrift": None, "missingProtected": None, "errors": []}
    label = "candidate identity"
    try:
        _scenario_identity(identity)
        result["identity"] = dict(identity)
        label = "baseline"
        raw_baseline = baseline_path.read_bytes()
        result["baselineSHA256"] = hashlib.sha256(raw_baseline).hexdigest()
        baseline, protected = _scenario_baseline(_scenario_json(raw_baseline))
        label = "main report"
        current, covered = _scenario_report(_scenario_json(main_report.read_bytes()), identity)
        label = "heavy report"
        heavy, heavy_covered = _scenario_report(_scenario_json(heavy_report.read_bytes()), identity)
        if heavy_covered:
            raise ValueError("heavy scenario credits need deliberate combined-report handling")
        if current != heavy:
            raise ValueError("producer scenario inventories differ")
        result["requiredDrift"] = [
            {"action": action, "baseline": baseline.get(action), "current": current.get(action)}
            for action in sorted(baseline.keys() | current.keys()) if baseline.get(action) != current.get(action)
        ]
        # Drift must not hide a simultaneous loss needed by admission review.
        result["missingProtected"] = [{"action": action, "scenario": scenario}
                                      for action, scenario in sorted(protected - covered)]
        result["preserved"] = not result["requiredDrift"] and not result["missingProtected"]
    except (OSError, ValueError) as error:
        result["errors"].append(f"{label}: {error}")
    output.parent.mkdir(parents=True, exist_ok=True)
    write_json(output, result)
    if not result["preserved"]:
        raise ValueError(f"scenario preservation refused; see {output}")
    return result


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


def join(main, heavy, output, identity, baseline):
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
    check_scenario_preservation(main / "scenario-coverage.json", heavy / "scenario-coverage.json",
                                baseline, identity, output / "scenario-preservation.json")


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
    joining.add_argument("--baseline", type=Path, required=True)
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
            join(args.main, args.heavy, args.output, identity, args.baseline)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        sys.exit(f"Swift test partition refused: {error}")
