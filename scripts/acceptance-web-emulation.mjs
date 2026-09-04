#!/usr/bin/env node

import { closeSync, openSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { mkdir, mkdtemp, readFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { dirname, isAbsolute, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn, spawnSync } from 'node:child_process';
import { chromium } from '@playwright/test';
import { PNG } from 'playwright-core/lib/utilsBundle';
import { sha256, verifyWebEmulatorAssets } from './lib/web-emulator-assets.mjs';

const cliArguments = process.argv.slice(2);
if (cliArguments.length) {
  if (cliArguments.length === 1 && ['--help', '-h'].includes(cliArguments[0])) {
    console.log(`Usage: scripts/acceptance-web-emulation.mjs

Launch every pinned public/generated browser-emulation fixture, verify runtime,
visual and interaction probes, and exercise supported save restoration in a new
private root. Configuration is supplied only through documented VARKIV_WEB_*
environment variables; no positional arguments are accepted. The acceptance
never reads a user library, NAS mount, production database, media, or save.`);
    process.exit(0);
  }
  console.error(`error: unexpected arguments: ${cliArguments.join(' ')}`);
  process.exit(2);
}

const projectRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const manifestPath = join(projectRoot, 'testdata', 'web-emulation', 'fixtures.json');
const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
const assetRoot = process.env.VARKIV_WEB_EMULATOR_DIRECTORY || '';
const requestedRoot = process.env.VARKIV_WEB_ACCEPTANCE_DIR || '';
const keep = process.env.VARKIV_WEB_ACCEPTANCE_KEEP === '1';
const requestedFixture = process.env.VARKIV_WEB_ACCEPTANCE_FIXTURE || '';
const requestedInitialSave = process.env.VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE || '';
const requestedInitialSaveSHA256 = (process.env.VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE_SHA256 || '').toLowerCase();
const requestedSavePrefixHex = (process.env.VARKIV_WEB_ACCEPTANCE_EXPECT_SAVE_PREFIX_HEX || '').toLowerCase();
const fixtures = requestedFixture ? manifest.fixtures.filter(item => item.id === requestedFixture) : manifest.fixtures;

if (!assetRoot || !isAbsolute(assetRoot)) {
  throw new Error('VARKIV_WEB_EMULATOR_DIRECTORY must be an absolute operator-provided EmulatorJS data directory');
}
if (requestedRoot && !isAbsolute(requestedRoot)) {
  throw new Error('VARKIV_WEB_ACCEPTANCE_DIR must be an absolute, new path');
}
if (requestedFixture && fixtures.length !== 1) {
  throw new Error(`unknown VARKIV_WEB_ACCEPTANCE_FIXTURE ${requestedFixture}`);
}
if (requestedInitialSave && (!isAbsolute(requestedInitialSave) || fixtures.length !== 1 || !fixtures[0].save_probe)) {
  throw new Error('VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE requires an absolute path and exactly one save-enabled fixture');
}
if (requestedInitialSave && !/^[0-9a-f]{64}$/.test(requestedInitialSaveSHA256)) {
  throw new Error('VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE_SHA256 must lock the seeded save');
}
if (requestedSavePrefixHex && (!requestedInitialSave || !/^(?:[0-9a-f]{2})+$/.test(requestedSavePrefixHex))) {
  throw new Error('VARKIV_WEB_ACCEPTANCE_EXPECT_SAVE_PREFIX_HEX requires a seeded save and complete hexadecimal bytes');
}

const initialSaveBytes = requestedInitialSave ? readFileSync(requestedInitialSave) : null;
if (initialSaveBytes && sha256(initialSaveBytes) !== requestedInitialSaveSHA256) {
  throw new Error(`seeded save SHA-256 mismatch (${sha256(initialSaveBytes)})`);
}
if (initialSaveBytes && fixtures[0].expected_save_bytes && initialSaveBytes.byteLength !== fixtures[0].expected_save_bytes) {
  throw new Error(`seeded save expected ${fixtures[0].expected_save_bytes} bytes, got ${initialSaveBytes.byteLength}`);
}
const generateROM = generator => {
  if (generator !== 'varkiv-atari2600-solid-v1') throw new Error(`unsupported ROM generator ${generator}`);
  const rom = Buffer.alloc(4096, 0);
  const program = Buffer.from([
    0x78, 0xd8, 0xa2, 0xff, 0x9a, 0xa9, 0x00, 0x85, 0x00, 0x85, 0x01,
    0xa9, 0x02, 0x85, 0x00, 0x85, 0x02, 0x85, 0x02, 0x85, 0x02, 0xa9, 0x00, 0x85, 0x00,
    0xa9, 0x02, 0x85, 0x01, 0xa0, 0x25, 0x85, 0x02, 0x88, 0xd0, 0xfb,
    0xa9, 0x00, 0x85, 0x01, 0xa9, 0x46, 0x85, 0x09, 0xa0, 0xc0, 0x85, 0x02, 0x88, 0xd0, 0xfb,
    0xa9, 0x02, 0x85, 0x01, 0xa0, 0x1e, 0x85, 0x02, 0x88, 0xd0, 0xfb, 0x4c, 0x0b, 0xf0
  ]);
  program.copy(rom);
  for (const offset of [0xffa, 0xffc, 0xffe]) {
    rom[offset] = 0x00;
    rom[offset + 1] = 0xf0;
  }
  return rom;
};
const transformROM = (bytes, transform) => {
  if (transform !== 'snes-spctest-sram-handshake-v2') throw new Error(`unsupported ROM transform ${transform}`);
  if (bytes.byteLength !== 128 * 1024) throw new Error(`${transform}: expected 128 KiB source ROM`);
  const rom = Buffer.from(bytes);
  const trampoline = 0x7eef;
  Buffer.from([
    0xaf, 0x00, 0x00, 0x70, // LDA $70:0000
    0xc9, 0x5a, // CMP #$5A
    0xf0, 0x09, // BEQ native/restored stage
    0xa9, 0x5a, // first run: LDA #$5A
    0x8f, 0x00, 0x00, 0x70, // STA $70:0000
    0x4c, 0x00, 0x80, // JMP $8000
    0xa9, 0xa5, // loaded run: LDA #$A5
    0x8f, 0x01, 0x00, 0x70, // STA $70:0001
    0x4c, 0x00, 0x80 // JMP $8000
  ]).copy(rom, trampoline);
  rom[0x7fd6] = 0x02; // ROM + RAM + battery
  rom[0x7fd8] = 0x01; // 2 KiB SRAM
  rom[0x7ffc] = 0xef;
  rom[0x7ffd] = 0xfe;
  rom[0x7fdc] = 0xff;
  rom[0x7fdd] = 0xff;
  rom[0x7fde] = 0x00;
  rom[0x7fdf] = 0x00;
  let checksum = 0;
  for (const value of rom) checksum = (checksum + value) & 0xffff;
  const complement = checksum ^ 0xffff;
  rom[0x7fdc] = complement & 0xff;
  rom[0x7fdd] = complement >> 8;
  rom[0x7fde] = checksum & 0xff;
  rom[0x7fdf] = checksum >> 8;
  return rom;
};
const verifyBytes = (label, bytes, expectedSize, expectedHash) => {
  if (bytes.byteLength !== expectedSize) throw new Error(`${label}: expected ${expectedSize} bytes, got ${bytes.byteLength}`);
  const actualHash = sha256(bytes);
  if (actualHash !== expectedHash) throw new Error(`${label}: SHA-256 mismatch (${actualHash})`);
};
const download = async (url, allowRedirect = false) => {
  let lastError;
  for (let attempt = 1; attempt <= 3; attempt++) {
    try {
      const response = await fetch(url, {
        redirect: allowRedirect ? 'follow' : 'error',
        signal: AbortSignal.timeout(30_000),
        headers: { 'User-Agent': 'Varkiv-web-acceptance' }
      });
      if (allowRedirect && !response.url.startsWith('https://')) throw new Error(`archive redirect left HTTPS: ${response.url}`);
      if (!response.ok) throw new Error(`download failed: ${response.status} ${url}`);
      return Buffer.from(await response.arrayBuffer());
    } catch (error) {
      lastError = error;
      if (attempt < 3) await new Promise(resolve => setTimeout(resolve, 250 * attempt));
    }
  }
  throw lastError;
};
const captureVisual = async (page, fixtureID, spec) => {
  const canvases = page.locator('canvas');
  const canvasIndex = await canvases.evaluateAll(items => items.reduce((best, item, index) => {
    const area = item.getBoundingClientRect().width * item.getBoundingClientRect().height;
    return area > best.area ? { index, area } : best;
  }, { index: -1, area: 0 }).index);
  if (canvasIndex < 0) throw new Error(`${fixtureID}: visual probe found no rendered canvas`);
  const fraction = spec.center_fraction;
  if (!(fraction > 0 && fraction <= 1)) throw new Error(`${fixtureID}: invalid visual center fraction`);
  const temporalFrames = spec.temporal_occupancy_frames || 1;
  const temporalInterval = spec.temporal_interval_ms || 0;
  if (!Number.isInteger(temporalFrames) || temporalFrames < 1 || temporalFrames > 120) {
    throw new Error(`${fixtureID}: temporal_occupancy_frames must be an integer from 1 to 120`);
  }
  if (!Number.isInteger(temporalInterval) || temporalInterval < 0 || temporalInterval > 1000) {
    throw new Error(`${fixtureID}: temporal_interval_ms must be an integer from 0 to 1000`);
  }
  if (temporalFrames > 1) {
    const gridWidth = spec.temporal_grid_width;
    const gridHeight = spec.temporal_grid_height;
    const minimumNonblankFrames = spec.temporal_min_nonblank_frames;
    const minimumBrightCells = spec.temporal_min_bright_cells;
    const minimumCellHits = spec.temporal_min_cell_hits;
    if (!Number.isInteger(gridWidth) || gridWidth < 16 || gridWidth > 512 || !Number.isInteger(gridHeight) || gridHeight < 16 || gridHeight > 512) {
      throw new Error(`${fixtureID}: temporal grid dimensions must be integers from 16 to 512`);
    }
    if (!Number.isInteger(minimumNonblankFrames) || minimumNonblankFrames < 1 || minimumNonblankFrames > temporalFrames) {
      throw new Error(`${fixtureID}: temporal_min_nonblank_frames must be from 1 to temporal_occupancy_frames`);
    }
    if (!Number.isInteger(minimumBrightCells) || minimumBrightCells < 1 || minimumBrightCells > gridWidth * gridHeight) {
      throw new Error(`${fixtureID}: temporal_min_bright_cells must fit the temporal grid`);
    }
    if (!Number.isInteger(minimumCellHits) || minimumCellHits < 1 || minimumCellHits > temporalFrames) {
      throw new Error(`${fixtureID}: temporal_min_cell_hits must be from 1 to temporal_occupancy_frames`);
    }
    const cellHits = new Uint16Array(gridWidth * gridHeight);
    const states = new Set();
    let nonblankFrames = 0;
    let width = 0;
    let height = 0;
    for (let frame = 0; frame < temporalFrames; frame++) {
      const decoded = PNG.sync.read(await canvases.nth(canvasIndex).screenshot());
      if (!width) {
        width = decoded.width;
        height = decoded.height;
      } else if (decoded.width !== width || decoded.height !== height) {
        throw new Error(`${fixtureID}: visual dimensions changed during temporal probe`);
      }
      const left = Math.floor(width * (1 - fraction) / 2);
      const right = Math.ceil(width * (1 + fraction) / 2);
      const top = Math.floor(height * (1 - fraction) / 2);
      const bottom = Math.ceil(height * (1 + fraction) / 2);
      const grid = Buffer.alloc(gridWidth * gridHeight);
      let brightCells = 0;
      for (let gridY = 0; gridY < gridHeight; gridY++) {
        const fromY = top + Math.floor((bottom - top) * gridY / gridHeight);
        const toY = top + Math.max(1, Math.floor((bottom - top) * (gridY + 1) / gridHeight));
        for (let gridX = 0; gridX < gridWidth; gridX++) {
          const fromX = left + Math.floor((right - left) * gridX / gridWidth);
          const toX = left + Math.max(1, Math.floor((right - left) * (gridX + 1) / gridWidth));
          let bright = false;
          for (let y = fromY; y < toY && !bright; y++) {
            for (let x = fromX; x < toX; x++) {
              const source = (y * width + x) * 4;
              const brightness = Math.max(decoded.data[source], decoded.data[source + 1], decoded.data[source + 2]);
              if (decoded.data[source + 3] && brightness > spec.brightness_threshold) {
                bright = true;
                break;
              }
            }
          }
          if (bright) {
            grid[gridY * gridWidth + gridX] = 1;
            brightCells++;
          }
        }
      }
      if (brightCells) {
        nonblankFrames++;
        const digest = sha256(grid);
        states.add(digest);
        for (let index = 0; index < grid.length; index++) {
          if (grid[index]) cellHits[index]++;
        }
      }
      if (frame + 1 < temporalFrames && temporalInterval) await page.waitForTimeout(temporalInterval);
    }
    if (!nonblankFrames) throw new Error(`${fixtureID}: temporal visual probe found no bright frame`);
    const occupancy = Buffer.alloc(gridWidth * gridHeight);
    for (let index = 0; index < occupancy.length; index++) {
      if (cellHits[index] >= minimumCellHits) occupancy[index] = 1;
    }
    const digest = sha256(occupancy);
    const brightCells = occupancy.reduce((count, value) => count + value, 0);
    const report = {
      width,
      height,
      center_fraction: fraction,
      stable_bright_cells: brightCells,
      center_sha256: digest,
      temporal_frames: temporalFrames,
      temporal_interval_ms: temporalInterval,
      temporal_grid: `${gridWidth}x${gridHeight}`,
      temporal_nonblank_frames: nonblankFrames,
      temporal_unique_states: states.size,
      temporal_min_cell_hits: minimumCellHits
    };
    if (brightCells < minimumBrightCells) {
      throw new Error(`${fixtureID}: temporal visual probe was too dark ${JSON.stringify(report)}`);
    }
    if (nonblankFrames < minimumNonblankFrames) {
      throw new Error(`${fixtureID}: temporal visual probe had too few nonblank frames ${JSON.stringify(report)}`);
    }
    if (spec.expected_center_sha256 && digest !== spec.expected_center_sha256) {
      throw new Error(`${fixtureID}: terminal visual hash drifted ${JSON.stringify(report)}`);
    }
    return { report, centerPixels: occupancy };
  }
  const decoded = PNG.sync.read(await canvases.nth(canvasIndex).screenshot());
  const left = Math.floor(decoded.width * (1 - fraction) / 2);
  const right = Math.ceil(decoded.width * (1 + fraction) / 2);
  const top = Math.floor(decoded.height * (1 - fraction) / 2);
  const bottom = Math.ceil(decoded.height * (1 + fraction) / 2);
  let brightPixels = 0;
  const centerPixels = Buffer.alloc((right - left) * (bottom - top) * 4);
  let target = 0;
  for (let y = top; y < bottom; y++) {
    for (let x = left; x < right; x++) {
      const source = (y * decoded.width + x) * 4;
      const brightness = Math.max(decoded.data[source], decoded.data[source + 1], decoded.data[source + 2]);
      if (decoded.data[source + 3] && brightness > spec.brightness_threshold) brightPixels++;
      decoded.data.copy(centerPixels, target, source, source + 4);
      target += 4;
    }
  }
  const report = {
    width: decoded.width,
    height: decoded.height,
    center_fraction: fraction,
    bright_pixels: brightPixels,
    center_sha256: sha256(centerPixels)
  };
  if (report.bright_pixels < spec.min_bright_pixels) {
    throw new Error(`${fixtureID}: visual probe failed ${JSON.stringify(report)}`);
  }
  if (spec.expected_center_sha256 && report.center_sha256 !== spec.expected_center_sha256) {
    throw new Error(`${fixtureID}: terminal visual hash drifted ${JSON.stringify(report)}`);
  }
  return { report, centerPixels };
};
const changedPixels = (before, after) => {
  if (before.length !== after.length) throw new Error('visual comparison dimensions drifted');
  let changed = 0;
  for (let index = 0; index < before.length; index += 4) {
    if (before[index] !== after[index] || before[index + 1] !== after[index + 1] || before[index + 2] !== after[index + 2] || before[index + 3] !== after[index + 3]) changed++;
  }
  return changed;
};
const performInputSteps = async (page, steps) => {
  await page.evaluate(() => window.EJS_emulator.elements.parent.focus());
  for (const step of steps) {
    if (step.action === 'down') {
      await page.keyboard.down(step.key);
    } else if (step.action === 'up') {
      await page.keyboard.up(step.key);
    } else if (step.action === 'press') {
      await page.keyboard.down(step.key);
      await page.waitForTimeout(step.hold_ms || 120);
      await page.keyboard.up(step.key);
    } else {
      throw new Error(`unsupported input action ${step.action}`);
    }
    if (step.after_ms) await page.waitForTimeout(step.after_ms);
  }
};
const api = async (baseURL, path, options = {}) => {
  const response = await fetch(baseURL + path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  });
  const body = await response.text();
  if (!response.ok) throw new Error(`${options.method || 'GET'} ${path}: ${response.status} ${body}`);
  return body ? JSON.parse(body) : null;
};
const freePort = () => new Promise((resolve, reject) => {
  const probe = createServer();
  probe.once('error', reject);
  probe.listen(0, '127.0.0.1', () => {
    const address = probe.address();
    probe.close(error => error ? reject(error) : resolve(address.port));
  });
});
const waitForHealth = async (baseURL, child) => {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`temporary server exited with ${child.exitCode}`);
    try {
      const health = await api(baseURL, '/api/v1/health');
      if (health.status === 'ok') return health;
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  throw new Error('temporary server did not become healthy');
};
const stopChild = child => new Promise(resolve => {
  if (!child || child.exitCode !== null) return resolve();
  child.once('exit', resolve);
  child.kill('SIGTERM');
  setTimeout(() => {
    if (child.exitCode === null) child.kill('SIGKILL');
    resolve();
  }, 3000).unref();
});

let acceptanceRoot;
if (requestedRoot) {
  mkdirSyncNew(requestedRoot);
  acceptanceRoot = requestedRoot;
} else {
  acceptanceRoot = await mkdtemp(join(process.env.TMPDIR || '/tmp', 'varkiv-web.'));
}
await mkdir(acceptanceRoot, { recursive: true, mode: 0o700 });
const libraryRoot = join(acceptanceRoot, 'library');
const stateRoot = join(acceptanceRoot, 'state');
const screenshotRoot = join(acceptanceRoot, 'screenshots');
await mkdir(libraryRoot, { recursive: true, mode: 0o700 });
await mkdir(stateRoot, { recursive: true, mode: 0o700 });
await mkdir(screenshotRoot, { recursive: true, mode: 0o700 });

function mkdirSyncNew(path) {
  try {
    statSync(path);
    throw new Error(`refusing to reuse VARKIV_WEB_ACCEPTANCE_DIR: ${path}`);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  spawnSync('mkdir', ['-m', '700', path], { stdio: 'inherit' });
}

let server;
let browser;
try {
  verifyWebEmulatorAssets(assetRoot, manifest);

  const licenseCache = new Map();
  for (const fixture of fixtures) {
    const licenseSource = fixture.license_url || fixture.license_path;
    if (!licenseSource) throw new Error(`${fixture.id}: license source is required`);
    let licenseBytes = licenseCache.get(licenseSource);
    if (!licenseBytes) {
      licenseBytes = fixture.license_path
        ? readFileSync(join(projectRoot, fixture.license_path))
        : await download(fixture.license_url);
      verifyBytes(`${fixture.id} license`, licenseBytes, licenseBytes.byteLength, fixture.license_sha256);
      licenseCache.set(licenseSource, licenseBytes);
      const licenseFile = join(acceptanceRoot, 'licenses', `${sha256(Buffer.from(licenseSource)).slice(0, 12)}-${fixture.license}.txt`);
      await mkdir(dirname(licenseFile), { recursive: true, mode: 0o700 });
      writeFileSync(licenseFile, licenseBytes, { mode: 0o600 });
    }
    let romBytes;
    if (fixture.rom_generator) {
      romBytes = generateROM(fixture.rom_generator);
    } else if (fixture.archive_url) {
      const archiveBytes = await download(fixture.archive_url, true);
      verifyBytes(`${fixture.id} archive`, archiveBytes, fixture.archive_size, fixture.archive_sha256);
      const archiveFile = join(acceptanceRoot, 'downloads', `${fixture.id}.zip`);
      await mkdir(dirname(archiveFile), { recursive: true, mode: 0o700 });
      writeFileSync(archiveFile, archiveBytes, { mode: 0o600 });
      const extracted = spawnSync('unzip', ['-p', archiveFile, fixture.archive_member], { encoding: null, maxBuffer: 16 * 1024 * 1024 });
      if (extracted.status !== 0) throw new Error(`${fixture.id}: could not extract fixed archive member ${fixture.archive_member}`);
      romBytes = Buffer.from(extracted.stdout);
    } else {
      const redirectPolicy = fixture.rom_redirect_policy || 'forbid';
      if (!['forbid', 'verified-github-release'].includes(redirectPolicy)) {
        throw new Error(`${fixture.id}: unsupported ROM redirect policy ${redirectPolicy}`);
      }
      if (redirectPolicy === 'verified-github-release' && !/^https:\/\/github\.com\/[^/]+\/[^/]+\/releases\/download\//.test(fixture.rom_url)) {
        throw new Error(`${fixture.id}: verified release redirect must start at a GitHub HTTPS release URL`);
      }
      romBytes = await download(fixture.rom_url, redirectPolicy === 'verified-github-release');
    }
    if (fixture.rom_transform) {
      verifyBytes(`${fixture.id} source ROM`, romBytes, fixture.source_rom_size, fixture.source_rom_sha256);
      romBytes = transformROM(romBytes, fixture.rom_transform);
    }
    verifyBytes(`${fixture.id} ROM`, romBytes, fixture.rom_size, fixture.rom_sha256);
    const romFile = join(libraryRoot, fixture.platform, fixture.id, fixture.library_name || fixture.rom_path.split('/').at(-1));
    await mkdir(dirname(romFile), { recursive: true, mode: 0o700 });
    writeFileSync(romFile, romBytes, { mode: 0o600 });
    fixture.library_path = romFile.slice(libraryRoot.length + 1).split('\\').join('/');
  }

  const binary = join(acceptanceRoot, 'varkiv');
  const build = spawnSync('go', ['build', '-trimpath', '-o', binary, './cmd/varkiv'], { cwd: projectRoot, encoding: 'utf8' });
  if (build.status !== 0) throw new Error(`go build failed:\n${build.stdout}${build.stderr}`);

  const port = await freePort();
  const address = `127.0.0.1:${port}`;
  const baseURL = `http://${address}`;
  const logPath = join(acceptanceRoot, 'server.log');
  const log = openSync(logPath, 'wx', 0o600);
  server = spawn(binary, [
    'serve', '--addr', address, '--db', join(acceptanceRoot, 'library.db'),
    '--library', libraryRoot, '--state', stateRoot, '--web-emulator-directory', assetRoot
  ], { cwd: projectRoot, stdio: ['ignore', log, log] });
  closeSync(log);
  const health = await waitForHealth(baseURL, server);

  for (const fixture of fixtures) {
    const request = { source: fixture.library_path, platform: fixture.platform, rom_storage: 'reference' };
    const preview = await api(baseURL, '/api/v1/imports/roms/preview', { method: 'POST', body: JSON.stringify(request) });
    const selected = preview.candidates.filter(item => item.status === 'new').map(item => item.token);
    if (selected.length !== 1) throw new Error(`${fixture.id}: expected one importable signed candidate, got ${selected.length}`);
    const committed = await api(baseURL, '/api/v1/imports/roms/commit', {
      method: 'POST', body: JSON.stringify({ ...request, preview_token: preview.preview_token, selected_tokens: selected })
    });
    if (committed.imported !== 1 || committed.failure_policy !== 'atomic') {
      throw new Error(`${fixture.id}: unexpected commit result ${JSON.stringify(committed)}`);
    }
  }

  const games = (await api(baseURL, '/api/v1/games?locale=en')).data;
  browser = await chromium.launch({ headless: true });
  const browserResults = [];
  for (const fixture of fixtures) {
    const game = games.find(item => (item.editions || []).some(edition => (edition.artifacts || []).some(artifact => artifact.sha256 === fixture.rom_sha256)));
    const edition = game?.editions?.find(item => (item.artifacts || []).some(artifact => artifact.sha256 === fixture.rom_sha256));
    if (!edition) throw new Error(`${fixture.id}: imported Edition could not be resolved by SHA-256`);
    let session;
    try {
      session = await api(baseURL, '/api/v1/web-emulation/sessions', {
        method: 'POST', body: JSON.stringify({ edition_id: edition.id, locale: 'en' })
      });
    } catch (error) {
      throw new Error(`${fixture.id}: browser session creation failed: ${error.message}`);
    }
    if (session.status.core !== fixture.core || session.status.driver_id !== fixture.driver_id || session.asset_source !== 'self-hosted') {
      throw new Error(`${fixture.id}: runtime contract drifted ${JSON.stringify(session.status)}`);
    }
    let seededSaveRevision = '';
    if (initialSaveBytes) {
      const token = session.player_url.startsWith('/play/') ? session.player_url.slice('/play/'.length) : '';
      if (!token) throw new Error(`${fixture.id}: seeded save session omitted its capability token`);
      const seeded = await api(baseURL, `/api/v1/web-emulation/saves/${token}`, {
        method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: initialSaveBytes
      });
      if (!seeded.created || seeded.conflict || !seeded.revision?.id) {
        throw new Error(`${fixture.id}: seeded save upload failed ${JSON.stringify(seeded)}`);
      }
      seededSaveRevision = seeded.revision.id;
    }
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();
    const failures = [];
    let closing = false;
    let abortedSaveReads = 0;
    let fetchErrors = 0;
    let visibleSyncErrors = 0;
    page.on('pageerror', error => {
      if (closing || error.message === 'Wake Lock permission request denied') return;
      if (error.message === 'Failed to fetch') fetchErrors++;
      else failures.push(`pageerror: ${error.message}`);
    });
    page.on('requestfailed', request => {
      if (closing) return;
      if (request.method() === 'GET' && request.url().includes('/api/v1/web-emulation/saves/') && request.failure()?.errorText === 'net::ERR_ABORTED') abortedSaveReads++;
      else failures.push(`requestfailed: ${request.url()} ${request.failure()?.errorText || ''}`);
    });
    await page.goto(baseURL + session.player_url, { waitUntil: 'domcontentloaded' });
    await page.locator('body[data-runtime-state="ready"]').waitFor({ timeout: 30_000 });
    const initialSaveRead = page.waitForResponse(response => response.request().method() === 'GET' && response.url().includes('/api/v1/web-emulation/saves/') && (response.status() === 200 || response.status() === 204), { timeout: 30_000 });
    await page.locator('.ejs_start_button').click({ timeout: 15_000 });
    await page.locator('body[data-runtime-state="started"]').waitFor({ timeout: 60_000 });
    await initialSaveRead;
    await page.waitForTimeout(1000);
    if (fixture.visual_delay_ms) {
      if (!Number.isInteger(fixture.visual_delay_ms) || fixture.visual_delay_ms < 0 || fixture.visual_delay_ms > 60_000) {
        throw new Error(`${fixture.id}: visual_delay_ms must be an integer from 0 to 60000`);
      }
      await page.waitForTimeout(fixture.visual_delay_ms);
    }
    // Persist the full page before strict pixel assertions so a deterministic
    // hash failure still leaves reviewable visual evidence when KEEP is set.
    await page.screenshot({ path: join(screenshotRoot, `${fixture.id}.png`) });
    let visualResult = null;
    if (fixture.visual_probe) {
      visualResult = (await captureVisual(page, fixture.id, fixture.visual_probe)).report;
    }
    let interactionResult = null;
    if (fixture.interaction_probe) {
      await page.waitForTimeout(fixture.interaction_probe.delay_ms);
      const interactionVisual = { ...fixture.visual_probe, center_fraction: fixture.interaction_probe.center_fraction };
      const before = await captureVisual(page, fixture.id, interactionVisual);
      await page.screenshot({ path: join(screenshotRoot, `${fixture.id}-before-interaction.png`) });
      const inputState = await page.evaluate(() => ({
        controls: window.EJS_emulator.controls[0],
        keyboard_input: window.EJS_emulator.getSettingValue('keyboardInput'),
        parent_id: window.EJS_emulator.elements.parent.id,
        parent_tab_index: window.EJS_emulator.elements.parent.tabIndex,
        active_id: document.activeElement?.id || '',
        active_tag: document.activeElement?.tagName || ''
      }));
      await page.evaluate(() => {
        window.__varkivInputProbe = { events: [], simulated: [] };
        const parent = window.EJS_emulator.elements.parent;
        parent.addEventListener('keydown', event => window.__varkivInputProbe.events.push(['down', event.keyCode]));
        parent.addEventListener('keyup', event => window.__varkivInputProbe.events.push(['up', event.keyCode]));
        const gameManager = window.EJS_emulator.gameManager;
        const original = gameManager.simulateInput.bind(gameManager);
        gameManager.simulateInput = (...args) => {
          window.__varkivInputProbe.simulated.push(args);
          return original(...args);
        };
        parent.focus();
      });
      await page.keyboard.down(fixture.interaction_probe.key);
      await page.waitForTimeout(fixture.interaction_probe.hold_ms);
      await page.keyboard.up(fixture.interaction_probe.key);
      await page.waitForTimeout(fixture.interaction_probe.settle_ms);
      const inputTrace = await page.evaluate(() => window.__varkivInputProbe);
      const after = await captureVisual(page, fixture.id, interactionVisual);
      await page.screenshot({ path: join(screenshotRoot, `${fixture.id}-interactive.png`) });
      const changed = changedPixels(before.centerPixels, after.centerPixels);
      if (changed < fixture.interaction_probe.min_changed_pixels) {
        throw new Error(`${fixture.id}: interaction probe changed only ${changed} center pixels (${before.report.center_sha256} -> ${after.report.center_sha256}); input=${JSON.stringify(inputState)}; trace=${JSON.stringify(inputTrace)}`);
      }
      interactionResult = {
        key: fixture.interaction_probe.key,
        changed_pixels: changed,
        before_sha256: before.report.center_sha256,
        after_sha256: after.report.center_sha256,
        trace: inputTrace
      };
    }
    let saveResult = 'not-requested';
    let saveBytes = 0;
    let saveSHA256 = '';
    if (fixture.save_probe) {
      if (fixture.save_interaction) {
        await performInputSteps(page, fixture.save_interaction);
        await page.screenshot({ path: join(screenshotRoot, `${fixture.id}-saved-in-rom.png`) });
      }
      const uploaded = page.waitForResponse(response => response.request().method() === 'POST' && response.url().includes('/api/v1/web-emulation/saves/'), { timeout: 30_000 });
      await page.evaluate(() => window.EJS_emulator.gameManager.saveSaveFiles());
      let uploadResponse;
      try {
        uploadResponse = await uploaded;
      } catch (error) {
        const diagnostic = await page.evaluate(() => {
          const manager = window.EJS_emulator?.gameManager;
          const path = manager?.getSaveFilePath?.() || '';
          return {
            path,
            exists: !!path && !!manager?.FS?.analyzePath(path)?.exists,
            files: manager?.FS?.analyzePath('/data/saves')?.exists ? manager.FS.readdir('/data/saves').filter(value => value !== '.' && value !== '..') : []
          };
        });
        throw new Error(`${fixture.id}: core did not emit a browser save ${JSON.stringify(diagnostic)}`, { cause: error });
      }
      const uploadedBody = uploadResponse.request().postDataBuffer();
      const uploadBody = await uploadResponse.json();
      if (uploadResponse.status() !== 201 || !uploadedBody?.byteLength || uploadBody.conflict || !uploadBody.revision?.id || (!initialSaveBytes && !uploadBody.created)) {
        throw new Error(`${fixture.id}: real core save upload failed ${JSON.stringify(uploadBody)}`);
      }
      if (initialSaveBytes && !uploadBody.created && uploadBody.revision.id !== seededSaveRevision) {
        throw new Error(`${fixture.id}: byte-identical seeded save did not deduplicate to its current revision`);
      }
      const expectedPrefixHex = requestedSavePrefixHex || fixture.expected_save_prefix_hex || '';
      if (expectedPrefixHex && !uploadedBody.subarray(0, expectedPrefixHex.length / 2).equals(Buffer.from(expectedPrefixHex, 'hex'))) {
        throw new Error(`${fixture.id}: core-emitted save does not contain expected prefix ${expectedPrefixHex}`);
      }
      visibleSyncErrors += await page.locator('.sync[data-error="true"]:visible').count();
      closing = true;
      await context.close();

      const restoreSession = await api(baseURL, '/api/v1/web-emulation/sessions', {
        method: 'POST', body: JSON.stringify({ edition_id: edition.id, locale: 'en' })
      });
      const restoreContext = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      const restorePage = await restoreContext.newPage();
      let restoreClosing = false;
      restorePage.on('pageerror', error => {
        if (restoreClosing || error.message === 'Wake Lock permission request denied') return;
        if (error.message === 'Failed to fetch') fetchErrors++;
        else failures.push(`restore pageerror: ${error.message}`);
      });
      restorePage.on('requestfailed', request => {
        if (restoreClosing) return;
        if (request.method() === 'GET' && request.url().includes('/api/v1/web-emulation/saves/') && request.failure()?.errorText === 'net::ERR_ABORTED') abortedSaveReads++;
        else failures.push(`restore requestfailed: ${request.url()} ${request.failure()?.errorText || ''}`);
      });
      await restorePage.goto(baseURL + restoreSession.player_url, { waitUntil: 'domcontentloaded' });
      await restorePage.locator('body[data-runtime-state="ready"]').waitFor({ timeout: 30_000 });
      const restored = restorePage.waitForResponse(response => response.request().method() === 'GET' && response.url().includes('/api/v1/web-emulation/saves/') && response.status() === 200, { timeout: 30_000 });
      await restorePage.locator('.ejs_start_button').click({ timeout: 15_000 });
      await restorePage.locator('body[data-runtime-state="started"]').waitFor({ timeout: 60_000 });
      const restoreResponse = await restored;
      const restoredBody = await restoreResponse.body();
      saveBytes = restoredBody.byteLength;
      saveSHA256 = sha256(restoredBody);
      if (restoreResponse.headers()['x-varkiv-revision'] !== uploadBody.revision.id || saveBytes === 0) {
        throw new Error(`${fixture.id}: fresh session did not restore the uploaded revision`);
      }
      if (!restoredBody.equals(uploadedBody)) {
        throw new Error(`${fixture.id}: restored save bytes differ from the browser upload`);
      }
      if (fixture.expected_save_bytes && saveBytes !== fixture.expected_save_bytes) {
        throw new Error(`${fixture.id}: expected ${fixture.expected_save_bytes} restored save bytes, got ${saveBytes}`);
      }
      if (fixture.expected_save_prefix_hex && !restoredBody.subarray(0, fixture.expected_save_prefix_hex.length / 2).equals(Buffer.from(fixture.expected_save_prefix_hex, 'hex'))) {
        throw new Error(`${fixture.id}: restored save does not contain the in-ROM SRAM sentinel`);
      }
      await restorePage.waitForTimeout(1000);
      await restorePage.screenshot({ path: join(screenshotRoot, `${fixture.id}-restored.png`) });
      visibleSyncErrors += await restorePage.locator('.sync[data-error="true"]:visible').count();
      restoreClosing = true;
      await restoreContext.close();
      saveResult = `restored:${uploadBody.revision.id}`;
    } else {
      visibleSyncErrors += await page.locator('.sync[data-error="true"]:visible').count();
      closing = true;
      await context.close();
    }
    const unexplainedFetchErrors = Math.max(0, fetchErrors - abortedSaveReads);
    const toleratedHeadlessFetchErrors = fixture.save_probe ? (initialSaveBytes ? 2 : 1) : 0;
    if (visibleSyncErrors) failures.push(`player displayed ${visibleSyncErrors} save-sync error(s)`);
    if (unexplainedFetchErrors > toleratedHeadlessFetchErrors || (unexplainedFetchErrors && visibleSyncErrors)) {
      failures.push(`pageerror: ${unexplainedFetchErrors} unexplained fetch failure(s)`);
    }
    if (failures.length) throw new Error(`${fixture.id}: browser failures:\n${failures.join('\n')}`);
    browserResults.push({
      id: fixture.id, core: fixture.core, runtime: 'started', save: saveResult,
      save_bytes: saveBytes, save_sha256: saveSHA256, visual: visualResult, interaction: interactionResult,
      seeded_save_revision: seededSaveRevision || null,
      seeded_save_sha256: initialSaveBytes ? requestedInitialSaveSHA256 : null,
      tolerated_headless_fetch_errors: toleratedHeadlessFetchErrors,
      ignored_headless_save_aborts: abortedSaveReads,
      ignored_headless_fetch_errors: fixture.save_probe ? unexplainedFetchErrors : 0
    });
  }

  const report = {
    manifest_schema: manifest.schema_version,
    application_version: health.version,
    emulatorjs_version: manifest.emulatorjs.version,
    asset_source: 'operator-directory',
    fixtures: browserResults,
    privacy: { user_library_read: false, roms_committed_to_repository: false },
    evidence_root: acceptanceRoot
  };
  writeFileSync(join(acceptanceRoot, 'report.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  console.log(JSON.stringify(report, null, 2));
} finally {
  if (browser) await browser.close().catch(() => {});
  await stopChild(server);
  if (!keep && acceptanceRoot) rmSync(acceptanceRoot, { recursive: true, force: true });
  else if (acceptanceRoot) console.error(`retained_evidence=${acceptanceRoot}`);
}
