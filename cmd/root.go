package cmd

import (
	"context"
	"errors"
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

// ... (RunFlags, Flags, Consts, initLogger, normalizeFlags は変更なし)

// グローバルなオプションインスタンス。
var opts pipeline.GenerateOptions

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
	SummaryModelName   string
	ScriptModelName    string
}

var Flags RunFlags

const (
	maxRetries     = 3
	contextTimeout = 20 * time.Minute
)

// initLogger はアプリケーションのデフォルトロガーを設定します。
// ... (中略: initLogger, normalizeFlags, initLLMClient は変更なし) ...
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

func normalizeFlags(f *RunFlags) {
	if f.LLMAPIKey == "" {
		f.LLMAPIKey = os.Getenv("GEMINI_API_KEY")
	}
	if f.VoicevoxAPIURL == "" {
		f.VoicevoxAPIURL = os.Getenv("VOICEVOX_API_URL")
	}
}

func initLLMClient(ctx context.Context, apiKey string) (*gemini.Client, error) {
	if apiKey != "" {
		return gemini.NewClient(ctx, gemini.Config{APIKey: apiKey})
	}
	return gemini.NewClientFromEnv(ctx)
}

// ----------------------------------------------------------------------
// 新しいヘルパー関数
// ----------------------------------------------------------------------

// createHTTPClient は HTTP クライアントの初期化ロジックを分離します。
// (行番号 120 相当の修正)
func createHTTPClient(scrapeTimeout time.Duration) *httpkit.Client {
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	return httpkit.New(scrapeTimeout, clientOptions...)
}

// initializeVoicevoxEngine は VOICEVOX Engineの初期化を独立させます。
func initializeVoicevoxEngine(ctx context.Context, config *pipeline.PipelineConfig) (*voicevox.Engine, error) {
	if config.VoicevoxAPIURL == "" {
		return nil, nil // VOICEVOXを使用しない
	}

	slog.Info("VOICEVOXクライアントを初期化します", slog.String("url", config.VoicevoxAPIURL))

	vvClient := voicevox.NewClient(config.VoicevoxAPIURL, config.VoicevoxAPITimeout)

	// 修正: ロード処理のコンテキストを親コンテキスト (ctx) から派生させる
	// (行番号 109 相当の修正)
	loadCtx, cancel := context.WithTimeout(ctx, config.VoicevoxAPITimeout)
	defer cancel()

	speakerData, loadErr := voicevox.LoadSpeakers(loadCtx, vvClient)
	if loadErr != nil {
		slog.Error("VOICEVOX話者データのロードに失敗しました", slog.String("error", loadErr.Error()))
		return nil, fmt.Errorf("VOICEVOX話者データのロードに失敗しました: %w", loadErr)
	}
	slog.Info("VOICEVOXスタイルデータのロード完了。")

	parser := voicevox.NewTextParser()
	engineConfig := voicevox.EngineConfig{
		MaxParallelSegments: voicevox.DefaultMaxParallelSegments,
		SegmentTimeout:      voicevox.DefaultSegmentTimeout,
	}

	return voicevox.NewEngine(vvClient, speakerData, parser, engineConfig), nil
}

// ----------------------------------------------------------------------
// 依存関係構築
// ----------------------------------------------------------------------

// appDependencies はパイプライン実行に必要な全ての依存関係を保持する構造体です。
type appDependencies struct {
	Extractor      *extract.Extractor
	Scraper        scraper.Scraper
	Cleaner        *cleaner.Cleaner
	VoicevoxEngine *voicevox.Engine
	HTTPClient     *httpkit.Client
	PipelineConfig pipeline.PipelineConfig
}

// newAppDependencies は全ての依存関係の構築（ワイヤリング）を実行します。
func newAppDependencies(ctx context.Context, f RunFlags) (*appDependencies, error) {

	// 1. HTTPクライアントの初期化 (ヘルパー関数を使用)
	httpClient := createHTTPClient(f.ScrapeTimeout)
	slog.Debug("HTTPクライアントを初期化しました", slog.Duration("timeout", f.ScrapeTimeout))

	// PipelineConfig 構造体を組み立て
	config := pipeline.PipelineConfig{
		Verbose:            clibase.Flags.Verbose,
		Parallel:           f.Parallel,
		LLMAPIKey:          f.LLMAPIKey,
		VoicevoxAPIURL:     f.VoicevoxAPIURL,
		OutputWAVPath:      f.OutputWAVPath,
		ScrapeTimeout:      f.ScrapeTimeout,
		VoicevoxAPITimeout: f.VoicevoxAPITimeout,
	}

	// 2. Extractorの初期化
	extractor, err := extract.NewExtractor(httpClient)
	if err != nil {
		slog.Error("エクストラクタの初期化に失敗しました", slog.String("error", err.Error()))
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 3. Scraperの初期化
	scraperInstance := scraper.NewParallelScraper(extractor, config.Parallel)

	// 4. LLM Client & Cleanerの初期化
	client, err := initLLMClient(ctx, config.LLMAPIKey)
	if err != nil {
		slog.Error("LLMクライアントの初期化に失敗しました。APIキーが設定されているか確認してください", slog.String("error", err.Error()))
		return nil, fmt.Errorf("LLMクライアントの初期化に失敗しました: %w", err)
	}

	cleanerConfig := cleaner.CleanerConfig{
		MapModel:     f.MapModelName,
		ReduceModel:  f.ReduceModelName,
		SummaryModel: f.SummaryModelName,
		ScriptModel:  f.ScriptModelName,
		Verbose:      config.Verbose,
	}

	cleanerInstance, err := cleaner.NewCleaner(
		client,
		cleanerConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	// 5. VOICEVOX Engineの初期化 (ヘルパー関数を使用)
	vvEngine, err := initializeVoicevoxEngine(ctx, &config)
	if err != nil {
		return nil, err
	}

	return &appDependencies{
		HTTPClient:     httpClient,
		Extractor:      extractor,
		Scraper:        scraperInstance,
		Cleaner:        cleanerInstance,
		VoicevoxEngine: vvEngine,
		PipelineConfig: config,
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

	normalizeFlags(&Flags)

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
		deps.VoicevoxEngine,
		deps.PipelineConfig,
	)

	// 3. Pipelineの実行
	return pipelineInstance.Run(ctx, Flags.FeedURL)
}

// ... (addRunFlags, runCmd, Execute は変更なし) ...
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "RSSフィードの取得、並列抽出、AI構造化処理を実行します。",
	Long:  "RSSフィードからURLを抽出し、記事本文を並列で取得後、LLMでクリーンアップ・構造化します。",
	RunE:  runCmdFunc,
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

	runCmd.Flags().StringVar(&Flags.MapModelName,
		"map-model", cleaner.DefaultMapModelName, "Mapフェーズ (クリーンアップ) に使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.ReduceModelName,
		"reduce-model", cleaner.DefaultReduceModelName, "Reduceフェーズ (スクリプト生成) に使用するAIモデル名 (例: gemini-2.5-pro)。")
	runCmd.Flags().StringVar(&Flags.SummaryModelName,
		"summary-model", cleaner.DefaultSummaryModelName, "最終要約フェーズに使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.ScriptModelName,
		"script-model", cleaner.DefaultScriptModelName, "スクリプト生成フェーズに使用するAIモデル名 (例: gemini-2.5-pro)。")
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
