#!/usr/bin/env bash
set -euo pipefail

files=("$@")
if [ "${#files[@]}" -eq 0 ]; then
  shopt -s nullglob
  files=(.github/workflows/*.yml .github/workflows/*.yaml)
  shopt -u nullglob
fi

if [ "${#files[@]}" -eq 0 ]; then
  echo "error: no GitHub Actions workflow files found" >&2
  exit 1
fi

checked=0
failed=0
for file in "${files[@]}"; do
  if [ ! -f "$file" ]; then
    echo "error: workflow file does not exist: $file" >&2
    failed=1
    continue
  fi

  while IFS=: read -r line_number line; do
    spec="$(printf '%s\n' "$line" | sed -E 's/^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*([^[:space:]#]+).*/\2/')"
    case "$spec" in
      ./*|docker://*)
        continue
        ;;
    esac

    checked=$((checked + 1))
    if [[ ! "$spec" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$ ]]; then
      echo "error: $file:$line_number uses a mutable or non-SHA action reference: $spec" >&2
      failed=1
      continue
    fi
    if [[ ! "$line" =~ \#[[:space:]]+v[0-9]+([.][0-9]+){0,2}([[:space:]]|$) ]]; then
      echo "error: $file:$line_number must retain a human-readable version comment" >&2
      failed=1
    fi
  done < <(grep -nE '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*' "$file" || true)
done

if [ "$checked" -eq 0 ]; then
  echo "error: no external GitHub Actions references found" >&2
  exit 1
fi
if [ "$failed" -ne 0 ]; then
  exit 1
fi

printf 'github_actions_pinning=passed references=%s workflows=%s\n' "$checked" "${#files[@]}"
