#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-device-windows-save.sh' '' \
    'Runs the isolated Windows handheld package and save-sync acceptance.'
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
server_image="${VARKIV_WINDOWS_SERVER_IMAGE:-varkiv:${version}}"
native_wine_image="${VARKIV_WINDOWS_ACCEPTANCE_IMAGE:-varkiv-windows-acceptance:debian13-wine10-20251229}"
qemu_wine_image="${VARKIV_WINDOWS_QEMU_ACCEPTANCE_IMAGE:-varkiv-windows-qemu-acceptance:debian13-wine10-20251229}"
temp_parent="${TMPDIR:-/tmp}"
container_user="$(id -u):$(id -g)"
windows_runtime="${VARKIV_WINDOWS_RUNTIME:-auto}"
host_system="$(uname -s)"
host_arch="$(uname -m)"

case "${windows_runtime}" in
  auto)
    if [[ "${host_system}" == "Darwin" && "${host_arch}" == "arm64" ]]; then
      windows_runtime="qemu"
    else
      windows_runtime="native"
    fi
    ;;
  native|qemu) ;;
  *) echo "VARKIV_WINDOWS_RUNTIME must be auto, native, or qemu" >&2; exit 1 ;;
esac
if [[ "${windows_runtime}" == "qemu" ]]; then
  wine_image="${qemu_wine_image}"
  wine_platform="linux/arm64"
  runtime_emulation="qemu-user-static"
  # Docker Desktop presents macOS bind mounts as root-owned inside its VM.
  # This acceptance container has no capabilities, a read-only root, and only
  # synthetic scoped mounts; native Linux CI continues to run as the caller.
  wine_container_user="${VARKIV_WINDOWS_CONTAINER_USER:-0:0}"
else
  wine_image="${native_wine_image}"
  wine_platform="linux/amd64"
  runtime_emulation="none"
  wine_container_user="${VARKIV_WINDOWS_CONTAINER_USER:-${container_user}}"
fi

if [[ -n "${VARKIV_WINDOWS_AGENT_DIR:-}" ]]; then
  evidence_root="${VARKIV_WINDOWS_AGENT_DIR}"
  [[ ! -e "${evidence_root}" ]] || { echo "Windows Agent evidence directory already exists" >&2; exit 1; }
  mkdir -m 700 -p "${evidence_root}"
else
  evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-windows-agent.XXXXXX")"
fi

server_container="varkiv-windows-agent-server-$$"
agent_container="varkiv-windows-agent-client-$$"
task_container="varkiv-windows-agent-task-$$"
network_name="varkiv-windows-agent-$$"
server_root="${evidence_root}/server"
device_a_root="${evidence_root}/device-a"
device_b_root="${evidence_root}/device-b"
pair_root="${evidence_root}/pair"
wine_prefix_a="${evidence_root}/wine-prefix-a"
wine_prefix_pair="${evidence_root}/wine-prefix-pair"
wine_prefix_installed="${evidence_root}/wine-prefix-installed"
package_root="${evidence_root}/target-package"
binary_root="${evidence_root}/bin"
windows_binary="${binary_root}/varkiv.exe"
linux_binary="${binary_root}/varkiv-linux"
host_binary="${binary_root}/varkiv-host"
agent_config_a="${device_a_root}/agent.json"
agent_config_b_pair="${pair_root}/agent.json"
agent_config_b_package="${package_root}/Varkiv/agent.json"
windows_install_dir='C:\Users\Fixture\AppData\Local\Varkiv'
installed_dir="${wine_prefix_installed}/drive_c/users/Fixture/AppData/Local/Varkiv"
agent_config_b="${installed_dir}/agent.json"
task_xml="${installed_dir}/varkiv-agent-task.xml"

purge_prefix() {
  local prefix=$1
  case "${prefix}" in
    "${evidence_root}"/wine-prefix-*) rm -rf -- "${prefix}" ;;
    *) echo "refusing to remove unexpected Wine prefix: ${prefix}" >&2; return 1 ;;
  esac
}

