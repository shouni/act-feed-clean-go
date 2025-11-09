package cleaner

import (
	"act-feed-clean-go/prompts"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"github.com/shouni/go-web-exact/v2/pkg/types"
	"golang.org/x/time/rate"
)

// ----------------------------------------------------------------
// パッケージレベルのユーティリティ関数
// ----------------------------------------------------------------

// CombineContents は、成功した抽出結果の本文を効率的に結合します。
func CombineContents(results []types.URLResult, titlesMap map[string]string) string {
	var builder strings.Builder

	// 成功した結果のみをフィルタリング
	validResults := make([]types.URLResult, 0, len(results))
	for _, res := range results {
		if res.Error == nil && res.Content != "" {
			validResults = append(validResults, res)
		}
	}

	for i, res := range validResults {
		// URLからタイトルを取得。見つからない場合はURL自体をタイトルとして使用
		title := titlesMap[res.URL]
		if title == "" {
			title = res.URL // フォールバック
		}

		// 1. LLMがソースを識別するためのURLとインデックスを追記
		builder.WriteString(fmt.Sprintf("--- SOURCE DOCUMENT %d ---\n", i+1))
		builder.WriteString(fmt.Sprintf("TITLE: %s\n", title))
		builder.WriteString(fmt.Sprintf("URL: %s\n\n", res.URL))

		// 2. 本文を追加
		builder.WriteString(res.Content)

		// 3. 最後の文書でなければ明確な区切り文字を追加
		if i < len(validResults)-1 {
			builder.WriteString(ContentSeparator)
		}
	}

	return builder.String()
}

// ExtractTextBetweenTags は、指定されたタグマーカー間のテキストを抽出します。
func ExtractTextBetweenTags(text, startTag, endTag string) string {
	startMarker := fmt.Sprintf("<%s>", strings.ToUpper(startTag))
	endMarker1 := fmt.Sprintf("</%s>", strings.ToUpper(endTag))
	endMarker2 := fmt.Sprintf("<%s>", strings.ToUpper(endTag))

	startIndex := strings.Index(text, startMarker)
	if startIndex == -1 {
		return ""
	}
	startIndex += len(startMarker)

	// 最初に startIndex 以降で </TAG> の位置を探す
	endIndex := strings.Index(text[startIndex:], endMarker1)
	if endIndex != -1 {
		endIndex += startIndex // 全体文字列での位置に変換
	} else {
		// 見つからなければ startIndex 以降で <TAG> の位置を探す
		endIndex = strings.Index(text[startIndex:], endMarker2)
		if endIndex != -1 {
			endIndex += startIndex // 全体文字列での位置に変換
		}
	}

	if endIndex == -1 || endIndex < startIndex {
		return ""
	}

	return strings.TrimSpace(text[startIndex:endIndex])
}

// ExtractTitleFromMarkdown は、Markdownテキストの最初の # 見出しの内容を抽出します。
func ExtractTitleFromMarkdown(markdownText string) string {
	lines := strings.Split(markdownText, "\n")
	for _, line := range lines {
		// 先頭が "# " で始まる行を探す
		if strings.HasPrefix(line, "# ") {
			// "# " を取り除き、前後のスペースをトリム
			title := strings.TrimSpace(line[2:])
			if title != "" {
				return title
			}
		}
	}
	return ""
}

// ----------------------------------------------------------------
// Cleaner 内部ヘルパーメソッド
// ----------------------------------------------------------------

