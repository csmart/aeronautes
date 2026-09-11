#!/usr/bin/env python3
"""Check the workflow's teardown ordering and refusal using owned files."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


WORKTREE = Path(__file__).resolve().parents[1]
FAKE_COMMAND = '''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

name, args = Path(sys.argv[0]).name, sys.argv[1:]
with Path(os.environ['CONTROL_CALLS']).open('a') as output:
    output.write(json.dumps([name] + args) + '\\n')
failure = os.environ.get('CONTROL_FAILURE', '')
if name == 'docker' and args[0] == 'ps':
    if failure == 'ps':
        raise SystemExit(55)
    pattern = args[args.index('--filter') + 1]
    container = pattern.removeprefix('name=^/').removesuffix('$')
    if container in os.environ['CONTROL_CONTAINERS'].split():
        print(container)
elif name == 'docker' and args[:2] == ['rm', '-f']:
    if failure == 'rm:' + args[2]:
        raise SystemExit(55)
elif name == 'mountpoint':
    if failure == 'mountpoint':
        raise SystemExit(55)
    if not Path(args[-1]).is_dir():
        raise SystemExit(1)
    raise SystemExit(0 if os.environ['CONTROL_MOUNTED'] == '1' else 32)
elif name == 'sudo' and args[0] == 'umount':
    if failure == 'umount':
        raise SystemExit(55)
else:
    raise SystemExit('unexpected control command')
'''


class CleanupTests(unittest.TestCase):
    def test_owned_cleanup_and_failure_paths(self) -> None:
        workflow = (WORKTREE / '.github/workflows/ci.yml').read_text().splitlines()
        start = workflow.index('      - name: Clean up owned Swift fixture')
        start = workflow.index('        run: |', start) + 1
        lines = []
        for line in workflow[start:]:
            if line and not line.startswith('          '):
                break
            lines.append(line[10:])
        program = 'set -euo pipefail\n' + '\n'.join(lines) + '\n'
        cases = [
            ('success', '', True, 'directory', 'saio saio-storage-check saio-format', 0),
            ('partial containers', '', False, 'directory', 'saio-format', 0),
            ('missing data directory', '', False, 'absent', '', 0),
            ('unexpected symlink', '', False, 'symlink', '', 1),
            ('unexpected file', '', False, 'file', '', 1),
            ('container observation failure', 'ps', True, 'directory', 'saio', 55),
            ('container removal failure', 'rm:saio', True, 'directory', 'saio', 55),
            ('unmount failure', 'umount', True, 'directory', 'saio', 55),
            ('mount observation failure', 'mountpoint', True, 'directory', '', 1),
        ]
        for name, failure, mounted, entry, containers, expected_exit in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                storage = root / 'storage'
                storage.mkdir()
                data = storage / 'data'
                if entry == 'directory':
                    data.mkdir()
                elif entry == 'file':
                    data.write_bytes(b'unexpected owned file')
                elif entry == 'symlink':
                    data.symlink_to(root / 'absent-target')
                backing = storage / 'data.xfs'
                backing.write_bytes(b'owned backing control')
                calls = root / 'calls.jsonl'
                fake = root / 'fake-command.py'
                fake.write_text(FAKE_COMMAND)
                fake.chmod(0o755)
                # No test command may remove a real container or unmount a filesystem.
                for command in ('docker', 'mountpoint', 'sudo'):
                    (root / command).symlink_to(fake)
                environment = dict(
                    os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                    SAIO_STORAGE_DIR=str(storage), CONTROL_FAILURE=failure,
                    CONTROL_CONTAINERS=containers, CONTROL_CALLS=str(calls),
                    CONTROL_MOUNTED='1' if mounted else '0',
                )
                process = subprocess.run(
                    ['bash', '-c', program], cwd=WORKTREE, env=environment,
                    capture_output=True, text=True, timeout=15,
                )
                self.assertEqual(process.stderr, '')
                self.assertEqual(process.returncode, expected_exit)
                operations = [json.loads(line) for line in calls.read_text().splitlines()]
                if expected_exit == 0:
                    self.assertFalse(storage.exists())
                else:
                    self.assertEqual(backing.read_bytes(), b'owned backing control')
                unmounts = [i for i, op in enumerate(operations) if op[:2] == ['sudo', 'umount']]
                if failure in ('ps', 'rm:saio') or not mounted:
                    self.assertEqual(unmounts, [])
                if name == 'success':
                    removals = [i for i, op in enumerate(operations) if op[:3] == ['docker', 'rm', '-f']]
                    self.assertEqual(len(removals), 3)
                    self.assertEqual(len(unmounts), 1)
                    self.assertLess(max(removals), unmounts[0])


if __name__ == '__main__':
    unittest.main()
