# Quickstart

This guide starts Varkiv either from the released `ghcr.io/kocomic/varkiv` image or from a source checkout. Release deployment files pin the image by manifest digest rather than relying on a mutable tag.

Varkiv never needs write access to the external ROM directory. Use ROMs and BIOS files only when you have the right to do so, and keep an independent backup of them.

## Option A: local UI demo

Use this path to inspect the interface without adding personal files.

Prerequisites:

- Go 1.26.6.
- An available TCP port 8080 on the local machine.

From the repository root:

```bash
./scripts/demo.sh
```

In another terminal, verify readiness:

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:8080/api/v1/health/ready
```

Open <http://127.0.0.1:8080>. The demo imports only fictional, non-playable ROM bytes from `testdata/` and writes its database to the ignored `.demo/` directory. It does not read your ROM library. Stop it with `Ctrl+C` in the first terminal.

If port 8080 is already in use, select another loopback address explicitly:

```bash
VARKIV_DEMO_ADDR=127.0.0.1:18080 ./scripts/demo.sh
```

Verify and open that same address:

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:18080/api/v1/health/ready
```

Open <http://127.0.0.1:18080>.

## Option B: persistent library from a released image

Each tagged GitHub Release contains a Compose file with an immutable GHCR manifest digest, an environment template, the exact image reference, and `SHA256SUMS`. The release workflow rejects the release unless anonymous pulls of both `linux/amd64` and `linux/arm64` run the expected Varkiv version.

Choose a version from [Varkiv Releases](https://github.com/kocomic/varkiv/releases), omit the leading `v`, and download its deployment files:

```bash
version='<release version>'
base_url="https://github.com/kocomic/varkiv/releases/download/v${version}"
curl --fail --location --remote-name "$base_url/varkiv-${version}-compose.yaml"
curl --fail --location --remote-name "$base_url/varkiv-${version}-env.example"
curl --fail --location --remote-name "$base_url/varkiv-${version}-container-image.txt"
curl --fail --location --remote-name "$base_url/SHA256SUMS"
sha256sum --ignore-missing --check SHA256SUMS
cp "varkiv-${version}-env.example" .env
```

Open `.env` in a private local editor. Set `ROM_LIBRARY_PATH` to an existing absolute ROM directory, `VARKIV_DATA_PATH` to an existing private directory on a local ext4/btrfs/ZFS volume, and `GAME_LIBRARY_TOKEN` to the output of `openssl rand -hex 32`. Do not place SQLite state on SMB, NFS, WebDAV, SSHFS, or another network filesystem. Keep the ROM mount read-only.

Validate, pull, and start the exact release:

```bash
compose_file="varkiv-${version}-compose.yaml"
docker compose --env-file .env -f "$compose_file" config --quiet
docker compose --env-file .env -f "$compose_file" pull
docker compose --env-file .env -f "$compose_file" up -d
docker compose --env-file .env -f "$compose_file" ps
```

Verify readiness with `curl --fail http://127.0.0.1:8080/api/v1/health/ready`, then follow the import and backup guidance below. Upgrades use a new release's Compose and environment template only after a backup; do not replace the digest inside an existing release file.

## Option C: persistent library built from source

Use this path for a real personal library. It builds the current checkout locally and stores application state in the Docker named volume `varkiv-data`. The external ROM directory is mounted read-only.

### 1. Prerequisites

- Docker Engine with the `docker compose` command.
- `openssl` or another local cryptographically secure token generator.
- An existing, readable, absolute host directory for the ROM library.
- Enough local storage for the database, managed copies, media, saves, packages, and recovery data.

Keep SQLite application state on a local reliable filesystem. Do not place the `varkiv-data` volume on SMB, NFS, WebDAV, SSHFS, or another network filesystem. A host-mounted ROM directory may itself be backed by stable NAS storage because the container mounts it read-only.

### 2. Create the private environment

Copy the source-build template and generate a token:

```bash
cp .env.example .env
openssl rand -hex 32
```

Open `.env` in a local text editor. Replace the placeholders with:

```dotenv
ROM_LIBRARY_PATH=/absolute/path/to/your/rom-library
GAME_LIBRARY_TOKEN=paste-the-generated-64-character-token-here
VARKIV_BIND=127.0.0.1
VARKIV_PORT=8080
TZ=Asia/Shanghai
```

`ROM_LIBRARY_PATH` must already exist and must be absolute. Do not put `.env`, its token, personal paths, database files, or logs into Git, screenshots, bug reports, or public paste services.

Binding to `127.0.0.1` is the safest first run. For LAN access, change `VARKIV_BIND` to `0.0.0.0` only after setting a strong token, then keep Varkiv behind a trusted VPN or HTTPS reverse proxy. Do not expose plain HTTP directly to the internet.

### 3. Validate and start

Validate the resolved Compose configuration before building:

```bash
docker compose --env-file .env config --quiet
docker compose --env-file .env up -d --build
docker compose --env-file .env ps
```

Wait for the service to become healthy, then verify its readiness endpoint:

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:8080/api/v1/health/ready
```

If readiness fails, inspect only the recent application output and redact private paths before sharing it:

```bash
docker compose --env-file .env logs --tail=100 app
```

Open <http://127.0.0.1:8080> and enter the value of `GAME_LIBRARY_TOKEN` when prompted. The browser keeps the management token in the current tab, not in the catalog database.

### 4. Import a small first platform

Start with a small directory containing real ROM files you are permitted to use. If the host directory contains `gba/`, that directory appears inside the container as `/library/gba`.

In the web interface:

1. Open **Import sources**.
2. Choose **Scan ROM files or folders**.
3. Select the matching platform.
4. Enter the library-relative file or directory, such as `gba` or `gba/example.gba`. Do not enter the host's absolute path.
5. Select **Scan ROMs & preview**, then review importable, missing, duplicate, and conflicting entries.
6. Select **Commit selected items** to import only the reviewed entries.

Varkiv fingerprints files before creating editions. Missing ROMs are skipped rather than turned into empty catalog entries. A committed reference import does not move, rename, or delete source files.

### 5. Stop, resume, and preserve data

Stop the application while retaining the container and all data:

```bash
docker compose --env-file .env stop
```

Resume it later:

```bash
docker compose --env-file .env start
```

Remove the application container and network while retaining the named data volume:

```bash
docker compose --env-file .env down
```

Do **not** add `--volumes` or `-v` to `docker compose down` unless you intentionally want to delete the `varkiv-data` volume. Removing that volume destroys the database and all managed state stored in it. It does not delete the external read-only ROM directory, but it can delete catalog metadata, managed ROM copies, media, saves, packages, and recovery data.

Before upgrades or storage changes, follow the complete [backup and recovery procedure](DEPLOYMENT.md). For a NAS or Synology installation with explicit host paths, preflight checks, and restore drills, continue with [NAS deployment](NAS_DEPLOYMENT.md).

## Next steps

- Learn the catalog and import rules in the [product baseline](PRODUCT.md).
- Review authentication and automation in the [API guide](API.md).
- Configure optional browser play only after reading [Web emulation](WEB_EMULATION.md).
- Pair a Device Agent and validate target-specific behavior using [Deployment](DEPLOYMENT.md) and [Acceptance](ACCEPTANCE.md).
