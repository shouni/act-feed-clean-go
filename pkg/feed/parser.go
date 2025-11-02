package feed

import (
	"context"
	"fmt"
	"net/http"
	"os" // os.Stderr を利用するため

	"act-feed-clean-go/pkg/httpclient" // 記憶しているクライアント

	"github.com/mmcdole/gofeed"
)

// Yahoo News ITカテゴリのRSSフィードURL
const YahooNewsITRSSURL = "https://news.yahoo.co.jp/rss/categories/it.xml"

// FetchAndParse はRSSフィードを取得し、パースして構造体を返します
func FetchAndParse(ctx context.Context, client httpclient.Client) (*gofeed.Feed, error) {
	fmt.Fprintln(os.Stderr, "📰 RSSフィードを取得・パース中:", YahooNewsITRSSURL)

	req, err := http.NewRequestWithContext(ctx, "GET", YahooNewsITRSSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエスト作成失敗: %w", err)
	}

	// 記憶しているhttpclient.Client.Do() を利用
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTPリクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPステータスエラー: %d", resp.StatusCode)
	}

	// gofeedライブラリでパース
	fp := gofeed.NewParser()
	feed, err := fp.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RSSフィードのパース失敗: %w", err)
	}

	return feed, nil
}
