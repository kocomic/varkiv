#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-knulli-hooks.sh' '' \
    'Runs the isolated KNULLI service and game-stop hook acceptance.'
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
server_image="${VARKIV_KNULLI_SERVER_IMAGE:-varkiv:${version}}"
hook_image="${VARKIV_HANDHELD_HOOK_IMAGE:-varkiv/handheld-hook-runtime:bash5.3.15-alpine3.24}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_KNULLI_ACCEPTANCE_DIR:-}" ]]; then
  evidence_root="${VARKIV_KNULLI_ACCEPTANCE_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "KNULLI acceptance directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-knulli-hooks.XXXXXX")"
fi

server_container="varkiv-knulli-server-$$"
agent_container="varkiv-knulli-agent-$$"
target_container="varkiv-knulli-target-$$"
network_name="varkiv-knulli-$$"
server_root="${evidence_root}/server"
control_root="${evidence_root}/control-device"
pair_root="${evidence_root}/pair-userdata"
installed_root="${evidence_root}/installed-userdata"
package_root="${evidence_root}/target-package"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv"
host_binary="${binary_root}/varkiv-host"
control_config="${control_root}/agent.json"
pair_config="${pair_root}/system/varkiv/agent.json"
package_config="${package_root}/userdata/system/varkiv/agent.json"
installed_config="${installed_root}/system/varkiv/agent.json"
service_script="${installed_root}/system/services/varkiv"
hook_script="${installed_root}/system/scripts/varkiv-sync.sh"

cleanup() {
  local status=$?
  rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
    "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" \
    "${installed_root}/system/varkiv/agent.pid" "${installed_root}/system/varkiv/oneshot.pid"
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
docker build --quiet --file "${project_root}/Dockerfile.handheld-acceptance" --tag "${hook_image}" "${project_root}" >/dev/null
[[ "$(docker run --rm --entrypoint /usr/local/bin/bash "${hook_image}" -c 'printf %s "$BASH_VERSION"')" == 5.3.15* ]] || { echo "KNULLI Bash runtime identity drifted" >&2; exit 1; }

fixture_rom="${project_root}/testdata/pegasus/gba/Advance Wars (USA).gba"
expected_rom_sha="fc7c9a43789d27038753bdf114a59d39eb53aabe0a765b3512e6d584d17f9735"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "KNULLI fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "KNULLI fixture ROM identity drifted" >&2; exit 1; }

control_rom="control-private-name.gba"
target_rom="knulli-private-name.gba"
control_save="control-private-name.srm"
target_save="knulli-private-name.srm"
mkdir -m 700 -p "${server_root}/data" "${server_root}/state" "${server_root}/library/gba" \
  "${control_root}/roms/gba" "${control_root}/saves" \
  "${pair_root}/system/varkiv/saves" "${pair_root}/roms/gba" \
  "${installed_root}" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/gba/agent-knulli.gba"
cp "${fixture_rom}" "${control_root}/roms/gba/${control_rom}"
cp "${fixture_rom}" "${pair_root}/roms/gba/${target_rom}"
printf '%s' 'knulli-hook-save-v1' > "${control_root}/saves/${control_save}"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
  go build -trimpath -o "${host_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${control_root}" "${pair_root}" "${installed_root}"
chmod 0755 "${varkiv_binary}" "${host_binary}"
runtime_version="$(docker run --rm --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${hook_image}" version | awk 'NF >= 2 {print $2}')"
[[ "${runtime_version}" == "${version}" ]] || { echo "KNULLI Agent version identity drifted" >&2; exit 1; }

owner_token="knulli-hook-$(openssl rand -hex 24)"
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
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "KNULLI server was not published on loopback" >&2; exit 1; }
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
post_json games '{"id":"agent-knulli-game","default_title":"KNULLI Agent fixture","platform":"gba","titles":{}}' >/dev/null
post_json editions '{"id":"agent-knulli-edition","game_id":"agent-knulli-game","default_title":"KNULLI Agent fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"gba/agent-knulli.gba","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-knulli-stream","owner_type":"edition","owner_key":"agent-knulli-edition","driver_id":"builtin-driver-retroarch","portability":"core-dependent","edition_ids":["agent-knulli-edition"],"compatibility":"native"},"binding":{"id":"agent-knulli-binding","edition_id":"agent-knulli-edition","device_profile_id":"builtin-device-knulli","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"file","refresh":"process-exit"},"enabled":true}}' >/dev/null

run_control_agent() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${control_root},dst=/device" \
    --entrypoint /usr/local/bin/varkiv "${hook_image}" "$@"
}
run_target_pair() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${pair_root},dst=/userdata" \
    --entrypoint /usr/local/bin/varkiv "${hook_image}" "$@"
}

