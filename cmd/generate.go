package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"act-feed-clean-go/pkg/cleaner"
	"act-feed-clean-go/pkg/pipeline"
	"act-feed-clean-go/pkg/scraper"

	"github.com/shouni/go-ai-client/v2/pkg/ai/gemini"
	"github.com/shouni/go-cli-base"
	"github.com/shouni/go-http-kit/pkg/httpkit"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// 構造体と定数

// appDependencies はパイプライン実行に必要な全ての依存関係を保持する構造体です。
type appDependencies struct {
	Extractor              *extract.Extractor
	Scraper                scraper.Scraper
	Cleaner                *cleaner.Cleaner
	VoicevoxEngineExecutor voicevox.EngineExecutor
	HTTPClient             *httpkit.Client
	PipelineConfig         pipeline.PipelineConfig
}

// ヘルパー関数 (初期化)

// createHTTPClient は HTTP クライアントの初期化ロジックを分離します。
func createHTTPClient(scrapeTimeout time.Duration) *httpkit.Client {
	// maxRetries を関数スコープに移動し、可読性と安全性を向上
	const maxRetries = 3
	clientOptions := []httpkit.ClientOption{
		httpkit.WithMaxRetries(maxRetries),
	}
	return httpkit.New(scrapeTimeout, clientOptions...)
}

// 依存関係構築 (メイン責務)

// newAppDependencies は全ての依存関係の構築（ワイヤリング）を実行します。
// フラグ情報は引数 f から一貫して取得されます。
func newAppDependencies(ctx context.Context, f RunFlags) (*appDependencies, error) {

	// 1. HTTPクライアントの初期化
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

	// グローバル変数ではなく、引数 f から CleanerConfig を使用
	cleanerInstance, err := cleaner.NewCleaner(
		client,
		f.CleanerConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	// 5. VOICEVOX Engineの初期化
	// グローバル変数ではなく、引数 f から HttpTimeout を使用
	voicevoxExecutor, err := voicevox.NewEngineExecutor(ctx, f.HttpTimeout, config.OutputWAVPath != "")
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
