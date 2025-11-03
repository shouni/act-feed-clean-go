package prompts

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed map_segment_prompt.md
var MapSegmentPromptTemplate string

//go:embed reduce_final_prompt.md
var ReduceFinalPromptTemplate string

// ----------------------------------------------------------------
// テンプレート構造体
// ----------------------------------------------------------------

type MapTemplateData struct {
	SegmentText string
}

type ReduceTemplateData struct {
	CombinedText string
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
// パースに失敗した場合は、内部にエラーを保持したPromptBuilderを返します。
func NewMapPromptBuilder() *PromptBuilder {
	tmpl, err := template.New("map_segment").Parse(MapSegmentPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// NewReducePromptBuilder は Reduceフェーズ用の PromptBuilder を初期化します。
// パースに失敗した場合は、内部にエラーを保持したPromptBuilderを返します。
func NewReducePromptBuilder() *PromptBuilder {
	tmpl, err := template.New("reduce_final").Parse(ReduceFinalPromptTemplate)
	return &PromptBuilder{tmpl: tmpl, err: err}
}

// Err は PromptBuilder の初期化（テンプレートパース）時に発生したエラーを返します。
func (b *PromptBuilder) Err() error {
	return b.err
}

// BuildMap は MapTemplateData を埋め込み、Geminiへ送るための最終的なプロンプト文字列を完成させます。
func (b *PromptBuilder) BuildMap(data MapTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Map prompt template is not properly initialized: %w", b.err)
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Mapプロンプトの実行に失敗しました: %w", err)
	}

	if data.SegmentText == "" {
		return "", fmt.Errorf("Mapプロンプト実行失敗: SegmentTextが空です (template: %s)", b.tmpl.Name())
	}

	return sb.String(), nil
}

// BuildReduce は ReduceTemplateData を埋め込み、Geminiへ送るための最終的なプロンプト文字列を完成させます。
func (b *PromptBuilder) BuildReduce(data ReduceTemplateData) (string, error) {
	if b.tmpl == nil || b.err != nil {
		return "", fmt.Errorf("Reduce prompt template is not properly initialized: %w", b.err)
	}

	var sb strings.Builder
	if err := b.tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("Reduceプロンプトの実行に失敗しました: %w", err)
	}

	if data.CombinedText == "" {
		return "", fmt.Errorf("Reduceプロンプト実行失敗: CombinedTextが空です (template: %s)", b.tmpl.Name())
	}

	return sb.String(), nil
}
