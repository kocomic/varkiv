#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-steamos-save.sh' '' \
    'Runs the isolated SteamOS/Bazzite package and save-sync acceptance.'
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
image="${VARKIV_STEAMOS_AGENT_IMAGE:-varkiv:${version}}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_STEAMOS_AGENT_DIR:-}" ]]; then
  evidence_root="${VARKIV_STEAMOS_AGENT_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "SteamOS Agent evidence directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-steamos-agent.XXXXXX")"
fi

server_container="varkiv-steamos-agent-server-$$"
agent_container="varkiv-steamos-agent-client-$$"
service_container="varkiv-steamos-agent-service-$$"
network_name="varkiv-steamos-agent-$$"
server_root="${evidence_root}/server"
device_a_root="${evidence_root}/device-a"
device_b_root="${evidence_root}/device-b"
device_b_pair_home="${device_b_root}/pair-home"
device_b_installed_home="${device_b_root}/installed-home"
package_root="${evidence_root}/target-package"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv"
host_binary="${binary_root}/varkiv-host"
agent_config_a="${device_a_root}/agent.json"
agent_config_b_pair="${device_b_pair_home}/.config/varkiv/agent.json"
agent_config_b_package="${package_root}/home/.config/varkiv/agent.json"
agent_config_b="${device_b_installed_home}/.config/varkiv/agent.json"
service_unit="${device_b_installed_home}/.config/systemd/user/varkiv-agent.service"

cleanup() {
  local status=$?
  rm -f -- "${agent_config_a}" "${agent_config_b_pair}" "${agent_config_b_package}" "${agent_config_b}" \
    "${agent_config_a}.sync.lock" "${agent_config_b_pair}.sync.lock" "${agent_config_b}.sync.lock"
  for container_name in "${service_container}" "${agent_container}" "${server_container}"; do
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

for command_name in docker go curl jq openssl shasum stat find cmp id; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
docker image inspect "${image}" >/dev/null 2>&1 || { echo "missing current Varkiv image: ${image}" >&2; exit 1; }

fixture_rom="${project_root}/testdata/pegasus/gba/Advance Wars (USA).gba"
expected_rom_sha="c4a4c5ba06e6c6f174b676e8b1ffd02333ce015d7e1ec8e18f8cca7961a5842a"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "SteamOS fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "SteamOS fixture ROM identity drifted" >&2; exit 1; }

rom_name_a="deck-private-name.gba"
rom_name_b="ally-private-name.gba"
save_name_a="deck-private-name.srm"
save_name_b="ally-private-name.srm"
mkdir -m 700 -p "${device_a_root}/roms/gba" "${device_a_root}/saves" \
  "${device_b_pair_home}/Games/gba" "${device_b_pair_home}/.local/share/varkiv/saves" \
  "${device_b_pair_home}/.config/varkiv" "${device_b_installed_home}" \
  "${server_root}/data" "${server_root}/state" "${server_root}/library/gba" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/gba/agent-retroarch.gba"
cp "${fixture_rom}" "${device_a_root}/roms/gba/${rom_name_a}"
cp "${fixture_rom}" "${device_b_pair_home}/Games/gba/${rom_name_b}"
printf '%s' 'steamos-agent-save-v1' > "${device_a_root}/saves/${save_name_a}"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
  go build -trimpath -o "${host_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${device_a_root}" "${device_b_root}"
chmod 0755 "${varkiv_binary}" "${host_binary}"
runtime_version="$(docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${image}" version | awk 'NF >= 2 {print $2}')"
[[ "${runtime_version}" == "${version}" ]] || { echo "SteamOS Agent version identity drifted" >&2; exit 1; }

owner_token="steamos-agent-$(openssl rand -hex 24)"
docker network create "${network_name}" >/dev/null
docker run --detach --rm --name "${server_container}" --network "${network_name}" --network-alias server \
  --publish 127.0.0.1::8080 --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --env "GAME_LIBRARY_TOKEN=${owner_token}" \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${server_root},dst=/server" \
  --entrypoint /usr/local/bin/varkiv \
  "${image}" serve --addr 0.0.0.0:8080 --db /server/data/library.db --state /server/state --library /server/library >/dev/null

mapped_address=""
for _ in $(seq 1 100); do
  mapped_address="$(docker port "${server_container}" 8080/tcp 2>/dev/null | tail -n 1)"
  [[ -n "${mapped_address}" ]] && break
  sleep 0.1
done
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "SteamOS Agent server was not published on loopback" >&2; exit 1; }
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
  local path=$1
  local body=$2
  owner_api --request POST --header 'Content-Type: application/json' --data-binary "${body}" "${server_origin}/api/v1/${path}"
}

