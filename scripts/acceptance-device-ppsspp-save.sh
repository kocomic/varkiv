#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-ppsspp-save.sh' '' \
    'Runs the isolated ROCKNIX PPSSPP multi-file save-sync acceptance.'
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
image="${VARKIV_PPSSPP_AGENT_IMAGE:-varkiv:${version}}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_PPSSPP_AGENT_DIR:-}" ]]; then
  evidence_root="${VARKIV_PPSSPP_AGENT_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "PPSSPP Agent evidence directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-ppsspp-agent.XXXXXX")"
fi

server_container="varkiv-ppsspp-agent-server-$$"
agent_container="varkiv-ppsspp-agent-client-$$"
network_name="varkiv-ppsspp-agent-$$"
server_root="${evidence_root}/server"
device_a_root="${evidence_root}/device-a"
device_b_root="${evidence_root}/device-b"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv"
agent_config_a="${device_a_root}/agent.json"
agent_config_b="${device_b_root}/agent.json"

cleanup() {
  local status=$?
  rm -f -- "${agent_config_a}" "${agent_config_b}" "${agent_config_a}.sync.lock" "${agent_config_b}.sync.lock"
  for container_name in "${agent_container}" "${server_container}"; do
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

for command_name in docker go curl jq openssl shasum stat find wc cmp id; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
docker image inspect "${image}" >/dev/null 2>&1 || { echo "missing current Varkiv image: ${image}" >&2; exit 1; }

fixture_rom="${project_root}/testdata/portable-standalone-v2/psp/standalone-v2.iso"
expected_rom_sha="73ff0956416b04b11e8f24390c7ee4dfea4822a2849119532f2be2655502911a"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "PPSSPP fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | cut -d ' ' -f 1)" == "${expected_rom_sha}" ]] || { echo "PPSSPP fixture ROM identity drifted" >&2; exit 1; }

save_relative="PSP/SAVEDATA/ULUS-00000"
for root in "${device_a_root}" "${device_b_root}"; do
  mkdir -m 700 -p "${root}/roms/psp" "${root}/PPSSPP-user" "${root}/saves"
  cp "${fixture_rom}" "${root}/roms/psp/private-device-game.iso"
done
mkdir -m 700 -p "${server_root}/data" "${server_root}/state" "${server_root}/library/psp" \
  "${device_a_root}/PPSSPP-user/${save_relative}" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/psp/agent-ppsspp.iso"
printf '%s' 'ppsspp-agent-param-v1' > "${device_a_root}/PPSSPP-user/${save_relative}/PARAM.SFO"
printf '%s' 'ppsspp-agent-data-v1' > "${device_a_root}/PPSSPP-user/${save_relative}/DATA.BIN"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${device_a_root}" "${device_b_root}"
chmod 0755 "${varkiv_binary}"
runtime_version="$(docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${image}" version | awk 'NF >= 2 {print $2}')"
[[ "${runtime_version}" == "${version}" ]] || { echo "Linux Agent version identity drifted" >&2; exit 1; }

owner_token="ppsspp-agent-$(openssl rand -hex 24)"
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
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "PPSSPP Agent server was not published on loopback" >&2; exit 1; }
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

post_json games '{"id":"agent-ppsspp-game","default_title":"Agent PPSSPP fixture","platform":"psp","titles":{}}' >/dev/null
post_json editions '{"id":"agent-ppsspp-edition","game_id":"agent-ppsspp-game","default_title":"Agent PPSSPP fixture","edition_type":"original","languages":["en"],"product_code":"ULUS-00000","titles":{},"artifact_path":"psp/agent-ppsspp.iso","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-ppsspp-stream","owner_type":"edition","owner_key":"agent-ppsspp-edition","driver_id":"builtin-driver-ppsspp","portability":"driver-dependent","edition_ids":["agent-ppsspp-edition"],"compatibility":"native"},"binding":{"id":"agent-ppsspp-rocknix-binding","edition_id":"agent-ppsspp-edition","device_profile_id":"builtin-device-rocknix","driver_id":"builtin-driver-ppsspp","local_paths":["{{driver.user_dir}}/PSP/SAVEDATA/{{edition.product_code}}"],"discovery":{"mode":"directory","refresh":"process-exit"},"enabled":true}}' >/dev/null
post_json launch-bindings '{"edition_id":"agent-ppsspp-edition","device_profile_id":"builtin-device-rocknix","driver_id":"builtin-driver-ppsspp","arguments":["{{rom.path}}"],"enabled":true}' >/dev/null

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

pair_agent() {
  local root=$1
  local config=$2
  local name=$3
  local code
  code="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-rocknix"}}' | jq -er '.code')"
  local output
  output="$(run_agent "${root}" agent pair --config /device/agent.json --server http://server:8080 --code "${code}" \
    --name "${name}" --root /device --os linux --distribution rocknix --arch arm64 --allow-http \
    --path save_dir=/device/saves --driver-root builtin-driver-ppsspp=/device/PPSSPP-user \
    --rom-root psp=/device/roms/psp)"
  [[ "${output}" == "paired=true config_saved=true" ]] || { echo "PPSSPP Agent pairing did not complete" >&2; exit 1; }
  [[ "$(stat -f '%Lp' "${config}" 2>/dev/null || stat -c '%a' "${config}")" == "600" ]] || { echo "PPSSPP Agent config permissions are not private" >&2; exit 1; }
}

