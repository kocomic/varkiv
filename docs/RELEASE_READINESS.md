# 发布门禁

稳定发布要求软件门禁和外部门禁同时完成。CI、容器或模拟器成功不能替代名称、权利链、发布凭据和真机证据。

## 软件门禁

维护者在干净提交执行：

```bash
go test ./...
go vet ./...
npm ci
npm run test:e2e:install
npm run test:e2e

./scripts/check-version.sh
./scripts/check-source-hygiene.sh
./scripts/test-source-hygiene.sh
./scripts/check-docs.py
./scripts/acceptance-container.sh --port 18085
./scripts/acceptance-compose-nas.sh --port 18186
./scripts/acceptance-compose-nas.sh --port 18187 --published-compose

version="$(tr -d '\r\n' < internal/buildinfo/VERSION)"
./scripts/build-release-archives.sh "$version" dist
./bin/varkiv release-audit --db ./data/library.db --require-hardware --json
```

发布产物必须带版本、SHA-256、第三方 NOTICE 和可追溯来源；amd64/arm64 容器镜像发布到 GHCR，并提供固定到 manifest digest 的 Compose、SBOM 和 provenance。仓库及包内不得包含 ROM、BIOS、固件、密钥、token、数据库或宿主绝对路径。

## 外部门禁

### E1：真实设备

目标矩阵中的每项必须有当前、独立、经维护者确认的 Hardware-tested / Sync-tested 证据。软件探测和虚拟机结果只作为较低等级。

### E2：正式名称

按 [NAMING.md](NAMING.md)完成语义审查、主要代码/应用商店检索，以及 CNIPA、USPTO、EUIPO、WIPO 的第 9/42 类近似检索。域名不是门禁。

### E3：贡献权利

- 确认现有代码与素材可按 Apache-2.0 发布。
- 执行 Apache-2.0 inbound = outbound 政策，并保留第三方与素材来源记录。
- 核对 [CONTRIBUTION_RIGHTS.md](CONTRIBUTION_RIGHTS.md) 和 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

### E4：受保护发布

- 发布密钥、签名、registry 和 provenance 只存在于受保护环境。
- 从干净 tag 构建，产物与清单可重复核验。
- 失败或缺少授权时不允许降级为无签名稳定发布。

## 发布判定

```text
software gates PASS
AND hardware gates PASS
AND naming review PASS
AND contribution rights PASS
AND protected release PASS
```

任何一项未完成，只能继续作为预览版或内部构建，不得在文案中暗示稳定支持。
