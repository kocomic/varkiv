# 第三方依赖与许可证记录

生成依据：仓库锁定的 `go.mod`、`go list -deps ./cmd/varkiv`、`package-lock.json`、Android Gradle 依赖和模块许可证文件。它是发布审计输入，不替代各依赖自带的标准许可证全文。

## 分发目标 Go 二进制的运行依赖并集

| 模块 | 版本 | 主许可证 |
|---|---:|---|
| `github.com/dustin/go-humanize` | v1.0.1 | MIT |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `github.com/mattn/go-isatty` | v0.0.20 | MIT |
| `github.com/ncruces/go-strftime` | v1.0.0 | MIT |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| `golang.org/x/exp` | v0.0.0-20251023183803-a4bb9ffd2546 | BSD-3-Clause |
| `golang.org/x/sys` | v0.37.0 | BSD-3-Clause |
| `modernc.org/libc` | v1.67.6 | BSD-3-Clause；另含上游第三方声明 |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause；子组件另有声明 |
| `modernc.org/memory` | v1.11.0 | BSD-3-Clause；mmap-go/Go 来源另有声明 |
| `modernc.org/sqlite` | v1.45.0 | BSD-3-Clause；SQLite 生成来源需保留其适用声明 |

本表是 Linux amd64/arm64/armv7、Windows amd64/arm64 与 macOS arm64 无 CGO 发布目标的依赖并集；某个具体目标可以只链接其中的子集。模块版本和校验和由 `go.sum` 锁定。发布自动化不得从本表推断“只有一种许可证”；必须读取对应模块的 `LICENSE` 以及 `LICENSE-3RD-PARTY.md`、`LICENSE-*` 等附加文件。

## Android release APK/AAB 运行依赖

| Maven 坐标 | 版本 | 主许可证 |
|---|---:|---|
| `org.jetbrains.kotlin:kotlin-stdlib` | 2.0.21 | Apache-2.0 |
| `org.jetbrains:annotations` | 13.0 | Apache-2.0 |

这两项来自当前 `releaseRuntimeClasspath`，并由 Gradle 生成的 release SDK dependency metadata 交叉核对。上游 Maven 元数据分别声明 [Kotlin standard library](https://repo.maven.apache.org/maven2/org/jetbrains/kotlin/kotlin-stdlib/2.0.21/kotlin-stdlib-2.0.21.pom) 和 [JetBrains annotations](https://repo.maven.apache.org/maven2/org/jetbrains/annotations/13.0/annotations-13.0.pom) 使用 Apache-2.0。APK/AAB 内嵌本文件与经 SHA-256 固定的 Apache-2.0 标准全文；这不授予 Varkiv 自身 Apache-2.0 许可。

## Web E2E（开发/测试，不进入服务端运行镜像）

| 包 | 版本 | 许可证 |
|---|---:|---|
| `@playwright/test` / `playwright` / `playwright-core` | 1.62.1 | Apache-2.0 |
| `fsevents` | 2.3.2 | MIT；仅 macOS 可选开发依赖 |

## 可选网页联机实验

Varkiv 仓库与主应用镜像不包含 EmulatorJS 资源。实验资源下载器固定 EmulatorJS `4.3.0-pre` 的 10 个必要文件及 SHA-256；这些资源仍适用 EmulatorJS 的 GPL-3.0，必须由操作者单独下载并只读挂载。

`deploy/netplay-server/Dockerfile` 从 EmulatorJS-netplay 固定提交 `4090ca7bda795a8b7a7596f4d41a4605b515d9c5` 构建 Apache-2.0 信令 sidecar，并复制上游 LICENSE。Rust 依赖及校验和由该提交的 `Cargo.lock` 固定；rsproxy 只作 crates.io 稀疏索引和下载传输镜像，Cargo 仍验证锁文件校验和。当前 sidecar 是可选的本地构建实验产物，不随 Varkiv 主应用镜像发布；公开分发前仍需为最终静态链接二进制生成完整依赖 SBOM 与许可证集合。

## Android 直接测试声明

| 包 | 版本 | 用途 | 许可证 |
|---|---:|---|---|
| `junit:junit` | 4.13.2 | JVM 单元测试 | EPL-1.0 |
| `androidx.test:core` | 1.7.0 | Android instrumentation 应用上下文 | Apache-2.0 |
| `androidx.test:runner` | 1.6.2 | AndroidJUnitRunner | Apache-2.0 |
| `androidx.test.ext:junit` | 1.3.0 | JUnit 4 Android runner 扩展 | Apache-2.0；传递使用 JUnit 4 |

Android Gradle Plugin、Kotlin Gradle Plugin、Gradle Wrapper、Android SDK 与 JDK 是构建工具，不等于自动打进 APK 的运行库；Kotlin 标准库及其 annotations 传递项则属于上表列出的 release 运行内容。测试配置的传递依赖不进入 release APK/AAB；发布前仍需用最终 release 依赖报告和内嵌 notice 检查复核实际内容。

AndroidX Test 版本按 Android 官方的 [instrumented test setup](https://developer.android.com/training/testing/instrumented-tests) 与 [AndroidX Test release notes](https://developer.android.com/jetpack/androidx/releases/test) 选择；三项依赖只存在于 `debugAndroidTestRuntimeClasspath`，当前 `releaseRuntimeClasspath` 审计未发现 AndroidX Test 或 JUnit。

## 发布检查

- `go mod verify`
- `go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/varkiv | sort -u`
- `./scripts/check-third-party-notices.sh --all`
- `./scripts/test-third-party-notices.sh`
- `./scripts/collect-third-party-licenses.sh NEW_EMPTY_DIRECTORY`
- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...`
- `npm audit`
- `cd clients/android && ./gradlew dependencies`
- 对最终 Go 二进制执行 `go version -m`，对最终 APK/AAB 检查内嵌 `THIRD_PARTY_NOTICES.md` 与 `Apache-2.0.txt`。
- 将项目 `LICENSE`、需要的 `NOTICE`、本文件及 `collect-third-party-licenses.sh` 从当前模块缓存收集的实际第三方许可证正文一起放入 release 资产和桌面归档；Android 产物同时内嵌其运行依赖声明与许可证全文。

Varkiv 自身采用 Apache-2.0，标准全文位于仓库根目录 `LICENSE`；本文件只记录第三方组件及其各自适用的许可证与附加声明。
