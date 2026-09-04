#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
source "$repository_root/scripts/lib/nas-env.sh"

env_file="$repository_root/.env.nas"
with_web_emulator=false
backup_name="varkiv-state-$(date -u +%Y%m%dT%H%M%SZ)"
project_name=""

usage() {
  printf '%s\n' 'usage: scripts/nas-backup.sh [--env-file FILE] [--name SAFE_NAME] [--project-name NAME] [--with-web-emulator]'
}

while (($# > 0)); do
  case "$1" in
    --env-file)
      (($# >= 2)) || { usage >&2; exit 2; }
      env_file=$2
      shift 2
      ;;
    --name)
      (($# >= 2)) || { usage >&2; exit 2; }
      backup_name=$2
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
  printf '%s\n' 'backup name must be 1-96 safe filename characters' >&2
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
data_root=$(nas_env_value "$env_file" VARKIV_DATA_PATH)
nas_require_absolute_directory VARKIV_BACKUP_PATH "$backup_root"
nas_require_absolute_directory VARKIV_DATA_PATH "$data_root"
backup_root=$(nas_physical_directory "$backup_root")
data_root=$(nas_physical_directory "$data_root")
backup_target="$backup_root/$backup_name"
if [[ -e "$backup_target" || -L "$backup_target" ]]; then
  printf '%s\n' 'backup target already exists; refusing to overwrite it' >&2
  exit 1
fi

container_id=$("${compose[@]}" ps -a -q app)
[[ -n "$container_id" ]] || {
  printf '%s\n' 'application container does not exist; deploy it before creating an operational backup' >&2
  exit 1
}
was_running=$(docker inspect "$container_id" --format '{{.State.Running}}')
restart_required=false

restart_application() {
  status=$?
  if [[ "$restart_required" == true ]]; then
    "${compose[@]}" start app >/dev/null || {
      printf '%s\n' 'error: backup finished but the application could not be restarted' >&2
      return 1
    }
    restart_required=false
  fi
  return "$status"
}
trap restart_application EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ "$was_running" == true ]]; then
  restart_required=true
  "${compose[@]}" stop app >/dev/null
fi

backup_result=$("${compose[@]}" run --rm --no-deps \
  --volume "$data_root:/data:ro" \
  --volume "$backup_root:/backups" \
  app backup-state --db /data/library.db --state /data --out "/backups/$backup_name")
case "$backup_result" in
  *'state_backup_created=true'*) ;;
  *) printf '%s\n' 'backup command did not report a completed state backup' >&2; exit 1 ;;
esac

check_result=$("${compose[@]}" run --rm --no-deps \
  --volume "$data_root:/data:ro" \
  --volume "$backup_root:/backups:ro" \
  app check-state --from "/backups/$backup_name")
case "$check_result" in
  *'state_backup_valid=true'*) ;;
  *) printf '%s\n' 'new state backup did not pass validation' >&2; exit 1 ;;
esac

if [[ "$restart_required" == true ]]; then
  "${compose[@]}" start app >/dev/null
  restart_required=false
fi
trap - EXIT INT TERM

printf 'nas_backup=passed name=%s source_stopped=%s validation=passed existing_data_overwritten=false\n' "$backup_name" "$was_running"
