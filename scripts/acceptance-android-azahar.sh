#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-android-azahar.sh [--vanilla-apk FILE] [--googleplay-apk FILE] [--keep]

Builds the repository's Apache-2.0 3DSX fixture with a digest-pinned devkitARM
container, installs both official Azahar 2126.0 Android variants in a one-use
API 35 ARM64 Google APIs AVD, and proves that Varkiv selects the installed
explicit package, grants the SAF ROM, and reaches the rendered fixture frame.
No personal library, NAS path, existing emulator profile, or device is read.
EOF
}

vanilla_source=""
googleplay_source=""
keep=0
while (($#)); do
  case "$1" in
    --vanilla-apk) vanilla_source="${2:-}"; shift 2 ;;
    --googleplay-apk) googleplay_source="${2:-}"; shift 2 ;;
    --keep) keep=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
android_root="$repo_root/clients/android"
fixture_source="$repo_root/testdata/3ds-homebrew"
sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
[[ -n "$sdk_root" ]] || { echo "ANDROID_SDK_ROOT or ANDROID_HOME is required" >&2; exit 1; }

adb="$sdk_root/platform-tools/adb"
emulator="$sdk_root/emulator/emulator"
avdmanager="$sdk_root/cmdline-tools/latest/bin/avdmanager"
for command_path in "$adb" "$emulator" "$avdmanager" "$android_root/gradlew"; do
  [[ -x "$command_path" ]] || { echo "required executable is unavailable: $command_path" >&2; exit 1; }
done
for command_name in curl docker jq perl shasum; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "required command is unavailable: $command_name" >&2; exit 1; }
done
[[ "$(uname -m)" == "arm64" || "$(uname -m)" == "aarch64" ]] || {
  echo "the pinned real-Azahar acceptance currently requires an ARM64 host/AVD" >&2
  exit 1
}

image_abi="arm64-v8a"
image_package="system-images;android-35;google_apis;$image_abi"
image_root="$sdk_root/system-images/android-35/google_apis/$image_abi"
[[ -f "$image_root/package.xml" ]] || { echo "missing pinned system image: $image_package" >&2; exit 1; }

azahar_version="2126.0"
vanilla_url="https://github.com/azahar-emu/azahar/releases/download/2126.0/azahar-android-vanilla-2126.0.apk"
vanilla_size=50206847
vanilla_sha256="112d354be2145c17fa26d354ba4336445b1549a50bd995140bdeaf7219d5b6ff"
googleplay_url="https://github.com/azahar-emu/azahar/releases/download/2126.0/azahar-android-googleplay-2126.0.apk"
googleplay_size=50206827
googleplay_sha256="6d78f4d5e71c6f0158225dcde0c7b0fb3b871bffd8c675b322ca9b9ba4006e21"
toolchain_image="devkitpro/devkitarm@sha256:116afba8df8453961de2936ffab20dd441edf4d682856c1ec8b0e53d7ed0bbf5"
fixture_size=70440
fixture_sha256="6e847cdfd3f9db0180787ef05ec73af798adb84e37611d076d04190b99e1571f"

acceptance_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-android-azahar.XXXXXX")"
chmod 700 "$acceptance_root"
export ANDROID_AVD_HOME="$acceptance_root/avd"
export ANDROID_EMULATOR_HOME="$acceptance_root/emulator-home"
mkdir -m 700 -p "$ANDROID_AVD_HOME" "$ANDROID_EMULATOR_HOME" "$acceptance_root/downloads" "$acceptance_root/build-a" "$acceptance_root/build-b"

avd_name="varkiv-azahar-e2e-$$"
emulator_pid=""
emulator_serial=""
cleanup() {
  local status=$?
  if [[ -n "$emulator_serial" ]]; then "$adb" -s "$emulator_serial" emu kill >/dev/null 2>&1 || true; fi
  if [[ -n "$emulator_pid" ]]; then wait "$emulator_pid" 2>/dev/null || true; fi
  if ((keep == 0)); then
    case "$acceptance_root" in
      "${TMPDIR:-/tmp}"/varkiv-android-azahar.*) rm -rf -- "$acceptance_root" ;;
      *) echo "refusing to remove unexpected acceptance root: $acceptance_root" >&2; status=1 ;;
    esac
  else
    printf 'retained_evidence=%s\n' "$acceptance_root" >&2
  fi
  return "$status"
}
trap cleanup EXIT INT TERM

