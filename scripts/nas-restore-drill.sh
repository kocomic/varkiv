#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
source "$repository_root/scripts/lib/nas-env.sh"

env_file="$repository_root/.env.nas"
with_web_emulator=false
backup_name=""
restore_name=""
project_name=""

usage() {
  printf '%s\n' 'usage: scripts/nas-restore-drill.sh --backup SAFE_NAME [--name SAFE_NAME] [--env-file FILE] [--project-name NAME] [--with-web-emulator]'
}

while (($# > 0)); do
  case "$1" in
    --env-file)
      (($# >= 2)) || { usage >&2; exit 2; }
      env_file=$2
      shift 2
      ;;
    --backup)
      (($# >= 2)) || { usage >&2; exit 2; }
      backup_name=$2
      shift 2
      ;;
    --name)
      (($# >= 2)) || { usage >&2; exit 2; }
      restore_name=$2
      shift 2
      ;;
    --project-name)
      (($# >= 2)) || { usage >&2; exit 2; }
      project_name=$2
      shift 2
      ;;
    --with-web-emulator)
      with_web_emulator=true
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

[[ "$backup_name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]] || {
  printf '%s\n' 'backup name is required and must use 1-96 safe filename characters' >&2
  exit 2
}
if [[ -z "$restore_name" ]]; then
  restore_name="restore-$backup_name"
fi
[[ "$restore_name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,103}$ ]] || {
  printf '%s\n' 'restore name must use 1-104 safe filename characters' >&2
  exit 2
}
if [[ -n "$project_name" && ! "$project_name" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]]; then
  printf '%s\n' 'project name must use lowercase letters, digits, underscore, or hyphen' >&2
  exit 2
fi

preflight=("$repository_root/scripts/nas-preflight.sh" --env-file "$env_file" --require-image-access)
compose=(docker compose --env-file "$env_file")
if [[ -n "$project_name" ]]; then
  compose+=(-p "$project_name")
fi
compose+=(-f "$repository_root/compose.yaml" -f "$repository_root/compose.nas.yaml")
if [[ "$with_web_emulator" == true ]]; then
  preflight+=(--with-web-emulator)
  compose+=(-f "$repository_root/compose.web-emulator.yaml")
fi
"${preflight[@]}" >/dev/null

backup_root=$(nas_env_value "$env_file" VARKIV_BACKUP_PATH)
restore_root=$(nas_env_value "$env_file" VARKIV_RESTORE_PATH)
data_root=$(nas_env_value "$env_file" VARKIV_DATA_PATH)
nas_require_absolute_directory VARKIV_BACKUP_PATH "$backup_root"
nas_require_absolute_directory VARKIV_RESTORE_PATH "$restore_root"
nas_require_absolute_directory VARKIV_DATA_PATH "$data_root"
backup_root=$(nas_physical_directory "$backup_root")
restore_root=$(nas_physical_directory "$restore_root")
data_root=$(nas_physical_directory "$data_root")
backup_source="$backup_root/$backup_name"
restore_target="$restore_root/$restore_name"

[[ -d "$backup_source" && ! -L "$backup_source" ]] || {
  printf '%s\n' 'backup source must be an existing non-symbolic-link directory' >&2
  exit 1
}
if [[ -e "$restore_target" || -L "$restore_target" ]]; then
  printf '%s\n' 'restore target already exists; refusing to overwrite it' >&2
  exit 1
fi

check_result=$("${compose[@]}" run --rm --no-deps \
  --volume "$data_root:/data:ro" \
  --volume "$backup_root:/backups:ro" \
  app check-state --from "/backups/$backup_name")
case "$check_result" in
  *'state_backup_valid=true'*) ;;
  *) printf '%s\n' 'source state backup did not pass validation' >&2; exit 1 ;;
esac

restore_result=$("${compose[@]}" run --rm --no-deps \
  --volume "$data_root:/data:ro" \
  --volume "$backup_root:/backups:ro" \
  --volume "$restore_root:/restore" \
  app restore-state --from "/backups/$backup_name" --out "/restore/$restore_name")
case "$restore_result" in
  *'state_restore_created=true'*) ;;
  *) printf '%s\n' 'restore command did not report a completed restore' >&2; exit 1 ;;
esac

database_check=$("${compose[@]}" run --rm --no-deps \
  --volume "$data_root:/data:ro" \
  --volume "$restore_root:/restore:ro" \
  app db-check --db "/restore/$restore_name/library.db")
case "$database_check" in
  *'integrity=ok foreign_keys=ok mode=read-only'*) ;;
  *) printf '%s\n' 'restored database did not pass read-only validation' >&2; exit 1 ;;
esac

printf 'nas_restore_drill=passed backup=%s restore=%s source_overwritten=false active_data_touched=false\n' "$backup_name" "$restore_name"
