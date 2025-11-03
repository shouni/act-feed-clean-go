package cleaner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

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
	mapBuilder    *prompts.PromptBuilder
	reduceBuilder *prompts.PromptBuilder
	// 修正: LLMモデル名を保持するフィールドを追加
	MapModelName    string
	ReduceModelName string
	// 修正: 警告ログ制御のためのVerboseフラグを追加
	Verbose bool
}

// DefaultModelName は Map/Reduce のデフォルトモデル名です。
const DefaultModelName = "gemini-2.5-flash"

// NewCleaner は新しいCleanerインスタンスを作成し、PromptBuilderを初期化します。
// Map/Reduceで使用するLLMモデル名とVerboseフラグを受け取るように変更。
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
		mapBuilder:    mapBuilder,
		reduceBuilder: reduceBuilder,
		// 修正: モデル名をセット
		MapModelName:    mapModel,
		ReduceModelName: reduceModel,
		// 修正: Verboseフラグをセット
		Verbose: verbose,
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

	// 1. LLMクライアントの初期化
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
	segments := c.segmentText(combinedText, MaxSegmentChars) // c.segmentTextに変更
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

	// 修正: ReduceLLMModelを使用
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
// Cleanerのメソッドに修正。
func (c *Cleaner) segmentText(text string, maxChars int) []string {
	var segments []string
	current := []rune(text)

	for len(current) > 0 {
		if len(current) <= maxChars {
			segments = append(segments, string(current))
			break
		}

		// 修正: runesで操作するため、maxChars分のruneスライスを取得
		segmentCandidateRunes := current[:maxChars]
		segmentCandidate := string(segmentCandidateRunes)

		splitIndex := maxChars // デフォルトはmaxCharsで強制分割
		separatorFound := false

		// 1. ContentSeparator (最高優先度) を探す
		if lastSepIdx := strings.LastIndex(segmentCandidate, ContentSeparator); lastSepIdx != -1 {
			// 区切り文字の直後までを分割位置とする
			potentialSplitIndex := lastSepIdx + len(ContentSeparator)
			// 修正: maxCharsを超えない場合のみ採用
			if potentialSplitIndex <= maxChars {
				splitIndex = potentialSplitIndex
				separatorFound = true
			}
		}

		// 2. ContentSeparator が見つからない、または採用されなかった場合、一般的な改行(\n\n)を探す
		if !separatorFound {
			if lastSepIdx := strings.LastIndex(segmentCandidate, DefaultSeparator); lastSepIdx != -1 {
				// 同様にmaxCharsを超えないように調整
				potentialSplitIndex := lastSepIdx + len(DefaultSeparator)
				// 修正: maxCharsを超えない場合のみ採用
				if potentialSplitIndex <= maxChars {
					splitIndex = potentialSplitIndex
					separatorFound = true
				}
			}
		}

		if !separatorFound {
			// 修正: Verboseフラグに基づき警告を表示
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

			// 修正: MapModelNameを使用
			response, err := client.GenerateContent(ctx, prompt, c.MapModelName)

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
