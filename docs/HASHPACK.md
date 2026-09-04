# HashPack v1

HashPack is Varkiv's privacy-minimized, shareable ROM identity data format. It
lets people exchange hashes and descriptive grouping hints without exporting
ROM bytes or a personal library backup.

Normative words **MUST**, **MUST NOT**, **SHOULD**, and **MAY** are used in the
RFC 2119 sense.

## Privacy boundary

A pack contains public dataset metadata and content identities only. It MUST
NOT contain ROM bytes, local or network paths, original filenames, saves,
media, device identities, account data, play history, or import-source
credentials. `game_key` is a source-scoped grouping hint, not a user-library
identifier. A record's SHA-256 is the ROM/content identity.

Publishers are responsible for having the right to distribute the metadata.
`source.license` is a required dataset-license declaration; Varkiv checks its
shape, not its legal validity. An SPDX identifier is recommended where one
exists.

## Container

A v1 pack is a ZIP file with media type
`application/vnd.varkiv.hashpack+zip`. It MUST contain exactly two regular file
entries at the archive root, with no duplicates or directory entries:

```text
manifest.json
records.ndjson
```

The compressed archive is limited to 64 MiB. `manifest.json` is limited to
1 MiB. The uncompressed `records.ndjson` payload is limited to 128 MiB and
1,000,000 records. Both JSON forms reject unknown fields.

`manifest.json` contains exactly one JSON value; trailing whitespace is allowed,
but a second value or non-whitespace suffix is invalid. `records.ndjson` is
UTF-8 newline-delimited JSON. Each record is one non-empty compact JSON value
followed by `\n`; blank lines, insignificant whitespace outside strings, and an
unterminated final line are invalid. Object member order has no semantic
meaning to a decoded record, but changing that order changes the exact bytes
and therefore `records_sha256` and `pack_id`. The exact byte sequence, including
every newline, is hashed for `records_sha256`.

## Manifest

The manifest conforms to
[`hashpack-manifest-v1.schema.json`](../schemas/hashpack-manifest-v1.schema.json).
The repository also includes a schema-valid, semantically consistent
[`manifest example`](../schemas/examples/hashpack-manifest-v1.json) paired with
one [`record example`](../schemas/examples/hashpack-record-v1.json); neither
contains ROM bytes. The example record's compact representation plus its final
newline is the payload used by the example manifest's `records_sha256`.
It contains:

- `format_version`: integer `1`.
- `pack_id`: canonical semantic identity described below.
- `source`: stable source ID, display name, optional publisher, and required
  dataset license.
- `release`: a non-empty publisher-controlled release label, at most 128 UTF-8
  bytes in the reference implementation and with no control characters.
- `created_at`: non-zero RFC 3339 timestamp. It records packaging time but does
  not participate in semantic identity.
- `record_count`: 1 through 1,000,000 and exactly equal to decoded record count.
- `records_sha256`: SHA-256 of the exact `records.ndjson` bytes.

`source.id` is a lowercase portable identifier matching
`^[a-z0-9][a-z0-9._-]{0,127}$`. Source strings are trimmed; name and license are
required, and name, publisher, and license have a 200-byte reference limit and
must contain no control characters.

JSON Schema `maxLength` counts Unicode code points, while the Go reference
implementation applies the release and source limits to UTF-8 bytes. The
schema is therefore a shape-level approximation for non-ASCII text; importers
MUST still enforce the byte limits above.

## Records

Each line conforms to
[`hashpack-record-v1.schema.json`](../schemas/hashpack-record-v1.schema.json).
Important semantics not fully expressible in that schema are:

- `sha256` MUST be unique within the pack. `size` MUST be positive.
- Hash strings are lowercase hexadecimal. `parent_sha256`, when present,
  identifies a related parent content object; it is not a substitute for the
  record's own identity.
- `platform`, `game_key`, `game_default_title`, and
  `edition_default_title` are required and non-empty after trimming.
- `game_titles` and `edition_titles` map locale tags to non-empty localized
  labels. Empty keys or values are removed by the reference writer.
- `edition_type` defaults to `original` when absent at the Go API boundary, but
  canonical serialized records include it.
- `role` defaults to `rom` at the Go API boundary and canonical records include
  one of `rom`, `disc`, or `executable`.
- `disc_index` is from 0 to 64; zero means unspecified or the first/default
  artifact according to the source dataset.
- `languages` contains unique, non-empty strings. The canonical writer sorts
  them lexicographically.

## Canonical identity

Let `NUL` be the single byte `0x00`. After source and release normalization,
`pack_id` is lowercase hexadecimal SHA-256 over the UTF-8 bytes of these fields
joined by `NUL`, in this exact order:

```text
varkiv-hashpack-v1
source.id
source.name
source.publisher
source.license
release
records_sha256
```

Equivalently, there is one `NUL` separator between adjacent fields and no
separator after the final field. `created_at`, JSON whitespace, ZIP compression,
entry timestamps, and entry order do not participate. The SHA-256 of the full
ZIP is a separate transport digest and is not `pack_id`.

## Import and replay rules

An importer MUST validate archive shape and size, decode both entries, validate
each record, reject duplicate record hashes, verify `record_count`, verify
`records_sha256`, and recompute `pack_id` before showing an import preview.

Varkiv binds a preview token to the exact uploaded pack bytes. Commit requires
the exact pack again, preventing a preview/commit substitution. Re-importing the
same semantic `pack_id` is idempotent. A source/release pair already associated
with a different `pack_id` is a conflict and MUST NOT be overwritten silently.

HashPack imports update the shared reference catalogue only. They do not create
a playable library entry: Varkiv still requires a locally available ROM whose
bytes can be hashed and matched.
