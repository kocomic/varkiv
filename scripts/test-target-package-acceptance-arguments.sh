#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-target-package-arguments.XXXXXX")
cleanup() {
  if [ -d "$test_root" ]; then
    case "$(basename "$test_root")" in
      varkiv-target-package-arguments.*) rm -r -- "$test_root" ;;
      *) echo "error: refusing to remove unexpected test root" >&2 ;;
    esac
  fi
}
trap cleanup EXIT

help_root="$test_root/help-output"
help_output=$(VARKIV_TARGET_ACCEPTANCE_ROOT="$help_root" ./scripts/acceptance-target-packages.sh --help)
printf '%s' "$help_output" | grep -Fq 'Usage: scripts/acceptance-target-packages.sh'
test ! -e "$help_root"

unknown_root="$test_root/unknown-output"
unknown_log="$test_root/unknown.log"
if VARKIV_TARGET_ACCEPTANCE_ROOT="$unknown_root" ./scripts/acceptance-target-packages.sh --unexpected >"$unknown_log" 2>&1; then
  echo "error: unknown target-package acceptance argument was accepted" >&2
  exit 1
fi
grep -Fq 'error: unknown argument: --unexpected' "$unknown_log"
test ! -e "$unknown_root"

printf 'target_package_acceptance_arguments=passed help_is_read_only=true unknown_rejected=true\n'
