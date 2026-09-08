#!/usr/bin/env python3
"""Exercise the provenance gate against the real pin and corrupted copies."""

import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "swift_source", ROOT / "scripts/check-swift-source.py"
)
SOURCE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SOURCE)


class SourceGateTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="aeronautes-swift-test-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        shutil.copyfile(ROOT / "go.mod", self.root / "go.mod")
        self.dependency = self.root / "third_party/ncw-swift"
        shutil.copytree(ROOT / "third_party/ncw-swift", self.dependency)

    def test_recorded_source_and_patches_pass(self):
        self.assertEqual(SOURCE.check(self.root), 38)

    def test_source_drift_is_refused(self):
        for mutation in ("changed", "missing", "extra"):
            with self.subTest(mutation=mutation):
                path = self.dependency / "source/swift.go"
                original = path.read_bytes()
                extra = self.dependency / "source/unreviewed.go"
                if mutation == "changed":
                    path.write_bytes(original + b"\n// changed\n")
                elif mutation == "missing":
                    path.unlink()
                else:
                    extra.write_text("package swift\n")
                with self.assertRaisesRegex(ValueError, "source differs"):
                    SOURCE.check(self.root)
                path.write_bytes(original)
                extra.unlink(missing_ok=True)

    def test_required_version_drift_is_refused(self):
        SOURCE.run(["go", "mod", "edit", "-require=" + SOURCE.MODULE + "@v2.0.4"], self.root)
        with self.assertRaisesRegex(ValueError, "required version"):
            SOURCE.check(self.root)

    def test_wrong_replacement_is_refused(self):
        SOURCE.run(["go", "mod", "edit", "-replace=" + SOURCE.MODULE + "=./wrong"], self.root)
        with self.assertRaisesRegex(ValueError, "replacement must be exactly"):
            SOURCE.check(self.root)

    def test_additional_versioned_replacement_is_refused(self):
        SOURCE.run(["go", "mod", "edit", "-replace=" + SOURCE.MODULE
                    + "@v2.0.4=" + SOURCE.REPLACEMENT], self.root)
        with self.assertRaisesRegex(ValueError, "replacement must be exactly"):
            SOURCE.check(self.root)

    def test_excluded_swift_version_is_refused(self):
        SOURCE.run(["go", "mod", "edit", "-exclude=" + SOURCE.MODULE + "@v2.0.4"], self.root)
        with self.assertRaisesRegex(ValueError, "exclude entries"):
            SOURCE.check(self.root)

    def test_recorded_checksums_are_enforced(self):
        path = self.dependency / "UPSTREAM.json"
        original = path.read_text()
        for field in ("sum", "goModSum"):
            with self.subTest(field=field):
                record = json.loads(original)
                record[field] = "h1:" + "A" * 43 + "="
                path.write_text(json.dumps(record))
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    SOURCE.check(self.root)
        path.write_text(original)

    def test_reported_revision_must_match_the_record(self):
        upstream = json.loads((self.dependency / "UPSTREAM.json").read_text())
        download = {"Path": SOURCE.MODULE, "Version": upstream["version"],
                    "Origin": {"Hash": "0" * 40}}
        with self.assertRaisesRegex(ValueError, "upstream revision"):
            SOURCE.verified_files(upstream, download)

    def test_source_symlink_is_refused(self):
        source = self.dependency / "source"
        moved = self.dependency / "other"
        source.rename(moved)
        source.symlink_to(moved, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "source root must be a directory"):
            SOURCE.check(self.root)

    def test_missing_or_unapplicable_patch_is_refused(self):
        for name in SOURCE.PATCHES:
            with self.subTest(patch=name):
                patch = self.dependency / name
                original = patch.read_bytes()
                patch.unlink()
                with self.assertRaises(SOURCE.subprocess.CalledProcessError) as caught:
                    SOURCE.check(self.root)
                self.assertEqual(caught.exception.cmd[:2], ["git", "apply"])
                patch.write_text("--- a/missing.go\n+++ b/missing.go\n@@ -1 +1 @@\n-old\n+new\n")
                with self.assertRaises(SOURCE.subprocess.CalledProcessError) as caught:
                    SOURCE.check(self.root)
                self.assertEqual(caught.exception.cmd[:2], ["git", "apply"])
                patch.write_bytes(original)

    def test_ordered_patch_sequence_is_required(self):
        path = self.dependency / "UPSTREAM.json"
        original = json.loads(path.read_text())
        for patches in (None, "listing-response.patch", [],
                        ["listing-response.patch", "credential-pairing.patch"],
                        ["credential-pairing.patch"],
                        ["credential-pairing.patch", "listing-response.patch"],
                        ["credential-pairing.patch", "listing-fidelity.patch", "listing-response.patch"],
                        ["credential-pairing.patch", "listing-response.patch", "listing-fidelity.patch", "listing-fidelity.patch"],
                        ["credential-pairing.patch", "credential-pairing.patch"],
                        ["credential-pairing.patch", "unreviewed.patch"]):
            with self.subTest(patches=patches):
                record = dict(original, patches=patches)
                path.write_text(json.dumps(record))
                with self.assertRaisesRegex(ValueError, "ordered patch sequence"):
                    SOURCE.check(self.root)
        path.write_text(json.dumps(dict(original, patch="credential-pairing.patch")))
        with self.assertRaisesRegex(ValueError, "ordered patch sequence"):
            SOURCE.check(self.root)

    def test_altered_patch_is_refused(self):
        anchors = {
            "credential-pairing.patch": b"+// The callback selects a URL",
            "listing-response.patch": b"+\t\t\tobject.LastModified = object.LastModified.Truncate(time.Second)",
            "listing-fidelity.patch": b"+// Aeronautes",
        }
        for name, anchor in anchors.items():
            with self.subTest(patch=name):
                patch = self.dependency / name
                original = patch.read_bytes()
                changed = original.replace(anchor, anchor + b" // changed", 1)
                self.assertNotEqual(changed, original)
                patch.write_bytes(changed)
                try:
                    with self.assertRaisesRegex(ValueError, "source differs"):
                        SOURCE.check(self.root)
                finally:
                    patch.write_bytes(original)

    def test_unrecorded_patch_is_refused(self):
        (self.dependency / "unreviewed.patch").write_text("unrecorded")
        with self.assertRaisesRegex(ValueError, "unrecorded Swift patch"):
            SOURCE.check(self.root)

    def test_patch_symlink_is_refused(self):
        for name in SOURCE.PATCHES:
            with self.subTest(patch=name):
                patch = self.dependency / name
                moved = self.dependency / "moved.patch.txt"
                patch.rename(moved)
                patch.symlink_to(moved)
                try:
                    with self.assertRaisesRegex(ValueError, "patch must not be a symlink"):
                        SOURCE.check(self.root)
                finally:
                    patch.unlink()
                    moved.rename(patch)

    def test_archive_contents_are_checked_independently(self):
        upstream = json.loads((self.dependency / "UPSTREAM.json").read_text())
        # The normal gate populated the cache. Keep its metadata unchanged
        # while substituting a corrupt archive; .ziphash/Sum cannot prove it.
        download = json.loads(SOURCE.run(
            ["go", "mod", "download", "-json", SOURCE.MODULE + "@" + upstream["version"]],
            self.root,
        ))
        corrupted = self.root / "corrupt.zip"
        with zipfile.ZipFile(download["Zip"]) as original:
            with zipfile.ZipFile(corrupted, "w") as copy:
                for entry in original.infolist():
                    data = original.read(entry)
                    if entry.filename.endswith("/swift.go"):
                        data += b"\n// unreviewed\n"
                    copy.writestr(entry, data)
        download["Zip"] = str(corrupted)
        with self.assertRaisesRegex(ValueError, "source checksum mismatch"):
            SOURCE.verified_files(upstream, download)


if __name__ == "__main__":
    unittest.main()
