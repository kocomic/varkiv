# NAS 部署与验收

这套部署用于支持 Docker Compose 的 x86_64 或 ARM64 NAS。它把原始 ROM 目录只读挂载到容器，把数据库、受管 ROM、媒体、存档、整合包和恢复快照写入一个明确的宿主目录；备份与恢复演练使用另外两个目录。Compose 不会在路径拼错时替操作者创建空目录。

## 1. 规划目录

建议把四棵目录彼此分开：

```text
/volume1/games                              原始 ROM，只读
/volume1/docker/varkiv/data               活动数据库和受管状态
/volume1/backups/varkiv                   完整状态备份
/volume1/docker/varkiv/restore-drills     恢复演练输出
```

`data` 必须位于 NAS 本机的 ext4、btrfs 或 ZFS 文件系统。不要把 `library.db` 放在 SMB、CIFS、NFS、SSHFS 或其他网络文件系统上；外部 ROM 目录可以是只读网络挂载。备份目录不能位于 `data` 内，恢复演练目录也不能与活动数据或备份相互嵌套。

容器进程固定使用 UID/GID `10001:10001`。先创建空的私有写入目录；下面的路径只是示例，必须换成这台 NAS 的真实路径：

```bash
sudo mkdir -p \
  /volume1/docker/varkiv/data \
  /volume1/backups/varkiv \
  /volume1/docker/varkiv/restore-drills
sudo chown -R 10001:10001 \
  /volume1/docker/varkiv/data \
  /volume1/backups/varkiv \
  /volume1/docker/varkiv/restore-drills
sudo chmod 700 \
  /volume1/docker/varkiv/data \
  /volume1/backups/varkiv \
  /volume1/docker/varkiv/restore-drills
```

不要对 ROM 目录执行递归 `chown` 或 `chmod`。Varkiv 只要求容器用户能读取和遍历它。

## 2. 创建私有环境文件

```bash
cp .env.nas.example .env.nas
chmod 600 .env.nas
openssl rand -hex 32
```

编辑 `.env.nas`，填入上面的四个绝对路径，并把生成的 64 位十六进制值写入 `GAME_LIBRARY_TOKEN`。`.env.nas` 已被 Git 忽略，不能提交、截图或发送到公共日志。

将 `VARKIV_IMAGE` 设置为项目 GHCR 镜像。测试 `main` 可使用 `:edge`；长期部署优先使用版本 Release 提供的 digest。不要把未知来源镜像用于包含私人游戏资料的部署。

## 3. 拉取、预检和启动

推荐使用仓库中的单文件 `compose.ghcr.yaml`，它没有 `build:`，适合普通 Docker Compose 和 NAS 项目界面：

```bash
docker compose --env-file .env.nas -f compose.ghcr.yaml pull
./scripts/nas-preflight.sh --env-file .env.nas --require-image-access
docker compose --env-file .env.nas -f compose.ghcr.yaml up -d
docker compose --env-file .env.nas -f compose.ghcr.yaml ps
```

预检脚本还需要备份和恢复演练目录，因此 `.env.nas` 保留四棵目录配置。它通过解析 `VARKIV_IMAGE` 检查镜像内的非 root 用户能否读取 ROM 并写入三个私有目录。

### 从源码构建

先做不写入目录的配置预检：

```bash
./scripts/nas-preflight.sh --env-file .env.nas
```

它会拒绝缺失/相对/符号链接路径、弱令牌、相互嵌套的数据树、已识别的网络数据库文件系统和无效 Compose 配置。若系统没有 `findmnt`，会输出 `database_filesystem_not_verified` 警告；此时必须通过 NAS 的存储管理界面确认 `VARKIV_DATA_PATH` 位于本机卷后再继续。

从源码构建，随后用镜像内的非 root 用户检查四个挂载点的实际权限：

```bash
docker compose --env-file .env.nas \
  -f compose.yaml -f compose.nas.yaml build --pull
./scripts/nas-preflight.sh --env-file .env.nas --require-image-access
```

启动：

```bash
docker compose --env-file .env.nas \
  -f compose.yaml -f compose.nas.yaml up -d
docker compose --env-file .env.nas \
  -f compose.yaml -f compose.nas.yaml ps
```

访问 `http://NAS地址:8080`，输入 `.env.nas` 中的令牌。不要把 8080 直接暴露到互联网；远程访问应放在可信 VPN 后，或由 HTTPS 反向代理提供边界。

### Synology DSM Container Manager

版本 Release 直接提供 `varkiv-<version>-compose.yaml`，其中镜像已经固定到发布 digest；在 Container Manager 的“项目”界面上传它并填写私有环境变量即可，不需要在 NAS 编译 Go 或构建镜像。`main` 的每次成功 CI 还会产生一个保留 30 天的 `varkiv-edge-deployment-*` Actions 产物，其中包含 digest Compose、环境模板、镜像身份和 SHA-256 清单。

GHCR package 公开时 DSM 可匿名拉取；若仓库维护者没有把 package 设为公开，需要先在 NAS 的 Docker 环境登录 GHCR。不要把 registry token 写进 Compose 或可分享的环境模板。

源码部署仍提供与 `compose.yaml + compose.nas.yaml` 经过自动等价性检查的单文件 [compose.synology.yaml](../compose.synology.yaml)。源码包可在干净提交上生成：

```bash
./scripts/build-nas-deployment-bundle.sh
```

