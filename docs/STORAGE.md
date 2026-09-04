# ROM 与媒体存储设计

## 目标

Varkiv 同时服务两类资料库：已经整理好的 NAS/掌机整合包，以及希望由服务统一保管的新导入内容。单一“上传后移动进固定目录”的策略不适合两者，因此 ROM 与媒体分别选择存储策略。

## 三个物理区域

```text
library/                       用户现有资料库，可只读挂载
state/roms/{platform}/{safe-edition-id}/
                               Varkiv 受管 ROM 副本
state/media/blobs/sha256/{prefix}/{sha256}.{ext}
                               内容寻址的媒体原件
state/media/cache/             按内容寻址的缩略图与后续转码，可随时重建
state/recovery/packages/{slug}/release-*/
                               整合包更新前的精确受管文件快照，不进入导出包，不自动清理
state/recovery/managed-storage/{operation-id}/
                               未关联受管文件与 0600 恢复清单，不进入导出包，不自动清理
```

SQLite 只保存关系、相对路径、大小、哈希和来源，不保存大文件。数据库、`state/roms`、`state/media`、`state/saves`、`state/exports` 和仍需保留的 `state/recovery` 必须作为一个一致备份集合；使用 `backup-state`、`check-state` 与 `restore-state` 可生成逐文件 SHA-256 清单、核对数据库引用，并只恢复到全新根。外部 `library` 的引用型 ROM 不在该集合内，仍需单独备份。旧版可能留下的 `state/exports/<slug>/.varkiv-backups` 不会被自动移动或删除；升级后应由管理者离线审查。

## ROM 导入策略

Web 导入有两条彼此独立的入口：直接扫描 ROM 文件/目录，或读取 Pegasus、ES-DE 元数据。浏览器不上传 NAS 文件路径；宿主目录先只读挂载到容器 `/library`，界面只接受这个资料库根目录内的相对路径。

来源配置只保存相对于 `/library` 的路径和导入策略。`SourceScan` 保存计数、状态、过期时间与预览令牌摘要，不保存 ROM 内容、候选快照或明文令牌。停用来源只停止后续扫描；删除无历史配置也只删除数据库关系，不删除、移动或重命名源目录中的任何文件。

当一次扫描的 ROM 全部缺失时，保存来源与导入游戏是两个独立结果：`LibrarySource` 和只含聚合计数的 `SourceScan` 可以保留，以便文件到位后重扫；Game、Edition、Artifact 和媒体关系仍保持零写入。界面不会把“来源已保存”表述为“导入成功”。

### `reference`：保持原位

- 默认用于现有 Pegasus、ES-DE 和 NAS 整合包。
- Artifact 保存相对于 `library` 的路径，原始文件不复制、不移动、不重命名。
- 几乎不增加空间占用，并保持已有前端目录可直接使用。
- NAS 离线或路径改变时 Artifact 标记 missing，但游戏、版本和存档关系仍保留。

### `copy`：受管复制

- 适合零散导入、希望脱离来源整合包的文件。
- 复制到 `state/roms/{platform}/{safe-edition-id}/`，保留多碟、CUE/BIN 和目录型游戏的相对布局。安全目录名保留正常 slug/UUID；含分隔符或其他非法字符时使用规范化名称加短哈希，避免越界和碰撞。
- 文件与目录使用统一内容身份；目录哈希按可移植相对路径和精确文件字节顺序计算，因此 `reference`、`copy` 与 v5/v6 恢复不会改变同一目录型游戏的 SHA-256。
- 先写临时文件、计算 SHA-256、`fsync`，再原子重命名；任一文件失败会回滚本次 Edition 的受管目录。
- Game、Edition、Artifact 与 MediaAsset 在单一 SQLite 事务中提交；任一元数据行失败不会留下半导入记录。若事务未提交，刚复制的 Edition 目录会清理；若提交后的响应读取失败则保留文件，避免数据库悬空。
- 不删除来源。真正的 move 需要可恢复操作日志和二次确认，不在当前 API 伪装成普通 copy。

### 元数据引用缺失 ROM

- 预览阶段逐个验证元数据引用，分别显示已确认、部分缺失和全部缺失，不把路径字符串冒充为可用文件。
- 缺失项只进入预览报告并跳过，不创建 Game、Edition 或 Artifact；文件到位后重新扫描。
- 勾选保存来源时，即使全部缺失也只保留可重扫配置和聚合扫描历史；不勾选则连来源配置也不保留。
- `copy` 模式拒绝缺失 ROM，避免产生看似受管、实际空壳的目录。
- 已经正常入库的 ROM 之后离线时，Artifact 才会由“重新检查文件”标记为缺失；恢复原路径后再次检查即可清除。需要改路径或出现多个同名候选时保持人工确认，不按文件名静默绑定。

