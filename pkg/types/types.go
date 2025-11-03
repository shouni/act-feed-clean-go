package types

// URLResult は、特定のURLから抽出された結果、またはその処理中に発生したエラーを保持します。
// これは、Scraperの出力、Cleanerの入力として利用されます。
type URLResult struct {
	URL     string // 処理対象のURL
	Content string // 抽出された記事の本文（または中間処理の結果）
	Error   error  // 処理中に発生したエラー
}
