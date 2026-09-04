# 网页联机实验

网页联机是 Varkiv 的隔离实验能力，不是所有模拟器通用的联机协议。它让两台浏览器在各自拥有完全相同 ROM 的前提下建立短时会话：房主运行模拟器并通过 WebRTC 发送画面和声音，加入者通过 WebRTC 数据通道把输入发回房主。

## 当前可验收范围

| 项目 | 固定合同 |
|---|---|
| 平台 | NES / Famicom |
| 运行时 | EmulatorJS `4.3.0-pre` |
| 核心 | `fceumm`，核心包 SHA-256 固定 |
| 人数 | 2 人 |
| 内容 | SHA-256、大小、平台必须完全一致 |
| 网络 | 浏览器之间 WebRTC；Varkiv 只代理信令 |
| 存档 | `no-persist`，实验会话不恢复或写入持久存档 |

会话创建前和播放器发放前都会重新打开 ROM、核对大小与 SHA-256，并验证 NES 头。一次性邀请口令只授权加入当前房间；主端和客端播放器使用分别签名、短时有效的能力 URL。第三位加入者、过期邀请、ROM 漂移、核心漂移和平台漂移都会在进入播放器前被拒绝。

## 架构

```text
Varkiv UI
  ├─ 会话与精确兼容校验 ──> in-process multiplayer broker
  ├─ /play-netplay/<signed capability> ──> pinned EmulatorJS player
  └─ /socket.io + /list ──same-origin proxy──> signal sidecar
                                                    │
Browser A (host) <──────── WebRTC media/data ─────> Browser B (guest)
```

信令 sidecar 使用 EmulatorJS-netplay 的固定提交构建，容器以非 root、只读根文件系统和零 Linux capabilities 运行。浏览器只看到 Varkiv 同源地址，不接触内部 sidecar 地址。房间状态保存在内存中，进程重启后自然失效。

这条链路采用房主流式模型，不是 RetroArch 的确定性帧同步。`/api/multiplayer/v1` 仍是面向其他客户端的通用协调协议；浏览器实验的资料库解析入口位于 `/api/v1/web-netplay/*`，两者不应混为一种运行时。

## 本机与 Compose 启用

先把实验资源下载到一个尚不存在的新目录。下载器固定 HTTPS 版本地址、文件大小和 SHA-256，全部通过后才原子发布目录：

```sh
npm run --silent fetch:web-netplay-assets -- \
  --directory /absolute/new/path/to/EmulatorJS/4.3.0-pre/data
```

Compose 使用主配置和实验 overlay。EmulatorJS GPL-3.0 资源由操作者只读挂载，不进入 Varkiv 的 Apache-2.0 镜像：

```dotenv
EMULATORJS_NETPLAY_DATA_PATH=/absolute/path/to/EmulatorJS/4.3.0-pre/data
VARKIV_WEB_NETPLAY_ICE_SERVERS=[]
```

```sh
docker compose -f compose.yaml -f compose.web-netplay.yaml config --quiet
docker compose -f compose.yaml -f compose.web-netplay.yaml up -d --build
```

也可以直接启动服务，将 `--web-netplay-signal-upstream` 指向只在内网监听的 sidecar：

```sh
./bin/varkiv serve \
  --addr 127.0.0.1:8080 \
  --db ./data/library.db \
  --state ./data \
  --library ./library \
  --web-netplay-emulator-directory /absolute/path/to/data \
  --web-netplay-signal-upstream http://127.0.0.1:18090 \
  --web-netplay-ice-servers '[]'
```

`GET /api/v1/web-netplay/readiness` 只返回能力、固定运行身份、资源核验统计和信令是否可达，不返回宿主路径、内部信令地址或 ICE 凭据。

## 网络边界

空 ICE 列表只适合 localhost 或双方网络能够直接建立连接的环境。跨 NAT 或公网部署需要 HTTPS，并配置 STUN/TURN：

```dotenv
VARKIV_WEB_NETPLAY_ICE_SERVERS=[{"urls":["stun:stun.example.net:3478"]},{"urls":["turns:turn.example.net:5349"],"username":"short-lived-user","credential":"short-lived-secret"}]
```

TURN 凭据必然需要下发给加入会话的浏览器，因此应使用短时、限流、可轮换的凭据，不应放入仓库或长期共享配置。Varkiv 不内置公共 TURN，也不把一次 LAN 成功推导成公网可用。

## 可重复验收

完整验收会下载固定的公开 NES homebrew、构建信令 sidecar、启动隔离 Varkiv 数据库和两个 Chromium 上下文。它从真实 UI 完成创建、邀请码显示、手机端加入和模拟器启动，并验证两端进入 `connected`、每端恰有一个 peer、客端按键在主端形成按下/松开事件、没有存档 API 请求或存档文件。负向路径同时覆盖未鉴权创建、畸形或错误邀请码、不同 ROM、第三人、同客端幂等重试和伪造播放器能力。它不会读取用户资料库、NAS、数据库或存档：

```sh
./scripts/acceptance-compose-web-netplay.sh
```

脚本只删除自己按 PID 创建的容器、镜像和临时根。仅验证已经运行的信令服务时，可直接执行：

```sh
VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY=/absolute/path/to/data \
VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM=http://127.0.0.1:18090 \
npm run acceptance:web-netplay
```

## 已知限制

- 目前只开放 NES，不能据此推断 SNES、街机、PS2、3DS 或原生模拟器可联机。
- 房主负责模拟、编码和上行带宽；房主离开即结束本轮实验。
- 自动 E2E 会验证 Gamepad API 状态接线，但 Chromium 无法构造可信的原生手柄设备；真实双端手柄输入仍是硬件验收项，不能由脚本注入结果代替。
- 当前会话不持久化游戏存档，避免流式客端、主端自动存档和现有 SaveStream 在语义未定前相互覆盖。
- 无 TURN 时只承诺本机/LAN 实验；公网、弱网、移动网切换和真实手柄双端仍需分别验收。
- EmulatorJS `4.3.0-pre` 是预发布运行时，与稳定 Web Player 的 `4.2.3` 资源完全隔离。