post_json games '{"id":"agent-steamos-game","default_title":"SteamOS Agent fixture","platform":"gba","titles":{}}' >/dev/null
post_json editions '{"id":"agent-steamos-edition","game_id":"agent-steamos-game","default_title":"SteamOS Agent fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"gba/agent-retroarch.gba","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-steamos-stream","owner_type":"edition","owner_key":"agent-steamos-edition","driver_id":"builtin-driver-retroarch","portability":"core-dependent","edition_ids":["agent-steamos-edition"],"compatibility":"native"},"binding":{"id":"agent-steamos-binding","edition_id":"agent-steamos-edition","device_profile_id":"builtin-device-steamos-bazzite","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"file","refresh":"process-exit"},"enabled":true}}' >/dev/null
post_json launch-bindings '{"edition_id":"agent-steamos-edition","device_profile_id":"builtin-device-steamos-bazzite","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","arguments":["-L","{{core.library}}","{{rom.path}}"],"enabled":true}' >/dev/null

run_agent() {
  local root=$1
  shift
	docker run --rm --name "${agent_container}" --network "${network_name}" \
	  --user "${container_user}" \
	  --read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${root},dst=/device" \
    --entrypoint /usr/local/bin/varkiv "${image}" "$@"
}

run_target_pair() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" \
    --read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
    --mount "type=bind,src=${device_b_pair_home},dst=/home/deck" \
    --entrypoint /usr/local/bin/varkiv "${image}" "$@"
}

run_packaged_agent() {
  docker run --rm --name "${agent_container}" --network "${network_name}" \
    --user "${container_user}" \
    --read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${device_b_installed_home},dst=/home/deck" \
    --entrypoint /home/deck/.local/share/varkiv/varkiv "${image}" "$@"
}

start_packaged_service() {
  docker run --detach --rm --name "${service_container}" --network "${network_name}" \
    --user "${container_user}" \
    --read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
    --mount "type=bind,src=${device_b_installed_home},dst=/home/deck" \
    --entrypoint /home/deck/.local/share/varkiv/varkiv "${image}" \
    agent run --config /home/deck/.config/varkiv/agent.json --interval 60s >/dev/null
}

stop_packaged_service() {
  if docker container inspect "${service_container}" >/dev/null 2>&1; then
    docker rm --force "${service_container}" >/dev/null
  fi
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

wait_for_packaged_download() {
  local previous_finished=${1:-}
  for _ in $(seq 1 200); do
    if jq -e --arg previous "${previous_finished}" '.last_sync.state == "complete" and .last_sync.uploaded == 0 and .last_sync.downloaded == 1 and .last_sync.conflicts == 0 and .last_sync.finished_at != $previous' "${agent_config_b}" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  return 1
}

pairing_code_a="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-steamos-bazzite"}}' | jq -er '.code')"
pair_output_a="$(run_agent "${device_a_root}" agent pair --config /device/agent.json --server http://server:8080 --code "${pairing_code_a}" \
  --name 'SteamOS Agent A' --root /device --os linux --distribution steamos-bazzite --arch amd64 --allow-http \
  --path save_dir=/device/saves --rom-root gba=/device/roms/gba)"
[[ "${pair_output_a}" == "paired=true config_saved=true" ]] || { echo "SteamOS Agent A pairing did not complete" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${agent_config_a}" 2>/dev/null || stat -c '%a' "${agent_config_a}")" == "600" ]] || { echo "SteamOS Agent A config permissions are not private" >&2; exit 1; }

pairing_code_b="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-steamos-bazzite"}}' | jq -er '.code')"
pair_output_b="$(run_target_pair agent pair --config /home/deck/.config/varkiv/agent.json --server http://server:8080 --code "${pairing_code_b}" \
  --name 'SteamOS packaged Agent B' --root /home/deck/.local/share/varkiv \
  --os linux --distribution steamos-bazzite --arch amd64 --allow-http \
  --path save_dir=/home/deck/.local/share/varkiv/saves --rom-root gba=/home/deck/Games/gba)"
[[ "${pair_output_b}" == "paired=true config_saved=true" ]] || { echo "SteamOS packaged Agent pairing did not complete" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${agent_config_b_pair}" 2>/dev/null || stat -c '%a' "${agent_config_b_pair}")" == "600" ]] || { echo "SteamOS packaged Agent config permissions are not private" >&2; exit 1; }

"${host_binary}" agent target-package --kind steamos-bazzite --binary "${varkiv_binary}" \
  --config "${agent_config_b_pair}" --out "${package_root}" >/dev/null
package_verification="$("${host_binary}" agent target-package verify --path "${package_root}" --json)"
jq -e '.verified == true and .kind == "steamos-bazzite" and .files == 6 and .missing == 0 and .changed == 0 and .extra == 0' <<<"${package_verification}" >/dev/null
cp -R "${package_root}/home/." "${device_b_installed_home}/"
mkdir -m 700 -p "${device_b_installed_home}/Games/gba" "${device_b_installed_home}/.local/share/varkiv/saves"
cp "${fixture_rom}" "${device_b_installed_home}/Games/gba/${rom_name_b}"
[[ "$(<"${service_unit}")" == *'ExecStart=%h/.local/share/varkiv/varkiv agent run --config %h/.config/varkiv/agent.json --interval 60s'* ]] || { echo "SteamOS package service command drifted" >&2; exit 1; }
[[ "$(<"${service_unit}")" != *'/bin/sh'* && "$(<"${service_unit}")" != *'sudo'* ]] || { echo "SteamOS package service command is unsafe" >&2; exit 1; }