cleanup() {
  local status=$?
  rm -f -- "${agent_config_a}" "${agent_config_b_pair}" "${agent_config_b_package}" "${agent_config_b}" \
    "${agent_config_a}.sync.lock" "${agent_config_b_pair}.sync.lock" "${agent_config_b}.sync.lock"
  for container_name in "${task_container}" "${agent_container}" "${server_container}"; do
    if docker container inspect "${container_name}" >/dev/null 2>&1; then
      docker rm --force "${container_name}" >/dev/null 2>&1 || true
    fi
  done
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null 2>&1 || true
  fi
  for prefix in "${wine_prefix_a}" "${wine_prefix_pair}" "${wine_prefix_installed}"; do
    [[ ! -e "${prefix}" ]] || purge_prefix "${prefix}" || true
  done
  return "${status}"
}
trap cleanup EXIT INT TERM

for command_name in docker go curl jq openssl ruby shasum stat find cmp id file; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
docker image inspect "${server_image}" >/dev/null 2>&1 || { echo "missing current Varkiv image: ${server_image}" >&2; exit 1; }

if ! docker image inspect "${native_wine_image}" >/dev/null 2>&1; then
  build_args=()
  [[ -z "${VARKIV_DEBIAN_SNAPSHOT_HOST:-}" ]] || build_args+=(--build-arg "DEBIAN_SNAPSHOT_HOST=${VARKIV_DEBIAN_SNAPSHOT_HOST}")
  docker build --platform linux/amd64 --file "${project_root}/Dockerfile.windows-acceptance" \
    --tag "${native_wine_image}" "${build_args[@]}" "${project_root}"
fi
if [[ "${windows_runtime}" == "qemu" ]] && ! docker image inspect "${qemu_wine_image}" >/dev/null 2>&1; then
  docker build --platform linux/arm64 --file "${project_root}/Dockerfile.windows-qemu-acceptance" \
    --build-arg "WINE_ROOT_IMAGE=${native_wine_image}" --tag "${qemu_wine_image}" "${project_root}"
fi
if [[ "${windows_runtime}" == "qemu" ]]; then
  wine_packages="$(docker run --rm --platform "${wine_platform}" --network none --read-only --cap-drop=ALL \
    --security-opt=no-new-privileges --entrypoint /usr/bin/dpkg-query "${wine_image}" \
    --admindir=/opt/amd64/var/lib/dpkg --show --showformat='${Package}=${Version}\n' wine wine64)"
  [[ "${wine_packages}" == $'wine=10.0~repack-6\nwine64=10.0~repack-6' ]] || { echo "Windows acceptance Wine packages drifted" >&2; exit 1; }
  wine_version='wine-10.0 (Debian 10.0~repack-6)'
else
  wine_version="$(docker run --rm --platform "${wine_platform}" --network none --read-only --cap-drop=ALL \
    --security-opt=no-new-privileges "${wine_image}" --version)"
  [[ "${wine_version}" == 'wine-10.0 (Debian 10.0~repack-6)' ]] || { echo "Windows acceptance Wine identity drifted: ${wine_version}" >&2; exit 1; }
fi
wine_image_id="$(docker image inspect "${wine_image}" --format '{{.Id}}')"
if [[ "${windows_runtime}" == "qemu" ]]; then
  qemu_version="$(docker run --rm --platform "${wine_platform}" --network none --read-only --cap-drop=ALL \
    --security-opt=no-new-privileges --entrypoint /usr/bin/qemu-x86_64-static "${wine_image}" --version | head -n 1)"
  [[ "${qemu_version}" == 'qemu-x86_64 version 10.0.6 (Debian 1:10.0.6+ds-0+deb13u2)' ]] || { echo "Windows acceptance QEMU identity drifted: ${qemu_version}" >&2; exit 1; }
else
  qemu_version=""
fi

fixture_rom="${project_root}/testdata/pegasus/gba/Advance Wars (USA).gba"
expected_rom_sha="fc7c9a43789d27038753bdf114a59d39eb53aabe0a765b3512e6d584d17f9735"
[[ -f "${fixture_rom}" && ! -L "${fixture_rom}" ]] || { echo "Windows fixture ROM is unavailable" >&2; exit 1; }
[[ "$(shasum -a 256 "${fixture_rom}" | awk '{print $1}')" == "${expected_rom_sha}" ]] || { echo "Windows fixture ROM identity drifted" >&2; exit 1; }

