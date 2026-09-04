#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { closeSync, openSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { mkdir, mkdtemp } from 'node:fs/promises';
import { createServer } from 'node:net';
import { dirname, isAbsolute, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn, spawnSync } from 'node:child_process';
import { chromium } from '@playwright/test';
import { verifyWebEmulatorAssets } from './lib/web-emulator-assets.mjs';

const args = process.argv.slice(2);
if (args.length) {
  if (args.length === 1 && ['--help', '-h'].includes(args[0])) {
    console.log(`Usage: scripts/acceptance-web-netplay.mjs

Runs an isolated two-browser NES netplay acceptance against a separately
started EmulatorJS signal server. No user library, database, saves, or NAS
paths are read. Set VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY and
VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM.`);
    process.exit(0);
  }
  throw new Error(`unexpected arguments: ${args.join(' ')}`);
}

const projectRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const manifest = JSON.parse(readFileSync(join(projectRoot, 'testdata', 'web-netplay', 'assets.json'), 'utf8'));
const assetRoot = process.env.VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY || '';
const signalUpstream = process.env.VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM || '';
const requestedRoot = process.env.VARKIV_WEB_NETPLAY_ACCEPTANCE_DIR || '';
const keep = process.env.VARKIV_WEB_NETPLAY_ACCEPTANCE_KEEP === '1';
const token = `acceptance-${createHash('sha256').update(String(process.pid)).digest('hex')}`;
const fixture = {
  url: 'https://raw.githubusercontent.com/battlelinegames/nes-starter-kit/355eb268f44055abd9885af794751b996285fc74/starter.nes',
  size: 40976,
  sha256: 'afa7800f4cb388f3cade9fa536d45bffaf0d5e1dd15ca5f86f894bfb5c9c648a'
};

if (!isAbsolute(assetRoot)) throw new Error('VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY must be an absolute verified data directory');
if (!/^https?:\/\/[^/?#]+(?::\d+)?$/.test(signalUpstream)) throw new Error('VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM must be an HTTP(S) origin');
if (requestedRoot && !isAbsolute(requestedRoot)) throw new Error('VARKIV_WEB_NETPLAY_ACCEPTANCE_DIR must be an absolute, new path');

const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const freePort = () => new Promise((resolve, reject) => {
  const probe = createServer();
  probe.once('error', reject);
  probe.listen(0, '127.0.0.1', () => {
    const address = probe.address();
    probe.close(error => error ? reject(error) : resolve(address.port));
  });
});
const stopChild = child => new Promise(resolve => {
  if (!child || child.exitCode !== null) return resolve();
  child.once('exit', resolve);
  child.kill('SIGTERM');
  setTimeout(() => {
    if (child.exitCode === null) child.kill('SIGKILL');
    resolve();
  }, 3000).unref();
});
const api = async (baseURL, path, options = {}, authenticated = false) => {
  const response = await fetch(baseURL + path, {
    ...options,
    headers: {
      ...(options.body ? {'Content-Type': 'application/json'} : {}),
      ...(authenticated ? {Authorization: `Bearer ${token}`} : {}),
      ...(options.headers || {})
    }
  });
  const text = await response.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = text; }
  if (!response.ok) throw new Error(`${options.method || 'GET'} ${path}: ${response.status} ${text}`);
  return body;
};
const apiResult = async (baseURL, path, options = {}, authenticated = false) => {
  const response = await fetch(baseURL + path, {
    ...options,
    headers: {
      ...(options.body ? {'Content-Type': 'application/json'} : {}),
      ...(authenticated ? {Authorization: `Bearer ${token}`} : {}),
      ...(options.headers || {})
    }
  });
  const text = await response.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = text; }
  return {status: response.status, body};
};
const waitForHealth = async (baseURL, child) => {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`temporary Varkiv server exited with ${child.exitCode}`);
    try {
      const health = await api(baseURL, '/api/v1/health');
      if (health.status === 'ok') return health;
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  throw new Error('temporary Varkiv server did not become healthy');
};
const waitForNetplay = async (scope, label) => {
  const body = scope.locator('body[data-netplay-state="connected"]');
  await body.waitFor({ timeout: 45_000 });
  return body.evaluate((element, name) => {
    const runtimeWindow = element.ownerDocument.defaultView;
    const netplay = runtimeWindow.EJS_emulator?.netplay;
    return {
      label: name,
      runtime: element.dataset.runtimeState,
      state: element.dataset.netplayState,
      players: Object.keys(netplay?.players || {}).length,
      owner: !!netplay?.owner,
      web_rtc_ready: !!netplay?.webRtcReady,
      peer_connections: Object.keys(netplay?.peerConnections || {}).length
    };
  }, label);
};

