#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/verify-android-release-signatures.sh --apk FILE --aab FILE

Require an APK Signature Scheme signature on FILE.apk and a cryptographically
valid JAR signature on FILE.aab. The AAB check also requires the signature
metadata that jarsigner alone does not require when verifying an unsigned JAR.
EOF
}

if [[ $# -eq 1 && "$1" == "--help" ]]; then
  usage
  exit 0
fi

apk=""
aab=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --apk)
      [[ $# -ge 2 ]] || { echo "error: --apk requires a file" >&2; usage >&2; exit 2; }
      apk="$2"
      shift 2
      ;;
    --aab)
      [[ $# -ge 2 ]] || { echo "error: --aab requires a file" >&2; usage >&2; exit 2; }
      aab="$2"
      shift 2
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$apk" && -n "$aab" ]] || { echo "error: --apk and --aab are required" >&2; usage >&2; exit 2; }
[[ -f "$apk" && ! -L "$apk" ]] || { echo "error: APK must be a regular non-symlink file" >&2; exit 1; }
[[ -f "$aab" && ! -L "$aab" ]] || { echo "error: AAB must be a regular non-symlink file" >&2; exit 1; }

for command_name in unzip jarsigner awk; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "error: missing command: $command_name" >&2; exit 1; }
done

apksigner_command="${APKSIGNER:-}"
if [[ -z "$apksigner_command" ]]; then
  apksigner_command="$(command -v apksigner || true)"
fi
[[ -n "$apksigner_command" && -x "$apksigner_command" ]] || { echo "error: apksigner is unavailable; set APKSIGNER to its executable path" >&2; exit 1; }

"$apksigner_command" verify --verbose --print-certs "$apk" >/dev/null

has_aab_entry() {
  local expression="$1"
  unzip -Z1 "$aab" | awk -v expression="$expression" '$0 ~ expression { found=1 } END { exit(found ? 0 : 1) }'
}

has_aab_entry '^META-INF/MANIFEST[.]MF$' || { echo "error: AAB has no signed JAR manifest" >&2; exit 1; }
has_aab_entry '^META-INF/[^/]+[.]SF$' || { echo "error: AAB has no JAR signature file" >&2; exit 1; }
has_aab_entry '^META-INF/[^/]+[.](RSA|DSA|EC)$' || { echo "error: AAB has no JAR signature block" >&2; exit 1; }
jarsigner -verify -certs "$aab" >/dev/null 2>&1 || { echo "error: AAB JAR signature verification failed" >&2; exit 1; }

echo "android_release_signatures=passed apk_scheme=verified aab_jar_signature=verified"
