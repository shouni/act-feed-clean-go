package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"act-feed-clean-go/pkg/cleaner"
	"act-feed-clean-go/pkg/types"

	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// PipelineConfig はパイプライン実行のためのすべての設定値を保持します。
type PipelineConfig struct {
	Parallel      int
	Verbose       bool
	OutputWAVPath string
}

// ParsedFeed は抽出された記事のリンクとタイトル
type ParsedFeed struct {
	Link  string
	Title string
}

// FeedParser はフィードの取得とリンク抽出の責務を負う
type FeedParser interface {
	FetchAndExtractLinks(ctx context.Context, feedURL string) (feedTitle string, items []ParsedFeed, err error)
}

// ScraperExecutor は scraper.Scraper インターフェースに一致 (既存の定義を再利用)
type ScraperExecutor interface {
	ScrapeInParallel(ctx context.Context, urls []string) []types.URLResult
}

// Pipeline は記事の取得から結合までの一連の流れを管理します。
type Pipeline struct {
	// 依存関係 (Public fields)
	FeedParser FeedParser
	Extractor  *extract.Extractor
	Scraper    ScraperExecutor
	// CleanerはLLMが利用できない場合nilになり得る
	Cleaner                *cleaner.Cleaner
	VoicevoxEngineExecutor voicevox.EngineExecutor

	// 設定値 (Private) - 設定値はすべてここに集約する
	config PipelineConfig
}

// New は新しい Pipeline インスタンスを初期化し、依存関係を注入します。
func New(
	feedParser FeedParser,
	extractor *extract.Extractor,
	scraperInstance ScraperExecutor,
	cleanerInstance *cleaner.Cleaner,
	VoicevoxEngineExecutor voicevox.EngineExecutor,
	config PipelineConfig,
) *Pipeline {
	return &Pipeline{
		FeedParser:             feedParser,
		Extractor:              extractor,
		Scraper:                scraperInstance,
		Cleaner:                cleanerInstance, // LLMが利用できない場合はnilが注入される
		VoicevoxEngineExecutor: VoicevoxEngineExecutor,

		// 設定値全体を保持
		config: config,
	}
}

// Run はフィードの取得、記事の並列抽出、AI処理、およびI/O処理を実行します。
func (p *Pipeline) Run(ctx context.Context, feedURL string) error {

	// --- 1. RSSフィードの取得とURLリスト生成 ---
	feedTitle, parsedItems, err := p.FeedParser.FetchAndExtractLinks(ctx, feedURL)
	if err != nil {
		slog.Error("RSSフィードの取得・パース・リンク抽出に失敗しました", slog.String("error", err.Error()))
		return fmt.Errorf("RSSフィードの取得・パース・リンク抽出に失敗しました: %w", err)
	}

	urlsToScrape := make([]string, 0, len(parsedItems))
	articleTitlesMap := make(map[string]string)

	for _, item := range parsedItems {
		if item.Link != "" && item.Title != "" {
			urlsToScrape = append(urlsToScrape, item.Link)
			articleTitlesMap[item.Link] = item.Title
		}
	}
	if len(urlsToScrape) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	slog.Info("記事URLの抽出を開始します",
		slog.Int("urls", len(urlsToScrape)),
		slog.Int("parallel", p.config.Parallel),
		slog.String("feed_url", feedURL),
	)

	// --- 2. Scraperによる並列抽出の実行 ---
	results := p.Scraper.ScrapeInParallel(ctx, urlsToScrape)

	// --- 3. 抽出結果の確認と成功リストの作成 ---
	successCount := 0
	var successfulResults []types.URLResult

	for _, res := range results {
		if res.Error == nil {
			successCount++
			successfulResults = append(successfulResults, res) // 成功した結果を格納
		} else {
			slog.Warn("抽出エラー",
				slog.String("url", res.URL),
				slog.String("error", res.Error.Error()),
			)
		}
	}

	slog.Info("抽出完了",
		slog.Int("success", successCount),
		slog.Int("total", len(urlsToScrape)),
	)

	if successCount == 0 {
		return fmt.Errorf("処理すべき記事本文が一つも見つかりませんでした")
	}

	// --- 4. AI処理の実行分岐 ---
	if p.Cleaner != nil {
		// LLMが利用可能な場合
		scriptText, err := p.processWithAI(ctx, feedTitle, successfulResults, articleTitlesMap)
		if err != nil {
			return err
		}
		// 5. 出力分岐 (AI処理結果の出力)
		return p.handleOutput(ctx, scriptText)
	}

	// LLMが利用不可の場合 (AI処理スキップ)
	slog.Info("AI処理コンポーネントが未設定のため、抽出結果を結合して出力します。", slog.String("mode", "AIスキップ"))
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
