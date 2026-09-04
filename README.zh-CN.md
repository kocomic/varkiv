[English](README.md) | [**简体中文**](README.zh-CN.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md)

![Varkiv](internal/server/web/assets/varkiv-logo.svg)

# Varkiv

轻量、自托管的私人游戏库与设备中枢。Varkiv 将 ROM 目录、Pegasus 元数据、ES-DE 元数据和人工整理结果统一为可维护的目录，并生成设备整合包、协调存档同步。

> Varkiv 目前处于预览阶段。升级前请备份资料库状态，并为 ROM 收藏保留独立备份。

## 为什么选择 Varkiv

- 通过 `Series → Game → Edition → Artifact` 分别管理系列、单平台游戏、原版/汉化版/改版以及实际文件。
- 无需强制刮削即可导入真实 ROM。缺失文件无法计算指纹，因此会被跳过；Varkiv 不会建立无法验证的空记录。
- 提交前审查带签名的导入预览。系统会重新验证来源、顺序和 SHA-256 指纹；批次失败时不会写入任何条目。
- 可导入 Pegasus 与 ES-DE 游戏库，也可生成包含前端元数据、媒体、模拟器驱动、RetroArch 核心、启动参数和配置模板的设备整合包。
- 外部 ROM 目录可以保持只读，也可以明确复制到受管存储。媒体按内容寻址，来源文件不会被隐式移动、改名或删除。
- 以单个 Go 服务、SQLite 和文件系统存储运行在个人 NAS 上，并通过明确的设备档案连接 Windows 掌机、SteamOS/Bazzite、Android 和选定掌机 Linux。

## 快速开始

安装 Go 1.26.6 后，在仓库根目录运行本地界面演示：

```bash
./scripts/demo.sh
```

打开 <http://127.0.0.1:8080>。演示只使用虚构、不可运行的 ROM 字节。运行数据写入已忽略的 `.demo/` 目录，本地构建的二进制写入已忽略的 `bin/varkiv` 路径。按 `Ctrl+C` 停止。

如需通过 Docker Compose 从源码建立持久资料库，请按照可复制、可验证的 [Quickstart](docs/QUICKSTART.md) 操作。首个镜像发布后，文档会使用真实镜像地址说明容器安装；占位 Registry 地址不是受支持的安装方式。

## 产品边界

- Varkiv 是私人的个人游戏库，不是多用户媒体服务器。
- 存档属于版本、平台或兼容存档容器，日常流程由 Device Agent 自动同步，而不是在网页中手工上传。
- 网页运行是可选能力，需要另行提供并校验 EmulatorJS 资源；页面能够加载不代表游戏已经运行。
- PS2、Nintendo 3DS 等平台在没有受支持的网页运行环境时，仍然由原生模拟器运行。
- 实验性网页联机与 RetroArch、PPSSPP 等原生模拟器的联机协议彼此隔离。

## 文档

| 从这里开始 | 用途 |
|---|---|
| [Quickstart](docs/QUICKSTART.md) | 安全运行演示，或建立从源码构建的持久资料库 |
| [产品基线](docs/PRODUCT.md) | 领域模型、用户流程和不可越过的行为边界 |
| [API 指南](docs/API.md) | 鉴权、错误、分页、工作流和 OpenAPI 入口 |
| [协议索引](docs/PROTOCOLS.md) | HashPack、便携清单、启动 sidecar 与设备同步合同 |
| [部署](docs/DEPLOYMENT.md)与[NAS 部署](docs/NAS_DEPLOYMENT.md) | 运维、更新、备份、恢复和群晖说明 |
| [文档索引](docs/README.md) | 存储、数据库、平台、网页运行、联机、验收和发布门禁 |

## 开发

构建和测试说明见 [CONTRIBUTING.md](CONTRIBUTING.md)。浏览器、容器、Android、目标整合包和真机证据属于不同验收等级；声明运行支持前请阅读 [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md)。

构建本地开发二进制并查看其机器可读身份：

```bash
./scripts/build-local.sh
./bin/varkiv version --json
```

## 许可证与隐私

Varkiv 采用 [Apache-2.0](LICENSE) 许可证。仓库不包含商业 ROM、BIOS、固件、私钥或 EmulatorJS 运行资源。另请参阅[隐私边界](docs/PRIVACY.md)、[第三方声明](docs/THIRD_PARTY_NOTICES.md)和[安全策略](SECURITY.md)。
