#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="${1:-$repo_root/dist/api-docs}"

if [[ -z "$output" || "$output" == "/" || "$output" == "$repo_root" || -e "$output" ]]; then
  echo "output must be a new, non-root path: $output" >&2
  exit 2
fi

output_parent="$(dirname "$output")"
mkdir -p "$output_parent"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-api-docs.XXXXXX")"
cleanup() {
  rm -rf -- "$temporary"
}
trap cleanup EXIT HUP INT TERM

cd "$repo_root"
export npm_config_audit=false
export npm_config_fund=false
export npm_config_update_notifier=false
npx --yes @redocly/cli@2.51.1 lint internal/server/openapi.yaml
npx --yes @redocly/cli@2.51.1 lint internal/server/multiplayer_openapi.yaml
npx --yes @redocly/cli@2.51.1 build-docs internal/server/openapi.yaml --output "$temporary/index.html"
npx --yes @redocly/cli@2.51.1 build-docs internal/server/multiplayer_openapi.yaml --output "$temporary/multiplayer.html"

test -s "$temporary/index.html"
test -s "$temporary/multiplayer.html"
mv "$temporary" "$output"
trap - EXIT HUP INT TERM

printf 'api_docs=passed output=%s files=2\n' "$output"
