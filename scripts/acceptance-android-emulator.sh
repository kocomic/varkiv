#!/usr/bin/env bash
set -Eeuo pipefail

stage="argument-validation"
report_error() {
  local status=$?
  local line=$1
  printf 'Android AVD acceptance failed (stage=%s line=%s status=%s)\n' "${stage}" "${line}" "${status}" >&2
  return "${status}"
}
trap 'report_error "$LINENO"' ERR

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-android-emulator.sh [--port PORT] [--keep]

Runs the Android Agent-lite against a new loopback-only Varkiv server and a
one-use API 35 Google APIs AVD. The acceptance uses only repository synthetic
fixtures and deletes its exact AVD/server state unless --keep is supplied.
EOF
}

port=18088
keep=0
while (($#)); do
  case "$1" in
    --port)
      port="${2:-}"
      shift 2
      ;;
    --keep)
      keep=1
      shift
      ;;
    --help|-h)
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

[[ "$port" =~ ^[0-9]+$ ]] && ((port >= 1024 && port <= 65535)) || { echo "invalid --port" >&2; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stage="toolchain-preflight"
android_root="$repo_root/clients/android"
sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
[[ -n "$sdk_root" ]] || { echo "ANDROID_SDK_ROOT or ANDROID_HOME is required" >&2; exit 1; }

adb="$sdk_root/platform-tools/adb"
emulator="$sdk_root/emulator/emulator"
avdmanager="$sdk_root/cmdline-tools/latest/bin/avdmanager"
for command_path in "$adb" "$emulator" "$avdmanager" "$android_root/gradlew"; do
  [[ -x "$command_path" ]] || { echo "required executable is unavailable: $command_path" >&2; exit 1; }
done
for command_name in curl go jq openssl; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "required command is unavailable: $command_name" >&2; exit 1; }
done

case "$(uname -m)" in
  arm64|aarch64)
    image_abi="arm64-v8a"
    ;;
  x86_64|amd64)
    image_abi="x86_64"
    ;;
  *)
    echo "unsupported Android emulator host architecture" >&2
    exit 1
    ;;
esac
image_package="system-images;android-35;google_apis;$image_abi"
image_root="$sdk_root/system-images/android-35/google_apis/$image_abi"
[[ -f "$image_root/package.xml" ]] || { echo "missing pinned system image: $image_package" >&2; exit 1; }

stage="fixture-setup"
acceptance_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-android-avd.XXXXXX")"
chmod 700 "$acceptance_root"
export ANDROID_AVD_HOME="$acceptance_root/avd"
export ANDROID_EMULATOR_HOME="$acceptance_root/emulator-home"
mkdir -m 700 -p "$ANDROID_AVD_HOME" "$ANDROID_EMULATOR_HOME" "$acceptance_root/library/gba" "$acceptance_root/library/psp" "$acceptance_root/state"

avd_name="varkiv-e2e-$$"
server_pid=""
emulator_pid=""
emulator_serial=""
cleanup() {
  local status=$?
  if [[ -n "$emulator_serial" ]]; then
    "$adb" -s "$emulator_serial" reverse --remove "tcp:$port" >/dev/null 2>&1 || true
    "$adb" -s "$emulator_serial" emu kill >/dev/null 2>&1 || true
  fi
  if [[ -n "$emulator_pid" ]]; then
    wait "$emulator_pid" 2>/dev/null || true
  fi
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if ((keep == 0)); then
    rm -rf -- "$acceptance_root"
  else
    printf 'retained_evidence=%s\n' "$acceptance_root" >&2
  fi
  return "$status"
}
trap cleanup EXIT INT TERM

stage="server-build"
cp "$repo_root/testdata/neutral/gba/recovery.gba" "$acceptance_root/library/gba/android-e2e.gba"
cp "$repo_root/testdata/portable-standalone-v2/psp/standalone-v2.iso" "$acceptance_root/library/psp/android-e2e.iso"
(
  cd "$repo_root"
  go build -trimpath -o "$acceptance_root/varkiv" ./cmd/varkiv
)

