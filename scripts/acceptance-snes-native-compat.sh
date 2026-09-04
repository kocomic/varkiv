#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'Usage: scripts/acceptance-snes-native-compat.sh' '' \
    'Runs the isolated Web -> RetroArch/Snes9x -> Agent -> Web save bridge.' \
    'Requires VARKIV_WEB_EMULATOR_DIRECTORY.'
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
fixture_id="snes-spctest-sram"
image="varkiv/retroarch-snes9x-compat:1.22.2-6ca2343"
alpine_mirror="${VARKIV_ALPINE_MIRROR:-https://dl-cdn.alpinelinux.org}"
web_save_sha="48878c969caa13651d00cf0cab230da32e5d1fdd0bdf6217489af87a8f40a3d7"
native_save_sha="17f7c19ea1ad7f71dc8ddcb6b1a5c5af489448febcfc0a57ef43d88f81c6e2d8"
rom_sha="6dc7830c6db7f89d622f6bb8904e0c3f50131561a4d81bc8a4452c749b1a9358"
container_name="varkiv-snes-compat-$$"
temp_parent="${TMPDIR:-/tmp}"
evidence_root="$(mktemp -d "${temp_parent%/}/varkiv-snes-compat.XXXXXX")"
web_first="${evidence_root}/web-first"
native_root="${evidence_root}/native"
web_return="${evidence_root}/web-return"

cleanup_container() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker rm --force "${container_name}" >/dev/null 2>&1 || true
  fi
}
trap cleanup_container EXIT INT TERM

for command_name in docker node npm go unzip shasum; do
  command -v "${command_name}" >/dev/null || { echo "missing required command: ${command_name}" >&2; exit 1; }
