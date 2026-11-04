package workspacecore

import (
	"encoding/json"
	"strings"
	"testing"
)

// A live Qwen response supplied reasons as a string because the prompt did
// not name its JSON type. The model-facing contract must make the decoder's
// array requirement visible instead of silently coercing untrusted output.
func TestResearchProposalPromptStatesReasonsArrayContract(t *testing.T) {
	if !strings.Contains(ResearchProposalShapeText(), "reasons（JSON 字符串数组") {
		t.Fatal("the model-facing proposal shape does not say that reasons is a JSON string array")
	}
	prefix := `{"goal":"合成材料测试","scope":"测试资料","queries":["a","b"],"expected_output":"测试结果","reasons":`
	if _, err := DecodeResearchProposal(json.RawMessage(prefix+`"一个理由"}`), AgentContextPackage{}); err == nil {
		t.Fatal("a scalar reason must not be coerced into the array contract")
	}
	if _, err := DecodeResearchProposal(json.RawMessage(prefix+`["一个理由"]}`), AgentContextPackage{}); err != nil {
		t.Fatalf("a valid string array must remain accepted: %v", err)
	}
}
