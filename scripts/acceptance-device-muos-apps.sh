#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-muos-apps.sh' '' \
    'Runs the isolated muOS application-entry and save-sync acceptance.'
}
if (($# == 1)) && [[ "$1" == "--help" || "$1" == "-h" ]]; then
  usage
  exit 0
elif (($# > 0)); then
  echo "error: unexpected arguments" >&2
  usage >&2
  exit 2
fi

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' < "${project_root}/internal/buildinfo/VERSION")"
server_image="${VARKIV_MUOS_SERVER_IMAGE:-varkiv:${version}}"
shell_image="${VARKIV_HANDHELD_HOOK_IMAGE:-varkiv/handheld-hook-runtime:bash5.3.15-alpine3.24}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_MUOS_ACCEPTANCE_DIR:-}" ]]; then
  evidence_root="${VARKIV_MUOS_ACCEPTANCE_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "muOS acceptance directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-muos-apps.XXXXXX")"
fi

server_container="varkiv-muos-server-$$"
agent_container="varkiv-muos-agent-$$"
target_container="varkiv-muos-target-$$"
network_name="varkiv-muos-$$"
server_root="${evidence_root}/server"
control_root="${evidence_root}/control-device"
pair_storage="${evidence_root}/pair-storage"
installed_storage="${evidence_root}/installed-storage"
package_root="${evidence_root}/target-package"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv"
host_binary="${binary_root}/varkiv-host"
control_config="${control_root}/agent.json"
pair_config="${pair_storage}/application/Varkiv/agent.json"
package_config="${package_root}/application/Varkiv/agent.json"
installed_config="${installed_storage}/application/Varkiv/agent.json"
sync_script="${installed_storage}/application/Varkiv/mux_launch.sh"
start_script="${installed_storage}/application/Varkiv Start Automatic Sync/mux_launch.sh"
stop_script="${installed_storage}/application/Varkiv Stop Automatic Sync/mux_launch.sh"
pid_file="${installed_storage}/application/Varkiv/agent.pid"

cleanup() {
  local status=$?
  rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
    "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" \
    "${pid_file}" "${installed_storage}/application/Varkiv/.agent.pid."*
  for container_name in "${target_container}" "${agent_container}" "${server_container}"; do
    if docker container inspect "${container_name}" >/dev/null 2>&1; then
      docker rm --force "${container_name}" >/dev/null 2>&1 || true
    fi
  done
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null 2>&1 || true
  fi
  return "${status}"
}
trap cleanup EXIT INT TERM

for command_name in docker go curl jq openssl shasum stat find id grep; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
docker image inspect "${server_image}" >/dev/null 2>&1 || { echo "missing current Varkiv image: ${server_image}" >&2; exit 1; }
docker build --quiet --file "${project_root}/Dockerfile.handheld-acceptance" --tag "${shell_image}" "${project_root}" >/dev/null
[[ "$(docker run --rm --entrypoint /usr/local/bin/bash "${shell_image}" -c 'printf %s "$BASH_VERSION"')" == 5.3.15* ]] || { echo "muOS shell runtime identity drifted" >&2; exit 1; }

fixture_rom="${project_root}/testdata/pegasus/gba/Advance Wars (USA).gba"
expected_rom_sha="fc7c9a43789d27038753bdf114a59d39eb53aabe0a765b3512e6d584d17f9735"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "muOS fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "muOS fixture ROM identity drifted" >&2; exit 1; }

control_rom="control-private-name.gba"
target_rom="muos-private-name.gba"
control_save="control-private-name.srm"
target_save="muos-private-name.srm"
mkdir -m 700 -p "${server_root}/data" "${server_root}/state" "${server_root}/library/gba" \
  "${control_root}/roms/gba" "${control_root}/saves" \
  "${pair_storage}/application/Varkiv/saves" "${pair_storage}/roms/gba" \
  "${installed_storage}" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/gba/agent-muos.gba"
cp "${fixture_rom}" "${control_root}/roms/gba/${control_rom}"
cp "${fixture_rom}" "${pair_storage}/roms/gba/${target_rom}"
printf '%s' 'muos-app-save-v1' > "${control_root}/saves/${control_save}"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
  go build -trimpath -o "${host_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${control_root}" "${pair_storage}" "${installed_storage}"
chmod 0755 "${varkiv_binary}" "${host_binary}"
runtime_version="$(docker run --rm --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${shell_image}" version | awk 'NF >= 2 {print $2}')"
[[ "${runtime_version}" == "${version}" ]] || { echo "muOS Agent version identity drifted" >&2; exit 1; }

owner_token="muos-app-$(openssl rand -hex 24)"
docker network create "${network_name}" >/dev/null
docker run --detach --rm --name "${server_container}" --network "${network_name}" --network-alias server \
  --publish 127.0.0.1::8080 --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --env "GAME_LIBRARY_TOKEN=${owner_token}" \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${server_root},dst=/server" --entrypoint /usr/local/bin/varkiv \
  "${server_image}" serve --addr 0.0.0.0:8080 --db /server/data/library.db --state /server/state --library /server/library >/dev/null

mapped_address=""
for _ in $(seq 1 100); do
  mapped_address="$(docker port "${server_container}" 8080/tcp 2>/dev/null | tail -n 1)"
  [[ -n "${mapped_address}" ]] && break
  sleep 0.1
done
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "muOS server was not published on loopback" >&2; exit 1; }
server_origin="http://${mapped_address}"
for _ in $(seq 1 200); do
  if curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" | jq -e '.status == "ready" and (.schema_version | type) == "number" and .schema_version == .supported_schema_version' >/dev/null