done
if [[ -z "${VARKIV_WEB_EMULATOR_DIRECTORY:-}" || "${VARKIV_WEB_EMULATOR_DIRECTORY}" != /* ]]; then
  echo "VARKIV_WEB_EMULATOR_DIRECTORY must be an absolute EmulatorJS 4.2.3 data directory" >&2
  exit 1
fi
[[ "${alpine_mirror}" == https://* ]] || { echo "VARKIV_ALPINE_MIRROR must be an HTTPS origin" >&2; exit 1; }

mkdir -m 700 -p "${native_root}/content" "${native_root}/saves" "${native_root}/states" \
  "${native_root}/system" "${native_root}/playlists" "${native_root}/logs" "${native_root}/config"

(
  cd "${project_root}"
  VARKIV_WEB_ACCEPTANCE_DIR="${web_first}" \
  VARKIV_WEB_ACCEPTANCE_KEEP=1 \
  VARKIV_WEB_ACCEPTANCE_FIXTURE="${fixture_id}" \
  node scripts/acceptance-web-emulation.mjs
)

rom_path="${web_first}/library/snes/${fixture_id}/spctest-sram.sfc"
web_save_path="${web_first}/state/saves/blobs/48/${web_save_sha}"
[[ "$(shasum -a 256 "${rom_path}" | awk '{print $1}')" == "${rom_sha}" ]] || { echo "Web ROM identity drifted" >&2; exit 1; }
[[ "$(shasum -a 256 "${web_save_path}" | awk '{print $1}')" == "${web_save_sha}" ]] || { echo "Web save identity drifted" >&2; exit 1; }
[[ "$(wc -c < "${web_save_path}" | tr -d ' ')" == "2048" ]] || { echo "Web save size drifted" >&2; exit 1; }
[[ "$(od -An -tx1 -N2 "${web_save_path}" | tr -d ' \n')" == "5a60" ]] || { echo "Web first-stage sentinel drifted" >&2; exit 1; }

cp "${rom_path}" "${native_root}/content/spctest-sram.sfc"
cp "${web_save_path}" "${native_root}/saves/spctest-sram.srm"
chmod -R a+rwX "${native_root}"

docker build --progress=plain --build-arg "ALPINE_MIRROR=${alpine_mirror}" --tag "${image}" "${project_root}/testdata/native-snes-compat"
[[ "$(docker image inspect "${image}" --format '{{index .Config.Labels "org.varkiv.retroarch.commit"}}')" == "69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576" ]]
[[ "$(docker image inspect "${image}" --format '{{index .Config.Labels "org.varkiv.snes9x.commit"}}')" == "6ca2343e5f3b0acbea49ca958251e3a0af58a81d" ]]

docker run --detach --rm --name "${container_name}" \
  --network=none --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --tmpfs /home/acceptance:rw,noexec,nosuid,nodev,uid=10001,gid=10001 \
  --env HOME=/home/acceptance \
  --env XDG_CONFIG_HOME=/home/acceptance/.config \
  --env XDG_CACHE_HOME=/home/acceptance/.cache \
  --env XDG_DATA_HOME=/home/acceptance/.local/share \
  --mount "type=bind,src=${native_root},dst=/evidence" \
  --mount "type=bind,src=${project_root}/testdata/native-snes-compat/retroarch.cfg,dst=/config/retroarch.cfg,readonly" \
  "${image}" --config /config/retroarch.cfg --verbose \
  -L /opt/libretro/snes9x_libretro.so /evidence/content/spctest-sram.sfc >/dev/null

loaded=0
for _ in $(seq 1 100); do
  if [[ -f "${native_root}/logs/retroarch.log" ]] && grep -Fq "Checksum OK" "${native_root}/logs/retroarch.log"; then
    loaded=1
    break
  fi
  sleep 0.1
done
[[ "${loaded}" == "1" ]] || { echo "native core did not load the pinned fixture" >&2; exit 1; }
sleep 1
docker kill --signal=SIGINT "${container_name}" >/dev/null
container_removed=0
for _ in $(seq 1 300); do
  if ! docker container inspect "${container_name}" >/dev/null 2>&1; then
    container_removed=1
    break
  fi
  sleep 0.1
done
if ((container_removed != 1)); then
  echo "acceptance container did not clean up" >&2
  exit 1
fi

native_save_path="${native_root}/saves/spctest-sram.srm"
[[ "$(wc -c < "${native_save_path}" | tr -d ' ')" == "2048" ]] || { echo "native save size drifted" >&2; exit 1; }
[[ "$(shasum -a 256 "${native_save_path}" | awk '{print $1}')" == "${native_save_sha}" ]] || { echo "native save identity drifted" >&2; exit 1; }
[[ "$(od -An -tx1 -N2 "${native_save_path}" | tr -d ' \n')" == "5aa5" ]] || { echo "native handshake sentinel missing" >&2; exit 1; }
grep -Fq "RetroArch 1.22.2 (Git 69a4f0ea1e)" "${native_root}/logs/retroarch.log"
grep -Fq 'Redirecting save file to "/evidence/saves/spctest-sram.srm"' "${native_root}/logs/retroarch.log"
grep -Fq "Saved successfully" "${native_root}/logs/retroarch.log"
private_macos_root='/''Users/'
if grep -Eq "${private_macos_root}|Library/Application Support|Documents/RetroArch" "${native_root}/logs/retroarch.log"; then
  echo "native log leaked a host user path" >&2
  exit 1
fi

VARKIV_RUNTIME_BRIDGE_IMAGE="${image}" \
VARKIV_RUNTIME_BRIDGE_ROM="${rom_path}" \
VARKIV_RUNTIME_BRIDGE_SAVE="${native_save_path}" \
VARKIV_RUNTIME_BRIDGE_DIR="${evidence_root}/agent-bridge" \
"${project_root}/scripts/acceptance-device-runtime-bridge.sh"

(
  cd "${project_root}"
  VARKIV_WEB_ACCEPTANCE_DIR="${web_return}" \
  VARKIV_WEB_ACCEPTANCE_KEEP=1 \
  VARKIV_WEB_ACCEPTANCE_FIXTURE="${fixture_id}" \
  VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE="${native_save_path}" \
  VARKIV_WEB_ACCEPTANCE_INITIAL_SAVE_SHA256="${native_save_sha}" \
  VARKIV_WEB_ACCEPTANCE_EXPECT_SAVE_PREFIX_HEX="5aa5" \
  node scripts/acceptance-web-emulation.mjs
)

node -e '
  const report = require(process.argv[1]);
  const fixture = report.fixtures[0];
  if (fixture.save_sha256 !== process.argv[2] || fixture.seeded_save_sha256 !== process.argv[2]) process.exit(1);
  if (!fixture.seeded_save_revision || fixture.runtime !== "started") process.exit(1);
  if (report.privacy.user_library_read || report.privacy.roms_committed_to_repository) process.exit(1);
' "${web_return}/report.json" "${native_save_sha}"

echo "SNES Web -> RetroArch -> Web save compatibility passed"
echo "web_save_sha256=${web_save_sha}"
echo "native_save_sha256=${native_save_sha}"
echo "evidence_root=${evidence_root}"
