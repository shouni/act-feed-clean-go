package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"act-feed-clean-go/pkg/cleaner"
	//	"act-feed-clean-go/pkg/types"

	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/go-web-exact/v2/pkg/types"
	"github.com/shouni/web-text-pipe-go/pkg/scraper/runner"
)

// PipelineConfig はパイプライン実行のためのすべての設定値を保持します。
type PipelineConfig struct {
	Parallel      int
	Verbose       bool
	OutputWAVPath string
}

// Pipeline は記事の取得から結合までの一連の流れを管理します。
type Pipeline struct {
	ScraperRunner          *runner.Runner
	Cleaner                *cleaner.Cleaner
	VoicevoxEngineExecutor voicevox.EngineExecutor
	config                 PipelineConfig
}

// New 関数
func New(
	ScraperRunner *runner.Runner,
	cleanerInstance *cleaner.Cleaner,
	VoicevoxEngineExecutor voicevox.EngineExecutor,
	config PipelineConfig,
) *Pipeline {
	return &Pipeline{
		ScraperRunner:          ScraperRunner, // そのままポインタを格納
		Cleaner:                cleanerInstance,
		VoicevoxEngineExecutor: VoicevoxEngineExecutor,
		config:                 config,
	}
}

// Run はフィードの取得、記事の並列抽出、AI処理、およびI/O処理を実行します。
func (p *Pipeline) Run(ctx context.Context, feedURL string) error {

	runnerConfig := runner.RunnerConfig{
		FeedURL:                  feedURL,
		ClientTimeout:            3 * time.Second,
		OverallTimeoutMultiplier: 3,
	}

	// --- 1. ScrapeAndRun の呼び出し ---
	results, err := p.ScraperRunner.ScrapeAndRun(ctx, runnerConfig)
	if err != nil {
		return err
	}

	// --- 2. 抽出結果の確認と成功リストの作成 ---
	successCount := 0
	var successfulResults []types.URLResult

	// NOTE: 新しいアーキテクチャでは、フィードのメタデータ（feedTitle, articleTitlesMap）は
	// ScrapeAndRunの外部からは直接取得できません。
	// この修正では、AI処理分岐のために必要なこれらの変数を「ダミー」または「未取得」として扱い、
	// AI処理をスキップするか、呼び出し元のロジックに依存しない形で結合処理を行います。

	// 暫定的なダミー値を使用 (AI処理/processWithoutAIで必要とされるため)
	feedTitle := "スクレイピング結果"
	articleTitlesMap := make(map[string]string)

	totalProcessedURLs := len(results) // ScrapeAndRunで処理されたURLの総数

	for _, res := range results {
		if res.Error == nil {
			successCount++
			successfulResults = append(successfulResults, res) // 成功した結果を格納
			// 暫定的にURLをキーとして、Contentをタイトルと仮定（正確なタイトルは取得不可）
			// または、後続の処理でタイトルが必要ない場合はこの行を削除
			articleTitlesMap[res.URL] = fmt.Sprintf("記事タイトル (URL: %s)", res.URL)
		} else {
			slog.Warn("抽出エラー",
				slog.String("url", res.URL),
				slog.String("error", res.Error.Error()),
			)
		}
	}

	slog.Info("抽出完了",
		slog.Int("success", successCount),
		slog.Int("total", totalProcessedURLs), // urlsToScrapeではなくresultsの長さを使用
	)

	if successCount == 0 {
		return fmt.Errorf("処理すべき記事本文が一つも見つかりませんでした")
	}

	// --- 3. AI処理の実行分岐 ---
	if p.Cleaner != nil {
		// LLMが利用可能な場合
		// NOTE: feedTitle, articleTitlesMap が正確な情報を含まない可能性があるため、
		// processWithAIのロジックを見直す必要があります。ここではそのまま残します。
		scriptText, err := p.processWithAI(ctx, feedTitle, successfulResults, articleTitlesMap)
		if err != nil {
			return err
		}
		// 5. 出力分岐 (AI処理結果の出力)
		return p.handleOutput(ctx, scriptText)
	}

	// LLMが利用不可の場合 (AI処理スキップ)
	slog.Info("AI処理コンポーネントが未設定のため、抽出結果を結合して出力します。", slog.String("mode", "AIスキップ"))
	// NOTE: feedTitle, articleTitlesMap が正確な情報を含まない可能性があるため、
	// processWithoutAIのロジックを見直す必要があります。ここではそのまま残します。
	combinedScriptText, err := p.processWithoutAI(feedTitle, successfulResults, articleTitlesMap)
	if err != nil {
		return err
	}
	slog.Info("AI処理スキップモードでスクリプトが正常に生成されました。", slog.String("mode", "AIスキップ"))
	// 5. 出力分岐 (AI処理スキップ結果の出力)
	return p.handleOutput(ctx, combinedScriptText)
}

