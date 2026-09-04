# SQLite 数据库说明

## 为什么使用 SQLite

这是个人或家庭规模的单实例服务。SQLite 减少了独立数据库服务、账号和网络配置，同时仍提供事务、外键、WAL、在线一致性快照和完整性检查。程序将连接池限制为一个写连接，并设置：

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
```

SQLite 只保存资料关系、ROM/媒体路径和存档 revision 元数据。大文件不会写入数据库；受管 ROM 位于 `/data/roms`，媒体和存档分别按 SHA-256 存放在 `/data/media` 与 `/data/saves`。

## 状态文件

| 路径 | 内容 | 是否必须备份 |
|---|---|---|
| `/data/library.db` | 游戏、版本、文件关联、设备和存档 revision | 是 |
| `/data/library.db-wal`、`-shm` | 运行期 WAL 文件 | 不应单独复制 |
| `/data/saves/` | 内容寻址的存档 blob | 是 |
| `/data/roms/` | 选择“受管复制”导入的 ROM | 是 |
| `/data/media/` | 内容寻址的封面、截图、视频和手册原件 | 是 |
| `/data/exports/` | 生成的设备整合包 | 可重新生成 |
| `/data/recovery/` | 整合包更新快照，以及未关联受管文件的私密隔离与恢复清单 | 是；隔离文件仍需恢复时必须保留 |
| `/library/` | 外部只读 ROM 资料库 | 使用独立备份策略 |

不要在服务运行时直接复制 `library.db` 单文件；应使用 `varkiv backup`，灾难恢复则使用带清单的 `backup-state` 完整状态备份。

## Schema 版本和迁移

当前 schema 版本为 `27`，保存在 SQLite 的 `PRAGMA user_version` 中：

- `1`：游戏、版本、多语言名称、Artifact 和导入来源记录。
- `2`：设备和存档 revision 历史。
- `3`：Artifact 存储来源字段，以及 Game/Edition 级媒体资产关系。
- `4`：跨平台 Series、系列多语言名称和带关系类型/排序的成员表。
- `5`：非空 Artifact SHA-256 的部分唯一索引，阻止相同 ROM 内容以不同路径重复入库。
- `6`：持久化 LibrarySource 与 SourceScan；仅保存资料库根目录内的相对路径、策略、状态和令牌 SHA-256 摘要，不保存预览明文令牌。
- `7`：持久化 PackageProfile、受限配置模板、带过期时间的 PackagePlan 与不可变 PackageRelease 审计；模板不执行脚本、环境变量、条件或任意函数。
- `8`：持久化 FrontendAdapter、DeviceProfile、EmulatorDriver、RetroArchCore、CoreMapping 与 LaunchBinding；PackageProfile 增加规范设备和前端引用。
- `9`：持久化 SaveStream、SaveBinding、原子多文件 SaveRevision/SaveFile、短码配对摘要、哈希客户端令牌、同步会话/操作与隐私最小化 inventory。
- `10`：持久化惰性的 RuntimeImportHint；保存结构化启动建议或未受信任的原始命令，但不会执行或自动创建 LaunchBinding。
- `11`：LibrarySource 增加显式、资料库根内的 ES-DE runtime metadata 相对路径；来源唯一性和预览签名同时覆盖它。
- `12`：DeviceProfile 与 RetroArchCore 增加版本化内置契约；只协调更高版本的内置项。
- `13`：LibrarySource 首次接受多平台中性 `library-manifest.json` v4，保留为可重扫来源而不伪造单一平台；当前 handler 已升级到 contract v4，可读取 v4/v5/v6。
- `14`：持久化版本化 `SourceAdapter` 契约，并让每个 LibrarySource 显式引用经审计的安全解析 handler。
- `15`：约束 Artifact 角色、碟号、大小与 missing 布尔值；角色统一为 `rom/disc/executable/patch/dlc/update/other`。
- `16`：把 preview 早期遗留的物理 `works/work_id` 原子迁移为 `games/game_id`，并把游戏多语言标题的 owner 类型改为 `game`。
- `17`：持久化自定义平台定义、别名、ROM 扩展名、目录型游戏、ES-DE 目录和目标模拟器建议。
- `18`：为媒体关系记录上次显式文件检查的状态与时间；既有记录升级后只标为 `unverified`，迁移不会访问 NAS、资料库或受管媒体字节。
- `19`：真机/同步证据绑定到运行对象 ID、合同版本和实际 target，并用独立记录保留同一前端、驱动或 core 在多个设备目标上的证据；旧的未绑定高等级证据会安全降级而不是继续冒充当前支持。
- `20`：新增设备级 ROM 歧义确认。确认同时绑定不含路径的客户端条目标识、ROM 身份摘要、平台、最强匹配层级和完整候选集合；任何一项漂移都会让旧确认失效。
- `21`：FrontendAdapter 增加独立 `handler`，把用户可命名的外部格式标识与实际执行的 Pegasus/ES-DE 编译导出器分开；被 PackageProfile 引用后不能漂移。
- `22`：增加精确 SaveCompatibilityGroup、设备 RuntimeAttestation 以及 SaveStream/SaveBinding 的兼容组与 core 引用；跨 Driver 存档共享必须匹配合同版本、OS/架构、大小和 SHA-256。
- `23`：CoreMapping 增加内置所有权；`builtin-*` 命名空间由应用保留，普通 API 与数据库触发器都拒绝自定义 SourceAdapter、FrontendAdapter、DeviceProfile、EmulatorDriver、RetroArchCore 或 CoreMapping 占用未来内置 ID。
- `24`：PackageProfile 增加内置所有权；应用预设可直接生成计划和整合包，但普通 API 不能原地改写或删除，定制内容持久化为独立自定义 Profile。
- `25`：七张运行目录表把内置所有权固化为数据库不变量，拒绝直接 SQL 降级、提升、重命名或占用 `builtin-*` 保留 ID。
- `26`：把早期中性清单的内部命名原子迁移为 Varkiv 技术标识，保留来源配置和扫描历史。
- `27`：新增独立的 ROM 参考识别库，以来源、不可变发布和 SHA-256 身份保存 `.hashpack` 资料，不创建本地 Artifact。

迁移按版本顺序在单个事务内执行。启动新版本时自动升级；数据库版本高于程序支持版本时拒绝启动。项目目前没有自动降级迁移，回滚程序前必须恢复与旧版本匹配的备份。

v4→v5 在创建唯一索引前会检查重复的非空 SHA-256。发现旧数据冲突时，启动会明确报告重复哈希及数量，整个迁移回滚且 `user_version` 保持为 `4`；不要直接删除记录，应先备份并在副本中确认哪些 Artifact 需要合并或修正。自动化夹具同时验证了干净 v4 升级和重复 v4 拒绝两条路径。

v5→v6 新建 `library_sources` 与 `source_scans`。来源路径必须是资料库根目录内的可移植相对路径；删除来源配置不会接触源文件，有扫描历史的来源必须停用而不能硬删，以保留审计记录。自动化夹具验证了 v5 实库升级、来源生命周期、扫描状态和历史保护。

v6→v7 新建 `package_profiles`、`package_config_templates`、`package_plans` 与 `package_releases`。计划绑定 Profile 与资料库指纹并在 30 分钟后过期；构建前必须重算。构建结果与计划终态在同一事务内记录，有历史的 Profile 只能停用。导出目录的 manifest v3 保存每个受管输出的 SHA-256；文件被用户修改后将产生冲突，不会静默覆盖。

v7→v8 新建 `frontend_adapters`、`device_profiles`、`emulator_drivers`、`retroarch_cores`、`core_mappings` 与 `launch_bindings`，并给 `package_profiles` 增加两个规范引用列。迁移不猜测或写入宿主可执行路径；服务启动后按旧 target/frontend 幂等回填内置引用。自动化夹具验证已有 Profile、模板、Plan 与 Release 历史不变、重复种子不增行且自定义对象不被覆盖。

v8→v9 新建 `save_streams`、`save_stream_editions`、`save_files`、`save_bindings`、`pairing_codes`、`client_tokens`、`sync_sessions`、`sync_operations` 与 `inventory_items`。旧单文件 revision 会映射到稳定的 game scope stream 和一个 SaveFile，不读取或重写原 blob；迁移在单一事务内验证引用，失败时 `user_version` 保持为 8。配对码和设备 token 只保存 SHA-256 摘要。

v9→v10 新建 `runtime_import_hints`，把来源中的声明式启动绑定与原始前端命令保存为待审核记录。原始命令字段没有执行入口，只有用户提交新的白名单 Driver/Core/argv 后才能产生 LaunchBinding。

v10→v11 为 `library_sources` 增加 `runtime_metadata_path` 并重建来源身份索引。迁移会先检查列是否已经存在，兼容保留列的降级测试夹具；旧来源保持空值和原扫描历史。

v11→v12 为 `device_profiles` 与 `retroarch_cores` 增加 `contract_version`。内置契约只有版本严格上升时才由种子协调更新；用户自定义设备与核心不会被启动时的内置目录覆盖。迁移对保留新列但降低 `user_version` 的恢复/测试数据库保持幂等。

v12→v13 在单一事务内重建 `library_sources` 与 `source_scans` 的类型约束，逐列复制既有来源、扫描令牌摘要、状态、计数、错误和时间戳，再加入 `varkiv` 类型。中性来源不要求伪造平台，其每个条目仍以 manifest 内的平台为准；迁移测试验证既有扫描历史不变。

v13→v14 新建 `source_adapters`，内置 Direct ROM、Pegasus、ES-DE 和 Varkiv 中性清单四个 handler，并按来源 kind 为既有 `library_sources` 回填外键。当前 Varkiv handler 为 contract v4：v6 在 v5 资源完整性语义上增加可移植自定义平台定义，仍读取 v4/v5。自定义适配器只能特化这些已编译、可审计的 handler 及能力开关，不加载代码、shell 或任意文件系统扩展。迁移夹具验证既有来源和扫描历史保持不变，且重复运行幂等。

v14→v15 先把大小写和两端空白不同但语义有效的 Artifact 角色规范化，再建立数据库级 insert/update guard。若旧库包含未知角色、负碟号、超过 64 的碟号、负大小或非布尔 missing 值，迁移在事务内拒绝并保持 `user_version=14`；不会猜测或删除资源。Store/API 同时执行相同校验。

v15→v16 在单一事务内把 `works` 改名为 `games`，把 Edition、Game 级媒体和 Series 成员外键统一为 `game_id`，重建游戏多语言标题约束，并重建对应索引。迁移不读取 ROM、媒体或存档文件，也不改变任何内容寻址 blob。自动化夹具验证 Game/Edition/Media/Series/多语言标题全部保留、旧物理名消失、外键和新 owner 类型继续受数据库约束；若检测到新旧表名并存的矛盾状态，迁移拒绝并保持 `user_version=15` 和原记录不变。

v16→v17 新增 `custom_platforms`，保存别名、扩展名、目录型 ROM、ES-DE 系统名、BIOS/运行边界、三端模拟器建议与启用状态。迁移只新增表，不读取或移动 ROM、媒体和存档，也不修改现有 Game 的平台字符串。自动化夹具从精确 `user_version=16` 升级，证明既有 Game 保留且新表可写；运行时组合注册表继续拒绝内置/自定义标识冲突。

v17→v18 为 `media_assets` 增加 `content_status` 与 `content_checked_at`，并建立状态索引。迁移不会探测文件系统，所有既有关系统一从 `unverified` 开始；上传或导入时已经完成身份验证的新媒体可直接记为 `available`。只有显式媒体重检才会更新为 `available`、`missing`、`changed`、`unsafe` 或 `unverified`，批量状态写入在单一事务内完成，不删除、移动、修复或重绑任何文件。无论持久状态为何，下载仍会重新验证路径、普通文件类型、大小和 SHA-256。

v18→v19 新建 `runtime_evidence_claims`。新证据以 `runtime_kind + runtime_id + target` 保存，并记录提交时的 `contract_version`；同一个 Pegasus、ES-DE、RetroArch 驱动或 core 可分别保留 Windows、Android、SteamOS 与掌机 Linux 的证据，不再互相覆盖或把一个平台的结果误认为全平台结果。迁移不探测设备、不读取 ROM/媒体/存档或绝对路径；无法证明绑定关系的旧 `hardware-tested/sync-tested` 记录保留为带 `stale_reason` 的历史摘要，并把内置对象降为 `package-tested`、自定义对象降为 `catalogued`，等待重新验收。

v19→v20 新建 `inventory_match_overrides`。表只保存设备 ID、Agent 生成的不透明客户端条目标识、平台、ROM 身份摘要、候选集合摘要、所选 Edition、匹配层级和来源 inventory 审计引用；客户端条目标识、身份摘要和候选摘要不进入公开 JSON。迁移只改变 SQLite 结构，不打开或散列 ROM、媒体、存档与 NAS 文件；自动化迁移夹具验证既有同步会话和 inventory 记录保持不变。

v20→v21 为 `frontend_adapters` 增加受约束的 `handler`。`format=pegasus` 与 `format=es-de` 的精确既有定义分别确定性回填同名 handler；其他自定义格式保持原记录和启停状态，但 handler 留空，避免依据名称猜测执行行为。留空项仍可作为目录元数据查看，只有维护者显式选择 `pegasus` 或 `es-de` 后才能被 PackageProfile 使用。数据库触发器与 Store 双层阻止禁用、错配或被引用后的 handler 漂移；迁移不创建输出目录、不打开 ROM/媒体/存档，也不修改历史 PackageProfile。

v21→v22 新建精确存档兼容组、兼容成员与设备运行时验真表，并为 SaveStream/SaveBinding 增加兼容组和 core 引用。迁移不扫描设备或运行文件；只有后续 Agent 对服务端明确请求的运行对象提交精确合同版本、大小和 SHA-256 后，跨 Driver 绑定才可同步。

v22→v23 为 `core_mappings` 增加 `builtin` 所有权。已有 `builtin-*` 映射只标记为应用所有，不改变 scope、平台、core、优先级或说明；其他映射保持自定义且仍可编辑。Store 和数据库插入触发器同时保留 `builtin-*` 命名空间，避免未来新增内置 Adapter、Device、Driver、Core 或 Mapping 时被旧自定义 ID 占位。迁移只修改 SQLite 目录元数据，不读取 ROM、媒体、存档、NAS 或设备文件；自动化从精确 v22 副本验证内置映射变为只读、自定义映射仍可编辑、所有保留 ID 创建均稳定拒绝。

v23→v24 为 `package_profiles` 增加 `builtin` 所有权。已有 `builtin-*` Profile 原样保留名称、目标、前端、语言、文件策略、输出 slug、模板、历史 Plan 与 Release，只标记为应用所有并转为 API 只读；非保留 ID 继续是可编辑、可删除或按历史规则停用的自定义 Profile。普通 API 与数据库插入触发器拒绝新建伪内置 Profile，便携包只有在目标库存在同 ID、真实内置且逐字段一致的 Profile 时才能复用。迁移不创建、修改或删除整合包输出，也不读取 ROM、媒体、存档或 NAS。

v24→v25 把 `builtin` 所有权从应用层约定提升为七张持久运行目录表的数据库写入不变量：直接 SQL 不能降级内置对象、提升自定义对象、重命名内置 ID，或把自定义 ID 改进保留的 `builtin-*` 命名空间。升级前会在同一迁移事务中检查既有冲突；若 v24 已被绕过触发器写入伪保留 ID，迁移原子失败并保留 schema 24 与原记录，要求维护者人工判断所有权，绝不静默把用户对象提升为应用对象。内置 seed 仍能在不改变 ID/所有权时按合同版本协调字段，自定义对象的普通内容编辑保持可用；迁移不触碰 ROM、媒体、存档、整合包或外部来源。

v25→v26 完成稳定版前的 Varkiv 技术命名迁移。迁移在单一事务中重建 `source_adapters`、`library_sources` 与 `source_scans`：前三种公开 handler 保持不变，第四种内置中性来源确定性归一为 `varkiv` 与 `builtin-source-varkiv`；来源配置、扫描历史、候选计数、令牌摘要、错误状态和时间保持原值。测试夹具从不含旧品牌字面量的 v25 中性占位命名升级，验证迁移结果不残留占位值。迁移不读取、移动、散列或删除 ROM、媒体、存档和外部来源文件。

v26→v27 新建 `hash_sources`、`hash_releases` 和 `hash_identities`。发布以 `source_id + version` 唯一且内容不可变，每个来源只有一个活动发布；新发布会保留旧发布审计行。Identity 只保存可分享的哈希与版本元数据，不保存 ROM 字节、文件名、路径、存档、媒体、设备或游玩记录，也不与 Game/Edition/Artifact 建立外键。迁移只新增空表与索引，不读取 ROM 或修改既有资料库记录；自动化夹具从精确 v26 升级并验证旧数据不变。

## 主要关系

```text
series n ─── n games 1 ─── n editions 1 ─── n artifacts
  │              │                │
  │              │                └──── n save_bindings n ─── 1 save_streams
  │              │
  │              └──── localized_titles（owner_type = game）
  └──── series_titles

