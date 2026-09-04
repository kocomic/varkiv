# Device sync protocol

This document defines the state, authorization, conflict, and privacy rules
around the device endpoints in Varkiv's `/api/v1` HTTP API. Exact routes,
request fields, responses, and error schemas are defined by
[`openapi.yaml`](../internal/server/openapi.yaml). Both documents are normative:
OpenAPI describes wire shape; this document describes behavior that a schema
cannot express.

Production deployments MUST use HTTPS. Plain HTTP is acceptable only on
loopback or after an explicit, informed local-network override. Clients MUST
reject unexpected redirects and MUST NOT forward a bearer token to a different
origin.

## State machine

```text
unpaired
  -> pairing code created by an administrator
  -> code redeemed once; device token issued once
  -> paired
  -> heartbeat and complete runtime attestation snapshot
  -> sync configuration read
  -> local inventory/save state collected
  -> session negotiated
  -> each operation: upload | download | noop | conflict
  -> transferred content verified
  -> operation acknowledged
  -> paired (repeat)

paired -> revoked (administrator action; token immediately unusable)
pairing code -> expired | redeemed (terminal)
session -> complete | partial | aborted | failed (terminal)
```

Pairing and device authorization do not grant filesystem access by themselves.
Device agents resolve approved save bindings locally and send only the bounded
portable payload required by an operation.

## Pairing and authentication

An authenticated administrator creates a pairing code for an enabled device
profile. Code TTL is 120 through 1,800 seconds; the default is 600 seconds. The
plain code is displayed once and only its digest is stored. Redemption is
anonymous, single-use, and must occur before expiry. A legacy device-profile
assertion supplied during redemption must match the code's profile.

Successful redemption returns one 32-byte random device token encoded as 64
hexadecimal characters. It is shown once; Varkiv stores only its digest. The
token expires after 90 days unless revoked earlier and carries the scopes
`sync:read`, `sync:write`, and `device:heartbeat`. Revoking a device revokes its
token in the same operation.

Send the token as `Authorization: Bearer <token>`. Never place pairing codes or
tokens in URLs, filenames, analytics, or logs. Administrative bearer tokens and
device tokens are different authorities:

- device sync reads require `sync:read`; writes require `sync:write`;
- a device may heartbeat only its own device ID and needs
  `device:heartbeat`;
- inventory-match review/commit and legacy global manifest operations remain
  administrator-only;
- direct save-collection, archive, and diagnostic-upload routes remain owner or
  administrator operations, not general device-token capabilities;
- a device may read only the save-revision metadata explicitly exposed to it by
  an authorized operation.

## Heartbeat, attestation, and configuration

A heartbeat accepts only allowlisted capabilities. Runtime attestations form a
complete current snapshot, not an additive patch, and are limited to 128 items.
Omitted previous attestations are revoked atomically. Each attestation binds
runtime kind and ID, contract version, SHA-256, positive size (at most 512 MiB),
and observation time; unknown or mismatched platform identity is rejected.

`GET /sync/config` returns only the paired device/profile, authorized bindings,
drivers, cores, runtime requirements, launches, and platforms needed by that
device. Streams whose compatibility depends on exact runtime binaries stay
locked until the required driver/core attestations match contract version,
hash, size, OS family, and architecture.

## Session creation and idempotency

Session creation requires `Idempotency-Key`: a stable, unpredictable value of
8 through 128 characters with no NUL, CR, or LF. The server binds the key to the
authenticated device and a normalized request fingerprint.

- Retrying the same device/key with the same request returns the original
  result and `Idempotent-Replayed: true`.
- Reusing the key with a different request returns `409 Conflict`.
- A paired token's device identity is authoritative over any legacy body field.

A session request contains at most 10,000 inventory items and 4,096 save-stream
states. `client_item_id` and `stream_id` values must be unique within their
respective lists. Inventory identity is matched by SHA-256 first, then by
structured identifiers such as serial, product code, or title ID. Ambiguous
matches require an administrator's signed preview/commit flow; Varkiv does not
return another user's local paths or filenames as matching evidence.

## Negotiation and conflicts

For each save stream, the server chooses exactly one action:

| Server revision | Client state | Required action |
| --- | --- | --- |
| absent | local content exists | `upload` |
| absent | no local content | `noop` |
| present | no local content | `download` |
| present | local hash equals current hash | `noop` |
| server advanced and local equals the client's known base | `download` |
| client's base is current and local differs | `upload` |
| any other divergence | `conflict` |

There is no last-writer-wins or modification-time winner. Revisions are
append-only and may be `current`, `superseded`, `conflict`, or `quarantined`.
If the current server revision advances after negotiation, a subsequent upload
MUST fail as a conflict; it MUST NOT overwrite the newer revision.

## Upload

An upload is multipart and limited to 256 MiB overall. The `manifest` part MUST
precede file parts, is limited to 1 MiB, and declares the edition (when
required) plus 1 through 4,096 logical files. File parts correspond one-for-one
with manifest entries. Actual content is limited to 240 MiB.

A logical path is UTF-8, at most 1,024 bytes, uses `/`, and is relative. It
MUST NOT contain an empty or dot segment, a segment over 255 bytes, controls,
`<>:\"|?*`, a trailing dot/space, or a Windows-reserved name (`CON`, `PRN`,
`AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`). Logical paths are unique.

Each file is SHA-256 checked. The revision content hash is SHA-256 of records
sorted by logical path, each encoded as:

```text
logical_path NUL file_sha256 NUL decimal_size NUL
```

The computed content hash must equal the value negotiated for the operation.
Varkiv stages every file, verifies the complete set, and only then publishes an
immutable revision.

## Download, verification, and acknowledgement

The negotiated operation names the immutable target revision and its files.
File responses include content length and an ETag derived from integrity data.
A client stages files on the destination volume, verifies byte counts, SHA-256,
and the complete content hash, backs up the previous local set, and atomically
replaces it. Only after successful replacement does it acknowledge the
operation with the actual content hash. A failed verification or replacement
must leave the previous local state recoverable and must not acknowledge.

## Limits and error behavior

Standard `application/json` endpoint request bodies are limited to 1 MiB,
reject unknown fields, and contain exactly one JSON value. The multipart upload
manifest follows its separate limits and validation rules above. Clients and
servers use bounded response/body readers. API errors use stable machine codes;
messages must not include bearer tokens, absolute paths, Android SAF URIs, or
uploaded content.

Expired sessions, invalid operation ownership, a changed negotiated base,
hash/size mismatch, duplicate logical path, unsafe path, and idempotency-key
reuse are hard failures. Clients may safely retry only when the endpoint's
OpenAPI contract declares idempotency and the same request key and bytes are
preserved.

## Privacy boundary

`client_item_id` is an opaque per-client correlation value, not a ROM path or
filename. ROM names and paths, local save paths, SAF document URIs, emulator
configuration directories, tokens, and diagnostic secrets stay on the device.
The server receives content hashes, bounded identity fields, approved logical
save paths, runtime attestations, and the save bytes required for an authorized
operation. Operators should apply normal backup encryption, access control,
retention, and log-redaction policies to that data.
