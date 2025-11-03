package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"act-feed-clean-go/pkg/cleaner"
	"act-feed-clean-go/pkg/feed"
	"act-feed-clean-go/pkg/scraper"
	"act-feed-clean-go/pkg/types"

	"github.com/shouni/go-http-kit/pkg/httpkit"
	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-voicevox/pkg/voicevox"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// PipelineConfig はパイプライン実行のためのすべての設定値を保持します。（変更なし）
type PipelineConfig struct {
	Parallel           int
	Verbose            bool
	LLMAPIKey          string
	VoicevoxAPIURL     string
	OutputWAVPath      string
	ScrapeTimeout      time.Duration
	VoicevoxAPITimeout time.Duration
	MapModelName       string
	ReduceModelName    string
}

// Pipeline は記事の取得から結合までの一連の流れを管理します。（変更なし）
type Pipeline struct {
	Client    *httpkit.Client
	Extractor *extract.Extractor

	Scraper scraper.Scraper
	Cleaner *cleaner.Cleaner

	// VOICEVOX統合のためのフィールド
	VoicevoxEngine *voicevox.Engine
	OutputWAVPath  string // 音声合成後の出力ファイルパス

	// 設定値への参照を保持
	config PipelineConfig // 設定値への参照を保持
}

// New は新しい Pipeline インスタンスを初期化し、依存関係を注入します。
// 変更点: 依存関係 (Extractor, Scraper, Cleaner, VoicevoxEngine) を引数で受け取る
func New(
	client *httpkit.Client,
	extractor *extract.Extractor,
	s scraper.Scraper, // 's' は scraper.Scraper の意
	c *cleaner.Cleaner, // 'c' は *cleaner.Cleaner の意
	vvEngine *voicevox.Engine,
	config PipelineConfig,
) *Pipeline {
	// ログ設定: New関数からログ初期化ロジックを移動または簡略化
	// ログ初期化は通常、アプリケーションのエントリポイント（cmd/root.go）で行うべきですが、
	// ここでは New に依存関係がないため、冗長なコードを削除します。

	return &Pipeline{
		Client:    client,
		Extractor: extractor,
		Scraper:   s,
		Cleaner:   c,

		VoicevoxEngine: vvEngine,
		OutputWAVPath:  config.OutputWAVPath,

		// 設定値全体を保持
		config: config,
	}
}

// NewPipelineDependencies は、NewPipelineWithDependencies の前に呼び出され、
// Pipeline に必要なすべての依存関係を構築します。
// この関数は、旧 New 関数の内部ロジックを保持します。
func NewPipelineDependencies(client *httpkit.Client, config PipelineConfig) (*extract.Extractor, scraper.Scraper, *cleaner.Cleaner, *voicevox.Engine, error) {
	// ログ設定は呼び出し元 (cmd/root.go) に移譲することを推奨しますが、
	// 互換性のため、設定は保持し、初期化のみを行います。

	logLevel := slog.LevelInfo
	if config.Verbose {
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

	// 1. Extractorの初期化
	extractor, err := extract.NewExtractor(client)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 2. Scraperの初期化
	parallelScraper := scraper.NewParallelScraper(extractor, config.Parallel)

	// 3. Cleanerの初期化
	mapModel := config.MapModelName
	if mapModel == "" {
		mapModel = cleaner.DefaultMapModelName
	}
	reduceModel := config.ReduceModelName
	if reduceModel == "" {
		reduceModel = cleaner.DefaultReduceModelName
	}
	llmCleaner, err := cleaner.NewCleaner(mapModel, reduceModel, config.Verbose)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}

	// 4. VOICEVOX Engineの初期化
	var vvEngine *voicevox.Engine
	if config.VoicevoxAPIURL != "" {
		slog.Info("VOICEVOXクライアントを初期化します", slog.String("url", config.VoicevoxAPIURL))

		vvClient := voicevox.NewClient(config.VoicevoxAPIURL, config.VoicevoxAPITimeout)

		loadCtx, cancel := context.WithTimeout(context.Background(), config.VoicevoxAPITimeout)
		defer cancel()

		speakerData, loadErr := voicevox.LoadSpeakers(loadCtx, vvClient)
		if loadErr != nil {
			cancel() // タイムアウトを確実に停止
			return nil, nil, nil, nil, fmt.Errorf("VOICEVOX話者データのロードに失敗しました: %w", loadErr)
		}
		cancel() // タイムアウトを確実に停止

		parser := voicevox.NewTextParser()
		engineConfig := voicevox.EngineConfig{
			MaxParallelSegments: voicevox.DefaultMaxParallelSegments,
			SegmentTimeout:      voicevox.DefaultSegmentTimeout,
		}

		vvEngine = voicevox.NewEngine(vvClient, speakerData, parser, engineConfig)
	}

	return extractor, parallelScraper, llmCleaner, vvEngine, nil
}