admin_token="android-avd-admin-$(openssl rand -hex 24 2>/dev/null || uuidgen | tr -d '-')"
"$acceptance_root/varkiv" serve \
  --addr "127.0.0.1:$port" \
  --db "$acceptance_root/library.db" \
  --state "$acceptance_root/state" \
  --library "$acceptance_root/library" \
  --token "$admin_token" >"$acceptance_root/server.log" 2>&1 &
server_pid=$!

stage="server-readiness"
ready=0
for _ in $(seq 1 100); do
  if curl --silent --show-error --fail "http://127.0.0.1:$port/api/v1/health/ready" >/dev/null 2>&1; then
    ready=1
    break
  fi
  if ! kill -0 "$server_pid" >/dev/null 2>&1; then
    cat "$acceptance_root/server.log" >&2
    echo "isolated Varkiv server exited before readiness" >&2
    exit 1
  fi
  sleep 0.1
done
((ready == 1)) || { cat "$acceptance_root/server.log" >&2; echo "isolated Varkiv server was not ready" >&2; exit 1; }

api_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local args=(--silent --show-error --fail --request "$method" --header "Authorization: Bearer $admin_token")
  if [[ -n "$body" ]]; then
    args+=(--header 'Content-Type: application/json' --data-binary "$body")
  fi
  curl "${args[@]}" "http://127.0.0.1:$port/api/v1/$path"
}

stage="server-catalog-setup"
profile_id="android-avd-e2e-profile"
profile_body="$(jq -nc --arg id "$profile_id" --arg arch "$image_abi" '{id:$id,name:"Disposable Android AVD",contract_version:1,target:"android",os_family:"android",distribution:"android-35",architecture:$arch,path_style:"android-uri",case_sensitive:true,max_path:1024,paths:{save_dir:"saves",rom_dir:"roms",config_dir:"config"},support_level:"package-tested",evidence:{scope:"isolated-avd"},enabled:true}')"
api_json POST device-profiles "$profile_body" >/dev/null
peer_body="$(jq -nc --arg profile "$profile_id" --arg arch "$image_abi" '{name:"Disposable Android peer",device_profile_id:$profile,os_family:"android",distribution:"android-35",architecture:$arch,capabilities:{save_streams:true,multi_file_saves:true}}')"
peer_device_id="$(api_json POST devices "$peer_body" | jq -er '.id')"

import_request='{"source":"gba/android-e2e.gba","platform":"gba","rom_storage":"reference"}'
preview="$(api_json POST imports/roms/preview "$import_request")"
preview_token="$(jq -er '.preview_token' <<<"$preview")"
selected_tokens="$(jq -c '[.candidates[] | select(.status == "new") | .token]' <<<"$preview")"
[[ "$(jq 'length' <<<"$selected_tokens")" == 1 ]] || { echo "expected one Android E2E import candidate" >&2; exit 1; }
commit_request="$(jq -nc --arg source 'gba/android-e2e.gba' --arg platform gba --arg storage reference --arg preview "$preview_token" --argjson selected "$selected_tokens" '{source:$source,platform:$platform,rom_storage:$storage,preview_token:$preview,selected_tokens:$selected}')"
api_json POST imports/roms/commit "$commit_request" >/dev/null

games="$(api_json GET 'games?locale=en&limit=20&offset=0')"
gba_edition_id="$(jq -er '.data[] | select(.platform == "gba") | .editions[0].id' <<<"$games")"
gba_stream_id="android-avd-e2e-stream"
setup_body="$(jq -nc --arg stream "$gba_stream_id" --arg edition "$gba_edition_id" --arg profile "$profile_id" '{stream:{id:$stream,owner_type:"edition",owner_key:$edition,driver_id:"builtin-driver-retroarch",portability:"driver-dependent",edition_ids:[$edition],compatibility:"native"},binding:{id:"android-avd-e2e-binding",edition_id:$edition,device_profile_id:$profile,driver_id:"builtin-driver-retroarch",local_paths:["{{device.save_dir}}/{{rom.stem}}.srm"],discovery:{mode:"file",refresh:"process-exit"},enabled:true}}')"
api_json POST save-bindings/setup "$setup_body" >/dev/null
launch_body="$(jq -nc --arg edition "$gba_edition_id" --arg profile "$profile_id" '{edition_id:$edition,device_profile_id:$profile,driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",arguments:["-L","{{core.library}}","{{rom.path}}"],enabled:true}')"
api_json POST launch-bindings "$launch_body" >/dev/null

