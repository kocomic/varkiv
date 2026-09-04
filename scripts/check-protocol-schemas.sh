#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

export npm_config_audit=false
export npm_config_fund=false
export npm_config_update_notifier=false

ajv=(npx --yes ajv-cli@5.0.0)
ajv_with_formats=(npx --yes --package=ajv-cli@5.0.0 --package=ajv-formats@3.0.1 ajv)

node <<'NODE'
const crypto = require('node:crypto');
const fs = require('node:fs');
const manifest = JSON.parse(fs.readFileSync('schemas/examples/hashpack-manifest-v1.json', 'utf8'));
const record = JSON.parse(fs.readFileSync('schemas/examples/hashpack-record-v1.json', 'utf8'));
const recordsSHA256 = crypto.createHash('sha256').update(`${JSON.stringify(record)}\n`).digest('hex');
const packID = crypto.createHash('sha256').update([
  'varkiv-hashpack-v1',
  manifest.source.id,
  manifest.source.name,
  manifest.source.publisher ?? '',
  manifest.source.license,
  manifest.release,
  recordsSHA256,
].join('\0')).digest('hex');
if (manifest.record_count !== 1 || manifest.records_sha256 !== recordsSHA256 || manifest.pack_id !== packID) {
  throw new Error('HashPack examples do not have a consistent record digest and pack identity');
}
NODE

"${ajv_with_formats[@]}" validate --spec=draft2020 -c ajv-formats \
  -s schemas/hashpack-manifest-v1.schema.json \
  -d schemas/examples/hashpack-manifest-v1.json
"${ajv[@]}" validate --spec=draft2020 \
  -s schemas/hashpack-record-v1.schema.json \
  -d schemas/examples/hashpack-record-v1.json
"${ajv[@]}" validate --spec=draft2020 \
  -s schemas/library-manifest-v6.schema.json \
  -d testdata/portable-v6/library-manifest.json \
  -d testdata/portable-runtime-v2/library-manifest.json \
  -d testdata/portable-standalone-v2/library-manifest.json \
  -d testdata/runtime-batch-library-manifest.json
"${ajv[@]}" validate --spec=draft2020 \
  -s schemas/varkiv-launches-v2.schema.json \
  -d testdata/portable-runtime-v2/varkiv-launches.json \
  -d testdata/portable-standalone-v2/varkiv-launches.json \
  -d testdata/varkiv-launches.json

printf 'protocol_schemas=passed schemas=4 fixtures=9\n'
