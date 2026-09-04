#!/bin/sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 NEW_OUTPUT_DIRECTORY [--go-only]" >&2
  exit 2
fi

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=$1
mode=all
if [ "$#" -eq 2 ]; then
  if [ "$2" != --go-only ]; then
    echo "usage: $0 NEW_OUTPUT_DIRECTORY [--go-only]" >&2
    exit 2
  fi
  mode=go
fi
lock_file="$repo_root/docs/third-party-inventory.lock.tsv"

case "$output" in
  ""|/|.|..|"$repo_root")
    echo "refusing unsafe third-party license output: $output" >&2
    exit 1
    ;;
esac
if [ -e "$output" ]; then
  echo "third-party license output already exists: $output" >&2
  exit 1
fi

"$repo_root/scripts/check-third-party-notices.sh" "--$mode"
mkdir -p "$output/go"
cp "$repo_root/docs/THIRD_PARTY_NOTICES.md" "$output/"
cp "$lock_file" "$output/"
if [ "$mode" = all ]; then
  mkdir -p "$output/android"
  cp "$repo_root/docs/licenses/Apache-2.0.txt" "$output/android/Apache-2.0.txt"
fi

tab=$(printf '\t')
while IFS="$tab" read -r scope module version license; do
  [ "$scope" = go-runtime ] || continue
  module_dir=$(cd "$repo_root" && go list -m -f '{{.Dir}}' "$module")
  if [ -z "$module_dir" ] || [ ! -d "$module_dir" ]; then
    echo "Go module source directory unavailable: $module@$version" >&2
    exit 1
  fi
  safe_name=$(printf '%s@%s' "$module" "$version" | tr '/:' '__')
  destination="$output/go/$safe_name"
  mkdir -p "$destination"
  found=false
  find "$module_dir" -maxdepth 1 -type f \( -iname '*license*' -o -iname 'copying*' -o -iname 'notice*' \) -print | sort | while IFS= read -r license_file; do
    cp "$license_file" "$destination/"
    printf '.\n' > "$destination/.license-found"
  done
  if [ ! -f "$destination/.license-found" ]; then
    echo "no upstream license file found for $module@$version" >&2
    exit 1
  fi
  rm -f -- "$destination/.license-found"
done < "$lock_file"

echo "third_party_license_bundle=$output"
