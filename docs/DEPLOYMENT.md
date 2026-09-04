# 部署指南

推荐使用自动发布的容器镜像和 Docker Compose。服务是单进程 Go 应用，SQLite 必须位于本地可靠文件系统；ROM 资料库可以来自只读 NAS 共享。群晖与其他 NAS 的路径和文件系统细节见 [NAS_DEPLOYMENT.md](NAS_DEPLOYMENT.md)。

## 数据布局

```text
/library                 外部 ROM，只读
/data/
  library.db             SQLite
  managed/               明确复制进来的 ROM
  media/                 媒体原件与可重建缓存
  saves/                 SaveStream blobs
  packages/              整合包 release
  recovery/              包更新、维护和恢复快照
```

`/data` 必须持久化且可写。不要把 SQLite 放在 SMB/NFS/WebDAV；可把 `/library` 指向网络共享，但应由宿主先稳定挂载。详细所有权和恢复边界见 [STORAGE.md](STORAGE.md)。

## 使用发布镜像

镜像发布到 `ghcr.io/<owner>/<repository>`：

- `edge`：`main` 的最新成功 CI 提交，适合试用。
- `sha-<12位提交>`：同一 edge 构建的提交专属标签，便于回滚定位；严格不可变身份仍以 digest 为准。
- `<version>`：只有版本标签通过完整发布门禁后才生成。
- `image@sha256:<digest>`：最严格的固定方式；每个 GitHub Release 都附带已经写入 digest 的 Compose。

镜像清单同时包含 `linux/amd64` 和 `linux/arm64`，并附 SBOM、BuildKit provenance 和 GitHub artifact attestation。首次发布后，仓库维护者需要把 GHCR package 设置为公开；私有部署则先在 NAS 执行 `docker login ghcr.io`。

复制模板：

```bash
cp .env.ghcr.example .env
openssl rand -hex 32
```

编辑 `.env`：

```dotenv
VARKIV_IMAGE=ghcr.io/owner/repository:edge
ROM_LIBRARY_PATH=/absolute/path/to/roms
VARKIV_DATA_PATH=/absolute/path/to/varkiv-data
GAME_LIBRARY_TOKEN=replace-with-generated-secret
VARKIV_BIND=0.0.0.0
VARKIV_PORT=8080
```

使用版本 Release 附带的 Compose 时，镜像已经固定到 digest；仍需设置路径和 token。下面以仓库模板名为例，下载 Release 文件后把 `compose.ghcr.yaml` 换成实际文件名：

```bash
docker compose --env-file .env -f compose.ghcr.yaml pull
docker compose --env-file .env -f compose.ghcr.yaml up -d
docker compose --env-file .env -f compose.ghcr.yaml ps
docker compose --env-file .env -f compose.ghcr.yaml logs --tail=100 app
curl --fail http://127.0.0.1:8080/api/v1/health/ready
```

运行中的镜像身份可以用 `docker inspect` 查看；联网环境还可执行：

```bash
gh attestation verify \
  oci://ghcr.io/owner/repository@sha256:<digest> \
  -R owner/repository
```

## 从源码构建

```bash
cp .env.example .env
openssl rand -hex 32
```

编辑 `.env`，至少设置：

```dotenv
ROM_LIBRARY_PATH=/absolute/path/to/roms
GAME_LIBRARY_TOKEN=replace-with-generated-secret
VARKIV_BIND=0.0.0.0
VARKIV_PORT=8080
```

然后启动：

```bash
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 app
curl --fail http://127.0.0.1:8080/api/v1/health/live
curl --fail http://127.0.0.1:8080/api/v1/health/ready
```

源码 Compose 把 ROM 目录只读挂载到 `/library`，将 `/data` 保存到 `varkiv-data`。发布镜像 Compose 则把 `/data` 显式绑定到 `VARKIV_DATA_PATH`。两者都不会把路径缺失静默变成空目录。在局域网浏览器中输入管理 token；它只保存于当前标签页。

