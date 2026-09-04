# Varkiv 架构决策记录

## ADR-001：以独立项目而不是 RomM Fork 开始

Varkiv 的人工分组、多语言和可逆导入是底层模型，不适合作为刮削优先模型上的附属字段。独立项目也避免持续维护大型上游 Fork。

## ADR-002：Go + SQLite + 内嵌 Web UI

个人部署优先考虑低依赖和单容器。Go 标准库负责 HTTP、XML 和文件处理；SQLite 提供可靠事务和查询；Web UI 不使用独立构建链。后续可以在不改变 API 的情况下替换前端。

## ADR-003：Edition 是运行与存档的最小单位

游戏组只承担展示聚合。原版、汉化版和改版均是独立 Edition，拥有自己的 Artifact、启动配置、游玩记录和 save namespace。

## ADR-004：外部前端是适配目标，不是事实来源

Pegasus 和 ES-DE 无法完整表达多语言游戏组。统一模型是内部事实来源；导入保留原始信息，导出允许按目标能力有损展平，并生成额外 manifest 供无损回导。

## ADR-005：EmulatorJS 是可选运行驱动

平台运行能力由 Profile 决定。PS2、3DS 等平台使用本地设备代理；浏览器不承担通用启动器职责。

## ADR-006：设备模板、模拟器驱动和前端适配器正交

Windows 掌机、SteamOS/Bazzite、Android 与选定掌机 Linux 系统只定义设备能力和路径环境；PCSX2、RetroArch、3DS 等定义启动与存档行为；Pegasus、ES-DE 定义展示和配置格式。三者通过 Profile 组合，避免新增设备或模拟器时修改领域模型。普通桌面 Linux 不进入兼容承诺。

## ADR-007：存档只追加 revision，不覆盖历史

每次内容变化创建不可变 revision，存档 blob 按 SHA-256 寻址并去重。上传必须带设备已知的基线 revision；基线不是当前最新版本时保留双方并标记冲突。正式协议使用 SaveStream 和多文件原子 revision；旧单文件接口只作兼容，产品不把手工上传作为正常用户流程。

## ADR-008：局域网监听必须显式启用令牌

默认只监听 `127.0.0.1`。监听非回环地址时必须提供 Bearer Token，否则服务拒绝启动。静态 UI 可以公开加载，但所有资料库、整合包、设备和存档 API 均受令牌保护；互联网访问还应使用 HTTPS 反向代理或可信 VPN。

## ADR-009：产品是资料库控制面，不是新的通用启动器

一级产品任务是建库、人工整理、来源导入、整合包生成和设备同步。Pegasus、ES-DE 和设备本机模拟器继续承担游戏浏览与启动；Web UI 是管理控制面。EmulatorJS 可以作为 Edition 的可选 LaunchBinding，但不决定产品架构，也不阻塞原生掌机主线。

## ADR-010：Device Adapter 先于 Web Player

多设备访问和自动存档同步是已确认需求，PS2、3DS 等平台又必须使用本机模拟器，因此第一条纵向交付选择 Windows 掌机 + RetroArch Agent。EmulatorJS 延后到自动同步协议和至少一个真实客户端稳定之后。这样不会用只覆盖少数平台的 Web 能力替代核心需求。

## ADR-011：复杂存档归属于 SaveStream，不强制归属于单一 ROM

Edition 仍是游戏级身份，但 PS1/PS2 记忆卡、平台共享文件和多文件目录存档不能强制伪装成某一个 Artifact 的单文件附件。正式模型使用 SaveStream 表达 game、platform、container owner，SaveRevision 原子包含一个或多个 SaveFile；SaveBinding 再把 Edition、设备环境、模拟器驱动和本地发现规则连接起来。Artifact 哈希只负责 ROM 匹配，不作为存档 revision 的唯一外键。

## ADR-012：设备支持按证据分级

平台/路径预设、夹具包生成、真实硬件运行和双向存档同步分别标记为 Catalogued、Package-tested、Hardware-tested、Sync-tested。文档和 UI 不再把“有预设”或“生成成功”写成完整支持。每提升一级必须保存目标系统、前端、模拟器版本、文件策略和验收证据。

## ADR-013：三种 Device Adapter 共享协议而不共享安装形态

