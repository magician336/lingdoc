package acceptancebudget

const (
	QwenEmbeddingFlash       = "qwen3.7-text-embedding-flash"
	QwenFlashNanoCNYPerToken = int64(125)
)

// EmbeddingInputNanoCNYPerToken is the exact acceptance embedding allowlist and
// immutable per-model price mapping. Historical original-Qwen records stay at
// 500; Flash is 125. The caller's budget profile is checked separately in
// Reserve and validLedger, so Flash never expands the legacy six-call profile.
func EmbeddingInputNanoCNYPerToken(model string) (int64, bool) {
	switch model {
	case QwenEmbedding:
		return QwenNanoCNYPerToken, true
	case QwenEmbeddingFlash:
		return QwenFlashNanoCNYPerToken, true
	default:
		return 0, false
	}
}
