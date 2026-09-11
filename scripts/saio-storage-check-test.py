#!/usr/bin/env python3
"""Exercise the fixture storage gate, including refusal and probe cleanup."""

import errno
import importlib.util
from pathlib import Path
import stat
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location(
    "saio_storage_check", Path(__file__).with_name("saio-storage-check.py")
)
STORAGE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(STORAGE)
MOUNT = "50 20 7:0 / /srv/node rw,relatime - xfs /dev/loop0 rw,attr2\n"


class StorageTests(unittest.TestCase):
    def test_writable_xfs_root_mount_is_accepted(self) -> None:
        STORAGE.check_mount(Path("/srv/node"), MOUNT)

    def test_missing_wrong_or_ambiguous_mount_is_refused(self) -> None:
        cases = {
            "absent": MOUNT.replace("/srv/node", "/somewhere/else"),
            "overlay": MOUNT.replace("xfs", "overlay"),
            "ordinary xfs subdirectory": MOUNT.replace("7:0 / ", "7:0 /subdir "),
            "read-only bind": MOUNT.replace("rw,relatime", "ro,relatime"),
            "read-only filesystem": MOUNT.replace("rw,attr2", "ro,attr2"),
            "duplicate": MOUNT + MOUNT,
            "malformed": "unexpected mount information\n",
        }
        for name, mountinfo in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                STORAGE.check_mount(Path("/srv/node"), mountinfo)

    def test_service_user_and_owned_directory_are_required(self) -> None:
        root = Path("/srv/node")
        cases = [
            (1000, 1000, 1000, 1000, stat.S_IFDIR, True),
            (0, 0, 1000, 1000, stat.S_IFDIR, False),
            (1000, 0, 1000, 1000, stat.S_IFDIR, False),
            (1000, 1000, 0, 1000, stat.S_IFDIR, False),
            (1000, 1000, 1000, 0, stat.S_IFDIR, False),
            (1000, 1000, 1000, 1000, stat.S_IFREG, False),
        ]
        for uid, gid, owner, group, mode, accepted in cases:
            info = SimpleNamespace(st_uid=owner, st_gid=group, st_mode=mode)
            with self.subTest(case=(uid, gid, owner, group, mode)), \
                    patch.object(STORAGE.os, "geteuid", return_value=uid), \
                    patch.object(STORAGE.os, "getegid", return_value=gid), \
                    patch.object(Path, "stat", return_value=info):
                if accepted:
                    STORAGE.check_owner(root)
                else:
                    with self.assertRaises(ValueError):
                        STORAGE.check_owner(root)

    def test_both_full_attributes_must_coexist_and_round_trip(self) -> None:
        stored = {}

        def write_attribute(fd, name, value):
            stored[name] = value

        def read_attribute(fd, name):
            self.assertEqual(set(stored), {"user.swift.metadata", "user.swift.metadata_checksum"})
            return stored[name]

        with tempfile.TemporaryDirectory() as directory, \
                patch.object(STORAGE.os, "setxattr", side_effect=write_attribute), \
                patch.object(STORAGE.os, "getxattr", side_effect=read_attribute):
            STORAGE.check_metadata(Path(directory))
            self.assertEqual(list(Path(directory).iterdir()), [])
        self.assertEqual(len(stored["user.swift.metadata"]), 65536)
        self.assertEqual(len(stored["user.swift.metadata_checksum"]), 32)

    def test_failed_write_or_read_removes_probe_and_refuses(self) -> None:
        for operation in ("setxattr", "getxattr"):
            with self.subTest(operation=operation), tempfile.TemporaryDirectory() as directory, \
                    patch.object(STORAGE.os, "setxattr"), patch.object(STORAGE.os, "getxattr"):
                getattr(STORAGE.os, operation).side_effect = OSError(errno.ENOSPC, "full xattr storage")
                with self.assertRaises(OSError):
                    STORAGE.check_metadata(Path(directory))
                self.assertEqual(list(Path(directory).iterdir()), [])

    def test_wrong_metadata_or_checksum_is_refused(self) -> None:
        values = {"user.swift.metadata": b"x" * 65536, "user.swift.metadata_checksum": b"0" * 32}
        for broken in values:
            def read_attribute(fd, name):
                return b"wrong" if name == broken else values[name]

            with self.subTest(attribute=broken), tempfile.TemporaryDirectory() as directory, \
                    patch.object(STORAGE.os, "setxattr"), \
                    patch.object(STORAGE.os, "getxattr", side_effect=read_attribute):
                with self.assertRaisesRegex(ValueError, "did not round-trip"):
                    STORAGE.check_metadata(Path(directory))
                self.assertEqual(list(Path(directory).iterdir()), [])

    def test_each_failed_gate_prevents_success(self) -> None:
        for gate in ("check_mount", "check_owner", "check_metadata"):
            with self.subTest(gate=gate), patch.object(Path, "read_text", return_value=MOUNT), \
                    patch.object(STORAGE, "check_mount"), patch.object(STORAGE, "check_owner"), \
                    patch.object(STORAGE, "check_metadata"):
                getattr(STORAGE, gate).side_effect = ValueError("refused storage")
                with self.assertRaisesRegex(ValueError, "refused storage"):
                    STORAGE.check_storage()
                if gate == "check_mount":
                    STORAGE.check_owner.assert_not_called()
                if gate != "check_metadata":
                    STORAGE.check_metadata.assert_not_called()

    def test_success_is_reported_only_after_all_checks(self) -> None:
        calls = []
        with patch.object(Path, "read_text", return_value=MOUNT), \
                patch.object(STORAGE, "check_mount", side_effect=lambda *args: calls.append("mount")), \
                patch.object(STORAGE, "check_owner", side_effect=lambda *args: calls.append("owner")), \
                patch.object(STORAGE, "check_metadata", side_effect=lambda *args: calls.append("metadata")), \
                patch.object(STORAGE.os, "geteuid", return_value=1000), \
                patch.object(STORAGE.os, "getegid", return_value=1000):
            result = STORAGE.check_storage()
        self.assertEqual(calls, ["mount", "owner", "metadata"])
        self.assertEqual(result["status"], "passed")
        self.assertEqual((result["metadata_bytes"], result["checksum_bytes"]), (65536, 32))


if __name__ == "__main__":
    unittest.main()
