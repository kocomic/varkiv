#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-target-packages.sh

Cross-compile and verify the eight private device-package fixtures. The output
is written to a new 0700 review root and retained for manual inspection. Set
VARKIV_TARGET_ACCEPTANCE_ROOT to choose an exact new output path.
EOF
}

while (($# > 0)); do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

version=$(tr -d '\r\n' < internal/buildinfo/VERSION)
if [ -n "${VARKIV_TARGET_ACCEPTANCE_ROOT:-}" ]; then
  acceptance_root=$VARKIV_TARGET_ACCEPTANCE_ROOT
  if [ -e "$acceptance_root" ]; then
    echo "error: VARKIV_TARGET_ACCEPTANCE_ROOT must name a new path" >&2
    exit 1
  fi
  mkdir -m 0700 "$acceptance_root"
else
  acceptance_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-target-packages.XXXXXX")
fi

bin_root="$acceptance_root/bin"
config_root="$acceptance_root/config"
package_root="$acceptance_root/packages"
mkdir -m 0700 "$bin_root" "$config_root" "$package_root"

host_binary="$bin_root/varkiv-host"
go build -trimpath -o "$host_binary" ./cmd/varkiv
test "$("$host_binary" version)" = "Varkiv $version"

build_target() {
  output=$1
  shift
  env "$@" go build -trimpath -o "$output" ./cmd/varkiv
  LC_ALL=C grep -aFq "$version" "$output"
}

build_target "$bin_root/varkiv-windows-amd64.exe" GOOS=windows GOARCH=amd64 CGO_ENABLED=0
build_target "$bin_root/varkiv-windows-arm64.exe" GOOS=windows GOARCH=arm64 CGO_ENABLED=0
build_target "$bin_root/varkiv-linux-amd64" GOOS=linux GOARCH=amd64 CGO_ENABLED=0
build_target "$bin_root/varkiv-linux-arm64" GOOS=linux GOARCH=arm64 CGO_ENABLED=0
build_target "$bin_root/varkiv-linux-armv7" GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0

fixture_token=fixture-target-package-token
fixture_origin=https://fixture.invalid

write_config() {
  kind=$1
  target=$2
  profile=$3
  config=$4
  root_dir=$5
  printf '{\n  "server_url": "%s",\n  "device_id": "fixture-%s",\n  "access_token": "%s",\n  "device_profile_id": "%s",\n  "device_target": "%s",\n  "root_dir": "%s"\n}\n' \
    "$fixture_origin" "$kind" "$fixture_token" "$profile" "$target" "$root_dir" > "$config"
  chmod 0600 "$config"
}

package_count=0
total_files=0

accept_package() {
  kind=$1
  target=$2
  profile=$3
  binary=$4
  root_dir=$5
  config="$config_root/$kind.json"
  package="$package_root/$kind"
  write_config "$kind" "$target" "$profile" "$config" "$root_dir"

  if [ "$kind" = windows-handheld ]; then
    "$host_binary" agent target-package \
      --kind "$kind" \
      --binary "$binary" \
      --config "$config" \
      --windows-user 'FIXTURE\Player' \
      --windows-install-dir 'C:\Users\Fixture\AppData\Local\Varkiv' \
      --out "$package"
  else
    "$host_binary" agent target-package \
      --kind "$kind" \
      --binary "$binary" \
      --config "$config" \
      --out "$package"
  fi

  verification=$("$host_binary" agent target-package verify --path "$package" --json)
  printf '%s' "$verification" | grep -Fq '"verified":true'
  printf '%s' "$verification" | grep -Fq "\"kind\":\"$kind\""

  manifest="$package/varkiv-target-manifest.json"
  guide="$package/HARDWARE-ACCEPTANCE.txt"
  test -s "$manifest"
  test -s "$guide"
  grep -Fq '"format_version": 1' "$manifest"
  grep -Fq '"sensitive": true' "$manifest"
  grep -Fq "\"kind\": \"$kind\"" "$manifest"

  for metadata in "$manifest" "$guide"; do
    for private_value in "$fixture_token" "$fixture_origin" "$acceptance_root" "fixture-$kind" "$profile"; do
      if grep -Fq "$private_value" "$metadata"; then
        echo "error: privacy-minimized target metadata contains private fixture data for $kind" >&2
        exit 1
      fi
    done
  done
  for private_value in "$fixture_token" "$fixture_origin" "$acceptance_root" "fixture-$kind" "$profile"; do
    if printf '%s' "$verification" | grep -Fq "$private_value"; then
      echo "error: target verifier output contains private fixture data for $kind" >&2
      exit 1
    fi
  done

  copied_binary=$(find "$package" -type f \( -name varkiv -o -name varkiv.exe \) -print -quit)
  test -n "$copied_binary"
  LC_ALL=C grep -aFq "$version" "$copied_binary"

  while IFS= read -r shell_script; do
    sh -n "$shell_script"
    if grep -Fq 'eval ' "$shell_script"; then
      echo "error: generated target script uses eval for $kind" >&2
      exit 1
    fi
  done < <(find "$package" -type f -name '*.sh' -print)

  file_count=$(find "$package" -type f | wc -l | tr -d ' ')
  package_count=$((package_count + 1))
  total_files=$((total_files + file_count))
}

accept_package windows-handheld windows builtin-device-windows-handheld "$bin_root/varkiv-windows-amd64.exe" 'C:\\Users\\Fixture\\VarkivData'
accept_package steamos-bazzite steamos-bazzite builtin-device-steamos-bazzite "$bin_root/varkiv-linux-amd64" /home/deck/.local/share/varkiv
accept_package rocknix rocknix builtin-device-rocknix "$bin_root/varkiv-linux-arm64" /storage/.config/varkiv
accept_package knulli knulli builtin-device-knulli "$bin_root/varkiv-linux-arm64" /userdata/system/configs/varkiv
accept_package darkos darkos builtin-device-darkos "$bin_root/varkiv-linux-arm64" /roms/tools/.varkiv
accept_package arkos arkos builtin-device-arkos "$bin_root/varkiv-linux-arm64" /roms/tools/.varkiv
accept_package muos muos builtin-device-muos "$bin_root/varkiv-linux-arm64" /mnt/mmc/MUOS/application/Varkiv
accept_package onionos onionos builtin-device-onionos "$bin_root/varkiv-linux-armv7" /mnt/SDCARD/App/Varkiv

test "$package_count" -eq 8
test "$(find "$bin_root" -maxdepth 1 -type f | wc -l | tr -d ' ')" -eq 6

printf 'target_package_acceptance=passed version=%s architectures=5 packages=%d files=%d\n' "$version" "$package_count" "$total_files"
printf 'review_root=%s\n' "$acceptance_root"