// Run, processWithoutAI 関数は DI の変更による影響を受けないため、そのまま保持します。
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

	slog.Info("記事URLの抽出を開始します",
		slog.Int("urls", len(urlsToScrape)),
		slog.Int("parallel", p.config.Parallel),
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

	//  LLMAPIKeyがない場合はAI処理をスキップし、抽出結果をテキストで出力 (p.config.LLMAPIKeyにアクセス)
	if p.config.LLMAPIKey == "" {
		slog.Info("LLM APIキー未設定のため、AI処理をスキップし、抽出結果をテキストで出力します。")
		return p.processWithoutAI(rssFeed.Title, results, articleTitlesMap)
	}

	// --- 4. AI処理の実行 (Cleanerによる Map-Reduce) ---
	slog.Info("LLM処理開始", slog.String("phase", "Map-Reduce"))

	combinedTextForAI := cleaner.CombineContents(results)
	structuredText, err := p.Cleaner.CleanAndStructureText(ctx, combinedTextForAI, p.config.LLMAPIKey)
	if err != nil {
		slog.Error("AIによるコンテンツの構造化に失敗しました", slog.String("error", err.Error()))
		return fmt.Errorf("AIによるコンテンツの構造化に失敗しました: %w", err)
	}

	// --- 5. AI処理結果の出力分岐 ---
	if p.VoicevoxEngine != nil && p.OutputWAVPath != "" {
		// --- 5-A. VOICEVOXによる音声合成とWAV出力 ---
		slog.Info("AI生成スクリプトをVOICEVOXで音声合成します", slog.String("output", p.OutputWAVPath))

		// ディレクトリの存在確認と作成
		outputDir := filepath.Dir(p.OutputWAVPath)
		if outputDir != "." { // カレントディレクトリでない場合のみ
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				return fmt.Errorf("出力ディレクトリの作成に失敗しました (%s): %w", outputDir, err)
			}
		}

		err := p.VoicevoxEngine.Execute(ctx, structuredText, p.OutputWAVPath, voicevox.VvTagNormal)
		if err != nil {
			return fmt.Errorf("音声合成パイプラインの実行に失敗しました: %w", err)
		}
		slog.Info("VOICEVOXによる音声合成が完了し、ファイルに保存されました。", "output_file", p.OutputWAVPath)

		// 音声合成が成功したら、以降のテキスト出力処理をスキップしてここで終了
		return nil
	}

	// AI処理が実行されたが音声合成が行われない場合、テキスト出力を実行
	return iohandler.WriteOutput("", []byte(structuredText))
}

// processWithoutAI は LLMAPIKeyがない場合に実行される処理
func (p *Pipeline) processWithoutAI(feedTitle string, results []types.URLResult, titlesMap map[string]string) error {
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", feedTitle))

	for _, res := range results {
		if res.Error != nil {
			slog.Warn("抽出失敗 (処理スキップ)",
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

	// combinedTextが空の場合のチェックと警告ログの追加
	if combinedText == "" {
		slog.Warn("すべての記事本文が空でした。空の出力を生成します。", slog.String("mode", "AI処理スキップ"))
	}
	slog.Info("スクリプト生成結果", slog.String("mode", "AI処理スキップ"))

	// iohandler.WriteOutputの第二引数は []byte を受け取ります。
	return iohandler.WriteOutput("", []byte(combinedText))
}