psp_import_request='{"source":"psp/android-e2e.iso","platform":"psp","rom_storage":"reference"}'
psp_preview="$(api_json POST imports/roms/preview "$psp_import_request")"
psp_preview_token="$(jq -er '.preview_token' <<<"$psp_preview")"
psp_selected_tokens="$(jq -c '[.candidates[] | select(.status == "new") | .token]' <<<"$psp_preview")"
[[ "$(jq 'length' <<<"$psp_selected_tokens")" == 1 ]] || { echo "expected one PPSSPP E2E import candidate" >&2; exit 1; }
psp_commit_request="$(jq -nc --arg source 'psp/android-e2e.iso' --arg platform psp --arg storage reference --arg preview "$psp_preview_token" --argjson selected "$psp_selected_tokens" '{source:$source,platform:$platform,rom_storage:$storage,preview_token:$preview,selected_tokens:$selected}')"
api_json POST imports/roms/commit "$psp_commit_request" >/dev/null
games="$(api_json GET 'games?locale=en&limit=20&offset=0')"
psp_edition_id="$(jq -er '.data[] | select(.platform == "psp") | .editions[0].id' <<<"$games")"
psp_edition="$(api_json GET "editions/$psp_edition_id")"
psp_edition_update="$(jq -c '{game_id,default_title,edition_type,version,languages,author,serial,product_code:"ULUS-00000",title_id,sort_order,titles}' <<<"$psp_edition")"
api_json PUT "editions/$psp_edition_id" "$psp_edition_update" >/dev/null
psp_stream_id="android-avd-ppsspp-stream"
psp_setup_body="$(jq -nc --arg stream "$psp_stream_id" --arg edition "$psp_edition_id" --arg profile "$profile_id" '{stream:{id:$stream,owner_type:"edition",owner_key:$edition,driver_id:"builtin-driver-ppsspp",portability:"driver-dependent",edition_ids:[$edition],compatibility:"native"},binding:{id:"android-avd-ppsspp-binding",edition_id:$edition,device_profile_id:$profile,driver_id:"builtin-driver-ppsspp",local_paths:["{{driver.user_dir}}/PSP/SAVEDATA/{{edition.product_code}}"],discovery:{mode:"directory",refresh:"process-exit"},enabled:true}}')"
api_json POST save-bindings/setup "$psp_setup_body" >/dev/null
psp_launch_body="$(jq -nc --arg edition "$psp_edition_id" --arg profile "$profile_id" '{edition_id:$edition,device_profile_id:$profile,driver_id:"builtin-driver-ppsspp",arguments:["{{rom.path}}"],enabled:true}')"
api_json POST launch-bindings "$psp_launch_body" >/dev/null
pairing_body="$(jq -nc --arg profile "$profile_id" '{expires_in_seconds:600,requested_device:{device_profile_id:$profile}}')"
pairing_code="$(api_json POST pairing-codes "$pairing_body" | jq -er '.code')"
rom_base64="$(openssl base64 -A -in "$repo_root/testdata/neutral/gba/recovery.gba")"
psp_rom_base64="$(openssl base64 -A -in "$repo_root/testdata/portable-standalone-v2/psp/standalone-v2.iso")"

stage="avd-create"
printf 'no\n' | "$avdmanager" create avd --name "$avd_name" --package "$image_package" --device pixel_2 >/dev/null
emulator_port=""
for candidate in $(seq 5588 2 5680); do
  if ! "$adb" devices | awk 'NR>1 {print $1}' | grep -Fxq "emulator-$candidate"; then
    emulator_port="$candidate"
    break
  fi
