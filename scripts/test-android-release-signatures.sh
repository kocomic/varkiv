#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
verifier="${project_root}/scripts/verify-android-release-signatures.sh"

help_output="$($verifier --help)"
[[ "$help_output" == Usage:* ]] || { echo "signature verifier help contract failed" >&2; exit 1; }
if "$verifier" --unknown >/dev/null 2>&1; then
  echo "signature verifier accepted an unknown argument" >&2
  exit 1
fi

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/varkiv-android-signatures.XXXXXX")"
cleanup() {
  rm -rf -- "$fixture_root"
}
trap cleanup EXIT
chmod 700 "$fixture_root"

for command_name in jar jarsigner keytool cp chmod; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "missing command: $command_name" >&2; exit 1; }
done

mkdir -p "$fixture_root/payload" "$fixture_root/bin"
printf '%s\n' 'fixture bundle payload' > "$fixture_root/payload/resource.txt"
printf '%s\n' 'fixture apk payload' > "$fixture_root/signed.apk"
printf '%s\n' 'fixture unsigned apk payload' > "$fixture_root/unsigned.apk"

cat > "$fixture_root/bin/apksigner" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
candidate="${!#}"
[[ "$(basename "$candidate")" == "signed.apk" ]]
EOF
chmod 700 "$fixture_root/bin/apksigner"

keytool -genkeypair -noprompt \
  -keystore "$fixture_root/fixture.jks" \
  -storepass fixture-password \
  -keypass fixture-password \
  -alias fixture \
  -keyalg RSA \
  -keysize 2048 \
  -validity 2 \
  -dname 'CN=Varkiv Signature Test, OU=Test, O=Example, L=Test, ST=Test, C=ZZ' >/dev/null 2>&1

jar --create --file "$fixture_root/unsigned.aab" -C "$fixture_root/payload" .
cp "$fixture_root/unsigned.aab" "$fixture_root/signed.aab"
jarsigner \
  -keystore "$fixture_root/fixture.jks" \
  -storepass fixture-password \
  -keypass fixture-password \
  "$fixture_root/signed.aab" fixture >/dev/null 2>&1

APKSIGNER="$fixture_root/bin/apksigner" "$verifier" \
  --apk "$fixture_root/signed.apk" \
  --aab "$fixture_root/signed.aab" >/dev/null

if APKSIGNER="$fixture_root/bin/apksigner" "$verifier" \
  --apk "$fixture_root/signed.apk" \
  --aab "$fixture_root/unsigned.aab" >/dev/null 2>&1; then
  echo "signature verifier accepted an unsigned AAB" >&2
  exit 1
fi

cp "$fixture_root/signed.aab" "$fixture_root/tampered.aab"
printf '%s\n' 'tampered bundle payload' > "$fixture_root/payload/resource.txt"
jar --update --file "$fixture_root/tampered.aab" -C "$fixture_root/payload" resource.txt
if APKSIGNER="$fixture_root/bin/apksigner" "$verifier" \
  --apk "$fixture_root/signed.apk" \
  --aab "$fixture_root/tampered.aab" >/dev/null 2>&1; then
  echo "signature verifier accepted a tampered AAB" >&2
  exit 1
fi

if APKSIGNER="$fixture_root/bin/apksigner" "$verifier" \
  --apk "$fixture_root/unsigned.apk" \
  --aab "$fixture_root/signed.aab" >/dev/null 2>&1; then
  echo "signature verifier accepted an unsigned APK" >&2
  exit 1
fi

echo "android_release_signature_tests=passed signed=1 unsigned_aab_rejected=1 tampered_aab_rejected=1 unsigned_apk_rejected=1"