copy_or_download() {
  local source_path="$1" url="$2" destination="$3"
  if [[ -n "$source_path" ]]; then
    [[ -f "$source_path" ]] || { echo "pinned local artifact is unavailable: $source_path" >&2; exit 1; }
    cp "$source_path" "$destination"
  else
    curl --fail --location --retry 3 --output "$destination" "$url"
  fi
}

verify_artifact() {
  local path="$1" expected_size="$2" expected_sha="$3" label="$4"
  local actual_size actual_sha
  actual_size="$(wc -c < "$path" | tr -d ' ')"
  actual_sha="$(shasum -a 256 "$path" | awk '{print $1}')"
  [[ "$actual_size" == "$expected_size" ]] || { echo "$label size drifted" >&2; exit 1; }
  [[ "$actual_sha" == "$expected_sha" ]] || { echo "$label SHA-256 drifted" >&2; exit 1; }
}

build_fixture() {
  local output_root="$1"
  docker run --rm --platform linux/arm64 --network none --cap-drop ALL \
    --security-opt no-new-privileges \
    --mount "type=bind,src=$fixture_source,dst=/source,readonly" \
    --mount "type=bind,src=$output_root,dst=/output" \
    "$toolchain_image" sh -euc \
    'cp -R /source /tmp/fixture; cd /tmp/fixture; make >/dev/null; cp varkiv-fixture.3dsx /output/'
}

dump_ui() {
  "$adb" -s "$emulator_serial" shell uiautomator dump /sdcard/varkiv-window.xml >/dev/null
  "$adb" -s "$emulator_serial" exec-out cat /sdcard/varkiv-window.xml
}

wait_for_text() {
  local value="$1" xml
  for _ in $(seq 1 30); do
    xml="$(dump_ui 2>/dev/null || true)"
    if [[ "$xml" == *"text=\"$value\""* ]]; then return 0; fi
    sleep 0.5
  done
  echo "Android UI did not expose expected text: $value" >&2
  return 1
}

tap_text() {
  local value="$1" xml bounds
  wait_for_text "$value"
  xml="$(dump_ui)"
  bounds="$(TAP_TEXT="$value" perl -0ne 'my $v=$ENV{TAP_TEXT}; if (/<node[^>]*text="\Q$v\E"[^>]*bounds="(\[[0-9]+,[0-9]+\]\[[0-9]+,[0-9]+\])"/) { print $1; exit }' <<<"$xml")"
  if [[ ! "$bounds" =~ \[([0-9]+),([0-9]+)\]\[([0-9]+),([0-9]+)\] ]]; then
    echo "unable to resolve Android UI bounds for: $value" >&2
    return 1
  fi
  "$adb" -s "$emulator_serial" shell input tap "$(( (BASH_REMATCH[1] + BASH_REMATCH[3]) / 2 ))" "$(( (BASH_REMATCH[2] + BASH_REMATCH[4]) / 2 ))"
  sleep 0.6
}

configure_azahar() {
  local package_name="$1" folder_name="$2"
  "$adb" -s "$emulator_serial" shell mkdir -p "/sdcard/$folder_name"
  if [[ "$package_name" == "org.azahar_emu.azahar" ]]; then
    "$adb" -s "$emulator_serial" shell appops set "$package_name" MANAGE_EXTERNAL_STORAGE allow
  fi
  "$adb" -s "$emulator_serial" shell am start -W -n "$package_name/org.citra.citra_emu.ui.main.MainActivity" >/dev/null
  tap_text "Get started"
  tap_text "Next"
  for _ in 1 2 3 4; do
    if [[ "$(dump_ui)" == *'text="Warning"'* ]]; then tap_text "Skip"; else break; fi
  done
  wait_for_text "Data Folders"
  tap_text "Select User Folder"
  tap_text "$folder_name"
  tap_text "USE THIS FOLDER"
  tap_text "ALLOW"
  tap_text "OK"
  wait_for_text "Data Folders"
  tap_text "Next"
  wait_for_text "Warning"
  tap_text "Skip"
  wait_for_text "Done"
  tap_text "Continue"
  wait_for_text "Applications"
  "$adb" -s "$emulator_serial" shell find "/sdcard/$folder_name" -maxdepth 1 -type d | grep -Fq "/sdcard/$folder_name/config" || {
    echo "Azahar user directory was not initialized" >&2
    exit 1
  }
}

