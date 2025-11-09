package cleaner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
// モデル名定数と設定
// ----------------------------------------------------------------

const (
	DefaultModelName = "gemini-2.5-flash"
	// DefaultMapModelName は Mapフェーズのデフォルトモデル名です。
	DefaultMapModelName = DefaultModelName
	// DefaultReduceModelName は Reduceフェーズのデフォルトモデル名です。
	DefaultReduceModelName = DefaultModelName
	// DefaultSummaryModelName は FinalSummaryフェーズのデフォルトモデル名です。
	DefaultSummaryModelName = DefaultModelName
	// DefaultScriptModelName は ScriptGenerationフェーズのデフォルトモデル名です。
	DefaultScriptModelName = DefaultModelName
	// DefaultLLMRateLimit は、LLMへのリクエスト間の最小間隔です。
	DefaultLLMRateLimit = 1000 * time.Millisecond
)

// Cleaner はコンテンツのクリーンアップと要約を担当します。
type Cleaner struct {
	client *gemini.Client // LLMクライアントを注入
	prompt *PromptManager // prompt_manager.go で定義
	config CleanerConfig
	// LLMリクエストレートリミットの間隔
	rateLimit time.Duration
}

type CleanerConfig struct {
	MapModel     string        // Mapフェーズで使用するGeminiモデル名
	ReduceModel  string        // Reduceフェーズで使用するGeminiモデル名
	SummaryModel string        // FinalSummaryフェーズで使用するGeminiモデル名
	ScriptModel  string        // ScriptGenerationフェーズで使用するGeminiモデル名
	LLMRateLimit time.Duration // LLMリクエストのレートリミット間隔
	Verbose      bool          // 詳細ログを有効にするか
}

// NewCleaner は新しいCleanerインスタンスを作成し、依存関係とPromptBuilderを初期化します。
func NewCleaner(client *gemini.Client, config CleanerConfig) (*Cleaner, error) {
	if client == nil {
		return nil, fmt.Errorf("LLMクライアントはnilであってはなりません")
	}

	// デフォルト値の設定
	if config.MapModel == "" {
		config.MapModel = DefaultMapModelName
	}
	if config.ReduceModel == "" {
		config.ReduceModel = DefaultReduceModelName
	}
	if config.SummaryModel == "" {
		config.SummaryModel = DefaultSummaryModelName
	}
	if config.ScriptModel == "" {
		config.ScriptModel = DefaultScriptModelName
	}
	if config.LLMRateLimit <= 0 {
		config.LLMRateLimit = DefaultLLMRateLimit
	}

	// PromptManagerを構築 (prompt_manager.goで定義)
	manager, err := NewPromptManager()
	if err != nil {
		return nil, fmt.Errorf("PromptManagerの初期化に失敗しました: %w", err)
	}

	return &Cleaner{
		client:    client, // 注入
		prompt:    manager,
		config:    config,
		rateLimit: config.LLMRateLimit,
	}, nil
}

// ----------------------------------------------------------------
// メインロジック
// ----------------------------------------------------------------

