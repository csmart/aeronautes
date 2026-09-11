#!/usr/bin/env python3
"""Correct the pinned fixture's error selector before its original startup."""

import os
from pathlib import Path


def configure_error_log(path):
    original = b"+\\..err:\n"
    corrected = b"+\\.err:\n"
    lines = path.read_bytes().splitlines(keepends=True)
    counts = (lines.count(original), lines.count(corrected))
    if counts == (0, 1):
        return
    if counts != (1, 0):
        raise ValueError("expected one original or corrected Swift error selector")
    # The pinned logger's extra dot drops ordinary .err messages.
    lines[lines.index(original)] = corrected
    path.write_bytes(b"".join(lines))


def start():
    configure_error_log(Path("/etc/socklog.rules/~-errors"))
    os.execv("/init", ["/init"])


if __name__ == "__main__":
    start()
