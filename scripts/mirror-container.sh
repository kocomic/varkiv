#!/usr/bin/env bash

set -euo pipefail

source_image=""
target_image=""
tags=()
while (($#)); do
  case "$1" in
    --source|--target|--tag)
      (($# >= 2)) || { echo 'missing argument value' >&2; exit 2; }
      case "$1" in
        --source) source_image=$2 ;;
        --target) target_image=$2 ;;
        --tag) tags+=("$2") ;;
      esac
      shift 2
      ;;
    *) echo 'usage: mirror-container.sh --source GHCR_DIGEST --target DOCKER_HUB_REPOSITORY --tag TAG [--tag TAG]' >&2; exit 2 ;;
  esac
done

[[ "$source_image" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[a-f0-9]{64}$ ]] || exit 2
[[ "$target_image" =~ ^docker\.io/[a-z0-9._-]+/[a-z0-9._-]+$ ]] || exit 2
((${#tags[@]} > 0)) || exit 2
for tag in "${tags[@]}"; do
  [[ "$tag" == edge || "$tag" =~ ^sha-[a-f0-9]{12}$ || "$tag" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || {
    echo 'only edge, commit, and semantic version tags are supported' >&2
    exit 2
  }
done

command -v skopeo >/dev/null
command -v shasum >/dev/null
mirror_root=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-mirror.XXXXXX")
trap 'rm -f -- "$mirror_root/source.json" "$mirror_root/target.json" "$mirror_root/error.log"; rmdir -- "$mirror_root"' EXIT
digest="${source_image##*@}"

skopeo inspect --raw "docker://$source_image" > "$mirror_root/source.json"
[[ "sha256:$(shasum -a 256 "$mirror_root/source.json" | awk '{print $1}')" == "$digest" ]] || {
  echo 'source manifest does not match its immutable digest' >&2
  exit 1
}

# Preflight every immutable tag before writing any target. Unknown errors fail
# closed: an authentication or network failure must never authorize an overwrite.
for tag in "${tags[@]}"; do
  if skopeo inspect --raw "docker://$target_image:$tag" > "$mirror_root/target.json" 2> "$mirror_root/error.log"; then
    found="sha256:$(shasum -a 256 "$mirror_root/target.json" | awk '{print $1}')"
    if [[ "$tag" != edge && "$found" != "$digest" ]]; then
      echo "refusing to replace existing immutable tag: $target_image:$tag" >&2
      exit 1
    fi
  elif ! grep -Eqi 'manifest unknown|name unknown' "$mirror_root/error.log"; then
    echo "unable to prove target tag is absent: $target_image:$tag" >&2
    exit 1
  fi
done

for tag in "${tags[@]}"; do
  if [[ "$tag" == edge ]]; then
    skopeo inspect --raw "docker://${source_image%@*}:edge" > "$mirror_root/target.json"
    if [[ "sha256:$(shasum -a 256 "$mirror_root/target.json" | awk '{print $1}')" != "$digest" ]]; then
      echo 'container_mirror=edge_skipped reason=newer_source_image'
      continue
    fi
  fi
  # --all retains both architectures and the embedded SBOM/provenance manifests.
  # --preserve-digests rejects registries that rewrite the manifest format.
  skopeo copy --all --preserve-digests --retry-times 3 \
    "docker://$source_image" "docker://$target_image:$tag"
  skopeo inspect --raw "docker://$target_image:$tag" > "$mirror_root/target.json"
  [[ "sha256:$(shasum -a 256 "$mirror_root/target.json" | awk '{print $1}')" == "$digest" ]] || {
    echo "mirrored manifest digest mismatch: $target_image:$tag" >&2
    exit 1
  }
  printf 'container_mirror=passed image=%s:%s digest=%s\n' "$target_image" "$tag" "$digest"
done
