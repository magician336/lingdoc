package workspacecore

import (
	"encoding/json"
	"strings"
	"testing"
)

// The research proposal contract is the seam the whole path reads: the model
// proposes, the server decodes against this one shape, and the interface only
// ever sees a proposal that decoded. The cases below pin the decoder at that
// seam - a payload that does not decode is refused whole, never reshaped.

func TestResearchProposalAcceptsLegalShapeAndKeepsSynonymAsExtraQuery(t *testing.T) {
	raw := json.RawMessage(`{"goal":"弄清合成材料的定义","question_type":"定义","scope":"只查项目资料","exclusions":["不含二手综述"],"queries":["合成材料 定义","合成材料 反例","synthetic material definition"],"expected_output":"候选资料与反例"}`)
	proposal, err := DecodeResearchProposal(raw)
	if err != nil {
		t.Fatalf("legal proposal refused: %v", err)
	}
	if proposal.Goal != "弄清合成材料的定义" || proposal.QuestionType != "定义" || proposal.Scope != "只查项目资料" || proposal.ExpectedOutput != "候选资料与反例" {
		t.Fatalf("proposal lost a field: %+v", proposal)
	}
	if len(proposal.Queries) != 3 || proposal.Queries[2] != "synthetic material definition" {
		t.Fatalf("a synonym is not an extra query: %+v", proposal.Queries)
	}
	if len(proposal.Exclusions) != 1 || proposal.Exclusions[0] != "不含二手综述" {
		t.Fatalf("exclusions lost: %+v", proposal.Exclusions)
	}
}

func TestResearchProposalRefusesUnknownField(t *testing.T) {
	raw := json.RawMessage(`{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o","confidence":0.9}`)
	if _, err := DecodeResearchProposal(raw); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestResearchProposalRefusesMissingRequiredField(t *testing.T) {
	for name, raw := range map[string]string{
		"no goal":            `{"scope":"s","queries":["a","b"],"expected_output":"o"}`,
		"no scope":           `{"goal":"g","queries":["a","b"],"expected_output":"o"}`,
		"no expected output": `{"goal":"g","scope":"s","queries":["a","b"]}`,
		"no queries":         `{"goal":"g","scope":"s","expected_output":"o"}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw)); err == nil {
			t.Fatalf("missing required field accepted: %s", name)
		}
	}
}

func TestResearchProposalRefusesFewerThanTwoQueries(t *testing.T) {
	for name, raw := range map[string]string{
		"one query":  `{"goal":"g","scope":"s","queries":["a"],"expected_output":"o"}`,
		"no query":   `{"goal":"g","scope":"s","queries":[],"expected_output":"o"}`,
		"blank line": `{"goal":"g","scope":"s","queries":["a","  "],"expected_output":"o"}`,
	} {
		if _, err := DecodeResearchProposal(json.RawMessage(raw)); err == nil {
			t.Fatalf("fewer than two usable queries accepted: %s", name)
		}
	}
}

func TestResearchProposalRefusesOversizedPayload(t *testing.T) {
	raw := json.RawMessage(`{"goal":"` + strings.Repeat("长", 70000) + `","scope":"s","queries":["a","b"],"expected_output":"o"}`)
	if _, err := DecodeResearchProposal(raw); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestResearchProposalStatesArePresentAbsentAndInvalid(t *testing.T) {
	legal := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r","task_card":{"goal":"g","scope":"s","queries":["a","b"],"expected_output":"o"}}`)}
	state, err := ResearchProposalStateForTurn(legal)
	if err != nil || state != ResearchProposalStatePresent {
		t.Fatalf("legal proposal is not present: %q %v", state, err)
	}
	absent := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r"}`)}
	state, err = ResearchProposalStateForTurn(absent)
	if err != nil || state != ResearchProposalStateAbsent {
		t.Fatalf("missing proposal is not absent: %q %v", state, err)
	}
	invalid := AgentTurn{Suggestions: json.RawMessage(`{"reply":"r","task_card":{"goal":"g","scope":"s","queries":["a"],"expected_output":"o"}}`)}
	state, err = ResearchProposalStateForTurn(invalid)
	if err != nil || state != ResearchProposalStateInvalid {
		t.Fatalf("broken proposal is not invalid: %q %v", state, err)
	}
}
