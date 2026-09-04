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
  './scripts/install-android-sdk.sh' \
  'platforms: linux/amd64,linux/arm64' \
  'provenance: mode=max' \
  'sbom: true' \
  'subject-digest: ${{ steps.container_push.outputs.digest }}' \
  'subject-path: '\''dist/*'\''' \
  './scripts/render-container-deployment.sh' \
  'version image already exists; refusing to replace immutable image' \
  'name: Prove anonymous ${{ matrix.platform }} image' \
  'name: Publish verified GitHub Release' \
  'fail-fast: false' \
  '- linux/amd64' \
  '- linux/arm64' \
  "if: matrix.platform == 'linux/arm64'" \
  '--platform '\''${{ matrix.platform }}'\''' \
  'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7' \
  'actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8' \
  'persist-credentials: false' \
  'sha256sum -c SHA256SUMS' \
  './scripts/verify-anonymous-container.sh' \
  'gh release create "$GITHUB_REF_NAME" dist/*' \
  '--prerelease' \
  '--latest'; do
  require_literal "$value"
done

prove_section="$(sed -n '/^  prove:/,/^  publish:/p' "$workflow")"
publish_section="$(sed -n '/^  publish:/,$p' "$workflow")"
if grep -Fq 'docker/login-action@' <<<"$prove_section"; then
  printf '%s\n' 'anonymous proof jobs must not log in to GHCR' >&2
  exit 1
fi
if grep -Eq '^[[:space:]]+packages:' <<<"$prove_section"; then
  printf '%s\n' 'anonymous proof jobs must not receive package permissions' >&2
  exit 1
fi
if grep -Fq 'docker run' <<<"$publish_section" || grep -Fq 'setup-qemu-action@' <<<"$publish_section"; then
  printf '%s\n' 'publication job must consume proof results instead of reusing a Docker daemon' >&2
  exit 1
fi
if ! grep -A3 '^    needs:' <<<"$publish_section" | grep -Fq -- '- prove'; then
  printf '%s\n' 'both anonymous architecture proofs must gate GitHub Release publication' >&2
  exit 1
fi

if grep -Eq 'run: sdkmanager([[:space:]]|$)' "$workflow"; then
  printf '%s\n' 'release workflow must use the bounded Android SDK installer' >&2
  exit 1
fi

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

printf '%s\n' 'release_workflow_contract=passed channels=preview,stable platforms=amd64,arm64 isolated_anonymous_jobs=true recoverable_assets=true immutable_tag=true'
