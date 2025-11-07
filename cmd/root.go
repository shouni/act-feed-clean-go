package cmd

import (
	"act-feed-clean-go/internal/pipeline"
	"context"
	"log/slog"
	"os"
	"time"

	"act-feed-clean-go/internal/cleaner"

	"github.com/shouni/go-cli-base"
	"github.com/spf13/cobra"
)

// ----------------------------------------------------------------------
// 構造体と定数
// ----------------------------------------------------------------------

// RunFlags は 'run' コマンド固有のフラグを保持する構造体です。
type RunFlags struct {
	FeedURL       string
	Parallel      int
	HttpTimeout   time.Duration
	OutputWAVPath string
	CleanerConfig cleaner.CleanerConfig
}

var Flags RunFlags

const (
	// contextTimeout は、パイプライン全体の実行に許容される最大時間です。
	contextTimeout = 20 * time.Minute
)

// ----------------------------------------------------------------------
// ヘルパー関数 (ロギング、正規化、初期化) (initLogger を保持)
// ----------------------------------------------------------------------

// initLogger はアプリケーションのデフォルトロガーを設定します。
func initLogger() {
	logLevel := slog.LevelInfo
	if clibase.Flags.Verbose {
		logLevel = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
	slog.Info("ロガーを初期化しました", slog.String("level", logLevel.String()))
}

// ----------------------------------------------------------------------
// Cobra コマンド実行関数
// ----------------------------------------------------------------------

// runCmdFunc は 'run' サブコマンドが呼び出されたときに実行される関数です。
func runCmdFunc(cmd *cobra.Command, args []string) error {
	parentCtx := cmd.Context()
	ctx, cancel := context.WithTimeout(parentCtx, contextTimeout)
	defer cancel()

	initLogger()

	// 1. 依存関係の構築（generate.go にあるヘルパー関数に委譲）
	deps, err := newAppDependencies(ctx, Flags)
	if err != nil {
		return err
	}

	pipelineConfig := pipeline.PipelineConfig{
		Parallel:      Flags.Parallel,
		OutputWAVPath: Flags.OutputWAVPath,
		ClientTimeout: Flags.HttpTimeout,
		Verbose:       clibase.Flags.Verbose,
	}

	// 2. Pipelineインスタンスを生成（依存関係を注入）
	pipelineInstance := pipeline.New(
		deps.ScraperRunner,
		deps.Cleaner,
		deps.VoicevoxEngineExecutor,
		pipelineConfig,
	)

	// 3. Pipelineの実行
	return pipelineInstance.Run(ctx, Flags.FeedURL)
}

// ----------------------------------------------------------------------
// Cobra コマンド定義 (フラグ、Execute)
// ----------------------------------------------------------------------

// addRunFlags は 'run' コマンドに固有のフラグを設定します。
func addRunFlags(runCmd *cobra.Command) {
	// 注: CleanerConfigのフラグ名は、以前の修正で確認した正しいフィールド名を使用
	runCmd.Flags().StringVarP(&Flags.FeedURL,
		"feed-url", "f", "https://news.yahoo.co.jp/rss/categories/it.xml", "処理対象のRSSフィードURL")
	runCmd.Flags().IntVarP(&Flags.Parallel,
		"parallel", "p", 10, "Webスクレイピングの最大同時並列リクエスト数")
	runCmd.Flags().DurationVarP(&Flags.HttpTimeout,
		"http-timeout", "t", 30*time.Second, "HTTPタイムアウト時間")
	runCmd.Flags().StringVarP(&Flags.OutputWAVPath,
		"output-wav-path", "v", "asset/audio_output.wav", "音声合成されたWAVファイルの出力パス。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.MapModel,
		"map-model", cleaner.DefaultMapModelName, "Mapフェーズ (クリーンアップ) に使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.ReduceModel,
		"reduce-model", cleaner.DefaultReduceModelName, "Reduceフェーズ (スクリプト生成) に使用するAIモデル名 (例: gemini-2.5-pro)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.SummaryModel,
		"summary-model", cleaner.DefaultSummaryModelName, "最終要約フェーズに使用するAIモデル名 (例: gemini-2.5-flash)。")
	runCmd.Flags().StringVar(&Flags.CleanerConfig.ScriptModel,
		"script-model", cleaner.DefaultScriptModelName, "スクリプト生成フェーズに使用するAIモデル名 (例: gemini-2.5-pro)。")
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "RSSフィードの取得、並列抽出、AI構造化処理を実行します。",
	Long:  "RSSフィードからURLを抽出し、記事本文を並列で取得後、LLMでクリーンアップ・構造化します。",
	RunE:  runCmdFunc,
}

// Execute は、CLIアプリケーションのエントリポイントです。
func Execute() {
	addRunFlags(runCmd)
	clibase.Execute(
		"act-feed-clean-go",
		nil,
		nil,
		runCmd,
	)
}
