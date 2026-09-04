#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="$(tr -d '\r\n' < internal/buildinfo/VERSION)"
output="$repo_root/bin/varkiv"
if [[ -L "$repo_root/bin" ]]; then
  echo "refusing to build through a symbolic-link bin directory" >&2
  exit 1
fi
mkdir -p "$repo_root/bin"
if [[ ! -d "$repo_root/bin" || -L "$repo_root/bin" ]]; then
  echo "bin must be a real directory inside the repository" >&2
  exit 1
fi
if [[ -e "$output" && (! -f "$output" || -L "$output") ]]; then
  echo "refusing to replace a non-regular or symbolic-link local binary" >&2
  exit 1
fi
temporary="$(mktemp "$repo_root/bin/.varkiv-new.XXXXXX")"
cleanup() {
  rm -f "$temporary"
}
trap cleanup EXIT

go build -trimpath -o "$temporary" ./cmd/varkiv
identity="$("$temporary" version --json)"
expected="{\"format\":\"varkiv-version-v1\",\"application_version\":\"$version\"}"
if [[ "$identity" != "$expected" ]]; then
  echo "local build version identity does not match the canonical version" >&2
  exit 1
fi

chmod 0755 "$temporary"
mv -f "$temporary" "$output"
trap - EXIT
echo "local_build=passed version=$version output=bin/varkiv"
