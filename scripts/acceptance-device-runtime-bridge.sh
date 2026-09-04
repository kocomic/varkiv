#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-runtime-bridge.sh' '' \
    'Runs the isolated ROCKNIX runtime-identity bridge acceptance.' \
    'Requires VARKIV_RUNTIME_BRIDGE_ROM and VARKIV_RUNTIME_BRIDGE_SAVE.'
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
image="${VARKIV_RUNTIME_BRIDGE_IMAGE:-varkiv/retroarch-snes9x-compat:1.22.2-6ca2343}"
rom_path="${VARKIV_RUNTIME_BRIDGE_ROM:-}"
save_path="${VARKIV_RUNTIME_BRIDGE_SAVE:-}"
expected_rom_sha="6dc7830c6db7f89d622f6bb8904e0c3f50131561a4d81bc8a4452c749b1a9358"
expected_save_sha="17f7c19ea1ad7f71dc8ddcb6b1a5c5af489448febcfc0a57ef43d88f81c6e2d8"
expected_driver_sha="484621fe4675e3cf9a0d47ec9f63d611540dfe98db0d7799d9c8d14e5881b080"
expected_core_sha="52a3ceadeb4798cc323094c614eff20456fad7cf2287a5add8a475c677c3939b"
expected_driver_size=14705288
expected_core_size=2436288
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"

if [[ -n "${VARKIV_RUNTIME_BRIDGE_DIR:-}" ]]; then
  evidence_root="${VARKIV_RUNTIME_BRIDGE_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "runtime bridge evidence directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-runtime-bridge.XXXXXX")"
fi

server_container="varkiv-runtime-bridge-server-$$"
agent_container="varkiv-runtime-bridge-agent-$$"
network_name="varkiv-runtime-bridge-$$"
owner_token="runtime-bridge-owner-$$"
server_root="${evidence_root}/server"
device_root="${evidence_root}/device"
binary_root="${evidence_root}/bin"
varkiv_binary="${binary_root}/varkiv"
agent_config="${device_root}/agent.json"