owner_api() {
  curl --silent --show-error --fail --header "Authorization: Bearer ${owner_token}" "$@"
}
post_json() {
  local api_path=$1
  local body=$2
  owner_api --request POST --header 'Content-Type: application/json' --data-binary "${body}" "${server_origin}/api/v1/${api_path}"
}
post_json games '{"id":"agent-muos-game","default_title":"muOS Agent fixture","platform":"gba","titles":{}}' >/dev/null
post_json editions '{"id":"agent-muos-edition","game_id":"agent-muos-game","default_title":"muOS Agent fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"gba/agent-muos.gba","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-muos-stream","owner_type":"edition","owner_key":"agent-muos-edition","driver_id":"builtin-driver-retroarch","portability":"core-dependent","edition_ids":["agent-muos-edition"],"compatibility":"native"},"binding":{"id":"agent-muos-binding","edition_id":"agent-muos-edition","device_profile_id":"builtin-device-muos","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"file","refresh":"process-exit"},"enabled":true}}' >/dev/null

run_control_agent() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${control_root},dst=/device" \
    --entrypoint /usr/local/bin/varkiv "${shell_image}" "$@"
}
run_target_pair() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${pair_storage},dst=/run/muos/storage" \
    --entrypoint /usr/local/bin/varkiv "${shell_image}" "$@"
}

pairing_code_a="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-muos"}}' | jq -er '.code')"
pair_output_a="$(run_control_agent agent pair --config /device/agent.json --server http://server:8080 --code "${pairing_code_a}" \
  --name 'muOS control Agent' --root /device --os linux --distribution muos --arch arm64 --allow-http \
  --path save_dir=/device/saves --rom-root gba=/device/roms/gba)"
[[ "${pair_output_a}" == 'paired=true config_saved=true' ]] || { echo "muOS control pairing failed" >&2; exit 1; }

pairing_code_b="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-muos"}}' | jq -er '.code')"
pair_output_b="$(run_target_pair agent pair --config /run/muos/storage/application/Varkiv/agent.json --server http://server:8080 --code "${pairing_code_b}" \
  --name 'muOS packaged Agent' --root /run/muos/storage/application/Varkiv --os linux --distribution muos --arch arm64 --allow-http \
  --path save_dir=/run/muos/storage/application/Varkiv/saves --rom-root gba=/run/muos/storage/roms/gba)"
[[ "${pair_output_b}" == 'paired=true config_saved=true' ]] || { echo "muOS target pairing failed" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${pair_config}" 2>/dev/null || stat -c '%a' "${pair_config}")" == 600 ]] || { echo "muOS target config permissions drifted" >&2; exit 1; }

