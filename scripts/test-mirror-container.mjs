import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const script = fileURLToPath(new URL('./mirror-container.sh', import.meta.url));
const manifest = JSON.stringify({ manifests: [
  { platform: { os: 'linux', architecture: 'amd64' } },
  { platform: { os: 'linux', architecture: 'arm64' } },
  { platform: { os: 'unknown', architecture: 'unknown' }, annotations: { type: 'attestation-manifest' } },
] });
const digest = `sha256:${createHash('sha256').update(manifest).digest('hex')}`;
const source = `ghcr.io/example/varkiv@${digest}`;
const target = 'docker.io/example/varkiv';
const fakeSkopeo = `#!/usr/bin/env node
const fs = require('node:fs');
const p = require('node:path');
const args = process.argv.slice(2);
const root = process.env.FAKE_STATE;
fs.appendFileSync(p.join(root, 'calls'), JSON.stringify(args) + '\\n');
const manifest = fs.readFileSync(p.join(root, 'manifest'));
const ref = args.at(-1);
const destination = p.join(root, encodeURIComponent(ref));
if (args[0] === 'inspect') {
  if (ref.startsWith('docker://ghcr.io/')) {
    process.stdout.write(process.env.FAKE_SOURCE_MISMATCH ? 'wrong source' :
      ref.endsWith(':edge') && process.env.FAKE_STALE_EDGE ? 'newer image' : manifest);
  } else if (fs.existsSync(destination)) {
    process.stdout.write(fs.readFileSync(destination));
  } else {
    process.stderr.write(process.env.FAKE_AUTH_ERROR ? 'unauthorized: authentication required' : 'manifest unknown');
    process.exit(1);
  }
} else if (args[0] === 'copy') {
  if (!args.includes('--all') || !args.includes('--preserve-digests')) process.exit(90);
  if (process.env.FAKE_COPY_ERROR) process.exit(44);
  fs.writeFileSync(destination, process.env.FAKE_TARGET_MISMATCH ? 'rewritten manifest' : manifest);
} else process.exit(91);
`;

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'varkiv-mirror-test.'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = join(root, 'bin');
  mkdirSync(bin);
  writeFileSync(join(bin, 'skopeo'), fakeSkopeo, { mode: 0o700 });
  writeFileSync(join(root, 'manifest'), manifest);
  writeFileSync(join(root, 'calls'), '');
  return {
    run(tags = ['0.1.0-preview.5'], env = {}, image = source) {
      return spawnSync('bash', [script, '--source', image, '--target', target, ...tags.flatMap(tag => ['--tag', tag])], {
        encoding: 'utf8', env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, FAKE_STATE: root, ...env },
      });
    },
    existing(tag, content) { writeFileSync(join(root, encodeURIComponent(`docker://${target}:${tag}`)), content); },
    copies() { return readFileSync(join(root, 'calls'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse).filter(args => args[0] === 'copy'); },
  };
}

test('copies every manifest and verifies the exact digest', t => {
  const f = fixture(t);
  const r = f.run();
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /container_mirror=passed/);
  assert.equal(f.copies().length, 1);
});
test('retrying an identical immutable tag is idempotent', t => {
  const f = fixture(t);
  f.existing('0.1.0-preview.5', manifest);
  assert.equal(f.run().status, 0);
});
test('preflights all tags before writing any destination', t => {
  const f = fixture(t);
  f.existing('0.1.0-preview.5', 'previous release');
  assert.equal(f.run(['sha-0123456789ab', '0.1.0-preview.5']).status, 1);
  assert.equal(f.copies().length, 0);
});
test('an authentication failure cannot authorize replacement', t => {
  const f = fixture(t);
  assert.equal(f.run(undefined, { FAKE_AUTH_ERROR: '1' }).status, 1);
  assert.equal(f.copies().length, 0);
});
test('source mismatch fails before publication', t => {
  const f = fixture(t);
  assert.equal(f.run(undefined, { FAKE_SOURCE_MISMATCH: '1' }).status, 1);
  assert.equal(f.copies().length, 0);
});
test('copy and destination-digest failures do not report success', t => {
  for (const env of [{ FAKE_COPY_ERROR: '1' }, { FAKE_TARGET_MISMATCH: '1' }]) {
    const f = fixture(t);
    const r = f.run(undefined, env);
    assert.notEqual(r.status, 0);
    assert.doesNotMatch(r.stdout, /container_mirror=passed/);
  }
});
test('edge may advance while commit tags retain their identity', t => {
  const f = fixture(t);
  f.existing('edge', 'previous edge');
  assert.equal(f.run(['sha-0123456789ab', 'edge']).status, 0);
  assert.equal(f.copies().length, 2);
});
test('a stale run publishes its commit but does not move edge backwards', t => {
  const f = fixture(t);
  const r = f.run(['sha-0123456789ab', 'edge'], { FAKE_STALE_EDGE: '1' });
  assert.equal(r.status, 0);
  assert.match(r.stdout, /edge_skipped/);
  assert.equal(f.copies().length, 1);
});
test('rejects mutable sources, unsupported tags, and injected arguments', t => {
  const f = fixture(t);
  assert.equal(f.run(['latest']).status, 2);
  assert.equal(f.run(['edge\ninjected']).status, 2);
  assert.equal(f.run(undefined, {}, 'ghcr.io/example/varkiv:edge').status, 2);
  assert.equal(f.copies().length, 0);
});
