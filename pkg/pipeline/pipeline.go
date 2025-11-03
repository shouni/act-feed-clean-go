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

// PipelineConfig はパイプライン実行のためのすべての設定値を保持します。
type PipelineConfig struct {
	Parallel           int
	Verbose            bool
	LLMAPIKey          string
	VoicevoxAPIURL     string
	OutputWAVPath      string
	ScrapeTimeout      time.Duration
	VoicevoxAPITimeout time.Duration
	// --- 変更点: AIモデル名のフィールドを追加 ---
	MapModelName    string
	ReduceModelName string
	// ------------------------------------------
}

// Pipeline は記事の取得から結合までの一連の流れを管理します。
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
func New(client *httpkit.Client, config PipelineConfig) (*Pipeline, error) {
	// ログ設定: slog.Handlerの選択と設定
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
			// Timeキー以外の属性はそのまま返す
			return a
		},
	})
	slog.SetDefault(slog.New(handler))

	// 1. Extractorの初期化
	extractor, err := extract.NewExtractor(client)
	if err != nil {
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	// 2. Scraperの初期化 (configからParallelにアクセス)
	parallelScraper := scraper.NewParallelScraper(extractor, config.Parallel)

	// 3. Cleanerの初期化 (configからVerboseにアクセス)
	// --- 変更点: Configからモデル名を取得し、空の場合はデフォルトを使用するロジックに変更 ---
	mapModel := config.MapModelName
	if mapModel == "" {
		mapModel = cleaner.DefaultMapModelName // cleaner側もこの定数名に更新されると想定
	}
	reduceModel := config.ReduceModelName
	if reduceModel == "" {
		reduceModel = cleaner.DefaultReduceModelName // cleaner側もこの定数名に更新されると想定
	}

	llmCleaner, err := cleaner.NewCleaner(mapModel, reduceModel, config.Verbose)
	if err != nil {
		return nil, fmt.Errorf("クリーナーの初期化に失敗しました: %w", err)
	}
	// --------------------------------------------------------------------------

	// 4. VOICEVOX Engineの初期化
	var vvEngine *voicevox.Engine
	if config.VoicevoxAPIURL != "" {
		slog.Info("VOICEVOXクライアントを初期化します", slog.String("url", config.VoicevoxAPIURL))

		// VOICEVOXクライアントに専用の VoicevoxAPITimeout を使用
		vvClient := voicevox.NewClient(config.VoicevoxAPIURL, config.VoicevoxAPITimeout)

		// 話者データ Load には VoicevoxAPITimeout を使用
		loadCtx, cancel := context.WithTimeout(context.Background(), config.VoicevoxAPITimeout)
		defer cancel()

		// voicevox.LoadSpeakers は voicevox.Engine が依存する speakerData を取得
		speakerData, loadErr := voicevox.LoadSpeakers(loadCtx, vvClient)
		if loadErr != nil {
			return nil, fmt.Errorf("VOICEVOX話者データのロードに失敗しました: %w", loadErr)
		}

		parser := voicevox.NewTextParser()
		engineConfig := voicevox.EngineConfig{
			MaxParallelSegments: voicevox.DefaultMaxParallelSegments,
			SegmentTimeout:      voicevox.DefaultSegmentTimeout,
		}

		// Engineの組み立てとExecutorとしての返却
		vvEngine = voicevox.NewEngine(vvClient, speakerData, parser, engineConfig)
	}

	return &Pipeline{
		Client:    client,
		Extractor: extractor,
		Scraper:   parallelScraper,
		Cleaner:   llmCleaner,

		VoicevoxEngine: vvEngine,
		OutputWAVPath:  config.OutputWAVPath,

		// 設定値全体を保持
		config: config,
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
