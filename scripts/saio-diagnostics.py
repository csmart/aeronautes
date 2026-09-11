#!/usr/bin/env python3
"""Retain bounded diagnostics from the disposable CI Swift fixture."""

import datetime
import json
from pathlib import Path
import re
import subprocess
import sys
import time


PROBE_TIMEOUT = 10
OUTPUT_LIMIT = 4 * 1024 * 1024
# Exec keeps the reader under the container's own deadline after launch.
ROTATED_LOG_TAIL = """
from pathlib import Path
import os
import sys

root = Path(sys.argv[1])
paths = [str(path) for path in sorted(root.glob("@*.[su]"))]
paths.append(str(root / "current"))
os.execvp("tail", ["tail", "-n", "10000", "--", *paths])
"""
PROBES = (
    ("container-state", ["docker", "inspect", "--format",
                         '{"id":{{json .Id}},"image":{{json .Image}},'
                         '"state":{{json .State}},"restartCount":{{json .RestartCount}}}', "saio"]),
    ("container-stats", ["docker", "stats", "--no-stream", "--format", "{{json .}}", "saio"]),
    ("container-log", ["docker", "logs", "--timestamps", "--tail", "10000", "saio"]),
    # Swift's syslog output is kept inside the image, outside docker logs.
    ("swift-log", ["docker", "exec", "saio", "tail", "-n", "10000", "/var/log/swift/all.log"]),
    # Access traffic can rotate away a failure before the suite finishes cleanup.
    ("swift-error-log", ["docker", "exec", "saio", "timeout", "-k", "1", "8",
                         "python3", "-c", ROTATED_LOG_TAIL,
                         "/var/log/socklog/errors"]),
    ("container-memory-events", ["docker", "exec", "saio", "cat", "/sys/fs/cgroup/memory.events"]),
    ("container-disk", ["docker", "exec", "saio", "df", "-k", "/", "/srv/node", "/var/log"]),
    ("container-inodes", ["docker", "exec", "saio", "df", "-i", "/", "/srv/node", "/var/log"]),
    ("runner-disk", ["df", "-k", "."]),
    ("runner-inodes", ["df", "-i", "."]),
    ("runner-memory", ["free", "-m"]),
)


def redact(output):
    text = output.decode("utf-8", errors="replace")
    # The fixture uses tempauth tokens, including in proxy access log fields.
    text = re.sub(r"\bAUTH_tk[A-Za-z0-9_-]+", "[redacted-token]", text)
    text = re.sub(
        r"(?im)^.*\b(?:authorization|x-auth-token|x-storage-token|x-auth-key|"
        r"x-storage-pass|x-service-token|cookie|set-cookie)\b.*$",
        "[redacted authentication line]", text,
    )
    return text


def probe(argv, timeout=PROBE_TIMEOUT):
    record = {"argv": argv, "started": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "exit": None, "timed_out": False}
    started = time.monotonic()
    try:
        result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=timeout, check=False)
        record["exit"] = result.returncode
        output = result.stdout
    except subprocess.TimeoutExpired as error:
        record["timed_out"] = True
        output = error.output or b""
    except OSError as error:
        record["error"] = str(error)
        output = b""
    record["elapsed_seconds"] = round(time.monotonic() - started, 3)
    # Redact before retaining the tail so a clipped token prefix cannot escape.
    cleaned = redact(output).encode("utf-8")
    record["output_truncated"] = len(cleaned) > OUTPUT_LIMIT
    record["output"] = cleaned[-OUTPUT_LIMIT:].decode("utf-8", errors="ignore")
    return record


def collect(directory):
    directory.mkdir(parents=True, exist_ok=True)
    for name, argv in PROBES:
        record = probe(argv)
        (directory / (name + ".json")).write_text(json.dumps(record, indent=2) + "\n")
        print(f"{name}: exit={record['exit']} timed_out={record['timed_out']}", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: saio-diagnostics.py <output-directory>")
    collect(Path(sys.argv[1]))