输出位于 `dist/`，只包含当前 Git 提交的已审查源码、合成测试夹具、精确的 `docker-compose.yml` 副本和不含令牌的环境模板；命令会给出提交、字节数和 SHA-256，拒绝覆盖既有包，也拒绝从带未提交修改的工作树构建。它不包含 `.env.nas`、用户 ROM、媒体、存档、数据库或浏览器数据。

在 File Station 中把源码包上传到新建的私有工作目录并解压。在 Container Manager → 项目中选择该目录，上传根目录的 `docker-compose.yml`，项目名使用 `varkiv`。源码项目需要先完成“构建”再启动；镜像版 Compose 只需要拉取和启动。不要使用“清理”或“删除”，因为这两项会移除项目资源。首次部署前仍要创建目录并填写私有环境变量；如果 DSM 项目界面不能安全注入环境变量，则应通过受控终端部署，而不是把真实令牌写进可分享的 Compose 文件。

## 4. 首次验收

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:8080/api/v1/health/ready
docker compose --env-file .env.nas \
  -f compose.ghcr.yaml exec -T app \
  varkiv db-check --db /data/library.db
```

然后完成三个手工检查：

1. 不带令牌访问 `/api/v1/games` 返回 `401`，带 Bearer Token 返回 `200`。
2. 在界面建立一个测试条目，执行 `docker compose ... restart app` 后仍存在。
3. 在 NAS 文件管理器中确认 `data/library.db` 已出现，原始 ROM 目录没有新增或修改文件。

仓库的自动门禁使用独立临时目录复验同一合同，不读取 NAS 或真实 ROM：

```bash
./scripts/acceptance-compose-nas.sh
./scripts/acceptance-compose-nas.sh --published-compose --port 18187
```

两次运行分别检查源码 Compose 与发布镜像 Compose 的 host bind 投影、ROM 只读、非 root 和只读根、鉴权、写入后重启持久化、完整备份、恢复演练，以及恢复过程未改变活动数据库；退出时只清理本次生成的精确临时资源。

## 5. 备份

首次部署完成后立即创建一个完整状态备份：

```bash
./scripts/nas-backup.sh --env-file .env.nas
```

脚本先验证配置和镜像权限，只停止这个 Compose 项目的 `app` 服务，把活动 `/data` 重新挂载为只读并创建不可覆盖的完整备份，运行 `check-state` 后再启动原服务。若中途失败，退出钩子仍会尝试恢复原服务。输出只包含安全的备份目录名和状态，不包含令牌或宿主路径。

可为维护窗口指定一个明确名称：

```bash
./scripts/nas-backup.sh \
  --env-file .env.nas \
  --name before-preview-93-update
```

备份含数据库、受管 ROM/媒体、存档和整合包恢复信息，属于私人数据。外部只读 ROM 不在其中，必须继续沿用 NAS 自身的快照/备份策略。

## 6. 恢复演练与回滚边界

恢复演练永远写入一个不存在的新目录，不覆盖活动数据：

```bash
./scripts/nas-restore-drill.sh \
  --env-file .env.nas \
  --backup before-preview-93-update
```

脚本先校验备份，把活动 `/data` 强制只读，然后恢复到 `VARKIV_RESTORE_PATH/restore-before-preview-93-update` 并对恢复后的数据库执行只读完整性检查。活动服务无需停止，恢复目录不会被自动切换为正式数据。

真正回滚前必须停止应用并保留当前 `VARKIV_DATA_PATH`，不能把恢复结果复制覆盖到旧目录。推荐为已校验的恢复目录创建一份新的、仍被 Git 忽略的环境文件和 Compose override，令应用显式使用恢复结果中的 `library.db` 与 `state/`；先换一个端口验收，再由操作者切换反向代理。当前版本故意不提供“覆盖原数据”命令。

## 7. 网页模拟器可选层

网页模拟器资源默认关闭。先准备经过固定哈希校验的 EmulatorJS 4.2.3 `data/` 目录，在 `.env.nas` 设置 `EMULATORJS_DATA_PATH`，然后在每一条 Compose 和运维命令中加入 `-f compose.web-emulator.yaml`，预检和运维脚本加入 `--with-web-emulator`。

```bash
./scripts/nas-preflight.sh \
  --env-file .env.nas --with-web-emulator --require-image-access
docker compose --env-file .env.nas \
  -f compose.ghcr.yaml -f compose.web-emulator.yaml up -d
```

PS2、3DS 等 native-only 平台仍由设备上的模拟器运行，不会因为部署了网页资源而被错误标记为浏览器可运行。

## 8. 更新

每次更新先运行 `nas-backup.sh` 和 `nas-restore-drill.sh`。镜像部署修改 `.env.nas` 中的版本/digest 后执行：

```bash
docker compose --env-file .env.nas -f compose.ghcr.yaml pull
docker compose --env-file .env.nas -f compose.ghcr.yaml up -d
```

源码部署则重新构建：

```bash
docker compose --env-file .env.nas \
  -f compose.yaml -f compose.nas.yaml build --pull
docker compose --env-file .env.nas \
  -f compose.yaml -f compose.nas.yaml up -d
```

更新后重复健康、数据库、401/200、浏览器和重启持久化验收。数据库 schema 高于程序支持版本时，服务会拒绝启动而不会尝试降级；此时使用已演练的备份创建并验收一个独立恢复部署。
