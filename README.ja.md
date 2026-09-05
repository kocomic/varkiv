[English](README.md) | [简体中文](README.zh-CN.md) | [繁體中文](README.zh-TW.md) | [**日本語**](README.ja.md)

![Varkiv](internal/server/web/assets/varkiv-logo.svg)

# Varkiv

個人の ROM コレクション向けに設計された、軽量なセルフホスト型ゲームライブラリ兼デバイスハブです。Varkiv は ROM フォルダー、Pegasus メタデータ、ES-DE メタデータ、手動整理の結果を一つの保守可能なカタログにまとめ、端末用パッケージの生成とセーブ同期を支援します。

> Varkiv は現在プレビュー版です。更新前にライブラリの状態をバックアップし、ROM コレクションは別途バックアップしてください。

## Varkiv を選ぶ理由

- `Series → Game → Edition → Artifact` により、シリーズ、機種別ゲーム、オリジナル版／翻訳版／改造版、実ファイルを分けて管理します。
- スクレイピングを必須とせず、実在する ROM を取り込めます。ファイルがなければ指紋を計算できないため、その項目をスキップし、検証不能な空の記録は作成しません。
- 確定前に署名付きの取り込みプレビューを確認できます。確定時にソース、順序、SHA-256 指紋を再検証し、バッチが失敗した場合は一件も書き込みません。
- Pegasus と ES-DE のライブラリを取り込み、フロントエンドのメタデータ、メディア、エミュレータードライバー、RetroArch コア、起動引数、設定テンプレートを含む端末用パッケージを書き出せます。
- 外部 ROM フォルダーを読み取り専用のまま参照するか、明示的に管理領域へコピーできます。メディアは内容ベースで管理され、ソースが暗黙に移動、改名、削除されることはありません。
- 単一の Go サービス、SQLite、ファイルシステムストレージを個人 NAS で動かし、明示的なデバイスプロファイルを介して Windows 携帯機、SteamOS/Bazzite、Android、対応する携帯機 Linux と接続します。

## クイックスタート

Go 1.26.6 をインストールし、リポジトリのルートでローカル UI デモを実行します。

```bash
./scripts/demo.sh
```

<http://127.0.0.1:8080> を開いてください。デモは架空の実行不能な ROM バイトだけを使います。実行データは Git 管理外の `.demo/` フォルダーに書き込み、ローカルビルドのバイナリは Git 管理外の `bin/varkiv` に生成します。停止するには `Ctrl+C` を押します。

永続ライブラリを構築する場合は、コピーして検証できる [Quickstart](docs/QUICKSTART.md) に従ってください。`ghcr.io/kocomic/varkiv` と [Docker Hub](https://hub.docker.com/r/kocomic/varkiv) から `linux/amd64` と `linux/arm64` のイメージを取得できます。両レジストリの manifest digest は同一です。Release には digest 固定の Compose、環境テンプレート、チェックサム、SBOM、ビルド provenance が付属します。開発用途ではソースビルドも利用できます。

## 製品の境界

- Varkiv は個人用のプライベートライブラリであり、複数ユーザー向けメディアサーバーではありません。
- セーブデータはエディション、機種、または互換セーブコンテナーに属し、日常的なブラウザーへの手動アップロードではなく Device Agent の自動同期を前提とします。
- ブラウザー実行は任意機能で、別途用意して検証した EmulatorJS アセットが必要です。ページを開けることは、ゲームが動作している証拠ではありません。
- PS2 や Nintendo 3DS などは、対応するブラウザー実行環境がない場合、引き続きネイティブエミュレーターで実行します。
- 実験的なブラウザーネットプレイは、RetroArch や PPSSPP などのネイティブエミュレーターの通信プロトコルとは分離されています。

## ドキュメント

| 最初に読む文書 | 用途 |
|---|---|
| [Quickstart](docs/QUICKSTART.md) | デモを安全に実行するか、ソースビルドの永続ライブラリを作成する |
| [製品ベースライン](docs/PRODUCT.md) | ドメインモデル、ユーザーフロー、変更してはならない動作境界 |
| [API ガイド](docs/API.md) | 認証、エラー、ページング、ワークフロー、OpenAPI の入口 |
| [プロトコル索引](docs/PROTOCOLS.md) | HashPack、ポータブルマニフェスト、起動 sidecar、デバイス同期契約 |
| [デプロイ](docs/DEPLOYMENT.md)と[NAS デプロイ](docs/NAS_DEPLOYMENT.md) | 運用、更新、バックアップ、復元、Synology 向け手順 |
| [ドキュメント索引](docs/README.md) | ストレージ、データベース、機種、ブラウザー実行、ネットプレイ、受け入れ、リリースゲート |

## 開発

ビルドとテストの手順は [CONTRIBUTING.md](CONTRIBUTING.md) を参照してください。ブラウザー、コンテナー、Android、対象端末パッケージ、実機証拠にはそれぞれ異なる受け入れレベルがあります。ランタイム対応を表明する前に [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) を確認してください。

ローカル開発用バイナリをビルドし、機械可読な識別情報を確認します。

```bash
./scripts/build-local.sh
./bin/varkiv version --json
```

## ライセンスとプライバシー

Varkiv は [Apache-2.0](LICENSE) でライセンスされています。このリポジトリに商用 ROM、BIOS、ファームウェア、秘密鍵、EmulatorJS ランタイムアセットは含まれません。[プライバシー境界](docs/PRIVACY.md)、[サードパーティー通知](docs/THIRD_PARTY_NOTICES.md)、[セキュリティポリシー](SECURITY.md)も確認してください。
