#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-release-reproducibility.sh

Build the five public desktop/handheld release archives twice and require
byte-for-byte identity, safe archive paths, non-overwrite behavior, private
staging-path redaction, and exact temporary-root cleanup. This command uses
repository source and generated build output only; it does not read a user
library, NAS mount, database, media, saves, signing key, or release secret.
EOF
}

if (($# > 0)); then
  if (($# == 1)) && [[ "$1" == "--help" || "$1" == "-h" ]]; then
    usage
    exit 0
  fi
  echo "error: unexpected arguments: $*" >&2
  usage >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
version="$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")"
temp_base="${TMPDIR:-/tmp}"
keep=false
if [ -n "${VARKIV_RELEASE_REPRODUCIBILITY_DIR:-}" ]; then
  review_root="$VARKIV_RELEASE_REPRODUCIBILITY_DIR"
  case "$review_root" in
    /*) ;;
    *)
      echo "error: VARKIV_RELEASE_REPRODUCIBILITY_DIR must be an absolute new directory" >&2
      exit 1
      ;;
  esac
  if [ -e "$review_root" ]; then
    echo "error: release reproducibility review directory already exists" >&2
    exit 1
  fi
  mkdir -m 0700 "$review_root"
  keep=true
else
  review_root="$(mktemp -d "${temp_base%/}/varkiv-release-reproducibility.XXXXXX")"
  case "$review_root" in
    "${temp_base%/}"/varkiv-release-reproducibility.*) ;;
    *)
      echo "error: unsafe release reproducibility root" >&2
      exit 1
      ;;
  esac
fi
cleanup() {
  if [ "$keep" = false ]; then
    rm -rf -- "$review_root"
  fi
}
trap cleanup EXIT

first="$review_root/first"
second="$review_root/second"
first_log="$review_root/first.log"
second_log="$review_root/second.log"
"$repo_root/scripts/build-release-archives.sh" "$version" "$first" > "$first_log"
sleep 2
"$repo_root/scripts/build-release-archives.sh" "$version" "$second" > "$second_log"

(cd "$first" && find . -type f -print | LC_ALL=C sort) > "$review_root/first-files.txt"
(cd "$second" && find . -type f -print | LC_ALL=C sort) > "$review_root/second-files.txt"
cmp "$review_root/first-files.txt" "$review_root/second-files.txt"
archive_count="$(wc -l < "$review_root/first-files.txt" | tr -d ' ')"
if [ "$archive_count" -ne 5 ]; then
  echo "error: reproducibility acceptance expected five archives" >&2
  exit 1
fi
while IFS= read -r relative; do
  cmp "$first/$relative" "$second/$relative"
done < "$review_root/first-files.txt"

windows_archive="$first/varkiv-${version}-windows-amd64.zip"
unzip -t "$windows_archive" >/dev/null
windows_prefix="varkiv-${version}-windows-amd64"
unzip -Z1 "$windows_archive" > "$review_root/windows-files.txt"
grep -Fx "$windows_prefix/varkiv.exe" "$review_root/windows-files.txt" >/dev/null
grep -Fx "$windows_prefix/LICENSE" "$review_root/windows-files.txt" >/dev/null
grep -Fx "$windows_prefix/THIRD_PARTY_NOTICES.md" "$review_root/windows-files.txt" >/dev/null
grep -Fx "$windows_prefix/THIRD_PARTY_LICENSES/THIRD_PARTY_NOTICES.md" "$review_root/windows-files.txt" >/dev/null

for label in linux-amd64 linux-arm64 linux-armv7; do
  prefix="varkiv-${version}-${label}"
  archive="$first/$prefix.tar.gz"
  listing="$review_root/$label-files.txt"
  gzip -t "$archive"
  tar -tzf "$archive" > "$listing"
  grep -Fx "$prefix/varkiv" "$listing" >/dev/null
  grep -Fx "$prefix/LICENSE" "$listing" >/dev/null
  grep -Fx "$prefix/THIRD_PARTY_NOTICES.md" "$listing" >/dev/null
  grep -Fx "$prefix/THIRD_PARTY_LICENSES/THIRD_PARTY_NOTICES.md" "$listing" >/dev/null
done

license_prefix="varkiv-${version}-third-party-licenses"
license_archive="$first/$license_prefix.tar.gz"
gzip -t "$license_archive"
tar -tzf "$license_archive" > "$review_root/license-files.txt"
grep -Fx "$license_prefix/THIRD_PARTY_NOTICES.md" "$review_root/license-files.txt" >/dev/null
grep -Fx "$license_prefix/third-party-inventory.lock.tsv" "$review_root/license-files.txt" >/dev/null

for listing in "$review_root"/*-files.txt; do
  while IFS= read -r entry; do
    case "$entry" in
      /*|../*|*/../*|*/..)
        echo "error: release archive contains an unsafe path" >&2
        exit 1
        ;;
    esac
  done < "$listing"
done

mkdir "$review_root/existing"
if "$repo_root/scripts/build-release-archives.sh" "$version" "$review_root/existing" >/dev/null 2>&1; then
  echo "error: release builder overwrote an existing output directory" >&2
  exit 1
fi
if rg -F "$review_root" "$first_log" "$second_log" >/dev/null; then
  echo "error: release builder output exposed its host staging path" >&2
  exit 1
fi

if [ "$keep" = true ]; then
  cleanup_state=retained
else
  cleanup_state=passed
fi
printf 'release_reproducibility=passed version=%s archives=%s builds=2 cleanup=%s\n' "$version" "$archive_count" "$cleanup_state"
