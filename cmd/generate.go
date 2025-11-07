package cmd

import (
	"act-feed-clean-go/internal/cleaner"
	"act-feed-clean-go/internal/pipeline"
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-ai-client/v2/pkg/ai/gemini"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/web-text-pipe-go/pkg/scraper/builder"
	"github.com/shouni/web-text-pipe-go/pkg/scraper/runner"
)

// ----------------------------------------------------------------------
// 構造体と定数
// ----------------------------------------------------------------------

// appDependencies はパイプライン実行に必要な全ての依存関係を保持する構造体です。
type appDependencies struct {
	ScraperRunner          *runner.Runner
	Cleaner                *cleaner.Cleaner
	VoicevoxEngineExecutor voicevox.EngineExecutor
	PipelineConfig         pipeline.PipelineConfig
}

// 依存関係構築 (メイン責務)

// newAppDependencies は全ての依存関係の構築（ワイヤリング）を実行します。
// フラグ情報は引数 f から一貫して取得されます。
func newAppDependencies(ctx context.Context, f RunFlags) (*appDependencies, error) {
	config := pipeline.PipelineConfig{
		Parallel:      f.Parallel,
		OutputWAVPath: f.OutputWAVPath,
		ClientTimeout: f.HttpTimeout,
	}

	// 1. scraperRunnerの初期化
	scraperRunner, err := builder.BuildScraperRunner(f.HttpTimeout, f.Parallel)
	if err != nil {
		slog.Error("scraperRunnerの初期化に失敗しました", slog.String("error", err.Error()))
		return nil, fmt.Errorf("scraperRunnerの初期化に失敗しました: %w", err)
	}

	// 2. geminiの初期化
	client, err := gemini.NewClientFromEnv(ctx)
	if err != nil {
		slog.Error("LLMクライアントの初期化に失敗しました。APIキーが設定されているか確認してください", slog.String("error", err.Error()))
		return nil, fmt.Errorf("LLMクライアントの初期化に失敗しました: %w", err)
	}

	// 3. cleanerの初期化
	cleanerInstance, err := cleaner.NewCleaner(
		client,
		f.CleanerConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	// 4. VOICEVOX Engineの初期化
	voicevoxExecutor, err := voicevox.NewEngineExecutor(ctx, f.HttpTimeout, config.OutputWAVPath != "")
	if err != nil {
		return nil, err
	}

	return &appDependencies{
		ScraperRunner:          scraperRunner,
		Cleaner:                cleanerInstance,
		VoicevoxEngineExecutor: voicevoxExecutor,
		PipelineConfig:         config,
	}, nil
}
