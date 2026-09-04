#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-anonymous-container-test.XXXXXX")"
chmod 700 "$test_root"
cleanup() {
  case "$test_root" in
    "${TMPDIR:-/tmp}"/varkiv-anonymous-container-test.*) rm -rf -- "$test_root" ;;
    *) echo "refusing to remove unexpected anonymous-container test root" >&2; return 1 ;;
  esac
}
trap cleanup EXIT INT TERM

fake_bin="$test_root/bin"
mkdir -m 700 "$fake_bin"
fake_docker="$fake_bin/docker"
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'if [[ "$1" == logout ]]; then exit 0; fi' \
  '[[ "$1" == run ]] || exit 90' \
  'platform=""' \
  'while (($#)); do' \
  '  if [[ "$1" == --platform ]]; then platform="$2"; shift 2; continue; fi' \
  '  shift' \
  'done' \
  'key="${platform#linux/}"' \
  'counter="$FAKE_STATE/$key"' \
  'count=0' \
  '[[ ! -f "$counter" ]] || count="$(<"$counter")"' \
  'count=$((count + 1))' \
  'printf "%d\n" "$count" >"$counter"' \
  'if ((count <= FAKE_FAILURES)); then echo "registry tag is not visible yet" >&2; exit 44; fi' \
  'printf "%s\n" "${FAKE_OUTPUT:-Varkiv 0.1.0-preview.4}"' >"$fake_docker"
chmod 700 "$fake_docker"

fake_sleep="$fake_bin/sleep"
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "%s\n" "$1" >>"$FAKE_SLEEP_LOG"' >"$fake_sleep"
chmod 700 "$fake_sleep"

image_ref="ghcr.io/kocomic/varkiv@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
run_verifier() {
  verifier_platform=()
  if [[ -n "${2:-}" ]]; then
    verifier_platform=(--platform "$2")
  fi
  PATH="$fake_bin:$PATH" \
    FAKE_STATE="$test_root/state" \
    FAKE_FAILURES="$1" \
    VARKIV_ANONYMOUS_PULL_ATTEMPTS=3 \
    VARKIV_ANONYMOUS_PULL_DELAY_SECONDS=0 \
    "$repository_root/scripts/verify-anonymous-container.sh" \
      --image "$image_ref" --version 0.1.0-preview.4 "${verifier_platform[@]}"
}

mkdir -m 700 "$test_root/state"
default_sleep_log="$test_root/default-sleeps"
PATH="$fake_bin:$PATH" \
  FAKE_STATE="$test_root/state" \
  FAKE_FAILURES=19 \
  FAKE_SLEEP_LOG="$default_sleep_log" \
  "$repository_root/scripts/verify-anonymous-container.sh" \
    --image "$image_ref" --version 0.1.0-preview.4 >/dev/null 2>&1
[[ "$(<"$test_root/state/amd64")" == 20 && "$(<"$test_root/state/arm64")" == 20 ]] || {
  echo "anonymous default retry window drifted" >&2
  exit 1
}
[[ "$(wc -l <"$default_sleep_log" | tr -d ' ')" == 38 ]] || {
  echo "anonymous default retry delay count drifted" >&2
  exit 1
}
[[ "$(sort -u "$default_sleep_log")" == 30 ]] || {
  echo "anonymous default retry delay drifted" >&2
  exit 1
}

rm -rf -- "$test_root/state"
mkdir -m 700 "$test_root/state"
run_verifier 2 >/dev/null 2>&1
[[ "$(<"$test_root/state/amd64")" == 3 && "$(<"$test_root/state/arm64")" == 3 ]] || {
  echo "anonymous propagation retry contract drifted" >&2
  exit 1
}

rm -rf -- "$test_root/state"
mkdir -m 700 "$test_root/state"
run_verifier 0 linux/arm64 >/dev/null 2>&1
[[ ! -e "$test_root/state/amd64" && "$(<"$test_root/state/arm64")" == 1 ]] || {
  echo "anonymous single-platform isolation contract drifted" >&2
  exit 1
}

rm -rf -- "$test_root/state"
mkdir -m 700 "$test_root/state"
set +e
run_verifier 3 >/dev/null 2>&1
permanent_status=$?
set -e
[[ "$permanent_status" == 1 && "$(<"$test_root/state/amd64")" == 3 && ! -e "$test_root/state/arm64" ]] || {
  echo "anonymous permanent failure contract drifted" >&2
  exit 1
}

rm -rf -- "$test_root/state"
mkdir -m 700 "$test_root/state"
set +e
PATH="$fake_bin:$PATH" \
  FAKE_STATE="$test_root/state" \
  FAKE_FAILURES=0 \
  FAKE_OUTPUT='unexpected version' \
  VARKIV_ANONYMOUS_PULL_ATTEMPTS=3 \
  VARKIV_ANONYMOUS_PULL_DELAY_SECONDS=0 \
  "$repository_root/scripts/verify-anonymous-container.sh" \
    --image "$image_ref" --version 0.1.0-preview.4 >/dev/null 2>&1
mismatch_status=$?
set -e
[[ "$mismatch_status" == 1 && "$(<"$test_root/state/amd64")" == 1 ]] || {
  echo "anonymous output mismatch must fail without retry" >&2
  exit 1
}

set +e
VARKIV_ANONYMOUS_PULL_ATTEMPTS=31 \
  "$repository_root/scripts/verify-anonymous-container.sh" \
    --image "$image_ref" --version 0.1.0-preview.4 >/dev/null 2>&1
invalid_attempts_status=$?
VARKIV_ANONYMOUS_PULL_DELAY_SECONDS=61 \
  "$repository_root/scripts/verify-anonymous-container.sh" \
    --image "$image_ref" --version 0.1.0-preview.4 >/dev/null 2>&1
invalid_delay_status=$?
"$repository_root/scripts/verify-anonymous-container.sh" \
  --image "$image_ref" --version 0.1.0-preview.4 --platform linux/386 >/dev/null 2>&1
invalid_platform_status=$?
set -e
[[ "$invalid_attempts_status" == 2 && "$invalid_delay_status" == 2 && "$invalid_platform_status" == 2 ]] || {
  echo "anonymous retry bounds accepted invalid configuration" >&2
  exit 1
}

printf '%s\n' 'anonymous_container_tests=passed default_window=20x30s propagation_retry=passed platform_isolation=passed permanent_failure=passed output_identity=passed invalid_config=passed'
