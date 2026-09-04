#!/usr/bin/env node

import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { sha256, verifyWebEmulatorAssets } from './lib/web-emulator-assets.mjs';

const sandbox = mkdtempSync(join(tmpdir(), 'varkiv-web-assets-test.'));
const root = join(sandbox, 'assets');
mkdirSync(root, { mode: 0o700 });
const good = Buffer.from('fixed-loader');
const manifest = {
  emulatorjs: {
    version: 'test',
    assets: [{ path: 'loader.js', size: good.byteLength, sha256: sha256(good) }]
  }
};

try {
  writeFileSync(join(root, 'loader.js'), good, { mode: 0o600 });
  assert.deepEqual(verifyWebEmulatorAssets(root, manifest), {
    version: 'test', assetsVerified: 1, bytesVerified: good.byteLength
  });

  writeFileSync(join(root, 'loader.js'), Buffer.from('drift-loader'), { mode: 0o600 });
  assert.throws(() => verifyWebEmulatorAssets(root, manifest), /size drifted|SHA-256 drifted/);

  rmSync(join(root, 'loader.js'));
  const outside = join(sandbox, 'outside.js');
  writeFileSync(outside, good, { mode: 0o600 });
  symlinkSync(outside, join(root, 'loader.js'));
  assert.throws(() => verifyWebEmulatorAssets(root, manifest), /must not use symbolic links/);

  const unsafe = { emulatorjs: { version: 'test', assets: [{ ...manifest.emulatorjs.assets[0], path: '../outside.js' }] } };
  assert.throws(() => verifyWebEmulatorAssets(root, unsafe), /manifest path is invalid/);

  const command = spawnSync(process.execPath, [join(import.meta.dirname, 'verify-web-emulator-assets.mjs'), '--directory', root], { encoding: 'utf8' });
  assert.equal(command.status, 1);
  assert.equal(command.stderr.includes(sandbox), false);
  assert.equal(JSON.parse(command.stderr).directory_reported, false);

  const existing = join(sandbox, 'existing');
  mkdirSync(existing, { mode: 0o700 });
  const sentinel = join(existing, 'keep.txt');
  writeFileSync(sentinel, 'preserve', { mode: 0o600 });
  const fetch = spawnSync(process.execPath, [join(import.meta.dirname, 'fetch-web-emulator-assets.mjs'), '--directory', existing], { encoding: 'utf8' });
  assert.equal(fetch.status, 1);
  assert.match(fetch.stderr, /destination already exists/);
  assert.equal(fetch.stderr.includes(sandbox), false);
  assert.equal(readFileSync(sentinel, 'utf8'), 'preserve');
  console.log('web_emulator_asset_verifier=passed cases=6');
} finally {
  rmSync(sandbox, { recursive: true, force: true });
}
