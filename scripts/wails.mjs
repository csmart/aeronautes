import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const modulePath = 'github.com/wailsapp/wails/v3';
const runtime = '@wailsio/runtime';

function goEnvironment() {
  // Workspace and alternate-module overrides must not hide the owned pin.
  return { ...process.env, GOWORK: 'off', GOFLAGS: '', GO111MODULE: 'on' };
}

export function wailsVersion(root) {
  const config = JSON.parse(execFileSync('go', ['mod', 'edit', '-json', join(root, 'go.mod')], {
    cwd: root, env: goEnvironment(), encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }));
  const required = (config.Require ?? []).filter(entry => entry.Path === modulePath);
  const version = required[0]?.Version;
  if (required.length !== 1 || !/^v3\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
    throw new Error('go.mod must require exactly one stable Wails v3 release');
  }
  if ((config.Replace ?? []).some(entry => entry.Old.Path === modulePath)
      || (config.Exclude ?? []).some(entry => entry.Path === modulePath)) {
    throw new Error('Wails replacements and exclusions would differ from the installed CLI');
  }
  const readJSON = name => JSON.parse(readFileSync(join(root, 'frontend', name), 'utf8'));
  const manifest = readJSON('package.json');
  const lock = readJSON('package-lock.json');
  for (const [owner, value] of [
    ['package.json', manifest.dependencies?.[runtime]],
    ['package-lock.json root', lock.packages?.['']?.dependencies?.[runtime]],
    ['package-lock.json runtime', lock.packages?.[`node_modules/${runtime}`]?.version],
  ]) {
    if (value !== version.slice(1)) {
      throw new Error(`${owner}: runtime ${JSON.stringify(value)} must equal ${version.slice(1)}`);
    }
  }
  return version;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const action = process.argv[2] ?? 'check';
    if (!['check', 'install'].includes(action) || process.argv.length > 3) {
      throw new Error('usage: node scripts/wails.mjs [check|install]');
    }
    const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
    const version = wailsVersion(root);
    if (action === 'install') {
      execFileSync('go', ['install', `${modulePath}/cmd/wails3@${version}`], {
        cwd: root, env: goEnvironment(), stdio: 'inherit',
      });
    }
    console.log(`Wails version: ${version}`);
  } catch (error) {
    console.error(`Wails version check failed: ${error.message}`);
    process.exitCode = 1;
  }
}
