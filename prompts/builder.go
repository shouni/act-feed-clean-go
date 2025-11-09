package prompts

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

// --- テンプレート埋め込み ---

//go:embed map_prompt.md
var MapSegmentPromptTemplate string

//go:embed reduce_prompt.md
var ReduceFinalPromptTemplate string

//go:embed summary_prompt.md
var FinalSummaryPromptTemplate string

//go:embed zundametan_duet.md
var zundametanDuetPromptTemplate string // VOICEVOXスクリプト生成用テンプレート

// ---

// ----------------------------------------------------------------
// テンプレート構造体
// ----------------------------------------------------------------

type MapTemplateData struct {
	Title       string
	SegmentText string
}

// ReduceTemplateData は Mapの結果を統合する（中間要約）。
type ReduceTemplateData struct {
	CombinedText string // Mapフェーズの結果を統合した中間要約テキスト
}

// FinalSummaryTemplateData は中間要約を元に最終要約を作成する。
type FinalSummaryTemplateData struct {
	//	Title               string
	IntermediateSummary string // Reduceフェーズの結果（中間要約）
}

// ScriptTemplateData は最終要約を元にVOICEVOX用スクリプトを作成する。
type ScriptTemplateData struct {
	//	Title            string
	FinalSummaryText string // Final Summaryフェーズの結果
}

// ----------------------------------------------------------------
// ビルダー実装
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

// NewFinalSummaryPromptBuilder は 最終要約フェーズ用の PromptBuilder を初期化します。
func NewFinalSummaryPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("final_summary").Parse(FinalSummaryPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// NewScriptPromptBuilder は VOICEVOXスクリプト作成フェーズ用の PromptBuilder を初期化します。
// zundametan_duet.md テンプレートを使用します。
func NewScriptPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("script_voicevox").Parse(zundametanDuetPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// Err は PromptBuilder の初期化（テンプレートパース）時に発生したエラーを返します。
func (b *PromptBuilder) Err() error {
	return b.err
}

// ----------------------------------------------------------------
// 汎用ビルドロジック (コア)
// ----------------------------------------------------------------

// buildPrompt はテンプレート実行の共通ロジックを処理します。
// data は任意のテンプレートデータ構造体を interface{} として受け取ります。
// emptyCheckFunc はデータ固有の空チェックを実行する関数です。
func (b *PromptBuilder) buildPrompt(data interface{}, emptyCheckFunc func(data interface{}) error) (string, error) {
	if err := b.Err(); err != nil {
		return "", fmt.Errorf("%s prompt template is not properly initialized: %w", b.tmpl.Name(), err)
	}

	// データ固有の空チェックを実行
	if err := emptyCheckFunc(data); err != nil {
		// emptyCheckFuncが具体的なフィールド名を含むエラーを返すため、それをそのまま利用
		return "", fmt.Errorf("%sプロンプト実行失敗: %w", b.tmpl.Name(), err)
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("%sプロンプトの実行に失敗しました: %w", b.tmpl.Name(), err)
	}

	return sb.String(), nil
}

// ----------------------------------------------------------------
// ビルドメソッド (BuildXxx は buildPrompt を呼び出すだけのラッパー)
// ----------------------------------------------------------------

// BuildMap は MapTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildMap(data MapTemplateData) (string, error) {
	return b.buildPrompt(data, func(d interface{}) error {
		if d.(MapTemplateData).SegmentText == "" {
			return fmt.Errorf("MapTemplateData.SegmentTextが空です")
		}
		return nil
	})
}

// BuildReduce は ReduceTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildReduce(data ReduceTemplateData) (string, error) {
	return b.buildPrompt(data, func(d interface{}) error {
		if d.(ReduceTemplateData).CombinedText == "" {
			return fmt.Errorf("ReduceTemplateData.CombinedTextが空です")
		}
		return nil
	})
}

// BuildFinalSummary は FinalSummaryTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildFinalSummary(data FinalSummaryTemplateData) (string, error) {
	return b.buildPrompt(data, func(d interface{}) error {
		if d.(FinalSummaryTemplateData).IntermediateSummary == "" {
			return fmt.Errorf("FinalSummaryTemplateData.IntermediateSummaryが空です")
		}
		return nil
	})
}

// BuildScript は ScriptTemplateData を埋め込み、プロンプト文字列を完成させます。
func (b *PromptBuilder) BuildScript(data ScriptTemplateData) (string, error) {
	return b.buildPrompt(data, func(d interface{}) error {
		if d.(ScriptTemplateData).FinalSummaryText == "" {
			return fmt.Errorf("ScriptTemplateData.FinalSummaryTextが空です")
		}
		return nil
	})
}
