package feed

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/shouni/go-http-kit/pkg/httpkit"

	"github.com/mmcdole/gofeed"
)

// FetchAndParse は指定されたURLからRSSフィードを取得し、パースして構造体を返します。
// feedURL を引数として受け取ることで汎用性を高めます。
func FetchAndParse(ctx context.Context, client httpkit.Client, feedURL string) (*gofeed.Feed, error) {
	//
	// 【問題点: 行番号 18 / 修正案: ロギング】
	// 長期的な運用を考慮する場合は、log/slog など構造化されたロギングライブラリの導入が推奨されます。
	// 例: slog.Info("RSSフィードを取得・パース中", "url", feedURL)
	//
	fmt.Fprintln(os.Stderr, "📰 RSSフィードを取得・パース中:", feedURL)

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエスト作成失敗 (URL: %s): %w", feedURL, err)
	}

	// 記憶しているhttpclient.Client.Do() を利用
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTPリクエスト失敗 (URL: %s): %w", feedURL, err)
	}
	defer resp.Body.Close()

	// 【問題点: 行番号 32 / 修正案: エラーメッセージにURLを含める】
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPステータスエラー: %d (URL: %s)", resp.StatusCode, feedURL)
	}

	// gofeedライブラリでパース
	fp := gofeed.NewParser()
	feed, err := fp.Parse(resp.Body)
	if err != nil {
		// パース失敗時にもURLを含めるとデバッグに役立ちます
		return nil, fmt.Errorf("RSSフィードのパース失敗 (URL: %s): %w", feedURL, err)
	}

	return feed, nil
}