### 手工版本与首个 ROM

- `artifact_path` 留空时明确创建 metadata-only Edition，便于先整理尚未到位的资料；界面不会把它标成已有 ROM。
- 一旦填写路径，该文件或目录必须已存在于资料库根内并可计算 SHA-256。服务先完成路径约束和哈希，再在单一 SQLite 事务中写入 Edition、标题、主版本指针和首个 Artifact。
- 缺失、越界、不可读取或内容哈希冲突都会在任何记录写入前或事务内回滚；错误响应使用稳定代码，且不包含宿主绝对路径。

Artifact 的 `storage_kind` 明确区分 `library` 与 `managed`；`source_path` 记录来源，以便重复检测和审计。删除 Artifact/Edition 只解除资料关系，不在同一个请求内碰文件。设置页的受管存储维护会重新读取完整 Artifact/MediaAsset 引用集合，只把没有任何引用的原件普通文件列入签名预览；`state/media/cache` 和上传 `.staging` 明确排除。明确勾选并确认后才移入恢复区。

## 媒体策略

媒体不是 ROM Artifact。封面、截图、Logo、标题画面、视频、手册等使用独立 `MediaAsset`：

- 可挂在 Game：多个原版/汉化/改版共享的封面、Logo。
- 可挂在 Edition：汉化版专属封面、改版截图或手册。
- `copy` 为默认：流式计算 SHA-256，按内容寻址保存；相同内容只保留一个 blob，多条关系共享。
- `reference`：保留 ES-DE/Pegasus 原媒体路径，适合完全可移动的现有整合包。
- `ignore`：只导入游戏关系，不导入媒体。
- `source_type` 区分 `upload`、`frontend-import` 和未来的 `provider`。用户上传资产不可假设能够重新抓取；provider 资源以后可单独标记为可重建。

缩略图、WebP/AVIF 和视频标准化文件不进入 `MediaAsset` 原件表，而进入可删除 cache。当前 `thumbnails-v1/<sha256-prefix>/<sha256>/<size>.png` 实现固定 128/256/480/768 px 桶，目录为 0700、文件为 0600，以同目录临时文件完成写入后发布。读取缓存前仍先重新验证原件大小与 SHA-256；无身份、缺失、漂移或不安全的原件不会返回旧缓存。这样 UI 优化不会改变原文件，也不会让导出依赖某种 Web 专用格式。

## 整合包的 reference 模式

导入来源的 `reference` 表示服务器资料库关系继续指向外部 `/library`；整合包 `file_mode=reference` 则是不同边界：它生成可覆盖到目标设备根目录的元数据包，不复制内容。所有 ROM、媒体、启动和配置路径均按目标根相对布局表达，不复用服务器绝对路径。中性 manifest 仍携带已知大小与 SHA-256，便于内容到位后复核。

reference 构建不会把这些外部目标记录为“由 Varkiv 管理”。如果用户随后在输出根放入 ROM，再把配置改成 copy/hardlink，计划会把已有 ROM 视为未受管文件并拒绝覆盖；只有前端元数据、配置、启动清单和 package manifest 等本次实际生成的文件进入受管路径集合。

不论 copy、hardlink 还是 reference，构建计划都会先把当前普通文件重新计算 SHA-256，并对目录型游戏使用规范的相对路径 + 内容树哈希；只要数据库中存在合法的已记录指纹且当前内容不再匹配，就以冲突停止整批构建。用户必须在版本/媒体整理界面显式重新检查并接受新身份，不能让已替换字节沿用旧 ROM 或媒体身份进入整合包。

## 安全与恢复

