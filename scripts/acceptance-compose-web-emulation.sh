#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

version=$(tr -d '\r\n' < internal/buildinfo/VERSION)
image="varkiv:$version"
port=18084
skip_build=false

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-compose-web-emulation.sh [--image TAG] [--port PORT] [--skip-build]

Verify the checked-in Compose EmulatorJS overlay with an ephemeral download of
the 32 pinned EmulatorJS 4.2.3 assets. The script verifies every size and
SHA-256 before startup, uses a uniquely named project and data volume, and
removes those exact resources on success or failure. It never mounts a
production volume, user ROM library, NAS path, media, or saves.
EOF
}

while (($# > 0)); do
  case "$1" in
    --image)
      (($# >= 2)) || { echo "error: --image requires a value" >&2; exit 2; }
      image=$2
      shift 2
      ;;
    --port)
      (($# >= 2)) || { echo "error: --port requires a value" >&2; exit 2; }
      port=$2
      shift 2
      ;;
    --skip-build)
      skip_build=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ "$port" =~ ^[0-9]+$ ]] || { echo "error: --port must be an integer" >&2; exit 2; }
((port >= 1024 && port <= 65535)) || { echo "error: --port must be between 1024 and 65535" >&2; exit 2; }
[[ -n "$image" && "$image" != -* ]] || { echo "error: --image must be a non-empty Docker image reference" >&2; exit 2; }

for command_name in curl docker node openssl python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "error: required command is unavailable: $command_name" >&2
    exit 2
  }
done
docker compose version >/dev/null

if [ "$skip_build" = false ]; then
  docker build --pull -t "$image" .
else
  docker image inspect "$image" >/dev/null
fi
test "$(docker run --rm "$image" version)" = "Varkiv $version"

suffix="$(date +%s)-$$-$(openssl rand -hex 4)"
project="varkiv-web-acceptance-$suffix"
data_volume="varkiv-web-data-$suffix"
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-compose-web.XXXXXX")
asset_root="$fixture_root/assets"
override_file="$fixture_root/acceptance.override.yaml"
config_json="$fixture_root/compose.json"
loader_file="$asset_root/loader.js"
network="$project"_default
container_id=""
compose_started=false
stage=fixture-setup

cat > "$override_file" <<'YAML'
services:
  app:
    image: "${VARKIV_ACCEPTANCE_IMAGE:?Set VARKIV_ACCEPTANCE_IMAGE}"
volumes:
  data:
    name: "${VARKIV_ACCEPTANCE_DATA_VOLUME:?Set VARKIV_ACCEPTANCE_DATA_VOLUME}"
YAML

export EMULATORJS_DATA_PATH="$asset_root"
export GAME_LIBRARY_TOKEN="$(openssl rand -hex 32)"
export VARKIV_ACCEPTANCE_DATA_VOLUME="$data_volume"
export VARKIV_ACCEPTANCE_IMAGE="$image"
export VARKIV_BIND=127.0.0.1
export VARKIV_PORT="$port"
export ROM_LIBRARY_PATH="$repository_root/testdata"
export TZ=UTC

compose_command=(docker compose --project-name "$project" -f compose.yaml -f compose.web-emulator.yaml -f "$override_file")

cleanup_resources() {
  if [ "$compose_started" = true ]; then
    "${compose_command[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    compose_started=false
  fi
  if docker volume inspect "$data_volume" >/dev/null 2>&1; then
    docker volume rm "$data_volume" >/dev/null 2>&1 || true
  fi
  if docker network inspect "$network" >/dev/null 2>&1; then
    docker network rm "$network" >/dev/null 2>&1 || true
  fi
  if [ -d "$fixture_root" ]; then
    case "$(basename "$fixture_root")" in
      varkiv-compose-web.*) rm -r -- "$fixture_root" ;;
      *) echo "error: refusing to remove unexpected fixture path" >&2 ;;
    esac
  fi
}
report_and_cleanup() {
  status=$?
  cleanup_resources
  if ((status != 0)); then
    printf 'compose_web_emulation_acceptance=failed stage=%s\n' "$stage" >&2
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
      printf '::error title=Web emulator Compose acceptance::Failed at stage %s\n' "$stage" >&2
    fi
  fi
  return "$status"
}
trap report_and_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

stage=asset-download
node scripts/fetch-web-emulator-assets.mjs --directory "$asset_root"
node scripts/verify-web-emulator-assets.mjs --directory "$asset_root" >/dev/null

if docker volume inspect "$data_volume" >/dev/null 2>&1; then
  echo "error: generated Docker volume already exists" >&2
  exit 1
fi
if docker ps -a --filter "label=com.docker.compose.project=$project" --format '{{.ID}}' | grep -q .; then
  echo "error: generated Compose project already exists" >&2
  exit 1
fi

stage=compose-contract
test "$(grep -Fc 'create_host_path: false' compose.yaml)" -eq 1
test "$(grep -Fc 'create_host_path: false' compose.web-emulator.yaml)" -eq 1
"${compose_command[@]}" config --format json > "$config_json"
COMPOSE_CONFIG_JSON="$config_json" EXPECTED_ASSET_ROOT="$asset_root" EXPECTED_DATA_VOLUME="$data_volume" python3 - <<'PY'
import json
import os
from pathlib import Path