### 可选 Web Player

浏览器模拟资源不进入仓库或镜像。将已审查的 EmulatorJS `data/` 挂载为只读同源目录：

```bash
npm run --silent verify:web-emulator-assets -- --directory /absolute/path/to/data
docker compose -f compose.yaml -f compose.web-emulator.yaml up -d --build
```

挂载前必须通过固定资源、大小和 SHA-256 校验；缺失路径不能让 Compose 自动创建空目录。完整矩阵见 [WEB_EMULATION.md](WEB_EMULATION.md)。

### 实验性网页联机

网页联机使用独立的 EmulatorJS 4.3.0-pre 资源目录和一个只在 Compose 网络内暴露的信令 sidecar，不会替换稳定 Web Player：

```bash
npm run --silent fetch:web-netplay-assets -- --directory /absolute/new/path/to/netplay-data
EMULATORJS_NETPLAY_DATA_PATH=/absolute/path/to/netplay-data \
  docker compose -f compose.yaml -f compose.web-netplay.yaml up -d --build
curl --fail http://127.0.0.1:8080/api/v1/web-netplay/readiness
```

默认空 ICE 列表只适合本机或可直连网络；公网需要 HTTPS 和操作者自己的短时 TURN 凭据。版本、网络、安全和验收边界见 [WEB_NETPLAY.md](WEB_NETPLAY.md)。

## 首次验收

1. `/health/live` 与 `/health/ready` 均成功。
2. 使用 token 打开管理界面。
3. 平台预设可见，数据库 migration 与 build version 正确。
4. 对一个小目录执行导入预览；确认缺失 ROM 被跳过。
5. 提交后重启容器，确认 Game、Edition 和媒体关系仍存在。
6. 生成一个小型 Pegasus 或 ES-DE 包，检查目标目录只有计划内文件。

仓库自带的隔离容器门禁不会使用用户卷：

```bash
./scripts/acceptance-container.sh --port 18085
./scripts/acceptance-compose-nas.sh --port 18186
./scripts/acceptance-compose-nas.sh --port 18187 --published-compose
```

## 安全默认值

- 默认仅监听回环地址；非回环监听必须设置强随机 token。
- 对互联网开放时使用 HTTPS 反向代理或可信 VPN，不直接暴露明文 HTTP。
- `/library` 只读，容器不能获得 Docker socket、宿主根目录或设备节点。
- `.env`、私钥、数据库、日志、备份和 Agent 配置不进入 Git 或发布包。
- 反向代理应保留 request ID，并限制请求体、超时和并发；不要记录 Authorization。

## 更新

镜像部署先阅读发布说明并完成备份。使用版本标签或 digest 时，显式修改 `VARKIV_IMAGE` 或换用新 Release Compose；不要把固定部署静默切换到 `edge`：

```bash
docker compose --env-file .env -f compose.ghcr.yaml pull
docker compose --env-file .env -f compose.ghcr.yaml up -d
docker compose --env-file .env -f compose.ghcr.yaml ps
curl --fail http://127.0.0.1:8080/api/v1/health/ready
```

源码构建部署使用：

```bash
git pull --ff-only
docker compose up -d --build
```

迁移在服务启动时按版本顺序执行。不要手工修改 `schema_migrations`，也不要把新版本数据库直接交给旧版本二进制。

## 备份

### 完整状态备份

停服备份最容易获得单一时间点：

```bash
docker compose stop app
docker run --rm \
  -v varkiv-data:/data:ro \
  -v /absolute/path/to/backups:/backups \
  varkiv:0.1.0-preview.4 backup-state \
  --db /data/library.db \
  --state /data \
  --out /backups/varkiv-state
docker compose start app
```

如果部署方式不支持上面的 service command 语法，可直接使用固定镜像并挂载相同 `/data` 与备份目录。备份命令生成逐文件 SHA-256 清单，并交叉核对数据库引用。外部只读 `/library` 不进入状态包，仍需用户自己的 ROM 备份策略。

