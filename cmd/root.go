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
	LLMAPIKey          string
	FeedURL            string
	Parallel           int
	ScrapeTimeout      time.Duration
	VoicevoxAPIURL     string
	OutputWAVPath      string
	VoicevoxAPITimeout time.Duration
}

var Flags RunFlags

// ----------------------------------------------------------------------
// Cobra コマンド定義
// ----------------------------------------------------------------------

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	// APIキーのチェック（環境変数から取得を試みる）
	if Flags.LLMAPIKey == "" {
		Flags.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
	}

	// VOICEVOX API URLのチェック（環境変数から取得を試みる）
	if Flags.VoicevoxAPIURL == "" {
		Flags.VoicevoxAPIURL = os.Getenv("VOICEVOX_API_URL")
	}

	// 1. HTTPクライアントの初期化
	const maxRetries = 3
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	// スクレ―ピングとVOICEVOXクライアントでタイムアウトが異なるため、ここでは基盤となるタイムアウトは使用しない
	// タイムアウトは各パイプライン内で設定される
	httpClient := httpkit.New(Flags.ScrapeTimeout, clientOptions...)

	// PipelineConfig 構造体を組み立て
	config := pipeline.PipelineConfig{
		Parallel:           Flags.Parallel,
		Verbose:            clibase.Flags.Verbose,
		LLMAPIKey:          Flags.LLMAPIKey,
		VoicevoxAPIURL:     Flags.VoicevoxAPIURL,
		OutputWAVPath:      Flags.OutputWAVPath,
		ScrapeTimeout:      Flags.ScrapeTimeout,
		VoicevoxAPITimeout: Flags.VoicevoxAPITimeout,
	}

	// 2. Pipelineの初期化と依存性の注入
	pipelineInstance, err := pipeline.New(httpClient, config)
	if err != nil {
		// Extractor, Cleanerの初期化エラーなどを捕捉
		return fmt.Errorf("パイプラインの初期化に失敗しました: %w", err)
	}

	// 3. Pipelineの実行
	// 処理が長くなる可能性があるため、長めのタイムアウトを設定
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	return pipelineInstance.Run(ctx, Flags.FeedURL)
}

// addRunFlags は 'run' コマンドに固有のフラグを設定します。
func addRunFlags(runCmd *cobra.Command) {
	runCmd.Flags().StringVarP(&Flags.LLMAPIKey,
		"llm-api-key", "k", "", "Gemini APIキー (これが設定されている場合のみAI処理が実行されます)")
	runCmd.Flags().StringVarP(&Flags.FeedURL,
		"feed-url", "f", "https://news.yahoo.co.jp/rss/categories/it.xml", "処理対象のRSSフィードURL")
	runCmd.Flags().IntVarP(&Flags.Parallel,
		"parallel", "p", 10, "Webスクレイピングの最大同時並列リクエスト数")
	runCmd.Flags().DurationVarP(&Flags.ScrapeTimeout,
		"scraper-timeout", "s", 15*time.Second, "WebスクレイピングのHTTPタイムアウト時間")
	runCmd.Flags().StringVar(&Flags.VoicevoxAPIURL,
		"voicevox-api-url", "", "VOICEVOXエンジンのAPI URL。環境変数からも読み込みます。")
	runCmd.Flags().DurationVar(&Flags.VoicevoxAPITimeout,
		"voicevox-api-timeout", 30*time.Second, "VOICEVOX API (audio_query, synthesis) のHTTPタイムアウト時間")
	runCmd.Flags().StringVarP(&Flags.OutputWAVPath,
		"output-wav-path", "v", "asset/audio_output.wav", "音声合成されたWAVファイルの出力パス。")
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "RSSフィードの取得、並列抽出、AI構造化処理を実行します。",
	Long:  "RSSフィードからURLを抽出し、記事本文を並列で取得後、LLMでクリーンアップ・構造化します。",
	RunE:  runCmdFunc,
}

// Execute は、CLIアプリケーションのエントリポイントです。
func Execute() {
	addRunFlags(runCmd)
	// clibase.Executeは、rootCmdを内部で設定
	clibase.Execute(
		"act-feed-clean-go",
		nil,
		nil,
		runCmd,
	)
}
