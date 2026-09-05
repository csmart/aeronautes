#!/usr/bin/env python3
"""Prepare archived audit probes outside the normal source/test tree."""

import argparse
import json
from pathlib import Path
import re


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[4])
    parser.add_argument("--rclone", type=Path, required=True)
    args = parser.parse_args()
    repo = args.repo.resolve()
    out = args.out.resolve()
    rclone = args.rclone.resolve()
    archive = Path(__file__).resolve().parent
    if not rclone.is_file():
        parser.error("--rclone must name the verified pinned binary")
    if not (repo / "frontend/node_modules").is_dir():
        parser.error("install the locked frontend dependencies first")
    if out.exists() and any(out.iterdir()):
        parser.error("--out must be an empty directory")
    out.mkdir(parents=True, exist_ok=True)
    replacements = {}
    manifest = json.loads((archive / "manifest.json").read_text())
    for target, source in manifest.items():
        destination = out / source.removesuffix(".txt")
        body = (archive / source).read_text()
        # JSON string syntax also escapes a path safely in the Go string literal.
        body = body.replace('"__AUDIT_RCLONE__"', json.dumps(str(rclone)))
        destination.write_text(body)
        replacements[str(repo / target)] = str(destination)
    (out / "overlay.json").write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
    ui = (archive / "ui_audit.test.ts.txt").read_text()
    # The archived imports use quoted paths; encode complete import strings.
    ui = re.sub(r"'__AUDIT_REPO__([^']*)'", lambda m: json.dumps(str(repo) + m[1]), ui)
    (out / "audit.test.ts").write_text(ui)
    (out / "node_modules").symlink_to(repo / "frontend/node_modules", target_is_directory=True)
    (out / "vitest.config.mts").write_text(
        "import base from " + json.dumps(str(repo / "frontend/vite.config.ts")) + ";\n"
        "export default { ...base, root: " + json.dumps(str(repo / "frontend")) + ",\n"
        "  test: { ...base.test, include: [" + json.dumps(str(out / "audit.test.ts")) + "] } };\n"
    )
    print(f"Prepared {len(replacements)} Go source overlays and the UI probe suite in {out}")


if __name__ == "__main__":
    main()
