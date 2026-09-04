# 网页模拟器

网页模拟器是可选能力，不是所有平台的替代运行环境。Varkiv 只为已验证的单文件 ROM 创建短时播放会话；PS2、PS3、3DS、GameCube/Wii、Wii U、Vita 等复杂平台继续由设备端原生模拟器运行。

## 资源与许可边界

Varkiv 的仓库和镜像采用 Apache-2.0。EmulatorJS 本身采用 GPL-3.0，因此本仓库和官方镜像不复制、修改或打包 EmulatorJS 的 JavaScript、WASM、核心或品牌资源。服务只实现一个可配置的集成协议。操作者需要单独选择以下方式之一：

- 首选：把固定版本的 EmulatorJS `data/` 目录以只读方式交给 Varkiv，由内置受限文件端点发布为同源 `/emulatorjs/`；
- 或者：由反向代理在同一 Origin 下发布固定资源目录，再把同源路径（例如 `/emulatorjs/`）配置给 Varkiv；
- 明确接受浏览器访问外部资源源后，配置固定 HTTPS 版本目录。

开发预览固定使用 `https://cdn.emulatorjs.org/4.2.3/data/`。这是 EmulatorJS 当前稳定版 4.2.3，而不是 `latest`、`nightly` 或 4.3 预发布版本。生产环境更建议独立保存并审查固定版本资源，再以只读目录交给 Varkiv。目录不会写入数据库、备份或整合包，也不会被 Varkiv 修改。

可以把固定的 32 个资源下载到一个尚不存在的新目录；下载器使用清单中的 HTTPS 版本地址，逐个下载后先完成大小与 SHA-256 校验，全部通过才把 staging 原子改名为目标目录，既有目标永不覆盖：

```bash
npm run --silent fetch:web-emulator-assets -- --directory /absolute/new/path/to/EmulatorJS/4.2.3/data
```

也可以对自行准备的目录单独执行离线身份检查。检查要求根目录和每一级资源都不是符号链接，并在读取前先核对普通文件类型与精确大小，再核对 SHA-256；成功输出不包含宿主目录。它允许目录中存在 EmulatorJS 的其他资源，但不会把未列入锁定清单的文件视为已审核：

```bash
npm run --silent verify:web-emulator-assets -- --directory /absolute/path/to/EmulatorJS/4.2.3/data
```

官方资料：