let root;
let server;
let browser;
const cleanupRoot = () => {
  if (!keep && root) {
    rmSync(root, {recursive: true, force: true});
    root = undefined;
  }
};
process.once('exit', cleanupRoot);
try {
  verifyWebEmulatorAssets(assetRoot, manifest);
  if (requestedRoot) {
    try {
      statSync(requestedRoot);
      throw new Error(`refusing to reuse VARKIV_WEB_NETPLAY_ACCEPTANCE_DIR: ${requestedRoot}`);
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    const made = spawnSync('mkdir', ['-m', '700', requestedRoot]);
    if (made.status !== 0) throw new Error('could not create requested acceptance directory');
    root = requestedRoot;
  } else {
    root = await mkdtemp(join(process.env.TMPDIR || '/tmp', 'varkiv-netplay.'));
  }
  const libraryRoot = join(root, 'library');
  const stateRoot = join(root, 'state');
  await mkdir(join(libraryRoot, 'nes'), { recursive: true, mode: 0o700 });
  await mkdir(stateRoot, { recursive: true, mode: 0o700 });

  const response = await fetch(fixture.url, { redirect: 'error', signal: AbortSignal.timeout(30_000), headers: {'User-Agent': 'Varkiv-netplay-acceptance'} });
  if (!response.ok) throw new Error(`fixture download failed: HTTP ${response.status}`);
  const rom = Buffer.from(await response.arrayBuffer());
  if (rom.byteLength !== fixture.size || sha256(rom) !== fixture.sha256) throw new Error('public NES fixture identity drifted');
  writeFileSync(join(libraryRoot, 'nes', 'starter.nes'), rom, { mode: 0o600, flag: 'wx' });

  const binary = join(root, 'varkiv');
  const build = spawnSync('go', ['build', '-trimpath', '-o', binary, './cmd/varkiv'], { cwd: projectRoot, encoding: 'utf8' });
  if (build.status !== 0) throw new Error(`go build failed:\n${build.stdout}${build.stderr}`);
  const port = await freePort();
  const address = `127.0.0.1:${port}`;
  const baseURL = `http://${address}`;
  const logPath = join(root, 'server.log');
  const log = openSync(logPath, 'wx', 0o600);
  server = spawn(binary, [
    'serve', '--addr', address, '--db', join(root, 'library.db'), '--library', libraryRoot,
    '--state', stateRoot, '--token', token, '--web-netplay-emulator-directory', assetRoot,
    '--web-netplay-signal-upstream', signalUpstream, '--web-netplay-ice-servers', '[]'
  ], { cwd: projectRoot, stdio: ['ignore', log, log] });
  closeSync(log);
  const health = await waitForHealth(baseURL, server);
  const readiness = await api(baseURL, '/api/v1/web-netplay/readiness');
  if (!readiness.enabled || !readiness.signal_ready || !readiness.integrity_verified || readiness.save_policy !== 'no-persist') {
    throw new Error(`netplay readiness contract failed: ${JSON.stringify(readiness)}`);
  }

  const importRequest = { source: 'nes/starter.nes', platform: 'nes', rom_storage: 'reference' };
  const preview = await api(baseURL, '/api/v1/imports/roms/preview', { method: 'POST', body: JSON.stringify(importRequest) }, true);
  const selected = preview.candidates.filter(candidate => candidate.status === 'new').map(candidate => candidate.token);
  if (selected.length !== 1) throw new Error(`expected one importable fixture, got ${selected.length}`);
  const committed = await api(baseURL, '/api/v1/imports/roms/commit', { method: 'POST', body: JSON.stringify({...importRequest, preview_token: preview.preview_token, selected_tokens: selected}) }, true);
  if (committed.imported !== 1) throw new Error(`fixture import failed: ${JSON.stringify(committed)}`);
  const games = (await api(baseURL, '/api/v1/games?locale=en', {}, true)).data;
  const game = games.find(item => (item.editions || []).some(candidate => (candidate.artifacts || []).some(artifact => artifact.sha256 === fixture.sha256)));
  const edition = games.flatMap(game => game.editions || []).find(item => (item.artifacts || []).some(artifact => artifact.sha256 === fixture.sha256));
  if (!game || !edition) throw new Error('imported NES edition was not found');

  const differentROM = Buffer.from(rom);
  differentROM[differentROM.length - 1] ^= 0xff;
  const differentSHA256 = sha256(differentROM);
  writeFileSync(join(libraryRoot, 'nes', 'different.nes'), differentROM, {mode: 0o600, flag: 'wx'});
  const differentRequest = {source: 'nes/different.nes', platform: 'nes', rom_storage: 'reference'};
  const differentPreview = await api(baseURL, '/api/v1/imports/roms/preview', {method: 'POST', body: JSON.stringify(differentRequest)}, true);
  const differentTokens = differentPreview.candidates.filter(candidate => candidate.status === 'new').map(candidate => candidate.token);
  if (differentTokens.length !== 1) throw new Error(`expected one mismatched fixture candidate, got ${differentTokens.length}`);
  await api(baseURL, '/api/v1/imports/roms/commit', {method: 'POST', body: JSON.stringify({...differentRequest, preview_token: differentPreview.preview_token, selected_tokens: differentTokens})}, true);
  const gamesWithMismatch = (await api(baseURL, '/api/v1/games?locale=en', {}, true)).data;
  const differentEdition = gamesWithMismatch.flatMap(item => item.editions || []).find(item => (item.artifacts || []).some(artifact => artifact.sha256 === differentSHA256));
  if (!differentEdition) throw new Error('mismatched NES edition was not found');

  browser = await chromium.launch({ headless: true });
  const hostContext = await browser.newContext({ viewport: {width: 960, height: 700} });
  const guestContext = await browser.newContext({ viewport: {width: 390, height: 844} });
  for (const [context, clientID] of [[hostContext, 'host-browser'], [guestContext, 'guest-browser']]) {
    await context.addInitScript(({authToken, netplayClientID}) => {
      sessionStorage.setItem('game-library-token', authToken);
      sessionStorage.setItem('varkiv-web-netplay-client', netplayClientID);
      if (netplayClientID === 'guest-browser') {
        const buttons = Array.from({length: 17}, () => ({pressed: false, touched: false, value: 0}));
        const gamepad = {id: 'Varkiv acceptance gamepad', index: 0, connected: true, mapping: 'standard', timestamp: 0, axes: [0, 0, 0, 0], buttons};
        Object.defineProperty(navigator, 'getGamepads', {configurable: true, value: () => [gamepad]});
        Object.defineProperty(window, '__varkivAcceptanceGamepad', {configurable: true, value: gamepad});
      }
    }, {authToken: token, netplayClientID: clientID});
  }
  const hostPage = await hostContext.newPage();
  const guestPage = await guestContext.newPage();
  const pageFailures = [];
  const headlessWarnings = [];
  const nonOKResponses = [];
  const saveRequests = [];
  for (const [label, page] of [['host', hostPage], ['guest', guestPage]]) {
    page.on('pageerror', error => {
      if (error.message === 'Wake Lock permission request denied' || error.message === 'Failed to fetch') headlessWarnings.push(`${label}: ${error.message}`);
      else pageFailures.push(`${label}: ${error.message}`);
    });
    page.on('response', response => {
      if (response.status() >= 400) nonOKResponses.push(`${label}:${response.status()}:${response.url()}`);
    });
    page.on('request', request => {
      if (request.url().includes('/web-emulation/saves/')) saveRequests.push(`${label}:${request.method()}:${request.url()}`);
    });
    page.on('requestfailed', request => {
      const expectedGuestAbort = label === 'guest' && request.method() === 'GET' && request.url().includes('/api/v1/web-emulation/content/') && request.failure()?.errorText === 'net::ERR_ABORTED';
      if (expectedGuestAbort) headlessWarnings.push(`${label}: content fetch aborted after remote stream handoff`);
      else pageFailures.push(`${label}: request failed ${request.url()} ${request.failure()?.errorText || ''}`);
    });
  }

  const unauthorizedCreate = await apiResult(baseURL, '/api/v1/web-netplay/sessions', {
    method: 'POST', body: JSON.stringify({edition_id: edition.id, locale: 'en', client_id: 'unauthorized', display_name: 'Unauthorized'})
  });
  if (unauthorizedCreate.status !== 401) throw new Error(`unauthenticated room creation was not rejected: ${JSON.stringify(unauthorizedCreate)}`);
  const malformedInvite = await apiResult(baseURL, '/api/v1/web-netplay/sessions/join', {
    method: 'POST', body: JSON.stringify({invite_code: 'not-an-invitation', edition_id: edition.id, locale: 'en', client_id: 'bad-invite', display_name: 'Bad invite'})
  });
  if (malformedInvite.status !== 400 || malformedInvite.body?.error?.code !== 'web_netplay_invite_invalid') {
    throw new Error(`malformed invitation contract failed: ${JSON.stringify(malformedInvite)}`);
  }
  const invalidPlayer = await apiResult(baseURL, '/play-netplay/not-a-signed-capability');
  if (invalidPlayer.status !== 401) throw new Error(`invalid player capability was not rejected: ${JSON.stringify(invalidPlayer)}`);

  const openNetplayDialog = async (page, selectedGameID, selectedEditionID) => {
    await page.goto(baseURL + '/?acceptance=web-netplay#library', {waitUntil: 'domcontentloaded'});
    await page.locator(`.game-detail[data-game="${selectedGameID}"]`).first().click();
    const action = page.locator(`[data-web-netplay="${selectedEditionID}"]`);
    await action.waitFor({state: 'visible'});
    await action.click();
    await page.locator('#web-netplay-dialog').waitFor({state: 'visible'});
  };

  await openNetplayDialog(hostPage, game.id, edition.id);
  await hostPage.locator('#web-netplay-form input[name="display_name"]').fill('Host');
  const [hostResponse] = await Promise.all([
    hostPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/web-netplay/sessions'),
    hostPage.locator('#web-netplay-form button[type="submit"]').click()
  ]);
  if (hostResponse.status() !== 201) throw new Error(`host UI create failed: ${hostResponse.status()} ${await hostResponse.text()}`);
  const host = await hostResponse.json();
  if (host.role !== 'host' || !host.invite_code) throw new Error(`host UI contract failed: ${JSON.stringify(host)}`);
  const visibleInvite = hostPage.locator('#web-player-invite-code');
  await visibleInvite.waitFor({state: 'visible'});
  if ((await visibleInvite.textContent()) !== host.invite_code) throw new Error('host invitation is not visibly recoverable from the player UI');

  const wrongContent = await apiResult(baseURL, '/api/v1/web-netplay/sessions/join', {
    method: 'POST', body: JSON.stringify({invite_code: host.invite_code, edition_id: differentEdition.id, locale: 'en', client_id: 'wrong-content', display_name: 'Wrong content'})
  });
  if (wrongContent.status !== 409 || wrongContent.body?.error?.code !== 'compatibility_mismatch') {
    throw new Error(`mismatched content was not rejected: ${JSON.stringify(wrongContent)}`);
  }
  const [sessionID] = host.invite_code.split('.');
  const invalidToken = await apiResult(baseURL, '/api/v1/web-netplay/sessions/join', {
    method: 'POST', body: JSON.stringify({invite_code: `${sessionID}.${'0'.repeat(64)}`, edition_id: edition.id, locale: 'en', client_id: 'wrong-token', display_name: 'Wrong token'})
  });
  if (invalidToken.status !== 401 || invalidToken.body?.error?.code !== 'invalid_invitation') {
    throw new Error(`wrong invitation secret was not rejected: ${JSON.stringify(invalidToken)}`);
  }

  await openNetplayDialog(guestPage, game.id, edition.id);
  await guestPage.locator('#web-netplay-form input[value="guest"]').check();
  await guestPage.locator('#web-netplay-form input[name="display_name"]').fill('Guest');
  await guestPage.locator('#web-netplay-form input[name="invite_code"]').fill(host.invite_code);
  const [guestResponse] = await Promise.all([
    guestPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/web-netplay/sessions/join'),
    guestPage.locator('#web-netplay-form button[type="submit"]').click()
  ]);
  if (guestResponse.status() !== 200) throw new Error(`guest UI join failed: ${guestResponse.status()} ${await guestResponse.text()}`);
  const guest = await guestResponse.json();
  if (guest.role !== 'guest' || guest.session.state !== 'ready') throw new Error(`guest UI contract failed: ${JSON.stringify(guest)}`);

  const thirdParticipant = await apiResult(baseURL, '/api/v1/web-netplay/sessions/join', {
    method: 'POST', body: JSON.stringify({invite_code: host.invite_code, edition_id: edition.id, locale: 'en', client_id: 'third-browser', display_name: 'Third'})
  });
  if (thirdParticipant.status !== 409 || thirdParticipant.body?.error?.code !== 'session_full') {
    throw new Error(`third participant was not rejected: ${JSON.stringify(thirdParticipant)}`);
  }
  const replayedGuest = await apiResult(baseURL, '/api/v1/web-netplay/sessions/join', {
    method: 'POST', body: JSON.stringify({invite_code: host.invite_code, edition_id: edition.id, locale: 'en', client_id: 'guest-browser', display_name: 'Guest'})
  });
  if (replayedGuest.status !== 200 || replayedGuest.body?.session?.participants?.length !== 2) {
    throw new Error(`idempotent guest retry failed: ${JSON.stringify(replayedGuest)}`);
  }

  const hostScope = hostPage.frameLocator('#web-player-frame');
  const guestScope = guestPage.frameLocator('#web-player-frame');
  try {
    await Promise.all([
      hostScope.locator('body[data-runtime-state="ready"]').waitFor({timeout: 30_000}),
      guestScope.locator('body[data-runtime-state="ready"]').waitFor({timeout: 30_000})
    ]);
  } catch (error) {
    const states = await Promise.all([hostScope, guestScope].map(scope => scope.locator('body').evaluate(body => ({
      runtime: body.dataset.runtimeState,
      netplay: body.dataset.netplayState,
      boot: body.querySelector('.boot span')?.textContent || '',
      emulator: !!body.ownerDocument.defaultView.EJS_emulator
    }))));
    throw new Error(`players did not become ready: states=${JSON.stringify(states)} failures=${JSON.stringify(pageFailures)}`, {cause: error});
  }
  await hostScope.locator('.ejs_start_button').click({timeout: 15_000});
  await hostScope.locator('body[data-runtime-state="started"]').waitFor({timeout: 60_000});
  await hostScope.locator('body[data-netplay-state="waiting"]').waitFor({timeout: 30_000});
  await guestScope.locator('.ejs_start_button').click({timeout: 15_000});
  await guestScope.locator('body[data-runtime-state="started"]').waitFor({timeout: 60_000});
  const [hostState, guestState] = await Promise.all([waitForNetplay(hostScope, 'host'), waitForNetplay(guestScope, 'guest')]);
  await Promise.all([
    hostPage.locator('#web-player-netplay[data-state="connected"]').waitFor({timeout: 10_000}),
    guestPage.locator('#web-player-netplay[data-state="connected"]').waitFor({timeout: 10_000}),
    guestPage.locator('#web-player-input-state[data-state="connected"]').waitFor({timeout: 10_000})
  ]);
  const mobileFrame = await guestPage.locator('.web-player-frame').boundingBox();
  if (!mobileFrame || mobileFrame.height < 500 || mobileFrame.width > 390) {
    throw new Error(`mobile netplay frame is not usable: ${JSON.stringify(mobileFrame)}`);
  }

  await hostScope.locator('body').evaluate(body => {
    const runtimeWindow = body.ownerDocument.defaultView;
    runtimeWindow.__varkivRemoteInputs = [];
    const manager = runtimeWindow.EJS_emulator.gameManager;
    const original = manager.functions.simulateInput;
    manager.functions.simulateInput = (...values) => {
      runtimeWindow.__varkivRemoteInputs.push(values);
      return original(...values);
    };
  });
  await guestScope.locator('body').evaluate(body => body.ownerDocument.defaultView.EJS_emulator.elements.parent.focus());
  await guestPage.keyboard.down('x');
  await guestPage.waitForTimeout(180);
  await guestPage.keyboard.up('x');
  await hostScope.locator('body').evaluate(body => new Promise((resolve, reject) => {
    const runtimeWindow = body.ownerDocument.defaultView;
    const deadline = Date.now() + 10_000;
    const poll = () => {
      if ((runtimeWindow.__varkivRemoteInputs || []).length >= 2) return resolve();
      if (Date.now() >= deadline) return reject(new Error('remote input deadline exceeded'));
      setTimeout(poll, 50);
    };
    poll();
  }));
  const remoteInputs = await hostScope.locator('body').evaluate(body => body.ownerDocument.defaultView.__varkivRemoteInputs);
  if (!remoteInputs.some(values => values[2] !== 0) || !remoteInputs.some(values => values[2] === 0)) {
    throw new Error(`guest input did not produce a press/release pair on host: ${JSON.stringify(remoteInputs)}`);
  }
  if (saveRequests.length) throw new Error(`netplay unexpectedly called persistent save API: ${saveRequests.join(', ')}`);
  if (pageFailures.length || nonOKResponses.length) throw new Error(`browser failures:\n${[...pageFailures, ...nonOKResponses].join('\n')}`);

  const saves = spawnSync('find', [stateRoot, '-type', 'f'], {encoding: 'utf8'}).stdout.trim().split('\n').filter(Boolean).filter(path => path.includes('/saves/'));
  if (saves.length) throw new Error(`netplay wrote persistent save files: ${saves.join(', ')}`);
  const report = {
    format: 'varkiv-web-netplay-acceptance-v1', application_version: health.version,
    emulatorjs_version: readiness.emulatorjs_version, signal_ready: readiness.signal_ready,
    runtime: readiness.runtime, content_sha256: fixture.sha256, session_state: guest.session.state,
    browsers: [hostState, guestState], remote_input_events: remoteInputs.length,
    remote_press_release: true, gamepad_api_detected: true, persistent_save_requests: saveRequests.length,
    ui_flow: {host_created: true, invitation_visible: true, guest_joined: true, mobile_frame_height: Math.round(mobileFrame.height)},
    failure_contracts: {unauthenticated_create: 401, malformed_invite: 400, invalid_secret: 401, content_mismatch: 409, third_participant: 409, guest_retry_idempotent: true, invalid_player_capability: 401},
    ignored_headless_warnings: headlessWarnings,
    persistent_save_files: saves.length, user_library_read: false, evidence_root: root
  };
  writeFileSync(join(root, 'report.json'), JSON.stringify(report, null, 2) + '\n', {mode: 0o600});
  console.log(JSON.stringify(report, null, 2));
  await hostContext.close();
  await guestContext.close();
} finally {
  if (browser) await browser.close().catch(() => {});
  await stopChild(server);
  if (keep && root) console.error(`retained_evidence=${root}`);
  else cleanupRoot();
}
