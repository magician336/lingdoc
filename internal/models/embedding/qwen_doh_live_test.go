package embedding

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestQwenEmbeddingDoHLive exercises the real embedding adapter in the same
// fake-IP configuration as the chat acceptance test. It makes one short,
// explicitly opted-in billable request and never prints the credential.
func TestQwenEmbeddingDoHLive(t *testing.T) {
	if os.Getenv("LINGDOC_QWEN_DOH_EMBED_LIVE_TEST") != "1" {
		t.Skip("requires explicit Qwen embedding network test")
	}
	key := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_API_KEY"))
	if key == "" {
		t.Fatal("Qwen test credential is unavailable")
	}
	model, err := NewOpenAIEmbedder(key, "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen3.7-text-embedding-flash", 0, 0, "qwen-doh-test", nil)
	if err != nil {
		t.Fatalf("initialize Qwen embedding adapter: %v", err)
	}
	model.maxRetries = 0
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	vectors, err := model.BatchEmbed(ctx, []string{"open scientific literature retrieval"})
	if err != nil {
		t.Fatalf("real Qwen embedding request failed: %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 1024 {
		t.Fatalf("unexpected Qwen embedding dimensions: count=%d", len(vectors))
	}
}