- [EmulatorJS 版本说明](https://github.com/EmulatorJS/EmulatorJS#versioning-guide)
- [EmulatorJS 4.2.3 发布记录](https://github.com/EmulatorJS/EmulatorJS/releases/tag/v4.2.3)
- [EmulatorJS 选项](https://emulatorjs.org/docs/options/)
- [EmulatorJS 核心列表](https://emulatorjs.org/docs4devs/cores/)

## 启用

本机开发（首选同源目录）：

```sh
go run ./cmd/varkiv serve \
  --addr 127.0.0.1:8080 \
  --db ./data/library.db \
  --state ./data \
  --library ./library \
  --web-emulator-directory /absolute/path/to/EmulatorJS/data
```

也可以用 `--web-emulator-assets https://cdn.emulatorjs.org/4.2.3/data/` 做开发验证，但浏览器会直接访问该第三方地址。

Docker Compose 使用仓库内的 `compose.web-emulator.yaml` 只读挂载，不把 GPL-3.0 资源复制进 Apache-2.0 镜像。先在 `.env` 中填写宿主机绝对路径：

```dotenv
EMULATORJS_DATA_PATH=/absolute/path/to/EmulatorJS/4.2.3/data
```

```sh
docker compose -f compose.yaml -f compose.web-emulator.yaml config --quiet
docker compose -f compose.yaml -f compose.web-emulator.yaml up -d
```

该 overlay 在容器内固定使用 `/opt/emulatorjs`，显式清空外部资源 URL，并把 `EMULATORJS_DATA_PATH` 只读挂载到该位置；缺少变量时 Compose 配置阶段就会失败，宿主目录不存在时 `create_host_path: false` 会拒绝启动，不会悄悄创建一个空目录。`VARKIV_WEB_EMULATOR_DIRECTORY` 与 `VARKIV_WEB_EMULATOR_ASSETS` 互斥；前者会在服务启动前用内置固定清单复核全部 32 个文件、19,102,261 字节及 SHA-256，缺失、变化、符号链接或特殊文件都会阻止服务启动。通过后同源端点只发布清单成员，并在每次响应前重新核验实际字节，不开放目录列表或额外文件。两者都留空会彻底关闭网页运行入口，`/api/v1/capabilities` 同时返回 `web_emulation: false`。

无需令牌的 `/api/v1/web-emulation/readiness` 只报告 `disabled`、`external-unverified` 或 `self-hosted-verified`，以及固定版本、已核验文件数/字节数、当前服务实际支持的平台和扩展名；`platform_capabilities[]` 还把每个平台绑定到精确核心和自己的扩展名集合。它不会返回宿主路径或外部 URL。平台页和游戏详情只消费这份服务端清单来决定运行状态、文件资格和入口，不在浏览器端复制核心名单，也不会把另一平台恰好支持的扩展名误用到当前平台。同源反向代理路径和外部 URL 保留为兼容方式，但服务无法验证其实际响应字节，因此不会冒充完整性已核验。

仓库验收脚本把 32 个固定资源下载到新建的私有临时根并核验身份，不读取 ROM 或用户状态；它验证合并后的 Compose 配置、启动硬门禁、同源资源字节、`self-hosted-verified`、只读挂载和容器/数据卷/网络/端口零残留：

```sh
./scripts/acceptance-compose-web-emulation.sh --port 18084
```

## 首批平台

当前开放以下不强制依赖 BIOS、适合浏览器内存模型的单文件入口。`真实浏览器` 表示固定许可、提交、SHA-256、核心资源、启动事件和实际画面都由 `scripts/acceptance-web-emulation.mjs` 复现；全部 12 个夹具必须通过有界画面探针。稳定终态锁定完整画布 SHA-256；存在周期闪烁的 NES 则锁定跨 96 帧的时间占用 SHA-256，避免把截图时相当成游戏内容。`目录` 只表示已建立隔离合同，不能冒充实际运行通过。

| Varkiv 平台 | EmulatorJS system | 精确 core | 当前证据 |
| --- | --- | --- | --- |
| Atari 2600 | `atari2600` | `stella2014` | 真实浏览器；确定性 4 KiB 项目 ROM 的 TIA 固定背景画面 |
| Game Boy / Game Boy Color | `gb` | `gambatte` | 真实 DMG/CGB 浏览器 + 32 KiB battery 新会话可视恢复 |
| Game Boy Advance | `gba` | `mgba` | 真实浏览器 + 32 KiB SRAM 新会话恢复 |
| Nintendo 64 | `n64` | `mupen64plus_next` | 真实浏览器；固定 Unlicense SaveTest-N64 渲染存档类型检测表，未宣称持久存档兼容 |
| NES / Famicom | `nes` | `fceumm` | 真实浏览器；`HELLO WORLD` 以 96 帧时间占用摘要锁定画面身份 |
| SNES / Super Famicom | `snes` | `snes9x` | 真实浏览器；固定 MIT SPC-700 指令夹具到达 `Success` 测试 `0557`，全画布 SHA-256 精确匹配；2 KiB raw `.srm` 已在固定 Web core、固定 RetroArch/同提交原生 core 与全新 Web 会话间逐字节往返 |
| Mega Drive / Genesis | `segaMD` | `genesis_plus_gx` | 真实浏览器；中央画面区域亮像素探针确认作者自制 smiley sprite；固定核心未产出 SRAM 文件，不宣称存档可用 |
| Master System | `segaMS` | `smsplus` | 真实浏览器；进入 SMSGGDJ 编辑器，键盘映射交互改变 8,155 个画布像素，ROM 内 SAVE 后将 32,768 字节逐字节恢复到全新会话 |
| Game Gear | `segaGG` | `genesis_plus_gx` | 真实浏览器；进入 SMSGGDJ 编辑器，键盘映射交互改变 9,523 个画布像素，ROM 内 SAVE 后将 16,234 字节逐字节恢复到全新会话 |
| Neo Geo Pocket / Color | `ngp` | `mednafen_ngp` | 已锁定 Web/legacy 核心资源和单 ROM ZIP 安全合同；公共许可 ROM 的可重复画面夹具仍待补充，因此暂不标记为 package-tested |

可重复验收要求显式传入操作者资源目录；脚本只在新建的 `0700` 临时根下载清单中的公开夹具、核对许可与 SHA-256、建立临时数据库并启动随机本机端口，结束后停止该服务。第三方 ROM 不写入仓库，也不读取现有资料库：

```sh
VARKIV_WEB_EMULATOR_DIRECTORY=/absolute/path/to/EmulatorJS/4.2.3/data \
  node scripts/acceptance-web-emulation.mjs
```

固定来源、提交、许可、ROM/资源大小和 SHA-256 见 `testdata/web-emulation/fixtures.json`。需要人工复核截图和数据库 revision 时，可额外设置 `VARKIV_WEB_ACCEPTANCE_KEEP=1`；输出根只包含公开测试夹具与新建状态，不可替代个人库备份。

除 NGPC 的受限单 ROM ZIP 外，压缩包、多文件光盘、BIOS、补丁链以及大于 128 MiB 的内容暂不开放。NGPC ZIP 必须只有一个位于根目录的 `.ngp`、`.ngc`、`.ngpc` 或 `.npc` 普通文件，只能使用 Store/Deflate、不得加密，解压后也不得超过 128 MiB；多文件、嵌套路径和异常压缩方法全部拒绝。平台预设中的 `runtime: web` 只是生态候选，不代表当前部署可运行；界面会把当前服务已支持、服务未启用、仅有外部 Web 方案和必须本机模拟器分开显示，不能绕过这里更严格的版本、文件和部署检查。

## 安全模型

1. 管理员 API 先验证 Edition 的启动 Artifact、文件类型、大小和 SHA-256。
2. 服务签发 4 小时有效的随机能力 URL，绑定 Edition、Artifact、SHA-256、精确核心、EmulatorDriver、SaveStream 和界面语言；时长覆盖一次正常游玩，但不是永久链接。
3. 播放器 URL 不包含管理员 Bearer Token、数据库 ID 以外的本机身份、ROM 路径或 NAS 路径。
4. ROM 内容端点支持 HTTP Range，但每次发送前都会重新验证文件大小与 SHA-256；漂移时返回 409，且不返回 ROM 字节。
5. 播放器使用 `no-referrer`、独立 CSP 和同源 frame 限制。外部资源源只在用户点击运行后由浏览器访问，ROM 始终从 Varkiv 的短时同源端点读取。
6. 播放器只向同 Origin 父页面发布固定的准备、核心就绪、启动、运行、超时和资源失败状态；外层界面只接受当前 iframe 的消息。关闭对话框会立即卸载 iframe，不会让不可见核心继续运行。

## 自动存档

网页运行不提供孤立的手工存档管理页面。每次创建会话时，Varkiv 会按 Edition 与精确 EmulatorJS core 解析或建立 `core-dependent` SaveStream；核心启动后自动恢复当前 revision。只有 EmulatorJS 核心实际产出持久存档文件时，周期落盘事件或工具栏保存事件才会写入不可变 revision；没有文件时不会创建空 revision。API 以 `save_support: automatic-when-core-emits` 明确表达这条边界。

播放器持续携带最近一次成功 revision 作为下一次上传基线。另一设备抢先更新时，服务会保留冲突 revision，不会静默覆盖。上传能力与 ROM 能力使用同一个签名会话，既不向 iframe 暴露管理员 Bearer Token，也不能改写其他 Edition 或其他核心的存档流。

EmulatorJS 4.2.3 的浏览器存档仍可能与 Windows、Android 或掌机上的 RetroArch/独立模拟器格式不同。不同核心默认隔离；只有经过往返测试并显式标记兼容后，才允许共享。当前唯一跨运行时例外是下方列出的固定 SNES 组合；它既证明文件格式，也由隔离的 Linux arm64 Device Agent 进程核验精确二进制后解锁同一个共享流。它不代表真实掌机已经通过，也不会把既有的其他 Driver 独立 SaveStream 静默合并。浏览器关闭瞬间无法保证异步网络请求完成，因此核心默认的周期落盘是主要保护，退出前最后几秒仍可能丢失；设备 Agent 的冲突保留规则不变。

自动验收不会把“核心能启动”当作“存档可用”。SMSGGDJ 的 Master System 与 Game Gear 夹具必须先通过浏览器键盘事件和 EmulatorJS `simulateInput` 映射进入 FILES，再按 ROM 自身的双确认 SAVE 流程写入 SRAM；脚本抓取浏览器实际上传字节，在全新浏览器上下文中恢复同一 revision，并要求返回内容与上传内容逐字节相等。当前只证明这两个固定 ROM、固定核心和固定 EmulatorJS 版本的浏览器链路，不推导原生模拟器兼容性。

SNES 也不再只检查“画面不黑”。同一固定 MIT release 中的 SPC-700 指令测试会先额外运行 15 秒，必须停在 `Success` / `0557` 终态，并要求完整画布字节的 SHA-256 精确等于清单值；仍在运行、显示失败或渲染发生漂移都会使验收失败。第二个夹具先核验原 ROM，再执行项目定义且 SHA-256 锁定的 `snes-spctest-sram-handshake-v2` 变换：把未使用的 LoROM 空间写入复位前导，声明 2 KiB battery SRAM；空白 SRAM 首次运行写入 `0x5A`，加载到同一字节后再次运行则在下一字节写入 `0xA5`，随后恢复原始 `$8000` 测试入口。变换 ROM 固定为 131,072 字节、SHA-256 `6dc7830c6db7f89d622f6bb8904e0c3f50131561a4d81bc8a4452c749b1a9358`，二进制只存在于新建临时根，不提交仓库。

`scripts/acceptance-snes-native-compat.sh` 把该夹具扩展为跨运行时门禁：Web 先生成前缀 `5a60`、SHA-256 `48878c969caa13651d00cf0cab230da32e5d1fdd0bdf6217489af87a8f40a3d7` 的 2,048 字节存档；随后在无网络、非 root、只读根容器中运行 RetroArch 1.22.2（commit `69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576`）和 Snes9x 1.63（commit `6ca2343e5f3b0acbea49ca958251e3a0af58a81d`），输出前缀 `5aa5`、SHA-256 `17f7c19ea1ad7f71dc8ddcb6b1a5c5af489448febcfc0a57ef43d88f81c6e2d8`。中间的 `acceptance-device-runtime-bridge.sh` 会启动与当前二进制支持 schema 完全一致的独立服务和实际 Linux arm64 Agent 进程：短码绑定 ROCKNIX Profile，只哈希固定镜像中的两个运行文件，解锁 Web-driver 共享流并上传这份存档；换成错误 core 后下一次完整心跳会先撤销绑定且 revision 数不变，恢复精确 core 后重新授权仍是无重复上传的 no-op。最后全新 Web 上下文恢复并重新上传同一字节。运行方式：

```sh
VARKIV_WEB_EMULATOR_DIRECTORY=/absolute/path/to/EmulatorJS/4.2.3/data \
  ./scripts/acceptance-snes-native-compat.sh
```

脚本核验镜像标签、核心字符串、运行日志、两次存档哈希、Agent 版本与运行时身份、漂移撤销、revision 去重和容器/网络清理；报告不含 token、路径或 basename，唯一明文 Agent 配置在核验后删除。只有上述精确组合进入 Catalog；其他 OS/架构、真实掌机或同名的其他 Snes9x 构建仍需各自可验证的二进制与硬件证据。

当前 Mega Drive 的固定 Genesis Plus GX/测试夹具组合能够运行，但没有产出可同步的 `.srm`，因此只能标记为“可运行、未证明持久存档”。服务不会为没有核心输出的会话伪造空 revision；其他核心或 ROM 必须单独验收。