rom_name_a="legion-private-name.gba"
rom_name_b="winmax-private-name.gba"
save_name_a="legion-private-name.srm"
save_name_b="winmax-private-name.srm"
mkdir -m 700 -p "${device_a_root}/roms/gba" "${device_a_root}/saves" \
  "${device_b_root}/roms/gba" "${device_b_root}/saves" "${pair_root}" \
  "${wine_prefix_a}" "${wine_prefix_pair}" "${wine_prefix_installed}" \
  "${server_root}/data" "${server_root}/state" "${server_root}/library/gba" "${binary_root}"
cp "${fixture_rom}" "${server_root}/library/gba/agent-retroarch.gba"
cp "${fixture_rom}" "${device_a_root}/roms/gba/${rom_name_a}"
cp "${fixture_rom}" "${device_b_root}/roms/gba/${rom_name_b}"
printf '%s' 'windows-agent-save-v1' > "${device_a_root}/saves/${save_name_a}"

(
  cd "${project_root}"
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o "${windows_binary}" ./cmd/varkiv
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "${linux_binary}" ./cmd/varkiv
  go build -trimpath -o "${host_binary}" ./cmd/varkiv
)
file "${windows_binary}" | grep -Eq 'PE32\+ executable .*x86-64' || { echo "Windows Agent is not an x86-64 PE" >&2; exit 1; }
chmod -R a+rwX "${server_root}"
chmod 0700 "${windows_binary}" "${host_binary}"
chmod 0755 "${linux_binary}"

owner_token="windows-agent-$(openssl rand -hex 24)"
docker network create "${network_name}" >/dev/null
docker run --detach --rm --name "${server_container}" --network "${network_name}" --network-alias server \
  --publish 127.0.0.1::8080 --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev --env "GAME_LIBRARY_TOKEN=${owner_token}" \
  --mount "type=bind,src=${linux_binary},dst=/usr/local/bin/varkiv,readonly" \
  --mount "type=bind,src=${server_root},dst=/server" --entrypoint /usr/local/bin/varkiv \
  "${server_image}" serve --addr 0.0.0.0:8080 --db /server/data/library.db --state /server/state --library /server/library >/dev/null

mapped_address=""
for _ in $(seq 1 100); do
  mapped_address="$(docker port "${server_container}" 8080/tcp 2>/dev/null | tail -n 1)"
  [[ -n "${mapped_address}" ]] && break
  sleep 0.1
done
[[ "${mapped_address}" == 127.0.0.1:* ]] || { echo "Windows Agent server was not published on loopback" >&2; exit 1; }
server_origin="http://${mapped_address}"
for _ in $(seq 1 200); do
  if curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl --silent --show-error --fail "${server_origin}/api/v1/health/ready" | jq -e '.status == "ready" and (.schema_version | type) == "number" and .schema_version == .supported_schema_version' >/dev/null
