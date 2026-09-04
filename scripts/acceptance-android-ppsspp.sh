#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-android-ppsspp.sh [--ppsspp-apk FILE] [--keep]

Builds a deterministic Apache-2.0 PSP homebrew fixture with a digest-pinned
official PSPDEV image, installs the pinned official PPSSPP APK in a one-use API
35 ARM64 Google APIs AVD, and launches the fixture through Varkiv's Debug-only
SAF provider. The APK, fixture, screenshot, and exact AVD are deleted unless
--keep is supplied. No personal library or existing device is read.
EOF
}

ppsspp_source=""
keep=0
while (($#)); do
  case "$1" in
    --ppsspp-apk)
      ppsspp_source="${2:-}"
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
fixture_source="$repo_root/testdata/psp-homebrew"
sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
[[ -n "$sdk_root" ]] || { echo "ANDROID_SDK_ROOT or ANDROID_HOME is required" >&2; exit 1; }

adb="$sdk_root/platform-tools/adb"
emulator="$sdk_root/emulator/emulator"
avdmanager="$sdk_root/cmdline-tools/latest/bin/avdmanager"
for command_path in "$adb" "$emulator" "$avdmanager" "$android_root/gradlew"; do
  [[ -x "$command_path" ]] || { echo "required executable is unavailable: $command_path" >&2; exit 1; }
done
for command_name in cmp curl docker jq shasum; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "required command is unavailable: $command_name" >&2; exit 1; }
done
[[ -f "$fixture_source/main.c" && -f "$fixture_source/Makefile" ]] || {
  echo "PSP fixture source is unavailable" >&2
  exit 1
}
[[ "$(uname -m)" == "arm64" || "$(uname -m)" == "aarch64" ]] || {
  echo "the pinned real-PPSSPP acceptance currently requires an ARM64 host/AVD" >&2
  exit 1
}

image_abi="arm64-v8a"
image_package="system-images;android-35;google_apis;$image_abi"
image_root="$sdk_root/system-images/android-35/google_apis/$image_abi"
[[ -f "$image_root/package.xml" ]] || { echo "missing pinned system image: $image_package" >&2; exit 1; }

ppsspp_url="https://www.ppsspp.org/files/1_20_4/ppsspp.apk"
ppsspp_size=45448397
ppsspp_sha256="dd702a31270ecd68db37afee311d6efe08b1d4da9601d2750ce1a73bbd316c3f"
pspdev_image="ghcr.io/pspdev/pspdev:v20260701@sha256:c9f1e60e8635d4df5ea246981b7473cbf48a9cf8457c1735f787821a684957f2"
fixture_size=129952
fixture_sha256="c4fcae86a1a1bf0d8bcff1eecb6af912d0c39898f6a4f1667f8db6fd01823f63"

acceptance_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-android-ppsspp.XXXXXX")"
chmod 700 "$acceptance_root"
export ANDROID_AVD_HOME="$acceptance_root/avd"
export ANDROID_EMULATOR_HOME="$acceptance_root/emulator-home"
mkdir -m 700 -p "$ANDROID_AVD_HOME" "$ANDROID_EMULATOR_HOME" "$acceptance_root/build" "$acceptance_root/downloads"

avd_name="varkiv-ppsspp-e2e-$$"
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
      "${TMPDIR:-/tmp}"/varkiv-android-ppsspp.*)
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

build_fixture() {
  local output_name="$1"
  docker run --rm --platform linux/amd64 --network none --cap-drop ALL \
    --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,nosuid,size=128m \
    --mount "type=bind,src=$fixture_source,dst=/source,readonly" \
    --mount "type=bind,src=$acceptance_root/build,dst=/output" \
    "$pspdev_image" sh -euc \
    'export PATH="$PSPDEV/bin:$PATH"; cp -R /source /tmp/build; cd /tmp/build; make >/dev/null; test -s EBOOT.PBP; cp EBOOT.PBP "/output/$1"' \
    fixture-build "$output_name"
}

ppsspp_apk="$acceptance_root/downloads/ppsspp-1.20.4.apk"
copy_or_download "$ppsspp_source" "$ppsspp_url" "$ppsspp_apk"
verify_artifact "$ppsspp_apk" "$ppsspp_size" "$ppsspp_sha256" "PPSSPP APK"

