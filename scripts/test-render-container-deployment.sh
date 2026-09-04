#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
renderer="$repository_root/scripts/render-container-deployment.sh"
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-container-compose.XXXXXX")
stage=requirements

report_failure() {
  status=$?
  if ((status != 0)); then
    printf 'container_compose_renderer_tests=failed stage=%s\n' "$stage" >&2
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
      printf '::error title=Container Compose renderer::Failed at stage %s\n' "$stage" >&2
    fi
  fi
  return "$status"
}

for command_name in docker python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done
docker compose version >/dev/null

cleanup() {
  case "$fixture_root" in
    "${TMPDIR:-/tmp}"/varkiv-container-compose.*) rm -rf -- "$fixture_root" ;;
    *) printf '%s\n' 'error: refusing to clean unexpected fixture path' >&2 ;;
  esac
}
trap 'report_failure; cleanup' EXIT

tag_image='ghcr.io/example/varkiv:edge'
digest_image='ghcr.io/example/varkiv@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'

stage=tag-render
"$renderer" --image "$tag_image" --out "$fixture_root/tag.yaml" >/dev/null
grep -Fq "image: \"$tag_image\"" "$fixture_root/tag.yaml"
! grep -Fq 'VARKIV_IMAGE' "$fixture_root/tag.yaml"
! grep -Eq '^[[:space:]]+build:' "$fixture_root/tag.yaml"

stage=digest-render
"$renderer" --image "$digest_image" --out "$fixture_root/digest.yaml" >/dev/null
grep -Fq "image: \"$digest_image\"" "$fixture_root/digest.yaml"

stage=compose-config
mkdir "$fixture_root/data" "$fixture_root/roms"
GAME_LIBRARY_TOKEN=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
VARKIV_DATA_PATH="$fixture_root/data" \
ROM_LIBRARY_PATH="$fixture_root/roms" \
VARKIV_BIND=127.0.0.1 \
VARKIV_PORT=18080 \
  docker compose -f "$fixture_root/digest.yaml" config --format json > "$fixture_root/config.json"
CONFIG_JSON="$fixture_root/config.json" EXPECT_IMAGE="$digest_image" python3 - <<'PY'
import json
import os

with open(os.environ["CONFIG_JSON"], encoding="utf-8") as handle:
    payload = json.load(handle)
app = payload["services"]["app"]
assert app["image"] == os.environ["EXPECT_IMAGE"], app["image"]
assert "build" not in app, app.get("build")
assert app["read_only"] is True, app["read_only"]
assert app["user"] == "10001:10001", app["user"]
assert app["cap_drop"] == ["ALL"], app["cap_drop"]
mounts = {item["target"]: item for item in app["volumes"]}
assert mounts["/data"]["type"] == "bind" and not mounts["/data"].get("read_only", False), mounts["/data"]
assert mounts["/library"]["type"] == "bind" and mounts["/library"]["read_only"] is True, mounts["/library"]
assert mounts["/data"]["bind"]["create_host_path"] is False, mounts["/data"]
assert mounts["/library"]["bind"]["create_host_path"] is False, mounts["/library"]
PY

stage=negative-cases
if "$renderer" --image "$tag_image" --out "$fixture_root/tag.yaml" >/dev/null 2>&1; then
  printf '%s\n' 'renderer overwrote an existing deployment file' >&2
  exit 1
fi
if "$renderer" --image 'ghcr.io/Example/varkiv:edge' --out "$fixture_root/uppercase.yaml" >/dev/null 2>&1; then
  printf '%s\n' 'renderer accepted an uppercase registry path' >&2
  exit 1
fi
if "$renderer" --image $'ghcr.io/example/varkiv:edge\nservices: {}' --out "$fixture_root/injection.yaml" >/dev/null 2>&1; then
  printf '%s\n' 'renderer accepted a newline injection' >&2
  exit 1
fi

stage=complete
printf '%s\n' 'container_compose_renderer_tests=passed positive_cases=3 negative_cases=3'
