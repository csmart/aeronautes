import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { wailsVersion } from './wails.mjs';

const modulePath = 'github.com/wailsapp/wails/v3';
const runtime = '@wailsio/runtime';

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'aeronautes-wails-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, 'frontend'));
  writeFileSync(join(root, 'go.mod'), `module fixture\n\nrequire ${modulePath} v3.1.0 // exact owner\n`);
  writeFileSync(join(root, 'frontend/package.json'), JSON.stringify({ dependencies: { [runtime]: '3.1.0' } }));
  writeFileSync(join(root, 'frontend/package-lock.json'), JSON.stringify({ lockfileVersion: 3, packages: {
    '': { dependencies: { [runtime]: '3.1.0' } },
    [`node_modules/${runtime}`]: { version: '3.1.0' },
  } }));
  return root;
}

test('reads the actual module and JSON owners, including harmless comments and formatting', t => {
  assert.equal(wailsVersion(fixture(t)), 'v3.1.0');
});

for (const file of ['go.mod', 'frontend/package.json', 'frontend/package-lock.json']) {
  for (const state of ['missing', 'malformed']) {
    test(`${file} ${state} cannot certify agreement`, t => {
      const root = fixture(t);
      if (state === 'missing') rmSync(join(root, file));
      else writeFileSync(join(root, file), 'not a valid document');
      assert.throws(() => wailsVersion(root));
    });
  }
}

for (const requirement of ['', ...['main', 'v3.1.0.1', 'v3.1.0-beta.1', 'v3.1.0#suffix'].map(version => `${modulePath} ${version}`)]) {
  test(`missing or non-release requirement is refused: ${requirement}`, t => {
    const root = fixture(t);
    writeFileSync(join(root, 'go.mod'), `module fixture\nrequire (\n${requirement}\n)\n`);
    assert.throws(() => wailsVersion(root));
  });
}

test('duplicate Wails requirements are refused', t => {
  const root = fixture(t);
  writeFileSync(join(root, 'go.mod'), `module fixture\nrequire (\n${modulePath} v3.1.0\n${modulePath} v3.1.0\n)\n`);
  assert.throws(() => wailsVersion(root));
});

for (const directive of [`replace ${modulePath} => ./other`, `exclude ${modulePath} v3.1.0`]) {
  test(`a module directive cannot redirect the CLI: ${directive}`, t => {
    const root = fixture(t);
    writeFileSync(join(root, 'go.mod'), `module fixture\nrequire ${modulePath} v3.1.0\n${directive}\n`);
    assert.throws(() => wailsVersion(root), /replacements and exclusions/);
  });
}

for (const owner of ['manifest', 'lock root', 'lock runtime']) {
  for (const value of [undefined, '3.0.0', '^3.1.0', '3.1.0#suffix']) {
    test(`${owner} must have an exact matching runtime: ${value}`, t => {
      const root = fixture(t);
      const file = join(root, 'frontend', owner === 'manifest' ? 'package.json' : 'package-lock.json');
      const data = JSON.parse(readFileSync(file, 'utf8'));
      if (owner === 'manifest') data.dependencies[runtime] = value;
      else if (owner === 'lock root') data.packages[''].dependencies[runtime] = value;
      else data.packages[`node_modules/${runtime}`].version = value;
      writeFileSync(file, JSON.stringify(data));
      assert.throws(() => wailsVersion(root), /must equal 3\.1\.0/);
    });
  }
}

test('workspace and alternate-module overrides cannot hide the checked-in pin', t => {
  const root = fixture(t);
  const previous = Object.fromEntries(['GOWORK', 'GOFLAGS', 'GO111MODULE'].map(key => [key, process.env[key]]));
  t.after(() => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
  process.env.GOWORK = join(root, 'not-a-workspace');
  process.env.GOFLAGS = `-modfile=${join(root, 'not-a-module')}`;
  process.env.GO111MODULE = 'off';
  assert.equal(wailsVersion(root), 'v3.1.0');
  writeFileSync(join(root, 'go.mod'), `module fixture\nrequire ${modulePath} v3.2.0\n`);
  assert.throws(() => wailsVersion(root), /must equal 3\.2\.0/);
});
