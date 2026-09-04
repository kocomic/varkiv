# 文档索引

文档只记录长期有效的产品合同、技术边界和可执行门禁。阶段过程、旧预览日志与一次性审计结果不进入公开文档。

## 从哪里开始

| 目的 | 文档 |
|---|---|
| 首次运行演示或安全建立资料库 | [QUICKSTART.md](QUICKSTART.md) |
| 理解产品、用户旅程和不可越过的边界 | [PRODUCT.md](PRODUCT.md) |
| 查看当前实现、下一阶段和完成条件 | [PLAN.md](PLAN.md) |
| 部署服务或迁移数据 | [DEPLOYMENT.md](DEPLOYMENT.md)、[NAS_DEPLOYMENT.md](NAS_DEPLOYMENT.md) |
| 调用或扩展 API | [API.md](API.md)、运行时 `internal/server/openapi.yaml` |
| 接入公开交换格式或设备同步协议 | [PROTOCOLS.md](PROTOCOLS.md) |
| 修改存储、数据库或恢复逻辑 | [STORAGE.md](STORAGE.md)、[DATABASE.md](DATABASE.md) |
| 扩展平台、设备、模拟器或 core | [PLATFORMS.md](PLATFORMS.md) |
| 修改 Web Player | [WEB_EMULATION.md](WEB_EMULATION.md) |
| 修改网页联机实验 | [WEB_NETPLAY.md](WEB_NETPLAY.md) |
| 验证功能与发布状态 | [ACCEPTANCE.md](ACCEPTANCE.md)、[RELEASE_READINESS.md](RELEASE_READINESS.md) |

## 产品与设计

- [PRODUCT.md](PRODUCT.md)：唯一的产品事实源，包含领域模型、导入/导出、设备与存档行为。
- [UI_LANGUAGE.md](UI_LANGUAGE.md)：界面语言、字体、布局、图标和响应式约束。
- [NAMING.md](NAMING.md)：正式名称、技术命名与品牌使用边界。
- [DECISIONS.md](DECISIONS.md)：需要长期保留的架构决策。

## 实现合同

- [API.md](API.md)：认证、错误、分页、预览/提交、两份 OpenAPI 合同与离线 API 文档生成。
- [PROTOCOLS.md](PROTOCOLS.md)：公开协议与版本索引。
- [HASHPACK.md](HASHPACK.md)：隐私最小化 ROM 身份包 v1。
- [PORTABLE_FORMATS.md](PORTABLE_FORMATS.md)：资料库清单 v6、启动 sidecar v2 与内部包清单边界。
- [DEVICE_SYNC_PROTOCOL.md](DEVICE_SYNC_PROTOCOL.md)：配对、运行证明、协商、传输、冲突与隐私状态机。
- [DATABASE.md](DATABASE.md)：schema、迁移和一致性规则。
- [STORAGE.md](STORAGE.md)：ROM、媒体、存档、包和恢复目录。
- [PLATFORMS.md](PLATFORMS.md)：平台预设、设备目标和运行适配器。
- [WEB_EMULATION.md](WEB_EMULATION.md)：浏览器能力矩阵、资产和安全边界。
- [WEB_NETPLAY.md](WEB_NETPLAY.md)：双浏览器 WebRTC 实验、信令部署与验收边界。

## 运维、合规与贡献

- [DEPLOYMENT.md](DEPLOYMENT.md)：Compose、本机运行、更新、备份和恢复。
- [NAS_DEPLOYMENT.md](NAS_DEPLOYMENT.md)：NAS 路径、预检、Synology 和验收。
- [PRIVACY.md](PRIVACY.md)：隐私、日志和文件访问边界。
- [RELEASE_READINESS.md](RELEASE_READINESS.md)：软件门禁与仍需人工完成的外部门禁。
- [../LICENSE](../LICENSE)、[CONTRIBUTION_RIGHTS.md](CONTRIBUTION_RIGHTS.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)：项目许可、贡献权利和第三方声明。
- [../CONTRIBUTING.md](../CONTRIBUTING.md)：开发流程。

## 维护规则

1. 产品行为只在 `PRODUCT.md` 定义一次，其他文档链接到它。
2. OpenAPI 文件是 HTTP 合同的机器可读事实源，`API.md` 不复制全部 schema。
3. `PLAN.md` 只保留当前版本和下一阶段，不追加按日期排列的开发日志；用户可见变更写入精简的 `CHANGELOG.md`。
4. `ACCEPTANCE.md` 只保留可重复执行的门禁和证据等级，不记录每个预览版本的测试流水。
5. 删除或改名文档后运行 `./scripts/check-docs.py`，避免留下失效链接。
