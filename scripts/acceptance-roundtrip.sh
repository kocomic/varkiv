#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-roundtrip.sh

Build a private verification binary and exercise Direct ROM, Pegasus, ES-DE,
portable runtime-v2, standalone PPSSPP, media, and missing-ROM round trips in a
new 0700 review root. The retained output contains repository fixtures and
generated packages only; it never reads a user library, NAS mount, production
database, media collection, save, token, or signing material.
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
requested_root=${VARKIV_ACCEPTANCE_DIR:-}

if [ -n "$requested_root" ]; then
  case "$requested_root" in
    /*) ;;
    *) echo "VARKIV_ACCEPTANCE_DIR must be an absolute, new path" >&2; exit 2 ;;
  esac
  if [ -e "$requested_root" ]; then
    echo "Refusing to reuse or overwrite VARKIV_ACCEPTANCE_DIR: $requested_root" >&2
    exit 2
  fi
  mkdir -m 700 "$requested_root"
  acceptance_root=$requested_root
else
  acceptance_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-roundtrip.XXXXXX")
  chmod 700 "$acceptance_root"
fi

binary="$acceptance_root/varkiv"
pegasus_library="$project_root/testdata/pegasus"
esde_library="$project_root/testdata/esde"
runtime_library="$project_root/testdata/portable-runtime-v2"
standalone_library="$project_root/testdata/portable-standalone-v2"

cd "$project_root"
go build -trimpath -o "$binary" ./cmd/varkiv

# Direct ROM -> portable ES-DE -> ES-DE reimport.
"$binary" scan \
  --db "$acceptance_root/direct.db" \
  --library "$pegasus_library" \
  --source "$pegasus_library/gba" \
  --platform gba
"$binary" build-pack \
  --db "$acceptance_root/direct.db" \
  --library "$pegasus_library" \
  --out "$acceptance_root/direct-esde" \
  --name fixture-direct-esde \
  --frontend es-de \
  --target portable \
  --locale en \
  --mode copy
"$binary" import-esde \
  --db "$acceptance_root/direct-reimport.db" \
  --library "$acceptance_root/direct-esde" \
  --source "$acceptance_root/direct-esde/gamelists/gba/gamelist.xml" \
  --platform gba \
  --locale en

# Pegasus -> Android Pegasus -> Pegasus reimport, preserving multi-file bytes.
"$binary" import-pegasus \
  --db "$acceptance_root/pegasus.db" \
  --library "$pegasus_library" \
  --source "$pegasus_library/gba/metadata.pegasus.txt" \
  --platform gba \
  --locale en
"$binary" build-pack \
  --db "$acceptance_root/pegasus.db" \
  --library "$pegasus_library" \
  --out "$acceptance_root/android-pegasus" \
  --name fixture-android-pegasus \
  --frontend pegasus \
  --target android \
  --locale zh-CN \
  --mode copy
"$binary" import-pegasus \
  --db "$acceptance_root/pegasus-reimport.db" \
  --library "$acceptance_root/android-pegasus" \
  --source "$acceptance_root/android-pegasus/gba/metadata.pegasus.txt" \
  --platform gba \
  --locale zh-CN
cmp "$pegasus_library/gba/Advance Wars (USA).gba" "$acceptance_root/android-pegasus/gba/Advance Wars (USA).gba"
cmp "$pegasus_library/gba/Multi Demo Disc 1.gba" "$acceptance_root/android-pegasus/gba/Multi Demo Disc 1.gba"
cmp "$pegasus_library/gba/Multi Demo Disc 2.gba" "$acceptance_root/android-pegasus/gba/Multi Demo Disc 2.gba"
cmp "$pegasus_library/gba/media/advance-cover.svg" "$acceptance_root/android-pegasus/gba/media/advance-cover.svg"
grep -Fx 'assets.box_front: media/advance-cover.svg' "$acceptance_root/android-pegasus/gba/metadata.pegasus.txt" >/dev/null
"$binary" build-pack \
  --db "$acceptance_root/pegasus-reimport.db" \
  --library "$acceptance_root/android-pegasus" \
  --out "$acceptance_root/android-pegasus-reexport" \
  --name fixture-android-pegasus-reexport \
  --frontend pegasus \
  --target android \
  --locale zh-CN \
  --mode copy
cmp "$acceptance_root/android-pegasus/gba/media/advance-cover.svg" "$acceptance_root/android-pegasus-reexport/gba/media/advance-cover.svg"
cmp "$acceptance_root/android-pegasus/gba/metadata.pegasus.txt" "$acceptance_root/android-pegasus-reexport/gba/metadata.pegasus.txt"

# ES-DE -> ROCKNIX ES-DE -> ES-DE reimport.
"$binary" import-esde \
  --db "$acceptance_root/esde.db" \
  --library "$esde_library" \
  --source "$esde_library/gamelists/gba/gamelist.xml" \
  --platform gba \
  --locale zh-CN
"$binary" build-pack \
  --db "$acceptance_root/esde.db" \
  --library "$esde_library" \
  --out "$acceptance_root/rocknix-esde" \
  --name fixture-rocknix-esde \
  --frontend es-de \
  --target rocknix \
  --locale zh-CN \
  --mode copy
"$binary" import-esde \
  --db "$acceptance_root/esde-reimport.db" \
  --library "$acceptance_root/rocknix-esde" \
  --source "$acceptance_root/rocknix-esde/gamelists/gba/gamelist.xml" \
  --platform gba \
  --locale zh-CN
cmp "$esde_library/roms/gba/示例汉化版.gba" "$acceptance_root/rocknix-esde/roms/gba/示例汉化版.gba"
cmp "$esde_library/media/gba/example-cover.svg" "$acceptance_root/rocknix-esde/media/gba/example-cover.svg"
grep -F '<image>../../media/gba/example-cover.svg</image>' "$acceptance_root/rocknix-esde/gamelists/gba/gamelist.xml" >/dev/null
"$binary" build-pack \
  --db "$acceptance_root/esde-reimport.db" \
  --library "$acceptance_root/rocknix-esde" \
  --out "$acceptance_root/rocknix-esde-reexport" \
  --name fixture-rocknix-esde-reexport \
  --frontend es-de \
  --target rocknix \
  --locale zh-CN \
  --mode copy
cmp "$acceptance_root/rocknix-esde/media/gba/example-cover.svg" "$acceptance_root/rocknix-esde-reexport/media/gba/example-cover.svg"
cmp "$acceptance_root/rocknix-esde/gamelists/gba/gamelist.xml" "$acceptance_root/rocknix-esde-reexport/gamelists/gba/gamelist.xml"

# Neutral v6 + runtime v2 -> explicit structured-hint review -> saved profile
# export. Raw frontend commands cannot be applied by this CLI path.
"$binary" import-varkiv \
  --db "$acceptance_root/runtime.db" \
  --library "$runtime_library" \
  --source "$runtime_library/library-manifest.json"
hint_id=$("$binary" runtime-hints list --db "$acceptance_root/runtime.db" --edition e2e-runtime-v2-edition --status pending | awk 'NR == 2 { print $1 }')
if [ -z "$hint_id" ]; then
  echo "Expected one pending structured runtime hint" >&2
  exit 1
fi
"$binary" runtime-hints apply --db "$acceptance_root/runtime.db" --id "$hint_id"
"$binary" build-pack \
  --db "$acceptance_root/runtime.db" \
  --library "$runtime_library" \
  --out "$acceptance_root/runtime-pegasus" \
  --profile-id e2e-runtime-v2-profile
grep -Fx 'core=e2e_mgba_libretro' "$acceptance_root/runtime-pegasus/config/e2e-runtime-v2-edition.cfg" >/dev/null
grep -Fx 'rom=gba/runtime-v2.gba' "$acceptance_root/runtime-pegasus/config/e2e-runtime-v2-edition.cfg" >/dev/null
grep -F '"id": "e2e-runtime-v2-frontend"' "$acceptance_root/runtime-pegasus/varkiv-launches.json" >/dev/null
grep -F '"format": "manga-pegasus"' "$acceptance_root/runtime-pegasus/varkiv-launches.json" >/dev/null
grep -F '"handler": "pegasus"' "$acceptance_root/runtime-pegasus/varkiv-launches.json" >/dev/null
"$binary" import-varkiv \
  --db "$acceptance_root/runtime-reimport.db" \
  --library "$acceptance_root/runtime-pegasus" \
  --source "$acceptance_root/runtime-pegasus/library-manifest.json"
runtime_reimport_hint_id=$("$binary" runtime-hints list --db "$acceptance_root/runtime-reimport.db" --edition e2e-runtime-v2-edition --status pending | awk 'NR == 2 { print $1 }')
if [ -z "$runtime_reimport_hint_id" ]; then
  echo "Expected the custom-frontend runtime binding to survive fresh-database reimport" >&2
  exit 1
fi
"$binary" runtime-hints apply --db "$acceptance_root/runtime-reimport.db" --id "$runtime_reimport_hint_id"
"$binary" build-pack \
  --db "$acceptance_root/runtime-reimport.db" \
  --library "$acceptance_root/runtime-pegasus" \
  --out "$acceptance_root/runtime-reexport" \
  --profile-id e2e-runtime-v2-profile
cmp "$acceptance_root/runtime-pegasus/gba/metadata.pegasus.txt" "$acceptance_root/runtime-reexport/gba/metadata.pegasus.txt"
cmp "$acceptance_root/runtime-pegasus/config/e2e-runtime-v2-edition.cfg" "$acceptance_root/runtime-reexport/config/e2e-runtime-v2-edition.cfg"
cmp "$acceptance_root/runtime-pegasus/varkiv-launches.json" "$acceptance_root/runtime-reexport/varkiv-launches.json"

# Built-in standalone PPSSPP + reviewed argv + custom configuration profile ->
# Windows Pegasus -> fresh database -> second reviewed export. Android Intent
# fields must not bleed into the Windows launch contract.
"$binary" import-varkiv \
  --db "$acceptance_root/standalone.db" \
  --library "$standalone_library" \
  --source "$standalone_library/library-manifest.json"
standalone_hint_id=$("$binary" runtime-hints list --db "$acceptance_root/standalone.db" --edition e2e-standalone-v2-edition --status pending | awk 'NR == 2 { print $1 }')
if [ -z "$standalone_hint_id" ]; then
  echo "Expected one pending standalone runtime hint" >&2
  exit 1
fi
"$binary" runtime-hints apply --db "$acceptance_root/standalone.db" --id "$standalone_hint_id"
"$binary" build-pack \
  --db "$acceptance_root/standalone.db" \
  --library "$standalone_library" \
  --out "$acceptance_root/standalone-pegasus" \
  --profile-id e2e-standalone-v2-profile
cmp "$standalone_library/psp/standalone-v2.iso" "$acceptance_root/standalone-pegasus/psp/standalone-v2.iso"
grep -Fx 'driver=builtin-driver-ppsspp' "$acceptance_root/standalone-pegasus/config/ppsspp/e2e-standalone-v2-edition.ini" >/dev/null
grep -Fx 'product_code=ULUS-00000' "$acceptance_root/standalone-pegasus/config/ppsspp/e2e-standalone-v2-edition.ini" >/dev/null
if ! jq -e 'all(.bindings[]; ((has("android_package") | not) and (has("android_activity") | not) and (.executable_hints | length > 0)))' "$acceptance_root/standalone-pegasus/varkiv-launches.json" >/dev/null; then
  echo "Windows standalone launch bindings contain Android-only fields or omit executable hints" >&2
  exit 1
fi
"$binary" import-varkiv \
  --db "$acceptance_root/standalone-reimport.db" \
  --library "$acceptance_root/standalone-pegasus" \
  --source "$acceptance_root/standalone-pegasus/library-manifest.json"
standalone_reimport_hint_id=$("$binary" runtime-hints list --db "$acceptance_root/standalone-reimport.db" --edition e2e-standalone-v2-edition --status pending | awk 'NR == 2 { print $1 }')
if [ -z "$standalone_reimport_hint_id" ]; then
  echo "Expected the standalone binding to survive fresh-database reimport" >&2
  exit 1
fi
"$binary" runtime-hints apply --db "$acceptance_root/standalone-reimport.db" --id "$standalone_reimport_hint_id"
"$binary" build-pack \
  --db "$acceptance_root/standalone-reimport.db" \
  --library "$acceptance_root/standalone-pegasus" \
  --out "$acceptance_root/standalone-reexport" \
  --profile-id e2e-standalone-v2-profile
cmp "$acceptance_root/standalone-pegasus/config/ppsspp/e2e-standalone-v2-edition.ini" "$acceptance_root/standalone-reexport/config/ppsspp/e2e-standalone-v2-edition.ini"
cmp "$acceptance_root/standalone-pegasus/varkiv-launches.json" "$acceptance_root/standalone-reexport/varkiv-launches.json"

# A missing metadata target must remain a preview/skip, never a metadata-only
# database row pretending to have a ROM.
missing_result=$("$binary" import-esde \
  --db "$acceptance_root/missing.db" \
  --library "$project_root/testdata/missing" \
  --source "$project_root/testdata/missing/gamelist.xml" \
  --platform gba \
  --locale zh-CN)
if [ "$missing_result" != 'parsed=1 imported=0 skipped=1' ]; then
  echo "Unexpected missing-ROM result: $missing_result" >&2
  exit 1
fi

for database in \
  direct.db direct-reimport.db pegasus.db pegasus-reimport.db \
  esde.db esde-reimport.db runtime.db runtime-reimport.db \
  standalone.db standalone-reimport.db missing.db
do
  "$binary" db-check --db "$acceptance_root/$database" >/dev/null
done

for portable_file in \
  "$acceptance_root/direct-esde/library-manifest.json" \
  "$acceptance_root/android-pegasus/library-manifest.json" \
  "$acceptance_root/android-pegasus/varkiv-launches.json" \
  "$acceptance_root/rocknix-esde/library-manifest.json" \
  "$acceptance_root/rocknix-esde/gamelists/gba/gamelist.xml" \
  "$acceptance_root/rocknix-esde-reexport/library-manifest.json" \
  "$acceptance_root/rocknix-esde-reexport/gamelists/gba/gamelist.xml" \
  "$acceptance_root/android-pegasus-reexport/library-manifest.json" \
  "$acceptance_root/android-pegasus-reexport/gba/metadata.pegasus.txt" \
  "$acceptance_root/runtime-pegasus/library-manifest.json" \
  "$acceptance_root/runtime-pegasus/varkiv-launches.json" \
  "$acceptance_root/runtime-pegasus/config/e2e-runtime-v2-edition.cfg" \
  "$acceptance_root/runtime-reexport/varkiv-launches.json" \
  "$acceptance_root/runtime-reexport/config/e2e-runtime-v2-edition.cfg" \
  "$acceptance_root/standalone-pegasus/library-manifest.json" \
  "$acceptance_root/standalone-pegasus/varkiv-launches.json" \
  "$acceptance_root/standalone-pegasus/config/ppsspp/e2e-standalone-v2-edition.ini" \
  "$acceptance_root/standalone-reexport/varkiv-launches.json"
do
  if grep -F "$project_root" "$portable_file" >/dev/null; then
    echo "Portable output leaked the project root: $portable_file" >&2
    exit 1
  fi
done

printf 'roundtrip_acceptance=passed platforms=1 direct=3 pegasus=2 esde=1 media_roundtrips=2 runtime_bindings=2 standalone_bindings=1 missing_skipped=1\n'
printf 'review_root=%s\n' "$acceptance_root"
