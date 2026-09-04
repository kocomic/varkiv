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

`vX.Y.Z-后缀` 标签进入预览发布：自动执行软件、浏览器、导入导出、目标整合包、容器和 NAS Compose 门禁，发布唯一的版本镜像标签，并在未登录 GHCR 的条件下分别运行 amd64/arm64 镜像。预览发布不伪造 Android 正式签名，也不要求把尚未完成的真机门禁标成通过。

不带后缀的 `vX.Y.Z` 稳定标签还要求仓库变量 `PUBLIC_RELEASE_APPROVED=true`、`HARDWARE_RELEASE_APPROVED=true`，以及四项 Android 签名 secret：`ANDROID_RELEASE_KEYSTORE_B64`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`。缺少任一门禁会终止发布，不会降级为预览或无签名 APK。

发布工作流拒绝替换已经存在的 GitHub Release。GHCR 只发布不可变的 `:<version>` 标签；长期部署使用 Release 内记录的 manifest digest，`edge` 仅由 main 分支 CI 维护。Release 同时附带目标二进制包、第三方许可证包、应用 SBOM、digest Compose、环境模板、精确镜像引用和 `SHA256SUMS`，并为镜像与文件生成 GitHub attestation。

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
