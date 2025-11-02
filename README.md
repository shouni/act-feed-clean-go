# ✍️ Act Feed Clean Go

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/act-feed-clean-go)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/act-feed-clean-go)](https://github.com/shouni/act-feed-clean-go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 💡 概要 (About)— **堅牢なGo並列処理とAIを統合した次世代ドキュメント音声化パイプライン**

**Act Feed Clean Go (PAID Go)** は、独自の高生産性 CLI ツールです。

### 🌸 導入がもたらすポジティブな変化

| メリット | チームへの影響 | 期待される効果 |
| :--- | :--- | :--- |
| **統合パイプラインの提供** | **「複数ツールの連携」という間接作業が不要です。** スクリプト生成から最終的なWAVファイル出力までを一つのCLIコマンドで完結させる**統合パイプライン**を提供します。 | **制作の全工程が自動化**され、開発者はドキュメント配信をストレスフリーで行えるようになります。 |

-----

## ✨ 技術スタック (Technology Stack)

| 要素 | 技術 / ライブラリ | 役割 |
| :--- | :--- | :--- |
| **言語** | **Go (Golang)** | ツールの開発言語。クロスプラットフォームでの高速な実行を実現します。 |
| **並行処理** | **Goroutines (`sync` パッケージ)** | 複数の音声合成リクエストを**並列**で実行し、ネットワーク待ち時間や合成処理にかかる時間を短縮します。 |

-----

## 🛠️ 独自のネットワーククライアントによる堅牢化

本ツールは、**Webコンテンツの取得、VOICEVOX連携、外部API投稿**を含むすべてのネットワーク通信の堅牢性を確保するため、開発者が独自に設計・実装した**高信頼性HTTPクライアントライブラリ** [`shouni/go-http-kit`](https://github.com/shouni/go-http-kit) を使用しています。

### 1\. Webコンテンツ抽出と通信の分離

* Webコンテンツの抽出（`goquery`ベースの処理）と、実際のHTTP通信処理を完全に分離し、`GenerateHandler`に依存性注入（DI）することで、コードの保守性とテスト容易性を高めています。

### 2\. 堅牢な自動リトライとタイムアウト制御

* **専用クライアントによる全通信の堅牢化**: すべてのWeb取得リクエストおよびVOICEVOX APIリクエストは、共通の**DI対応クライアント**によって処理されます。
* **指数バックオフリトライ**: ネットワークエラーやサーバーの一時的な負荷増大（5xx系エラーなど）が発生した場合、**指数バックオフ戦略**に基づいて自動的にリトライを実行します。これにより、大規模なドキュメントの取得・合成時の堅牢性が飛躍的に向上しています。
* **ユーザー定義可能なタイムアウト**: `--http-timeout` フラグにより、**すべての**Webリクエスト（Web抽出、VOICEVOXロード、合成など）の全体的なタイムアウト時間をユーザーが柔軟に設定可能です。

### 3\. 高度なWebコンテンツ特定ロジック ([`shouni/go-web-exact`](https://github.com/shouni/go-web-exact) 内部)

* **インテリジェントな本文特定**: 主要なCMSやブログ構造に対応するため、複数のセレクタを組み合わせたロジックで本文エリアを特定します。
* **ノイズ要素の自動除去**: 広告、ソーシャルシェアボタン、コメント欄など、記事本文と無関係な要素を自動で除去します。

-----

## ✨ 主な機能

1.  **堅牢な処理**: 上記の**リトライ**、**不正データ検出**、および**フォールバック**ロジックにより、並列処理環境での安定した音声ファイル生成を実現します。
2**柔軟なI/O**: **Web (URL)**、**ファイル**、**標準入力**からの入力、およびファイル出力、標準出力、外部APIへの投稿に対応しています。

-----

## 📦 使い方

### 1\. 環境設定

ツールを実行する前に、以下の環境変数を設定してください。

| 変数名 | 必須/任意 | 説明 |
| :--- | :--- | :--- |
| `GEMINI_API_KEY` | 必須 | Google AI Studio で取得した Gemini API キー。 |
| `VOICEVOX_API_URL` | VOICEVOX使用時必須 | ローカルで起動しているVOICEVOXエンジンのURL。 (例: `http://localhost:50021`) |
| `POST_API_URL` | 外部API投稿時必須 | スクリプトを投稿する外部APIのエンドポイント。 |

### 2\. スクリプト生成コマンド

メインコマンドは `generate` です。

```bash
paidgo generate [flags]
```

#### フラグ一覧

入力ソースは **`--script-url`, `--script-file` のいずれか一つのみ**指定可能です。

| フラグ | 短縮形  | 説明 |
| :--- |:-----| :--- |
| `--script-url` | `-u` | **入力ソースURL**。Webページから記事本文を抽出し、AIに渡します。 |
| `--script-file` | `-f` | **入力スクリプトファイルのパス**。`'-'` を指定すると標準入力から読み込みます。 |

-----

## 🔊 実行例

### 例 1: Web記事を対話スクリプト化し、音声ファイルに出力（タイムアウト指定あり）

VOICEVOXエンジンと環境変数が設定済みであることを前提とします。

```bash
# Web上の技術記事を読み込み、対話モードでスクリプト生成、VOICEVOXの処理時間を考慮しタイムアウトを延長
./bin/paidgo generate \
    --script-url "https://github.com/shouni/prototypus-ai-doc-go" \
    --mode dialogue \
    --http-timeout 280s \
    --voicevox out_dialogue.wav
```

-----

### 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
