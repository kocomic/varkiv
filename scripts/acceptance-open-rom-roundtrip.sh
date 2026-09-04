#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-open-rom-roundtrip.sh

Download one size/SHA-256 pinned MIT GBA fixture, import it through Pegasus,
review its RetroArch/mGBA binding, export an Android Pegasus package, and prove
fresh-database reimport plus byte-identical re-export in a new 0700 root. No
user library, NAS mount, production database, media, save, or secret is read.
EOF
}

if [ "$#" -gt 0 ]; then
  if [ "$#" -eq 1 ] && { [ "$1" = "--help" ] || [ "$1" = "-h" ]; }; then
    usage
    exit 0
  fi
  echo "error: unexpected arguments: $*" >&2
  usage >&2
  exit 2
fi

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
requested_root=${VARKIV_OPEN_ROM_ACCEPTANCE_DIR:-}

for required_tool in curl jq go; do
  if ! command -v "$required_tool" >/dev/null 2>&1; then
    echo "Required acceptance tool is unavailable: $required_tool" >&2
    exit 2
  fi
done

if [ -n "$requested_root" ]; then
  case "$requested_root" in
    /*) ;;
    *) echo "VARKIV_OPEN_ROM_ACCEPTANCE_DIR must be an absolute, new path" >&2; exit 2 ;;
  esac
  if [ -e "$requested_root" ]; then
    echo "Refusing to reuse or overwrite VARKIV_OPEN_ROM_ACCEPTANCE_DIR" >&2
    exit 2
  fi
  mkdir -m 700 "$requested_root"
  acceptance_root=$requested_root
else
  acceptance_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-open-rom-roundtrip.XXXXXX")
  chmod 700 "$acceptance_root"
fi

rom_url='https://raw.githubusercontent.com/jsmolka/gba-tests/a7113b67e63f83a9b321696ddd7042ccfad6c881/ppu/hello.gba'
rom_size=1300
rom_sha256='38aed48b67bc0f701e8aa222b0c3334bd306bd29888707bb7224d81f5576c264'
license_url='https://raw.githubusercontent.com/jsmolka/gba-tests/a7113b67e63f83a9b321696ddd7042ccfad6c881/LICENSE'
license_size=1070
license_sha256='b59a9ed235d76752c977f04116bc72155a14af48176bdbf50d7e5c29fca35aa7'

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

verify_file() {
  actual_size=$(wc -c < "$1" | tr -d ' ')
  actual_sha256=$(sha256_file "$1")
  if [ "$actual_size" != "$2" ] || [ "$actual_sha256" != "$3" ]; then
    echo "Downloaded public fixture failed its fixed size/SHA-256 check" >&2
    exit 1
  fi
}

compare_files() {
  if ! cmp -s "$1" "$2"; then
    echo "Open-ROM round-trip byte comparison failed" >&2
    exit 1
  fi
}

library_root="$acceptance_root/source-library"
platform_root="$library_root/gba"
mkdir -m 700 -p "$platform_root"
rom_path="$platform_root/hello.gba"
license_path="$acceptance_root/LICENSE.gba-tests"

curl --fail --silent --show-error --proto '=https' --max-redirs 0 --retry 3 --output "$rom_path" "$rom_url"
curl --fail --silent --show-error --proto '=https' --max-redirs 0 --retry 3 --output "$license_path" "$license_url"
chmod 600 "$rom_path" "$license_path"
verify_file "$rom_path" "$rom_size" "$rom_sha256"
verify_file "$license_path" "$license_size" "$license_sha256"

metadata_path="$platform_root/metadata.pegasus.txt"
cat > "$metadata_path" <<'EOF'
collection: Game Boy Advance
shortname: gba

game: GBA Tests Hello
file: hello.gba
x-varkiv-game-id: open-rom-gba-hello-game
x-varkiv-edition-id: open-rom-gba-hello-edition
x-varkiv-game-title: GBA Tests Hello
x-varkiv-edition-title: a7113b67
x-varkiv-edition-type: homebrew
x-varkiv-version: a7113b67
x-varkiv-languages: en
EOF
chmod 600 "$metadata_path"

launch_sidecar="$library_root/varkiv-launches.json"
cat > "$launch_sidecar" <<'EOF'
{
  "format_version": 2,
  "device_profile_id": "builtin-device-android-handheld",
  "frontend_adapter_id": "builtin-frontend-pegasus",
  "bindings": [
    {
      "edition_id": "open-rom-gba-hello-edition",
      "platform_id": "gba",
      "rom_path": "gba/hello.gba",
      "binding": {
        "device_profile_id": "builtin-device-android-handheld",
        "frontend_adapter_id": "builtin-frontend-pegasus",
        "driver_id": "builtin-driver-retroarch",
        "core_id": "builtin-core-mgba",
        "arguments": ["-L", "{{core.library}}", "{{rom.path}}"]
      }
    }
  ]
}
EOF
chmod 600 "$launch_sidecar"

binary="$acceptance_root/varkiv"
cd "$project_root"
go build -trimpath -o "$binary" ./cmd/varkiv

import_result=$("$binary" import-pegasus \
  --db "$acceptance_root/source.db" \
  --library "$library_root" \
  --source "$metadata_path" \
  --platform gba \
  --locale en)
if [ "$import_result" != 'parsed=1 imported=1 skipped=0' ]; then
  echo "Unexpected open-ROM Pegasus import result: $import_result" >&2
  exit 1
fi

hint_json=$("$binary" runtime-hints list \
  --db "$acceptance_root/source.db" \
  --edition open-rom-gba-hello-edition \
  --status pending \
  --json)
if ! printf '%s' "$hint_json" | jq -e '
  length == 1 and
  .[0].source_kind == "structured-sidecar" and
  .[0].source_format == "varkiv-launches-v2" and
  .[0].device_profile_id == "builtin-device-android-handheld" and
  .[0].driver_id == "builtin-driver-retroarch" and
  .[0].core_id == "builtin-core-mgba"
' >/dev/null; then
  echo "Open-ROM import did not preserve the reviewed Android runtime hint" >&2
  exit 1
fi
hint_id=$(printf '%s' "$hint_json" | jq -r '.[0].id')
"$binary" runtime-hints apply --db "$acceptance_root/source.db" --id "$hint_id" >/dev/null

build_result=$("$binary" build-pack \
  --db "$acceptance_root/source.db" \
  --library "$library_root" \
  --out "$acceptance_root/android-pegasus" \
  --profile-id builtin-android-pegasus-zh)
if ! printf '%s' "$build_result" | grep -F 'exported=1 copied=1 linked=0 unchanged=0 missing=0 warnings=0 output_created=true' >/dev/null; then
  echo "Open-ROM Android Pegasus build was incomplete or produced a runtime warning: $build_result" >&2
  exit 1
fi

exported_rom="$acceptance_root/android-pegasus/gba/hello.gba"
exported_metadata="$acceptance_root/android-pegasus/gba/metadata.pegasus.txt"
exported_launches="$acceptance_root/android-pegasus/varkiv-launches.json"
compare_files "$rom_path" "$exported_rom"
verify_file "$exported_rom" "$rom_size" "$rom_sha256"
if ! jq -e '
  .format_version == 2 and
  .device_profile_id == "builtin-device-android-handheld" and
  .frontend_adapter_id == "builtin-frontend-pegasus" and
  (.bindings | length) == 1 and
  .bindings[0].edition_id == "open-rom-gba-hello-edition" and
  .bindings[0].platform_id == "gba" and
  .bindings[0].rom_path == "gba/hello.gba" and
  .bindings[0].binding.driver_id == "builtin-driver-retroarch" and
  .bindings[0].binding.core_id == "builtin-core-mgba" and
  .bindings[0].arguments == ["-L", "mgba_libretro", "gba/hello.gba"] and
  .bindings[0].android_package == "com.retroarch.aarch64" and
  .bindings[0].android_activity == "com.retroarch.browser.retroactivity.RetroActivityFuture"
' "$exported_launches" >/dev/null; then
  echo "Open-ROM package did not resolve the reviewed RetroArch/mGBA Android launch binding" >&2
  exit 1
fi

reimport_result=$("$binary" import-pegasus \
  --db "$acceptance_root/reimport.db" \
  --library "$acceptance_root/android-pegasus" \
  --source "$exported_metadata" \
  --platform gba \
  --locale en)
if [ "$reimport_result" != 'parsed=1 imported=1 skipped=0' ]; then
  echo "Unexpected open-ROM Pegasus reimport result: $reimport_result" >&2
  exit 1
fi

reimport_hint_json=$("$binary" runtime-hints list \
  --db "$acceptance_root/reimport.db" \
  --edition open-rom-gba-hello-edition \
  --status pending \
  --json)
if ! printf '%s' "$reimport_hint_json" | jq -e 'length == 1 and .[0].source_format == "varkiv-launches-v2"' >/dev/null; then
  echo "Open-ROM reimport did not recover exactly one structured runtime hint" >&2
  exit 1
fi
reimport_hint_id=$(printf '%s' "$reimport_hint_json" | jq -r '.[0].id')
"$binary" runtime-hints apply --db "$acceptance_root/reimport.db" --id "$reimport_hint_id" >/dev/null

rebuild_result=$("$binary" build-pack \
  --db "$acceptance_root/reimport.db" \
  --library "$acceptance_root/android-pegasus" \
  --out "$acceptance_root/android-pegasus-reexport" \
  --profile-id builtin-android-pegasus-zh)
if ! printf '%s' "$rebuild_result" | grep -F 'exported=1 copied=1 linked=0 unchanged=0 missing=0 warnings=0 output_created=true' >/dev/null; then
  echo "Open-ROM Android Pegasus rebuild was incomplete or produced a runtime warning: $rebuild_result" >&2
  exit 1
fi

compare_files "$exported_rom" "$acceptance_root/android-pegasus-reexport/gba/hello.gba"
compare_files "$exported_metadata" "$acceptance_root/android-pegasus-reexport/gba/metadata.pegasus.txt"
compare_files "$exported_launches" "$acceptance_root/android-pegasus-reexport/varkiv-launches.json"
"$binary" db-check --db "$acceptance_root/source.db" >/dev/null
"$binary" db-check --db "$acceptance_root/reimport.db" >/dev/null

for portable_file in \
  "$acceptance_root/android-pegasus/library-manifest.json" \
  "$acceptance_root/android-pegasus/gba/metadata.pegasus.txt" \
  "$acceptance_root/android-pegasus/varkiv-launches.json" \
  "$acceptance_root/android-pegasus-reexport/library-manifest.json" \
  "$acceptance_root/android-pegasus-reexport/gba/metadata.pegasus.txt" \
  "$acceptance_root/android-pegasus-reexport/varkiv-launches.json"
do
  if grep -F "$project_root" "$portable_file" >/dev/null; then
    echo "Portable output leaked the project root" >&2
    exit 1
  fi
  if grep -F "$acceptance_root" "$portable_file" >/dev/null; then
    echo "Portable output leaked the acceptance root" >&2
    exit 1
  fi
done

printf 'open_rom_roundtrip=passed platform=gba source=pegasus imported=1 runtime=retroarch core=mgba android_intent=resolved exported=1 reimported=1 runtime_recovered=1 reexported=1 rom_bytes=%s license=MIT\n' "$rom_size"
printf 'review_output=retained review_id=%s\n' "$(basename "$acceptance_root")"
