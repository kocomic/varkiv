#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-sdkmanager-test.XXXXXX")"
chmod 700 "$test_root"
cleanup() {
  case "$test_root" in
    "${TMPDIR:-/tmp}"/varkiv-sdkmanager-test.*) rm -rf -- "$test_root" ;;
    *) echo "refusing to remove unexpected SDK manager test root" >&2; return 1 ;;
  esac
}
trap cleanup EXIT INT TERM

fake_sdkmanager="$test_root/sdkmanager"
counter="$test_root/counter"
arguments="$test_root/arguments"

printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'count=0' \
  '[[ ! -f "$FAKE_COUNTER" ]] || count="$(<"$FAKE_COUNTER")"' \
  'count=$((count + 1))' \
  'printf "%d\n" "$count" >"$FAKE_COUNTER"' \
  'printf "%s\n" "$@" >"$FAKE_ARGUMENTS"' \
  'if ((count <= FAKE_FAILURES)); then exit 42; fi' >"$fake_sdkmanager"
chmod 700 "$fake_sdkmanager"

run_installer() {
  SDKMANAGER_BIN="$fake_sdkmanager" \
    FAKE_COUNTER="$counter" \
    FAKE_ARGUMENTS="$arguments" \
    VARKIV_ANDROID_SDK_ATTEMPTS=3 \
    VARKIV_ANDROID_SDK_RETRY_DELAY_SECONDS=0 \
    FAKE_FAILURES="$1" \
    "$repository_root/scripts/install-android-sdk.sh" \
      'platforms;android-36' 'build-tools;36.0.0'
}

run_installer 2 >/dev/null
[[ "$(<"$counter")" == 3 ]] || { echo "retry count drifted" >&2; exit 1; }
[[ "$(sed -n '1p' "$arguments")" == 'platforms;android-36' ]] || { echo "first package drifted" >&2; exit 1; }
[[ "$(sed -n '2p' "$arguments")" == 'build-tools;36.0.0' ]] || { echo "second package drifted" >&2; exit 1; }

rm -f -- "$counter" "$arguments"
set +e
run_installer 3 >/dev/null 2>&1
failure_status=$?
set -e
[[ "$failure_status" == 42 && "$(<"$counter")" == 3 ]] || {
  echo "permanent failure contract drifted" >&2
  exit 1
}

rm -f -- "$counter" "$arguments"
set +e
SDKMANAGER_BIN="$fake_sdkmanager" \
  FAKE_COUNTER="$counter" \
  FAKE_ARGUMENTS="$arguments" \
  FAKE_FAILURES=0 \
  VARKIV_ANDROID_SDK_ATTEMPTS=0 \
  VARKIV_ANDROID_SDK_RETRY_DELAY_SECONDS=0 \
  "$repository_root/scripts/install-android-sdk.sh" 'platforms;android-36' >/dev/null 2>&1
invalid_status=$?
set -e
[[ "$invalid_status" == 2 && ! -e "$counter" ]] || { echo "invalid configuration reached sdkmanager" >&2; exit 1; }

printf '%s\n' 'android_sdk_installer_tests=passed retry=passed permanent_failure=passed validation=passed'
