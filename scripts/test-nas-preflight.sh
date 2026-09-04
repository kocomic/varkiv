#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-nas-preflight-test.XXXXXX")

cleanup() {
  case "$fixture_root" in
    "${TMPDIR:-/tmp}"/varkiv-nas-preflight-test.*) rm -rf -- "$fixture_root" ;;
    *) printf '%s\n' 'error: refusing to clean an unexpected fixture path' >&2 ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p \
  "$fixture_root/bin" \
  "$fixture_root/library" \
  "$fixture_root/data" \
  "$fixture_root/backups" \
  "$fixture_root/restore"

cat > "$fixture_root/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case "${1:-} ${2:-}" in
  'compose version'|'compose --env-file') exit 0 ;;
  *) printf 'unexpected fake docker invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat > "$fixture_root/bin/findmnt" <<'EOF'
#!/bin/sh
printf '%s\n' "${VARKIV_TEST_FILESYSTEM:-ext4}"
EOF
chmod 0700 "$fixture_root/bin/docker" "$fixture_root/bin/findmnt"

write_env() {
  target=$1
  token=$2
  data_path=$3
  backup_path=$4
  restore_path=$5
  printf '%s\n' \
    "ROM_LIBRARY_PATH=$fixture_root/library" \
    "VARKIV_DATA_PATH=$data_path" \
    "VARKIV_BACKUP_PATH=$backup_path" \
    "VARKIV_RESTORE_PATH=$restore_path" \
    "GAME_LIBRARY_TOKEN=$token" \
    'VARKIV_BIND=127.0.0.1' \
    'VARKIV_PORT=18086' > "$target"
}

strong_token=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
valid_env="$fixture_root/valid.env"
write_env "$valid_env" "$strong_token" "$fixture_root/data" "$fixture_root/backups" "$fixture_root/restore"

valid_output=$(PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$valid_env" 2>&1)
case "$valid_output" in
  *'nas_preflight=passed'*'rom_mount=read_only'*'data_mount=host_bind'*) ;;
  *) printf 'error: valid NAS environment did not pass\n%s\n' "$valid_output" >&2; exit 1 ;;
esac
case "$valid_output" in
  *"$strong_token"*) printf '%s\n' 'error: preflight leaked the deployment token' >&2; exit 1 ;;
esac

weak_env="$fixture_root/weak.env"
write_env "$weak_env" short "$fixture_root/data" "$fixture_root/backups" "$fixture_root/restore"
if PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$weak_env" >/dev/null 2>&1; then
  printf '%s\n' 'error: weak token was accepted' >&2
  exit 1
fi

nested="$fixture_root/data/backups"
mkdir -p "$nested"
nested_env="$fixture_root/nested.env"
write_env "$nested_env" "$strong_token" "$fixture_root/data" "$nested" "$fixture_root/restore"
if PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$nested_env" >/dev/null 2>&1; then
  printf '%s\n' 'error: nested data and backup trees were accepted' >&2
  exit 1
fi

rom_nested_backup="$fixture_root/library/backups"
mkdir -p "$rom_nested_backup"
rom_nested_env="$fixture_root/rom-nested.env"
write_env "$rom_nested_env" "$strong_token" "$fixture_root/data" "$rom_nested_backup" "$fixture_root/restore"
if PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$rom_nested_env" >/dev/null 2>&1; then
  printf '%s\n' 'error: backup tree nested inside the ROM library was accepted' >&2
  exit 1
fi

relative_env="$fixture_root/relative.env"
write_env "$relative_env" "$strong_token" relative/data "$fixture_root/backups" "$fixture_root/restore"
if PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$relative_env" >/dev/null 2>&1; then
  printf '%s\n' 'error: relative data path was accepted' >&2
  exit 1
fi

network_env="$fixture_root/network.env"
write_env "$network_env" "$strong_token" "$fixture_root/data" "$fixture_root/backups" "$fixture_root/restore"
if VARKIV_TEST_FILESYSTEM=nfs PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$network_env" >/dev/null 2>&1; then
  printf '%s\n' 'error: network filesystem for SQLite state was accepted' >&2
  exit 1
fi

ln -s "$valid_env" "$fixture_root/linked.env"
if PATH="$fixture_root/bin:$PATH" "$repository_root/scripts/nas-preflight.sh" --env-file "$fixture_root/linked.env" >/dev/null 2>&1; then
  printf '%s\n' 'error: symbolic-link environment file was accepted' >&2
  exit 1
fi

printf '%s\n' 'nas_preflight_tests=passed valid=1 rejected=weak-token,nested-path,rom-nested-backup,relative-path,network-filesystem,symlink-env token_leak=false'