pairing_code_a="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-knulli"}}' | jq -er '.code')"
pair_output_a="$(run_control_agent agent pair --config /device/agent.json --server http://server:8080 --code "${pairing_code_a}" \
  --name 'KNULLI control Agent' --root /device --os linux --distribution knulli --arch arm64 --allow-http \
  --path save_dir=/device/saves --rom-root gba=/device/roms/gba)"
[[ "${pair_output_a}" == 'paired=true config_saved=true' ]] || { echo "KNULLI control pairing failed" >&2; exit 1; }

pairing_code_b="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-knulli"}}' | jq -er '.code')"
pair_output_b="$(run_target_pair agent pair --config /userdata/system/varkiv/agent.json --server http://server:8080 --code "${pairing_code_b}" \
  --name 'KNULLI packaged Agent' --root /userdata/system/varkiv --os linux --distribution knulli --arch arm64 --allow-http \
  --path save_dir=/userdata/system/varkiv/saves --rom-root gba=/userdata/roms/gba)"
[[ "${pair_output_b}" == 'paired=true config_saved=true' ]] || { echo "KNULLI target pairing failed" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${pair_config}" 2>/dev/null || stat -c '%a' "${pair_config}")" == 600 ]] || { echo "KNULLI target config permissions drifted" >&2; exit 1; }

"${host_binary}" agent target-package --kind knulli --binary "${varkiv_binary}" --config "${pair_config}" --out "${package_root}" >/dev/null
package_verification="$("${host_binary}" agent target-package verify --path "${package_root}" --json)"
jq -e '.verified == true and .kind == "knulli" and .files == 7 and .missing == 0 and .changed == 0 and .extra == 0' <<<"${package_verification}" >/dev/null
cp -R "${package_root}/userdata/." "${installed_root}/"
mkdir -m 700 -p "${installed_root}/roms/gba" "${installed_root}/system/varkiv/saves"
cp "${fixture_rom}" "${installed_root}/roms/gba/${target_rom}"
[[ -x "${service_script}" && -x "${hook_script}" ]] || { echo "KNULLI package scripts are not executable" >&2; exit 1; }
bash -n "${service_script}"
bash -n "${hook_script}"
grep -Fq '[ "${1:-}" = gameStop ] || exit 0' "${hook_script}" || { echo "KNULLI gameStop filter drifted" >&2; exit 1; }
grep -Fq 'if [ "$pid" -gt 1 ] && kill -0 "$pid"' "${hook_script}" || { echo "KNULLI concurrency guard drifted" >&2; exit 1; }

docker run --detach --rm --name "${target_container}" --network "${network_name}" \
  --user "${container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --mount "type=bind,src=${installed_root},dst=/userdata" \
  --entrypoint /usr/local/bin/bash "${hook_image}" -c 'while :; do sleep 3600; done' >/dev/null
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

set +e
initial_status="$(target_exec /userdata/system/services/varkiv status 2>&1)"
initial_status_code=$?
set -e
[[ "${initial_status_code}" != 0 && "${initial_status}" == stopped ]] || { echo "KNULLI service initial state drifted" >&2; exit 1; }

sync_a1="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a1}" == *'uploaded=1'* ]] || { echo "KNULLI control initial upload failed" >&2; exit 1; }
target_exec /userdata/system/scripts/varkiv-sync.sh gameStart '/userdata/roms/gba/private-ignored.gba' 'retroarch-private'
jq -e 'has("last_sync") | not' "${installed_config}" >/dev/null
target_exec /userdata/system/scripts/varkiv-sync.sh gameStop '/userdata/roms/gba/private-ignored.gba' 'retroarch-private'
wait_for_state complete || { echo "KNULLI gameStop download did not complete" >&2; exit 1; }
wait_for_file_value "${installed_root}/system/varkiv/saves/${target_save}" 'knulli-hook-save-v1' || { echo "KNULLI gameStop did not resolve the local ROM stem" >&2; exit 1; }
first_finished="$(jq -er '.last_sync.finished_at' "${installed_config}")"
for _ in $(seq 1 100); do [[ ! -e "${installed_root}/system/varkiv/oneshot.pid" ]] && break; sleep 0.1; done
[[ ! -e "${installed_root}/system/varkiv/oneshot.pid" ]] || { echo "KNULLI one-shot pid was not cleaned" >&2; exit 1; }

