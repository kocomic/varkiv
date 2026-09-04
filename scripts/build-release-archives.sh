#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 VERSION NEW_OUTPUT_DIRECTORY" >&2
  exit 2
fi

version="$1"
output="$2"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: invalid release version" >&2
  exit 1
fi
case "$output" in
  ""|/|.|..)
    echo "error: unsafe release output directory" >&2
    exit 1
    ;;
esac
if [ -e "$output" ]; then
  echo "error: release output already exists" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
output_parent="$(dirname "$output")"
output_name="$(basename "$output")"
if [ ! -d "$output_parent" ]; then
  echo "error: release output parent does not exist" >&2
  exit 1
fi
output_parent="$(cd "$output_parent" && pwd)"
output_abs="$output_parent/$output_name"
case "$output_name" in
  ""|.|..)
    echo "error: unsafe release output name" >&2
    exit 1
    ;;
esac

stage="$(mktemp -d "$output_parent/.varkiv-release-build.XXXXXX")"
case "$stage" in
  "$output_parent"/.varkiv-release-build.*) ;;
  *)
    echo "error: unsafe release staging directory" >&2
    exit 1
    ;;
esac
cleanup() {
  rm -rf -- "$stage"
}
trap cleanup EXIT

dist="$stage/dist"
mkdir -m 0700 "$dist"
archive_tool="$stage/varkiv-release-archive"
go build -trimpath -ldflags="-s -w -buildid=" -o "$archive_tool" ./cmd/varkiv-release-archive

license_prefix="varkiv-${version}-third-party-licenses"
licenses="$stage/$license_prefix"
"$repo_root/scripts/collect-third-party-licenses.sh" "$licenses" >/dev/null

build_package() {
  goos="$1"
  goarch="$2"
  goarm="$3"
  label="$4"
  binary="$5"
  format="$6"
  package_prefix="varkiv-${version}-${label}"
  package="$stage/$package_prefix"
  mkdir -m 0700 "$package"
  if [ -n "$goarm" ]; then
    GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w -buildid=" -o "$package/$binary" ./cmd/varkiv
  else
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w -buildid=" -o "$package/$binary" ./cmd/varkiv
  fi
  cp "$repo_root/LICENSE" "$repo_root/docs/THIRD_PARTY_NOTICES.md" "$package/"
  cp -R "$licenses" "$package/THIRD_PARTY_LICENSES"
  "$archive_tool" \
    --source "$package" \
    --prefix "$package_prefix" \
    --out "$dist/$package_prefix.$format" \
    --format "$format" >/dev/null
}

build_package windows amd64 "" windows-amd64 varkiv.exe zip
build_package linux amd64 "" linux-amd64 varkiv tar.gz
build_package linux arm64 "" linux-arm64 varkiv tar.gz
build_package linux arm 7 linux-armv7 varkiv tar.gz
"$archive_tool" \
  --source "$licenses" \
  --prefix "$license_prefix" \
  --out "$dist/$license_prefix.tar.gz" \
  --format tar.gz >/dev/null

archive_count="$(find "$dist" -type f -maxdepth 1 | wc -l | tr -d ' ')"
if [ "$archive_count" -ne 5 ]; then
  echo "error: unexpected release archive count" >&2
  exit 1
fi
mv "$dist" "$output_abs"
printf 'release_archives=created version=%s archives=%s\n' "$version" "$archive_count"