payload = json.loads(Path(os.environ["COMPOSE_CONFIG_JSON"]).read_text())
app = payload["services"]["app"]
environment = app["environment"]
assert environment["VARKIV_WEB_EMULATOR_ASSETS"] == "", environment
assert environment["VARKIV_WEB_EMULATOR_DIRECTORY"] == "/opt/emulatorjs", environment
mounts = {item["target"]: item for item in app["volumes"]}
assets = mounts["/opt/emulatorjs"]
assert assets["type"] == "bind", assets
assert Path(assets["source"]).resolve() == Path(os.environ["EXPECTED_ASSET_ROOT"]).resolve(), assets
assert assets["read_only"] is True, assets
assert assets.get("bind", {}).get("create_host_path", False) is False, assets
assert mounts["/library"]["read_only"] is True, mounts["/library"]
assert mounts["/library"].get("bind", {}).get("create_host_path", False) is False, mounts["/library"]
assert payload["volumes"]["data"]["name"] == os.environ["EXPECTED_DATA_VOLUME"], payload["volumes"]
PY

stage=compose-start
compose_started=true
"${compose_command[@]}" up -d --no-build
container_id=$("${compose_command[@]}" ps -q app)
test -n "$container_id"

stage=readiness
ready=false
attempt=0
while ((attempt < 30)); do
  if curl --fail --silent --show-error --max-time 2 "http://127.0.0.1:$port/api/v1/health/ready" >/dev/null 2>&1; then
    ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [ "$ready" != true ]; then
  "${compose_command[@]}" logs app >&2 || true
  echo "error: Compose web-emulation service did not become ready" >&2
  exit 1
fi

stage=web-emulation-contract
capabilities=$(curl --fail --silent --show-error \
  -H "Authorization: Bearer $GAME_LIBRARY_TOKEN" \
  "http://127.0.0.1:$port/api/v1/capabilities")
CAPABILITIES_JSON="$capabilities" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["CAPABILITIES_JSON"])
assert payload["features"]["web_emulation"] is True, payload["features"]
PY

readiness=$(curl --fail --silent --show-error \
  "http://127.0.0.1:$port/api/v1/web-emulation/readiness")
READINESS_JSON="$readiness" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["READINESS_JSON"])
assert payload["mode"] == "self-hosted-verified", payload
assert payload["same_origin"] is True, payload
assert payload["integrity_verified"] is True, payload
assert payload["emulatorjs_version"] == "4.2.3", payload
assert payload["assets_verified"] == 32, payload
assert payload["bytes_verified"] == 19102261, payload
assert len(payload["supported_platforms"]) == 11, payload
assert len(payload["supported_extensions"]) == 25, payload
capabilities = {item["platform_id"]: item for item in payload["platform_capabilities"]}
assert len(capabilities) == 11, capabilities
assert capabilities["n64"] == {
    "platform_id": "n64",
    "core": "mupen64plus_next",
    "extensions": [".n64", ".v64", ".z64"],
    "minimum_rom_bytes": 4096,
}, capabilities["n64"]
assert capabilities["ngpc"] == {
    "platform_id": "ngpc",
    "core": "mednafen_ngp",
    "extensions": [".ngc", ".ngp", ".ngpc", ".npc", ".zip"],
    "minimum_rom_bytes": 64,
}, capabilities["ngpc"]
serialized = os.environ["READINESS_JSON"].lower()
assert "path" not in serialized, serialized
assert "http" not in serialized, serialized
assert "cdn" not in serialized, serialized
PY

downloaded_loader="$fixture_root/downloaded-loader.js"
headers_file="$fixture_root/loader-headers.txt"
curl --fail --silent --show-error --dump-header "$headers_file" \
  --output "$downloaded_loader" "http://127.0.0.1:$port/emulatorjs/loader.js"
cmp "$loader_file" "$downloaded_loader"
grep -qi '^x-content-type-options: nosniff' "$headers_file"
test "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port/emulatorjs/")" != 200
test "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port/emulatorjs/compose.json")" != 200
test "$(find "$asset_root" -type f | wc -l | tr -d ' ')" = 32

stage=read-only-assets
test "$(docker inspect "$container_id" --format '{{range .Mounts}}{{if eq .Destination "/opt/emulatorjs"}}{{.RW}}{{end}}{{end}}')" = false
if docker exec "$container_id" sh -c ': > /opt/emulatorjs/acceptance-write-probe' >/dev/null 2>&1; then
  echo "error: EmulatorJS read-only mount accepted a write" >&2
  exit 1
fi
test ! -e "$fixture_root/acceptance-write-probe"

stage=cleanup
cleanup_resources
trap - EXIT
if docker ps -a --filter "label=com.docker.compose.project=$project" --format '{{.ID}}' | grep -q .; then
  echo "error: Compose acceptance left a container" >&2
  exit 1
fi
if docker volume inspect "$data_volume" >/dev/null 2>&1; then
  echo "error: Compose acceptance left its data volume" >&2
  exit 1
fi
if docker network inspect "$network" >/dev/null 2>&1; then
  echo "error: Compose acceptance left its network" >&2
  exit 1
fi
if curl --silent --max-time 1 "http://127.0.0.1:$port/api/v1/health" >/dev/null 2>&1; then
  echo "error: Compose acceptance left its listener" >&2
  exit 1
fi

printf 'compose_web_emulation_acceptance=passed version=%s image=%s read_only_assets=true cleanup=passed\n' "$version" "$image"
