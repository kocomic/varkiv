# 产品基线

状态：软件基线已实现，NAS 与真实设备验收仍在推进
正式名称：`Varkiv`；技术标识统一使用 `varkiv`

本文件是产品行为的唯一事实源。路线图、API、UI 和实现与它冲突时，应先修订产品决策，而不是让多个文档分别定义同一件事。

## 定位

一个轻量、自托管、面向个人与家庭的掌机资料库控制面：将现有 ROM、Pegasus、ES-DE 和人工整理结果编译为统一游戏库，为不同设备生成可重复更新的整合包，并通过设备客户端自动同步存档。

它不是模拟器、刮削站或通用启动器，也不要求替代 Pegasus、ES-DE。通用在线协议只承担模拟器感知的会话协调；游戏数据传输与启动仍由相应客户端和运行时完成。独立的网页联机实验可以为受限平台编排浏览器 WebRTC，但不改变这条通用边界。

## 核心原则

1. **人工整理优先**：不用刮削也能完整工作，人工名称、归组和版本关系是一级数据。
2. **版本真正独立**：原版、汉化版、改版和修订版各自拥有文件、启动和存档身份，只在展示上聚合。
3. **整合包是正式产物**：导入、预览、增量更新、配置模板和可恢复发布都属于核心流程。
4. **已有目录友好**：NAS 和整合包默认只读引用，不强迫重排、改名或上传全部 ROM。
5. **掌机优先**：覆盖 Windows 掌机、SteamOS/Bazzite、Android 和选定掌机 Linux，不承诺普通桌面 Linux。
6. **低运维**：单服务进程、SQLite、文件系统和可选设备客户端，不依赖 Redis 或任务集群。

## 用户要完成的任务

1. 导入 ROM 目录、Pegasus、ES-DE 或中性清单，并在写入前审查变化。
2. 管理跨平台 Series、单平台 Game、独立 Edition、多语言名称和资源关系。
3. 为一台设备生成或增量更新整合包，不破坏来源或未受管文件。
4. 让设备客户端按 ROM 内容识别 Edition，并自动同步模拟器存档。
5. 在 ROM 缺失、设备离线、内容冲突或数据库损坏时看懂状态并恢复。
6. 导出或导入可审计的 ROM 识别包，与他人共享内容哈希、版本类型和多语言名称，但不共享 ROM 或私有路径。
7. 让受支持的客户端在核对 ROM、模拟器、核心和版本后建立短时联机会话。

## 系统边界

```text
ROM / NAS / Pegasus / ES-DE
        │ 只读扫描、预览、确认
        ▼
Central Library Hub
Catalog · Sources · Packages · Sync · Backup
        │                         │
        │ package                 │ versioned protocol
        ▼                         ▼
Pegasus / ES-DE             Device Adapter
                            Agent / App / Hook
                                  │
                                  ▼
                          Emulator + save roots
```

控制面是目录关系、人工整理和 revision 的事实来源；它可以引用外部 ROM，也可以管理显式复制进 state 的内容。设备客户端只访问用户授权的 ROM、模拟器和存档根。服务不分发模拟器、core、BIOS、固件、密钥或商业 ROM。

## 领域模型

```text
Series ──跨平台浏览──> Game ──同平台聚合──> Edition
Game ──拥有──> shared Media
Edition ──拥有──> Artifact / Media / LaunchBinding / SaveBinding
```

- **Series**：跨平台关系，不拥有 ROM 或存档。
- **Game**：同一平台的游戏和本地化标题集合。
- **Edition**：原版、汉化版、改版或修订版，是运行与游戏级存档身份。
- **Artifact**：ROM、光盘、目录、补丁、DLC、更新或其他入口。
- **SHA-256**：识别具体内容并去重，不代替 Edition 或存档所有权。
- **MediaAsset**：可属于 Game 或 Edition；受管 blob 按内容去重，解除关系不立即删除共享原件。

游戏名与版本名可保存多个 BCP 47 语言标签。界面显示完整自然语言名称，不使用“简中”“繁中”之类内部缩写代替用户可读文本。

## 导入

支持四类来源：资料库扫描、Pegasus、ES-DE 和版本化中性清单。SourceAdapter 将来源规范化为统一候选，不加载来源提供的任意代码。

固定流程：