// CleanAndStructureText は、コンテンツをMap-Reduceパターンで構造化します。
// 最終的に中間統合要約を生成する役割を担います。
func (c *Cleaner) CleanAndStructureText(ctx context.Context, combinedText string) (string, error) {

	// 1. Mapフェーズのためのテキスト分割 (utils.goで定義)
	segments := c.segmentText(combinedText, MaxSegmentChars)
	slog.Info("テキストをセグメントに分割しました", slog.Int("segments", len(segments)))

	// 2. Mapフェーズの実行（各セグメントの並列処理）(utils.goで定義)
	intermediateSummaries, err := c.processSegmentsInParallel(ctx, segments)
	if err != nil {
		return "", fmt.Errorf("コンテンツのセグメント処理（Mapフェーズ）中にエラーが発生しました: %w", err)
	}

	// 3. Reduceフェーズの準備：中間要約の結合
	intermediateCombinedText := strings.Join(intermediateSummaries, "\n\n--- INTERMEDIATE SUMMARY END ---\n\n")

	// 4. Reduceフェーズ：中間要約の統合と構造化のためのLLM呼び出し
	slog.Info("中間要約の結合が完了しました。Reduceフェーズ（中間統合要約）を開始します。")

	// Reduce プロンプト（reduce_final_prompt.md）を使用して中間統合要約を作成
	reduceData := prompts.ReduceTemplateData{CombinedText: intermediateCombinedText}
	finalPrompt, err := c.prompt.ReduceBuilder.BuildReduce(reduceData)
	if err != nil {
		return "", fmt.Errorf("Reduce プロンプトの生成に失敗しました: %w", err)
	}

	// Reduceフェーズのモデル名に c.ReduceModel を使用
	finalResponse, err := c.client.GenerateContent(ctx, finalPrompt, c.config.ReduceModel)
	if err != nil {
		return "", fmt.Errorf("LLM Reduce処理（中間統合要約）に失敗しました: %w", err)
	}

	// Reduceの結果（中間統合要約）を返します。
	return finalResponse.Text, nil
}

// GenerateFinalSummary は、中間統合要約を元に、簡潔な最終要約を生成します。
// func (c *Cleaner) GenerateFinalSummary(ctx context.Context, title string, intermediateSummary string) (string, error) {
func (c *Cleaner) GenerateFinalSummary(ctx context.Context, intermediateSummary string) (string, error) {
	slog.Info("Final Summary Generation（最終要約）を開始します。")

	summaryData := prompts.FinalSummaryTemplateData{
		//		Title:               title,
		IntermediateSummary: intermediateSummary,
	}
	prompt, err := c.prompt.FinalSummaryBuilder.BuildFinalSummary(summaryData)
	if err != nil {
		return "", fmt.Errorf("Final Summary プロンプトの生成に失敗しました: %w", err)
	}

	// SummaryModelName を使用
	response, err := c.client.GenerateContent(ctx, prompt, c.config.SummaryModel)
	if err != nil {
		return "", fmt.Errorf("LLM Final Summary処理（最終要約）に失敗しました: %w", err)
	}
	slog.Info("Final Summary Generation（最終要約）が完了しました。", slog.Int("summary_length", len(response.Text)))

	return response.Text, nil
}

// GenerateScriptForVoicevox は、最終要約を元に、VOICEVOXエンジン向けのスクリプトを生成します。
// func (c *Cleaner) GenerateScriptForVoicevox(ctx context.Context, title string, finalSummary string) (string, error) {
func (c *Cleaner) GenerateScriptForVoicevox(ctx context.Context, finalSummary string) (string, error) {
	slog.Info("Script Generation（スクリプト作成）を開始します。")

	scriptData := prompts.ScriptTemplateData{
		//		Title:            title,
		FinalSummaryText: finalSummary,
	}
	prompt, err := c.prompt.ScriptBuilder.BuildScript(scriptData)
	if err != nil {
		return "", fmt.Errorf("Script プロンプトの生成に失敗しました: %w", err)
	}

	// ScriptModelName を使用
	response, err := c.client.GenerateContent(ctx, prompt, c.config.ScriptModel)
	if err != nil {
		return "", fmt.Errorf("LLM Script Generation処理に失敗しました: %w", err)
	}

	// utils.goで定義されたヘルパー関数を使用
	scriptText := ExtractTextBetweenTags(response.Text, "SCRIPT_START", "SCRIPT_END")

	if scriptText == "" {
		slog.Warn("指定されたスクリプトマーカーが見つからないか、形式が不正です。LLMのレスポンス全体をスクリプトとして使用します。",
			slog.String("startTag", "SCRIPT_START"),
			slog.String("endTag", "SCRIPT_END"),
			slog.String("llm_response_prefix", response.Text[:min(len(response.Text), 100)]),
		)
		return response.Text, nil
	}

	return scriptText, nil
}
