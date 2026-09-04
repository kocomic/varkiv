#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-onionos-apps.sh' '' \
    'Runs the isolated OnionOS application-entry and save-sync acceptance.'
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
server_image="${VARKIV_ONIONOS_SERVER_IMAGE:-varkiv:${version}}"
shell_image="${VARKIV_ONIONOS_SHELL_IMAGE:-varkiv/handheld-hook-runtime:bash5.3.15-alpine3.24-armv7}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_ONIONOS_ACCEPTANCE_DIR:-}" ]]; then
  evidence_root="${VARKIV_ONIONOS_ACCEPTANCE_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "OnionOS acceptance directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-onionos-apps.XXXXXX")"
fi

server_container="varkiv-onionos-server-$$"
agent_container="varkiv-onionos-agent-$$"
target_container="varkiv-onionos-target-$$"
network_name="varkiv-onionos-$$"
server_root="${evidence_root}/server"
control_root="${evidence_root}/control-device"
pair_sd="${evidence_root}/pair-sd"
installed_sd="${evidence_root}/installed-sd"
package_root="${evidence_root}/target-package"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv-linux-armv7"
host_binary="${binary_root}/varkiv-host"
control_config="${control_root}/agent.json"
pair_config="${pair_sd}/App/Varkiv/agent.json"
package_config="${package_root}/App/Varkiv/agent.json"
installed_config="${installed_sd}/App/Varkiv/agent.json"
sync_script="${installed_sd}/App/Varkiv/launch.sh"
start_script="${installed_sd}/App/Varkiv Start Automatic Sync/launch.sh"
stop_script="${installed_sd}/App/Varkiv Stop Automatic Sync/launch.sh"
pid_file="${installed_sd}/App/Varkiv/agent.pid"

cleanup() {
  local status=$?
  rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
    "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" \
    "${pid_file}" "${installed_sd}/App/Varkiv/.agent.pid."*
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
docker build --platform linux/arm/v7 --quiet --file "${project_root}/Dockerfile.handheld-acceptance" --tag "${shell_image}" "${project_root}" >/dev/null
shell_identity="$(docker run --rm --platform linux/arm/v7 --entrypoint /usr/local/bin/bash "${shell_image}" -c 'printf "%s|%s" "$(uname -m)" "$BASH_VERSION"')"
[[ "${shell_identity}" == armv7l\|5.3.15* ]] || { echo "OnionOS ARMv7 shell runtime identity drifted" >&2; exit 1; }

fixture_rom="${project_root}/testdata/pegasus/gba/Advance Wars (USA).gba"
expected_rom_sha="fc7c9a43789d27038753bdf114a59d39eb53aabe0a765b3512e6d584d17f9735"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "OnionOS fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "OnionOS fixture ROM identity drifted" >&2; exit 1; }

control_rom="control-private-name.gba"
target_rom="onion-private-name.gba"
control_save="control-private-name.srm"
target_save="onion-private-name.srm"
mkdir -m 700 -p "${server_root}/data" "${server_root}/state" "${server_root}/library/gba" \
  "${control_root}/roms/gba" "${control_root}/saves" \
  "${pair_sd}/App/Varkiv/saves" "${pair_sd}/Roms/GBA" \
  "${installed_sd}" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/gba/agent-onionos.gba"
cp "${fixture_rom}" "${control_root}/roms/gba/${control_rom}"
cp "${fixture_rom}" "${pair_sd}/Roms/GBA/${target_rom}"
printf '%s' 'onionos-app-save-v1' > "${control_root}/saves/${control_save}"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
  go build -trimpath -o "${host_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${control_root}" "${pair_sd}" "${installed_sd}"
chmod 0755 "${varkiv_binary}" "${host_binary}"
runtime_version="$(docker run --rm --platform linux/arm/v7 --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${shell_image}" version | awk 'NF >= 2 {print $2}')"
[[ "${runtime_version}" == "${version}" ]] || { echo "OnionOS Agent version identity drifted" >&2; exit 1; }

