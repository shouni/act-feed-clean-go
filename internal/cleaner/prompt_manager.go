package cleaner

import (
	"act-feed-clean-go/prompts"
	"fmt"
)

// PromptManager は、Map-Reduceや最終要約などに使用される
// 各プロンプトテンプレートのビルダーを管理します。
type PromptManager struct {
	MapBuilder          *prompts.PromptBuilder
	ReduceBuilder       *prompts.PromptBuilder
	FinalSummaryBuilder *prompts.PromptBuilder
	ScriptBuilder       *prompts.PromptBuilder
}

// NewPromptManager は PromptManager を初期化し、必要なすべてのPromptBuilderを作成します。
func NewPromptManager() (*PromptManager, error) {
	mapBuilder := prompts.NewMapPromptBuilder()
	if err := mapBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Map プロンプトビルダーの初期化に失敗しました: %w", err)
	}
	reduceBuilder := prompts.NewReducePromptBuilder()
	if err := reduceBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Reduce プロンプトビルダーの初期化に失敗しました: %w", err)
	}
	finalSummaryBuilder := prompts.NewFinalSummaryPromptBuilder()
	if err := finalSummaryBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Final Summary プロンプトビルダーの初期化に失敗しました: %w", err)
	}
	scriptBuilder := prompts.NewScriptPromptBuilder()
	if err := scriptBuilder.Err(); err != nil {
		return nil, fmt.Errorf("Script プロンプトビルダーの初期化に失敗しました: %w", err)
	}

	return &PromptManager{
		MapBuilder:          mapBuilder,
		ReduceBuilder:       reduceBuilder,
		FinalSummaryBuilder: finalSummaryBuilder,
		ScriptBuilder:       scriptBuilder,
	}, nil
}
