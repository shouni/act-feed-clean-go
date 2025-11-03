package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	clibase "github.com/shouni/go-cli-base"
	"github.com/shouni/go-utils/iohandler"
	"github.com/spf13/cobra"
	"golang.org/x/sync/semaphore"

	// 必要な依存関係
	"act-feed-clean-go/pkg/feed"
	"github.com/shouni/go-http-kit/pkg/httpkit"
	//	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// (前略: RunFlags, Flags, ExtractedArticle の定義はそのまま)

// RunFlags は 'run' コマンド固有のフラグを保持する構造体です。
type RunFlags struct {
	LLMAPIKey     string
	FeedURL       string
	Parallel      int
	ScrapeTimeout time.Duration
}

var Flags RunFlags

// ExtractedArticle は並列処理の結果を保持するための構造体 (runFeedExtraction内で使用)
type ExtractedArticle struct {
	URL     string
	Content string
	Error   error
}

// runFeedExtraction の本体（並列抽出ロジック）を cmd パッケージ内に定義します。
func runFeedExtraction(feedURL string, parallel int, scrapeTimeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // 全体タイムアウト
	defer cancel()

	// 1. クライアントとエクストラクタの初期化
	const maxRetries = 3

	// timeoutを第1引数に渡し、WithMaxRetriesでリトライ回数を設定します
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	httpClient := httpkit.New(scrapeTimeout, clientOptions...)
	extractor, nil := extract.NewExtractor(httpClient)

	// 2. RSSフィードの取得とURLリスト生成
	rssFeed, err := feed.FetchAndParse(ctx, httpClient, feedURL)
	if err != nil {
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	urlsToProcess := feed.ExtractLinks(rssFeed)
	if len(urlsToProcess) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	fmt.Fprintf(os.Stderr, "🌐 記事URL %d件を最大並列数 %d で本文抽出中...\n", len(urlsToProcess), parallel)

	// 3. 並列抽出の実行 (セマフォ制御)
	sem := semaphore.NewWeighted(int64(parallel))
	var wg sync.WaitGroup
	results := make(chan ExtractedArticle, len(urlsToProcess))

	// (並列処理ロジックは前回の実装とほぼ同じ)
	for _, url := range urlsToProcess {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				results <- ExtractedArticle{URL: url, Error: fmt.Errorf("セマフォ取得失敗: %w", err)}
				return
			}
			defer sem.Release(1)
			content, hasBodyFound, err := extractor.FetchAndExtractText(url, ctx)
			var extractErr error
			if err != nil {
				extractErr = fmt.Errorf("コンテンツの抽出に失敗しました: %w", err)
			} else if content == "" || !hasBodyFound {
				extractErr = fmt.Errorf("URL %s から有効な本文を抽出できませんでした", url)
			}
			log.Print(extractErr)

			results <- ExtractedArticle{URL: url, Content: content, Error: err}
		}(url)
	}
	wg.Wait()
	close(results)

	// 4. 結果の結合と出力準備
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", rssFeed.Title))
	successCount := 0

	for res := range results {
		if res.Error != nil {
			fmt.Fprintf(os.Stderr, "❌ 抽出失敗 [%s]: %v\n", res.URL, res.Error)
			continue
		}
		combinedTextBuilder.WriteString(fmt.Sprintf("## 【記事タイトル】 %s\n\n", res.URL))
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n")
		successCount++
	}
	fmt.Fprintf(os.Stderr, "✅ 抽出完了。成功件数: %d / 処理件数: %d\n", successCount, len(urlsToProcess))

	// 5. 結合テキストの出力
	if combinedTextBuilder.Len() > 0 {
		return iohandler.WriteOutput("", []byte(combinedTextBuilder.String()))
	}

	return nil
}

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	// APIキーのチェック（簡略化）
	if Flags.LLMAPIKey == "" {
		Flags.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
		if Flags.LLMAPIKey == "" {
			return fmt.Errorf("エラー: LLM APIキーが設定されていません。-kフラグまたはGEMINI_API_KEY環境変数を設定してください。")
		}
	}

	// 解決策: 定義した runFeedExtraction を呼び出す
	return runFeedExtraction(Flags.FeedURL, Flags.Parallel, Flags.ScrapeTimeout)
}

// (後略: addRunFlags, runCmd, Execute の定義はそのまま)

// addRunFlags は 'run' コマンドに固有のフラグを設定します。
func addRunFlags(runCmd *cobra.Command) {
	runCmd.Flags().StringVarP(&Flags.LLMAPIKey, "llm-api-key", "k", "", "Gemini APIキー (環境変数 GEMINI_API_KEY が優先)")
	runCmd.Flags().StringVarP(&Flags.FeedURL, "feed-url", "f", "https://news.yahoo.co.jp/rss/categories/it.xml", "処理対象のRSSフィードURL")
	runCmd.Flags().IntVarP(&Flags.Parallel, "parallel", "p", 10, "Webスクレイピングの最大同時並列リクエスト数")
	runCmd.Flags().DurationVarP(&Flags.ScrapeTimeout, "scraper-timeout", "s", 15*time.Second, "WebスクレイピングのHTTPタイムアウト時間")
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "RSSフィードの取得、並列抽出、AIクリーンアップを実行します。",
	Long:  "RSSフィードからURLを抽出し、記事本文を並列で取得後、AIで構造化・クリーンアップします。",
	RunE:  runCmdFunc,
}

// Execute は、CLIアプリケーションのエントリポイントです。
func Execute() {
	// runCmd にフラグを追加
	addRunFlags(runCmd)

	clibase.Execute(
		"act-feed-clean-go",
		nil,
		nil,
		runCmd,
	)
}
