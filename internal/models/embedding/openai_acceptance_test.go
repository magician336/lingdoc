package embedding

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func TestAcceptanceEmbeddingEndpointIsRestrictedToConfiguredAliyunModel(t *testing.T) {
	valid := "https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	if !acceptanceEmbeddingEndpointAllowed(acceptancebudget.QwenEmbedding, valid) {
		t.Fatal("configured Beijing Qwen embedding endpoint was rejected")
	}
	for _, tt := range []struct {
		model string
		url   string
	}{
		{model: "other-model", url: valid},
		{model: acceptancebudget.QwenEmbedding, url: "https://api.siliconflow.cn/v1"},
		{model: acceptancebudget.QwenEmbedding, url: "http://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
		{model: acceptancebudget.QwenEmbedding, url: "https://ws-example.cn-beijing.maas.aliyuncs.com/other/v1"},
	} {
		if acceptanceEmbeddingEndpointAllowed(tt.model, tt.url) {
			t.Fatalf("unexpectedly allowed model=%q endpoint=%q", tt.model, tt.url)
		}
	}
}

func TestAcceptanceEmbeddingInputUpperBoundCountsAllTextsAndRejectsOversize(t *testing.T) {
	got, err := acceptanceEmbeddingInputUpperBound([]string{"abc", "测试"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1024+3+len("测试") {
		t.Fatalf("upper bound = %d", got)
	}
	if _, err := acceptanceEmbeddingInputUpperBound([]string{"   "}); err == nil {
		t.Fatal("empty embedding input was accepted")
	}
	if _, err := acceptanceEmbeddingInputUpperBound([]string{strings.Repeat("x", acceptancebudget.InputMax)}); err == nil {
		t.Fatal("oversize embedding input was accepted")
	}
}
