#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-android-retroarch.sh [--retroarch-apk FILE] [--mgba-core-zip FILE] [--keep]

Installs pinned official RetroArch/mGBA artifacts into a one-use API 35 ARM64
Google APIs AVD, then launches a pinned MIT GBA test ROM from the Varkiv
Debug-only SAF provider. Downloaded executable artifacts and the exact AVD are
deleted unless --keep is supplied. No personal library or existing device is read.
EOF
}

retroarch_source=""
core_source=""
keep=0
while (($#)); do
  case "$1" in
    --retroarch-apk)
      retroarch_source="${2:-}"
      shift 2
      ;;
    --mgba-core-zip)
      core_source="${2:-}"
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

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
android_root="$repo_root/clients/android"
sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
[[ -n "$sdk_root" ]] || { echo "ANDROID_SDK_ROOT or ANDROID_HOME is required" >&2; exit 1; }

adb="$sdk_root/platform-tools/adb"
emulator="$sdk_root/emulator/emulator"
avdmanager="$sdk_root/cmdline-tools/latest/bin/avdmanager"
for command_path in "$adb" "$emulator" "$avdmanager" "$android_root/gradlew"; do
  [[ -x "$command_path" ]] || { echo "required executable is unavailable: $command_path" >&2; exit 1; }
done
for command_name in curl jq openssl shasum unzip; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "required command is unavailable: $command_name" >&2; exit 1; }
done
[[ "$(uname -m)" == "arm64" || "$(uname -m)" == "aarch64" ]] || {
  echo "the pinned real-RetroArch acceptance currently requires an ARM64 host/AVD" >&2
  exit 1
}

image_abi="arm64-v8a"
image_package="system-images;android-35;google_apis;$image_abi"
image_root="$sdk_root/system-images/android-35/google_apis/$image_abi"
[[ -f "$image_root/package.xml" ]] || { echo "missing pinned system image: $image_package" >&2; exit 1; }

retroarch_url="https://buildbot.libretro.com/nightly/android/2026-08-27-RetroArch_aarch64.apk"
retroarch_size=194229872
retroarch_sha256="7074c83288605e32f2699f30eb5c54d86ffa701fab2080eb2f3c0beefa0b17bc"
core_url="https://buildbot.libretro.com/nightly/android/latest/arm64-v8a/mgba_libretro_android.so.zip"
core_size=419393
# Libretro does not retain dated Android core archives beside the dated APK.
# Keep the official updater URL behind a hard byte identity gate: a new nightly
# must be reviewed and accepted here before it can enter the disposable AVD.
core_build_date="2026-08-29"
core_sha256="d85e348347e1a419fc71d1f962c2ac6d376a8d09c8a0938706234da436be89b5"
rom_url="https://raw.githubusercontent.com/jsmolka/gba-tests/a7113b67e63f83a9b321696ddd7042ccfad6c881/ppu/hello.gba"
rom_size=1300
rom_sha256="38aed48b67bc0f701e8aa222b0c3334bd306bd29888707bb7224d81f5576c264"
license_url="https://raw.githubusercontent.com/jsmolka/gba-tests/a7113b67e63f83a9b321696ddd7042ccfad6c881/LICENSE"
license_size=1070
license_sha256="b59a9ed235d76752c977f04116bc72155a14af48176bdbf50d7e5c29fca35aa7"

acceptance_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-android-retroarch.XXXXXX")"
chmod 700 "$acceptance_root"
export ANDROID_AVD_HOME="$acceptance_root/avd"
export ANDROID_EMULATOR_HOME="$acceptance_root/emulator-home"
mkdir -m 700 -p "$ANDROID_AVD_HOME" "$ANDROID_EMULATOR_HOME" "$acceptance_root/downloads"

avd_name="varkiv-retroarch-e2e-$$"
emulator_pid=""
emulator_serial=""
cleanup() {
  local status=$?
  if [[ -n "$emulator_serial" ]]; then
    "$adb" -s "$emulator_serial" emu kill >/dev/null 2>&1 || true
  fi
  if [[ -n "$emulator_pid" ]]; then
    wait "$emulator_pid" 2>/dev/null || true
  fi
  if ((keep == 0)); then
    case "$acceptance_root" in
      "${TMPDIR:-/tmp}"/varkiv-android-retroarch.*)
        rm -rf -- "$acceptance_root"
        ;;
      *)
        echo "refusing to remove unexpected acceptance root: $acceptance_root" >&2
        status=1
        ;;
    esac
  else
    printf 'retained_evidence=%s\n' "$acceptance_root" >&2
  fi
  return "$status"
}
trap cleanup EXIT INT TERM

