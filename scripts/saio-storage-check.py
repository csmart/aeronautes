#!/usr/bin/env python3
"""Refuse a Swift fixture whose data mount cannot retain full metadata."""

import json
import os
from pathlib import Path
import stat
import tempfile


def check_mount(root: Path, mountinfo: str) -> None:
    matches = []
    for line in mountinfo.splitlines():
        before, separator, after = line.partition(" - ")
        fields, filesystem = before.split(), after.split()
        if not separator or len(fields) < 6 or len(filesystem) < 3:
            raise ValueError("malformed mount information")
        if fields[4] == str(root):
            matches.append((fields, filesystem))
    if len(matches) != 1:
        raise ValueError("expected one dedicated Swift data mount")
    fields, filesystem = matches[0]
    if fields[3] != "/" or filesystem[0] != "xfs":
        raise ValueError("expected the root of an XFS filesystem")
    if "rw" not in fields[5].split(",") or "rw" not in filesystem[2].split(","):
        raise ValueError("Swift data mount must be writable")


def check_owner(root: Path) -> None:
    # The pinned image initializes its six device directories as this user.
    if (os.geteuid(), os.getegid()) != (1000, 1000):
        raise ValueError("storage check must run as the pinned Swift service user")
    info = root.stat()
    if not stat.S_ISDIR(info.st_mode) or (info.st_uid, info.st_gid) != (1000, 1000):
        raise ValueError("Swift service user must own the data directory")


def check_metadata(root: Path) -> None:
    # Swift splits serialized metadata into 65536-byte xattrs and adds a checksum.
    attributes = {
        "user.swift.metadata": b"x" * 65536,
        "user.swift.metadata_checksum": b"0" * 32,
    }
    with tempfile.NamedTemporaryFile(prefix=".aeronautes-storage-", dir=root) as probe:
        for name, value in attributes.items():
            os.setxattr(probe.fileno(), name, value)
        for name, value in attributes.items():
            if os.getxattr(probe.fileno(), name) != value:
                raise ValueError("Swift metadata attribute did not round-trip: " + name)


def check_storage(root: Path = Path("/srv/node")) -> dict:
    check_mount(root, Path("/proc/self/mountinfo").read_text())
    check_owner(root)
    check_metadata(root)
    return {
        "filesystem": "xfs",
        "path": str(root),
        "uid": os.geteuid(),
        "gid": os.getegid(),
        "metadata_bytes": 65536,
        "checksum_bytes": 32,
        "status": "passed",
    }


if __name__ == "__main__":
    print(json.dumps(check_storage(), sort_keys=True))
