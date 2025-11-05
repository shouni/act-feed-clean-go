package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"act-feed-clean-go/pkg/cleaner"
	"act-feed-clean-go/pkg/pipeline"
	"act-feed-clean-go/pkg/scraper"

	"github.com/shouni/go-ai-client/v2/pkg/ai/gemini"
	"github.com/shouni/go-cli-base"
	"github.com/shouni/go-http-kit/pkg/httpkit"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
	"github.com/spf13/cobra"
)

// ----------------------------------------------------------------------
// 構造体と定数
// ----------------------------------------------------------------------

// RunFlags は 'run' コマンド固有のフラグを保持する構造体です。
type RunFlags struct {
	FeedURL       string
	Parallel      int
	HttpTimeout   time.Duration
	OutputWAVPath string
	CleanerConfig cleaner.CleanerConfig
}

var Flags RunFlags

const (
	maxRetries = 3
	// contextTimeout は、パイプライン全体の実行に許容される最大時間です。
	contextTimeout = 20 * time.Minute
)

// appDependencies はパイプライン実行に必要な全ての依存関係を保持する構造体です。
type appDependencies struct {
	Extractor              *extract.Extractor
	Scraper                scraper.Scraper
	Cleaner                *cleaner.Cleaner
	VoicevoxEngineExecutor voicevox.EngineExecutor
	HTTPClient             *httpkit.Client
	PipelineConfig         pipeline.PipelineConfig
}

// ----------------------------------------------------------------------
// ヘルパー関数 (ロギング、正規化、初期化)
// ----------------------------------------------------------------------

// initLogger はアプリケーションのデフォルトロガーを設定します。
func initLogger() {
	logLevel := slog.LevelInfo
	if clibase.Flags.Verbose {
		logLevel = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
	slog.Info("ロガーを初期化しました", slog.String("level", logLevel.String()))
}

// createHTTPClient は HTTP クライアントの初期化ロジックを分離します。
func createHTTPClient(scrapeTimeout time.Duration) *httpkit.Client {
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	return httpkit.New(scrapeTimeout, clientOptions...)
}

// ----------------------------------------------------------------------
// 依存関係構築
// ----------------------------------------------------------------------

// newAppDependencies は全ての依存関係の構築（ワイヤリング）を実行します。
func newAppDependencies(ctx context.Context, f RunFlags) (*appDependencies, error) {

	// 1. HTTPクライアントの初期化 (ヘルパー関数を使用)
	httpClient := createHTTPClient(f.HttpTimeout)
	slog.Debug("HTTPクライアントを初期化しました", slog.Duration("timeout", f.HttpTimeout))

	// PipelineConfig 構造体を組み立て
	config := pipeline.PipelineConfig{
		Verbose:       clibase.Flags.Verbose,
		Parallel:      f.Parallel,
		OutputWAVPath: f.OutputWAVPath,
	}

	// 2. Extractorの初期化
	extractor, err := extract.NewExtractor(httpClient)
	if err != nil {
		slog.Error("エクストラクタの初期化に失敗しました", slog.String("error", err.Error()))
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 3. Scraperの初期化
	scraperInstance := scraper.NewParallelScraper(extractor, config.Parallel)

	// 4. geminiの初期化
	client, err := gemini.NewClientFromEnv(ctx)
	if err != nil {
		slog.Error("LLMクライアントの初期化に失敗しました。APIキーが設定されているか確認してください", slog.String("error", err.Error()))
		return nil, fmt.Errorf("LLMクライアントの初期化に失敗しました: %w", err)
	}

	cleanerInstance, err := cleaner.NewCleaner(
		client,
		Flags.CleanerConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	// 5. VOICEVOX Engineの初期化
	voicevoxExecutor, err := voicevox.NewEngineExecutor(ctx, Flags.HttpTimeout, config.OutputWAVPath != "")
	if err != nil {
		return nil, err
	}

	return &appDependencies{
		HTTPClient:             httpClient,
		Extractor:              extractor,
		Scraper:                scraperInstance,
		Cleaner:                cleanerInstance,
		VoicevoxEngineExecutor: voicevoxExecutor,
		PipelineConfig:         config,
	}, nil
}

// ----------------------------------------------------------------------
// Cobra コマンド実行関数
// ----------------------------------------------------------------------

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	parentCtx := cmd.Context()
	ctx, cancel := context.WithTimeout(parentCtx, contextTimeout)
	defer cancel()

	initLogger()

	// 1. 依存関係の構築（ヘルパー関数に委譲）
	deps, err := newAppDependencies(ctx, Flags)
	if err != nil {
		return err
	}

	// 2. Pipelineインスタンスを生成（依存関係を注入）
	pipelineInstance := pipeline.New(
		deps.HTTPClient,
		deps.Extractor,
		deps.Scraper,
		deps.Cleaner,
		deps.VoicevoxEngineExecutor,
		deps.PipelineConfig,
	)

	// 3. Pipelineの実行
	return pipelineInstance.Run(ctx, Flags.FeedURL)
}

// ----------------------------------------------------------------------
// Cobra コマンド定義 (フラグ、Execute)
// ----------------------------------------------------------------------

// addRunFlags は 'run' コマンドに固有のフラグを設定します。
func addRunFlags(runCmd *cobra.Command) {
	runCmd.Flags().StringVarP(&Flags.FeedURL,
		"feed-url", "f", "https://news.yahoo.co.jp/rss/categories/it.xml", "処理対象のRSSフィードURL")
	runCmd.Flags().IntVarP(&Flags.Parallel,
		"parallel", "p", 10, "Webスクレイピングの最大同時並列リクエスト数")
	runCmd.Flags().DurationVarP(&Flags.HttpTimeout,
		"http-timeout", "s", 30*time.Second, "HTTPタイムアウト時間")
	runCmd.Flags().StringVarP(&Flags.OutputWAVPath,
		"output-wav-path", "v", "asset/audio_output.wav", "音声合成されたWAVファイルの出力パス。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.MapModel,
		"map-model", cleaner.DefaultMapModelName, "Mapフェーズ (クリーンアップ) に使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.ReduceModel,
		"reduce-model", cleaner.DefaultReduceModelName, "Reduceフェーズ (スクリプト生成) に使用するAIモデル名 (例: gemini-2.5-pro)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.SummaryModel,
		"summary-model", cleaner.DefaultSummaryModelName, "最終要約フェーズに使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.ScriptModel,
		"script-model", cleaner.DefaultScriptModelName, "スクリプト生成フェーズに使用するAIモデル名 (例: gemini-2.5-pro)。")
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
