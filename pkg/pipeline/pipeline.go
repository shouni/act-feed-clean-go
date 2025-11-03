package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"act-feed-clean-go/pkg/feed"
	"github.com/shouni/go-http-kit/pkg/httpkit"
	"github.com/shouni/go-utils/iohandler"
	"github.com/shouni/go-web-exact/v2/pkg/extract"
)

// ExtractedArticle は並列処理の結果を保持するための構造体。
type ExtractedArticle struct {
	URL     string
	Title   string
	Content string
	Error   error
}

// ArticleInfo は並列処理に渡すための記事URLとタイトル情報。
type ArticleInfo struct {
	URL   string
	Title string
}

// Pipeline は記事の取得から結合までの一連の流れを管理します。
type Pipeline struct {
	// 依存性の注入 (DI)
	Client    *httpkit.Client
	Extractor *extract.Extractor

	// 設定値
	Parallel int
	Verbose  bool // 修正: Verboseフラグを追加
}

// New は新しい Pipeline インスタンスを初期化し、依存関係を注入します。
// cmd/root.go から呼び出され、httpClient、並列数、verboseフラグを渡されます。
func New(client *httpkit.Client, parallel int, verbose bool) (*Pipeline, error) { // 修正: verbose引数を追加
	// ExtractorはClientに依存するため、ここで初期化してDIする
	extractor, err := extract.NewExtractor(client)
	if err != nil {
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	return &Pipeline{
		Client:    client,
		Extractor: extractor,
		Parallel:  parallel,
		Verbose:   verbose,
	}, nil
}

// Run はフィードの取得、記事の並列抽出、結果の結合、およびI/O処理を実行します。
func (p *Pipeline) Run(ctx context.Context, feedURL string) error {

	// 1. RSSフィードの取得とURLリスト生成
	rssFeed, err := feed.FetchAndParse(ctx, p.Client, feedURL)
	if err != nil {
		return fmt.Errorf("RSSフィードの取得・パースに失敗しました: %w", err)
	}

	// タイトル情報を含む ArticleInfo のスライスを生成
	articlesToProcess := make([]ArticleInfo, 0, len(rssFeed.Items))
	for _, item := range rssFeed.Items {
		if item.Link != "" && item.Title != "" {
			articlesToProcess = append(articlesToProcess, ArticleInfo{URL: item.Link, Title: item.Title})
		}
	}

	if len(articlesToProcess) == 0 {
		return fmt.Errorf("フィードから有効な記事URLが見つかりませんでした")
	}

	fmt.Fprintf(os.Stderr, "🌐 記事URL %d件を最大並列数 %d で本文抽出中...\n", len(articlesToProcess), p.Parallel)

	// 2. 並列抽出の実行 (セマフォ制御)
	sem := semaphore.NewWeighted(int64(p.Parallel))
	var wg sync.WaitGroup
	results := make(chan ExtractedArticle, len(articlesToProcess))

	for _, article := range articlesToProcess {
		wg.Add(1)
		go func(article ArticleInfo) {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				results <- ExtractedArticle{URL: article.URL, Title: article.Title, Error: fmt.Errorf("セマフォ取得失敗: %w", err)}
				return
			}
			defer sem.Release(1)

			// ExtractorはPipelineの依存性として注入されているものを使用
			content, hasBodyFound, err := p.Extractor.FetchAndExtractText(article.URL, ctx)

			// エラーハンドリングの一貫化
			var finalErr error
			if err != nil {
				finalErr = fmt.Errorf("コンテンツの抽出に失敗しました: %w", err)
			} else if content == "" || !hasBodyFound {
				finalErr = fmt.Errorf("URL %s から有効な本文を抽出できませんでした", article.URL)
			}

			if finalErr != nil && p.Verbose {
				log.Printf("抽出エラー [%s] (%s): %v", article.Title, article.URL, finalErr)
			}

			results <- ExtractedArticle{
				URL:     article.URL,
				Title:   article.Title,
				Content: content,
				Error:   finalErr,
			}
		}(article)
	}
	wg.Wait()
	close(results)

	// 3. 結果の結合と出力準備
	var combinedTextBuilder strings.Builder
	combinedTextBuilder.WriteString(fmt.Sprintf("# %s\n\n", rssFeed.Title))
	successCount := 0

	for res := range results {
		if res.Error != nil {
			fmt.Fprintf(os.Stderr, "❌ 抽出失敗 [%s]: %v\n", res.URL, res.Error)
			continue
		}

		articleTitle := res.Title
		if articleTitle == "" {
			articleTitle = res.URL
		}

		// 記事タイトルと本文を結合
		combinedTextBuilder.WriteString(fmt.Sprintf("## 【記事タイトル】 %s\n\n", articleTitle))
		combinedTextBuilder.WriteString(res.Content)
		combinedTextBuilder.WriteString("\n\n---\n\n")
		successCount++
	}
	fmt.Fprintf(os.Stderr, "✅ 抽出完了。成功件数: %d / 処理件数: %d\n", successCount, len(articlesToProcess))

	// 4. 結合テキストの出力 (AI処理スキップ)
	combinedText := combinedTextBuilder.String()
	if combinedText == "" {
		return fmt.Errorf("処理すべき記事本文が見つかりませんでした")
	}

	fmt.Fprintln(os.Stderr, "\n--- スクリプト生成結果 (AI処理スキップ) ---")

	// iohandler を使用して []byte で出力
	return iohandler.WriteOutput("", []byte(combinedText))
}
