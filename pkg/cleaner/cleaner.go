package cleaner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"act-feed-clean-go/pkg/types"

	"act-feed-clean-go/prompts"

	"github.com/shouni/go-ai-client/v2/pkg/ai/gemini"
)

// ContentSeparator は、結合された複数の文書間を区切るための明確な区切り文字です。
const ContentSeparator = "\n\n--- DOCUMENT END ---\n\n"

// DefaultSeparator は、一般的な段落区切りに使用される標準的な区切り文字です。
const DefaultSeparator = "\n\n"

// MaxSegmentChars は、MapフェーズでLLMに一度に渡す安全な最大文字数。
const MaxSegmentChars = 400000

// ----------------------------------------------------------------
// モデル名定数の定義
// ----------------------------------------------------------------

// DefaultMapModelName は Mapフェーズのデフォルトモデル名です。
const DefaultMapModelName = "gemini-2.5-flash"

// DefaultReduceModelName は Reduceフェーズのデフォルトモデル名です。
const DefaultReduceModelName = "gemini-2.5-flash"

// DefaultSummaryModelName は FinalSummaryフェーズのデフォルトモデル名です。 (新規)
const DefaultSummaryModelName = "gemini-2.5-flash"

// DefaultScriptModelName は ScriptGenerationフェーズのデフォルトモデル名です。 (新規)
const DefaultScriptModelName = "gemini-2.5-flash"

// ----------------------------------------------------------------
// Cleaner 構造体とコンストラクタ
// ----------------------------------------------------------------

// Cleaner はコンテンツのクリーンアップと要約を担当します。
type Cleaner struct {
	client              *gemini.Client // 修正: LLMクライアントを注入
	mapBuilder          *prompts.PromptBuilder
	reduceBuilder       *prompts.PromptBuilder
	finalSummaryBuilder *prompts.PromptBuilder
	scriptBuilder       *prompts.PromptBuilder
	MapModelName        string
	ReduceModelName     string
	SummaryModelName    string
	ScriptModelName     string
	Verbose             bool
}

// NewCleaner は新しいCleanerインスタンスを作成し、依存関係とPromptBuilderを初期化します。
// 修正: クライアントインスタンスと全モデル名を受け取ります。
func NewCleaner(client *gemini.Client, mapModel, reduceModel, summaryModel, scriptModel string, verbose bool) (*Cleaner, error) {
	if client == nil {
		return nil, fmt.Errorf("LLMクライアントはnilであってはなりません")
	}

	// デフォルト値の設定
	if mapModel == "" {
		mapModel = DefaultMapModelName
	}
	if reduceModel == "" {
		reduceModel = DefaultReduceModelName
	}
	if summaryModel == "" {
		summaryModel = DefaultSummaryModelName
	}
	if scriptModel == "" {
		scriptModel = DefaultScriptModelName
	}

	mapBuilder := prompts.NewMapPromptBuilder()
	if err := mapBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Map プロンプトビルダーの初期化に失敗しました: %w", err)
	}
	reduceBuilder := prompts.NewReducePromptBuilder()
	if err := reduceBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Reduce プロンプトビルダーの初期化に失敗しました: %w", err)
	}

	// 新規追加
	finalSummaryBuilder := prompts.NewFinalSummaryPromptBuilder()
	if err := finalSummaryBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Final Summary プロンプトビルダーの初期化に失敗しました: %w", err)
	}

	// 新規追加
	scriptBuilder := prompts.NewScriptPromptBuilder()
	if err := scriptBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Script プロンプトビルダーの初期化に失敗しました: %w", err)
	}

	return &Cleaner{
		client:              client, // 注入
		mapBuilder:          mapBuilder,
		reduceBuilder:       reduceBuilder,
		finalSummaryBuilder: finalSummaryBuilder, // 新規追加
		scriptBuilder:       scriptBuilder,       // 新規追加
		MapModelName:        mapModel,
		ReduceModelName:     reduceModel,
		SummaryModelName:    summaryModel, // 新規追加
		ScriptModelName:     scriptModel,  // 新規追加
		Verbose:             verbose,
	}, nil
}

