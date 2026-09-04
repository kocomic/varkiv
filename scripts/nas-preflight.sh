#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$repo_root/scripts/lib/nas-env.sh"

env_file="$repo_root/.env"
with_web_emulator=false
require_image_access=false

usage() {
  printf '%s\n' 'usage: scripts/nas-preflight.sh [--env-file FILE] [--with-web-emulator] [--require-image-access]'
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --env-file)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      env_file=$2
      shift 2
      ;;
    --with-web-emulator)
      with_web_emulator=true
      shift
      ;;
    --require-image-access)
      require_image_access=true
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

case "$env_file" in
  /*) ;;
  *) env_file=$(CDPATH= cd -- "$(dirname -- "$env_file")" && pwd)/$(basename -- "$env_file") ;;
esac
[ -f "$env_file" ] || { printf '%s\n' 'environment file does not exist' >&2; exit 1; }
[ ! -L "$env_file" ] || { printf '%s\n' 'environment file must not be a symbolic link' >&2; exit 1; }

rom_path=$(nas_env_value "$env_file" ROM_LIBRARY_PATH)
data_path=$(nas_env_value "$env_file" VARKIV_DATA_PATH)
backup_path=$(nas_env_value "$env_file" VARKIV_BACKUP_PATH)
restore_path=$(nas_env_value "$env_file" VARKIV_RESTORE_PATH)
token=$(nas_env_value "$env_file" GAME_LIBRARY_TOKEN)

nas_require_absolute_directory ROM_LIBRARY_PATH "$rom_path"
nas_require_absolute_directory VARKIV_DATA_PATH "$data_path"
nas_require_absolute_directory VARKIV_BACKUP_PATH "$backup_path"
nas_require_absolute_directory VARKIV_RESTORE_PATH "$restore_path"

rom_path=$(nas_physical_directory "$rom_path")
data_path=$(nas_physical_directory "$data_path")
backup_path=$(nas_physical_directory "$backup_path")
restore_path=$(nas_physical_directory "$restore_path")
nas_require_separate_trees ROM_LIBRARY_PATH "$rom_path" VARKIV_DATA_PATH "$data_path"
nas_require_separate_trees ROM_LIBRARY_PATH "$rom_path" VARKIV_BACKUP_PATH "$backup_path"
nas_require_separate_trees ROM_LIBRARY_PATH "$rom_path" VARKIV_RESTORE_PATH "$restore_path"
nas_require_separate_trees VARKIV_DATA_PATH "$data_path" VARKIV_BACKUP_PATH "$backup_path"
nas_require_separate_trees VARKIV_DATA_PATH "$data_path" VARKIV_RESTORE_PATH "$restore_path"
nas_require_separate_trees VARKIV_BACKUP_PATH "$backup_path" VARKIV_RESTORE_PATH "$restore_path"
nas_reject_network_database_filesystem "$data_path"

if [ "${#token}" -lt 32 ] || [ "$token" = replace-with-a-64-character-random-hex-secret ] || [ "$token" = replace-with-a-long-random-secret ]; then
  printf '%s\n' 'GAME_LIBRARY_TOKEN must be a non-placeholder secret of at least 32 characters' >&2
  exit 1
fi

command -v docker >/dev/null 2>&1 || { printf '%s\n' 'docker is required' >&2; exit 1; }
docker compose version >/dev/null

cd "$repo_root"
if [ "$with_web_emulator" = true ]; then
  emulator_path=$(nas_env_value "$env_file" EMULATORJS_DATA_PATH)
  nas_require_absolute_directory EMULATORJS_DATA_PATH "$emulator_path"
  emulator_path=$(nas_physical_directory "$emulator_path")
  docker compose --env-file "$env_file" -f compose.yaml -f compose.nas.yaml -f compose.web-emulator.yaml config --quiet
else
  docker compose --env-file "$env_file" -f compose.yaml -f compose.nas.yaml config --quiet
fi

if [ "$require_image_access" = true ]; then
  if [ "$with_web_emulator" = true ]; then
    image=$(docker compose --env-file "$env_file" -f compose.yaml -f compose.nas.yaml -f compose.web-emulator.yaml config --images | sed -n '1p')
  else
    image=$(docker compose --env-file "$env_file" -f compose.yaml -f compose.nas.yaml config --images | sed -n '1p')
  fi
  [ -n "$image" ] || { printf '%s\n' 'compose did not resolve an application image' >&2; exit 1; }
  docker image inspect "$image" >/dev/null 2>&1 || { printf '%s\n' 'build the resolved image before --require-image-access' >&2; exit 1; }
  set -- docker run --rm --network none --read-only --user 10001:10001 \
    --cap-drop ALL --security-opt no-new-privileges \
    --mount "type=bind,src=$rom_path,dst=/library,readonly" \
    --mount "type=bind,src=$data_path,dst=/data" \
    --mount "type=bind,src=$backup_path,dst=/backups" \
    --mount "type=bind,src=$restore_path,dst=/restore"
  if [ "$with_web_emulator" = true ]; then
    set -- "$@" --mount "type=bind,src=$emulator_path,dst=/opt/emulatorjs,readonly"
  fi
  set -- "$@" --entrypoint /bin/sh "$image" -ec
  if [ "$with_web_emulator" = true ]; then
    set -- "$@" 'test -r /library && test -x /library && test -r /data && test -w /data && test -r /backups && test -w /backups && test -r /restore && test -w /restore && test -r /opt/emulatorjs && test -x /opt/emulatorjs'
  else
    set -- "$@" 'test -r /library && test -x /library && test -r /data && test -w /data && test -r /backups && test -w /backups && test -r /restore && test -w /restore'
  fi
  "$@"
fi

printf '%s\n' 'nas_preflight=passed'
printf '%s\n' 'rom_mount=read_only'
printf '%s\n' 'data_mount=host_bind'
printf '%s\n' 'backup_and_restore_roots=separate'
