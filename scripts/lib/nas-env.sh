#!/bin/sh

nas_env_value() {
  nas_env_file=$1
  nas_env_key=$2
  nas_env_result=$(awk -v wanted="$nas_env_key" '
    /^[[:space:]]*#/ { next }
    {
      line=$0
      sub(/^[[:space:]]*/, "", line)
      key=line
      sub(/[[:space:]]*=.*/, "", key)
      if (key != wanted) next
      sub(/^[^=]*=/, "", line)
      sub(/^[[:space:]]*/, "", line)
      sub(/[[:space:]]*$/, "", line)
      value=line
    }
    END { if (value != "") print value }
  ' "$nas_env_file")
  case "$nas_env_result" in
    \"*\") nas_env_result=${nas_env_result#\"}; nas_env_result=${nas_env_result%\"} ;;
    \'*\') nas_env_result=${nas_env_result#\'}; nas_env_result=${nas_env_result%\'} ;;
  esac
  printf '%s' "$nas_env_result"
}

nas_require_absolute_directory() {
  nas_label=$1
  nas_path=$2
  case "$nas_path" in
    /*) ;;
    *) printf '%s must be an absolute path\n' "$nas_label" >&2; return 1 ;;
  esac
  if [ -L "$nas_path" ]; then
    printf '%s must not be a symbolic link\n' "$nas_label" >&2
    return 1
  fi
  if [ ! -d "$nas_path" ]; then
    printf '%s must name an existing directory\n' "$nas_label" >&2
    return 1
  fi
}

nas_physical_directory() {
  (CDPATH= cd -- "$1" && pwd -P)
}

nas_require_separate_trees() {
  nas_left_label=$1
  nas_left=$2
  nas_right_label=$3
  nas_right=$4
  if [ "$nas_left" = "$nas_right" ]; then
    printf '%s and %s must be different directories\n' "$nas_left_label" "$nas_right_label" >&2
    return 1
  fi
  case "$nas_left/" in
    "$nas_right/"*) printf '%s must not be inside %s\n' "$nas_left_label" "$nas_right_label" >&2; return 1 ;;
  esac
  case "$nas_right/" in
    "$nas_left/"*) printf '%s must not be inside %s\n' "$nas_right_label" "$nas_left_label" >&2; return 1 ;;
  esac
}
nas_reject_network_database_filesystem() {
  nas_data_path=$1
  if ! command -v findmnt >/dev/null 2>&1; then
    printf '%s\n' 'nas_preflight_warning=findmnt_unavailable_database_filesystem_not_verified' >&2
    return 0
  fi
  nas_fs_type=$(findmnt -T "$nas_data_path" -n -o FSTYPE 2>/dev/null || true)
  case "$nas_fs_type" in
    nfs|nfs4|cifs|smb3|sshfs|fuse.sshfs|9p)
      printf 'VARKIV_DATA_PATH uses unsupported network filesystem %s\n' "$nas_fs_type" >&2
      return 1
      ;;
    '')
      printf '%s\n' 'nas_preflight_warning=database_filesystem_unknown' >&2
      ;;
  esac
}
