package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	// 外部ライブラリ
	"github.com/shouni/go-http-kit/pkg/httpkit" // 記憶しているクライアント
	"golang.org/x/sync/semaphore"

	// 内部パッケージ
	"act-feed-clean-go/pkg/extract"   // 記憶している抽出ロジック
	"act-feed-clean-go/pkg/feed"      // 今回作成したフィードロジック
	"act-feed-clean-go/pkg/iohandler" // 記憶している入出力ロジック
)

const (
	// CLIオプションで上書き可能だが、ここではデフォルト値を定義
	defaultFeedURL         = "https://news.yahoo.co.jp/rss/categories/it.xml"
	maxParallelism         = 10 // 以前のREADMEで定義されていたデフォルト値
	scraperTimeout         = 15 * time.Second
	totalProcessingTimeout = 60 * time.Second
)

// ExtractedArticle は並列処理の結果を保持するための構造体
type ExtractedArticle struct {
	URL     string
	Content string
	Error   error
}

// runFeedExtraction は、RSSフィードの取得から本文の並列抽出までを実行するメインロジックです。
// 実際にはCobraのRunEなどに設定されます。
func runFeedExtraction(feedURL string) error {
	// 処理全体のコンテキストを設定
	ctx, cancel := context.WithTimeout(context.Background(), totalProcessingTimeout)
	defer cancel()

	// 1. クライアントとエクストラクタの初期化
	// 記憶している httpkit の Config を利用
	clientConfig := httpkit.Config{
		RetryMax:    3, // リトライ回数を定義 (例: 3回)
		HTTPTimeout: scraperTimeout,
	}
	// httpkit.NewClient でリトライ機能付きクライアントを初期化
	httpClient := httpkit.NewClient(clientConfig)

	// extract.NewExtractor で本文抽出器を初期化（クライアントをDI）
	extractor := extract.NewExtractor(httpClient)

	// 2. RSSフィードの取得とURLリスト生成
	rssFeed, err := feed.FetchAndParse(ctx, httpClient, feedURL)
	if err != nil {
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	urlsToProcess := feed.ExtractLinks(rssFeed)
	if len(urlsToProcess) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	fmt.Fprintf(os.Stderr, "🌐 記事URL %d件を最大並列数 %d で本文抽出中...\n", len(urlsToProcess), maxParallelism)

	// 3. 並列抽出の実行 (セマフォ制御)
	sem := semaphore.NewWeighted(int64(maxParallelism)) // 並列数を制御
	var wg sync.WaitGroup
	results := make(chan ExtractedArticle, len(urlsToProcess))

	for _, url := range urlsToProcess {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			// セマフォ取得 (並列数制限)
			if err := sem.Acquire(ctx, 1); err != nil {
				results <- ExtractedArticle{URL: url, Error: fmt.Errorf("セマフォ取得失敗: %w", err)}
				return
			}
			defer sem.Release(1)

			// 抽出処理を実行
			content, err := extractor.FetchAndExtractText(ctx, url)

			results <- ExtractedArticle{URL: url, Content: content, Error: err}
		}(url)
	}

	wg.Wait()
	close(results)

	// 4. 結果の結合と出力準備
	var combinedTextBuilder strings.Builder
	// トップレベルの見出しとしてフィードタイトルを使用
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", rssFeed.Title))
	successCount := 0

	for res := range results {
		if res.Error != nil {
			fmt.Fprintf(os.Stderr, "❌ 抽出失敗 [%s]: %v\n", res.URL, res.Error)
			continue
		}

		// 結合テキストに追加 (以前記憶していた抽出プレフィックスルールを適用)
		combinedTextBuilder.WriteString(fmt.Sprintf("## 【記事タイトル】 %s\n\n", res.URL)) // 見出しレベルを#2に統一
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n") // 記事間のセパレータ
		successCount++
	}

	fmt.Fprintf(os.Stderr, "✅ 抽出完了。成功件数: %d / 処理件数: %d\n", successCount, len(urlsToProcess))

	// 5. 結合テキストの出力（AI処理を省略し、iohandlerへ直接渡す）
	if combinedTextBuilder.Len() > 0 {
		// ここで最終的にAIクリーンアップロジックが挟まれる
		// 簡略化のため、直接 io.handlerに出力
		return iohandler.WriteOutput("", combinedTextBuilder.String())
	}

	return nil
}

// ----------------------------------------------------
// Note: 実際のCobra実装では、以下のようにRunE内で上記の関数を呼び出します。
/*
var rootCmd = &cobra.Command{
    Use:   "act-feed-clean-go",
    Short: "RSSフィードを取得し、AIでクリーンアップします",
}

var runCmd = &cobra.Command{
    Use:   "run",
    Short: "フィードの取得と本文抽出を実行",
    RunE: func(cmd *cobra.Command, args []string) error {
		// CLIオプションからURLを取得するロジックをここに記述
        return runFeedExtraction(defaultFeedURL) // 例: ハードコード値を使用
    },
}

func Execute() {
    rootCmd.AddCommand(runCmd)
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
*/
