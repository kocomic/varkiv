import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, realpathSync } from 'node:fs';
import { isAbsolute, join, relative, sep } from 'node:path';

export const sha256 = value => createHash('sha256').update(value).digest('hex');

const safeManifestPath = value => {
  if (typeof value !== 'string' || !value || isAbsolute(value) || value.includes('\\') || value.includes('\0')) return false;
  const segments = value.split('/');
  return segments.every(segment => segment && segment !== '.' && segment !== '..');
};

const inside = (root, candidate) => {
  const path = relative(root, candidate);
  return path === '' || (path !== '..' && !path.startsWith(`..${sep}`) && !isAbsolute(path));
};

export function verifyWebEmulatorAssets(assetRoot, manifest) {
  if (!isAbsolute(assetRoot)) throw new Error('EmulatorJS directory must be absolute');
  let rootInfo;
  let root;
  try {
    rootInfo = lstatSync(assetRoot);
    root = realpathSync(assetRoot);
  } catch {
    throw new Error('EmulatorJS directory is missing or unreadable');
  }
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('EmulatorJS directory must be a real directory');
  const assets = manifest?.emulatorjs?.assets;
  const version = manifest?.emulatorjs?.version;
  if (typeof version !== 'string' || !version || !Array.isArray(assets) || !assets.length) {
    throw new Error('EmulatorJS asset manifest is invalid');
  }

  let bytesVerified = 0;
  const seen = new Set();
  for (const asset of assets) {
    if (!safeManifestPath(asset?.path) || seen.has(asset.path)) throw new Error('EmulatorJS asset manifest path is invalid');
    if (!Number.isSafeInteger(asset.size) || asset.size < 1 || !/^[0-9a-f]{64}$/.test(asset.sha256 || '')) {
      throw new Error(`EmulatorJS ${asset.path} manifest identity is invalid`);
    }
    seen.add(asset.path);

    let current = root;
    let finalInfo;
    for (const segment of asset.path.split('/')) {
      current = join(current, segment);
      let info;
      try {
        info = lstatSync(current);
      } catch {
        throw new Error(`EmulatorJS ${asset.path} is missing or unreadable`);
      }
      if (info.isSymbolicLink()) throw new Error(`EmulatorJS ${asset.path} must not use symbolic links`);
      finalInfo = info;
    }
    if (!finalInfo?.isFile()) throw new Error(`EmulatorJS ${asset.path} must be a regular file`);
    if (finalInfo.size !== asset.size) throw new Error(`EmulatorJS ${asset.path} size drifted`);
    let resolved;
    let bytes;
    try {
      resolved = realpathSync(current);
      bytes = readFileSync(resolved);
    } catch {
      throw new Error(`EmulatorJS ${asset.path} is missing or unreadable`);
    }
    if (!inside(root, resolved)) throw new Error(`EmulatorJS ${asset.path} escaped its directory`);
    if (sha256(bytes) !== asset.sha256) throw new Error(`EmulatorJS ${asset.path} SHA-256 drifted`);
    bytesVerified += bytes.byteLength;
  }

  return { version, assetsVerified: assets.length, bytesVerified };
}