owner_token="onionos-app-$(openssl rand -hex 24)"
docker network create "${network_name}" >/dev/null
docker run --detach --rm --name "${server_container}" --network "${network_name}" --network-alias server \
  --publish 127.0.0.1::8080 --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --env "GAME_LIBRARY_TOKEN=${owner_token}" \
  --mount "type=bind,src=${server_root},dst=/server" \
  "${server_image}" serve --addr 0.0.0.0:8080 --db /server/data/library.db --state /server/state --library /server/library >/dev/null

mapped_address=""
for _ in $(seq 1 100); do
  mapped_address="$(docker port "${server_container}" 8080/tcp 2>/dev/null | tail -n 1)"
  [[ -n "${mapped_address}" ]] && break
  sleep 0.1
done
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "OnionOS server was not published on loopback" >&2; exit 1; }
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
post_json games '{"id":"agent-onionos-game","default_title":"OnionOS Agent fixture","platform":"gba","titles":{}}' >/dev/null
post_json editions '{"id":"agent-onionos-edition","game_id":"agent-onionos-game","default_title":"OnionOS Agent fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"gba/agent-onionos.gba","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-onionos-stream","owner_type":"edition","owner_key":"agent-onionos-edition","driver_id":"builtin-driver-retroarch","portability":"core-dependent","edition_ids":["agent-onionos-edition"],"compatibility":"native"},"binding":{"id":"agent-onionos-binding","edition_id":"agent-onionos-edition","device_profile_id":"builtin-device-onionos","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"file","refresh":"process-exit"},"enabled":true}}' >/dev/null

run_control_agent() {
  docker run --rm --platform linux/arm/v7 --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${control_root},dst=/device" \
    --entrypoint /usr/local/bin/varkiv "${shell_image}" "$@"
}
run_target_pair() {
  docker run --rm --platform linux/arm/v7 --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${pair_sd},dst=/mnt/SDCARD" \
    --entrypoint /usr/local/bin/varkiv "${shell_image}" "$@"
}

pairing_code_a="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-onionos"}}' | jq -er '.code')"
pair_output_a="$(run_control_agent agent pair --config /device/agent.json --server http://server:8080 --code "${pairing_code_a}" \
  --name 'OnionOS control Agent' --root /device --os linux --distribution onionos --arch armv7 --allow-http \
  --path save_dir=/device/saves --rom-root gba=/device/roms/gba)"
[[ "${pair_output_a}" == 'paired=true config_saved=true' ]] || { echo "OnionOS control pairing failed" >&2; exit 1; }

pairing_code_b="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-onionos"}}' | jq -er '.code')"
pair_output_b="$(run_target_pair agent pair --config /mnt/SDCARD/App/Varkiv/agent.json --server http://server:8080 --code "${pairing_code_b}" \
  --name 'OnionOS packaged Agent' --root /mnt/SDCARD/App/Varkiv --os linux --distribution onionos --arch armv7l --allow-http \
  --path save_dir=/mnt/SDCARD/App/Varkiv/saves --rom-root gba=/mnt/SDCARD/Roms/GBA)"
[[ "${pair_output_b}" == 'paired=true config_saved=true' ]] || { echo "OnionOS target pairing failed" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${pair_config}" 2>/dev/null || stat -c '%a' "${pair_config}")" == 600 ]] || { echo "OnionOS target config permissions drifted" >&2; exit 1; }