build_fixture fixture-first.pbp
build_fixture fixture-second.pbp
cmp "$acceptance_root/build/fixture-first.pbp" "$acceptance_root/build/fixture-second.pbp"
verify_artifact "$acceptance_root/build/fixture-first.pbp" "$fixture_size" "$fixture_sha256" "PSP fixture"

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
"$adb" -s "$emulator_serial" install -r "$ppsspp_apk" >/dev/null
version_name="$("$adb" -s "$emulator_serial" shell dumpsys package org.ppsspp.ppsspp | sed -n 's/.*versionName=//p' | head -n 1 | tr -d '\r')"
[[ "$version_name" == "v1.20.4" ]] || { echo "PPSSPP package version drifted" >&2; exit 1; }

debug_apk="$android_root/app/build/outputs/apk/debug/app-debug.apk"
test_apk="$android_root/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk"
"$adb" -s "$emulator_serial" install -r "$debug_apk" >/dev/null
"$adb" -s "$emulator_serial" install -r "$test_apk" >/dev/null
"$adb" -s "$emulator_serial" logcat -c
fixture_name="varkiv-ppsspp-fixture.pbp"
"$adb" -s "$emulator_serial" shell run-as org.varkiv.agent mkdir -p files
device_staging="/data/local/tmp/varkiv-ppsspp-fixture.pbp"
"$adb" -s "$emulator_serial" push "$acceptance_root/build/fixture-first.pbp" "$device_staging" >/dev/null
"$adb" -s "$emulator_serial" shell chmod 0644 "$device_staging"
"$adb" -s "$emulator_serial" shell run-as org.varkiv.agent cp "$device_staging" "files/$fixture_name"
private_fixture_size="$("$adb" -s "$emulator_serial" shell run-as org.varkiv.agent stat -c %s "files/$fixture_name" | tr -d '\r')"
[[ "$private_fixture_size" == "$fixture_size" ]] || { echo "private PSP fixture transfer drifted" >&2; exit 1; }
"$adb" -s "$emulator_serial" shell am instrument -w -r \
  -e class 'org.varkiv.agent.PpssppLaunchInstrumentedTest#launchesPinnedPpssppWithSafContentUri' \
  -e e2e_ppsspp_pbp_file "$fixture_name" \
  org.varkiv.agent.test/androidx.test.runner.AndroidJUnitRunner | tee "$acceptance_root/instrumentation.txt"

"$adb" -s "$emulator_serial" logcat -d -v threadtime > "$acceptance_root/logcat.txt"
grep -Fq 'OK (1 test)' "$acceptance_root/instrumentation.txt" || { echo "PPSSPP instrumentation did not pass" >&2; exit 1; }
if grep -Eq 'Permission Denial: opening provider org\.varkiv\.agent\.TestDocumentsProvider|FileNotFoundException.*varkiv-fixture|Failed to load.*varkiv-fixture' "$acceptance_root/logcat.txt"; then
  echo "PPSSPP logged a fixture permission or load failure" >&2
  exit 1
fi
"$adb" -s "$emulator_serial" exec-out run-as org.varkiv.agent cat files/ppsspp-real-launch.png > "$acceptance_root/ppsspp-real-launch.png"
[[ -s "$acceptance_root/ppsspp-real-launch.png" ]] || { echo "PPSSPP rendered-frame screenshot is unavailable" >&2; exit 1; }
screenshot_sha256="$(shasum -a 256 "$acceptance_root/ppsspp-real-launch.png" | awk '{print $1}')"

jq -n \
  --arg version "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" \
  --arg image "$image_package" \
  --arg ppsspp_sha256 "$ppsspp_sha256" \
  --arg pspdev_image "$pspdev_image" \
  --arg fixture_sha256 "$fixture_sha256" \
  --arg screenshot_sha256 "$screenshot_sha256" \
  '{format:"varkiv-android-ppsspp-acceptance-v1",application_version:$version,api_level:35,system_image:$image,ppsspp:{package:"org.ppsspp.ppsspp",version_name:"v1.20.4",sha256:$ppsspp_sha256},fixture:{license:"Apache-2.0",toolchain_image:$pspdev_image,sha256:$fixture_sha256,reproducible_build:true},saf_uri_grant:true,fixture_opened:true,rendered_frame:true,screenshot_sha256:$screenshot_sha256,cleanup_scope:"one-use-avd-downloaded-apk-and-generated-homebrew",privacy:{user_library_read:false,user_device_selected:false,nas_read:false,private_paths_reported:false}}' > "$acceptance_root/report.json"

printf 'android_ppsspp_e2e=passed version=%s api=35 abi=%s ppsspp=%s fixture=%s screenshot=%s\n' \
  "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" "$image_abi" \
  "${ppsspp_sha256:0:12}" "${fixture_sha256:0:12}" "${screenshot_sha256:0:12}"
