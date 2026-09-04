#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
checker="$repository_root/scripts/check-source-hygiene.sh"
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-source-hygiene-test.XXXXXX")

cleanup() {
  rm -rf -- "$fixture_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$fixture_root/scripts"
cp "$checker" "$fixture_root/scripts/check-source-hygiene.sh"
chmod 0700 "$fixture_root/scripts/check-source-hygiene.sh"
git -C "$fixture_root" init -q
printf 'safe synthetic source\n' > "$fixture_root/README.md"

run_checker() {
  (cd "$fixture_root" && ./scripts/check-source-hygiene.sh)
}

run_checker >/dev/null

printf 'VARKIV_IMAGE=ghcr.io/owner/repository:edge\n' > "$fixture_root/.env.ghcr.example"
run_checker >/dev/null
rm -f -- "$fixture_root/.env.ghcr.example"

printf '/%s/%s\n' 'Users' 'private-owner/Games/library' > "$fixture_root/leaked-path.txt"
if run_checker >/dev/null 2>&1; then
  echo 'error: machine-specific path was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/leaked-path.txt"

printf 'not a real rom\n' > "$fixture_root/private.gba"
if run_checker >/dev/null 2>&1; then
  echo 'error: unapproved ROM-like file was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/private.gba"

printf 'not a real wrapped archive\n' > "$fixture_root/private.7z.tkzlm"
if run_checker >/dev/null 2>&1; then
  echo 'error: unapproved wrapped archive was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/private.7z.tkzlm"

printf 'not a private cover\n' > "$fixture_root/private-cover.png"
if run_checker >/dev/null 2>&1; then
  echo 'error: unapproved media file was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/private-cover.png"

mkdir -p "$fixture_root/testdata/neutral/gba"
printf 'drifted synthetic fixture\n' > "$fixture_root/testdata/neutral/gba/recovery.gba"
if run_checker >/dev/null 2>&1; then
  echo 'error: drifted approved fixture was accepted' >&2
  exit 1
fi
rm -rf -- "$fixture_root/testdata"

mkdir -p "$fixture_root/testdata/psp-homebrew"
printf 'drifted PSP fixture documentation\n' > "$fixture_root/testdata/psp-homebrew/README.md"
if run_checker >/dev/null 2>&1; then
  echo 'error: drifted approved ROM-like source document was accepted' >&2
  exit 1
fi
rm -rf -- "$fixture_root/testdata"

printf 'TOKEN=private\n' > "$fixture_root/.env.local"
if run_checker >/dev/null 2>&1; then
  echo 'error: machine-local credential file was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/.env.local"

printf '%s%s\n' '-----BEGIN ' 'PRIVATE KEY-----' > "$fixture_root/private-key.txt"
if run_checker >/dev/null 2>&1; then
  echo 'error: private key marker was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/private-key.txt"

printf '%s%s%s\n' 'gh' 'p_' 'AAAAAAAAAAAAAAAAAAAA' > "$fixture_root/provider-token.txt"
if run_checker >/dev/null 2>&1; then
  echo 'error: provider credential signature was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/provider-token.txt"

printf 'private database fixture\n' > "$fixture_root/library.db"
if run_checker >/dev/null 2>&1; then
  echo 'error: database artifact was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/library.db"

printf 'private save fixture\n' > "$fixture_root/primary.sav"
if run_checker >/dev/null 2>&1; then
  echo 'error: save artifact was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/primary.sav"

ln -s README.md "$fixture_root/linked-source"
if run_checker >/dev/null 2>&1; then
  echo 'error: symbolic link was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/linked-source"

dd if=/dev/zero of="$fixture_root/oversized-source.bin" bs=1 count=0 seek=8388609 2>/dev/null
if run_checker >/dev/null 2>&1; then
  echo 'error: oversized source file was accepted' >&2
  exit 1
fi
rm -f -- "$fixture_root/oversized-source.bin"

run_checker >/dev/null
printf 'source_hygiene_negative_tests=passed cases=13 cleanup=passed\n'
