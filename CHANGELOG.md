# Changelog

Varkiv follows [Semantic Versioning](https://semver.org/). Only changes that affect users, deployment, public APIs, stored data, or supported clients belong here; implementation notes and one-off test evidence remain in commits and release artifacts.

## 0.1.0-preview.5 - 2026-09-05

- Made release acceptance checks independent of optional runner tooling so forbidden-pattern and reproducibility privacy checks cannot pass because a search command is missing.

## 0.1.0-preview.4 - 2026-09-05

- Isolated anonymous `linux/amd64` and `linux/arm64` release proofs in separate runners, preventing Docker manifest-list digest reuse from masking a valid multi-architecture image.
- Gated GitHub Release publication on both anonymous architecture proofs and verified, checksum-protected release assets.

## 0.1.0-preview.3 - 2026-09-05

- Extended the bounded anonymous GHCR verification window and added a default-policy regression test after real registry propagation exceeded the earlier one-minute gate.

## 0.1.0-preview.2 - 2026-09-05

Initial public source preview.

### Included

- Self-hosted personal game library based on Series, Game, Edition, and Artifact, including localized titles and grouped original, translated, and modified ROM editions.
- Preview-first imports from direct ROM folders, Pegasus, ES-DE, and Varkiv manifests; missing ROMs are skipped and batch commits are drift-protected and atomic.
- Content-addressed ROM and media storage, shareable privacy-clean `.hashpack` identity libraries, and export profiles for multiple frontends, devices, emulator drivers, RetroArch cores, and launch templates.
- Edition-, platform-, and container-scoped save streams with append-only revisions, conflict preservation, device pairing, a Go agent, a Windows tray client, and an Android client.
- Responsive four-language web interface, verified browser-emulation capability gates, controller guidance, and an experimental two-browser NES WebRTC loop.
- SQLite migrations, OpenAPI 3.1 contracts, Docker Compose and Synology deployment templates, backup and staged recovery commands, and CI/release workflows for multi-architecture images and provenance.
- English, Simplified Chinese, Traditional Chinese, and Japanese project introductions; a safe source-build Quickstart; public portable-format and device-sync specifications; strict HashPack archive/NDJSON framing; and reproducible offline API reference generation.

### Preview boundaries

- This is not a stable API or data-format release. Read the migration notes and make a full state backup before updating.
- Browser emulation is enabled only for platform/runtime combinations that pass their explicit gate; native-only platforms are not presented as web-playable.
- Software and emulator tests do not replace real NAS, handheld, controller, network, trademark, or contribution-rights acceptance. See [release gates](docs/RELEASE_READINESS.md).
