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
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// ----------------------------------------------------------------------
// 構造体とフラグ
// ----------------------------------------------------------------------

// RunFlags は 'run' コマンド固有のフラグを保持する構造体です。
type RunFlags struct {
	LLMAPIKey     string
	FeedURL       string
	Parallel      int
	ScrapeTimeout time.Duration
}

var Flags RunFlags

// ExtractedArticle は並列処理の結果を保持するための構造体 (Titleを追加)
type ExtractedArticle struct {
	URL     string
	Title   string // 修正: 記事タイトルを追加
	Content string
	Error   error
}

// ArticleInfo は並列処理に渡すための記事URLとタイトル情報
type ArticleInfo struct {
	URL   string
	Title string
}

// ----------------------------------------------------------------------
// メイン処理ロジック
// ----------------------------------------------------------------------

// runFeedExtraction の本体（並列抽出ロジック）を cmd パッケージ内に定義します。
func runFeedExtraction(feedURL string, parallel int, scrapeTimeout time.Duration) error {
	// 全体タイムアウトを 5分に設定
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. クライアントとエクストラクタの初期化
	const maxRetries = 3

	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	httpClient := httpkit.New(scrapeTimeout, clientOptions...)

	// 修正: エラーチェックを導入 (nil代入を修正)
	extractor, err := extract.NewExtractor(httpClient)
	if err != nil {
		return fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 2. RSSフィードの取得とURLリスト生成
	rssFeed, err := feed.FetchAndParse(ctx, httpClient, feedURL)
	if err != nil {
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	// 修正: URLsToProcess を ArticleInfo のスライスに置き換え、タイトルを保持
	articlesToProcess := make([]ArticleInfo, 0, len(rssFeed.Items))
	for _, item := range rssFeed.Items {
		if item.Link != "" && item.Title != "" {
			articlesToProcess = append(articlesToProcess, ArticleInfo{URL: item.Link, Title: item.Title})
		}
	}

	if len(articlesToProcess) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	fmt.Fprintf(os.Stderr, "🌐 記事URL %d件を最大並列数 %d で本文抽出中...\n", len(articlesToProcess), parallel)

	// 3. 並列抽出の実行 (セマフォ制御)
	sem := semaphore.NewWeighted(int64(parallel))
	var wg sync.WaitGroup
	results := make(chan ExtractedArticle, len(articlesToProcess))

	for _, article := range articlesToProcess {
		wg.Add(1)
		go func(article ArticleInfo) {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				results <- ExtractedArticle{URL: article.URL, Title: article.Title, Error: fmt.Errorf("セマフォ取得失敗: %w", err)}
				return
			}
			defer sem.Release(1)

			content, hasBodyFound, err := extractor.FetchAndExtractText(article.URL, ctx)

			// 修正: エラーハンドリングを一貫させ、finalErrを導入
			var finalErr error
			if err != nil {
				finalErr = fmt.Errorf("コンテンツの抽出に失敗しました: %w", err)
			} else if content == "" || !hasBodyFound {
				finalErr = fmt.Errorf("URL %s から有効な本文を抽出できませんでした", article.URL)
			}

			// log.Print(extractErr) の代わりに finalErr をログに出力（verboseモード）
			if finalErr != nil && clibase.Flags.Verbose {
				log.Printf("抽出エラー [%s] (%s): %v", article.Title, article.URL, finalErr)
			}

			results <- ExtractedArticle{
				URL:     article.URL,
				Title:   article.Title, // タイトルを渡す
				Content: content,
				Error:   finalErr, // 統一されたエラーを渡す
			}
		}(article)
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

		articleTitle := res.Title
		if articleTitle == "" {
			articleTitle = res.URL // タイトルがない場合のフォールバック
		}

		// 修正: URLではなく記事タイトルを使用
		combinedTextBuilder.WriteString(fmt.Sprintf("## 【記事タイトル】 %s\n\n", articleTitle))
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n")
		successCount++
	}
	fmt.Fprintf(os.Stderr, "✅ 抽出完了。成功件数: %d / 処理件数: %d\n", successCount, len(articlesToProcess))

	// 5. 結合テキストの出力 (AI処理スキップ)
	combinedText := combinedTextBuilder.String()
	if combinedText == "" {
		return fmt.Errorf("処理すべき記事本文が見つかりませんでした")
	}

	fmt.Fprintln(os.Stderr, "\n--- スクリプト生成結果 (AI処理スキップ) ---")

	// 修正: 結合されたテキストを []byte に変換して iohandler に渡す
	return iohandler.WriteOutput("", []byte(combinedText))
}

// ----------------------------------------------------------------------
// Cobra コマンド定義
// ----------------------------------------------------------------------

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	// LLM APIキーはAI処理スキップ中は使用されないため、チェックを緩和
	if Flags.LLMAPIKey == "" {
		Flags.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
		// キーがなくても実行続行
	}

	return runFeedExtraction(Flags.FeedURL, Flags.Parallel, Flags.ScrapeTimeout)
}

// addRunFlags は 'run' コマンドに固有のフラグを設定します。
func addRunFlags(runCmd *cobra.Command) {
	runCmd.Flags().StringVarP(&Flags.LLMAPIKey, "llm-api-key", "k", "", "Gemini APIキー (AI処理スキップ中は使用されません)")
	runCmd.Flags().StringVarP(&Flags.FeedURL, "feed-url", "f", "https://news.yahoo.co.jp/rss/categories/it.xml", "処理対象のRSSフィードURL")
	runCmd.Flags().IntVarP(&Flags.Parallel, "parallel", "p", 10, "Webスクレイピングの最大同時並列リクエスト数")
	runCmd.Flags().DurationVarP(&Flags.ScrapeTimeout, "scraper-timeout", "s", 15*time.Second, "WebスクレイピングのHTTPタイムアウト時間")
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "RSSフィードの取得、並列抽出を実行します。",
	Long:  "RSSフィードからURLを抽出し、記事本文を並列で取得します。",
	RunE:  runCmdFunc,
}

// Execute は、CLIアプリケーションのエントリポイントです。
func Execute() {
	addRunFlags(runCmd)
	clibase.Execute(
		"act-feed-clean-go",
		nil,
		nil,
		runCmd,
	)
}