// segmentText は、結合されたテキストを、安全な最大文字数を超えないように分割します。
func (c *Cleaner) segmentText(text string, maxChars int) []string {
	var segments []string
	current := []rune(text)

	for len(current) > 0 {
		if len(current) <= maxChars {
			segments = append(segments, string(current))
			break
		}

		segmentCandidateRunes := current[:maxChars]
		segmentCandidate := string(segmentCandidateRunes)

		splitIndex := maxChars // デフォルトはmaxCharsで強制分割
		separatorFound := false

		// 1. ContentSeparator (最高優先度) を探す
		if lastSepIdx := strings.LastIndex(segmentCandidate, ContentSeparator); lastSepIdx != -1 {
			potentialSplitIndex := lastSepIdx + len(ContentSeparator)
			if potentialSplitIndex <= maxChars {
				splitIndex = potentialSplitIndex
				separatorFound = true
			}
		}

		// 2. ContentSeparator が見つからない、または採用されなかった場合、一般的な改行(\n\n)を探す
		if !separatorFound {
			if lastSepIdx := strings.LastIndex(segmentCandidate, DefaultSeparator); lastSepIdx != -1 {
				potentialSplitIndex := lastSepIdx + len(DefaultSeparator)
				if potentialSplitIndex <= maxChars {
					splitIndex = potentialSplitIndex
					separatorFound = true
				}
			}
		}

		// 3. 意味的な区切り文字（句読点、スペース）を探し、より自然な場所で分割
		if !separatorFound {
			const lookback = 50
			start := max(0, len(segmentCandidateRunes)-lookback)

			lastMeaningfulBreak := -1

			for i := len(segmentCandidateRunes) - 1; i >= start; i-- {
				r := segmentCandidateRunes[i]

				if unicode.IsPunct(r) || unicode.IsSpace(r) {
					lastMeaningfulBreak = i + 1
					break
				}
			}

			if lastMeaningfulBreak != -1 {
				splitIndex = lastMeaningfulBreak
				separatorFound = true
			}
		}

		if !separatorFound {
			if c.config.Verbose {
				slog.Warn("分割点で適切な区切りが見つかりませんでした。強制的に分割します。", slog.Int("max_chars", maxChars))
			}
			splitIndex = maxChars
		}

		segments = append(segments, string(current[:splitIndex]))
		current = current[splitIndex:]
	}

	return segments
}

// processSegmentsInParallel は Mapフェーズを並列処理します。
// LLMリクエストのレートリミット（DefaultLLMRateLimit = 1秒）を適用します。
func (c *Cleaner) processSegmentsInParallel(ctx context.Context, segments []string) ([]string, error) {
	var wg sync.WaitGroup

	// LLMリクエストレートリミッターの準備
	// DefaultLLMRateLimit (1秒) に基づき、バーストサイズ1の厳密なリミッターを作成
	limiter := rate.NewLimiter(rate.Every(c.rateLimit), 1)

	// segmentIndex, summary, error を格納するチャネル
	resultsChan := make(chan struct {
		index   int
		summary string
		err     error
	}, len(segments))

	for i, segment := range segments {
		wg.Add(1)

		go func(index int, seg string) {
			defer wg.Done()

			// 💡 レートリミットの待機
			// Wait(ctx) は、レートリミットに達した場合に待機し、ctx.Done() が発火した場合はエラーを返す。
			if err := limiter.Wait(ctx); err != nil {
				resultsChan <- struct {
					index   int
					summary string
					err     error
				}{index: index + 1, summary: "", err: fmt.Errorf("LLMリミット待機中にキャンセル: %w", err)}
				return
			}

			mapData := prompts.MapTemplateData{SegmentText: seg}
			prompt, err := c.prompt.MapBuilder.BuildMap(mapData)
			if err != nil {
				resultsChan <- struct {
					index   int
					summary string
					err     error
				}{index: index + 1, summary: "", err: fmt.Errorf("プロンプト生成失敗: %w", err)}
				return
			}

			// Mapフェーズのモデル名に c.config.MapModel を使用
			response, err := c.client.GenerateContent(ctx, prompt, c.config.MapModel)

			if err != nil {
				resultsChan <- struct {
					index   int
					summary string
					err     error
				}{index: index + 1, summary: "", err: fmt.Errorf("LLM処理失敗: %w", err)}
				return
			}

			resultsChan <- struct {
				index   int
				summary string
				err     error
			}{index: index + 1, summary: response.Text, err: nil}
		}(i, segment)
	}

	wg.Wait()
	close(resultsChan)

	// エラー蓄積ロジック
	var summaries []string
	var errorMessages []string

	for res := range resultsChan {
		if res.err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("セグメント %d: %v", res.index, res.err))
		} else {
			summaries = append(summaries, res.summary)
		}
	}

	if len(errorMessages) > 0 {
		return nil, fmt.Errorf("Mapフェーズで %d 件のエラーが発生しました:\n- %s",
			len(errorMessages),
			strings.Join(errorMessages, "\n- "))
	}

	return summaries, nil
}
