#!/usr/bin/env bash

set -euo pipefail

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
workflow="$repository_root/.github/workflows/release.yml"

[[ -f "$workflow" && ! -L "$workflow" ]] || {
  printf '%s\n' 'release workflow is unavailable' >&2
  exit 1
}

require_literal() {
  local value=$1
  grep -Fq -- "$value" "$workflow" || {
    printf 'release workflow is missing contract: %s\n' "$value" >&2
    exit 1
  }
}

for value in \
  "name: Publish release" \
  "- 'v*'" \
  'git merge-base --is-ancestor "$GITHUB_SHA" origin/main' \
  'release already exists; refusing to replace published assets' \
  'channel=preview' \
  'test "$PUBLIC_RELEASE_APPROVED" = true' \
  'test "$HARDWARE_RELEASE_APPROVED" = true' \
  "if: steps.release.outputs.channel == 'stable'" \
  'platforms: linux/amd64,linux/arm64' \
  'provenance: mode=max' \
  'sbom: true' \
  'subject-digest: ${{ steps.container_push.outputs.digest }}' \
  'subject-path: '\''dist/*'\''' \
  './scripts/render-container-deployment.sh' \
  'docker logout ghcr.io' \
  'docker run --rm --platform linux/amd64' \
  'docker run --rm --platform linux/arm64' \
  'gh release create "$GITHUB_REF_NAME" dist/*' \
  '--prerelease' \
  '--latest'; do
  require_literal "$value"
done

for secret_name in \
  ANDROID_RELEASE_KEYSTORE_B64 \
  ANDROID_KEYSTORE_PASSWORD \
  ANDROID_KEY_ALIAS \
  ANDROID_KEY_PASSWORD; do
  require_literal '${{ secrets.'"$secret_name"' }}'
done

if grep -Fq 'echo "tag=$image:latest"' "$workflow" || grep -Fq 'echo "tag=$image:preview"' "$workflow"; then
  printf '%s\n' 'release workflow must publish only the immutable version image tag' >&2
  exit 1
fi

printf '%s\n' 'release_workflow_contract=passed channels=preview,stable platforms=amd64,arm64 anonymous_pull=true immutable_tag=true'