server_network_ip="$(docker container inspect "${server_container}" --format "{{with index .NetworkSettings.Networks \"${network_name}\"}}{{.IPAddress}}{{end}}")"
[[ "${server_network_ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { echo "Windows Agent server network address is unavailable" >&2; exit 1; }
agent_server_origin="http://${server_network_ip}:8080"

owner_api() {
  curl --silent --show-error --fail --header "Authorization: Bearer ${owner_token}" "$@"
}
post_json() {
  local path=$1
  local body=$2
  owner_api --request POST --header 'Content-Type: application/json' --data-binary "${body}" "${server_origin}/api/v1/${path}"
}

post_json games '{"id":"agent-windows-game","default_title":"Windows Agent fixture","platform":"gba","titles":{}}' >/dev/null
post_json editions '{"id":"agent-windows-edition","game_id":"agent-windows-game","default_title":"Windows Agent fixture","edition_type":"original","languages":["en"],"titles":{},"artifact_path":"gba/agent-retroarch.gba","artifact_role":"rom"}' >/dev/null
post_json save-bindings/setup '{"stream":{"id":"agent-windows-stream","owner_type":"edition","owner_key":"agent-windows-edition","driver_id":"builtin-driver-retroarch","portability":"core-dependent","edition_ids":["agent-windows-edition"],"compatibility":"native"},"binding":{"id":"agent-windows-binding","edition_id":"agent-windows-edition","device_profile_id":"builtin-device-windows-handheld","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","local_paths":["{{device.save_dir}}/{{rom.stem}}.srm"],"discovery":{"mode":"file","refresh":"process-exit"},"enabled":true}}' >/dev/null
post_json launch-bindings '{"edition_id":"agent-windows-edition","device_profile_id":"builtin-device-windows-handheld","driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","arguments":["-L","{{core.library}}","{{rom.path}}"],"enabled":true}' >/dev/null

run_wine_agent() {
  local prefix=$1
  shift
  docker run --rm --name "${agent_container}" --platform "${wine_platform}" --network "${network_name}" \
    --user "${wine_container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev --env HOME=/tmp --env XDG_RUNTIME_DIR=/tmp --env WINEPREFIX=/wineprefix \
    --mount "type=bind,src=${prefix},dst=/wineprefix" \
    --mount "type=bind,src=${windows_binary},dst=/work/varkiv.exe,readonly" \
    --mount "type=bind,src=${device_a_root},dst=/device-a" \
    --mount "type=bind,src=${device_b_root},dst=/device-b" \
    --mount "type=bind,src=${pair_root},dst=/pair" \
    "${wine_image}" "$@"
}

prime_wine_prefix() {
  local prefix=$1 probe_output probe_status probe_version attempt
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    set +e
    probe_output="$(run_wine_agent "${prefix}" 'Z:\work\varkiv.exe' version 2>&1 | tr -d '\r')"
    probe_status=$?
    set -e
    probe_version="$(awk '$1 == "Varkiv" && NF == 2 {print $2; found=1} END {if (!found) exit 1}' <<<"${probe_output}" 2>/dev/null || true)"
    if [[ "${probe_status}" == 0 && -n "${probe_version}" ]]; then
      printf '%s\n' "${probe_version}"
      return 0
    fi
    if [[ "${probe_output}" != *'rosetta error:'* && "${probe_output}" != *'error opening file descriptor for wineserver tmpdir file'* && "${probe_output}" != *'failed to open L"C:\\windows\\syswow64\\rundll32.exe"'* ]]; then
      printf '%s\n' "${probe_output}" >&2
      [[ "${probe_status}" != 0 ]] && return "${probe_status}"
      return 1
    fi
    if [[ "${attempt}" == 5 ]]; then
      purge_prefix "${prefix}"
      mkdir -m 700 -p "${prefix}"
    fi
  done
  echo "Windows PE probe did not become ready after ten bounded ${windows_runtime} attempts" >&2
  return 1
}

runtime_version="$(prime_wine_prefix "${wine_prefix_a}")"
[[ "${runtime_version}" == "${version}" ]] || { echo "Windows PE version identity drifted" >&2; exit 1; }
echo "windows_agent_stage=pe_probe passed=true"

pairing_code_a="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-windows-handheld"}}' | jq -er '.code')"
echo "windows_agent_stage=pair_a"
pair_output_a="$(run_wine_agent "${wine_prefix_a}" 'Z:\work\varkiv.exe' agent pair --config 'Z:\device-a\agent.json' \
  --server "${agent_server_origin}" --code "${pairing_code_a}" --name 'Windows Agent A' --root 'Z:\device-a' \
  --os windows --distribution windows --arch amd64 --allow-http \
  --path 'save_dir=Z:\device-a\saves' --rom-root 'gba=Z:\device-a\roms\gba' | tr -d '\r')"
[[ "${pair_output_a}" == "paired=true config_saved=true" ]] || { echo "Windows Agent A pairing did not complete" >&2; exit 1; }
chmod 0600 "${agent_config_a}"

pairing_code_b="$(post_json pairing-codes '{"expires_in_seconds":600,"requested_device":{"device_profile_id":"builtin-device-windows-handheld"}}' | jq -er '.code')"
echo "windows_agent_stage=pair_b"
prime_wine_prefix "${wine_prefix_pair}" >/dev/null
pair_output_b="$(run_wine_agent "${wine_prefix_pair}" 'Z:\work\varkiv.exe' agent pair --config 'Z:\pair\agent.json' \
  --server "${agent_server_origin}" --code "${pairing_code_b}" --name 'Windows packaged Agent B' --root 'Z:\device-b' \
  --os windows --distribution windows --arch amd64 --allow-http \
  --path 'save_dir=Z:\device-b\saves' --rom-root 'gba=Z:\device-b\roms\gba' | tr -d '\r')"
[[ "${pair_output_b}" == "paired=true config_saved=true" ]] || { echo "Windows packaged Agent pairing did not complete" >&2; exit 1; }
chmod 0600 "${agent_config_b_pair}"
[[ "$(stat -f '%Lp' "${agent_config_a}" 2>/dev/null || stat -c '%a' "${agent_config_a}")" == 600 ]] || { echo "Windows Agent A config permissions are not private" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${agent_config_b_pair}" 2>/dev/null || stat -c '%a' "${agent_config_b_pair}")" == 600 ]] || { echo "Windows packaged Agent config permissions are not private" >&2; exit 1; }

