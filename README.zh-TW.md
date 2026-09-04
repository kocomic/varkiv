[English](README.md) | [简体中文](README.zh-CN.md) | [**繁體中文**](README.zh-TW.md) | [日本語](README.ja.md)

![Varkiv](internal/server/web/assets/varkiv-logo.svg)

# Varkiv

輕量、自架的私人遊戲庫與裝置中樞。Varkiv 將 ROM 目錄、Pegasus 中繼資料、ES-DE 中繼資料和人工整理結果統一成可維護的目錄，並產生裝置整合包、協調存檔同步。

> Varkiv 目前處於預覽階段。升級前請備份遊戲庫狀態，並為 ROM 收藏保留獨立備份。

## 為什麼選擇 Varkiv

- 透過 `Series → Game → Edition → Artifact` 分別管理系列、單一平台遊戲、原版／漢化版／改版及實際檔案。
- 無需強制刮削即可匯入實際 ROM。缺少的檔案無法計算指紋，因此會被略過；Varkiv 不會建立無法驗證的空白記錄。
- 送出前先檢視帶簽章的匯入預覽。系統會重新驗證來源、順序和 SHA-256 指紋；批次失敗時不會寫入任何項目。
- 可匯入 Pegasus 與 ES-DE 遊戲庫，也可產生包含前端中繼資料、媒體、模擬器驅動程式、RetroArch 核心、啟動參數和設定範本的裝置整合包。
- 外部 ROM 目錄可以保持唯讀，也可以明確複製到受管儲存空間。媒體依內容定址，來源檔案不會被隱含移動、重新命名或刪除。
- 以單一 Go 服務、SQLite 和檔案系統儲存在個人 NAS 上執行，並透過明確的裝置設定檔連接 Windows 掌機、SteamOS/Bazzite、Android 和指定掌機 Linux。

## 快速開始

安裝 Go 1.26.6 後，在儲存庫根目錄執行本機介面示範：

```bash
./scripts/demo.sh
```

開啟 <http://127.0.0.1:8080>。示範只使用虛構、無法執行的 ROM 位元組。執行資料寫入已忽略的 `.demo/` 目錄，本機建置的二進位檔寫入已忽略的 `bin/varkiv` 路徑。按 `Ctrl+C` 停止。

若要透過 Docker Compose 從原始碼建立持久遊戲庫，請依照可複製、可驗證的 [Quickstart](docs/QUICKSTART.md) 操作。第一個映像發布後，文件會以真實映像位址說明容器安裝；預留位置 Registry 位址不是受支援的安裝方式。

## 產品邊界

- Varkiv 是私人的個人遊戲庫，不是多使用者媒體伺服器。
- 存檔屬於版本、平台或相容存檔容器，日常流程由 Device Agent 自動同步，而不是在網頁中手動上傳。
- 網頁執行是選用功能，需要另外提供並驗證 EmulatorJS 資源；頁面能載入不代表遊戲已經執行。
- PS2、Nintendo 3DS 等平台在沒有受支援的網頁執行環境時，仍由原生模擬器執行。
- 實驗性網頁連線遊玩與 RetroArch、PPSSPP 等原生模擬器的連線協定彼此隔離。

## 文件

| 從這裡開始 | 用途 |
|---|---|
| [Quickstart](docs/QUICKSTART.md) | 安全執行示範，或建立從原始碼建置的持久遊戲庫 |
| [產品基線](docs/PRODUCT.md) | 領域模型、使用者流程和不可跨越的行為邊界 |
| [API 指南](docs/API.md) | 驗證、錯誤、分頁、工作流程和 OpenAPI 入口 |
| [協定索引](docs/PROTOCOLS.md) | HashPack、可攜式清單、啟動 sidecar 與裝置同步契約 |
| [部署](docs/DEPLOYMENT.md)與[NAS 部署](docs/NAS_DEPLOYMENT.md) | 維運、更新、備份、復原和 Synology 說明 |
| [文件索引](docs/README.md) | 儲存、資料庫、平台、網頁執行、連線遊玩、驗收和發布門檻 |

## 開發

建置和測試說明請見 [CONTRIBUTING.md](CONTRIBUTING.md)。瀏覽器、容器、Android、目標整合包和實機證據屬於不同驗收等級；聲明執行支援前請閱讀 [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md)。

建置本機開發二進位檔並查看其機器可讀身分：

```bash
./scripts/build-local.sh
./bin/varkiv version --json
```

## 授權與隱私

Varkiv 採用 [Apache-2.0](LICENSE) 授權。儲存庫不包含商業 ROM、BIOS、韌體、私鑰或 EmulatorJS 執行資源。另請參閱[隱私邊界](docs/PRIVACY.md)、[第三方聲明](docs/THIRD_PARTY_NOTICES.md)和[安全性政策](SECURITY.md)。
