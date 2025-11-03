package cleaner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	// 修正: 現在のプロジェクト内の pkg/types をインポート
	"act-feed-clean-go/pkg/types"
	// prompts パッケージは外部依存としてそのまま利用することを想定
	"github.com/shouni/action-perfect-get-on-go/prompts"
	"github.com/shouni/go-ai-client/v2/pkg/ai/gemini"
)

// ContentSeparator は、結合された複数の文書間を区切るための明確な区切り文字です。
const ContentSeparator = "\n\n--- DOCUMENT END ---\n\n"

// DefaultSeparator は、一般的な段落区切りに使用される標準的な区切り文字です。
const DefaultSeparator = "\n\n"

// MaxSegmentChars は、MapフェーズでLLMに一度に渡す安全な最大文字数。
const MaxSegmentChars = 400000

// ----------------------------------------------------------------
// Cleaner 構造体とコンストラクタ
// ----------------------------------------------------------------

// Cleaner はコンテンツのクリーンアップと要約を担当します。
// Map/Reduce用のプロンプトビルダーを保持します。
type Cleaner struct {
	mapBuilder    *prompts.PromptBuilder
	reduceBuilder *prompts.PromptBuilder
}

// NewCleaner は新しいCleanerインスタンスを作成し、PromptBuilderを初期化します。
func NewCleaner() (*Cleaner, error) {
	// プロンプトテンプレートの初期化と検証
	mapBuilder := prompts.NewMapPromptBuilder()
	if err := mapBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Map プロンプトビルダーの初期化に失敗しました: %w", err)
	}
	reduceBuilder := prompts.NewReducePromptBuilder()
	if err := reduceBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Reduce プロンプトビルダーの初期化に失敗しました: %w", err)
	}

	return &Cleaner{
		mapBuilder:    mapBuilder,
		reduceBuilder: reduceBuilder,
	}, nil
}

// ----------------------------------------------------------------
// メインロジック
// ----------------------------------------------------------------

// CombineContents は、成功した抽出結果の本文を効率的に結合します。
// 各コンテンツの前には、ソースURL情報が付加されます。
func CombineContents(results []types.URLResult) string {
	var builder strings.Builder

	// 成功した結果のみをフィルタリング
	validResults := make([]types.URLResult, 0, len(results))
	for _, res := range results {
		if res.Error == nil && res.Content != "" {
			validResults = append(validResults, res)
		}
	}

	for i, res := range validResults {
		// URLを追記することで、LLMがどのソースのテキストであるかを識別できるようにする
		builder.WriteString(fmt.Sprintf("--- SOURCE URL %d: %s ---\n", i+1, res.URL))
		builder.WriteString(res.Content)

		// 最後の文書でなければ明確な区切り文字を追加
		if i < len(validResults)-1 {
			builder.WriteString(ContentSeparator)
		}
	}

	return builder.String()
}

// CleanAndStructureText は、コンテンツをMap-Reduceパターンで構造化します。
func (c *Cleaner) CleanAndStructureText(ctx context.Context, combinedText string, apiKeyOverride string) (string, error) {

	// 1. LLMクライアントの初期化
	var client *gemini.Client
	var err error

	if apiKeyOverride != "" {
		client, err = gemini.NewClient(ctx, gemini.Config{APIKey: apiKeyOverride})
	} else {
		client, err = gemini.NewClientFromEnv(ctx)
	}

	if err != nil {
		return "", fmt.Errorf("LLMクライアントの初期化に失敗しました。APIキー（--llm-api-keyオプションまたは環境変数）が設定されているか確認してください: %w", err)
	}

	// 2. Mapフェーズのためのテキスト分割
	segments := segmentText(combinedText, MaxSegmentChars)
	log.Printf("テキストを %d 個のセグメントに分割しました。中間要約を開始します。", len(segments))

	// 3. Mapフェーズの実行（各セグメントの並列処理）
	intermediateSummaries, err := c.processSegmentsInParallel(ctx, client, segments)
	if err != nil {
		return "", fmt.Errorf("セグメント処理（Mapフェーズ）に失敗しました: %w", err)
	}

	// 4. Reduceフェーズの準備：中間要約の結合
	finalCombinedText := strings.Join(intermediateSummaries, "\n\n--- INTERMEDIATE SUMMARY END ---\n\n")

	// 5. Reduceフェーズ：最終的な統合と構造化のためのLLM呼び出し
	log.Println("中間要約の結合が完了しました。最終的な構造化（Reduceフェーズ）を開始します。")

	reduceData := prompts.ReduceTemplateData{CombinedText: finalCombinedText}
	finalPrompt, err := c.reduceBuilder.BuildReduce(reduceData)
	if err != nil {
		return "", fmt.Errorf("最終 Reduce プロンプトの生成に失敗しました: %w", err)
	}

	finalResponse, err := client.GenerateContent(ctx, finalPrompt, "gemini-2.5-flash")
	if err != nil {
		return "", fmt.Errorf("LLM最終構造化処理（Reduceフェーズ）に失敗しました: %w", err)
	}

	return finalResponse.Text, nil
}

