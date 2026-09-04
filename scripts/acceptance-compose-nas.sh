#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

port=18186
image=""
skip_build=false
published_compose=false

usage() {
  printf '%s\n' 'usage: scripts/acceptance-compose-nas.sh [--image TAG --skip-build] [--published-compose] [--port PORT]'
}

while (($# > 0)); do
  case "$1" in
    --image)
      (($# >= 2)) || { usage >&2; exit 2; }
      image=$2
      shift 2
      ;;
    --port)
      (($# >= 2)) || { usage >&2; exit 2; }
      port=$2
      shift 2
      ;;
    --skip-build)
      skip_build=true
      shift
      ;;
    --published-compose)
      published_compose=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

[[ "$port" =~ ^[0-9]+$ ]] && ((port >= 1024 && port <= 65535)) || {
  printf '%s\n' 'port must be an integer between 1024 and 65535' >&2
  exit 2
}
if [[ "$skip_build" == true && -z "$image" ]]; then
  printf '%s\n' '--skip-build requires --image' >&2
  exit 2
fi

for command_name in curl docker openssl python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done

suffix="$(date +%s)-$$-$(openssl rand -hex 4)"
project_name="varkiv_nas_acceptance_${suffix//-/_}"
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-nas-compose.XXXXXX")
env_file="$fixture_root/nas.env"
created_image=false
stage=setup

report_error() {
  local task_status=$?
  local line_number=$1
  printf 'nas_compose_acceptance=error stage=%s line=%s status=%s\n' "$stage" "$line_number" "$task_status" >&2
  if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
    printf '::error title=NAS Compose acceptance::Failed at stage %s (line %s)\n' "$stage" "$line_number" >&2
  fi
  return "$task_status"
}
trap 'report_error "$LINENO"' ERR

if [[ -z "$image" ]]; then
  image="varkiv:nas-acceptance-$suffix"
  created_image=true
fi

compose=(docker compose --env-file "$env_file" -p "$project_name")
if [[ "$published_compose" == true ]]; then
  compose+=(-f "$repository_root/compose.ghcr.yaml")
else
  compose+=(-f "$repository_root/compose.yaml" -f "$repository_root/compose.nas.yaml")
fi

cleanup() {
  status=$?
  "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  if [[ "$created_image" == true ]]; then
    docker image rm "$image" >/dev/null 2>&1 || true
  fi
  case "$fixture_root" in
    "${TMPDIR:-/tmp}"/varkiv-nas-compose.*) rm -rf -- "$fixture_root" ;;
    *) printf '%s\n' 'error: refusing to clean an unexpected fixture path' >&2 ;;
  esac
  if ((status != 0)); then
    printf 'nas_compose_acceptance=failed stage=%s\n' "$stage" >&2
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
      printf '::error title=NAS Compose acceptance::Failed at stage %s\n' "$stage" >&2
    fi
  fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$fixture_root/data" "$fixture_root/backups" "$fixture_root/restore"
# Native Linux bind mounts enforce every parent directory's traversal bits.
# Keep the private fixture root unlistable while allowing uid 10001 to reach
# only the deliberately writable acceptance directories below it.
chmod 0711 "$fixture_root"
chmod 0777 "$fixture_root/data" "$fixture_root/backups" "$fixture_root/restore"
token=$(openssl rand -hex 32)
printf '%s\n' \
  "ROM_LIBRARY_PATH=$repository_root/testdata" \
  "VARKIV_DATA_PATH=$fixture_root/data" \
  "VARKIV_BACKUP_PATH=$fixture_root/backups" \
  "VARKIV_RESTORE_PATH=$fixture_root/restore" \
  "VARKIV_IMAGE=$image" \
  "GAME_LIBRARY_TOKEN=$token" \
  'VARKIV_BIND=127.0.0.1' \
  "VARKIV_PORT=$port" \
  'TZ=UTC' > "$env_file"

stage=image
if [[ "$skip_build" == true ]]; then
  docker image inspect "$image" >/dev/null
else
  docker build --pull -t "$image" .
fi

stage=preflight
./scripts/nas-preflight.sh --env-file "$env_file" --require-image-access >/dev/null

stage=compose-projection
if [[ "$published_compose" == true ]]; then
  test "$(grep -Fc 'create_host_path: false' compose.ghcr.yaml)" -eq 2
else
  test "$(grep -Fc 'create_host_path: false' compose.yaml)" -eq 1
  test "$(grep -Fc 'create_host_path: false' compose.nas.yaml)" -eq 1
  test "$(grep -Fc 'create_host_path: false' compose.synology.yaml)" -eq 2
fi
config_json="$fixture_root/compose.json"
"${compose[@]}" config --format json > "$config_json"
synology_config_json="$fixture_root/synology-compose.json"
if [[ "$published_compose" == true ]]; then
  printf '{}\n' > "$synology_config_json"
else
  docker compose --env-file "$env_file" -p "$project_name" -f "$repository_root/compose.synology.yaml" config --format json > "$synology_config_json"
fi
CONFIG_JSON="$config_json" SYNOLOGY_CONFIG_JSON="$synology_config_json" EXPECT_DATA="$fixture_root/data" EXPECT_LIBRARY="$repository_root/testdata" EXPECT_IMAGE="$image" EXPECT_PUBLISHED="$published_compose" python3 - <<'PY'
import json
import os

with open(os.environ['CONFIG_JSON'], encoding='utf-8') as handle:
    payload = json.load(handle)
app = payload['services']['app']
mounts = {item['target']: item for item in app['volumes']}
data = mounts['/data']
library = mounts['/library']
assert app['image'] == os.environ['EXPECT_IMAGE'], app['image']
assert data['type'] == 'bind' and data.get('read_only') is not True, data
assert os.path.realpath(data['source']) == os.path.realpath(os.environ['EXPECT_DATA']), data
assert data.get('bind', {}).get('create_host_path', False) is False, data
assert library['type'] == 'bind' and library.get('read_only') is True, library
assert os.path.realpath(library['source']) == os.path.realpath(os.environ['EXPECT_LIBRARY']), library
assert library.get('bind', {}).get('create_host_path', False) is False, library
if os.environ['EXPECT_PUBLISHED'] == 'true':
    assert 'build' not in app, app.get('build')
else:
    with open(os.environ['SYNOLOGY_CONFIG_JSON'], encoding='utf-8') as handle:
        synology_payload = json.load(handle)
    synology_app = synology_payload['services']['app']
    assert synology_app == app, {'standard': app, 'synology': synology_app}
    assert not synology_payload.get('volumes'), synology_payload.get('volumes')
PY

stage=start
"${compose[@]}" up -d --no-build app >/dev/null
container_id=$("${compose[@]}" ps -q app)
[[ -n "$container_id" ]]

wait_ready() {
  local attempt health
  for ((attempt = 0; attempt < 60; attempt++)); do
    health=$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}')
    if [[ "$health" == healthy ]] && curl --fail --silent --show-error --max-time 2 "http://127.0.0.1:$port/api/v1/health/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  docker logs "$container_id" >&2 || true
  return 1
}
wait_ready

