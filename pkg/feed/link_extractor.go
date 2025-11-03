package feed

import (
	"github.com/mmcdole/gofeed"
)

// ExtractLinks はパースされたフィードから、本文抽出対象のURLリストを抽出します。
func ExtractLinks(feed *gofeed.Feed) []string {
	// 【修正点】nil ではなく空のスライス []string{} を返すように変更
	if feed == nil || len(feed.Items) == 0 {
		return []string{}
	}

	urls := make([]string, 0, len(feed.Items))
	for _, item := range feed.Items {
		// リンクが存在し、空文字列ではないことを確認
		if item.Link != "" {
			urls = append(urls, item.Link)
		}
	}
	return urls
}
