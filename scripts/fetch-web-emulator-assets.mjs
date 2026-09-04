#!/usr/bin/env node

import { randomBytes } from 'node:crypto';
import { lstat, mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { dirname, isAbsolute, join } from 'node:path';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { verifyWebEmulatorAssets } from './lib/web-emulator-assets.mjs';

const projectRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const manifest = JSON.parse(await readFile(join(projectRoot, 'testdata', 'web-emulation', 'fixtures.json'), 'utf8'));
const destination = process.argv[2] === '--directory' ? process.argv[3] : '';

if (!destination || !isAbsolute(destination) || process.argv.length !== 4) {
  console.error('usage: node scripts/fetch-web-emulator-assets.mjs --directory /absolute/new/path');
  process.exit(2);
}
if (!/^https:\/\/[^?#]+\/$/.test(manifest?.emulatorjs?.base_url || '')) {
  throw new Error('pinned EmulatorJS base URL is invalid');
}
try {
  await lstat(destination);
  throw new Error('destination already exists; choose a new path');
} catch (error) {
  if (error?.code !== 'ENOENT') throw error;
}

const staging = join(dirname(destination), `.varkiv-emulatorjs-${process.pid}-${randomBytes(6).toString('hex')}`);
let created = false;
try {
  await mkdir(staging, { mode: 0o700 });
  created = true;
  for (const asset of manifest.emulatorjs.assets) {
    const target = join(staging, ...asset.path.split('/'));
    await mkdir(dirname(target), { recursive: true, mode: 0o700 });
    let bytes;
    let failure;
    for (let attempt = 1; attempt <= 3; attempt++) {
      try {
        const response = await fetch(new URL(asset.path, manifest.emulatorjs.base_url), {
          redirect: 'error',
          signal: AbortSignal.timeout(30_000),
          headers: { 'User-Agent': 'Varkiv-pinned-asset-fetcher' }
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        bytes = Buffer.from(await response.arrayBuffer());
        failure = null;
        break;
      } catch (error) {
        failure = error;
        if (attempt < 3) await new Promise(resolve => setTimeout(resolve, attempt * 250));
      }
    }
    if (failure) throw new Error(`could not fetch pinned asset ${asset.path}: ${failure.message}`);
    await writeFile(target, bytes, { mode: 0o600, flag: 'wx' });
  }
  const report = verifyWebEmulatorAssets(staging, manifest);
  await rename(staging, destination);
  created = false;
  console.log(`emulatorjs_version=${report.version}`);
  console.log(`assets_verified=${report.assetsVerified}`);
  console.log(`bytes_verified=${report.bytesVerified}`);
  console.log('directory_reported=false');
} finally {
  if (created) await rm(staging, { recursive: true, force: true });
}