// ----------------------------------------------------------------
// メインロジック
// ----------------------------------------------------------------

// CombineContents は、成功した抽出結果の本文を効率的に結合します。
// (変更なし)
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
// 修正: 中間統合要約 (Intermediate Summary) を生成する役割に変更し、apiKeyOverrideを削除
func (c *Cleaner) CleanAndStructureText(ctx context.Context, combinedText string) (string, error) {

	// 1. LLMクライアントの初期化 (削除)

	// 2. Mapフェーズのためのテキスト分割
	segments := c.segmentText(combinedText, MaxSegmentChars)
	slog.Info("テキストをセグメントに分割しました", slog.Int("segments", len(segments)))

	// 3. Mapフェーズの実行（各セグメントの並列処理）
	// c.client を使用
	intermediateSummaries, err := c.processSegmentsInParallel(ctx, c.client, segments)
	if err != nil {
		// このエラーは Map-Reduce の最初のReduceが実行される前の、中間要約の結合テキストになる
		return "", fmt.Errorf("コンテンツのセグメント処理（Mapフェーズ）中にエラーが発生しました: %w", err)
	}

	// 4. Reduceフェーズの準備：中間要約の結合
	intermediateCombinedText := strings.Join(intermediateSummaries, "\n\n--- INTERMEDIATE SUMMARY END ---\n\n")

	// 5. Reduceフェーズ：中間要約の統合と構造化のためのLLM呼び出し
	slog.Info("中間要約の結合が完了しました。Reduceフェーズ（中間統合要約）を開始します。")

	// Reduce プロンプト（reduce_final_prompt.md）を使用して中間統合要約を作成
	reduceData := prompts.ReduceTemplateData{CombinedText: intermediateCombinedText}
	finalPrompt, err := c.reduceBuilder.BuildReduce(reduceData)
	if err != nil {
		return "", fmt.Errorf("Reduce プロンプトの生成に失敗しました: %w", err)
	}

	// Reduceフェーズのモデル名に c.ReduceModelName を使用
	finalResponse, err := c.client.GenerateContent(ctx, finalPrompt, c.ReduceModelName)
	if err != nil {
		return "", fmt.Errorf("LLM Reduce処理（中間統合要約）に失敗しました: %w", err)
	}

	// Reduceの結果（中間統合要約）を返します。
	// このテキストは、次の FinalSummary の入力となります。
	return finalResponse.Text, nil
}

// ----------------------------------------------------------------
// 新規追加の LLM 処理メソッド
// ----------------------------------------------------------------

// GenerateFinalSummary は、中間統合要約を元に、簡潔な最終要約を生成します。
func (c *Cleaner) GenerateFinalSummary(ctx context.Context, title string, intermediateSummary string) (string, error) {
	slog.Info("Final Summary Generation（最終要約）を開始します。")

	// Final Summary プロンプト（final_summary_prompt.md）を使用して最終要約を作成
	summaryData := prompts.FinalSummaryTemplateData{
		Title:               title,
		IntermediateSummary: intermediateSummary,
	}
	prompt, err := c.finalSummaryBuilder.BuildFinalSummary(summaryData)
	if err != nil {
		return "", fmt.Errorf("Final Summary プロンプトの生成に失敗しました: %w", err)
	}

	// SummaryModelName を使用
	response, err := c.client.GenerateContent(ctx, prompt, c.SummaryModelName)
	if err != nil {
		return "", fmt.Errorf("LLM Final Summary処理（最終要約）に失敗しました: %w", err)
	}
	slog.Info("Final Summary Generation（最終要約）が完了しました。", slog.Int("summary_length", len(response.Text)))

	return response.Text, nil
}