stage=isolation
[[ "$(docker inspect "$container_id" --format '{{.Config.User}}')" == '10001:10001' ]]
[[ "$(docker inspect "$container_id" --format '{{.HostConfig.ReadonlyRootfs}}')" == true ]]
[[ "$(docker inspect "$container_id" --format '{{range .Mounts}}{{if eq .Destination "/library"}}{{.RW}}{{end}}{{end}}')" == false ]]
[[ "$(docker inspect "$container_id" --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.RW}}{{end}}{{end}}')" == true ]]
[[ "$(docker inspect "$container_id" --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Type}}{{end}}{{end}}')" == bind ]]

stage=authentication
[[ "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port/api/v1/games")" == 401 ]]
[[ "$(curl --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $token" "http://127.0.0.1:$port/api/v1/games")" == 200 ]]

stage=persistence-write
create_code=$(curl --silent --show-error --output "$fixture_root/create.json" --write-out '%{http_code}' \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data '{"id":"nas-acceptance-game","default_title":"NAS Acceptance","platform":"gba","titles":{"zh-CN":"NAS 验收"}}' \
  "http://127.0.0.1:$port/api/v1/games")
[[ "$create_code" == 201 ]]
[[ -s "$fixture_root/data/library.db" ]]

stage=restart-persistence
"${compose[@]}" restart app >/dev/null
wait_ready
[[ "$(curl --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $token" "http://127.0.0.1:$port/api/v1/games/nas-acceptance-game")" == 200 ]]

stage=backup
backup_output=$(./scripts/nas-backup.sh --env-file "$env_file" --project-name "$project_name" --name acceptance-backup)
case "$backup_output" in
  *'nas_backup=passed'*'validation=passed'*'existing_data_overwritten=false'*) ;;
  *) printf '%s\n' 'NAS backup helper did not report the expected result' >&2; exit 1 ;;
esac
wait_ready
docker run --rm --user 10001:10001 --entrypoint /bin/sh \
  --mount "type=bind,src=$fixture_root/backups,dst=/backups,readonly" \
  "$image" -c 'test -f "$1"' sh /backups/acceptance-backup/backup.json
[[ "$(curl --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $token" "http://127.0.0.1:$port/api/v1/games/nas-acceptance-game")" == 200 ]]

stage=restore-drill
before_restore=$(docker run --rm --entrypoint sha256sum --mount "type=bind,src=$fixture_root/data,dst=/source,readonly" "$image" /source/library.db | awk '{print $1}')
restore_output=$(./scripts/nas-restore-drill.sh --env-file "$env_file" --project-name "$project_name" --backup acceptance-backup --name acceptance-restored)
case "$restore_output" in
  *'nas_restore_drill=passed'*'source_overwritten=false'*'active_data_touched=false'*) ;;
  *) printf '%s\n' 'NAS restore drill did not report the expected result' >&2; exit 1 ;;
esac
after_restore=$(docker run --rm --entrypoint sha256sum --mount "type=bind,src=$fixture_root/data,dst=/source,readonly" "$image" /source/library.db | awk '{print $1}')
[[ "$before_restore" == "$after_restore" ]]
docker run --rm --user 10001:10001 --entrypoint /bin/sh \
  --mount "type=bind,src=$fixture_root/restore,dst=/restore,readonly" \
  "$image" -c 'test -f "$1"' sh /restore/acceptance-restored/library.db
[[ "$(curl --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $token" "http://127.0.0.1:$port/api/v1/games/nas-acceptance-game")" == 200 ]]

stage=complete
printf 'nas_compose_acceptance=passed image=%s deployment_mode=%s auth=401/200 host_bind=passed rom_read_only=true restart_persistence=passed backup_restore=passed active_data_unchanged_by_restore=true\n' \
  "$image" "$([[ "$published_compose" == true ]] && printf published || printf source)"