pair_agent "${device_a_root}" "${agent_config_a}" 'PPSSPP Agent A'
pair_agent "${device_b_root}" "${agent_config_b}" 'PPSSPP Agent B'

sync_a1="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a1}" == *"sync_status=complete"* && "${sync_a1}" == *"uploaded=1"* && "${sync_a1}" == *"downloaded=0"* ]] || { echo "PPSSPP Agent A initial upload failed" >&2; exit 1; }
sync_b1="$(run_agent "${device_b_root}" agent sync --config /device/agent.json)"
[[ "${sync_b1}" == *"sync_status=complete"* && "${sync_b1}" == *"uploaded=0"* && "${sync_b1}" == *"downloaded=1"* ]] || { echo "PPSSPP Agent B initial download failed" >&2; exit 1; }
for name in PARAM.SFO DATA.BIN; do
  cmp "${device_a_root}/PPSSPP-user/${save_relative}/${name}" "${device_b_root}/PPSSPP-user/${save_relative}/${name}"
done

printf '%s' 'ppsspp-agent-data-v2' > "${device_a_root}/PPSSPP-user/${save_relative}/DATA.BIN"
sync_a2="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a2}" == *"uploaded=1"* && "${sync_a2}" == *"conflicts=0"* ]] || { echo "PPSSPP Agent A second upload failed" >&2; exit 1; }
sync_b2="$(run_agent "${device_b_root}" agent sync --config /device/agent.json)"
[[ "${sync_b2}" == *"downloaded=1"* && "${sync_b2}" == *"conflicts=0"* ]] || { echo "PPSSPP Agent B atomic update failed" >&2; exit 1; }
[[ "$(<"${device_b_root}/PPSSPP-user/${save_relative}/DATA.BIN")" == 'ppsspp-agent-data-v2' ]] || { echo "PPSSPP Agent B did not install the second revision" >&2; exit 1; }

backup_root="${device_b_root}/.varkiv/backups/agent-ppsspp-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do
  backup_dir="${candidate}"
  backup_count=$((backup_count + 1))
done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 ]] || { echo "PPSSPP recoverable backup count drifted" >&2; exit 1; }
[[ "$(<"${backup_dir}/DATA.BIN")" == 'ppsspp-agent-data-v1' ]] || { echo "PPSSPP recoverable backup content drifted" >&2; exit 1; }

printf '%s' 'ppsspp-agent-data-v3' > "${device_a_root}/PPSSPP-user/${save_relative}/DATA.BIN"
sync_a3="$(run_agent "${device_a_root}" agent sync --config /device/agent.json)"
[[ "${sync_a3}" == *"uploaded=1"* ]] || { echo "PPSSPP Agent A third upload failed" >&2; exit 1; }
printf '%s' 'ppsspp-agent-local-important' > "${device_b_root}/PPSSPP-user/${save_relative}/PARAM.SFO"
set +e
conflict_output="$(run_agent "${device_b_root}" agent sync --config /device/agent.json 2>&1)"
conflict_status=$?
set -e
[[ "${conflict_status}" != 0 && "${conflict_output}" == *"conflicts=1"* ]] || { echo "PPSSPP Agent conflict was not preserved" >&2; exit 1; }
[[ "$(<"${device_b_root}/PPSSPP-user/${save_relative}/PARAM.SFO")" == 'ppsspp-agent-local-important' ]] || { echo "PPSSPP Agent conflict overwrote local data" >&2; exit 1; }

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-ppsspp-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 2) and all(.data[].files[]; .logical_path == "DATA.BIN" or .logical_path == "PARAM.SFO")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-rocknix" and .capabilities.save_streams == true and .capabilities.multi_file_saves == true)' <<<"${devices_json}" >/dev/null

combined_output="${sync_a1}${sync_b1}${sync_a2}${sync_b2}${sync_a3}${conflict_output}${revisions_json}${devices_json}"
for private_value in '/device/' 'private-device-game.iso' 'PPSSPP-user' 'ULUS-00000' "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "PPSSPP Agent output disclosed private device data" >&2; exit 1; }
done

rm -f -- "${agent_config_a}" "${agent_config_b}" "${agent_config_a}.sync.lock" "${agent_config_b}.sync.lock"
jq -n --arg version "${runtime_version}" --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-ppsspp-agent-acceptance-v1",version:$version,target:"rocknix",os_family:"linux",architecture:"arm64",driver_id:"builtin-driver-ppsspp",driver_root_explicit:true,product_code_directory:true,rom_sha256:$rom_sha256,paired_devices:2,files_per_revision:2,uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,agent_configs_retained:false}}' > "${evidence_root}/report.json"

echo "ppsspp_agent_acceptance=passed version=${runtime_version} target=rocknix paired=2 files=2 uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
