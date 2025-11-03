package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	clibase "github.com/shouni/go-cli-base"
	"github.com/spf13/cobra"

	"act-feed-clean-go/pkg/pipeline"
	"github.com/shouni/go-http-kit/pkg/httpkit"
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

// ----------------------------------------------------------------------
// Cobra コマンド定義
// ----------------------------------------------------------------------

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	// APIキーのチェック（AI処理スキップ中は使用されないため、チェックを緩和）
	if Flags.LLMAPIKey == "" {
		Flags.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
		// キーがなくても実行続行
	}

	// 1. HTTPクライアントの初期化 (Pipelineへの依存性注入の準備)
	const maxRetries = 3
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	// フラグで指定されたタイムアウトを使用
	httpClient := httpkit.New(Flags.ScrapeTimeout, clientOptions...)

	// 2. Pipelineの初期化と依存性の注入
	// HTTPクライアントと並列数をPipelineに渡す
	pipelineInstance, err := pipeline.New(httpClient, Flags.Parallel)
	if err != nil {
		// Extractorの初期化エラーなどを捕捉
		return fmt.Errorf("パイプラインの初期化に失敗しました: %w", err)
	}

	// 3. Pipelineの実行
	// 実行コンテキストと全体タイムアウトを設定
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ロジック全体を Pipeline.Run に委譲
	return pipelineInstance.Run(ctx, Flags.FeedURL)
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
	// clibase.Execute を使用して、ルートコマンドとサブコマンドをシンプルに実行
	clibase.Execute(
		"act-feed-clean-go",
		nil, // ルート共通フラグの追加はなし
		nil, // 実行前処理はなし
		runCmd,
	)
}
