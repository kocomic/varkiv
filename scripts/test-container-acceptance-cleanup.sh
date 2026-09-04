#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

image=""
port=18083

usage() {
  cat <<'EOF'
Usage: scripts/test-container-acceptance-cleanup.sh --image TAG [--port PORT]

Hold one loopback port with a child fixture server, require container
acceptance to fail at startup, and prove its exact temporary Docker resources
were cleaned without stopping the existing listener.
EOF
}

while (($# > 0)); do
  case "$1" in
    --image)
      (($# >= 2)) || { echo "error: --image requires a value" >&2; exit 2; }
      image=$2
      shift 2
      ;;
    --port)
      (($# >= 2)) || { echo "error: --port requires a value" >&2; exit 2; }
      port=$2
      shift 2
      ;;
    -h|--help)
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

[[ -n "$image" && "$image" != -* ]] || { echo "error: --image is required" >&2; exit 2; }
[[ "$port" =~ ^[0-9]+$ ]] || { echo "error: --port must be an integer" >&2; exit 2; }
((port >= 1024 && port <= 65535)) || { echo "error: --port must be between 1024 and 65535" >&2; exit 2; }

for command_name in curl docker python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "error: required command is unavailable: $command_name" >&2
    exit 2
  }
done
docker image inspect "$image" >/dev/null

listener_log=$(mktemp "${TMPDIR:-/tmp}/varkiv-container-cleanup.XXXXXX")
listener_pid=""
cleanup_listener() {
  if [ -n "$listener_pid" ]; then
    kill "$listener_pid" >/dev/null 2>&1 || true
    wait "$listener_pid" >/dev/null 2>&1 || true
  fi
  rm -f -- "$listener_log"
}
trap cleanup_listener EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

python3 -m http.server "$port" --bind 127.0.0.1 >"$listener_log" 2>&1 &
listener_pid=$!

listening=false
attempt=0
while ((attempt < 20)); do
  if curl --fail --silent --show-error --max-time 1 "http://127.0.0.1:$port/" >/dev/null 2>&1; then
    listening=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [ "$listening" != true ]; then
  echo "error: fixture listener did not start" >&2
  exit 1
fi

set +e
failure_output=$(./scripts/acceptance-container.sh --image "$image" --port "$port" --skip-build 2>&1)
failure_status=$?
set -e

if [ "$failure_status" -eq 0 ]; then
  echo "error: occupied-port acceptance unexpectedly succeeded" >&2
  exit 1
fi
case "$failure_output" in
  *'container_acceptance=failed stage=container-start'*) ;;
  *)
    echo "error: acceptance failed outside the expected startup boundary" >&2
    printf '%s\n' "$failure_output" >&2
    exit 1
    ;;
esac

if docker ps -a --format '{{.Names}}' | grep -Eq '^varkiv-container-acceptance-'; then
  echo "error: failed acceptance left a container" >&2
  exit 1
fi
if docker volume ls --format '{{.Name}}' | grep -Eq '^varkiv-container-(data|backup|restore)-'; then
  echo "error: failed acceptance left a volume" >&2
  exit 1
fi
curl --fail --silent --show-error --max-time 1 "http://127.0.0.1:$port/" >/dev/null

printf 'container_acceptance_failure_cleanup=passed image=%s port=%s existing_listener=preserved\n' "$image" "$port"