### SQLite 快照

只需要目录数据时，优先使用应用命令或 SQLite backup API 生成一致副本，不在服务运行时直接复制 WAL 文件。SQLite 快照不包含受管 ROM、媒体、存档、整合包或恢复区，因此不能作为完整灾难恢复。

## 恢复

始终恢复到新建空目录或新卷，验证后再切换服务；不要覆盖活动 `/data`：

```bash
docker run --rm \
  -v varkiv-restore:/restore \
  -v /absolute/path/to/backup:/backup:ro \
  varkiv:0.1.0-preview.4 restore-state \
  --from /backup/varkiv-state \
  --out /restore/recovered

docker run --rm \
  -v varkiv-restore:/data:ro \
  varkiv:0.1.0-preview.4 db-check \
  --db /data/recovered/library.db
```

恢复器先验证清单、大小、SHA-256、路径边界和数据库引用；目标存在任何文件时拒绝继续。确认新卷通过 health、数据库检查和抽样下载后，再在停服窗口切换挂载。

## 受管存储维护

设置页的清理流程只处理受管区：标记候选、签名预览、提交时锁内重扫，再把明确选择的孤立文件移入 `recovery`。它不删除外部 ROM，不跟随链接，也不自动永久清空隔离区。执行前仍应有完整状态备份。

## 不使用 Docker

```bash
./scripts/build-local.sh
./bin/varkiv version --json

GAME_LIBRARY_TOKEN='replace-with-a-long-random-secret' \
./bin/varkiv serve \
  --db /srv/varkiv/library.db \
  --state /srv/varkiv \
  --library /mnt/roms \
  --addr 0.0.0.0:8080
```

以非 root 专用用户运行，确保 state 可写、library 只读，并使用系统服务管理器设置重启策略、文件描述符限制和私有环境文件。

## Device Agent

Agent 与服务使用相同版本的二进制。先在 Web“同步”页创建短码并选择 DeviceProfile，再在目标设备执行页面生成的配对命令。所有 ROM、存档和模拟器根必须显式提供；Agent 不搜索主目录、注册表、PATH 或整块磁盘。

```bash
./bin/varkiv agent pair \
  --config ./private/agent.json \
  --server https://library.example.invalid \
  --code ABCDE-FGHIJ \
  --name my-handheld \
  --root ./device-data \
  --rom-root gba=./roms/gba \
  --path save_dir=./saves

./bin/varkiv agent probe --config ./private/agent.json
./bin/varkiv agent sync --config ./private/agent.json
./bin/varkiv agent run --config ./private/agent.json --interval 60s
./bin/varkiv agent status --config ./private/agent.json --json
```

复杂模拟器使用 `--driver-root DRIVER_ID=DIR` 逐项授权。Windows 托盘、SteamOS systemd user unit、Android APK 和掌机 Linux Hook 通过 `target-package` 生成；具体参数以 `./bin/varkiv target-package --help` 为准，软件与真机验收见 [ACCEPTANCE.md](ACCEPTANCE.md)。

## 故障排查

| 现象 | 检查 |
|---|---|
| readiness 失败 | 日志、数据库权限、迁移版本、state 可写性 |
| 无法看到 ROM | 宿主挂载是否存在、容器内 `/library` 是否只读可见、来源是否在授权根内 |
| SQLite locked/I/O error | 数据库是否位于网络文件系统、磁盘空间、异常并发进程 |
| 整合包拒绝构建 | plan 中的空间、路径、hardlink、未受管覆盖或合同漂移错误 |
| Web Player 无法启动 | 区分 readiness、资源加载、core、ROM 读取和实际画面；按平台查看会话错误 |
| Agent 不同步 | token 是否撤销、设备 target 是否漂移、显式根和运行证明是否完整 |

问题报告只附脱敏 request ID、版本和稳定错误码，不附 token、ROM/存档文件名、绝对路径、数据库或私有配置。
