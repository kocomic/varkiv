#!/usr/bin/env bash

set -euo pipefail
umask 077

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repository_root"

version=$(tr -d '\r\n' < internal/buildinfo/VERSION)
image="varkiv:$version"
port=18085
skip_build=false

usage() {
  cat <<'EOF'
Usage: scripts/acceptance-container.sh [--image TAG] [--port PORT] [--skip-build]

Build and verify one isolated Varkiv container using repository fixtures only.
The script creates uniquely named temporary Docker volumes and removes only
those volumes and its exact container, including on failure. It never mounts a
production state volume, user ROM library, NAS path, media directory, or save.
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
    --skip-build)
      skip_build=true
      shift
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

[[ "$port" =~ ^[0-9]+$ ]] || { echo "error: --port must be an integer" >&2; exit 2; }
((port >= 1024 && port <= 65535)) || { echo "error: --port must be between 1024 and 65535" >&2; exit 2; }
[[ -n "$image" && "$image" != -* ]] || { echo "error: --image must be a non-empty Docker image reference" >&2; exit 2; }

for command_name in curl docker openssl python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "error: required command is unavailable: $command_name" >&2
    exit 2
  }
done

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "error: sha256sum or shasum is required" >&2
    return 2
  fi
}

test -d testdata
test -f LICENSE

if [ "$skip_build" = false ]; then
  docker build --pull -t "$image" .
else
  docker image inspect "$image" >/dev/null
fi

test "$(docker run --rm "$image" version)" = "Varkiv $version"
version_identity=$(docker run --rm "$image" version --json)
EXPECTED_VERSION="$version" VERSION_IDENTITY_JSON="$version_identity" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["VERSION_IDENTITY_JSON"])
assert payload == {
    "format": "varkiv-version-v1",
    "application_version": os.environ["EXPECTED_VERSION"],
}, payload
PY
test "$(docker image inspect "$image" --format '{{.Config.User}}')" = "10001:10001"
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.licenses"}}')" = "Apache-2.0"
health_test=$(docker image inspect "$image" --format '{{json .Config.Healthcheck.Test}}')
case "$health_test" in
  *'/api/v1/health/ready'*) ;;
  *) echo "error: image health check does not use the readiness endpoint" >&2; exit 1 ;;
esac

expected_license_hash=$(sha256_file LICENSE)
image_license_hash=$(docker run --rm --entrypoint sha256sum "$image" /usr/share/licenses/varkiv/LICENSE | awk '{print $1}')
test "$image_license_hash" = "$expected_license_hash"
license_files=$(docker run --rm --entrypoint sh "$image" -c 'find /usr/share/doc/varkiv/THIRD_PARTY_LICENSES -type f | wc -l | tr -d " "')
test "$license_files" = 18

suffix="$(date +%s)-$$-$(openssl rand -hex 4)"
container="varkiv-container-acceptance-$suffix"
data_volume="varkiv-container-data-$suffix"
backup_volume="varkiv-container-backup-$suffix"
restore_volume="varkiv-container-restore-$suffix"
created_container=false
created_data=false
created_backup=false
created_restore=false
stage=resource-setup

cleanup_resources() {
  if [ "$created_container" = true ]; then
    docker rm -f "$container" >/dev/null 2>&1 || true
    created_container=false
  fi
  if [ "$created_data" = true ]; then
    docker volume rm "$data_volume" >/dev/null 2>&1 || true
    created_data=false
  fi
  if [ "$created_backup" = true ]; then
    docker volume rm "$backup_volume" >/dev/null 2>&1 || true
    created_backup=false
  fi
  if [ "$created_restore" = true ]; then
    docker volume rm "$restore_volume" >/dev/null 2>&1 || true
    created_restore=false
  fi
}
report_and_cleanup() {
  status=$?
  cleanup_resources
  if ((status != 0)); then
    printf 'container_acceptance=failed stage=%s\n' "$stage" >&2
  fi
  return "$status"
}
trap report_and_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for resource in "$container" "$data_volume" "$backup_volume" "$restore_volume"; do
  if docker container inspect "$resource" >/dev/null 2>&1 || docker volume inspect "$resource" >/dev/null 2>&1; then
    echo "error: generated Docker resource name already exists" >&2
    exit 1
  fi
