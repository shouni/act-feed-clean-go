package cleaner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"unicode"

	"act-feed-clean-go/pkg/types"

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
type Cleaner struct {
	mapBuilder      *prompts.PromptBuilder
	reduceBuilder   *prompts.PromptBuilder
	MapModelName    string
	ReduceModelName string
	Verbose         bool
}

// DefaultModelName は Map/Reduce のデフォルトモデル名です。
const DefaultModelName = "gemini-2.5-flash"

// NewCleaner は新しいCleanerインスタンスを作成し、PromptBuilderを初期化します。
func NewCleaner(mapModel, reduceModel string, verbose bool) (*Cleaner, error) {
	if mapModel == "" {
		mapModel = DefaultModelName
	}
	if reduceModel == "" {
		reduceModel = DefaultModelName
	}

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
		mapBuilder:      mapBuilder,
		reduceBuilder:   reduceBuilder,
		MapModelName:    mapModel,
		ReduceModelName: reduceModel,
		Verbose:         verbose,
	}, nil
}

// ----------------------------------------------------------------
// メインロジック
// ----------------------------------------------------------------

// CombineContents は、成功した抽出結果の本文を効率的に結合します。
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

	// 1. LLMクライアントの初期化 (省略)
	var client *gemini.Client
	var err error

	if apiKeyOverride != "" {
		client, err = gemini.NewClient(ctx, gemini.Config{APIKey: apiKeyOverride})
	} else {
		client, err = gemini.NewClientFromEnv(ctx)
	}

	if err != nil {
		return "", fmt.Errorf("LLMクライアントの初期化に失敗しました。APIキーが設定されているか確認してください: %w", err)
	}

	// 2. Mapフェーズのためのテキスト分割
	segments := c.segmentText(combinedText, MaxSegmentChars)
	log.Printf("テキストを %d 個のセグメントに分割しました。中間要約を開始します。", len(segments))

	// 3. Mapフェーズの実行（各セグメントの並列処理）
	intermediateSummaries, err := c.processSegmentsInParallel(ctx, client, segments)
	if err != nil {
		return "", fmt.Errorf("セグメント処理（Mapフェーズ）に失敗しました: %w", err)
	}

	// 4. Reduceフェーズの準備：中間要約の結合
	finalCombinedText := strings.Join(intermediateSummaries, "\n\n--- INTERMEDIATE SUMMARY END ---\n\n")

	// 5. Reduceフェーズ：最終的な統合と構造化のためのLLM呼び出し (省略)
	log.Println("中間要約の結合が完了しました。最終的な構造化（Reduceフェーズ）を開始します。")

	reduceData := prompts.ReduceTemplateData{CombinedText: finalCombinedText}
	finalPrompt, err := c.reduceBuilder.BuildReduce(reduceData)
	if err != nil {
		return "", fmt.Errorf("最終 Reduce プロンプトの生成に失敗しました: %w", err)
	}

	finalResponse, err := client.GenerateContent(ctx, finalPrompt, c.ReduceModelName)
	if err != nil {
		return "", fmt.Errorf("LLM最終構造化処理（Reduceフェーズ）に失敗しました: %w", err)
	}

	return finalResponse.Text, nil
}

// ----------------------------------------------------------------
// ヘルパー関数群
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
			// maxCharsの直前から開始し、最初のスペースや句読点の直後を探す
			// 検索範囲をmaxCharsの直前50文字程度に制限して効率化を図る
			const lookback = 50
			start := max(0, len(segmentCandidateRunes)-lookback)

			// 最後に意味的な区切りが見つかった位置
			lastMeaningfulBreak := -1

			// ルーン単位で逆方向にスキャン
			for i := len(segmentCandidateRunes) - 1; i >= start; i-- {
				r := segmentCandidateRunes[i]

				// 句読点、スペース、全角スペースなど、文脈が途切れる可能性がある文字を探す
				if unicode.IsPunct(r) || unicode.IsSpace(r) {
					// 区切り文字の直後を分割点とする
					lastMeaningfulBreak = i + 1
					break
				}
			}

			if lastMeaningfulBreak != -1 {
				// 意味的な区切りが見つかった場合、その位置を採用
				splitIndex = lastMeaningfulBreak
				separatorFound = true
			}
		}

		if !separatorFound {
			// 最終手段: 安全な区切りが見つからない場合は、そのまま最大文字数で切り、警告を出す
			if c.Verbose {
				log.Printf("⚠️ WARNING: 分割点で適切な区切りが見つかりませんでした。強制的に %d 文字で分割します。", maxChars)
			}
			splitIndex = maxChars
		}

		segments = append(segments, string(current[:splitIndex]))
		current = current[splitIndex:]
	}

	return segments
}

// max は2つの整数のうち大きい方を返すヘルパー関数 (Go 1.20未満の互換性のため)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
				resultsChan <- struct {
					summary string
					err     error
				}{summary: "", err: fmt.Errorf("セグメント %d プロンプト生成失敗: %w", index+1, err)}
				return
			}

			response, err := client.GenerateContent(ctx, prompt, c.MapModelName)

			if err != nil {
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
	// エラー報告のために、すべての結果を集める必要があるため、一旦スライスにコピー
	allResults := make([]struct {
		summary string
		err     error
	}, 0, len(segments))
	for res := range resultsChan {
		allResults = append(allResults, res)
	}

	// エラーチェックと集約
	var firstError error
	for _, res := range allResults {
		if res.err != nil {
			if firstError == nil {
				firstError = res.err
			}
		} else {
			summaries = append(summaries, res.summary)
		}
	}

	if firstError != nil {
		// 少なくとも1つのエラーがあれば、処理全体を失敗させる
		return nil, firstError
	}

	return summaries, nil
}
