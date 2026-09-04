#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
temp_base="${TMPDIR:-/tmp}"
fixture_root="$(mktemp -d "${temp_base%/}/varkiv-actions-pinning.XXXXXX")"
case "$fixture_root" in
  "${temp_base%/}"/varkiv-actions-pinning.*) ;;
  *)
    echo "error: unsafe fixture root" >&2
    exit 1
    ;;
esac
cleanup() {
  rm -rf -- "$fixture_root"
}
trap cleanup EXIT

sha=0123456789abcdef0123456789abcdef01234567
printf '%s\n' \
  'name: good' \
  'jobs:' \
  '  verify:' \
  '    steps:' \
  "      - uses: example/action@${sha} # v1" \
  '      - uses: ./local-action' > "$fixture_root/good.yml"
"$script_dir/check-github-actions-pinning.sh" "$fixture_root/good.yml" >/dev/null

assert_rejected() {
  name="$1"
  reference="$2"
  comment="$3"
  printf '%s\n' \
    'name: bad' \
    'jobs:' \
    '  verify:' \
    '    steps:' \
    "      - uses: ${reference}${comment}" > "$fixture_root/$name.yml"
  if "$script_dir/check-github-actions-pinning.sh" "$fixture_root/$name.yml" >/dev/null 2>&1; then
    echo "error: unsafe GitHub Action fixture was accepted: $name" >&2
    exit 1
  fi
}

assert_rejected mutable-tag 'example/action@v1' ''
assert_rejected short-sha 'example/action@0123456789abcdef' ' # v1'
assert_rejected missing-version-comment "example/action@${sha}" ''

printf 'github_actions_pinning_tests=passed negative_cases=3\n'
