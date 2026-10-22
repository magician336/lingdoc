package workspacecore

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Issue #401: a research proposal is what the research agent hands back when
// the user asks for material on a topic. It travels on the existing turn
// endpoint, under one key, and it is always a candidate: it becomes a
// research task, an import candidate, an evidence card or a research claim
// only after a person saves it.
//
// This module owns the shape. The prompt renders it, the decoder is the only
// way in, and a payload that does not decode is refused whole - it is never
// reshaped on the way to the interface.

// ResearchProposalKey is the key a proposal travels under, both in the model's
// structured output and in the turn's suggestions.
const ResearchProposalKey = "task_card"

// ResearchProposalMinimumQueries is the smallest number of queries a proposal
// may carry. One query does not define a search, and the research task save
// path refuses fewer than two as well, so a proposal that decodes can be
// saved without being edited into shape first.
const ResearchProposalMinimumQueries = 2

// ResearchProposalMaximumQueries mirrors the research task's own bound: a
// proposal may not carry more queries than a task may hold.
const ResearchProposalMaximumQueries = 8

// ResearchProposalFailureCode is the failure a turn carries when the model
// stated a proposal that does not decode. The reply is lost with it: an
// interface that shows a proposal nobody can read is worse than a plain
// failure.
const ResearchProposalFailureCode = "invalid_proposal"

// ResearchProposalAgent is the agent whose answers carry a proposal. The agent
// and purpose vocabulary gets its own owner in a later ticket; until then the
// proposal contract names the one agent it applies to. The assistant's
// editable task suggestion is a different card and keeps its own rules.
const ResearchProposalAgent = "research_agent"

// researchProposalMaxPayload is the largest proposal the server reads.
const researchProposalMaxPayload = 60000

// ResearchProposalFields are the field names of the shape, in the order a
// prompt reads them. This list is the only place the shape is written down.
var ResearchProposalFields = []string{"goal", "question_type", "scope", "exclusions", "queries", "expected_output"}

// ResearchProposal is the shape of a research proposal.
//
// The field names are the research task's own field names: a proposal and the
// task it becomes speak one vocabulary, so nothing has to be translated on
// the way in, and the client stores what the user edited without renaming
// anything.
type ResearchProposal struct {
	Goal           string   `json:"goal"`
	QuestionType   string   `json:"question_type"`
	Scope          string   `json:"scope"`
	Exclusions     []string `json:"exclusions"`
	Queries        []string `json:"queries"`
	ExpectedOutput string   `json:"expected_output"`
}

// ResearchProposalShapeText renders the shape for a prompt. A prompt that
// spells the shape out itself is a second copy of the contract, and two copies
// drift; the prompt asks for this rendering instead.
//
// The rendering carries the rules that belong to the shape: a proposal needs
// at least two queries, and a synonym is an extra query rather than a field
// of its own.
func ResearchProposalShapeText() string {
	fields := make([]string, 0, len(ResearchProposalFields))
	for _, name := range ResearchProposalFields {
		if name == "queries" {
			fields = append(fields, name+"至少两条（同义词并入其中）")
			continue
		}
		fields = append(fields, name)
	}
	return strings.Join(fields, "、")
}

// ResearchProposalAbsent reports whether the raw payload states no proposal at
// all: the key is missing, or its value is JSON null.
func ResearchProposalAbsent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// DecodeResearchProposal decodes and validates a research proposal.
//
// A proposal that does not decode is refused as a whole. A half-understood
// proposal is worse than no proposal: the user would edit fields the server
// never promised, and the difference between "the model asked for this" and
// "the server guessed" would be lost.
func DecodeResearchProposal(raw json.RawMessage) (ResearchProposal, error) {
	proposal := ResearchProposal{}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > researchProposalMaxPayload || bytes.Equal(trimmed, []byte("null")) {
		return proposal, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return proposal, ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return proposal, ErrInvalidRequest
	}
	if err := validateResearchProposal(proposal); err != nil {
		return ResearchProposal{}, err
	}
	return proposal, nil
}

// validateResearchProposal holds the proposal to the same bounds the research
// task save path holds: what decodes here can be saved there without being
// edited into shape first.
func validateResearchProposal(proposal ResearchProposal) error {
	if strings.TrimSpace(proposal.Goal) == "" || len(proposal.Goal) > 6000 {
		return ErrInvalidRequest
	}
	if len(proposal.QuestionType) > 2000 {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(proposal.Scope) == "" || len(proposal.Scope) > 6000 {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(proposal.ExpectedOutput) == "" || len(proposal.ExpectedOutput) > 6000 {
		return ErrInvalidRequest
	}
	if len(proposal.Queries) < ResearchProposalMinimumQueries || len(proposal.Queries) > ResearchProposalMaximumQueries {
		return ErrInvalidRequest
	}
	for _, query := range proposal.Queries {
		if strings.TrimSpace(query) == "" || len(query) > 1000 {
			return ErrInvalidRequest
		}
	}
	if len(proposal.Exclusions) > 50 {
		return ErrInvalidRequest
	}
	for _, exclusion := range proposal.Exclusions {
		if strings.TrimSpace(exclusion) == "" || len(exclusion) > 1000 {
			return ErrInvalidRequest
		}
	}
	return nil
}

// The proposal has three states. They are facts about one answer, not verdicts
// about the project.
//
//   - present: the answer carries a proposal that decodes;
//   - absent: the answer carries no proposal at all;
//   - invalid: the answer carries a proposal that does not decode.
const (
	ResearchProposalStatePresent = "present"
	ResearchProposalStateAbsent  = "absent"
	ResearchProposalStateInvalid = "invalid"
)

// ResearchProposalStateForTurn judges, from the persisted turn alone, which of
// the three states the turn's answer is in. The stored turn is the only
// source: a client cannot say which state a proposal is in.
func ResearchProposalStateForTurn(turn AgentTurn) (string, error) {
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil {
		return "", err
	}
	if ResearchProposalAbsent(raw) {
		return ResearchProposalStateAbsent, nil
	}
	if _, err = DecodeResearchProposal(raw); err != nil {
		return ResearchProposalStateInvalid, nil
	}
	return ResearchProposalStatePresent, nil
}

// researchProposalFromSuggestions reads the proposal out of a turn's
// suggestions, which carry the model's whole structured answer.
func researchProposalFromSuggestions(suggestions json.RawMessage) (json.RawMessage, error) {
	if len(suggestions) == 0 {
		return nil, nil
	}
	var output struct {
		TaskCard json.RawMessage `json:"task_card"`
	}
	if err := json.Unmarshal(suggestions, &output); err != nil {
		return nil, ErrInvalidRequest
	}
	return output.TaskCard, nil
}

// researchProposalError validates the proposal a turn's model answer carries.
//
// Only the research agent's answers are a proposal. The assistant's editable
// task suggestion is a different card with its own rules, so this contract
// does not touch it. A proposal that does not decode is an error: it never
// becomes a suggestion.
func researchProposalError(agent string, structured json.RawMessage) error {
	if agent != ResearchProposalAgent {
		return nil
	}
	raw, err := researchProposalFromSuggestions(structured)
	if err != nil {
		return err
	}
	if ResearchProposalAbsent(raw) {
		return nil
	}
	_, err = DecodeResearchProposal(raw)
	return err
}
