package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func TestR32LiveBudgetOwnerBlocksEmbeddingBeforeHTTPTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.calls.json")
	budget := acceptancebudget.New(acceptancebudget.CostOnlyProfile, path)
	owner, err := budget.Reserve(context.Background(), acceptancebudget.DeepSeekModel, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.CancelNoCall() }()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * acceptancebudget.LockLeaseTTL)
	if err := os.Chtimes(path+".lock", stale, stale); err != nil {
		t.Fatal(err)
	}
	calls := 0
	embedder := &OpenAIEmbedder{modelName: acceptancebudget.QwenEmbeddingFlash,
		baseURL:    "https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		dimensions: 1024, supportsDimensionOverride: true, maxRetries: 0,
		httpClient: &http.Client{Transport: r31FlashRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, fmt.Errorf("unexpected provider dispatch")
		})}, acceptanceBudget: budget}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = embedder.BatchEmbed(ctx, []string{"hello"})
	if !errors.Is(err, acceptancebudget.ErrUnavailable) || calls != 0 {
		t.Fatalf("contender reached HTTP transport: err=%v calls=%d", err, calls)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("waiting adapter changed another reservation")
	}
}