```text
Scan → Preview → Select → Revalidate → Commit → Report
```

- 预览令牌绑定来源、候选顺序、文件身份和哈希；提交时全部重验，防止候选漂移。
- ROM 必须存在且能够安全读取、计算 SHA-256 后才能入库。元数据存在但 ROM 缺失时跳过，并进入报告。
- 批量提交采用全有或全无语义；失败不留下空 Game、Edition 或部分关系。
- CUE/BIN、M3U 和多碟引用归为同一 Edition，但每个实际文件仍有独立 Artifact。
- 用户明确选择 ROM 的 `reference` 或 `copy` 策略，以及媒体的 `reference`、`copy` 或 `ignore` 策略。
- 扫描不跟随越界符号链接，不移动、改名或删除来源文件。

## ROM 识别库与分享

`.hashpack` 是独立于游戏库和整合包的第三种交换物：

- 只包含规范 SHA-256、已知大小、平台、Game/Edition 名称、版本类型、语言、作者和可选 serial/product/title ID。
- 不包含 ROM 字节、宿主路径、文件名、存档、媒体、设备、凭据或游玩记录。
- 导入先解析并生成签名预览，提交必须重传同一字节并重验。无 ROM 时也不会创建 Game、Edition 或 Artifact。
- `source + release` 是不可变发布身份；同名发布内容改变会拒绝。不同来源对同一 SHA 有分歧时并存且标注来源，不相互覆盖。
- 识别库是参考读模型，不是 ROM 存在性证明或游戏库事实源；实际入库仍要对本地 ROM 计算哈希。

## 存储与媒体

- 外部 ROM：数据库保存经根目录约束的引用和内容身份，文件仍由用户管理。
- 受管 ROM：复制到 state 内的内容寻址区域，写入后复核大小和 SHA-256。
- 媒体原件：按 SHA-256 去重；缩略图是可重建私有缓存，不是事实源。
- 丢失或变化的文件只标记状态，不猜测重绑、不删除关系。
- 维护操作采用 `mark → signed preview → locked recheck → quarantine`；当前不自动永久删除。

目录与恢复合同见 [STORAGE.md](STORAGE.md)，数据库一致性见 [DATABASE.md](DATABASE.md)。

## 整合包与运行适配

```text
LibrarySource --SourceAdapter--> Canonical Catalog
Canonical Catalog --FrontendAdapter--> Package Plan / Release
Edition + DeviceProfile --EmulatorDriver--> LaunchBinding / SaveBinding
PackageRelease --ConfigTemplate--> Reviewed managed files
```

- FrontendAdapter 支持 Pegasus、ES-DE 和中性清单。
- PackageProfile 固化设备、前端、语言、路径和文件策略；构建前显示 ROM、媒体、元数据与配置差异。
- 支持 copy、同卷 hardlink 和 reference；不支持的文件系统能力必须在计划阶段阻断。
- 内置 DeviceProfile、EmulatorDriver、RetroArchCore 和配置模板只读；定制内容通过复制产生用户对象。
- 配置模板只允许白名单变量和声明式文件输出，禁止 shell、环境变量和宿主绝对路径注入。
- 受管文件更新前创建独立恢复快照；失败逐文件回滚，未受管既有文件不静默覆盖。
- 发布清单携带实际使用的运行合同快照，设备端拒绝缺失、漂移或同 ID 冒充的对象。

PS2、3DS、Dolphin 等原生模拟器从模型层纳入，但不会因为有预设就宣称已经在真实设备可用。平台范围和证据等级见 [PLATFORMS.md](PLATFORMS.md)。

## 自动存档同步

```text
Pair → Inventory → Match → Negotiate → Transfer → Verify → Commit
```

```text
SaveStream
  owner: Edition | Platform | Container
  driver_id + scope_key + portability
  └─ SaveRevision
       └─ SaveFile[]
```

- Game scope 用于 `.srm`、Title ID 独立存档等；Platform scope 用于共享数据库；Container scope 用于 PS1/PS2 记忆卡等多游戏容器。
- 一个 revision 是原子文件集合；历史只追加，基线分叉保留双方，不按修改时间静默覆盖。
- Savestate 与游戏内存档默认分开；跨模拟器只共享经过字节往返验证的精确格式。
- SHA-256 唯一匹配优先；ROM stem、Serial、Product Code 或 Title ID 仅作辅助。歧义必须由管理员通过签名预览绑定到具体 Edition。
- 下载先在目标同卷暂存并验证，再原子替换；替换前保留可校验本地备份。
- 网页负责配对、状态、冲突和恢复，不把手工上传存档作为正常旅程。

