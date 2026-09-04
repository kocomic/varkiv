[**English**](README.md) | [简体中文](README.zh-CN.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md)

![Varkiv](internal/server/web/assets/varkiv-logo.svg)

# Varkiv

A lightweight, self-hosted game library and device hub for personal ROM collections. Varkiv turns ROM folders, Pegasus metadata, ES-DE metadata, and manual curation into one maintainable catalog, then prepares device packages and coordinates save synchronization.

> Varkiv is preview software. Back up your library state before upgrading, and keep an independent backup of your ROM collection.

## Why Varkiv

- Model a series, a platform-specific game, each original/translated/modded edition, and its actual files separately with `Series → Game → Edition → Artifact`.
- Import real ROMs without mandatory scraping. Missing files are skipped because they cannot be fingerprinted; Varkiv does not create unverifiable catalog entries.
- Review a signed import preview before committing. The source, order, and SHA-256 fingerprints are revalidated, and a failed batch writes nothing.
- Import Pegasus and ES-DE libraries, or export device-ready packages with frontend metadata, media, emulator drivers, RetroArch cores, launch arguments, and configuration templates.
- Keep external ROM folders read-only or explicitly copy files into managed storage. Media is content-addressed, and sources are never moved, renamed, or deleted implicitly.
- Run a single Go service with SQLite and filesystem storage on a personal NAS; connect Windows handhelds, SteamOS/Bazzite, Android, and selected handheld Linux systems through explicit device profiles.

## Quick start

With Go 1.26.6 installed, run the local UI demo from the repository root:

```bash
./scripts/demo.sh
```

Open <http://127.0.0.1:8080>. The demo uses fictional, non-playable ROM bytes. Runtime data is written to the ignored `.demo/` directory, and the local build writes its binary to the ignored `bin/varkiv` path. Stop it with `Ctrl+C`.

For a persistent library built from source with Docker Compose, follow the copy-and-verify [Quickstart](docs/QUICKSTART.md). Published-container installation will be documented with real image coordinates after the first image release; placeholder registry paths are not a supported install method.

## Product boundaries

- Varkiv is a private personal library, not a multi-user media server.
- Save data belongs to an edition, platform, or compatible save container and is designed for automatic Device Agent synchronization rather than routine browser uploads.
- Browser play is optional and requires separately supplied, verified EmulatorJS assets. A page loading successfully is not proof that a game is running.
- Platforms such as PS2 and Nintendo 3DS remain native-emulator targets when no supported browser runtime exists.
- Experimental browser netplay is isolated from native RetroArch, PPSSPP, and other emulator networking protocols.

## Documentation

| Start here | Purpose |
|---|---|
| [Quickstart](docs/QUICKSTART.md) | Run the demo or create a persistent source-built library safely |
| [Product baseline](docs/PRODUCT.md) | Domain model, user journeys, and non-negotiable behavior |
| [API guide](docs/API.md) | Authentication, errors, pagination, workflows, and OpenAPI entry points |
| [Protocol index](docs/PROTOCOLS.md) | HashPack, portable manifests, launch sidecars, and device-sync contracts |
| [Deployment](docs/DEPLOYMENT.md) and [NAS deployment](docs/NAS_DEPLOYMENT.md) | Operations, updates, backup, recovery, and Synology guidance |
| [Documentation index](docs/README.md) | Storage, database, platforms, web play, netplay, acceptance, and release gates |

## Development

Build and test instructions are in [CONTRIBUTING.md](CONTRIBUTING.md). Browser, container, Android, target-package, and hardware evidence have different acceptance levels; see [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) before claiming runtime support.

Build a local development binary and inspect its machine-readable identity with:

```bash
./scripts/build-local.sh
./bin/varkiv version --json
```

## License and privacy

Varkiv is licensed under [Apache-2.0](LICENSE). The repository contains no commercial ROMs, BIOS files, firmware, private keys, or EmulatorJS runtime assets. See the [privacy boundary](docs/PRIVACY.md), [third-party notices](docs/THIRD_PARTY_NOTICES.md), and [security policy](SECURITY.md).