copy_or_download() {
  local source_path="$1"
  local url="$2"
  local destination="$3"
  if [[ -n "$source_path" ]]; then
    [[ -f "$source_path" ]] || { echo "pinned local artifact is unavailable: $source_path" >&2; exit 1; }
    cp "$source_path" "$destination"
  else
    curl --fail --location --retry 3 --output "$destination" "$url"
  fi
}

verify_artifact() {
  local path="$1"
  local expected_size="$2"
  local expected_sha="$3"
  local label="$4"
  local actual_size actual_sha
  actual_size="$(wc -c < "$path" | tr -d ' ')"
  actual_sha="$(shasum -a 256 "$path" | awk '{print $1}')"
  [[ "$actual_size" == "$expected_size" ]] || { echo "$label size drifted" >&2; exit 1; }
  [[ "$actual_sha" == "$expected_sha" ]] || { echo "$label SHA-256 drifted" >&2; exit 1; }
}

retroarch_apk="$acceptance_root/downloads/RetroArch_aarch64.apk"
core_zip="$acceptance_root/downloads/mgba_libretro_android.so.zip"
rom_path="$acceptance_root/downloads/hello.gba"
license_path="$acceptance_root/downloads/gba-tests-LICENSE"
copy_or_download "$retroarch_source" "$retroarch_url" "$retroarch_apk"
copy_or_download "$core_source" "$core_url" "$core_zip"
curl --fail --location --retry 3 --output "$rom_path" "$rom_url"
curl --fail --location --retry 3 --output "$license_path" "$license_url"
verify_artifact "$retroarch_apk" "$retroarch_size" "$retroarch_sha256" "RetroArch APK"
verify_artifact "$core_zip" "$core_size" "$core_sha256" "mGBA core archive"
verify_artifact "$rom_path" "$rom_size" "$rom_sha256" "MIT GBA ROM"
verify_artifact "$license_path" "$license_size" "$license_sha256" "GBA fixture license"
unzip -p "$core_zip" mgba_libretro_android.so > "$acceptance_root/mgba_libretro_android.so"

(
  cd "$android_root"
  ./gradlew --no-daemon assembleDebug assembleDebugAndroidTest
)

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
"$emulator" -avd "$avd_name" -port "$emulator_port" -no-window -no-audio -no-boot-anim -no-snapshot -no-metrics -gpu swiftshader_indirect >"$acceptance_root/emulator.log" 2>&1 &
emulator_pid=$!

booted=0
for _ in $(seq 1 150); do
  if [[ "$("$adb" -s "$emulator_serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]]; then
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
"$adb" -s "$emulator_serial" shell settings put secure immersive_mode_confirmations confirmed
"$adb" -s "$emulator_serial" install -r "$retroarch_apk" >/dev/null
root_result="$("$adb" -s "$emulator_serial" root 2>&1 || true)"
root_ready=0
root_uid=""
for _ in $(seq 1 30); do
  "$adb" -s "$emulator_serial" wait-for-device >/dev/null 2>&1 || true
  root_uid="$("$adb" -s "$emulator_serial" shell id -u 2>/dev/null | tr -d '\r' || true)"
  if [[ "$root_uid" == 0 ]]; then
    root_ready=1
    break
  fi
  sleep 0.5
done
if ((root_ready != 1)); then
  echo "the disposable Google APIs AVD did not permit isolated core injection: ${root_result:-no adb root status}; uid=${root_uid:-unavailable}" >&2
  exit 1
fi
retroarch_uid="$("$adb" -s "$emulator_serial" shell cmd package list packages -U com.retroarch.aarch64 | sed -n 's/.* uid://p' | tr -d '\r')"
[[ "$retroarch_uid" =~ ^[0-9]+$ ]] || { echo "unable to resolve RetroArch package uid" >&2; exit 1; }
"$adb" -s "$emulator_serial" shell mkdir -p /data/user/0/com.retroarch.aarch64/cores
"$adb" -s "$emulator_serial" push "$acceptance_root/mgba_libretro_android.so" /data/user/0/com.retroarch.aarch64/cores/mgba_libretro_android.so >/dev/null
"$adb" -s "$emulator_serial" shell chown -R "$retroarch_uid:$retroarch_uid" /data/user/0/com.retroarch.aarch64/cores