// GenerateScriptForVoicevox は、最終要約を元に、VOICEVOXエンジン向けのスクリプトを生成します。
func (c *Cleaner) GenerateScriptForVoicevox(ctx context.Context, title string, finalSummary string) (string, error) {
	slog.Info("Script Generation（スクリプト作成）を開始します。")

	// Script プロンプト（zundametan_duet.md）を使用してスクリプトを作成
	scriptData := prompts.ScriptTemplateData{
		Title:            title,
		FinalSummaryText: finalSummary,
	}
	prompt, err := c.scriptBuilder.BuildScript(scriptData)
	if err != nil {
		return "", fmt.Errorf("Script プロンプトの生成に失敗しました: %w", err)
	}

	// ScriptModelName を使用
	response, err := c.client.GenerateContent(ctx, prompt, c.ScriptModelName)
	if err != nil {
		return "", fmt.Errorf("LLM Script Generation処理に失敗しました: %w", err)
	}

	scriptText := ExtractTextBetweenTags(response.Text, "SCRIPT_START", "SCRIPT_END")

	// 抽出に失敗した場合の処理（例: LLMがタグを出力しなかった場合）
	if scriptText == "" {
		slog.Warn("指定されたスクリプトマーカーが見つからないか、形式が不正です。LLMのレスポンス全体をスクリプトとして使用します。",
			slog.String("startTag", "SCRIPT_START"),
			slog.String("endTag", "SCRIPT_END"),
			slog.String("llm_response_prefix", response.Text[:min(len(response.Text), 100)]), // レスポンスの一部をログに出力
		)
		return response.Text, nil
	}

	// 抽出されたスクリプトを返す
	return scriptText, nil
}

// ----------------------------------------------------------------
// ヘルパー関数群
// ----------------------------------------------------------------

func ExtractTextBetweenTags(text, startTag, endTag string) string {
	startMarker := fmt.Sprintf("<%s>", strings.ToUpper(startTag))
	// 閉じタグは、</TAG> と <TAG> の両方に対応するため、正規表現は使わず、
	// まず </TAG> を試行し、なければ <TAG> を試行するロジックに変更

	// 試行 1: 標準的な閉じタグ (例: </SCRIPT_END>)
	endMarker1 := fmt.Sprintf("</%s>", strings.ToUpper(endTag))
	// 試行 2: スラッシュなしの閉じタグ (LLMがよく間違えるパターン)
	endMarker2 := fmt.Sprintf("<%s>", strings.ToUpper(endTag))

	startIndex := strings.Index(text, startMarker)
	if startIndex == -1 {
		return ""
	}
	startIndex += len(startMarker)

	// 最初に </SCRIPT_END> の位置を探す
	endIndex := strings.LastIndex(text, endMarker1)
	if endIndex == -1 {
		// 見つからなければ <SCRIPT_END> の位置を探す
		endIndex = strings.LastIndex(text, endMarker2)
	}

	if endIndex == -1 || endIndex < startIndex {
		return ""
	}

	return strings.TrimSpace(text[startIndex:endIndex])
}

// segmentText は、結合されたテキストを、安全な最大文字数を超えないように分割します。
// (変更なし)
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
			// 修正: 組み込みmax()関数を使用
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
			if c.Verbose {
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
func (c *Cleaner) processSegmentsInParallel(ctx context.Context, client *gemini.Client, segments []string) ([]string, error) {
	var wg sync.WaitGroup

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

			mapData := prompts.MapTemplateData{SegmentText: seg}
			prompt, err := c.mapBuilder.BuildMap(mapData)
			if err != nil {
				resultsChan <- struct {
					index   int
					summary string
					err     error
				}{index: index + 1, summary: "", err: fmt.Errorf("プロンプト生成失敗: %w", err)}
				return
			}

			// Mapフェーズのモデル名に c.MapModelName を使用
			response, err := client.GenerateContent(ctx, prompt, c.MapModelName)

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
			// エラーが発生した場合、エラーメッセージを蓄積
			errorMessages = append(errorMessages, fmt.Sprintf("セグメント %d: %v", res.index, res.err))
		} else {
			// 成功した場合、要約を蓄積
			summaries = append(summaries, res.summary)
		}
	}

	// 蓄積されたエラーをチェックし、あれば単一のエラーとして結合して返す
	if len(errorMessages) > 0 {
		return nil, fmt.Errorf("Mapフェーズで %d 件のエラーが発生しました:\n- %s",
			len(errorMessages),
			strings.Join(errorMessages, "\n- "))
	}

	return summaries, nil
}