done

docker volume create "$data_volume" >/dev/null
created_data=true
docker volume create "$backup_volume" >/dev/null
created_backup=true
docker volume create "$restore_volume" >/dev/null
created_restore=true

for volume in "$data_volume" "$backup_volume" "$restore_volume"; do
  docker run --rm --user 0:0 --entrypoint chown -v "$volume:/mnt" "$image" 10001:10001 /mnt
done

stage=container-start
token=$(openssl rand -hex 32)
docker create \
  --name "$container" \
  --init \
  --user 10001:10001 \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m,mode=1777 \
  -e GAME_LIBRARY_TOKEN="$token" \
  -e TZ=UTC \
  -v "$data_volume:/data" \
  -v "$backup_volume:/backup" \
  -v "$backup_volume:/read-only-probe:ro" \
  -v "$restore_volume:/restore" \
  -v "$repository_root/testdata:/library:ro" \
  -p "127.0.0.1:$port:8080" \
  "$image" >/dev/null
created_container=true
docker start "$container" >/dev/null

stage=readiness
ready=false
attempt=0
while ((attempt < 30)); do
  if curl --fail --silent --show-error --max-time 2 "http://127.0.0.1:$port/api/v1/health/ready" >/dev/null 2>&1; then
    ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container" >&2 || true
  echo "error: container did not become ready" >&2
  exit 1
fi

docker_health=starting
attempt=0
while ((attempt < 45)); do
  docker_health=$(docker inspect "$container" --format '{{.State.Health.Status}}')
  [ "$docker_health" = healthy ] && break
  attempt=$((attempt + 1))
  sleep 1
done
test "$docker_health" = healthy

stage=health-contract
readiness=$(curl --fail --silent --show-error "http://127.0.0.1:$port/api/v1/health/ready")
READINESS_JSON="$readiness" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["READINESS_JSON"])
assert payload["status"] == "ready", payload
assert payload["schema_version"] == payload["supported_schema_version"], payload
PY

stage=http-auth-and-headers
test "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port/")" = 200
test "$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port/api/v1/games")" = 401
test "$(curl --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $token" "http://127.0.0.1:$port/api/v1/games")" = 200

headers=$(curl --fail --silent --show-error --dump-header - --output /dev/null "http://127.0.0.1:$port/api/v1/health")
printf '%s' "$headers" | grep -qi '^content-security-policy:'
printf '%s' "$headers" | grep -qi '^x-content-type-options: nosniff'
printf '%s' "$headers" | grep -qi '^referrer-policy:'

stage=container-isolation-user
test "$(docker inspect "$container" --format '{{.Config.User}}')" = "10001:10001"
stage=container-isolation-rootfs
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
stage=container-isolation-capabilities
case "$(docker inspect "$container" --format '{{json .HostConfig.CapDrop}}')" in
  *'"ALL"'*) ;;
  *) echo "error: container did not drop all capabilities" >&2; exit 1 ;;
esac
stage=container-isolation-privileges
case "$(docker inspect "$container" --format '{{json .HostConfig.SecurityOpt}}')" in
  *'no-new-privileges:true'*) ;;
  *) echo "error: no-new-privileges is not enabled" >&2; exit 1 ;;
esac
stage=container-isolation-library-mount
test "$(docker inspect "$container" --format '{{range .Mounts}}{{if eq .Destination "/library"}}{{.RW}}{{end}}{{end}}')" = false
test "$(docker inspect "$container" --format '{{range .Mounts}}{{if eq .Destination "/read-only-probe"}}{{.RW}}{{end}}{{end}}')" = false
stage=container-isolation-read-only-volume
if docker exec "$container" sh -c ': > /read-only-probe/varkiv-container-acceptance-write' >/dev/null 2>&1; then
  docker exec "$container" rm -f /backup/varkiv-container-acceptance-write >/dev/null 2>&1 || true
  echo "error: read-only volume accepted a write" >&2
  exit 1
