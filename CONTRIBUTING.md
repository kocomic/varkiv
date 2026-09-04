# Contributing

Varkiv 目前处于首个稳定 API 之前的 preview 阶段。欢迎问题报告、设计讨论、平台数据修正和小而可验证的改动。产品名与技术命名空间统一为 `Varkiv` / `varkiv`。

项目采用 [Apache-2.0 inbound = outbound 贡献政策](docs/CONTRIBUTION_RIGHTS.md)，当前不要求额外 CLA 或 DCO。提交者仍必须确认自己有权提交全部代码、文档和素材，并披露需要审查的第三方或生成式工具来源。

## 开始之前

- 不要提交商业 ROM、BIOS、固件、密钥、存档或从整合包复制的大型媒体文件。
- 不要让刮削结果覆盖人工整理；Game、Edition、Artifact 三层边界必须保持。
- 不要把某个平台硬编码到单一模拟器。游戏平台、设备、前端和模拟器驱动是独立维度。
- API 变更先更新 `internal/server/openapi.yaml` 和 `docs/API.md`，再更新实现与契约测试。
- 数据库 schema 变更必须增加单向迁移、schema 版本和旧库升级测试。

## 本地验证

需要 Go 1.26.6 或更新的兼容补丁版本，以及 Node.js（仅用于检查内嵌前端 JavaScript 语法）：

```bash
./scripts/check-source-hygiene.sh
./scripts/test-source-hygiene.sh
gofmt -w ./cmd ./internal
node --check internal/server/web/app.js
./scripts/check-protocol-schemas.sh
./scripts/build-api-docs.sh ./dist/api-docs
go test -race ./...
go vet ./...
go build ./...
```

源码卫生门禁会同时检查已跟踪文件与未被 `.gitignore` 排除的新文件：真实或未知 ROM/归档、媒体、存档、数据库、签名材料、机器专属 macOS 路径和超过 8 MiB 的单文件都会阻断提交。仓库中允许的 ROM 形状文件和两张 SVG 封面都是逐字节锁定的微型合成夹具；修改或新增夹具/项目图形必须先完成人工来源与隐私复核，再显式更新白名单身份。

Android companion 的本地 JVM、Lint 与 APK 验证：

```bash
cd clients/android
./gradlew --no-daemon testDebugUnitTest lintDebug assembleDebug assembleDebugAndroidTest
# 仅在连接专用测试设备或 AVD 后运行；不要在含个人资料的日用设备上使用测试身份。
./gradlew --no-daemon connectedDebugAndroidTest
```

提交前还应运行 `./scripts/demo.sh`，在 1365、768 和 390 px 三个宽度检查游戏库、导入/导出、设备/存档和平台页。任何新增 UI 控件都必须有键盘焦点、可读标签和无横向溢出的移动布局。

## 变更要求

- 一个 pull request 聚焦一个问题，按仓库模板说明用户可见行为、兼容性、来源和验证证据。
- 新 API 必须使用 `/api/v1`、结构化错误、请求 ID，并在集合端点使用统一分页。
- 每个 OpenAPI 操作必须保留由 HTTP 方法和版本内路径确定的唯一 `operationId`；路由、方法、合同与本地 `$ref` 解析由契约测试共同锁定。
- 修改请求或响应 schema 时同时补充约束、错误状态和必要示例；不要只让实现接受一个 OpenAPI 未声明的字段。
- 不兼容变更不能静默进入 v1；应新增版本或先经历弃用周期。
- 文件操作默认只读源资料库，不自动移动、重命名或删除用户 ROM。
- 新增平台预设时应同时提供 ID、别名、常见格式、ES-DE 映射和运行边界测试。

详细架构见 `docs/PLAN.md` 与 `docs/DECISIONS.md`，当前验收基线见 `docs/ACCEPTANCE.md`。
