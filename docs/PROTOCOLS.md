# Varkiv public protocols and portable formats

This page is the compatibility index for software that exchanges data with
Varkiv. The named format version, the matching JSON Schema, and the normative
rules in the linked document form each public contract. Go structs and example
files are implementation evidence, not a substitute for the contract.

## Stability classes

| Contract | Current version | Stability | Schema or API description |
| --- | ---: | --- | --- |
| [HashPack](HASHPACK.md) | 1 | Public, portable | [`hashpack-manifest-v1.schema.json`](../schemas/hashpack-manifest-v1.schema.json), [`hashpack-record-v1.schema.json`](../schemas/hashpack-record-v1.schema.json) |
| [Library manifest](PORTABLE_FORMATS.md#library-manifest-v6) | 6 | Public, portable | [`library-manifest-v6.schema.json`](../schemas/library-manifest-v6.schema.json) |
| [Launch sidecar](PORTABLE_FORMATS.md#launch-sidecar-v2) | 2 | Public, portable and inert | [`varkiv-launches-v2.schema.json`](../schemas/varkiv-launches-v2.schema.json) |
| [Device sync](DEVICE_SYNC_PROTOCOL.md) | HTTP API v1 | Public API contract | [`openapi.yaml`](../internal/server/openapi.yaml) plus the state and security rules in the protocol document |
| Package ownership manifest | 3 | Internal, managed state | No public schema; see [package manifest v3](PORTABLE_FORMATS.md#package-manifest-v3-internal) |

“Public” means a compatible implementation may produce or consume the format
without depending on Varkiv's database. It does not mean every semantic rule is
expressible in JSON Schema. Cross-file hashes, filesystem containment,
catalogue identity, idempotency, authorization, and atomicity are checked by the
application and are documented alongside the schemas.

## Compatibility policy

- Writers emit only the current version shown above.
- Readers reject an unknown major format version unless a contract explicitly
  says otherwise. Varkiv currently retains library-manifest v4/v5 and launch
  sidecar v1 readers for migration, but new integrations must emit v6 and v2.
- Optional fields may be omitted. A consumer must not invent a security- or
  identity-relevant default unless the format document defines it.
- A change that removes or reinterprets a field, changes identity calculation,
  or weakens a validation boundary requires a new format version.
- A schema validating a document is necessary but not sufficient acceptance.
  Producers must also satisfy the normative semantic rules in the linked
  specification.

## Media types and filenames

| Contract | Canonical transport |
| --- | --- |
| HashPack v1 | `application/vnd.varkiv.hashpack+zip` |
| Library manifest v6 | UTF-8 JSON named `library-manifest.json` |
| Launch sidecar v2 | UTF-8 JSON named `varkiv-launches.json` |
| Device sync | HTTPS JSON or multipart HTTP under `/api/v1` |

JSON timestamps use RFC 3339. SHA-256, MD5, and CRC-32 values are lowercase
hexadecimal where their schemas say so. Portable paths always use `/`, never an
absolute path, and are resolved only beneath an explicitly configured root.

## Schema use

The schemas use JSON Schema Draft 2020-12 and carry stable `urn:varkiv:schema:*`
identifiers. A typical validation command is:

```sh
check-jsonschema \
  --schemafile schemas/library-manifest-v6.schema.json \
  export/library-manifest.json
```

Repository maintainers can validate every schema plus the canonical HashPack
examples and existing v6/v2 export fixtures with the pinned AJV CLI check:

```sh
./scripts/check-protocol-schemas.sh
```

For HashPack, validate `manifest.json` and each decoded line of
`records.ndjson` against their separate schemas, then verify the archive and
digest rules in [HASHPACK.md](HASHPACK.md). For the launch sidecar, validation
does not make imported launch arguments trusted or executable; the review/apply
boundary remains mandatory.
