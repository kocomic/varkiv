# 平台预设

## 设计目标

平台注册表解决“同一个平台在目录、前端和用户口语中名称不同”的问题。例如：

```text
红白机 / Famicom / FC / NES  →  规范 ID: nes
Mega Drive / Genesis / MD     →  规范 ID: megadrive
PlayStation / PS1 / PSX       →  规范 ID: psx
Nintendo 3DS / N3DS           →  规范 ID: 3ds
```

平台只描述游戏系统，不等于模拟器，也不绑定设备。实际启动由已经实现的 `DeviceProfile + EmulatorDriver + LaunchBinding` 分层解析决定；是否能在某台真机启动仍以该设备证据为准。

## 内置范围

当前注册表内置 72 个常用平台，范围以 Switch 世代为上限，覆盖：

- Nintendo：NES、Famicom Disk System、SNES、N64、64DD、GameCube、Wii/WiiWare、Wii U、Switch、GB、GBC、GBA、NDS、3DS、Virtual Boy、Game & Watch、Pokémon Mini。
- Sony：PlayStation、PS2、PS3、PSP、PS Vita。
- Microsoft：Xbox、Xbox 360。
- Sega 与街机：SG-1000、Master System、Mega Drive/Genesis、Mega-CD/Sega CD、32X、Game Gear、Saturn、Dreamcast、NAOMI、Atomiswave 和通用街机。
- 经典主机与掌机：Atari、NEC、SNK、Bandai、3DO、ColecoVision、Intellivision、Vectrex、CD-i 和 PICO-8。
- 经典电脑：Apple II、Amiga、Commodore 64、Amstrad CPC、ZX Spectrum、MSX/MSX2、Atari 8-bit/ST、PC-88、PC-98、X68000、FM Towns、DOS 和 ScummVM。

PS4、PS5、Xbox One 和 Xbox Series 不在内置范围内，但仍可用自定义平台表达。这里的“最高到 Switch”是默认目录边界，不是阻止用户管理更晚平台的数据库限制。

每个预设包含：

- 规范平台 ID、中文/英文显示名、厂商和类别。
- 常见别名。
- ROM、光盘、播放列表或目录型格式。
- ES-DE 系统目录映射。
- BIOS 要求。
- `web`、`web_experimental` 或 `native` 运行边界。
- Windows、Android 和掌机 Linux 的模拟器建议。

源数据位于 [platforms.json](../internal/platforms/platforms.json)，加载和别名解析位于 [registry.go](../internal/platforms/registry.go)。

## 平台视觉系统

平台页不加载外部商标 Logo 或图标字体。内置原创 SVG 设备轮廓覆盖卡带主机、光盘主机、传统掌机、双屏掌机、混合掌机、街机柜和电脑；平台缩写与厂商主题色进一步区分具体系统。所有图形随应用离线提供，颜色使用 CSP 允许的白名单 CSS 类，不需要开放内联样式。

### 排版标尺

平台页使用面向游戏库而非监控后台的字号层级：桌面页标题最高 58 px、平台标题 22 px、基础正文 14 px、导航与操作 13–15 px、说明 12 px，最低短标签和技术标识 11 px；390 px 窄屏只收敛标题和空间，不再缩小阅读字号。移动端把三端模拟器建议折叠为可展开详情，不靠微型文字塞入所有信息。

