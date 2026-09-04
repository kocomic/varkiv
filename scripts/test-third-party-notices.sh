#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-license-negative.XXXXXX")
cleanup() {
  rm -rf -- "$test_root"
}
trap cleanup EXIT HUP INT TERM

new_fixture() {
  name=$1
  root="$test_root/$name"
  mkdir -p "$root/scripts" "$root/docs/licenses"
  cp "$repo_root/scripts/check-third-party-notices.sh" "$root/scripts/"
  cp "$repo_root/package-lock.json" "$root/"
  cp "$repo_root/LICENSE" "$root/"
  cp "$repo_root/docs/THIRD_PARTY_NOTICES.md" "$root/docs/"
  cp "$repo_root/docs/third-party-inventory.lock.tsv" "$root/docs/"
  cp "$repo_root/docs/licenses/Apache-2.0.txt" "$root/docs/licenses/"
  printf '%s\n' "$root"
}

expect_rejected() {
  label=$1
  root=$2
  if "$root/scripts/check-third-party-notices.sh" --node >"$root/stdout" 2>"$root/stderr"; then
    echo "negative third-party test unexpectedly passed: $label" >&2
    exit 1
  fi
}

positive=$(new_fixture positive)
"$positive/scripts/check-third-party-notices.sh" --node >/dev/null

version_drift=$(new_fixture version-drift)
node - "$version_drift/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
lock.packages['node_modules/playwright-core'].version = '0.0.0-drift';
fs.writeFileSync(path, `${JSON.stringify(lock, null, 2)}\n`);
NODE
expect_rejected version-drift "$version_drift"

license_missing=$(new_fixture license-missing)
node - "$license_missing/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
delete lock.packages['node_modules/fsevents'].license;
fs.writeFileSync(path, `${JSON.stringify(lock, null, 2)}\n`);
NODE
expect_rejected license-missing "$license_missing"

package_added=$(new_fixture package-added)
node - "$package_added/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
lock.packages['node_modules/unreviewed-fixture'] = {version: '1.0.0', dev: true, license: 'MIT'};
fs.writeFileSync(path, `${JSON.stringify(lock, null, 2)}\n`);
NODE
expect_rejected package-added "$package_added"

notice_drift=$(new_fixture notice-drift)
sed 's/fsevents` | 2.3.2 | MIT/fsevents` | 2.3.2 | BSD-3-Clause/' \
  "$notice_drift/docs/THIRD_PARTY_NOTICES.md" > "$notice_drift/docs/THIRD_PARTY_NOTICES.changed"
mv "$notice_drift/docs/THIRD_PARTY_NOTICES.changed" "$notice_drift/docs/THIRD_PARTY_NOTICES.md"
expect_rejected notice-drift "$notice_drift"

echo "third_party_dependency_negative_tests=passed cases=4"
