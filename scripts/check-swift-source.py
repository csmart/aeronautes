#!/usr/bin/env python3
"""Verify the reviewed ncw/swift source and patch (ADR 0016)."""

import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys
import tempfile
import zipfile


MODULE = "github.com/ncw/swift/v2"
REPLACEMENT = "./third_party/ncw-swift/source"


def run(args, cwd):
    # A workspace or inherited -modfile must not hide the checked-in pin.
    env = dict(os.environ, GOWORK="off", GOFLAGS="", GO111MODULE="on")
    return subprocess.run(
        args, cwd=cwd, env=env, check=True, capture_output=True,
        text=True, timeout=120,
    ).stdout


def module_hash(files):
    # Go's dirhash.Hash1 covers names and contents, independent of zip metadata.
    digest = hashlib.sha256()
    for name, data in sorted(files.items()):
        if "\n" in name:
            raise ValueError("newline in archive filename")
        digest.update(f"{hashlib.sha256(data).hexdigest()}  {name}\n".encode())
    return "h1:" + base64.b64encode(digest.digest()).decode()


def check_module(root, upstream):
    if upstream["module"] != MODULE:
        raise ValueError("unexpected upstream module")
    config = json.loads(run(["go", "mod", "edit", "-json"], root))
    required = [r for r in config.get("Require") or [] if r["Path"] == MODULE]
    if len(required) != 1 or required[0]["Version"] != upstream["version"]:
        raise ValueError("Swift required version differs from UPSTREAM.json")
    replacements = [
        r for r in config.get("Replace") or [] if r["Old"]["Path"] == MODULE
    ]
    expected = {"Old": {"Path": MODULE}, "New": {"Path": REPLACEMENT}}
    if replacements != [expected]:
        raise ValueError("Swift replacement must be exactly " + REPLACEMENT)
    if any(r["Path"] == MODULE for r in config.get("Exclude") or []):
        raise ValueError("Swift exclude entries are not allowed")


def verified_files(upstream, download):
    if download.get("Error"):
        raise ValueError("upstream download failed: " + download["Error"])
    if (download["Path"], download["Version"]) != (MODULE, upstream["version"]):
        raise ValueError("download returned a different module or version")
    revision = download.get("Origin", {}).get("Hash")
    if revision and revision != upstream["revision"]:
        raise ValueError("upstream revision differs from UPSTREAM.json")
    prefix = MODULE + "@" + upstream["version"] + "/"
    with zipfile.ZipFile(download["Zip"]) as archive:
        files = {}
        for entry in archive.infolist():
            name = entry.filename
            relative = name.removeprefix(prefix)
            if (not name.startswith(prefix) or entry.is_dir()
                    or relative != str(PurePosixPath(relative))
                    or PurePosixPath(relative).is_absolute()
                    or ".." in PurePosixPath(relative).parts
                    or "\\" in relative or name in files):
                raise ValueError("unexpected archive entry: " + name)
            files[name] = archive.read(entry)
    if module_hash(files) != upstream["sum"]:
        raise ValueError("upstream source checksum mismatch")
    module_file = files[prefix + "go.mod"]
    if module_hash({"go.mod": module_file}) != upstream["goModSum"]:
        raise ValueError("upstream go.mod checksum mismatch")
    if Path(download["GoMod"]).read_bytes() != module_file:
        raise ValueError("downloaded go.mod differs from the verified archive")
    return {name[len(prefix):]: data for name, data in files.items()}


def source_files(root):
    if root.is_symlink() or not root.is_dir():
        raise ValueError("source root must be a directory, not a symlink")
    files = {}
    for path in root.rglob("*"):
        if path.is_symlink():
            raise ValueError("symlink in source tree: " + str(path))
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError("non-file in source tree: " + str(path))
        files[path.relative_to(root).as_posix()] = path.read_bytes()
    return files


def check(root):
    dependency = root / "third_party/ncw-swift"
    upstream = json.loads((dependency / "UPSTREAM.json").read_text())
    check_module(root, upstream)
    if upstream["patch"] != "credential-pairing.patch":
        raise ValueError("unexpected patch filename")
    with tempfile.TemporaryDirectory(prefix="aeronautes-swift-source-") as temp:
        temp = Path(temp)
        # Download outside the application: directory replacements bypass the
        # source checks in `go mod verify` and may lose their go.sum entries.
        (temp / "go.mod").write_text("module swift-source-check\n\ngo 1.16\n")
        download = json.loads(run(
            ["go", "mod", "download", "-json", MODULE + "@" + upstream["version"]],
            temp,
        ))
        files = verified_files(upstream, download)
        expected = temp / "source"
        expected.mkdir()
        for name, data in files.items():
            path = expected / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        run(["git", "apply", "--no-index", "--whitespace=error-all",
             str(dependency / upstream["patch"])], expected)
        actual_files = source_files(dependency / "source")
        expected_files = source_files(expected)
        differences = sorted(
            name for name in actual_files.keys() | expected_files.keys()
            if actual_files.get(name) != expected_files.get(name)
        )
        if differences:
            raise ValueError("Swift source differs from upstream plus patch: "
                             + ", ".join(differences))
    return len(expected_files)


def main():
    try:
        count = check(Path(__file__).resolve().parents[1])
    except (OSError, ValueError, KeyError, zipfile.BadZipFile,
            subprocess.SubprocessError) as error:
        print("Swift source verification failed: " + str(error), file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError):
            print(error.stdout, file=sys.stderr)
            print(error.stderr, file=sys.stderr)
        return 1
    print(f"Swift source verified: {count} files match upstream plus the recorded patch")
    return 0


if __name__ == "__main__":
    sys.exit(main())