这套策略参考了 [RomM](https://demo.romm.app/) 的封面优先和约 11–14.5 px 应用信息层级、[Playnite](https://api.playnite.link/docs/manual/features/themesSupport/themesSupportOverview.html) 将桌面与全屏主题分开的做法，以及 [ES-DE 主题规范](https://gitlab.com/es-de/emulationstation-de/-/blob/master/THEMES.md) 对 medium/large 等可选字号档和宽高比适配的支持。参考的是层级与响应式原则，不复制其品牌资产或具体主题。

## 运行边界

- `web`：可以作为后续 EmulatorJS/浏览器核心候选，但仍需逐核心验证存档格式。
- `web_experimental`：浏览器实现、性能或兼容性不足，不作为稳定承诺。
- `native`：只交给设备上的本机模拟器。PS2、PS3、3DS、GameCube、Wii、Wii U、Switch、Xbox 系列、经典电脑和未完成浏览器验证的扩展系统不会伪装成 Web 可运行。

这三个值描述生态与产品方向，不是当前部署的核心开关。平台页从 `/api/v1/web-emulation/readiness` 的 `platform_capabilities[]` 读取服务端实际支持的平台、精确 core 和该平台自己的 ROM 扩展名，分别显示“浏览器可运行”“浏览器未启用”“仅外部 Web 方案”或“需要本机模拟器”；游戏详情也使用同一清单决定是否显示运行入口。这样新增或移除固定核心时不会让平台注册表、文件判断和界面状态各自漂移，也不会因 `.bin` 等跨平台后缀产生假阳性。当前 N64 已由固定 Mupen64Plus-Next 和公开测试 ROM 完成真浏览器渲染，但没有扩大为持久存档兼容声明。

模拟器列表是建议，不是强制绑定。例如 PS2 可以在 Windows/掌机 Linux 使用 PCSX2，在 Android 使用独立模拟器；不同设备的存档驱动仍需分别配置。

平台注册表和运行目录会做反向一致性校验：72 个内置平台都至少映射到一个声明式 EmulatorDriver。RetroArch 目录使用 60 个规范平台 ID，每个都有兼容的默认 core；Xbox、Xbox 360、Jaguar CD 和 FM Towns 使用保守的本机驱动合同，存档位置不明确时必须人工绑定，不会猜测共享目录。`32x`、`mame`、`fbneo`、`wiiware` 等可以作为来源别名或集合目录，但不形成第二套运行身份。

Switch 只声明经资料核对的 Eden Windows 与 SteamOS/Bazzite `-g` 启动合同，以及用户明确选择、按 16 位十六进制 Title ID 分目录的存档根。缺少或格式错误的标识会在 SaveStream/绑定写入前原子拒绝，不会退化到父目录；这不代表 Android、网页运行、密钥、固件或真机可用。

## UI 和 API

- 左侧“平台注册表”页面可搜索并按主机、掌机、街机和电脑筛选。
- 新建游戏和导入流程直接选择内置或已启用的自定义平台；“添加自定义平台”会打开完整配置编辑器，不再把裸 slug 当成已经完成的平台定义。
- 自动发现 Pegasus/ES-DE 元数据时，会用目录名和别名匹配规范平台。
- Pegasus 专题目录会归回硬件平台，例如 `FC hack → nes`、`SFC-MSU1 → snes`、`PS2 hack → ps2`，而不是误建游戏平台。
- 扫描器对已知平台使用对应扩展名，并把别名归一化为规范 ID。

```http
GET /api/platforms
GET /api/platforms/ps1
GET /api/custom-platforms
POST /api/custom-platforms
PUT /api/custom-platforms/{id}
DELETE /api/custom-platforms/{id}
```

第二个请求会通过别名解析并返回规范的 `psx` 预设。

命令行：

```bash
varkiv platforms
varkiv platforms --db ./data/library.db   # 同时列出已启用自定义平台
```

## 自定义平台

schema v17 起，自定义平台是 SQLite 中的持久领域对象，不再是状态目录中的临时覆盖文件。UI 和 API 可以配置：

- 不可变的规范 ID、显示名、中文名、厂商与类别。
- 导入目录、Pegasus、ES-DE 和设备清单可识别的平台别名。
- 点号开头的 ROM 扩展名，以及用于 PS3/ScummVM 等目录型游戏的 `directory` 声明。
- ES-DE 系统目录映射；导出使用第一个名称，其他名称也参与来源识别。
- BIOS 要求、Web/本机运行边界，以及 Windows、Android、掌机 Linux 的模拟器建议。
- 独立的启用状态。停用不会删除定义或已有游戏；它只退出当前扫描、导入选择、导出映射和设备同步平台注册表。

启用时，组合注册表会拒绝与任一内置或自定义平台的 ID、别名、ES-DE 名称冲突。被 Game、来源、核心映射或同步清单引用的平台不能删除，只能先停用；内置 45 项保持不可修改。旧数据库中已经存在但尚未登记的 slug 仍原样显示，用户可在设置中补齐定义，不会发生静默重命名。

自定义定义会贯穿直接 ROM 扫描、Pegasus/ES-DE 规范化、ES-DE 整合包目录、移动客户端的扩展名过滤和同步清单。`db-check` 同时验证 JSON 字段、字段约束及组合注册表无冲突。

### 整合包可移植性

`library-manifest.json` v6 只携带当前整合包条目实际引用的自定义平台，不导出未使用定义，也不携带本机启停状态和时间戳。导入预览会验证格式及与目标端活动注册表的所有键冲突，但保持只读；提交才把缺失定义与所选 Game/Edition 放入同一事务。目标端已有同 ID 定义时必须逐字段一致且处于启用状态，系统不会静默覆盖、合并或重新启用本地设置。v4/v5 清单仍可读取，但无法表达这一可移植平台层。

### Android 原生模拟器接管

Android Intent 是按模拟器的真实导出组件逐个声明的合同，不从包名、桌面配置或其他平台命令猜测。Azahar 当前使用导出的 `org.citra.citra_emu.activities.EmulationActivity`、`VIEW`、`content` URI 与只读授权，并同时声明官方 vanilla `org.azahar_emu.azahar` 和 Google Play `io.github.lime3ds.android`；客户端只选择已安装且 Manifest 可见的显式候选包。两个 2126.0 变体已在 API 35 ARM64 AVD 上分别实际打开同一公开 3DSX 夹具。

Dolphin 当前没有任意 ROM URI 的外部 Activity：导出的 App Link 只接收 `dolphinemu://app/play/<channel>/<gameId>`，并要求 Game ID 已存在于 Dolphin 自身缓存。Varkiv 因而不把桌面 `--exec={{rom.path}}` 模板伪装成 Android 合同；未来适配必须先定义可审计、可撤销的预索引/缓存流程，并在真实包上验收。

## 维护依据

ES-DE 的系统名、扩展名和模拟器定义随版本变化。预设以 ES-DE 当前 User Guide 和系统配置概念为基准，但只收录个人掌机管理最常用的子集；发布前应根据目标 ES-DE/掌机系统版本复查，而不是假设所有系统的映射永久不变。