cleanup() {
  for container_name in "${agent_container}" "${server_container}"; do
    if docker container inspect "${container_name}" >/dev/null 2>&1; then
      docker rm --force "${container_name}" >/dev/null 2>&1 || true
    fi
  done
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

for command_name in docker go curl jq shasum wc od id; do
  command -v "${command_name}" >/dev/null || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
[[ "${rom_path}" == /* && -f "${rom_path}" && ! -L "${rom_path}" ]] || { echo "VARKIV_RUNTIME_BRIDGE_ROM must be an absolute regular file" >&2; exit 1; }
[[ "${save_path}" == /* && -f "${save_path}" && ! -L "${save_path}" ]] || { echo "VARKIV_RUNTIME_BRIDGE_SAVE must be an absolute regular file" >&2; exit 1; }
[[ "$(shasum -a 256 "${rom_path}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "runtime bridge ROM identity drifted" >&2; exit 1; }
[[ "$(shasum -a 256 "${save_path}" | awk '{print $1}')" == "${expected_save_sha}" ]] || { echo "runtime bridge save identity drifted" >&2; exit 1; }
[[ "$(wc -c < "${save_path}" | tr -d ' ')" == "2048" ]] || { echo "runtime bridge save size drifted" >&2; exit 1; }
[[ "$(od -An -tx1 -N2 "${save_path}" | tr -d ' \n')" == "5aa5" ]] || { echo "runtime bridge save sentinel drifted" >&2; exit 1; }

runtime_identity="$(docker run --rm --entrypoint /bin/sh "${image}" -c 'sha256sum /usr/local/bin/retroarch /opt/libretro/snes9x_libretro.so; wc -c /usr/local/bin/retroarch /opt/libretro/snes9x_libretro.so')"
[[ "${runtime_identity}" == *"${expected_driver_sha}  /usr/local/bin/retroarch"* ]] || { echo "RetroArch image identity drifted" >&2; exit 1; }
[[ "${runtime_identity}" == *"${expected_core_sha}  /opt/libretro/snes9x_libretro.so"* ]] || { echo "Snes9x image identity drifted" >&2; exit 1; }
[[ "${runtime_identity}" == *"${expected_driver_size} /usr/local/bin/retroarch"* ]] || { echo "RetroArch image size drifted" >&2; exit 1; }
[[ "${runtime_identity}" == *"${expected_core_size} /opt/libretro/snes9x_libretro.so"* ]] || { echo "Snes9x image size drifted" >&2; exit 1; }

mkdir -m 700 -p "${server_root}/data" "${server_root}/state" "${server_root}/library/snes/agent-snes" \
  "${device_root}/roms/snes" "${device_root}/saves" "${device_root}/drifted-cores" "${binary_root}"
cp "${rom_path}" "${server_root}/library/snes/agent-snes/spctest-sram.sfc"
cp "${rom_path}" "${device_root}/roms/snes/spctest-sram.sfc"
cp "${save_path}" "${device_root}/saves/spctest-sram.srm"
cp "${project_root}/testdata/native-snes-compat/retroarch.cfg" "${device_root}/drifted-cores/snes9x_libretro.so"
(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "${varkiv_binary}" ./cmd/varkiv
)
chmod -R a+rwX "${server_root}" "${device_root}"
chmod 0755 "${varkiv_binary}"
runtime_version="$(docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --entrypoint /usr/local/bin/varkiv "${image}" version | awk 'NF >= 2 {print $2}')"
expected_version="$(tr -d '[:space:]' < "${project_root}/internal/buildinfo/VERSION")"
[[ -n "${runtime_version}" && "${runtime_version}" == "${expected_version}" ]] || { echo "Linux Agent version identity drifted" >&2; exit 1; }

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
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "runtime bridge server port was not published on loopback" >&2; exit 1; }
server_origin="http://${mapped_address}"
for _ in $(seq 1 200); do
  if curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" | jq -e '
  .status == "ready" and
  (.schema_version | type) == "number" and
  .schema_version == .supported_schema_version
' >/dev/null

owner_api() {
  curl --silent --show-error --fail --header "Authorization: Bearer ${owner_token}" "$@"
}
post_json() {
  local path="$1"
  local body="$2"
  owner_api --request POST --header 'Content-Type: application/json' --data-binary "${body}" "${server_origin}/api/v1/${path}"
}

post_json games '{"id":"agent-snes-game","default_title":"Agent SNES fixture","platform":"snes","titles":{}}' >/dev/null
post_json editions '{"id":"agent-snes-edition","game_id":"agent-snes-game","default_title":"Agent SNES fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"snes/agent-snes/spctest-sram.sfc","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-snes-shared-stream","owner_type":"edition","owner_key":"agent-snes-edition","driver_id":"builtin-driver-emulatorjs-snes9x","portability":"core-dependent","compatibility_group_id":"builtin-save-compat-snes9x-raw-srm-v1","edition_ids":["agent-snes-edition"],"compatibility":"verified"},"binding":{"id":"agent-snes-rocknix-binding","edition_id":"agent-snes-edition","device_profile_id":"builtin-device-rocknix","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-snes9x","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"declared","refresh":"process-exit"},"enabled":true}}' >/dev/null
pairing_code="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-rocknix"}}' | jq -er '.code')"

pair_output="$(docker run --rm --name "${agent_container}" --network "${network_name}" \
	--user "${container_user}" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${device_root},dst=/device" \
  --entrypoint /usr/local/bin/varkiv "${image}" agent pair \
  --config /device/agent.json --server http://server:8080 --code "${pairing_code}" \
  --name 'Runtime bridge fixture' --root /device --os linux --distribution rocknix --arch arm64 --allow-http \
  --path save_dir=/device/saves --path emulator_dir=/usr/local/bin --path core_dir=/opt/libretro \
  --rom-root snes=/device/roms/snes)"
[[ "${pair_output}" == "paired=true config_saved=true" ]] || { echo "Agent pairing did not complete" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${agent_config}" 2>/dev/null || stat -c '%a' "${agent_config}")" == "600" ]] || { echo "Agent config permissions are not private" >&2; exit 1; }

sync_output="$(docker run --rm --name "${agent_container}" --network "${network_name}" \
	--user "${container_user}" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${device_root},dst=/device" \
  --entrypoint /usr/local/bin/varkiv "${image}" agent sync --config /device/agent.json)"
[[ "${sync_output}" == *"sync_status=complete"* && "${sync_output}" == *"uploaded=1"* && "${sync_output}" == *"conflicts=0"* ]] || { echo "exact runtime Agent sync did not upload one revision" >&2; exit 1; }

devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
device_id="$(jq -er '.data | select(length == 1) | .[0].id' <<<"${devices_json}")"
jq -e '.data[0].capabilities.runtime_probe == true and .data[0].capabilities.runtime_identity_attested == true and .data[0].capabilities.verified_save_bridge == true' <<<"${devices_json}" >/dev/null
attestations_json="$(owner_api "${server_origin}/api/v1/runtime-attestations?device_id=${device_id}&limit=20&offset=0")"
jq -e --arg driver "${expected_driver_sha}" --arg core "${expected_core_sha}" --argjson driver_size "${expected_driver_size}" --argjson core_size "${expected_core_size}" '
  (.data | length) == 2 and
  any(.data[]; .kind == "driver" and .runtime_id == "builtin-driver-retroarch" and .sha256 == $driver and .size == $driver_size) and
  any(.data[]; .kind == "core" and .runtime_id == "builtin-core-snes9x" and .sha256 == $core and .size == $core_size) and
  (tostring | contains("/") | not) and (tostring | contains(".so") | not)
' <<<"${attestations_json}" >/dev/null
revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-snes-shared-stream/revisions?limit=20&offset=0")"
jq -e --arg save "${expected_save_sha}" '(.data | length) == 1 and .data[0].file_count == 1 and .data[0].total_size == 2048 and .data[0].files[0].checksum == $save and .data[0].files[0].logical_path == "spctest-sram.srm"' <<<"${revisions_json}" >/dev/null

jq '.path_overrides.core_dir = "/device/drifted-cores"' "${agent_config}" > "${device_root}/.agent-drift.json"
chmod 0600 "${device_root}/.agent-drift.json"
mv "${device_root}/.agent-drift.json" "${agent_config}"
drift_output="$(docker run --rm --name "${agent_container}" --network "${network_name}" \
	--user "${container_user}" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${device_root},dst=/device" \
  --entrypoint /usr/local/bin/varkiv "${image}" agent sync --config /device/agent.json)"
[[ "${drift_output}" == *"sync_status=complete"* && "${drift_output}" == *"uploaded=0"* && "${drift_output}" == *"downloaded=0"* ]] || { echo "drifted runtime did not safely omit the shared binding" >&2; exit 1; }
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '.data[0].capabilities.runtime_identity_attested == true and .data[0].capabilities.verified_save_bridge == false' <<<"${devices_json}" >/dev/null
attestations_json="$(owner_api "${server_origin}/api/v1/runtime-attestations?device_id=${device_id}&limit=20&offset=0")"
jq -e --arg core "${expected_core_sha}" '(.data | length) == 2 and any(.data[]; .kind == "core" and .sha256 != $core)' <<<"${attestations_json}" >/dev/null
revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-snes-shared-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 1' <<<"${revisions_json}" >/dev/null

jq '.path_overrides.core_dir = "/opt/libretro"' "${agent_config}" > "${device_root}/.agent-restored.json"
chmod 0600 "${device_root}/.agent-restored.json"
mv "${device_root}/.agent-restored.json" "${agent_config}"
restore_output="$(docker run --rm --name "${agent_container}" --network "${network_name}" \
	--user "${container_user}" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --mount "type=bind,src=${varkiv_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${device_root},dst=/device" \
  --entrypoint /usr/local/bin/varkiv "${image}" agent sync --config /device/agent.json)"
[[ "${restore_output}" == *"sync_status=complete"* && "${restore_output}" == *"uploaded=0"* && "${restore_output}" == *"downloaded=0"* ]] || { echo "restored exact runtime did not finish as a no-op" >&2; exit 1; }
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '.data[0].capabilities.verified_save_bridge == true' <<<"${devices_json}" >/dev/null
revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-snes-shared-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 1' <<<"${revisions_json}" >/dev/null

rm -f "${agent_config}"
jq -n \
  --arg version "${runtime_version}" \
  --arg driver_sha256 "${expected_driver_sha}" --arg core_sha256 "${expected_core_sha}" \
  --arg save_sha256 "${expected_save_sha}" \
  '{format:"varkiv-runtime-bridge-acceptance-v1",version:$version,target:"rocknix",os_family:"linux",architecture:"arm64",driver_id:"builtin-driver-retroarch",core_id:"builtin-core-snes9x",compatibility_group_id:"builtin-save-compat-snes9x-raw-srm-v1",driver_sha256:$driver_sha256,core_sha256:$core_sha256,save_sha256:$save_sha256,paired:true,exact_identity_promoted:true,revision_uploaded:1,drift_revoked:true,restored:true,revision_count:1,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,agent_config_retained:false}}' > "${evidence_root}/report.json"

echo "runtime_bridge_acceptance=passed version=${runtime_version} target=rocknix attestations=2 uploaded=1 drift_revoked=1 restored=1 revisions=1"
echo "evidence_root=${evidence_root}"