// ----------------------------------------------------------------------
// ヘルパー関数 (AI処理)
// ----------------------------------------------------------------------

// processWithAI は AI による Map-Reduce、Summary、Script Generation を実行します。
func (p *Pipeline) processWithAI(ctx context.Context, feedTitle string, results []types.URLResult, titlesMap map[string]string) (string, error) {
	slog.Info("LLM処理開始", slog.String("phase", "Map-Reduce"))

	// Map-Reduce のための結合テキスト構築
	combinedTextForAI := cleaner.CombineContents(results, titlesMap)

	reduceResult, err := p.Cleaner.CleanAndStructureText(ctx, combinedTextForAI)
	if err != nil {
		slog.Error("AIによるコンテンツの構造化に失敗しました", slog.String("error", err.Error()))
		return "", fmt.Errorf("AIによるコンテンツの構造化に失敗しました: %w", err)
	}

	// Final Summary
	title := cleaner.ExtractTitleFromMarkdown(reduceResult)
	if title == "" {
		slog.Warn("AIによるタイトル抽出に失敗しました。フィードのタイトルを代替として使用します。", slog.String("fallback_title", feedTitle))
		title = feedTitle
	}

	finalSummary, err := p.Cleaner.GenerateFinalSummary(ctx, title, reduceResult)
	if err != nil {
		slog.Error("Final Summaryの生成に失敗しました", slog.String("error", err.Error()))
		return "", fmt.Errorf("Final Summaryの生成に失敗しました: %w", err)
	}

	// Script Generation
	scriptText, err := p.Cleaner.GenerateScriptForVoicevox(ctx, title, finalSummary)
	if err != nil {
		slog.Error("VOICEVOXスクリプトの生成に失敗しました", slog.String("error", err.Error()))
		return "", fmt.Errorf("VOICEVOXスクリプトの生成に失敗しました: %w", err)
	}

	return scriptText, nil
}

// ----------------------------------------------------------------------
// ヘルパー関数 (I/O処理)
// ----------------------------------------------------------------------

// handleOutput は音声合成またはテキスト出力を実行します。
func (p *Pipeline) handleOutput(ctx context.Context, scriptText string) error {
	// 5-A. VOICEVOXによる音声合成とWAV出力
	if p.VoicevoxEngineExecutor != nil && p.config.OutputWAVPath != "" {
		slog.Info("AI生成スクリプトをVOICEVOXで音声合成します", slog.String("output", p.config.OutputWAVPath))
		err := p.VoicevoxEngineExecutor.Execute(ctx, scriptText, p.config.OutputWAVPath)
		if err != nil {
			return fmt.Errorf("音声合成パイプラインの実行に失敗しました: %w", err)
		}
		slog.Info("VOICEVOXによる音声合成が完了し、ファイルに保存されました。", "output_file", p.config.OutputWAVPath)
		return nil
	}

	// 5-B. テキスト出力
	return iohandler.WriteOutputString("", scriptText)
}

// processWithoutAI は LLMAPIKeyがない場合に実行される処理
func (p *Pipeline) processWithoutAI(feedTitle string, successfulResults []types.URLResult, titlesMap map[string]string) (string, error) {
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", feedTitle))

	for _, res := range successfulResults {
		articleTitle := titlesMap[res.URL]
		if articleTitle == "" {
			slog.Warn("記事タイトルが見つかりませんでした。URLを使用します。", slog.String("url", res.URL))
			articleTitle = res.URL // または "不明なタイトル" など、適切なフォールバック
		}
		combinedTextBuilder.WriteString(fmt.Sprintf("## %s\n\n", articleTitle))
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n")
	}
	return combinedTextBuilder.String(), nil
}