"${host_binary}" agent target-package --kind onionos --binary "${varkiv_binary}" --config "${pair_config}" --out "${package_root}" >/dev/null
package_verification="$("${host_binary}" agent target-package verify --path "${package_root}" --json)"
jq -e '.verified == true and .kind == "onionos" and .files == 11 and .missing == 0 and .changed == 0 and .extra == 0' <<<"${package_verification}" >/dev/null
mkdir -m 700 -p "${installed_sd}/App" "${installed_sd}/Roms/GBA"
cp -R "${package_root}/App/." "${installed_sd}/App/"
mkdir -m 700 -p "${installed_sd}/App/Varkiv/saves"
cp "${fixture_rom}" "${installed_sd}/Roms/GBA/${target_rom}"
for package_script in "${sync_script}" "${start_script}" "${stop_script}"; do
  [[ -x "${package_script}" ]] || { echo "OnionOS package script is not executable" >&2; exit 1; }
  sh -n "${package_script}"
  ! grep -Fq 'eval' "${package_script}" || { echo "OnionOS package script contains eval" >&2; exit 1; }
done
grep -Fq 'exec /mnt/SDCARD/App/Varkiv/varkiv agent sync --config /mnt/SDCARD/App/Varkiv/agent.json' "${sync_script}" || { echo "OnionOS sync argv drifted" >&2; exit 1; }
grep -Fq 'agent run --config "$base/agent.json" --interval 60s' "${start_script}" || { echo "OnionOS polling argv drifted" >&2; exit 1; }
jq -e '.label == "Varkiv Sync Now" and .launch == "launch.sh"' "${installed_sd}/App/Varkiv/config.json" >/dev/null
jq -e '.label == "Varkiv Start Automatic Sync" and .launch == "launch.sh"' "${installed_sd}/App/Varkiv Start Automatic Sync/config.json" >/dev/null
jq -e '.label == "Varkiv Stop Automatic Sync" and .launch == "launch.sh"' "${installed_sd}/App/Varkiv Stop Automatic Sync/config.json" >/dev/null

docker run --detach --rm --platform linux/arm/v7 --name "${target_container}" --network "${network_name}" \
  --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --mount "type=bind,src=${installed_sd},dst=/mnt/SDCARD" \
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

[[ ! -e "${pid_file}" ]] || { echo "OnionOS polling process was unexpectedly active" >&2; exit 1; }
sync_a1="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a1}" == *'uploaded=1'* ]] || { echo "OnionOS control initial upload failed" >&2; exit 1; }
sync_b1="$(target_exec /mnt/SDCARD/App/Varkiv/launch.sh)"
[[ "${sync_b1}" == *'downloaded=1'* ]] || { echo "OnionOS sync-now entry did not download" >&2; exit 1; }
wait_for_file_value "${installed_sd}/App/Varkiv/saves/${target_save}" 'onionos-app-save-v1' || { echo "OnionOS sync-now did not resolve local ROM stem" >&2; exit 1; }
first_finished="$(jq -er '.last_sync.finished_at' "${installed_config}")"

printf '%s' 'onionos-app-save-v2' > "${control_root}/saves/${control_save}"
sync_a2="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a2}" == *'uploaded=1'* ]] || { echo "OnionOS control second upload failed" >&2; exit 1; }
target_exec '/mnt/SDCARD/App/Varkiv Start Automatic Sync/launch.sh'
[[ -f "${pid_file}" ]] || { echo "OnionOS start entry did not publish a pid" >&2; exit 1; }
polling_pid="$(<"${pid_file}")"
[[ "${polling_pid}" =~ ^[0-9]+$ && "${polling_pid}" -gt 1 ]] || { echo "OnionOS start entry published an invalid pid" >&2; exit 1; }
target_exec /bin/sh -c 'pid=$(cat "/mnt/SDCARD/App/Varkiv/agent.pid"); kill -0 "$pid"'
target_exec '/mnt/SDCARD/App/Varkiv Start Automatic Sync/launch.sh'
[[ "$(<"${pid_file}")" == "${polling_pid}" ]] || { echo "OnionOS start entry was not idempotent" >&2; exit 1; }
wait_for_state complete "${first_finished}" || { echo "OnionOS polling entry did not complete the second download" >&2; exit 1; }
wait_for_file_value "${installed_sd}/App/Varkiv/saves/${target_save}" 'onionos-app-save-v2' || { echo "OnionOS polling entry did not install the second revision" >&2; exit 1; }
target_exec '/mnt/SDCARD/App/Varkiv Stop Automatic Sync/launch.sh'
target_exec '/mnt/SDCARD/App/Varkiv Stop Automatic Sync/launch.sh'
[[ ! -e "${pid_file}" ]] || { echo "OnionOS stop entry did not remove the pid" >&2; exit 1; }
for _ in $(seq 1 100); do
  if ! target_exec /bin/sh -c 'kill -0 "$1" 2>/dev/null' _ "${polling_pid}"; then break; fi
  sleep 0.1