run_variant() {
  local package_name="$1" label="$2" output_prefix="$3"
  "$adb" -s "$emulator_serial" shell run-as org.varkiv.agent mkdir -p files
  "$adb" -s "$emulator_serial" shell run-as org.varkiv.agent cp /data/local/tmp/varkiv-fixture.3dsx files/varkiv-fixture.3dsx
  "$adb" -s "$emulator_serial" shell am force-stop "$package_name"
  "$adb" -s "$emulator_serial" logcat -c
  "$adb" -s "$emulator_serial" shell am instrument -w -r \
    -e class 'org.varkiv.agent.AzaharLaunchInstrumentedTest#launchesPinnedAzaharVariantWithSafContentUri' \
    -e e2e_azahar_3dsx_file varkiv-fixture.3dsx \
    -e e2e_azahar_package "$package_name" \
    org.varkiv.agent.test/androidx.test.runner.AndroidJUnitRunner | tee "$acceptance_root/$output_prefix-instrumentation.txt"
  "$adb" -s "$emulator_serial" logcat -d -v threadtime > "$acceptance_root/$output_prefix-logcat.txt"
  grep -Fq 'OK (1 test)' "$acceptance_root/$output_prefix-instrumentation.txt" || { echo "$label instrumentation did not pass" >&2; exit 1; }
  grep -Fq 'varkiv-fixture.3dsx' "$acceptance_root/$output_prefix-logcat.txt" || { echo "$label did not log the 3DSX launch" >&2; exit 1; }
  if grep -Eq 'Permission Denial: opening provider org\.varkiv\.agent\.TestDocumentsProvider|FATAL EXCEPTION|Failed to load.*varkiv-fixture|FileNotFoundException.*varkiv-fixture' "$acceptance_root/$output_prefix-logcat.txt"; then
    echo "$label logged a fixture permission or load failure" >&2
    exit 1
  fi
  "$adb" -s "$emulator_serial" exec-out run-as org.varkiv.agent cat files/azahar-real-launch.png > "$acceptance_root/$output_prefix-real-launch.png"
  [[ -s "$acceptance_root/$output_prefix-real-launch.png" ]] || { echo "$label rendered-frame screenshot is unavailable" >&2; exit 1; }
}

vanilla_apk="$acceptance_root/downloads/azahar-vanilla.apk"
googleplay_apk="$acceptance_root/downloads/azahar-googleplay.apk"
copy_or_download "$vanilla_source" "$vanilla_url" "$vanilla_apk"
copy_or_download "$googleplay_source" "$googleplay_url" "$googleplay_apk"
verify_artifact "$vanilla_apk" "$vanilla_size" "$vanilla_sha256" "Azahar vanilla APK"
verify_artifact "$googleplay_apk" "$googleplay_size" "$googleplay_sha256" "Azahar Google Play APK"

build_fixture "$acceptance_root/build-a"
build_fixture "$acceptance_root/build-b"
cmp "$acceptance_root/build-a/varkiv-fixture.3dsx" "$acceptance_root/build-b/varkiv-fixture.3dsx"
verify_artifact "$acceptance_root/build-a/varkiv-fixture.3dsx" "$fixture_size" "$fixture_sha256" "3DSX fixture"

(
  cd "$android_root"
  ./gradlew --no-daemon assembleDebug assembleDebugAndroidTest
)

printf 'no\n' | "$avdmanager" create avd --name "$avd_name" --package "$image_package" --device pixel_2 >/dev/null
emulator_port=""
for candidate in $(seq 5588 2 5688); do
  if ! "$adb" devices | awk 'NR>1 {print $1}' | grep -Fxq "emulator-$candidate"; then emulator_port="$candidate"; break; fi
done
[[ -n "$emulator_port" ]] || { echo "no disposable emulator console port is available" >&2; exit 1; }
emulator_serial="emulator-$emulator_port"
"$emulator" -avd "$avd_name" -port "$emulator_port" -no-window -no-audio -no-boot-anim -no-snapshot -no-metrics -gpu swiftshader_indirect >"$acceptance_root/emulator.log" 2>&1 &
emulator_pid=$!