"${host_binary}" agent target-package --kind muos --binary "${varkiv_binary}" --config "${pair_config}" --out "${package_root}" >/dev/null
package_verification="$("${host_binary}" agent target-package verify --path "${package_root}" --json)"
jq -e '.verified == true and .kind == "muos" and .files == 8 and .missing == 0 and .changed == 0 and .extra == 0' <<<"${package_verification}" >/dev/null
mkdir -m 700 -p "${installed_storage}/application" "${installed_storage}/roms/gba"
cp -R "${package_root}/application/." "${installed_storage}/application/"
mkdir -m 700 -p "${installed_storage}/application/Varkiv/saves"
cp "${fixture_rom}" "${installed_storage}/roms/gba/${target_rom}"
for package_script in "${sync_script}" "${start_script}" "${stop_script}"; do
  [[ -x "${package_script}" ]] || { echo "muOS package script is not executable" >&2; exit 1; }
  bash -n "${package_script}"
  ! grep -Fq 'eval' "${package_script}" || { echo "muOS package script contains eval" >&2; exit 1; }
done
grep -Fq 'exec /run/muos/storage/application/Varkiv/varkiv agent sync --config /run/muos/storage/application/Varkiv/agent.json' "${sync_script}" || { echo "muOS sync argv drifted" >&2; exit 1; }
grep -Fq 'agent run --config "$base/agent.json" --interval 60s' "${start_script}" || { echo "muOS polling argv drifted" >&2; exit 1; }

docker run --detach --rm --name "${target_container}" --network "${network_name}" \
  --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --mount "type=bind,src=${installed_storage},dst=/run/muos/storage" \
  --entrypoint /usr/local/bin/bash "${shell_image}" -c 'while :; do sleep 3600; done' >/dev/null
target_exec() {
  docker exec --user "${container_user}" "${target_container}" "$@"
}
wait_for_state() {
  local state=$1
  local previous=${2:-}
  for _ in $(seq 1 200); do
    if jq -e --arg state "${state}" --arg previous "${previous}" '.last_sync.state == $state and .last_sync.finished_at != $previous' "${installed_config}" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  return 1
}
wait_for_file_value() {
  local file_name=$1
  local expected=$2
  for _ in $(seq 1 200); do
    if [[ -f "${file_name}" && "$(<"${file_name}")" == "${expected}" ]]; then return 0; fi
    sleep 0.1
  done
  return 1
}

[[ ! -e "${pid_file}" ]] || { echo "muOS polling process was unexpectedly active" >&2; exit 1; }
sync_a1="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a1}" == *'uploaded=1'* ]] || { echo "muOS control initial upload failed" >&2; exit 1; }
sync_b1="$(target_exec /run/muos/storage/application/Varkiv/mux_launch.sh)"
[[ "${sync_b1}" == *'downloaded=1'* ]] || { echo "muOS sync-now entry did not download" >&2; exit 1; }
wait_for_file_value "${installed_storage}/application/Varkiv/saves/${target_save}" 'muos-app-save-v1' || { echo "muOS sync-now did not resolve local ROM stem" >&2; exit 1; }
first_finished="$(jq -er '.last_sync.finished_at' "${installed_config}")"

