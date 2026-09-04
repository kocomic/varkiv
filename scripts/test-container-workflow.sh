#!/usr/bin/env bash

set -euo pipefail

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
workflow="$repository_root/.github/workflows/container.yml"

[[ -f "$workflow" && ! -L "$workflow" ]] || {
  printf '%s\n' 'edge container workflow is unavailable' >&2
  exit 1
}

require_literal() {
  local value=$1
  grep -Fq -- "$value" "$workflow" || {
    printf 'edge container workflow is missing contract: %s\n' "$value" >&2
    exit 1
  }
}

for value in \
  'workflow_run:' \
  'group: container-edge-${{ github.event.workflow_run.event }}-${{ github.event.workflow_run.head_branch }}' \
  "github.event.workflow_run.conclusion == 'success'" \
  "github.event.workflow_run.event == 'push'" \
  "github.event.workflow_run.head_branch == 'main'" \
  'ref: ${{ github.event.workflow_run.head_sha }}' \
  'platforms: linux/amd64,linux/arm64' \
  'provenance: mode=max' \
  'sbom: true' \
  './scripts/render-container-deployment.sh' \
  'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7'; do
  require_literal "$value"
done

if grep -Eq '^[[:space:]]*group:[[:space:]]*container-edge[[:space:]]*$' "$workflow"; then
  printf '%s\n' 'edge container concurrency must be isolated by event and branch' >&2
  exit 1
fi

printf '%s\n' 'container_workflow_contract=passed main_push_only=true concurrency=event+branch platforms=amd64,arm64'
