package pipeline

import (
	"context"
	"fmt"
	"log"
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
	LLMAPIKey string // 修正: LLM処理のためにAPIキーを保持
}

// New は新しい Pipeline インスタンスを初期化し、依存関係を注入します。
// LLMAPIKeyはcmd/root.goから渡されます。
func New(client *httpkit.Client, parallel int, verbose bool, llmAPIKey string) (*Pipeline, error) { // 修正: llmAPIKey引数を追加

	// 1. Extractorの初期化 (Scraperが依存)
	extractor, err := extract.NewExtractor(client)
	if err != nil {
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 2. Scraperの初期化 (並列処理ロジックをカプセル化)
	parallelScraper := scraper.NewParallelScraper(extractor, parallel)

	// 3. Cleanerの初期化 (AI処理ロジックをカプセル化)
	// 修正: NewCleanerにデフォルトモデル名とverboseフラグを渡す
	// NOTE: モデル名をCLIから設定するフラグがないため、デフォルト名を使用
	const defaultMapModel = cleaner.DefaultModelName
	const defaultReduceModel = cleaner.DefaultModelName
	llmCleaner, err := cleaner.NewCleaner(defaultMapModel, defaultReduceModel, verbose)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	return &Pipeline{
		Client:    client,
		Extractor: extractor,
		Scraper:   parallelScraper, // 注入
		Cleaner:   llmCleaner,      // 注入
		Parallel:  parallel,
		Verbose:   verbose,
		LLMAPIKey: llmAPIKey, // 保持
	}, nil
}

// Run はフィードの取得、記事の並列抽出、AI処理、およびI/O処理を実行します。
func (p *Pipeline) Run(ctx context.Context, feedURL string) error {

	// --- 1. RSSフィードの取得とURLリスト生成 ---
	rssFeed, err := feed.FetchAndParse(ctx, p.Client, feedURL)
	if err != nil {
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	urlsToScrape := make([]string, 0, len(rssFeed.Items))
	// TitleはExtractorではなくRSSから取得するため、一時的なマップで保持 (types.URLResultがTitleを持たないため)
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

	fmt.Fprintf(os.Stderr, "🌐 記事URL %d件を最大並列数 %d で本文抽出中...\n", len(urlsToScrape), p.Parallel)

	// --- 2. Scraperによる並列抽出の実行 ---
	// Scraperに処理を委譲。セマフォ制御やGoroutine管理はすべて scraper.ScrapeInParallel が担当。
	results := p.Scraper.ScrapeInParallel(ctx, urlsToScrape)

	// --- 3. 抽出結果の確認とAI処理の分岐 ---
	successCount := 0
	for _, res := range results {
		if res.Error == nil {
			successCount++
		} else if p.Verbose {
			log.Printf("❌ 抽出エラー [%s]: %v", res.URL, res.Error)
		}
	}

	fmt.Fprintf(os.Stderr, "✅ 抽出完了。成功件数: %d / 処理件数: %d\n", successCount, len(urlsToScrape))

	if successCount == 0 {
		return fmt.Errorf("処理すべき記事本文が一つも見つかりませんでした")
	}

	// AI処理をスキップするかどうかをLLMAPIKeyの有無で判断
	if p.LLMAPIKey == "" {
		return p.processWithoutAI(rssFeed.Title, results, articleTitlesMap)
	}

	// --- 4. AI処理の実行 (Cleanerによる Map-Reduce) ---
	fmt.Fprintln(os.Stderr, "\n🤖 LLM処理開始 (Cleanerによる Map-Reduce)...")

	// 4-1. コンテンツの結合 (Cleanerのヘルパー関数を使用)
	combinedTextForAI := cleaner.CombineContents(results)

	// 4-2. クリーンアップと構造化の実行
	structuredText, err := p.Cleaner.CleanAndStructureText(ctx, combinedTextForAI, p.LLMAPIKey)
	if err != nil {
		return fmt.Errorf("AIによるコンテンツの構造化に失敗しました: %w", err)
	}

	// --- 5. AI処理結果の出力 ---
	fmt.Fprintln(os.Stderr, "\n--- スクリプト生成完了 (AI構造化済み) ---")
	return iohandler.WriteOutput("", []byte(structuredText))
}

// processWithoutAI は LLMAPIKeyがない場合に実行される処理
func (p *Pipeline) processWithoutAI(feedTitle string, results []types.URLResult, titlesMap map[string]string) error {
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", feedTitle))

	for _, res := range results {
		if res.Error != nil {
			fmt.Fprintf(os.Stderr, "❌ 抽出失敗 [%s]: %v\n", res.URL, res.Error)
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

	fmt.Fprintln(os.Stderr, "\n--- スクリプト生成結果 (AI処理スキップ) ---")

	// iohandler を使用して []byte で出力
	return iohandler.WriteOutput("", []byte(combinedText))
}