done
[[ -n "$emulator_port" ]] || { echo "no disposable emulator console port is available" >&2; exit 1; }
emulator_serial="emulator-$emulator_port"
stage="avd-boot"
"$emulator" -avd "$avd_name" -port "$emulator_port" -no-window -no-audio -no-boot-anim -no-snapshot -gpu swiftshader_indirect >"$acceptance_root/emulator.log" 2>&1 &
emulator_pid=$!

booted=0
for _ in $(seq 1 150); do
  boot_state="$("$adb" -s "$emulator_serial" shell getprop sys.boot_completed 2>/dev/null || true)"
  if [[ "${boot_state//$'\r'/}" == 1 ]]; then
    booted=1
    break
  fi
  if ! kill -0 "$emulator_pid" >/dev/null 2>&1; then
    tail -n 200 "$acceptance_root/emulator.log" >&2
    echo "disposable Android emulator exited before boot" >&2
    exit 1
  fi
  sleep 2
done
((booted == 1)) || { tail -n 200 "$acceptance_root/emulator.log" >&2; echo "disposable Android emulator did not boot" >&2; exit 1; }

"$adb" -s "$emulator_serial" shell settings put global window_animation_scale 0
"$adb" -s "$emulator_serial" shell settings put global transition_animation_scale 0
"$adb" -s "$emulator_serial" shell settings put global animator_duration_scale 0
"$adb" -s "$emulator_serial" reverse "tcp:$port" "tcp:$port" >/dev/null

stage="android-instrumentation"
(
  cd "$android_root"
  ./gradlew --no-daemon connectedDebugAndroidTest \
    -Pandroid.testInstrumentationRunnerArguments.e2e_server="http://127.0.0.1:$port" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_pairing_code="$pairing_code" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_admin_token="$admin_token" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_stream_id="$gba_stream_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_edition_id="$gba_edition_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_ppsspp_stream_id="$psp_stream_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_ppsspp_edition_id="$psp_edition_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_profile_id="$profile_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_peer_device_id="$peer_device_id" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_rom_base64="$rom_base64" \
    -Pandroid.testInstrumentationRunnerArguments.e2e_ppsspp_rom_base64="$psp_rom_base64"
)

stage="result-verification"
revision_count="$(api_json GET "save-streams/$gba_stream_id/revisions?limit=20&offset=0" | jq -er '.data | length')"
psp_revision_count="$(api_json GET "save-streams/$psp_stream_id/revisions?limit=20&offset=0" | jq -er '.data | length')"
session_summary="$(api_json GET 'sync/sessions?limit=20&offset=0' | jq -cer '[.data[] | {uploaded_count,downloaded_count,conflict_count,status}]')"
[[ "$revision_count" == 3 ]] || { echo "Android E2E revision count drifted" >&2; exit 1; }
[[ "$psp_revision_count" == 3 ]] || { echo "Android PPSSPP E2E revision count drifted" >&2; exit 1; }
jq -e 'length == 3 and all(.[]; .status == "complete") and ([.[].uploaded_count] | add == 2) and ([.[].downloaded_count] | add == 2) and ([.[].conflict_count] | add == 2)' <<<"$session_summary" >/dev/null

stage="report"
jq -n --arg version "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" --arg image "$image_package" --argjson revisions "$revision_count" --argjson psp_revisions "$psp_revision_count" '{format:"varkiv-android-avd-acceptance-v3",application_version:$version,api_level:35,system_image:$image,pairing:true,saf_provider:true,rom_inventory:{gba:true,psp:true},edition_match:{gba:true,psp:true},launch_catalog:{retroarch:true,ppsspp:true},save_contracts:{retroarch_single_file:true,ppsspp_product_code_directory:true},upload:2,download:2,conflict:2,revisions:{retroarch:$revisions,ppsspp:$psp_revisions},cleanup_scope:"one-use-avd-and-isolated-server",privacy:{user_library_read:false,user_device_selected:false,private_paths_reported:false,tokens_reported:false}}' >"$acceptance_root/report.json"
printf 'android_avd_e2e=passed version=%s api=35 abi=%s paired=1 inventory=2 matched=2 launch=2 upload=2 download=2 conflict=2 retroarch_revisions=%s ppsspp_revisions=%s\n' "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" "$image_abi" "$revision_count" "$psp_revision_count"
