# Portable library and launch formats

Varkiv exports two public JSON files and one internal ownership file in an
integration package. The public files are declarative and portable; the
ownership file is private managed state.

## Library manifest v6

`library-manifest.json` describes games, editions, artifacts, media, series,
and any non-built-in platform definitions needed to interpret the package. New
writers emit `format_version: 6` and SHOULD validate against
[`library-manifest-v6.schema.json`](../schemas/library-manifest-v6.schema.json).

Varkiv retains v4/v5 readers for migration. That is not permission for new
producers to omit v6 integrity and platform fields.

### Layout and identity

- `entries` contains at most 50,000 editions. `game_id` groups editions of the
  same game; each `edition_id` remains an independently hashed/playable item.
- `series` is an optional cross-platform grouping layer. Member relation types
  are `mainline`, `port`, `remake`, `spinoff`, `collection`, or `other`.
- `platform` MUST resolve to a Varkiv built-in platform or to an exact definition
  in `custom_platforms`. At most 256 custom platforms may appear. Each supplied
  custom platform MUST be used, MUST NOT shadow a built-in or collide with
  another registry key, and MUST be enabled. An existing local definition is
  reusable only when its portable fields match exactly and it remains enabled.
- `game_titles` and `edition_titles` are locale-to-title maps. Default titles
  are portable fallbacks; if only one default title is populated, Varkiv uses
  it for the other. `edition_type` defaults to `original` when omitted by an
  older producer, but canonical v6 exports include it.

### Artifacts and media

Every entry contains 1 through 64 paths in `artifacts`. V6 keeps this legacy
path list and pairs it positionally with an equally sized `artifact_records`
array. Each record repeats the same path and adds role, disc index, original
name, size, and SHA-256. Artifact roles are `rom`, `disc`, `executable`,
`patch`, `dlc`, `update`, or `other`; `disc_index` is 0 through 64.

Each entry may contain at most 256 media records. `owner_type` is `game` or
`edition`. Supported kinds are `cover`, `box_front`, `box_back`, `box_spine`,
`logo`, `screenshot`, `title_screen`, `background`, `fanart`, `marquee`,
`bezel`, `manual`, `video`, `music`, `cartridge`, `poster`, `banner`, `tile`,
and `other`.

All paths are portable `/`-separated paths resolved beneath the explicitly
configured library root. The manifest itself must be a regular non-symlink file
inside that root and is limited to 16 MiB. Varkiv rejects traversal and symlink
escapes. Present files are rehashed and their size/hash MUST match the record.
Missing media is skipped. Missing ROM artifacts remain visible during preview
but the affected game is skipped at commit: metadata alone cannot create a
library entry because no content hash can be verified.

The schema covers JSON shape. Varkiv additionally enforces positional
`artifacts`/`artifact_records` equality, path containment, filesystem integrity,
custom-platform registry identity, series referential integrity, and atomic
catalogue commit.

## Launch sidecar v2

`varkiv-launches.json` carries declarative launch resolutions and the minimum
runtime catalogue needed to reconstruct an exported package on another Varkiv
server. It conforms to
[`varkiv-launches-v2.schema.json`](../schemas/varkiv-launches-v2.schema.json).

The sidecar is optional. Varkiv searches for it from the library manifest
toward, but never above, the configured root. It must be a regular non-symlink
file named exactly `varkiv-launches.json` and is limited to 4 MiB. A missing,
unknown-version, or malformed sidecar does not block ROM import.

Each binding identifies an edition, platform, portable ROM path, selected
driver/core/profile IDs, declarative argv, resolved executable hints, Android
component fields when applicable, and warnings. The optional nested
`runtime_catalog` may carry frontend adapters, device profiles, emulator
drivers, RetroArch cores, and one package profile. It contains at most 128
total definitions; a package profile contains at most 64 templates.

Runtime definitions are data, never plugins. Imported launch information is an
inert hint. Varkiv validates identifiers, argv templates, Android Intent
components, relative config/save paths, cross-references, and exact definition
identity. Existing definitions are reused only when canonical fields match and
the object remains enabled; reserved built-in IDs must resolve to their actual
built-ins. A user must review and apply a hint before Varkiv creates a launch
binding. Raw shell commands are never executed or promoted.

The package's reviewed runtime catalogue and selected games are committed
atomically. The schema cannot express catalogue identity, ID cross-references,
template-variable allowlists, or the “review before apply” trust boundary; the
application remains authoritative for those checks.

## Package manifest v3 (internal)

The package root may contain Varkiv's v3 ownership manifest,
`package-manifest.json`. Its current Go representation records
`format_version`, `generated_at`, the package `profile`, exported `files`,
legacy `managed_paths`, and integrity-bearing `managed_records`.

This is **not** a public import/export format. It is an internal write-set and
ownership journal used by the package builder to decide what a later build may
replace. In particular, files emitted in `reference` mode do not grant Varkiv
ownership merely because they appear in `files`; ownership comes from managed
paths/records created by Varkiv. An untracked target blocks overwrite, and a
managed target whose content drifted is a conflict. Builds create a recovery
snapshot before material changes.

Do not copy this manifest between servers, hand-edit it, or synthesize it as an
integration API. There is intentionally no public JSON Schema or compatibility
promise for v3. Older internal readers may exist solely for safe migration.