sync_a1="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a1}" == *"sync_status=complete"* && "${sync_a1}" == *"uploaded=1"* && "${sync_a1}" == *"downloaded=0"* ]] || { echo "SteamOS Agent A initial upload failed" >&2; exit 1; }
start_packaged_service
wait_for_file_value "${device_b_installed_home}/.local/share/varkiv/saves/${save_name_b}" 'steamos-agent-save-v1' || { echo "SteamOS packaged service initial download failed" >&2; exit 1; }
wait_for_packaged_download || { echo "SteamOS packaged service did not persist its initial result" >&2; exit 1; }
stop_packaged_service
sync_b1="$(run_packaged_agent agent status --config /home/deck/.config/varkiv/agent.json --json)"
jq -e '.last_sync.state == "complete" and .last_sync.uploaded == 0 and .last_sync.downloaded == 1 and .last_sync.conflicts == 0' <<<"${sync_b1}" >/dev/null
b1_finished_at="$(jq -er '.last_sync.finished_at' <<<"${sync_b1}")"

printf '%s' 'steamos-agent-save-v2' > "${device_a_root}/saves/${save_name_a}"
sync_a2="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a2}" == *"uploaded=1"* && "${sync_a2}" == *"conflicts=0"* ]] || { echo "SteamOS Agent A second upload failed" >&2; exit 1; }
start_packaged_service
wait_for_file_value "${device_b_installed_home}/.local/share/varkiv/saves/${save_name_b}" 'steamos-agent-save-v2' || { echo "SteamOS packaged service atomic update failed" >&2; exit 1; }
wait_for_packaged_download "${b1_finished_at}" || { echo "SteamOS packaged service did not persist its second result" >&2; exit 1; }
stop_packaged_service
sync_b2="$(run_packaged_agent agent status --config /home/deck/.config/varkiv/agent.json --json)"
jq -e '.last_sync.state == "complete" and .last_sync.uploaded == 0 and .last_sync.downloaded == 1 and .last_sync.conflicts == 0' <<<"${sync_b2}" >/dev/null

backup_root="${device_b_installed_home}/.local/share/varkiv/.varkiv/backups/agent-steamos-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do
  backup_dir="${candidate}"
  backup_count=$((backup_count + 1))
done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 ]] || { echo "SteamOS recoverable backup count drifted" >&2; exit 1; }
[[ "$(<"${backup_dir}/primary.srm")" == 'steamos-agent-save-v1' ]] || { echo "SteamOS recoverable backup content drifted" >&2; exit 1; }

printf '%s' 'steamos-agent-save-v3' > "${device_a_root}/saves/${save_name_a}"
sync_a3="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a3}" == *"uploaded=1"* ]] || { echo "SteamOS Agent A third upload failed" >&2; exit 1; }
printf '%s' 'steamos-agent-local-important' > "${device_b_installed_home}/.local/share/varkiv/saves/${save_name_b}"
set +e
conflict_output="$(run_packaged_agent agent sync --config /home/deck/.config/varkiv/agent.json 2>&1)"
conflict_status=$?
set -e
[[ "${conflict_status}" != 0 && "${conflict_output}" == *"conflicts=1"* ]] || { echo "SteamOS Agent conflict was not preserved" >&2; exit 1; }
[[ "$(<"${device_b_installed_home}/.local/share/varkiv/saves/${save_name_b}")" == 'steamos-agent-local-important' ]] || { echo "SteamOS Agent conflict overwrote local data" >&2; exit 1; }

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-steamos-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 1 and .files[0].logical_path == "primary.srm")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-steamos-bazzite" and .capabilities.save_streams == true)' <<<"${devices_json}" >/dev/null

combined_output="${sync_a1}${sync_b1}${sync_a2}${sync_b2}${sync_a3}${conflict_output}${revisions_json}${devices_json}${package_verification}"
for private_value in '/device/' "${rom_name_a}" "${rom_name_b}" "${save_name_a}" "${save_name_b}" "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "SteamOS Agent output disclosed private device data" >&2; exit 1; }
done

rm -f -- "${agent_config_a}" "${agent_config_b_pair}" "${agent_config_b_package}" "${agent_config_b}" \
  "${agent_config_a}.sync.lock" "${agent_config_b_pair}.sync.lock" "${agent_config_b}.sync.lock"
jq -n --arg version "${runtime_version}" --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-steamos-agent-acceptance-v2",version:$version,target:"steamos-bazzite",os_family:"linux",architecture:"amd64",driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",rom_sha256:$rom_sha256,paired_devices:2,distinct_local_rom_stems:true,logical_role:"primary.srm",target_package:{generated:true,verified:true,file_count:6,installed_into_new_home:true,systemd_execstart_ran:true,fixed_argv:true},uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,local_basenames_reported:false,agent_configs_retained:false}}' > "${evidence_root}/report.json"

echo "steamos_agent_acceptance=passed version=${runtime_version} target=steamos-bazzite paired=2 package=verified systemd_execstart=ran distinct_rom_stems=2 uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