Windows 与 SteamOS/Bazzite 使用 Full Agent；Android 使用受 SAF、Content URI 和后台限制约束的 Mobile App/Agent-lite；只读掌机 Linux 优先使用 Package + Hook、SSH/rsync/WebDAV。三种实现共享配对、inventory、协商、revision 和冲突语义，但不强求同一 UI、进程模型或文件访问方式。

## ADR-014：自定义适配器特化已编译 handler

SourceAdapter 和 FrontendAdapter 是版本化能力合同，不是从 Web 上传可执行插件的入口。用户自定义项可收窄能力、更换外部格式标识和组合已审计 handler；新 handler 必须以受限编译实现、迁移和夹具测试进入版本。这使扩展性不会变成任意 shell 或无界文件系统权限。

FrontendAdapter 的 `format` 与 `handler` 必须分离：前者允许用户表达自己的目录格式名称，后者只选择 Pegasus 或 ES-DE 的内置渲染器。PackageProfile 按 handler 校验并执行，不根据自定义名称猜测；旧库中无法确定的格式保持未绑定，必须人工确认后才能导出。任何无效或错配适配器都要在建立输出目录前失败。

## ADR-015：Package 回滚快照与可移植输出物理分离

更新既有整合包前，只备份本次将被替换的既有受管文件到 `state/recovery/packages/<slug>`。`state/exports/<slug>` 只包含要交付给设备的当前版本，不嵌套历史 ROM/媒体。旧版已有 `.varkiv-backups` 不自动移动或删除；保留期由管理者离线审查。

## ADR-016：列表读取使用独立读模型

Game 与 Series 的集合查询不复用逐项详情读取。读模型在 SQLite 内完成筛选、稳定排序、总数计算和分页，再以有界批次装配当前页；HTTP 层只翻译查询参数和分页信封。`projection=summary` 只投影列表标题、版本元数据、Artifact 聚合状态和不透明封面引用，不返回 ROM/媒体路径、哈希或逐项记录；编辑、运行和文件管理再读取完整 Game。full 与 summary 共享同一只读事务管线和 ID 页，避免两套投影的计数、排序或快照漂移。

## ADR-017：按领域拆分持久化与 HTTP 模块

数据库连接、迁移、Series、Game/Edition/Artifact/Media、原子导入、持久来源、Package 生命周期和 Device 持久化各自使用独立文件；HTTP 的平台/系列、资料库写入、导入、设备/存档和兼容接口也各自成域。模块仍共享同一个 `catalog` 或 `server` 包，以保留事务内私有 helper 和避免为了“分层”制造循环依赖；新能力应进入对应领域文件，只有构造、路由和安全中间件留在 `server.go`。拆分只改变源码所有权，不改变 API、SQLite schema 或数据。

## ADR-018：参考识别库与本地 Artifact 分离

可分享哈希数据只能作为带来源的参考读模型，不能在本地 ROM 不存在时伪造 Game、Edition 或 Artifact。`.hashpack` 因此使用独立的 Source/Release/Identity 表，同一来源发布不可变，不同来源的分歧并存，实际识别只在客户端或服务端对现有 ROM 重算 SHA-256 后发生。

## ADR-019：联机属于产品入口，协调协议独立版本化

Varkiv 保留联机入口与资料库/设备身份解析，但公开客户端只依赖 `/api/multiplayer/v1` 的 ContentIdentity、RuntimeIdentity 和会话生命周期，不依赖 Game、Edition 或 SQLite schema。短时会话先以内存模块随单进程部署，之后只有在公网中继、独立扩缩容或安全边界确有需要时再拆服务。首个 RetroArch Profile 只提供严格兼容校验和协调；没有真实流量中继、客户端自动启动与端到端运行证据时不得标记为可玩。

## ADR-020：网页联机用隔离的房主流式实验验证，不污染稳定 Web Player

网页直接联机先固定为 NES、EmulatorJS 4.3.0-pre、FCEUmm、两人和 `no-persist`。房主运行核心并向客端发送媒体，客端输入经 WebRTC 数据通道返回；Varkiv 负责精确 ROM/运行时校验、短时能力和同源信令代理。实验资源目录、播放器入口和能力发现与稳定 EmulatorJS 4.2.3 Web Player 分离，避免预发布 netplay 代码无意替换已经验收的单人运行链。信令先以固定上游提交的最小 sidecar 同 Compose 部署；只有公网 TURN、独立扩缩容或安全隔离成为真实需求时，才将它发展为独立服务项目。
