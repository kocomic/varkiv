# 路线图

当前版本：`0.1.0-preview.3`（preview.3）
产品事实源：[PRODUCT.md](PRODUCT.md)
状态：核心软件链已实现；当前优先级是可部署、可运行和真实设备证据

本文件只保留当前状态、下一步和完成条件。逐次变更由提交与 Release 产物保存，不在路线图累积开发流水。

## 已完成的软件基线

### 资料库

- Series、Game、Edition、Artifact 与多语言标题。
- 72 个平台预设及可扩展目录，默认范围到 Switch 世代。
- ROM/媒体内容寻址、直接扫描、多碟归组和人工编辑。
- Pegasus、ES-DE、中性清单和持久 SourceAdapter。
- 缺失 ROM 跳过、签名预览、防漂移候选和原子批量提交。
- 可导入/导出的 `.hashpack` ROM 识别库：来源归属、不可变发布、冲突并存、无 ROM 库污染。

### 整合包

- Pegasus、ES-DE 和中性恢复清单。
- PackageProfile、DeviceProfile、EmulatorDriver、RetroArch core 和配置模板。
- copy、hardlink、reference 策略，差异计划、不可变 release、恢复快照和回滚。
- Windows、SteamOS/Bazzite、Android、ROCKNIX、KNULLI、dArkOS、ArkOS 遗留、muOS、OnionOS 和便携目录的软件夹具。

### 同步与设备

- 短码配对、最小权限设备 token、显式 ROM/存档/运行根。
- SHA-256 inventory、Edition 绑定和匿名化歧义确认。
- Edition、Platform、Container 三类 SaveStream，多文件原子 revision 和冲突保留。
- Go Agent、Windows 托盘、Android 客户端及目标设备包。
- 运行合同证明、撤销、漂移检测和只追加证据记录。

### 运维与质量

- Go + SQLite 单进程、版本化迁移、OpenAPI 3.1 和结构化错误。
- Docker Compose、NAS/群晖部署包、备份、校验、分阶段恢复和恢复区。
- CI 成功后自动发布 amd64/arm64 GHCR edge 镜像；版本 Release 交付 digest Compose、SBOM 与来源证明。
- 单元、迁移、API、批量失败、Playwright、容器和模拟器验收脚本。
- 可选 EmulatorJS 资源校验、核心能力矩阵和多平台运行夹具。
- NES 双浏览器 WebRTC 实验闭环：精确 ROM/运行时校验、同源信令、房主媒体流、客端输入回传、两人限制和零持久存档写入。

## 当前工作顺序

### 1. 初始仓库与自动镜像交付

工作流已具备，初次推送到托管平台后完成外部启用与验收：

1. 将整理后的单一初始提交推送为默认分支 `main`，启用 GitHub Actions 与 Packages 写权限。
2. 等待 CI 成功并触发容器工作流，确认 `linux/amd64`、`linux/arm64` 的 `edge` 与 `sha-<commit>` 清单指向同一构建结果。
3. 核验镜像 SBOM、BuildKit provenance、GitHub attestation，以及 Actions 生成的 digest 固定 Compose 和 SHA-256 清单。
4. 按部署策略把 GHCR package 设为公开，或记录 NAS 使用的只读拉取凭据；凭据不得进入仓库、Compose 或构建产物。
5. 用生成的 digest Compose 在隔离目录运行 NAS 容器验收。只有固定版本 tag 通过完整发布门禁后才发布 `:<version>`，不把 `edge` 当长期固定版本。

完成条件：从空的 runner 缓存构建成功；两个架构均可拉取；镜像身份、源码提交与 Compose digest 一致；失败构建不会覆盖可用版本。

### 2. NAS 可运行验收

目标是在一台真实 NAS 上完成：

1. 运行 NAS 预检并记录文件系统、端口和目录边界。
2. 使用无凭据源码包或已发布镜像部署固定版本。
3. 验证健康检查、登录、只读 ROM 根和持久 state。
4. 导入一个小平台，重启服务，确认数据与媒体关系保持。
5. 生成 Pegasus/ES-DE 小型整合包并执行备份/恢复演练。

当前外部阻塞若涉及 NAS 登录、SSH 或目标路径授权，只记录为运维门禁，不用本机成功替代真实部署证据。

### 3. 浏览器模拟闭环

对每个开放平台分别证明：固定资产通过校验、ROM 被核心读取、游戏画面实际推进、输入生效，以及声明支持的存档可在新会话恢复。页面可打开、播放器外壳出现或 API readiness 通过都不能单独算完成。

NES 网页联机实验已完成自动化软件闭环；下一步只在明确需要公网使用时补 TURN/HTTPS、真实外网与双手柄证据。扩展到其他平台前必须逐平台固定核心和验证媒体/输入行为，不能从 NES 结果外推。

### 4. 设备链验证

按复杂度推进：

1. Windows 掌机 + RetroArch + Pegasus/ES-DE。
2. SteamOS/Bazzite + RetroArch。
3. 一种主流掌机 Linux 的 Package + Hook。
4. Android + Pegasus/SAF + RetroArch，再扩展 PPSSPP。
5. PS2、3DS、Dolphin 等复杂驱动与容器存档。

每条链分别完成导入、启动、重启、增量包、双向存档、离线恢复和冲突处理。

### 5. 稳定发布

- 完成第 9/42 类商标近似检索。
- 确认贡献权利链、Apache-2.0 inbound = outbound 政策和第三方 NOTICE。
- 配置受保护发布环境、签名与来源证明。
- 清零发布审计中的软件失败和人工外部门禁。

## 不进入当前范围

- 普通桌面 Linux 的泛化支持。
- 在线刮削、多人权限、成就和云游戏。
- 自动下载或分发模拟器、core、BIOS、固件、密钥或商业 ROM。
- 自动转换不同模拟器的 savestate。
- 根据名称、时间戳或可疑元数据猜测 ROM/存档归属。

## 完成条件

工具达到“功能完整”需要同时满足：

- 无刮削导入和 Pegasus/ES-DE 往返不会丢失 Edition、媒体或运行关系。
- 至少一条真实 NAS 部署能够更新、备份和恢复。
- 所有宣称支持的设备链都有与宣称等级相符的证据。
- Web Player 只展示真实通过运行门禁的平台。
- 设备客户端能自动同步真实游戏内存档，并安全处理离线、冲突和漂移。
- API、数据库迁移、批量失败、Playwright、容器与隐私门禁全部通过。
- 名称、许可证、贡献权利和发布凭据完成外部审查。
