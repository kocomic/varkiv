#!/bin/sh
set -eu

usage() {
  printf '%s\n' 'usage: scripts/acceptance-compose-web-netplay.sh [--signal-port PORT]'
}

signal_port=18191
while [ "$#" -gt 0 ]; do
  case "$1" in
    --signal-port)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      signal_port=$2
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

case "$signal_port" in
  ''|*[!0-9]*) printf '%s\n' 'error: signal port must be numeric' >&2; exit 2 ;;
esac
[ "$signal_port" -ge 1024 ] && [ "$signal_port" -le 65535 ] || {
  printf '%s\n' 'error: signal port must be between 1024 and 65535' >&2
  exit 2
}

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
acceptance_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-netplay-compose.XXXXXX")
asset_root="$acceptance_root/emulatorjs-data"
container_name="varkiv-netplay-acceptance-$$"
image_name="varkiv-netplay-acceptance:$$"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker image rm -f "$image_name" >/dev/null 2>&1 || true
  if [ -d "$acceptance_root" ]; then
    find "$acceptance_root" -depth -delete
  fi
}
trap cleanup EXIT INT TERM

cd "$repository_root"
node scripts/fetch-web-netplay-assets.mjs --directory "$asset_root"
node scripts/verify-web-netplay-assets.mjs --directory "$asset_root" >/dev/null
docker build --quiet --tag "$image_name" deploy/netplay-server >/dev/null
docker run --detach --name "$container_name" --init --read-only --user 10002:10002 \
  --cap-drop ALL --security-opt no-new-privileges:true \
  --publish "127.0.0.1:${signal_port}:3000" "$image_name" >/dev/null

ready=false
attempt=0
while [ "$attempt" -lt 80 ]; do
  if curl --silent --show-error --fail "http://127.0.0.1:${signal_port}/games" >/dev/null 2>&1; then
    ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.25
done
[ "$ready" = true ] || {
  docker logs "$container_name" >&2 || true
  printf '%s\n' 'error: netplay signal container did not become ready' >&2
  exit 1
}

EMULATORJS_NETPLAY_DATA_PATH="$asset_root" \
  GAME_LIBRARY_TOKEN="compose-acceptance-only" \
  ROM_LIBRARY_PATH="$acceptance_root" \
  VARKIV_DATA_PATH="$acceptance_root" \
  VARKIV_IMAGE="varkiv:compose-acceptance" \
  VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY="$asset_root" \
  VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM="http://127.0.0.1:${signal_port}" \
  docker compose -f compose.yaml -f compose.web-netplay.yaml config --quiet

VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY="$asset_root" \
  VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM="http://127.0.0.1:${signal_port}" \
  node scripts/acceptance-web-netplay.mjs

printf '%s\n' 'web_netplay_compose_acceptance=passed'
