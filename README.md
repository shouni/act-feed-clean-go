# ✍️ Act Feed Clean Go

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/act-feed-clean-go)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/act-feed-clean-go)](https://github.com/shouni/act-feed-clean-go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 💡 概要 (About) — **堅牢な並列ウェブ抽出、AI処理、および音声合成パイプライン**

**Act Feed Clean Go** は、RSSフィードから記事URLを抽出し、**並列処理**で本文を高速に取得、ノイズ除去を行い、最終的にAIによる**構造化・クリーンアップ**を実行し、さらに**VOICEVOXによる音声合成**まで実行できる高生産性 CLI ツールです。

### 🌸 導入がもたらすポジティブな変化

| メリット | チームへの影響 | 期待される効果 |
| :--- | :--- | :--- |
| **堅牢な並列ウェブ抽出** | **「不安定なWebサイトからの取得失敗」という課題を解消します。** リトライ機構により取得成功率を最大化します。 | **データ収集の信頼性が向上**し、後続のAI処理やデータ分析フェーズで、正確なインプットを常に保証できます。 |
| **統合AIパイプライン** | **データ前処理の手間がゼロになります。** 記事本文の抽出からAI処理までを一つのコマンドで完結させる**統合パイプライン**を確立します。 | **情報整理の全工程が自動化**され、開発者は取得した記事のクリーンな構造化データへすぐにアクセスできます。 |
| **音声合成統合** | **構造化された記事を即座に音声コンテンツ化できます。** 外部ツールへの手動データ転送が不要になります。 | **コンテンツ制作の自動化**が実現し、ブログ記事からポッドキャストや動画素材への変換スピードが劇的に向上します。 |

-----

## ✨ 技術スタック (Technology Stack)

| 要素 | 技術 / ライブラリ | 役割 |
| :--- | :--- | :--- |
| **言語** | **Go (Golang)** | ツールの開発言語。クロスプラットフォームでの高速な実行を実現します。 |
| **並行処理** | Goroutines / **カスタムセマフォ** (`pkg/scraper`) | 複数の記事URLに対するウェブ抽出リクエストを**チャネルを使用したカスタムセマフォ制御**の下で並列実行し、処理時間を短縮します。 |
| **AI処理** | **`google.golang.org/genai` (Gemini API)** | 大規模なテキストデータをMap-Reduceパターンで処理し、構造化・要約・クリーンアップを行います。 |
| **音声合成** | **`shouni/go-voicevox`** | VOICEVOXエンジンのAPIをラップし、**テキストスクリプトの並列音声合成とWAVファイルの結合**を実行します。 |
| **HTTPクライアント** | **`shouni/go-http-kit`** | **自動リトライ**と**タイムアウト制御**を持つ高信頼性のネットワーククライアントを提供します。 |
| **ウェブ抽出** | **`shouni/go-web-exact/v2`** | 記事本文のインテリジェントな特定とノイズ除去を実行します。 |
| **CLIフレームワーク** | **`spf13/cobra`** | ユーザーフレンドリーで強力なコマンドラインインターフェースを構築します。 |

-----

## 🛠️ 独自のネットワーククライアントによる堅牢化

本ツールは、Webコンテンツの取得を含むすべてのネットワーク通信の堅牢性を確保するため、開発者が独自に設計・実装した**高信頼性HTTPクライアントライブラリ** [`shouni/go-http-kit`](https://github.com/shouni/go-http-kit) を使用しています。

### 1\. Webコンテンツ抽出と通信の分離

* Webコンテンツの抽出（`go-web-exact`）と実際のHTTP通信処理を完全に分離し、**DI**（依存性注入）を利用することで、コードの保守性とテスト容易性を高めています。

### 2\. 堅牢な自動リトライとタイムアウト制御

* **専用クライアントによる全通信の堅牢化**: すべてのWeb取得リクエストは、共通の**DI対応クライアント**によって処理されます。
* **指数バックオフリトライ**: ネットワークエラーやサーバーの一時的な負荷増大（5xx系エラーなど）が発生した場合、**指数バックオフ戦略**に基づいて自動的にリトライを実行します。
* **ユーザー定義可能なタイムアウト**: `--scraper-timeout` フラグにより、**すべての**Webリクエストの個別タイムアウト時間をユーザーが柔軟に設定可能です。

### 3\. 高度なWebコンテンツ特定ロジック ([`shouni/go-web-exact/v2`](https://github.com/shouni/go-web-exact/v2) 内部)

* **インテリジェントな本文特定**: 主要なCMSやブログ構造に対応するため、複数のセレクタを組み合わせたロジックで本文エリアを特定します。
* **ノイズ要素の自動除去**: 広告、ソーシャルシェアボタン、コメント欄など、記事本文と無関係な要素を自動で除去します。

-----

## ✨ 主な機能

1.  **堅牢な抽出**: **自動リトライ**、**セマフォ制御**、および**不正データ検出**により、並列処理環境での記事本文取得の安定性を実現します。
2.  **RSSフィード対応**: RSSフィードのURLを指定するだけで、自動的にそこに含まれる記事URLを抽出し、並列処理を開始します。
3.  **AI構造化 (Map-Reduce)**: Gemini APIを利用し、大量の記事本文をセグメントに分割（Mapフェーズ）、並列で処理し、最終的に統合・構造化（Reduceフェーズ）します。**各フェーズで使用するAIモデルを指定可能**です。
4.  **音声合成機能 (VOICEVOX連携)**: AIによって構造化されたスクリプトを、VOICEVOXエンジンに送信し、複数のオーディオデータを結合して**単一のWAVファイル**として出力します。

-----

## 📦 使い方

### 1\. 環境設定

AI処理や音声合成を実行する際には、以下の環境変数またはフラグによる設定が必須となります。

| 変数名 | 必須/任意 | 説明 |
| :--- | :--- | :--- |
| `GEMINI_API_KEY` | 任意 (AI処理実行時必須) | Google AI Studio で取得した Gemini API キー。`--llm-api-key` フラグで上書き可能です。 |
| `VOICEVOX_API_URL` | 任意 (音声合成実行時必須) | 起動中の VOICEVOXエンジンのAPI URL。環境変数からも読み込み、`--voicevox-api-url` フラグで上書き可能です。 |

### 2\. 実行コマンド

メインコマンドは **`run`** です。

```bash
./bin/actfeedclean run [flags]
```

#### フラグ一覧

| フラグ | 短縮形 | 説明 | デフォルト値 |
| :--- |:----| :--- | :--- |
| `--feed-url` | `-f` | **処理対象のRSSフィードURL**。 | `https://news.yahoo.co.jp/rss/categories/it.xml` |
| `--parallel` | `-p` | Webスクレイピングの**最大同時並列リクエスト数**。 | `10` |
| `--scraper-timeout` | `-s` | Webスクレイピングの**HTTPタイムアウト時間**。 | `15s` |
| `--llm-api-key` | `-k` | Gemini APIキー。**これが設定されている場合のみAI処理が実行されます。** | |
| `--voicevox-api-url` | (なし) | **VOICEVOXエンジンのAPI URL**。環境変数からも読み込みます。 | |
| `--output-wav-path` | `-v` | 音声合成されたWAVファイルの出力パス。このフラグと`--voicevox-api-url`が設定されている場合にWAVファイルが出力されます。 | `asset/audio_output.wav` |
| **`--map-model`** | (なし) | **Mapフェーズ（記事のクリーンアップ・要約）に使用するAIモデル名**。 | `gemini-2.5-flash` |
| **`--reduce-model`** | (なし) | **Reduceフェーズ（最終スクリプト生成）に使用するAIモデル名**。精度重視なら`gemini-2.5-pro`を推奨。 | `gemini-2.5-flash` |

-----

## 🔊 実行例

### 例 1: AI処理をスキップし、デフォルトのRSSフィードを並列抽出し、結果を標準出力に出力

```bash
# GEMINI_API_KEYが未設定の場合、抽出のみ実行される
./bin/actfeedclean run 
```

### 例 2: 別のフィードURLを指定し、並列数とタイムアウトを変更して実行 (AI処理スキップ)

```bash
# 20並列で実行し、タイムアウトを30秒に延長。結果は output.txt にリダイレクト
./bin/actfeedclean run \
    -f "https://example.com/rss/tech.xml" \
    -p 20 \
    -s 30s > output.txt
```

### 例 3: AI処理を有効化し、抽出結果を構造化して標準出力に出力

```bash
# 環境変数または -k フラグでAPIキーを指定すると、AI構造化処理が実行され、結果が標準出力に出力される
export GEMINI_API_KEY="YOUR_API_KEY" 
./bin/actfeedclean run 
# または
./bin/actfeedclean run -k "ANOTHER_API_KEY"
```

### 例 4: AI処理と音声合成を有効化し、WAVファイルとして保存

```bash
# 事前にVOICEVOX Engineを起動しておく (デフォルト: http://127.0.0.1:50021)

# 環境変数でキーとURLを指定し、WAV出力パスを指定して実行 (デフォルトパスを使用)
export GEMINI_API_KEY="YOUR_API_KEY"
export VOICEVOX_API_URL="http://127.0.0.1:50021"

./bin/actfeedclean run -v "asset/audio_output.wav"
```

### 例 5: Map/Reduceフェーズに異なるカスタムAIモデルを指定して実行 ✨ **NEW**

```bash
# Mapフェーズには高速な'flash'、Reduceフェーズには高品質な'pro'モデルを指定
./bin/actfeedclean run \
  -k "ANOTHER_API_KEY" \
  --map-model "gemini-2.5-flash" \
  --reduce-model "gemini-2.5-pro" \
  --voicevox-api-url "http://127.0.0.1:50021" \
  -v "custom_output/high_quality_script.wav"
```

-----

### 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
