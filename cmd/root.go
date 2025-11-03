package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	clibase "github.com/shouni/go-cli-base"
	"github.com/spf13/cobra"

	"act-feed-clean-go/pkg/cleaner"
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
	MapModelName       string
	ReduceModelName    string
}

var Flags RunFlags

// ----------------------------------------------------------------------
// Cobra コマンド定義
// ----------------------------------------------------------------------

// initLogger はアプリケーションのデフォルトロガーを設定します。
func initLogger() {
	logLevel := slog.LevelInfo
	// clibase のグローバルな Verbose フラグに依存
	if clibase.Flags.Verbose {
		logLevel = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				// ログサービス (例: Cloud Logging) がタイムスタンプを自動付与するため、
				// 重複を避ける目的でタイムスタンプを削除します。
				return slog.Attr{}
			}
			// Timeキー以外の属性はそのまま返す
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
	slog.Info("ロガーを初期化しました", slog.String("level", logLevel.String()))
}

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// ログの初期化をここで行う (DIコンポーネントの構築前に実行する必要がある)
	initLogger()

	// APIキーのチェック（環境変数から取得を試みる）
	if Flags.LLMAPIKey == "" {
		Flags.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
	}

	// VOICEVOX API URLのチェック（環境変数から取得を試みる）
	if Flags.VoicevoxAPIURL == "" {
		Flags.VoicevoxAPIURL = os.Getenv("VOICEVOX_API_URL")
	}

	// 1. HTTPクライアントの初期化
	// TODO: clibase.NewHTTPClient() が利用可能になったら、そちらに切り替えることを検討
	const maxRetries = 3
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	// ScrapeTimeoutをベースタイムアウトとして使用
	httpClient := httpkit.New(Flags.ScrapeTimeout, clientOptions...)
	slog.Debug("HTTPクライアントを初期化しました", slog.Duration("timeout", Flags.ScrapeTimeout))

	// PipelineConfig 構造体を組み立て
	config := pipeline.PipelineConfig{
		Verbose:            clibase.Flags.Verbose,
		Parallel:           Flags.Parallel,
		LLMAPIKey:          Flags.LLMAPIKey,
		VoicevoxAPIURL:     Flags.VoicevoxAPIURL,
		OutputWAVPath:      Flags.OutputWAVPath,
		ScrapeTimeout:      Flags.ScrapeTimeout,
		VoicevoxAPITimeout: Flags.VoicevoxAPITimeout,
		MapModelName:       Flags.MapModelName,
		ReduceModelName:    Flags.ReduceModelName,
	}

	// 2. 依存関係の構築 (DIの準備)
	extractor, scraper, cleaner, vvEngine, err := pipeline.NewPipelineDependencies(httpClient, config)
	if err != nil {
		slog.Error("パイプライン依存関係の構築に失敗しました", slog.String("error", err.Error()))
		return fmt.Errorf("パイプライン依存関係の構築に失敗しました: %w", err)
	}

	// 3. Pipelineインスタンスを生成（依存関係を注入）
	pipelineInstance := pipeline.New(
		httpClient,
		extractor,
		scraper,
		cleaner,
		vvEngine,
		config,
	)

	// 4. Pipelineの実行
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
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

	// AIモデル名オプションの追加
	runCmd.Flags().StringVar(&Flags.MapModelName,
		"map-model", cleaner.DefaultMapModelName, "Mapフェーズ (クリーンアップ) に使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.ReduceModelName,
		"reduce-model", cleaner.DefaultReduceModelName, "Reduceフェーズ (スクリプト生成) に使用するAIモデル名 (例: gemini-2.5-pro)。")
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
	clibase.Execute(
		"act-feed-clean-go",
		nil,
		nil,
		runCmd,
	)
}
