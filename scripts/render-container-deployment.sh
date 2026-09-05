#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
template="$repository_root/compose.ghcr.yaml"
image=""
output=""

usage() {
  printf '%s\n' 'usage: scripts/render-container-deployment.sh --image REGISTRY_TAG_OR_DIGEST --out NEW_COMPOSE_FILE'
}

while (($# > 0)); do
  case "$1" in
    --image)
      (($# >= 2)) || { usage >&2; exit 2; }
      image=$2
      shift 2
      ;;
    --out)
      (($# >= 2)) || { usage >&2; exit 2; }
      output=$2
      shift 2
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

[[ "$image" =~ ^(ghcr\.io|docker\.io)/[a-z0-9._-]+/[a-z0-9._/-]+(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|@sha256:[0-9a-f]{64})$ ]] || {
  printf '%s\n' 'image must be a lowercase GHCR or Docker Hub tag or sha256 digest reference' >&2
  exit 2
}
[[ -n "$output" ]] || { usage >&2; exit 2; }
case "$output" in
  /*) ;;
  *) output="$PWD/$output" ;;
esac
output_parent=$(dirname -- "$output")
[[ -d "$output_parent" && ! -L "$output_parent" ]] || {
  printf '%s\n' 'output parent must be an existing non-symlink directory' >&2
  exit 1
}
[[ ! -e "$output" && ! -L "$output" ]] || {
  printf '%s\n' 'output already exists; refusing to overwrite it' >&2
  exit 1
}
[[ -f "$template" && ! -L "$template" ]] || {
  printf '%s\n' 'container Compose template is unavailable' >&2
  exit 1
}
if grep -Eq '^[[:space:]]+build:' "$template"; then
  printf '%s\n' 'container Compose template must not contain a build directive' >&2
  exit 1
fi

temporary=$(mktemp "$output_parent/.varkiv-compose.XXXXXX")
cleanup() {
  rm -f -- "$temporary"
}
trap cleanup EXIT

awk -v image="$image" '
  /^[[:space:]]+image: .*VARKIV_IMAGE/ {
    print "    image: \"" image "\""
    replacements++
    next
  }
  { print }
  END {
    if (replacements != 1) exit 42
  }
' "$template" > "$temporary" || {
  printf '%s\n' 'container Compose template did not contain exactly one image placeholder' >&2
  exit 1
}

chmod 0644 "$temporary"
mv -- "$temporary" "$output"
trap - EXIT
printf 'container_compose=created image=%s file=%s\n' "$image" "$(basename -- "$output")"
