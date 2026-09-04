# HTTP API 指南

机器可读合同以 [`internal/server/openapi.yaml`](../internal/server/openapi.yaml) 和独立的 [`internal/server/multiplayer_openapi.yaml`](../internal/server/multiplayer_openapi.yaml) 为准。本文件解释认证、错误、分页、重试和资源流程，不复制全部 request/response schema。每个操作都有由 HTTP 方法与版本内路径确定的唯一 `operationId`，可用于稳定生成客户端方法。

## 阅读与生成 API 文档

运行中的服务公开两份 OpenAPI 3.1 合同：

```bash
curl --fail-with-body --output varkiv-openapi.yaml \
  http://127.0.0.1:8080/api/v1/openapi.yaml
curl --fail-with-body --output varkiv-multiplayer-openapi.yaml \
  http://127.0.0.1:8080/api/multiplayer/v1/openapi.yaml
```

仓库提供固定 Redocly CLI 版本的本地生成器。它先 lint 两份合同，再在一个全新目录生成可离线打开的自包含 HTML；输出目录已存在时会拒绝覆盖：

```bash
./scripts/build-api-docs.sh ./dist/api-docs
open ./dist/api-docs/index.html          # 主资料库与设备 API
open ./dist/api-docs/multiplayer.html    # 联机协调 API
```

`open` 是 macOS 命令；其他系统直接用浏览器打开对应 HTML。生成器会通过 npm 获取精确版本 `@redocly/cli@2.51.1`，它是维护工具，不进入 Varkiv 服务镜像。无需渲染器也可以直接把两份 YAML 交给支持 OpenAPI 3.1 / JSON Schema 2020-12 的客户端生成器。

## 入口与版本

| 用途 | 入口 | 认证 |
|---|---|---|
| API 根与链接发现 | `GET /api/v1` | 公开 |
| OpenAPI 3.1 合同 | `GET /api/v1/openapi.yaml` | 公开 |
| 能力与限制发现 | `GET /api/v1/capabilities` | 公开 |
| 存活探针 | `GET /api/v1/health/live` | 公开 |
| 数据库与 schema 就绪探针 | `GET /api/v1/health/ready` | 公开 |

旧 `/api` 兼容入口可能返回弃用头；新客户端只能依赖 `/api/v1`。游戏资源统一使用 `/games` 与 `game_id`，不再公开早期 preview 的领域别名。

最小发现请求：

```bash
curl --fail-with-body http://127.0.0.1:8080/api/v1
curl --fail-with-body http://127.0.0.1:8080/api/v1/capabilities
curl --fail-with-body --output varkiv-openapi.yaml http://127.0.0.1:8080/api/v1/openapi.yaml
```

客户端应先读取 API 根和 capabilities，再启用可选功能；不能只根据服务版本字符串猜测能力。

## 认证

回环地址可按部署配置免认证。监听局域网或更广地址时，管理请求必须携带：

```http
Authorization: Bearer <GAME_LIBRARY_TOKEN>
```

| 凭据 | 获取方式 | 允许范围 |
|---|---|---|
| 所有者 token | 部署时配置 | 未显式标为公开的管理 API |
| 设备 token | `POST /pairing-codes/redeem` 一次返回 | 仅获准的心跳、同步与指定 revision 元数据 |
| 签名 capability | Web Player、联机或下载流程返回 | 指定内容与操作，且有短期有效期 |

设备通过短码换取独立、可撤销的 token。管理 token 与设备 token 不可互换；设备 token 即使猜中其他资源 ID，也会被路由 scope 和设备绑定再次拒绝。客户端不得把任何 token 写入 URL、日志、崩溃报告或错误正文。只有 OpenAPI 中明确声明 `security: []` 的操作才可匿名访问；其中的内容/session URL 仍可能依靠路径中的短时签名授权。

## 公共约定

### 请求身份

服务接受由字母、数字、点、下划线和连字符组成、最长 128 字符的 `X-Request-ID`；缺失或非法时生成新值。所有 API 响应都带回 `X-Request-ID`、`X-Varkiv-API-Version: v1` 和 `Cache-Control: no-store`。错误统一为结构化对象，至少包含稳定 `code`、用户可读 `message` 和 `request_id`；客户端按 `code` 分支，不解析英文句子。

```json
{
  "error": {
    "code": "invalid_argument",
    "message": "request payload is invalid",
    "request_id": "40e39da4b75c41c28cf40b9f8de46c48"
  }
}
```

### JSON 与时间

- 请求和响应使用 UTF-8 JSON；文件内容端点除外。
- 发送 JSON 正文时必须使用 `Content-Type: application/json`；上传和二进制端点以 OpenAPI 声明为准。
- 时间使用 RFC 3339 UTC。
- ID 是不透明稳定字符串，客户端不得推断其组成。
- 客户端读取响应时应兼容忽略未知字段；服务端对请求使用严格字段校验，会拒绝未声明字段、非法枚举和多余的第二个 JSON 值。