games 1 ─── n media_assets
editions 1 ─── n media_assets

editions ─── localized_titles（owner_type = edition）
editions ─── source_records

source_adapters 1 ─── n library_sources 1 ─── n source_scans
package_profiles 1 ─── n package_plans 1 ─── n package_releases
package_profiles 1 ─── n package_config_templates

frontend_adapters 1 ─── n device_profiles
device_profiles 1 ─── n launch_bindings n ─── 1 emulator_drivers
retroarch_cores 1 ─── n core_mappings
editions 1 ─── n launch_bindings
save_streams 1 ─── n save_revisions 1 ─── n save_files
devices 1 ─── n client_tokens / sync_sessions
sync_sessions 1 ─── n sync_operations
devices 1 ─── n inventory_match_overrides n ─── 1 editions
hash_sources 1 ─── n hash_releases 1 ─── n hash_identities
```

- `series`：仅用于跨平台浏览、关系语义和排序；通过 `series_members` 关联 Game。
- `games`：单个平台上的游戏归组；物理表、公开模型和 API 字段现在统一为 `Game/game_id`。
- `editions`：独立可运行版本，拥有稳定 `save_namespace`。
- `artifacts`：ROM、光盘、目录、补丁、DLC 等相对路径及哈希。
- `media_assets`：Game 或 Edition 级媒体关系；受管内容使用 SHA-256 blob 路径。
- `localized_titles`：游戏和版本的 BCP 47 多语言标题。
- `source_records`：Pegasus/ES-DE 原始来源和稳定 ID 映射。
- `source_adapters`：版本化来源能力与安全 handler 合同；用户定义项不能引入新可执行代码。
- `library_sources`：可重复扫描的 ROM 目录、Pegasus、ES-DE 或 Varkiv 中性来源配置；不拥有源文件。
- `source_scans`：一次只读预览的时间、计数、状态、过期时间和令牌摘要；不保存候选或明文令牌。
- `package_profiles`：规范设备/前端引用、语言、文件策略和可移植输出名。
- `package_config_templates`：仅允许白名单占位符与配置文件扩展名的文本模板。
- `package_plans`：带指纹、冲突、到期时间和终态的构建预览。
- `package_releases`：构建成功或失败的不可变审计结果；不保存服务器绝对路径。
- `frontend_adapters`：Pegasus、ES-DE 等前端格式能力；只保存声明式契约。
- `device_profiles`：目标系统、路径风格和目录能力，不代表某台真实设备。
- `emulator_drivers`：平台/设备范围、安全 argv 模板、配置和存档发现声明。
- `retroarch_cores`、`core_mappings`：核心注册项及 Edition > 设备 > 平台 > 全局的分层选择。
- `launch_bindings`：Edition 在设备画像上的 Driver/Core/前端和参数覆盖。
- `devices`：登记的真实设备实例；撤销设备会同时撤销其客户端 token。
- `inventory_match_overrides`：管理员对设备 ROM 歧义候选的确认；只有完全相同的隐私最小化身份和候选集合才能在后续同步复用。
- `save_streams`：game/platform/container 作用域的逻辑存档历史，可由多个 Edition 引用。
- `save_bindings`：把 stream、Edition、设备画像、驱动与白名单本地路径模板连接起来。
- `save_revisions`、`save_files`：不可覆盖的原子文件集合；blob 按 SHA-256 指向 `/data/saves`。
- `pairing_codes`、`client_tokens`：只保存凭证摘要与过期/撤销状态，不保存明文码或 token。
- `sync_sessions`、`sync_operations`：幂等协商、upload/download/conflict/noop 与完成状态审计。
- `hash_sources`：可审计的识别资料来源、许可与当前活动发布。
- `hash_releases`：不可变的 `.hashpack` 发布、整包/records 摘要和导入时间。
- `hash_identities`：按来源发布保存 SHA-256 与版本元数据；不是 Artifact，不证明本地 ROM 存在。

完整 schema 约束以 [migrations.go](../internal/catalog/migrations.go) 为准；各领域写入校验分别位于 `series_store.go`、`library_store.go`、`source_store.go`、`package_store.go`、`device_store.go` 及专用运行/同步 Store 文件。

## 检查

```bash
varkiv db-check --db /data/library.db
```

正常输出：

```text
schema_version=27 supported=27 integrity=ok foreign_keys=ok mode=read-only
```

`db-check` 不迁移或写入数据库；它在 SQLite 物理完整性和 foreign-key 检查通过后，还会通过正常读取边界审计完整运行目录：损坏 JSON、非法支持等级、缺少结构化证据，以及 DeviceProfile 中的绝对/越界/私有路径都会让命令失败，即使 `PRAGMA integrity_check` 本身仍为 `ok`。错误不会回显被拒绝的路径值。没有 WAL sidecar 的离线副本使用 immutable 只读打开，因此可在只读恢复卷上检查且不会创建 sidecar。

若完整性结果不是 `ok`，停止写入，不要直接运行修复 SQL；先保留数据库、WAL、存档目录和最近备份，再在副本上排查。

## 一致性快照

```bash
varkiv backup \
  --db /data/library.db \
  --out /data/backups/library-20260825-120000.db
```

命令使用 SQLite `VACUUM INTO` 创建事务一致、压缩后的新数据库。为防止误覆盖，目标文件已经存在时会失败。

这条命令只备份数据库，不包含 `/data/roms`、`/data/media`、`/data/saves` 和 `/data/recovery`。灾难恢复应使用 [部署文档](DEPLOYMENT.md) 中的 `backup-state` / `check-state` / `restore-state` 完整状态备份。要分阶段验证单个数据库备份，使用 `varkiv restore-db --from <backup> --out <new-db>`；它只创建不存在的新目标，不会替换当前数据库。