Device Adapter 有三种形态：Windows/SteamOS 的 Full Agent、Android 的 SAF 受限客户端、掌机 Linux 的 Package + Hook。所有根目录逐项授权，不扫描主目录、整块磁盘或 NAS 其他区域。

## 在线联机

Varkiv 主产品负责展示入口、从 Edition 与设备运行证明解析兼容身份，以及保存不含 ROM 的短时会话状态。对外协议使用独立的 `/api/multiplayer/v1` 版本空间，不暴露 Game、Edition、私有路径或文件名，因此其他客户端可以在不采用 Varkiv 资料库模型的情况下接入。

首个原生 Profile 是 `retroarch-netplay-v1`。加入者必须精确匹配 ROM SHA-256、大小、平台、RetroArch 版本、核心 ID 与核心版本；不允许用标题、文件名或“同一游戏”推断兼容。会话邀请是短时能力令牌，只返回一次且服务端只保存摘要。该 Profile 当前只交付协调和拒绝语义，不提供公网中继、不自动启动模拟器，也不宣称原生模拟器已经完成真实联机。

另有隔离的 `emulatorjs-webrtc-v1` 实验：只对精确匹配的 NES Edition 签发两人播放器，房主向客端传输画面/声音，客端输入经 WebRTC 数据通道回到房主；实验固定 EmulatorJS、FCEUmm 和资源哈希，并使用 `no-persist` 存档策略。它已经具备双浏览器自动验收，但不等于公网、其他平台或原生模拟器联机。详见 [WEB_NETPLAY.md](WEB_NETPLAY.md)。

## Web 管理与浏览器模拟

一级任务区为：游戏库、平台、来源、整合包、同步、设置。默认使用稳定的管理列表；封面墙只作为适合浏览的可选视图。Series、Game、Edition 和 Device 详情各自遵守领域边界。

Web Player 是可选能力，只开放资源和核心经过验证的轻量平台。路由健康或玩家外壳出现不等于游戏已经启动；必须分别验证资源、核心、ROM 读取、画面和存档。PS2、3DS 等保持 native-only。详见 [WEB_EMULATION.md](WEB_EMULATION.md)。

视觉与文案遵循 [UI_LANGUAGE.md](UI_LANGUAGE.md)：硬朗漫画编辑感、稳定栅格、统一图标、可读字号、长说明按需展开，操作风险不能隐藏。外观支持浅色、深色与跟随系统；选择只保存在当前浏览器，跟随系统时需实时响应系统变化，所有主题使用同一信息层级并保证正文、次要文字、表单和状态色可读。

## 安全、隐私与恢复

- 默认监听回环地址；局域网访问强制 Bearer Token，设备使用可撤销的最小权限 token。
- 日志、状态和验收报告不得泄露 token、ROM/存档文件名、宿主绝对路径或外部标识原值。
- 文件操作只在显式根目录内进行，不跟随越界链接，不隐式删除用户文件。
- 数据库、受管 ROM、媒体、存档、包和恢复区进入同一带逐文件哈希的状态备份；恢复不覆盖已有目标。
- 清理仅作用于程序拥有的临时目录或经签名预览确认的隔离对象。

完整边界见 [PRIVACY.md](PRIVACY.md)和[DEPLOYMENT.md](DEPLOYMENT.md)。

## 支持证据

| 等级 | 含义 |
|---|---|
| Catalogued | 有版本化平台、设备、驱动和 core 契约 |
| Package-tested | 小型夹具可生成并校验目标目录 |
| Emulator-tested | 指定模拟器/系统组合完成可重复软件运行 |
| Hardware-tested | 真实设备完成导入、启动、重启和增量更新 |
| Sync-tested | 真实设备完成双向同步、离线恢复、冲突和升级 |

低等级不自动推导高等级。当前软件实现不等于所有目标已完成真机验收；正式证据和命令见 [ACCEPTANCE.md](ACCEPTANCE.md)。