printf '%s' 'muos-app-save-v2' > "${control_root}/saves/${control_save}"
sync_a2="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a2}" == *'uploaded=1'* ]] || { echo "muOS control second upload failed" >&2; exit 1; }
target_exec '/run/muos/storage/application/Varkiv Start Automatic Sync/mux_launch.sh'
[[ -f "${pid_file}" ]] || { echo "muOS start entry did not publish a pid" >&2; exit 1; }
polling_pid="$(<"${pid_file}")"
[[ "${polling_pid}" =~ ^[0-9]+$ && "${polling_pid}" -gt 1 ]] || { echo "muOS start entry published an invalid pid" >&2; exit 1; }
target_exec /bin/sh -c 'pid=$(cat "/run/muos/storage/application/Varkiv/agent.pid"); kill -0 "$pid"'
target_exec '/run/muos/storage/application/Varkiv Start Automatic Sync/mux_launch.sh'
[[ "$(<"${pid_file}")" == "${polling_pid}" ]] || { echo "muOS start entry was not idempotent" >&2; exit 1; }
wait_for_state complete "${first_finished}" || { echo "muOS polling entry did not complete the second download" >&2; exit 1; }
wait_for_file_value "${installed_storage}/application/Varkiv/saves/${target_save}" 'muos-app-save-v2' || { echo "muOS polling entry did not install the second revision" >&2; exit 1; }
target_exec '/run/muos/storage/application/Varkiv Stop Automatic Sync/mux_launch.sh'
target_exec '/run/muos/storage/application/Varkiv Stop Automatic Sync/mux_launch.sh'
[[ ! -e "${pid_file}" ]] || { echo "muOS stop entry did not remove the pid" >&2; exit 1; }
for _ in $(seq 1 100); do
  if ! target_exec /bin/sh -c 'kill -0 "$1" 2>/dev/null' _ "${polling_pid}"; then break; fi
  sleep 0.1
done
! target_exec /bin/sh -c 'kill -0 "$1" 2>/dev/null' _ "${polling_pid}" || { echo "muOS stop entry left polling alive" >&2; exit 1; }

backup_root="${installed_storage}/application/Varkiv/.varkiv/backups/agent-muos-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do backup_dir="${candidate}"; backup_count=$((backup_count + 1)); done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 && "$(<"${backup_dir}/primary.srm")" == 'muos-app-save-v1' ]] || { echo "muOS recoverable backup drifted" >&2; exit 1; }

printf '%s' 'muos-app-save-v3' > "${control_root}/saves/${control_save}"
sync_a3="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a3}" == *'uploaded=1'* ]] || { echo "muOS control third upload failed" >&2; exit 1; }
printf '%s' 'muos-app-local-important' > "${installed_storage}/application/Varkiv/saves/${target_save}"
set +e
sync_b3="$(target_exec /run/muos/storage/application/Varkiv/mux_launch.sh 2>&1)"
sync_b3_code=$?
set -e
[[ "${sync_b3_code}" != 0 && "${sync_b3}" == *'error: sync completed with save conflicts; no conflicting local data was overwritten'* ]] || { echo "muOS sync-now conflict semantics drifted" >&2; exit 1; }
jq -e '.last_sync.state == "conflict" and .last_sync.conflicts == 1' "${installed_config}" >/dev/null
[[ "$(<"${installed_storage}/application/Varkiv/saves/${target_save}")" == 'muos-app-local-important' ]] || { echo "muOS conflict overwrote local data" >&2; exit 1; }

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-muos-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 1 and .files[0].logical_path == "primary.srm")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-muos" and .capabilities.save_streams == true)' <<<"${devices_json}" >/dev/null
agent_log="$(<"${installed_storage}/application/Varkiv/agent.log")"
combined_output="${pair_output_a}${pair_output_b}${sync_a1}${sync_a2}${sync_a3}${sync_b1}${sync_b3}${package_verification}${revisions_json}${devices_json}${agent_log}"
for private_value in '/run/muos/storage/roms/' "${control_rom}" "${target_rom}" "${control_save}" "${target_save}" "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "muOS output disclosed private device data" >&2; exit 1; }
done

rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
  "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" "${pid_file}"
jq -n --arg version "${runtime_version}" --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-muos-app-acceptance-v1",version:$version,target:"muos",architecture:"arm64",shell_runtime:{base:"bash:5.3.15-alpine3.24",digest:"sha256:a19c811ee9e97fa8a080001d82b8e0ded303f0795cffdb1cbd162731bc8ce208",bash:"5.3.15",acceptance_only:true},driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",rom_sha256:$rom_sha256,paired_devices:2,target_package:{generated:true,verified:true,file_count:8,installed_into_new_storage:true},application_entries:{sync_now:true,start_polling:true,start_idempotent:true,stop_polling:true,stop_idempotent:true},uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,local_basenames_reported:false,agent_configs_retained:false}}' > "${evidence_root}/report.json"

echo "muos_app_acceptance=passed version=${runtime_version} target=muos package=verified entries=sync-start-stop uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
