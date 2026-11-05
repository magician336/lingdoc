package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func TestR31FlashEmbeddingEndpointKeepsAllExistingRestrictions(t *testing.T) {
	valid := "https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	for _, model := range []string{acceptancebudget.QwenEmbedding, acceptancebudget.QwenEmbeddingFlash} {
		if !acceptanceEmbeddingEndpointAllowed(model, valid) {
			t.Fatalf("authorized exact model rejected: %s", model)
		}
		for _, endpoint := range []string{
			"http://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
			"https://api.siliconflow.cn/v1",
			"https://ws-example.cn-beijing.maas.aliyuncs.com.evil.invalid/compatible-mode/v1",
			"https://user:password@ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
			"https://ws-example.cn-beijing.maas.aliyuncs.com:443/compatible-mode/v1",
			"https://ws-example.cn-beijing.maas.aliyuncs.com/other/v1",
			valid + "/embeddings", valid + "?model=other", valid + "#other",
		} {
			if acceptanceEmbeddingEndpointAllowed(model, endpoint) {
				t.Fatalf("endpoint guard widened: %s / %s", model, endpoint)
			}
		}
	}
	for _, model := range []string{"other-model", acceptancebudget.QwenEmbeddingFlash + "-extra", strings.ToUpper(acceptancebudget.QwenEmbeddingFlash), " " + acceptancebudget.QwenEmbeddingFlash} {
		if acceptanceEmbeddingEndpointAllowed(model, valid) {
			t.Fatalf("unknown model allowed: %s", model)
		}
	}
}

type r31FlashRoundTripper func(*http.Request) (*http.Response, error)

func (f r31FlashRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestR31FlashEmbedding1024AndPricingThroughRealBudgetWithoutNetwork(t *testing.T) {
	for _, profile := range []string{acceptancebudget.CostOnlyProfile, acceptancebudget.Profile} {
		t.Run(profile, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.calls.json")
			calls := 0
			transport := r31FlashRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if profile != acceptancebudget.CostOnlyProfile {
					t.Error("legacy profile reached transport")
				}
				if req.URL.Scheme != "https" || req.URL.Host != "ws-example.cn-beijing.maas.aliyuncs.com" || req.URL.Path != "/compatible-mode/v1/embeddings" {
					t.Errorf("unexpected request target: %s", req.URL)
				}
				var body OpenAIEmbedRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					return nil, err
				}
				if body.Model != acceptancebudget.QwenEmbeddingFlash || body.Dimensions != 1024 || len(body.Input) != 1 || body.Input[0] != "hello" {
					t.Error("model/dimension/input changed")
				}
				payload, err := json.Marshal(map[string]any{
					"data":  []map[string]any{{"embedding": make([]float32, 1024), "index": 0}},
					"usage": map[string]int{"prompt_tokens": 2, "total_tokens": 2},
				})
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
			})
			e := &OpenAIEmbedder{modelName: acceptancebudget.QwenEmbeddingFlash, baseURL: "https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
				dimensions: 1024, supportsDimensionOverride: true, maxRetries: 0, httpClient: &http.Client{Transport: transport},
				acceptanceBudget: acceptancebudget.New(profile, path)}
			vectors, err := e.BatchEmbed(context.Background(), []string{"hello"})
			if profile == acceptancebudget.Profile {
				if !errors.Is(err, acceptancebudget.ErrWrongModel) || calls != 0 {
					t.Fatalf("legacy profile did not reject before transport: %v / %d", err, calls)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("legacy Flash attempt wrote ledger")
				}
				return
			}
			if err != nil || calls != 1 || len(vectors) != 1 || len(vectors[0]) != 1024 {
				t.Fatalf("Flash synthetic vector/call result: %v / %d", err, calls)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var ledger acceptancebudget.Ledger
			if err := json.Unmarshal(data, &ledger); err != nil {
				t.Fatal(err)
			}
			if len(ledger.Requests) != 1 || ledger.Requests[0].Model != acceptancebudget.QwenEmbeddingFlash ||
				ledger.Requests[0].Status != "response_received" || ledger.ReservedNanoCNY != 128625 || ledger.ActualNanoCNY != 250 {
				t.Fatal("Flash budget price/upper bound/usage changed")
			}
		})
	}
}