done
! target_exec /bin/sh -c 'kill -0 "$1" 2>/dev/null' _ "${polling_pid}" || { echo "OnionOS stop entry left polling alive" >&2; exit 1; }

backup_root="${installed_sd}/App/Varkiv/.varkiv/backups/agent-onionos-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do backup_dir="${candidate}"; backup_count=$((backup_count + 1)); done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 && "$(<"${backup_dir}/primary.srm")" == 'onionos-app-save-v1' ]] || { echo "OnionOS recoverable backup drifted" >&2; exit 1; }

printf '%s' 'onionos-app-save-v3' > "${control_root}/saves/${control_save}"
sync_a3="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a3}" == *'uploaded=1'* ]] || { echo "OnionOS control third upload failed" >&2; exit 1; }
printf '%s' 'onionos-app-local-important' > "${installed_sd}/App/Varkiv/saves/${target_save}"
set +e
sync_b3="$(target_exec /mnt/SDCARD/App/Varkiv/launch.sh 2>&1)"
sync_b3_code=$?
set -e
[[ "${sync_b3_code}" != 0 && "${sync_b3}" == *'error: sync completed with save conflicts; no conflicting local data was overwritten'* ]] || { echo "OnionOS sync-now conflict semantics drifted" >&2; exit 1; }
jq -e '.last_sync.state == "conflict" and .last_sync.conflicts == 1' "${installed_config}" >/dev/null
[[ "$(<"${installed_sd}/App/Varkiv/saves/${target_save}")" == 'onionos-app-local-important' ]] || { echo "OnionOS conflict overwrote local data" >&2; exit 1; }

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-onionos-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 1 and .files[0].logical_path == "primary.srm")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-onionos" and .capabilities.save_streams == true) and ([.data[].architecture] | sort) == ["armv7","armv7l"]' <<<"${devices_json}" >/dev/null
agent_log="$(<"${installed_sd}/App/Varkiv/agent.log")"
combined_output="${pair_output_a}${pair_output_b}${sync_a1}${sync_a2}${sync_a3}${sync_b1}${sync_b3}${package_verification}${revisions_json}${devices_json}${agent_log}"
for private_value in '/mnt/SDCARD/Roms/' "${control_rom}" "${target_rom}" "${control_save}" "${target_save}" "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "OnionOS output disclosed private device data" >&2; exit 1; }
done

rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
  "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" "${pid_file}"
jq -n --arg version "${runtime_version}" --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-onionos-app-acceptance-v1",version:$version,target:"onionos",architecture:"armv7",emulated_machine:"armv7l",shell_runtime:{base:"bash:5.3.15-alpine3.24",digest:"sha256:a19c811ee9e97fa8a080001d82b8e0ded303f0795cffdb1cbd162731bc8ce208",bash:"5.3.15",acceptance_only:true},driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",rom_sha256:$rom_sha256,paired_devices:2,target_package:{generated:true,verified:true,file_count:11,installed_into_new_sd_root:true},application_entries:{sync_now:true,start_polling:true,start_idempotent:true,stop_polling:true,stop_idempotent:true},uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,local_basenames_reported:false,agent_configs_retained:false}}' > "${evidence_root}/report.json"

echo "onionos_app_acceptance=passed version=${runtime_version} target=onionos arch=armv7 package=verified entries=sync-start-stop uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