### 分页

集合端点使用 `limit + offset`，响应返回同页的 `limit`、`offset` 与查询时的 `total`。默认 `limit` 为 100，最大 200；调用方以“本页实际返回数”推进 offset，在返回空页或达到 total 时停止。不得假定默认上限等于完整集合，也不得依赖未在端点说明中的数据库自然顺序。

offset 分页不是跨快照游标：并发导入、删除或筛选条件变化可能改变后续页。需要一致全量视图的客户端应在发现 `total`、offset 或业务身份漂移时从第 0 页重试，不能拼接两个不同时间点的结果。

### 状态码与重试

| 状态 | 客户端语义 |
|---|---|
| `200` / `201` / `204` | 成功；是否有正文以 OpenAPI 为准 |
| `400` / `415` / `422` | 请求、媒体类型或领域输入无效；修正后再提交 |
| `401` / `403` | 凭据缺失、失效或 scope 不足；不要用相同凭据无限重试 |
| `404` | 资源不存在，或短时 capability 已不可用 |
| `409` | 版本、内容身份、签名预览或原子批次冲突；重新读取/预览后决定 |
| `410` | 一次性或短时能力已消费/过期，必须重新申请 |
| `413` | 请求超过合同上限；缩小批次或文件 |
| `503` | 依赖暂不可用；仅对安全读取或明确幂等的写入做带抖动退避 |

除 OpenAPI 明确声明幂等语义外，客户端不能自动重放 `POST`。`PUT` 和 `DELETE` 的 HTTP 语义不代表业务前置条件仍然成立；冲突后仍须重新读取。要求 `Idempotency-Key` 的同步操作以 8–128 字符的稳定随机值标识一次逻辑请求，相同键只能重试完全相同的正文。

### 预览与提交

所有可能批量写入、覆盖受管文件或建立歧义绑定的流程遵循：

```text
POST .../preview → review signed token → POST .../commit
```

令牌绑定规范化输入、候选集合、顺序、内容身份和到期时间。提交会在锁内重新读取与验证；任何漂移使整个批次失败，不允许部分写入。客户端必须把预览当作短期能力，不能持久化后长期重放。

预览成功不保证提交成功；`409` 后应丢弃旧 token 并重新预览。提交响应在传输中丢失时，只有端点明确记录幂等结果才能原样重试；否则先读取目标资源或审计记录确认是否已落库。

### 文件下载与上传

- 上传需要 OpenAPI 指定的大小、类型和 SHA-256 约束。
- 下载前服务重新验证安全路径、普通文件类型、大小和内容身份。
- 内容 URL 和会话 token 都是短期、范围受限能力，不得作为永久资源地址保存。

## 核心资源

```text
Series → Game → Edition → Artifact
                 ├─ Media
                 ├─ LaunchBinding
                 └─ SaveBinding → SaveStream → SaveRevision → SaveFile

Source → SourceScan → import commit
PackageProfile → PackagePlan → PackageRelease
DeviceProfile + EmulatorDriver + RetroArchCore → runtime resolution
Device → Inventory → SyncSession → SyncOperation
```

Series 只做跨平台浏览；Game 属于一个平台；Edition 是真正的运行与游戏级存档身份；Artifact 是实际 ROM/光盘/目录等内容。SHA-256 用于内容识别，不替代 Edition ID。

## 端点分组

### 目录与媒体

- `/platforms`、`/custom-platforms`
- `/series`
- `/games`
- `/editions`
- `/artifacts`
- `/media`

合并 Game、移动 Edition、设置主版本和批量重新检查文件均有专用端点。媒体上传和内容下载不允许调用方直接指定宿主路径。

### 来源与导入

- `/import-sources`
- `/sources`、`/source-scans`
- `/imports/preview`、`/imports/commit`
- `/imports/roms/preview`、`/imports/roms/commit`
- `/source-adapters`
- `/hash-sources`、`/hash-identities/{sha256}`
- `/hash-packs/preview`、`/hash-packs/import`、`/hash-packs/export`

持久来源保存经过规范化的根目录、适配器和策略；删除来源配置不会删除 ROM 或媒体。缺失 ROM 只进入扫描报告，不能提交为空 Artifact。

Hash pack 是仅哈希与版本资料的 ZIP 合同。导入必须使用预览返回的签名 token 和完全相同的文件；首次提交返回 `201`，已存在的精确发布幂等返回 `200`，同来源同发布但内容不同返回 `409 hash_release_conflict`。导入只写识别库，不创建 Game、Edition 或 Artifact。

### 整合包

- `/package-profiles`
- `/package-plans`
- `/package-releases`
- `/frontend-adapters`
- `/config-template-presets`

先创建持久 Profile，再创建只读 Plan，最后以 plan ID 构建不可变 Release。计划会阻断越界路径、不支持的 hardlink、空间不足、未受管覆盖、手工漂移和非法模板。