fi

stage=container-isolation-root-write
if docker exec "$container" sh -c ': > /varkiv-container-acceptance-root-write' >/dev/null 2>&1; then
  docker exec "$container" rm -f /varkiv-container-acceptance-root-write >/dev/null 2>&1 || true
  echo "error: read-only container root accepted a write" >&2
  exit 1
fi
stage=container-isolation-tmpfs
docker exec "$container" sh -c 'probe=/tmp/varkiv-container-acceptance; umask 077; : > "$probe"; rm -f -- "$probe"'

stage=database-check
database_check=$(docker exec "$container" varkiv db-check --db /data/library.db)
DATABASE_CHECK="$database_check" python3 - <<'PY'
import os
import re

fields = dict(re.findall(r"([a-z_]+)=([^ ]+)", os.environ["DATABASE_CHECK"]))
assert fields["schema_version"] == fields["supported"], fields
assert fields["integrity"] == "ok", fields
assert fields["foreign_keys"] == "ok", fields
assert fields["mode"] == "read-only", fields
PY

stage=backup-and-restore
backup_result=$(docker exec "$container" varkiv backup-state --db /data/library.db --state /data --out /backup/fixture)
case "$backup_result" in *'state_backup_created=true'*) ;; *) echo "error: state backup was not created" >&2; exit 1 ;; esac
backup_check=$(docker exec "$container" varkiv check-state --from /backup/fixture)
case "$backup_check" in *'state_backup_valid=true'*) ;; *) echo "error: state backup did not validate" >&2; exit 1 ;; esac
restore_result=$(docker exec "$container" varkiv restore-state --from /backup/fixture --out /restore/recovered)
case "$restore_result" in *'state_restore_created=true'*) ;; *) echo "error: restored state was not created" >&2; exit 1 ;; esac
restored_check=$(docker exec "$container" varkiv db-check --db /restore/recovered/library.db)
case "$restored_check" in *'integrity=ok foreign_keys=ok mode=read-only'*) ;; *) echo "error: restored database did not validate" >&2; exit 1 ;; esac

stage=release-audit
release_audit=$(docker exec "$container" varkiv release-audit --db /data/library.db --json)
EXPECTED_VERSION="$version" RELEASE_AUDIT_JSON="$release_audit" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["RELEASE_AUDIT_JSON"])
external = {gate["id"]: gate["status"] for gate in payload["external_gates"]}
assert payload["format"] == "varkiv-release-audit-v3", payload
assert payload["application_version"] == os.environ["EXPECTED_VERSION"], payload
assert payload["software_ready"] is True, payload
assert payload["hardware_ready"] is False, payload
assert payload["hardware"]["ready"] is False, payload
assert payload["hardware_ready"] == payload["hardware"]["ready"], payload
assert payload["public_release_ready"] is False, payload
assert external["project-license"] == "ready", external
PY

stage=cleanup
cleanup_resources
trap - EXIT INT TERM

if docker container inspect "$container" >/dev/null 2>&1; then
  echo "error: acceptance container remained after cleanup" >&2
  exit 1
fi
for volume in "$data_volume" "$backup_volume" "$restore_volume"; do
  if docker volume inspect "$volume" >/dev/null 2>&1; then
    echo "error: acceptance volume remained after cleanup" >&2
    exit 1
  fi
done

printf 'container_acceptance=passed version=%s image=%s schema=%s docker_health=%s auth=401/200 backup_restore=passed cleanup=passed\n' \
  "$version" "$image" "$(printf '%s' "$readiness" | python3 -c 'import json,sys; print(json.load(sys.stdin)["schema_version"])')" "$docker_health"
