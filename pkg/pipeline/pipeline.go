package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"act-feed-clean-go/pkg/cleaner"
	"act-feed-clean-go/pkg/feed"
	"act-feed-clean-go/pkg/scraper"
	"act-feed-clean-go/pkg/types"

	"github.com/shouni/go-http-kit/pkg/httpkit"
	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// Pipeline は記事の取得から結合までの一連の流れを管理します。
type Pipeline struct {
	Client    *httpkit.Client
	Extractor *extract.Extractor

	Scraper scraper.Scraper
	Cleaner *cleaner.Cleaner

	// 設定値
	Parallel  int
	Verbose   bool
	LLMAPIKey string // LLM処理のためにAPIキーを保持
}

// New は新しい Pipeline インスタンスを初期化し、依存関係を注入します。
func New(client *httpkit.Client, parallel int, verbose bool, llmAPIKey string) (*Pipeline, error) {

	// ログ設定: slog.Handlerの選択と設定
	// Verboseが設定されている場合、ログレベルをDebug以上にする
	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}

	// TextHandler (ログをkey=value形式で出力) を使用
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		// Timeの表示を抑制
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))

	// 1. Extractorの初期化 (Scraperが依存)
	extractor, err := extract.NewExtractor(client)
	if err != nil {
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 2. Scraperの初期化 (並列処理ロジックをカプセル化)
	parallelScraper := scraper.NewParallelScraper(extractor, parallel)

	// 3. Cleanerの初期化 (AI処理ロジックをカプセル化)
	const defaultMapModel = cleaner.DefaultModelName
	const defaultReduceModel = cleaner.DefaultModelName
	llmCleaner, err := cleaner.NewCleaner(defaultMapModel, defaultReduceModel, verbose)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	return &Pipeline{
		Client:    client,
		Extractor: extractor,
		Scraper:   parallelScraper,
		Cleaner:   llmCleaner,
		Parallel:  parallel,
		Verbose:   verbose,
		LLMAPIKey: llmAPIKey,
	}, nil
}

// Run はフィードの取得、記事の並列抽出、AI処理、およびI/O処理を実行します。
func (p *Pipeline) Run(ctx context.Context, feedURL string) error {

	// --- 1. RSSフィードの取得とURLリスト生成 ---
	rssFeed, err := feed.FetchAndParse(ctx, p.Client, feedURL)
	if err != nil {
		slog.Error("RSSフィードの取得・パースに失敗しました", slog.String("error", err.Error()))
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	urlsToScrape := make([]string, 0, len(rssFeed.Items))
	articleTitlesMap := make(map[string]string)

	for _, item := range rssFeed.Items {
		if item.Link != "" && item.Title != "" {
			urlsToScrape = append(urlsToScrape, item.Link)
			articleTitlesMap[item.Link] = item.Title
		}
	}

	if len(urlsToScrape) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	// ログ出力の修正: slog.Infoを使用し、構造化データとして情報を付加
	slog.Info("記事URLの抽出を開始します",
		slog.Int("urls", len(urlsToScrape)),
		slog.Int("parallel", p.Parallel),
		slog.String("feed_url", feedURL),
	)

	// --- 2. Scraperによる並列抽出の実行 ---
	results := p.Scraper.ScrapeInParallel(ctx, urlsToScrape)

	// --- 3. 抽出結果の確認とAI処理の分岐 ---
	successCount := 0
	for _, res := range results {
		if res.Error == nil {
			successCount++
		} else {
			// Verboseフラグに関わらず、エラーはWarnレベルで出力
			// Verboseはログレベルで制御されるため、この条件分岐は不要
			// ただし、今回はユーザーの意図を反映し、Verboseモードでのみエラー詳細を表示
			if p.Verbose {
				slog.Warn("抽出エラー",
					slog.String("url", res.URL),
					slog.String("error", res.Error.Error()),
				)
			}
		}
	}

	// ログ出力の修正: slog.Info
	slog.Info("抽出完了",
		slog.Int("success", successCount),
		slog.Int("total", len(urlsToScrape)),
	)

	if successCount == 0 {
		return fmt.Errorf("処理すべき記事本文が一つも見つかりませんでした")
	}

	// AI処理をスキップするかどうかをLLMAPIKeyの有無で判断
	if p.LLMAPIKey == "" {
		return p.processWithoutAI(rssFeed.Title, results, articleTitlesMap)
	}

	// --- 4. AI処理の実行 (Cleanerによる Map-Reduce) ---
	// ログ出力の修正: slog.Info
	slog.Info("LLM処理開始", slog.String("phase", "Map-Reduce"))

	// 4-1. コンテンツの結合
	combinedTextForAI := cleaner.CombineContents(results)

	// 4-2. クリーンアップと構造化の実行
	structuredText, err := p.Cleaner.CleanAndStructureText(ctx, combinedTextForAI, p.LLMAPIKey)
	if err != nil {
		slog.Error("AIによるコンテンツの構造化に失敗しました", slog.String("error", err.Error()))
		return fmt.Errorf("AIによるコンテンツの構造化に失敗しました: %w", err)
	}

	// --- 5. AI処理結果の出力 ---
	slog.Info("スクリプト生成完了", slog.String("mode", "AI構造化済み"))
	return iohandler.WriteOutput("", []byte(structuredText))
}

// processWithoutAI は LLMAPIKeyがない場合に実行される処理
func (p *Pipeline) processWithoutAI(feedTitle string, results []types.URLResult, titlesMap map[string]string) error {
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", feedTitle))

	for _, res := range results {
		if res.Error != nil {
			// AI処理スキップモードでも失敗したURLを通知
			slog.Warn("抽出失敗",
				slog.String("url", res.URL),
				slog.String("mode", "AI処理スキップ"),
			)
			continue
		}

		articleTitle := titlesMap[res.URL]
		if articleTitle == "" {
			articleTitle = res.URL
		}

		// 記事タイトルと本文を結合
		combinedTextBuilder.WriteString(fmt.Sprintf("## 【記事タイトル】 %s\n\n", articleTitle))
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n")
	}

	combinedText := combinedTextBuilder.String()

	slog.Info("スクリプト生成結果", slog.String("mode", "AI処理スキップ"))

	return iohandler.WriteOutput("", []byte(combinedText))
}
