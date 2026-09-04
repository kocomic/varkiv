#!/usr/bin/env node

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyWebEmulatorAssets } from './lib/web-emulator-assets.mjs';

const projectRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const args = process.argv.slice(2);
if (args.length !== 2 || args[0] !== '--directory') {
  console.error('usage: node scripts/verify-web-netplay-assets.mjs --directory /absolute/path/to/EmulatorJS/data');
  process.exit(2);
}
const manifest = JSON.parse(readFileSync(join(projectRoot, 'testdata', 'web-netplay', 'assets.json'), 'utf8'));
try {
  const result = verifyWebEmulatorAssets(args[1], manifest);
  console.log(JSON.stringify({
    format: 'varkiv-web-netplay-assets-v1', emulatorjs_version: result.version,
    assets_verified: result.assetsVerified, bytes_verified: result.bytesVerified, directory_reported: false
  }, null, 2));
} catch (error) {
  console.error(JSON.stringify({
    format: 'varkiv-web-netplay-assets-v1', error: 'web_netplay_assets_invalid',
    message: error instanceof Error ? error.message : 'EmulatorJS netplay asset verification failed', directory_reported: false
  }));
  process.exit(1);
}
