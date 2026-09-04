# 验收指南

验收必须区分代码测试、容器部署、模拟器运行和真实设备。低层证据不能冒充高层证据，历史文档中的成功记录也不能代替当前执行。

## 日常门禁

```bash
go test ./...
go vet ./...
git diff --check

./scripts/check-version.sh
./scripts/check-source-hygiene.sh
./scripts/test-source-hygiene.sh
./scripts/check-docs.py
```

Go 测试覆盖 schema 迁移、候选令牌防漂移、批量原子失败、存储边界、HTTP 合同、包生成和同步协议。版本脚本核对二进制、镜像、Compose、OpenAPI 和文档中的公开版本。

## Web E2E

```bash
npm ci
npm run test:e2e:install
npm run test:e2e
```

Playwright 应覆盖所有一级路由、关键导入/导出流程和 390px 移动布局。视觉检查至少确认：

- 同级组件尺寸、标题基线和控件高度一致。
- 平台、同步、来源与整合包没有独立风格漂移。
- 四种 UI 语言不会造成按钮、标题或说明异常断行。
- 长说明可展开但不会挤压主操作；风险警告始终可见。
- 图标使用同一描边、切角和语义系统。

页面加载成功只证明管理 UI，不证明浏览器模拟器启动了游戏。

## 容器与 NAS

```bash
./scripts/acceptance-container.sh --port 18085
./scripts/acceptance-compose-nas.sh --port 18186
./scripts/acceptance-compose-nas.sh --port 18187 --published-compose
./scripts/build-nas-deployment-bundle.sh
```

容器脚本只使用仓库虚构夹具和唯一临时资源，结束后精确清理，不接触用户卷。真实 NAS 还必须按 [NAS_DEPLOYMENT.md](NAS_DEPLOYMENT.md)完成目录、文件系统、只读 ROM、重启持久化、备份和恢复演练。

## Web Player

```bash
npm run --silent verify:web-emulator-assets -- --directory /absolute/path/to/data
npm run test:e2e:web-emulation
```

每个平台独立验收以下层级：

1. 固定资源的类型、大小和 SHA-256 正确。
2. readiness 返回该平台精确的 core 与扩展名能力。
3. 浏览器实际读取 ROM，核心进入运行状态。
4. 画面按预期推进，输入能改变游戏状态。
5. 宣称支持存档时，真实文件可在全新会话恢复。

路由健康、画布出现或玩家状态 `ready` 都不能替代第 3–5 项。测试 ROM 的来源、许可和平台矩阵见 [WEB_EMULATION.md](WEB_EMULATION.md)。

## 网页联机实验

```bash
./scripts/acceptance-compose-web-netplay.sh
```

该门禁构建固定信令 sidecar 与核验后的 EmulatorJS 4.3.0-pre 资源，在新建数据库中导入两份不同 SHA-256 的固定公开 NES homebrew，并启动两个独立 Chromium 上下文。验收从真实资料库 UI 创建房间、读取可见邀请码，再由 390px 客端 UI 加入，不绕过产品界面直接拼播放器地址。

通过必须同时满足：

- 双方运行状态为 `started`、WebRTC 状态为 `connected`，各见两名玩家和一条 peer connection。
- 客端按键在主端形成按下和松开事件，移动端播放器有效高度不少于 500px。
- 未鉴权创建、畸形邀请码、错误密钥、ROM 指纹不一致、第三人加入和伪造播放器能力均按固定状态码拒绝。
- 同一客端重试保持幂等；失败尝试不会阻止随后合法客端加入。
- 浏览器壳层可识别标准 Gamepad API；自动化远端输入使用键盘事件，因为 Chromium 不提供可伪造为原生硬件的可信 `Gamepad`。真实手柄传输仍须单列硬件验收。
- 存档 API 请求和持久存档文件均为零。

普通 Playwright 套件另外覆盖信令未就绪时隐藏入口、就绪后服务瞬断、中文错误反馈、邀请码退出后清除，以及浅色和手机布局。脚本不读取用户资料库、NAS、数据库或存档，结束后只清理自己创建的容器、镜像、监听和临时根。

## Android 软件验收

以下脚本只操作新建的隔离 AVD，不选择已有真机：

```bash
./scripts/acceptance-android-emulator.sh --port 18088
./scripts/acceptance-android-retroarch.sh
./scripts/acceptance-android-ppsspp.sh
```

- `acceptance-android-emulator` 验证配对、SAF、ROM 哈希、RetroArch/PPSSPP 存档、原子下载和冲突。
- `acceptance-android-retroarch` 要求固定 APK/core/测试 ROM 真正启动并渲染。
- `acceptance-android-ppsspp` 要求官方应用读取 SAF URI 并完成画面渲染。

AVD 成功仍不替代真机的 SD 卡权限、后台限制、休眠、Intent 和文件选择器行为。

## 目标包

使用 `varkiv target-package build` 生成目标目录，使用 `target-package verify` 只读校验合同、SHA-256、权限、缺失/额外文件和符号链接。至少覆盖：

- Windows 私密包与托盘/后台二选一安装入口。
- SteamOS/Bazzite 用户目录与 systemd user unit。
- Android APK 与 SAF 授权流程。
- ROCKNIX/KNULLI/dArkOS/muOS/OnionOS 的前端目录和显式 Hook。

Package-tested 只说明生成目录正确，不说明真机模拟器、权限或生命周期已经可用。

## 真实设备证据

每种 `target + frontend + driver/core` 组合独立执行并记录：

1. 安装或复制目标包。
2. 导入并启动一个合法小型 ROM。
3. 重启前端和设备后再次启动。
4. 生成增量包，验证未受管文件不被覆盖。
5. 两台设备完成上传、下载、离线恢复和分叉冲突。
6. 升级服务和客户端后重复关键流程。

报告必须由设备本地生成，服务端只接收脱敏合同、软件身份和场景结果。维护者逐项预览并确认；一个设备的证据不能提升另一 target 的等级。

## 发布审计

```bash
./bin/varkiv release-audit --db ./data/library.db --json
./bin/varkiv release-audit --db ./data/library.db --require-hardware --json
```

第一条用于软件和数据库状态，第二条在发布前要求真实硬件门禁。名称、贡献权利和受保护发布授权仍是人工外部门禁，详见 [RELEASE_READINESS.md](RELEASE_READINESS.md)。

## 证据记录规则

- 报告“本次实际运行”的命令、结果和日期；不要引用旧预览号冒充当前执行。
- 分开列出自动测试、人工流程和外部风险。
- 不在报告、日志或截图中包含 token、ROM/存档文件名、宿主绝对路径或个人设备标识。
- 清理只针对测试创建的精确临时目录、容器、卷和 AVD。