"${host_binary}" agent target-package --kind windows-handheld --binary "${windows_binary}" \
  --config "${agent_config_b_pair}" --windows-user 'FIXTURE\Player' \
  --windows-install-dir "${windows_install_dir}" --out "${package_root}" >/dev/null
package_verification="$("${host_binary}" agent target-package verify --path "${package_root}" --json)"
jq -e '.verified == true and .kind == "windows-handheld" and .files == 7 and .missing == 0 and .changed == 0 and .extra == 0' <<<"${package_verification}" >/dev/null
echo "windows_agent_stage=package passed=true"
prime_wine_prefix "${wine_prefix_installed}" >/dev/null
mkdir -m 700 -p "${installed_dir}"
cp -R "${package_root}/Varkiv/." "${installed_dir}/"

read_task_value() {
  local element=$1
  ruby -r rexml/document -r rexml/xpath -e 'doc=REXML::Document.new(File.read(ARGV[0])); node=REXML::XPath.first(doc, "//*[local-name()=\"" + ARGV[1] + "\"]"); abort "missing task element" unless node; print node.text' "${task_xml}" "${element}"
}
task_command="$(read_task_value Command)"
task_arguments="$(read_task_value Arguments)"
task_working_dir="$(read_task_value WorkingDirectory)"
[[ "${task_command}" == "${windows_install_dir}\\varkiv.exe" ]] || { echo "Windows Task command drifted" >&2; exit 1; }
[[ "${task_arguments}" == "agent run --config \"${windows_install_dir}\\agent.json\" --interval 1m0s" ]] || { echo "Windows Task arguments drifted" >&2; exit 1; }
[[ "${task_working_dir}" == "${windows_install_dir}" ]] || { echo "Windows Task working directory drifted" >&2; exit 1; }
grep -Fq '<LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel>' "${task_xml}" || { echo "Windows Task privilege contract drifted" >&2; exit 1; }

