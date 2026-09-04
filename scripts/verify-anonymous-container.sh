#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/verify-anonymous-container.sh --image IMAGE@sha256:DIGEST --version VERSION

Logs out of GHCR, then proves that the immutable image can be pulled and run
without credentials on both linux/amd64 and linux/arm64. Registry propagation
failures are retried within a fixed bound; output mismatches fail immediately.
EOF
}

image_ref=""
version=""
while (($#)); do
  case "$1" in
    --image)
      image_ref="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ "$image_ref" =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$ ]] || {
  echo "error: --image must be an immutable lowercase GHCR digest reference" >&2
  exit 2
}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || {
  echo "error: --version must be a semantic version without a v prefix" >&2
  exit 2
}

attempts="${VARKIV_ANONYMOUS_PULL_ATTEMPTS:-6}"
delay_seconds="${VARKIV_ANONYMOUS_PULL_DELAY_SECONDS:-10}"
if [[ ! "$attempts" =~ ^([1-9]|10)$ ]]; then
  echo "error: VARKIV_ANONYMOUS_PULL_ATTEMPTS must be between 1 and 10" >&2
  exit 2
fi
if [[ ! "$delay_seconds" =~ ^[0-9]+$ ]] || ((delay_seconds > 60)); then
  echo "error: VARKIV_ANONYMOUS_PULL_DELAY_SECONDS must be between 0 and 60" >&2
  exit 2
fi
command -v docker >/dev/null 2>&1 || { echo "error: docker is unavailable" >&2; exit 2; }

diagnostic_log="$(mktemp "${TMPDIR:-/tmp}/varkiv-anonymous-pull.XXXXXX")"
cleanup() {
  rm -f -- "$diagnostic_log"
}
trap cleanup EXIT INT TERM

docker logout ghcr.io >/dev/null 2>&1 || true
expected="Varkiv $version"
for platform in linux/amd64 linux/arm64; do
  verified=0
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    : >"$diagnostic_log"
    output=""
    status=0
    output="$(docker run --rm --pull always --platform "$platform" "$image_ref" version 2>"$diagnostic_log")" || status=$?
    if ((status == 0)); then
      if [[ "$output" != "$expected" ]]; then
        printf 'error: anonymous %s image output mismatch: expected %q, received %q\n' "$platform" "$expected" "$output" >&2
        exit 1
      fi
      printf 'anonymous_container=passed platform=%s attempt=%d\n' "$platform" "$attempt"
      verified=1
      break
    fi

    printf 'warning: anonymous %s pull/run failed (attempt=%d/%d status=%d)\n' "$platform" "$attempt" "$attempts" "$status" >&2
    tail -n 20 "$diagnostic_log" >&2 || true
    if ((attempt < attempts && delay_seconds > 0)); then
      sleep "$delay_seconds"
    fi
  done
  ((verified == 1)) || {
    printf 'error: anonymous %s pull/run failed after %d attempts\n' "$platform" "$attempts" >&2
    exit 1
  }
done