### 运行适配

- `/device-profiles`
- `/emulator-drivers`
- `/retroarch-cores`
- `/core-mappings`
- `/launch-bindings`
- `/runtime-import-hints`

内置目录对象只读，`builtin-*` 命名空间保留。解析遵循 Edition → Device → Platform → Global 的明确优先级；响应应保留所用合同版本，不能只缓存最终命令字符串。

### Web Player

- `/web-emulation/readiness`
- `/web-emulation/editions/{id}`
- `/web-emulation/sessions`
- `/web-emulation/content/{token}/{name}`
- `/web-emulation/saves/{token}`

创建会话前服务核对平台、Artifact、资源和核心能力。成功创建会话不等于游戏已经运行，客户端仍需处理资源加载、核心错误和存档提交。

Web Player 声明支持键盘、浏览器 Gamepad API 和触屏虚拟控制器。手柄发现是自动的，但物理按钮布局保持可配置；界面展示 EmulatorJS 默认键盘键位，并引导用户在模拟器控制面板核对不同手柄的映射。

### 在线联机协议

联机协调 API 独立版本化为 `/api/multiplayer/v1`；能力与 OpenAPI 分别位于 `/capabilities` 和 `/openapi.yaml`。创建、读取和关闭会话需要管理员令牌；加入请求以正文中的短时邀请令牌授权，避免把秘密放进 URL、日志或跳转来源。

通用协议当前提供 `retroarch-netplay-v1` 协调预览，以及供浏览器实验声明能力的 `emulatorjs-webrtc-v1` Profile。创建与加入都携带通用的 ContentIdentity 和 RuntimeIdentity；服务精确比较 SHA-256、大小、平台、模拟器、版本、核心和核心版本。任何漂移返回 `409 compatibility_mismatch` 且不加入参与者。RetroArch Profile 的 `data_relay: false` 与 `automatic_launch: false` 仍是明确边界，不得把会话 `ready` 解释成原生模拟器已经联网运行。

资料库 UI 通过 `/web-netplay/readiness`、`/web-netplay/sessions` 和 `/web-netplay/sessions/join` 把 NES Edition 解析为浏览器实验的精确 ContentIdentity，并签发 `/play-netplay/{token}`。创建房间需要管理员认证；加入由正文中的一次性邀请授权。信令通过同源 `/list` 与 `/socket.io` 代理，内部上游地址不进入 API。播放器 URL、ROM 内容 URL 和邀请都属于短时能力，不能持久化。完整边界见 [WEB_NETPLAY.md](WEB_NETPLAY.md)。

### 设备与同步

- `/pairing-codes`、`/pairing-codes/redeem`
- `/devices`
- `/sync/config`
- `/sync/inventory-matches`
- `/sync/sessions`
- `/save-streams`
- `/save-revisions`
- `/save-bindings`
- `/save-compatibility-groups`
- `/runtime-attestations`

正式客户端流程：配对 → 心跳与运行证明 → inventory → 必要时人工匹配 → 创建 session → 逐项传输 → SHA-256 验证 → ack。revision 只追加；冲突返回独立分支，不支持“最后写入者获胜”。

`/saves`、`/saves/upload` 和 `/sync/manifest` 是旧单文件客户端兼容入口，不用于新 UI 或新 Agent。

### 运维与审计

- `/hardware-acceptance`
- `/support-readiness`
- `/storage-cleanup`

真实设备报告先预览、后由维护者确认。受管存储清理只隔离明确选择且在提交时再次验证为孤立的文件；恢复拒绝覆盖。API 不提供任意路径删除或自动永久清空恢复区。

## 兼容性规则

- 新增可选字段和枚举前先更新 OpenAPI、服务端验证与客户端能力发现。
- 删除或改变语义需要新版本路径，不能静默复用现有字段。
- `operationId` 由方法与版本内路径确定；同一路由的说明文字可改，SDK 方法身份不得随文案漂移。
- OpenAPI 中的相对 server URL `/api/v1` 应解析到用户配置的 Varkiv origin；生成客户端不能写死 localhost、端口或部署子网。
- 生成器必须支持 OpenAPI 3.1 与 JSON Schema 2020-12；不支持时应在 CI 中失败，不能静默降级或丢弃 `const`、联合类型和约束。
- 数据库 ID 和 API 资源名按领域职责保持稳定，不为展示层做无意义重命名。
- API 变更必须同时更新相关测试和本文件；完整流程见 [CONTRIBUTING.md](../CONTRIBUTING.md)。

契约测试会同时检查服务端路由与 OpenAPI 方法一一对应、`operationId` 唯一且可预测、所有本地 `$ref` 可解析，以及公开版本/许可元数据存在。客户端仓库仍应把“下载当前合同 → 生成 → 编译 → 运行最小发现请求”作为自己的 CI 门禁。