run_packaged_agent() {
  docker run --rm --name "${agent_container}" --platform "${wine_platform}" --network "${network_name}" \
    --user "${wine_container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev --env HOME=/tmp --env XDG_RUNTIME_DIR=/tmp --env WINEPREFIX=/wineprefix \
    --mount "type=bind,src=${wine_prefix_installed},dst=/wineprefix" \
    --mount "type=bind,src=${device_b_root},dst=/device-b" \
    "${wine_image}" "${task_command}" "$@"
}
start_packaged_task() {
  docker run --detach --rm --name "${task_container}" --platform "${wine_platform}" --network "${network_name}" \
    --user "${wine_container_user}" --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev --env HOME=/tmp --env XDG_RUNTIME_DIR=/tmp --env WINEPREFIX=/wineprefix \
    --mount "type=bind,src=${wine_prefix_installed},dst=/wineprefix" \
    --mount "type=bind,src=${device_b_root},dst=/device-b" \
    "${wine_image}" "${task_command}" agent run --config "${windows_install_dir}\\agent.json" --interval 1m0s >/dev/null
}
stop_packaged_task() {
  if docker container inspect "${task_container}" >/dev/null 2>&1; then docker rm --force "${task_container}" >/dev/null; fi
}
wait_for_file_value() {
  local file_name=$1 expected=$2
  for _ in $(seq 1 300); do
    if [[ -f "${file_name}" && "$(<"${file_name}")" == "${expected}" ]]; then return 0; fi
    sleep 0.1
  done
  return 1
}
wait_for_packaged_download() {
  local previous_finished=${1:-}
  for _ in $(seq 1 300); do
    if jq -e --arg previous "${previous_finished}" '.last_sync.state == "complete" and .last_sync.uploaded == 0 and .last_sync.downloaded == 1 and .last_sync.conflicts == 0 and .last_sync.finished_at != $previous' "${agent_config_b}" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  return 1
}

sync_a1="$(run_wine_agent "${wine_prefix_a}" 'Z:\work\varkiv.exe' agent sync --config 'Z:\device-a\agent.json' | tr -d '\r')"
echo "windows_agent_stage=initial_upload"
[[ "${sync_a1}" == *"sync_status=complete"* && "${sync_a1}" == *"uploaded=1"* ]] || { echo "Windows Agent A initial upload failed" >&2; exit 1; }
sync_b1="$(run_packaged_agent agent sync --config "${windows_install_dir}\\agent.json" | tr -d '\r')"
echo "windows_agent_stage=initial_download"
[[ "${sync_b1}" == *"downloaded=1"* && "${sync_b1}" == *"conflicts=0"* ]] || { echo "Windows packaged Agent initial download failed" >&2; exit 1; }
wait_for_file_value "${device_b_root}/saves/${save_name_b}" 'windows-agent-save-v1' || { echo "Windows packaged Agent initial content drifted" >&2; exit 1; }
b1_finished_at="$(jq -er '.last_sync.finished_at' "${agent_config_b}")"

printf '%s' 'windows-agent-save-v2' > "${device_a_root}/saves/${save_name_a}"
sync_a2="$(run_wine_agent "${wine_prefix_a}" 'Z:\work\varkiv.exe' agent sync --config 'Z:\device-a\agent.json' | tr -d '\r')"
[[ "${sync_a2}" == *"uploaded=1"* && "${sync_a2}" == *"conflicts=0"* ]] || { echo "Windows Agent A second upload failed" >&2; exit 1; }
start_packaged_task
echo "windows_agent_stage=task_argv"
wait_for_file_value "${device_b_root}/saves/${save_name_b}" 'windows-agent-save-v2' || { echo "Windows Task argv download failed" >&2; exit 1; }
wait_for_packaged_download "${b1_finished_at}" || { echo "Windows Task argv did not persist its result" >&2; exit 1; }
stop_packaged_task
sync_b2="$(run_packaged_agent agent status --config "${windows_install_dir}\\agent.json" --json | tr -d '\r')"
jq -e '.last_sync.state == "complete" and .last_sync.downloaded == 1 and .last_sync.conflicts == 0' <<<"${sync_b2}" >/dev/null

backup_root="${device_b_root}/.varkiv/backups/agent-windows-stream"
backup_dir=""
backup_count=0
while IFS= read -r candidate; do backup_dir="${candidate}"; backup_count=$((backup_count + 1)); done < <(find "${backup_root}" -mindepth 1 -maxdepth 1 -type d -print)
[[ "${backup_count}" == 1 && "$(<"${backup_dir}/primary.srm")" == 'windows-agent-save-v1' ]] || { echo "Windows recoverable backup drifted" >&2; exit 1; }

