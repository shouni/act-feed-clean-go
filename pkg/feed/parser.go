package feed

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/shouni/go-http-kit/pkg/httpkit"

	"github.com/mmcdole/gofeed"
)

// FetchAndParse の引数を *httpkit.Client ポインタ型に変更
func FetchAndParse(ctx context.Context, client *httpkit.Client, feedURL string) (*gofeed.Feed, error) {
	slog.Info("RSSフィードを取得・パース中", slog.String("url", feedURL))

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエスト作成失敗 (URL: %s): %w", feedURL, err)
	}

	// client.Do() を呼び出す。clientはポインタ型だが、メソッドはポインタレシーバでなくても呼び出せる
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTPリクエスト失敗 (URL: %s): %w", feedURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPステータスエラー: %d (URL: %s)", resp.StatusCode, feedURL)
	}

	fp := gofeed.NewParser()
	feed, err := fp.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RSSフィードのパース失敗 (URL: %s): %w", feedURL, err)
	}

	return feed, nil
}