booted=0
for _ in $(seq 1 150); do
  if [[ "$("$adb" -s "$emulator_serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]]; then booted=1; break; fi
  if ! kill -0 "$emulator_pid" >/dev/null 2>&1; then tail -n 200 "$acceptance_root/emulator.log" >&2; echo "disposable Android emulator exited before boot" >&2; exit 1; fi
  sleep 2
done
((booted == 1)) || { tail -n 200 "$acceptance_root/emulator.log" >&2; echo "disposable Android emulator did not boot" >&2; exit 1; }

"$adb" -s "$emulator_serial" shell settings put global window_animation_scale 0
"$adb" -s "$emulator_serial" shell settings put global transition_animation_scale 0
"$adb" -s "$emulator_serial" shell settings put global animator_duration_scale 0
"$adb" -s "$emulator_serial" shell settings put secure immersive_mode_confirmations confirmed

debug_apk="$android_root/app/build/outputs/apk/debug/app-debug.apk"
test_apk="$android_root/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk"
"$adb" -s "$emulator_serial" install -r "$debug_apk" >/dev/null
"$adb" -s "$emulator_serial" install -r "$test_apk" >/dev/null
"$adb" -s "$emulator_serial" push "$acceptance_root/build-a/varkiv-fixture.3dsx" /data/local/tmp/varkiv-fixture.3dsx >/dev/null

"$adb" -s "$emulator_serial" install -r "$vanilla_apk" >/dev/null
configure_azahar "org.azahar_emu.azahar" "VarkivAzaharVanilla"
run_variant "org.azahar_emu.azahar" "Azahar vanilla" "vanilla"
"$adb" -s "$emulator_serial" uninstall org.azahar_emu.azahar >/dev/null

"$adb" -s "$emulator_serial" install -r "$googleplay_apk" >/dev/null
configure_azahar "io.github.lime3ds.android" "VarkivAzaharPlay"
run_variant "io.github.lime3ds.android" "Azahar Google Play" "googleplay"

vanilla_screenshot_sha256="$(shasum -a 256 "$acceptance_root/vanilla-real-launch.png" | awk '{print $1}')"
googleplay_screenshot_sha256="$(shasum -a 256 "$acceptance_root/googleplay-real-launch.png" | awk '{print $1}')"
[[ "$vanilla_screenshot_sha256" == "$googleplay_screenshot_sha256" ]] || { echo "Azahar variant rendered frames drifted" >&2; exit 1; }

jq -n \
  --arg version "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" \
  --arg image "$image_package" \
  --arg azahar_version "$azahar_version" \
  --arg vanilla_sha256 "$vanilla_sha256" \
  --arg googleplay_sha256 "$googleplay_sha256" \
  --arg toolchain_image "$toolchain_image" \
  --arg fixture_sha256 "$fixture_sha256" \
  --arg screenshot_sha256 "$vanilla_screenshot_sha256" \
  '{format:"varkiv-android-azahar-acceptance-v1",application_version:$version,api_level:35,system_image:$image,azahar:{version:$azahar_version,variants:[{package:"org.azahar_emu.azahar",sha256:$vanilla_sha256,transport:"granted-file-descriptor"},{package:"io.github.lime3ds.android",sha256:$googleplay_sha256,transport:"granted-content-uri"}],explicit_package_fallback:true},fixture:{license:"Apache-2.0",toolchain_image:$toolchain_image,sha256:$fixture_sha256,reproducible_build:true},saf_uri_grant:true,fixture_opened:true,rendered_frame:true,screenshot_sha256:$screenshot_sha256,cleanup_scope:"one-use-avd-downloaded-apks-and-generated-homebrew",privacy:{user_library_read:false,user_device_selected:false,nas_read:false,private_paths_reported:false}}' > "$acceptance_root/report.json"

printf 'android_azahar_e2e=passed version=%s api=35 abi=%s vanilla=%s googleplay=%s fixture=%s screenshot=%s\n' \
  "$(tr -d '\r\n' < "$repo_root/internal/buildinfo/VERSION")" "$image_abi" \
  "${vanilla_sha256:0:12}" "${googleplay_sha256:0:12}" "${fixture_sha256:0:12}" "${vanilla_screenshot_sha256:0:12}"