- 完整状态备份只读取显式 `--db` 与 `--state`，不遍历外部资料库；输出必须位于独立且尚不存在的目录。备份和恢复均拒绝符号链接、特殊文件、清单外文件、哈希/权限漂移及已有目标。
- 完整状态备份保留数据库引用的媒体原件，但排除可重建的 `state/media/cache` 和未提交的 `state/media/.staging`；恢复后缩略图在首次请求时按已验证原件重建。
- 备份数据库会把 `save_files.blob_path` 转成 `state/saves/blobs/...` 可移植定位；恢复复制到新根后在事务内改写为该新根的绝对路径，再验证每个 blob 的大小与 SHA-256。原数据库不被改写。
- 完成前会重新哈希来源状态，发现新增、消失或变化即整次失败；成功清单还会语义校验受管 ROM/目录、媒体、存档和 package recovery。备份清单含相对文件名，整个备份目录属于私密数据。
- 所有数据库路径都是相对受控根目录的干净路径；读取来源和写入受管 ROM/媒体时逐级拒绝符号链接父目录，并复核已存在目标的真实路径仍在授权根内。
- 上传上限 64 MiB；当前接受常见图片、音频、视频和 PDF，拒绝 SVG/HTML 主动内容。
- 媒体内容响应使用 `nosniff`、SHA-256 ETag 和 immutable 私有缓存。
- 缩略图响应使用源 SHA-256、缓存合同版本和尺寸组成 ETag；即使 `If-None-Match` 命中，也先验证原件再返回 304。服务只解码受限 PNG/JPEG/GIF；UI 仅允许 WebP、AVIF、BMP 和 icon 在明确的 415 后回退到已验证原件，像素上限错误不回退到大图。Pegasus/ES-DE 引用来源里的 SVG 原件仍可往返导出，但界面只显示被动占位，不请求其原件用于渲染。
- Web 预览通过临时 `blob:` object URL 展示已鉴权取得的图片，并在重绘时撤销 URL；CSP 只在 `img-src` 放行 `blob:`，脚本、连接、对象和框架策略不放宽。
- 删除媒体关系不立即删除 blob。受管存储维护先以数据库引用作 mark，再扫描 `state/roms` 与 `state/media`；提交时在 SQLite `BEGIN IMMEDIATE` 锁内重新执行 mark、验证完整候选签名和逐文件身份，关闭“预览后刚被重新引用却仍被移动”的竞态。
- sweep 只使用同一 `state` 文件系统内的 rename，把选中普通文件移到随机 operation 恢复区；首个 move 前先 `fsync` 权限为 0600 的私密清单。任一项变化或移动失败会逆序回滚，符号链接、特殊文件会让扫描停止；媒体 `.staging` 与可重建 `cache` 被排除，不会跟随或越界。
- 恢复先检查整批 payload 和原目标：任一目标已被占用或恢复文件变化时全批停止、零覆盖；重复恢复是幂等的。列表 API 只返回 operation ID、状态、数量、容量和时间，不返回原文件名或路径；原相对路径只存在于本机私密恢复清单。
- 隔离不会立即释放磁盘空间，当前也没有自动或定时永久删除入口。恢复区随完整状态备份保存并已通过“备份 → 全新根恢复 → 再恢复隔离文件”测试；未来若加入 purge，必须使用独立预览、保留期和明确确认，不能复用隔离按钮。
- 导入预览绝不写文件；只有明确勾选并提交后才执行 copy。
- 预览快照和候选都使用服务端 HMAC 签名；提交会重新扫描，来源、哈希、顺序、状态或策略漂移时整批返回 `409 import_preview_stale`。签名密钥只存在于当前服务进程，重启后必须重新预览。
- 选中候选按全有或全无语义提交：任一项冲突或失败时数据库零写入，并清理本批次已创建的受管 ROM 目录；不返回部分成功。
- 数据库事务不能直接覆盖文件系统，因此受管导入采用“原子完成文件，再提交引用”的顺序；崩溃留下的无引用普通文件可由受管存储维护发现。清理恢复清单先于任何 move 持久化，服务中断后仍能从 `prepared` operation 恢复，而不会靠猜测文件名删除内容。
- 整合包更新是独立的文件事务边界：先快照将改写的既有受管目标，失败时恢复旧文件并只删除本次新建的精确目标。输出父目录符号链接与保留目录冲突会在写入前拒绝；成功快照永久保留，清理必须另行设计审查与确认。

## 与 RomM 的取舍

RomM 推荐把 ROM library、可重建 resources 和用户 assets 分开，这是正确的恢复边界；Varkiv 沿用这个原则。不同之处是媒体 blob 以内容哈希去重，并允许 Game 级共享，避免同一游戏的原版、汉化版和改版各复制一套封面。

参考：

- [RomM Folder Structure](https://docs.romm.app/latest/getting-started/folder-structure/)
- [RomM Architecture](https://docs.romm.app/latest/developers/architecture/)
- [Pegasus Asset Files](https://pegasus-frontend.org/docs/user-guide/meta-assets/)