printf '%s' 'windows-agent-save-v3' > "${device_a_root}/saves/${save_name_a}"
sync_a3="$(run_wine_agent "${wine_prefix_a}" 'Z:\work\varkiv.exe' agent sync --config 'Z:\device-a\agent.json' | tr -d '\r')"
[[ "${sync_a3}" == *"uploaded=1"* ]] || { echo "Windows Agent A third upload failed" >&2; exit 1; }
printf '%s' 'windows-agent-local-important' > "${device_b_root}/saves/${save_name_b}"
set +e
conflict_output="$(run_packaged_agent agent sync --config "${windows_install_dir}\\agent.json" 2>&1 | tr -d '\r')"
conflict_status=$?
set -e
[[ "${conflict_status}" != 0 && "${conflict_output}" == *"conflicts=1"* ]] || { echo "Windows Agent conflict was not preserved" >&2; exit 1; }
[[ "$(<"${device_b_root}/saves/${save_name_b}")" == 'windows-agent-local-important' ]] || { echo "Windows Agent conflict overwrote local data" >&2; exit 1; }
echo "windows_agent_stage=conflict passed=true"

revisions_json="$(owner_api "${server_origin}/api/v1/save-streams/agent-windows-stream/revisions?limit=20&offset=0")"
jq -e '(.data | length) == 3 and all(.data[]; .file_count == 1 and .files[0].logical_path == "primary.srm")' <<<"${revisions_json}" >/dev/null
devices_json="$(owner_api "${server_origin}/api/v1/devices?limit=20&offset=0")"
jq -e '(.data | length) == 2 and all(.data[]; .device_profile_id == "builtin-device-windows-handheld" and .capabilities.save_streams == true)' <<<"${devices_json}" >/dev/null

combined_output="${sync_a1}${sync_b1}${sync_a2}${sync_b2}${sync_a3}${conflict_output}${revisions_json}${devices_json}${package_verification}"
for private_value in 'Z:\device-' "${agent_server_origin}" "${rom_name_a}" "${rom_name_b}" "${save_name_a}" "${save_name_b}" "${owner_token}"; do
  [[ "${combined_output}" != *"${private_value}"* ]] || { echo "Windows Agent output disclosed private device data" >&2; exit 1; }
done

rm -f -- "${agent_config_a}" "${agent_config_b_pair}" "${agent_config_b_package}" "${agent_config_b}" \
  "${agent_config_a}.sync.lock" "${agent_config_b_pair}.sync.lock" "${agent_config_b}.sync.lock"
purge_prefix "${wine_prefix_a}"
purge_prefix "${wine_prefix_pair}"
purge_prefix "${wine_prefix_installed}"
jq -n --arg version "${runtime_version}" --arg wine "${wine_version}" --arg wine_image "${wine_image_id}" --arg process_user "${wine_container_user}" \
  --arg runtime_mode "${windows_runtime}" --arg emulation "${runtime_emulation}" --arg qemu "${qemu_version}" \
  --arg rom_sha256 "${expected_rom_sha}" \
  '{format:"varkiv-windows-agent-acceptance-v1",version:$version,target:"windows-handheld",os_family:"windows",architecture:"amd64",execution:{host:"linux-container",runtime_mode:$runtime_mode,runtime:$wine,runtime_image:$wine_image,container_user:$process_user,emulation:$emulation,qemu:$qemu,hardware:false,task_scheduler:false},driver_id:"builtin-driver-retroarch",core_id:"builtin-core-mgba",rom_sha256:$rom_sha256,paired_devices:2,distinct_local_rom_stems:true,logical_role:"primary.srm",target_package:{generated:true,verified:true,file_count:7,installed_into_new_prefix:true,task_xml_argv_ran:true,least_privilege_declared:true},uploads:3,downloads:2,conflicts:1,revisions:3,recoverable_backup:true,privacy:{user_library_read:false,paths_reported:false,tokens_reported:false,local_basenames_reported:false,agent_configs_retained:false,wine_prefixes_retained:false}}' > "${evidence_root}/report.json"

echo "windows_agent_acceptance=passed version=${runtime_version} target=windows-handheld paired=2 package=verified task_xml_argv=ran uploads=3 downloads=2 conflicts=1 revisions=3 backup=1"
echo "evidence_root=${evidence_root}"
