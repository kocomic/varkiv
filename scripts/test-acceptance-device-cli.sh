#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-acceptance-cli.XXXXXX")"
chmod 700 "${test_root}"
cleanup() {
  case "${test_root}" in
    "${TMPDIR:-/tmp}"/varkiv-acceptance-cli.*) rm -rf -- "${test_root}" ;;
    *) echo "refusing to remove unexpected CLI test root" >&2; return 1 ;;
  esac
}
trap cleanup EXIT INT TERM

scripts=(
  acceptance-android-emulator.sh
  acceptance-android-ppsspp.sh
  acceptance-android-retroarch.sh
  acceptance-compose-web-emulation.sh
  acceptance-container.sh
  acceptance-device-knulli-hooks.sh
  acceptance-device-muos-apps.sh
  acceptance-device-onionos-apps.sh
  acceptance-device-ppsspp-save.sh
  acceptance-device-runtime-bridge.sh
  acceptance-device-steamos-save.sh
  acceptance-device-windows-save.sh
  acceptance-open-rom-roundtrip.sh
  acceptance-release-reproducibility.sh
  acceptance-roundtrip.sh
  acceptance-snes-native-compat.sh
  acceptance-target-packages.sh
  acceptance-web-emulation.mjs
)

for script_name in "${scripts[@]}"; do
  script="${project_root}/scripts/${script_name}"
  help_output="$(TMPDIR="${test_root}" "${script}" --help)"
  [[ "${help_output}" == Usage:* ]] || { echo "missing help output: ${script_name}" >&2; exit 1; }
  set +e
  invalid_output="$(TMPDIR="${test_root}" "${script}" --invalid 2>&1)"
  invalid_status=$?
  set -e
  [[ "${invalid_status}" == 2 && "${invalid_output}" == *"error:"* ]] || {
    echo "invalid argument contract drifted: ${script_name}" >&2
    exit 1
  }
done

if rg -n '\.schema_version == [0-9]+' "${project_root}/scripts"/acceptance-*.sh; then
  echo "acceptance scripts must compare schema_version with supported_schema_version" >&2
  exit 1
fi

[[ -z "$(find "${test_root}" -mindepth 1 -print -quit)" ]] || {
  echo "help or invalid-argument checks created acceptance resources" >&2
  exit 1
}

printf 'acceptance_device_cli_tests=passed scripts=%d cleanup=passed\n' "${#scripts[@]}"
