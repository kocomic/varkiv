#!/usr/bin/env bash

set -euo pipefail

attempts="${VARKIV_ANDROID_SDK_ATTEMPTS:-3}"
delay_seconds="${VARKIV_ANDROID_SDK_RETRY_DELAY_SECONDS:-10}"
sdkmanager_command="${SDKMANAGER_BIN:-sdkmanager}"

if [[ ! "$attempts" =~ ^[1-5]$ ]]; then
  echo "error: VARKIV_ANDROID_SDK_ATTEMPTS must be between 1 and 5" >&2
  exit 2
fi
if [[ ! "$delay_seconds" =~ ^[0-9]+$ ]] || ((delay_seconds > 60)); then
  echo "error: VARKIV_ANDROID_SDK_RETRY_DELAY_SECONDS must be between 0 and 60" >&2
  exit 2
fi
if (($# == 0)); then
  echo "error: at least one Android SDK package is required" >&2
  exit 2
fi
for package in "$@"; do
  if [[ -z "$package" || "$package" == -* ]]; then
    echo "error: Android SDK package names must be non-empty and cannot be options" >&2
    exit 2
  fi
done

command -v "$sdkmanager_command" >/dev/null 2>&1 || {
  echo "error: sdkmanager is unavailable" >&2
  exit 2
}

status=1
for ((attempt = 1; attempt <= attempts; attempt++)); do
  printf 'android_sdk_install_attempt=%d/%d packages=%d\n' "$attempt" "$attempts" "$#"
  if "$sdkmanager_command" "$@"; then
    printf 'android_sdk_install=passed attempt=%d packages=%d\n' "$attempt" "$#"
    exit 0
  else
    status=$?
  fi

  printf 'warning: sdkmanager failed (attempt=%d/%d status=%d)\n' "$attempt" "$attempts" "$status" >&2
  if ((attempt < attempts && delay_seconds > 0)); then
    sleep "$delay_seconds"
  fi
done

printf 'error: sdkmanager failed after %d attempts (status=%d)\n' "$attempts" "$status" >&2
exit "$status"