printf '%s' 'knulli-hook-save-v2' > "${control_root}/saves/${control_save}"
sync_a2="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a2}" == *'uploaded=1'* ]] || { echo "KNULLI control second upload failed" >&2; exit 1; }
target_exec /userdata/system/services/varkiv start
[[ "$(target_exec /userdata/system/services/varkiv status)" == running ]] || { echo "KNULLI service did not start" >&2; exit 1; }
wait_for_state complete "${first_finished}" || { echo "KNULLI service download did not complete" >&2; exit 1; }
wait_for_file_value "${installed_root}/system/varkiv/saves/${target_save}" 'knulli-hook-save-v2' || { echo "KNULLI service did not install the second revision" >&2; exit 1; }
target_exec /userdata/system/scripts/varkiv-sync.sh gameStop '/userdata/roms/gba/private-ignored.gba' 'retroarch-private'
[[ ! -e "${installed_root}/system/varkiv/oneshot.pid" ]] || { echo "KNULLI hook bypassed the running-service guard" >&2; exit 1; }
target_exec /userdata/system/services/varkiv stop
for _ in $(seq 1 100); do
  set +e
  stopped_output="$(target_exec /userdata/system/services/varkiv status 2>&1)"
  stopped_code=$?
  set -e
  [[ "${stopped_code}" != 0 && "${stopped_output}" == stopped ]] && break
  sleep 0.1
done
[[ "${stopped_code}" != 0 && "${stopped_output}" == stopped ]] || { echo "KNULLI service did not stop" >&2; exit 1; }

backup_root="${installed_root}/system/varkiv/.varkiv/backups/agent-knulli-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do backup_dir="${candidate}"; backup_count=$((backup_count + 1)); done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 && "$(<"${backup_dir}/primary.srm")" == 'knulli-hook-save-v1' ]] || { echo "KNULLI recoverable backup drifted" >&2; exit 1; }

printf '%s' 'knulli-hook-save-v3' > "${control_root}/saves/${control_save}"
sync_a3="$(run_control_agent agent sync --config /device/agent.json)"
[[ "${sync_a3}" == *'uploaded=1'* ]] || { echo "KNULLI control third upload failed" >&2; exit 1; }
printf '%s' 'knulli-hook-local-important' > "${installed_root}/system/varkiv/saves/${target_save}"
second_finished="$(jq -er '.last_sync.finished_at' "${installed_config}")"
target_exec /userdata/system/scripts/varkiv-sync.sh gameStop '/userdata/roms/gba/private-ignored.gba' 'retroarch-private'
wait_for_state conflict "${second_finished}" || { echo "KNULLI gameStop conflict did not complete" >&2; exit 1; }
[[ "$(<"${installed_root}/system/varkiv/saves/${target_save}")" == 'knulli-hook-local-important' ]] || { echo "KNULLI conflict overwrote local data" >&2; exit 1; }
for _ in $(seq 1 100); do [[ ! -e "${installed_root}/system/varkiv/oneshot.pid" ]] && break; sleep 0.1; done
[[ ! -e "${installed_root}/system/varkiv/oneshot.pid" ]] || { echo "KNULLI conflict one-shot pid was not cleaned" >&2; exit 1; }

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-knulli-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 1 and .files[0].logical_path == "primary.srm")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-knulli" and .capabilities.save_streams == true)' <<<"${devices_json}" >/dev/null
agent_log="$(<"${installed_root}/system/varkiv/agent.log")"
combined_output="${pair_output_a}${pair_output_b}${sync_a1}${sync_a2}${sync_a3}${package_verification}${revisions_json}${devices_json}${agent_log}"
for private_value in '/userdata/roms/' 'private-ignored.gba' 'retroarch-private' "${control_rom}" "${target_rom}" "${control_save}" "${target_save}" "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "KNULLI output disclosed private device data" >&2; exit 1; }
done

target_exec /usr/local/bin/bash -c 'if [ -f /userdata/system/varkiv/agent.pid ]; then pid=$(cat /userdata/system/varkiv/agent.pid); kill "$pid" 2>/dev/null || true; fi'
rm -f -- "${control_config}" "${pair_config}" "${package_config}" "${installed_config}" \
  "${control_config}.sync.lock" "${pair_config}.sync.lock" "${installed_config}.sync.lock" \
  "${installed_root}/system/varkiv/agent.pid" "${installed_root}/system/varkiv/oneshot.pid"
jq -n --arg version "${runtime_version}" --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-knulli-hook-acceptance-v1",version:$version,target:"knulli",architecture:"arm64",shell_runtime:{base:"bash:5.3.15-alpine3.24",digest:"sha256:a19c811ee9e97fa8a080001d82b8e0ded303f0795cffdb1cbd162731bc8ce208",bash:"5.3.15",acceptance_only:true},driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",rom_sha256:$rom_sha256,paired_devices:2,target_package:{generated:true,verified:true,file_count:7,installed_into_new_userdata:true},service:{start:true,status:true,stop:true,download:true},game_stop_hook:{event_filter:true,arguments_ignored:true,service_guard:true,oneshot_cleanup:true,download:true,conflict:true},uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,local_basenames_reported:false,frontend_arguments_reported:false,agent_configs_retained:false}}' > "${evidence_root}/report.json"

echo "knulli_hook_acceptance=passed version=${runtime_version} target=knulli package=verified service=start-status-stop hook=download-conflict-guard uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
