#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version_file="internal/buildinfo/VERSION"
test -f "$version_file"
version="$(tr -d '\r\n' < "$version_file")"

if [[ ! "$version" =~ ^0\.[0-9]+\.[0-9]+-preview\.[0-9]+$ ]]; then
  echo "invalid canonical preview version: $version" >&2
  exit 1
fi

check_equal() {
  local label="$1"
  local actual="$2"
  if [[ "$actual" != "$version" ]]; then
    echo "$label version drifted: got '$actual', want '$version'" >&2
    exit 1
  fi
}

package_version="$(sed -n 's/^[[:space:]]*"version": "\([^"]*\)",*$/\1/p' package.json | head -n 1)"
lock_root_version="$(sed -n 's/^[[:space:]]\{2\}"version": "\([^"]*\)",*$/\1/p' package-lock.json | head -n 1)"
lock_package_version="$(sed -n 's/^[[:space:]]\{6\}"version": "\([^"]*\)",*$/\1/p' package-lock.json | head -n 1)"
openapi_version="$(sed -n 's/^[[:space:]]*version: \(.*\)$/\1/p' internal/server/openapi.yaml | head -n 1)"
compose_version="$(sed -n 's/^[[:space:]]*image: "${VARKIV_IMAGE:-varkiv:\([^}]*\)}"$/\1/p' compose.yaml | head -n 1)"
synology_compose_version="$(sed -n 's/^[[:space:]]*image: "${VARKIV_IMAGE:-varkiv:\([^}]*\)}"$/\1/p' compose.synology.yaml | head -n 1)"

check_equal "package.json" "$package_version"
check_equal "package-lock root" "$lock_root_version"
check_equal "package-lock package" "$lock_package_version"
check_equal "OpenAPI" "$openapi_version"
check_equal "Compose image" "$compose_version"
check_equal "Synology Compose image" "$synology_compose_version"

grep -Fq 'rootProject.file("../../internal/buildinfo/VERSION").readText().trim()' clients/android/app/build.gradle.kts
test "$(grep -Fo "?v=$version" internal/server/web/index.html | wc -l | tr -d ' ')" = 5
grep -Fq "varkiv:$version restore-state" docs/DEPLOYMENT.md
grep -Fq "varkiv:$version db-check" docs/DEPLOYMENT.md
grep -Fq "（preview.${version##*.}）" docs/PLAN.md
grep -Fq './scripts/build-local.sh' README.md
grep -Fq './bin/varkiv version --json' README.md
grep -Fq './scripts/build-local.sh' docs/DEPLOYMENT.md
bash -n scripts/build-local.sh

echo "version_identity=passed version=$version"
