package types

import "testing"

// TestIsSummaryCapableModelType pins the rule the knowledge-base summary
// guard relies on: only the chat (KnowledgeQA) model type can serve the
// text-generation call of a summary stage. Embedding, Rerank, ASR and VLLM
// models talk to other endpoint families and must never be bound to
// summary_model_id.
func TestIsSummaryCapableModelType(t *testing.T) {
	cases := []struct {
		modelType ModelType
		want      bool
	}{
		{ModelTypeKnowledgeQA, true},
		{ModelTypeEmbedding, false},
		{ModelTypeRerank, false},
		{ModelTypeASR, false},
		{ModelTypeVLLM, false},
		{ModelType(""), false},
		{ModelType("Chat"), false},
	}
	for _, c := range cases {
		if got := IsSummaryCapableModelType(c.modelType); got != c.want {
			t.Errorf("IsSummaryCapableModelType(%q) = %v, want %v", c.modelType, got, c.want)
		}
	}
}
