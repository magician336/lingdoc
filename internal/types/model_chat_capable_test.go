package types

import "testing"

// TestIsChatCapableModelType pins the rule the chat client path relies on:
// only the model types whose upstream speaks the text-generation protocol may
// be turned into a chat client.
//
// Embedding, Rerank and ASR models talk to other endpoint families, so a
// chat client built for them produces a call the provider rejects. VLLM is
// included on purpose: it is a multimodal model served over the same
// OpenAI-compatible chat/completions protocol, and the query-understand
// fallback loads it through GetChatModel.
func TestIsChatCapableModelType(t *testing.T) {
	cases := []struct {
		modelType ModelType
		want      bool
	}{
		{ModelTypeKnowledgeQA, true},
		{ModelTypeVLLM, true},
		{ModelTypeEmbedding, false},
		{ModelTypeRerank, false},
		{ModelTypeASR, false},
		{ModelType(""), false},
		{ModelType("Chat"), false},
	}
	for _, c := range cases {
		if got := IsChatCapableModelType(c.modelType); got != c.want {
			t.Errorf("IsChatCapableModelType(%q) = %v, want %v", c.modelType, got, c.want)
		}
	}
}

// TestIsChatCapableModelTypeFollowsTypeNotName pins that the verdict follows
// the model type: renaming a model must not change what it can serve.
func TestIsChatCapableModelTypeFollowsTypeNotName(t *testing.T) {
	// A chat-named model of Embedding type is still an embedding model.
	if IsChatCapableModelType(ModelTypeEmbedding) {
		t.Fatal("an Embedding model must not be chat capable, whatever it is called")
	}
	// An embedding-named model of KnowledgeQA type is a chat model.
	if !IsChatCapableModelType(ModelTypeKnowledgeQA) {
		t.Fatal("a KnowledgeQA model must stay chat capable, whatever it is called")
	}
}
