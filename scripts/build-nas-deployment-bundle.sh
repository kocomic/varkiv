#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

version=$(tr -d '\r\n' < internal/buildinfo/VERSION)
output="$repository_root/dist/varkiv-nas-source-$version.tar.gz"

usage() {
  printf '%s\n' 'usage: scripts/build-nas-deployment-bundle.sh [--out NEW_TAR_GZ]'
}

while (($# > 0)); do
  case "$1" in
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

case "$output" in
  /*) ;;
  *) output="$repository_root/$output" ;;
esac
if [[ -e "$output" || -L "$output" ]]; then
  printf '%s\n' 'output already exists; refusing to overwrite it' >&2
  exit 1
fi

for command_name in git tar shasum; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done

git diff --quiet && git diff --cached --quiet || {
  printf '%s\n' 'tracked worktree changes must be committed before building a deployment bundle' >&2
  exit 1
}
./scripts/check-source-hygiene.sh >/dev/null

commit=$(git rev-parse --verify HEAD)
bundle_name="varkiv-nas-source-$version"
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-nas-bundle.XXXXXX")
bundle_root="$fixture_root/$bundle_name"

cleanup() {
  case "$fixture_root" in
    "${TMPDIR:-/tmp}"/varkiv-nas-bundle.*) rm -rf -- "$fixture_root" ;;
    *) printf '%s\n' 'error: refusing to clean an unexpected bundle fixture path' >&2 ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$bundle_root" "$(dirname -- "$output")"
git archive HEAD | tar -xf - -C "$bundle_root"
cp "$bundle_root/compose.synology.yaml" "$bundle_root/docker-compose.yml"
printf '%s\n' \
  'format=varkiv-nas-source-v1' \
  "application_version=$version" \
  "git_commit=$commit" \
  'compose_file=docker-compose.yml' \
  'private_environment_template=.env.nas.example' \
  'contains_credentials=false' \
  'contains_user_roms=false' > "$bundle_root/BUNDLE-MANIFEST.txt"

COPYFILE_DISABLE=1 tar -czf "$output" -C "$fixture_root" "$bundle_name"
archive_sha=$(shasum -a 256 "$output" | awk '{print $1}')
archive_bytes=$(wc -c < "$output" | tr -d '[:space:]')

printf 'nas_deployment_bundle=created version=%s commit=%s bytes=%s sha256=%s contains_credentials=false contains_user_roms=false\n' \
  "$version" "$commit" "$archive_bytes" "$archive_sha"