# The sideloaded nightly registers MANAGE_EXTERNAL_STORAGE only after its first
# Activity start. Trigger that disposable first-run state, grant the app-op in
# the isolated AVD, then stop it before testing Varkiv's external launch.
"$adb" -s "$emulator_serial" shell am start -W -n com.retroarch.aarch64/com.retroarch.browser.retroactivity.RetroActivityFuture >/dev/null
sleep 1
"$adb" -s "$emulator_serial" shell appops set com.retroarch.aarch64 MANAGE_EXTERNAL_STORAGE allow
"$adb" -s "$emulator_serial" shell appops get com.retroarch.aarch64 MANAGE_EXTERNAL_STORAGE | grep -Fq 'MANAGE_EXTERNAL_STORAGE: allow' || {
  echo "RetroArch first-run storage app-op was not granted in the disposable AVD" >&2
  exit 1
}
"$adb" -s "$emulator_serial" shell am force-stop com.retroarch.aarch64

debug_apk="$android_root/app/build/outputs/apk/debug/app-debug.apk"
test_apk="$android_root/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk"
"$adb" -s "$emulator_serial" install -r "$debug_apk" >/dev/null
"$adb" -s "$emulator_serial" install -r "$test_apk" >/dev/null
"$adb" -s "$emulator_serial" logcat -c
rom_base64="$(openssl base64 -A -in "$rom_path")"
"$adb" -s "$emulator_serial" shell am instrument -w -r \
  -e class 'org.varkiv.agent.RetroArchLaunchInstrumentedTest#launchesPinnedRetroArchWithSafContentUri' \
  -e e2e_retroarch_rom_base64 "$rom_base64" \
  org.varkiv.agent.test/androidx.test.runner.AndroidJUnitRunner | tee "$acceptance_root/instrumentation.txt"

"$adb" -s "$emulator_serial" pull /data/user/0/org.varkiv.agent/files/retroarch-real-launch.png "$acceptance_root/retroarch-real-launch.png" >/dev/null
"$adb" -s "$emulator_serial" logcat -d -v threadtime > "$acceptance_root/logcat.txt"
grep -Fq 'OK (1 test)' "$acceptance_root/instrumentation.txt" || { echo "RetroArch instrumentation did not pass" >&2; exit 1; }
grep -Fq 'Libretro path: "/data/user/0/com.retroarch.aarch64/cores/mgba_libretro_android.so"' "$acceptance_root/logcat.txt" || { echo "RetroArch did not resolve the pinned mGBA core" >&2; exit 1; }
grep -Fq 'Auto-start game "content://org.varkiv.agent.test.documents/document/root%2Froms%2Fhello.gba"' "$acceptance_root/logcat.txt" || { echo "RetroArch did not receive the SAF ROM" >&2; exit 1; }
if grep -Eq 'Permission Denial: opening provider org\.varkiv\.agent\.TestDocumentsProvider|Failed to load content|Failed to open libretro core' "$acceptance_root/logcat.txt"; then
  echo "RetroArch logged a core/content permission failure" >&2
  exit 1
fi
screenshot_sha256="$(shasum -a 256 "$acceptance_root/retroarch-real-launch.png" | awk '{print $1}')"

jq -n \
  --arg version "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" \
  --arg image "$image_package" \
  --arg retroarch_sha256 "$retroarch_sha256" \
  --arg core_sha256 "$core_sha256" \
  --arg rom_sha256 "$rom_sha256" \
  --arg screenshot_sha256 "$screenshot_sha256" \
  --arg core_build_date "$core_build_date" \
  '{format:"varkiv-android-retroarch-acceptance-v1",application_version:$version,api_level:35,system_image:$image,retroarch:{package:"com.retroarch.aarch64",version_name:"1.22.2_GIT",version_code:1787835987,build_date:"2026-08-27",sha256:$retroarch_sha256,disposable_first_run_initialized:true},core:{id:"mgba",build_date:$core_build_date,sha256:$core_sha256},fixture:{license:"MIT",sha256:$rom_sha256},saf_uri_grant:true,core_loaded:true,rom_opened:true,rendered_frame:true,screenshot_sha256:$screenshot_sha256,cleanup_scope:"one-use-avd-and-downloaded-executables",privacy:{user_library_read:false,user_device_selected:false,nas_read:false,private_paths_reported:false}}' > "$acceptance_root/report.json"

printf 'android_retroarch_e2e=passed version=%s api=35 abi=%s retroarch=%s core=%s rom=%s screenshot=%s\n' \
  "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" "$image_abi" \
  "${retroarch_sha256:0:12}" "${core_sha256:0:12}" "${rom_sha256:0:12}" "${screenshot_sha256:0:12}"