// ----------------------------------------------------------------
// ヘルパー関数群
// ----------------------------------------------------------------

// segmentText は、結合されたテキストを、安全な最大文字数を超えないように分割します。
// 段落の区切りを優先して分割し、文脈の欠落を最小限に抑えます。
func segmentText(text string, maxChars int) []string {
	var segments []string
	current := []rune(text)

	for len(current) > 0 {
		if len(current) <= maxChars {
			segments = append(segments, string(current))
			break
		}

		splitIndex := maxChars
		segmentCandidate := string(current[:maxChars])
		separatorFound := false
		separatorLen := 0

		// 1. ContentSeparator (最高優先度) を探す
		if lastSepIdx := strings.LastIndex(segmentCandidate, ContentSeparator); lastSepIdx != -1 && lastSepIdx > maxChars/2 {
			splitIndex = lastSepIdx
			separatorLen = len(ContentSeparator)
			separatorFound = true
		} else if lastSepIdx := strings.LastIndex(segmentCandidate, DefaultSeparator); lastSepIdx != -1 && lastSepIdx > maxChars/2 {
			// 2. ContentSeparator が見つからない場合、一般的な改行(\n\n)を探す
			splitIndex = lastSepIdx
			separatorLen = len(DefaultSeparator)
			separatorFound = true
		}

		// 区切り文字の種類に応じて、加算する長さを適切に選択
		if separatorFound {
			// 区切り文字の直後までを分割位置とする
			splitIndex += separatorLen
		} else {
			// 安全な区切りが見つからない場合は、そのまま最大文字数で切り、警告を出す
			log.Printf("⚠️ WARNING: 分割点で適切な区切りが見つかりませんでした。強制的に %d 文字で分割します。", maxChars)
			splitIndex = maxChars
		}

		segments = append(segments, string(current[:splitIndex]))
		current = current[splitIndex:]
	}

	return segments
}

// processSegmentsInParallel は Mapフェーズを並列処理します。
func (c *Cleaner) processSegmentsInParallel(ctx context.Context, client *gemini.Client, segments []string) ([]string, error) {
	var wg sync.WaitGroup
	resultsChan := make(chan struct {
		summary string
		err     error
	}, len(segments))

	for i, segment := range segments {
		wg.Add(1)

		go func(index int, seg string) {
			defer wg.Done()

			mapData := prompts.MapTemplateData{SegmentText: seg}
			prompt, err := c.mapBuilder.BuildMap(mapData)
			if err != nil {
				log.Printf("❌ ERROR: セグメント %d のプロンプト生成に失敗しました: %v", index+1, err)
				resultsChan <- struct {
					summary string
					err     error
				}{summary: "", err: fmt.Errorf("セグメント %d プロンプト生成失敗: %w", index+1, err)}
				return
			}

			response, err := client.GenerateContent(ctx, prompt, "gemini-2.5-flash")

			if err != nil {
				log.Printf("❌ ERROR: セグメント %d の処理に失敗しました: %v", index+1, err)
				resultsChan <- struct {
					summary string
					err     error
				}{summary: "", err: fmt.Errorf("セグメント %d 処理失敗: %w", index+1, err)}
				return
			}

			resultsChan <- struct {
				summary string
				err     error
			}{summary: response.Text, err: nil}
		}(i, segment)
	}

	wg.Wait()
	close(resultsChan)

	var summaries []string
	for res := range resultsChan {
		if res.err != nil {
			// 1つでもエラーがあれば、処理全体を失敗させる
			return nil, res.err
		}
		summaries = append(summaries, res.summary)
	}

	return summaries, nil
}
