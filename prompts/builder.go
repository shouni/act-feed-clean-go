package prompts

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

// --- テンプレート埋め込み ---

//go:embed map_segment_prompt.md
var MapSegmentPromptTemplate string

//go:embed reduce_final_prompt.md
var ReduceFinalPromptTemplate string

//go:embed zundametan_duet.md
var zundametanDuetPromptTemplate string // 修正: Script Templateとして利用

//go:embed final_summary_prompt.md
var FinalSummaryPromptTemplate string

// ---

// ----------------------------------------------------------------
// テンプレート構造体 (修正・追加)
// ----------------------------------------------------------------

type MapTemplateData struct {
	SegmentText string
}

// ReduceTemplateData は Mapの結果を統合する（中間要約）。
type ReduceTemplateData struct {
	CombinedText string
}

// FinalSummaryTemplateData は中間要約を元に最終要約を作成する。
type FinalSummaryTemplateData struct {
	Title               string
	IntermediateSummary string // Reduceの結果
}

// ScriptTemplateData は最終要約を元にVOICEVOX用スクリプトを作成する。
type ScriptTemplateData struct {
	Title            string
	FinalSummaryText string // Final Summaryの結果
}

// ----------------------------------------------------------------
// ビルダー実装 (修正・追加)
// ----------------------------------------------------------------

// PromptBuilder はプロンプトの構成とテンプレート実行を管理します。
type PromptBuilder struct {
	tmpl *template.Template
	err  error
}

// NewMapPromptBuilder は Mapフェーズ用の PromptBuilder を初期化します。
func NewMapPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("map_segment").Parse(MapSegmentPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// NewReducePromptBuilder は Reduceフェーズ用の PromptBuilder を初期化します。
func NewReducePromptBuilder() *PromptBuilder {
	tmpl, err := template.New("reduce_final").Parse(ReduceFinalPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// --- 新規追加のコンストラクタ ---

// NewFinalSummaryPromptBuilder は 最終要約フェーズ用の PromptBuilder を初期化します。
func NewFinalSummaryPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("final_summary").Parse(FinalSummaryPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// NewScriptPromptBuilder は スクリプト作成フェーズ用の PromptBuilder を初期化します。
// zundametan_duet.md を使用します。
func NewScriptPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("script_voicevox").Parse(zundametanDuetPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// Err は PromptBuilder の初期化（テンプレートパース）時に発生したエラーを返します。
func (b *PromptBuilder) Err() error {
	return b.err
}

// BuildMap は MapTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildMap(data MapTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Map prompt template is not properly initialized: %w", b.err)
	}

	if data.SegmentText == "" {
		return "", fmt.Errorf("Mapプロンプト実行失敗: SegmentTextが空です (template: %s)", b.tmpl.Name())
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Mapプロンプトの実行に失敗しました: %w", err)
	}

	return sb.String(), nil
}

// BuildReduce は ReduceTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildReduce(data ReduceTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Reduce prompt template is not properly initialized: %w", b.err)
	}

	if data.CombinedText == "" {
		return "", fmt.Errorf("Reduceプロンプト実行失敗: CombinedTextが空です (template: %s)", b.tmpl.Name())
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Reduceプロンプトの実行に失敗しました: %w", err)
	}

	return sb.String(), nil
}

// BuildFinalSummary は FinalSummaryTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildFinalSummary(data FinalSummaryTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Final Summary prompt template is not properly initialized: %w", b.err)
	}

	if data.IntermediateSummary == "" {
		return "", fmt.Errorf("Final Summaryプロンプト実行失敗: IntermediateSummaryが空です (template: %s)", b.tmpl.Name())
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Final Summaryプロンプトの実行に失敗しました: %w", err)
	}

	return sb.String(), nil
}

// BuildScript は ScriptTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildScript(data ScriptTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Script prompt template is not properly initialized: %w", b.err)
	}

	if data.FinalSummaryText == "" {
		return "", fmt.Errorf("Scriptプロンプト実行失敗: FinalSummaryTextが空です (template: %s)", b.tmpl.Name())
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Scriptプロンプトの実行に失敗しました: %w", err)
	}

	return sb.String(), nil
}
