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

// The names the shape is written in. They are declared here, in the contract
// module, because the prompt renders them and the decoder reads them: two
// copies of a field name are two contracts that drift.
const (
	researchProposalGoalField                  = "goal"
	researchProposalQuestionTypeField          = "question_type"
	researchProposalScopeField                 = "scope"
	researchProposalExclusionsField            = "exclusions"
	researchProposalQueriesField               = "queries"
	researchProposalExpectedOutputField        = "expected_output"
	researchProposalCandidateSourcesField      = "candidate_sources"
	researchProposalCandidateSourceIDField     = "source_id"
	researchProposalCandidateSourceReasonField = "reason"
	researchProposalReasonsField               = "reasons"
	researchProposalGapsField                  = "gaps"
	researchProposalGapDescriptionField        = "description"
	researchProposalGapExpectedEvidenceField   = "expected_evidence"
)

// ResearchProposalMaximumCandidateSources is the largest number of candidate
// sources a proposal may name. A proposal says what it wants to read next; a
// longer list is a dump, not a plan.
const ResearchProposalMaximumCandidateSources = 20

// ResearchProposalMaximumCandidateSourceReason is the longest reason a
// candidate source may carry.
const ResearchProposalMaximumCandidateSourceReason = 1000

// ResearchProposalMaximumCandidateSourceID is the longest source id a candidate
// source may carry.
const ResearchProposalMaximumCandidateSourceID = 200

// ResearchProposalMaximumReasons is the largest number of reasons a proposal
// may carry. A proposal that cannot say why it chose this goal and this scope
// in a handful of lines has not said it at all.
const ResearchProposalMaximumReasons = 10

// ResearchProposalMaximumReasonLength is the longest reason a proposal may
// carry.
const ResearchProposalMaximumReasonLength = 1000

// ResearchProposalMaximumGaps is the largest number of gaps a proposal may
// carry. A longer list is a list of complaints, not a plan.
const ResearchProposalMaximumGaps = 10

// ResearchProposalMaximumGapDescriptionLength is the longest "what is missing"
// a gap may carry.
const ResearchProposalMaximumGapDescriptionLength = 1000

// ResearchProposalMaximumGapExpectedEvidenceLength is the longest "what would
// fill it" a gap may carry.
const ResearchProposalMaximumGapExpectedEvidenceLength = 1000

// ResearchProposalFields are the field names of the shape, in the order a
// prompt reads them. This list is the only place the shape is written down.
var ResearchProposalFields = []string{
	researchProposalGoalField,
	researchProposalQuestionTypeField,
	researchProposalScopeField,
	researchProposalExclusionsField,
	researchProposalQueriesField,
	researchProposalExpectedOutputField,
	researchProposalCandidateSourcesField,
	researchProposalReasonsField,
	researchProposalGapsField,
}

// ResearchProposalCandidateSource is one material the proposal wants to read
// next, with the reason it wants to read it.
//
// The reason is not decoration: a source without a reason is the model
// pointing at something without saying why, and the user cannot check that.
// The source id is not free text either - it has to be a source of the turn
// the proposal belongs to, or the proposal is refused.
type ResearchProposalCandidateSource struct {
	SourceID string `json:"source_id"`
	Reason   string `json:"reason"`
}

// ResearchProposalGap is one thing the proposal still misses, and what would
// fill it.
//
// The two parts are the point, not decoration. A gap that only says "no
// sources" tells the user nothing they can act on; a gap that says what kind
// of evidence is absent and what would close it is a decision the user can
// make. So the shape carries both, and the server refuses a gap that carries
// only one of them.
type ResearchProposalGap struct {
	Description      string `json:"description"`
	ExpectedEvidence string `json:"expected_evidence"`
}

// ResearchProposal is the shape of a research proposal.
//
// The field names are the research task's own field names: a proposal and the
// task it becomes speak one vocabulary, so nothing has to be translated on
// the way in, and the client stores what the user edited without renaming
// anything. The candidate sources are the one addition: they name what the
// agent wants to read next, and they are checked against the authorization
// the user gave this turn. The reasons and the gaps are the other two: they
// say why the plan looks like this, and what it still misses.
type ResearchProposal struct {
	Goal             string                            `json:"goal"`
	QuestionType     string                            `json:"question_type"`
	Scope            string                            `json:"scope"`
	Exclusions       []string                          `json:"exclusions"`
	Queries          []string                          `json:"queries"`
	ExpectedOutput   string                            `json:"expected_output"`
	CandidateSources []ResearchProposalCandidateSource `json:"candidate_sources"`
	Reasons          []string                          `json:"reasons"`
	Gaps             []ResearchProposalGap             `json:"gaps"`
}

// ResearchProposalShapeText renders the shape for a prompt. A prompt that
// spells the shape out itself is a second copy of the contract, and two copies
// drift; the prompt asks for this rendering instead.
//
// The rendering carries the rules that belong to the shape: a proposal needs
// at least two queries, a synonym is an extra query rather than a field of
// its own, a candidate source names a source of this turn, a reason says why
// the plan looks like this, and a gap says what is missing and what would
// fill it.
func ResearchProposalShapeText() string {
	fields := make([]string, 0, len(ResearchProposalFields))
	for _, name := range ResearchProposalFields {
		switch name {
		case researchProposalQueriesField:
			fields = append(fields, name+"至少两条（同义词并入其中）")
		case researchProposalCandidateSourcesField:
			fields = append(fields, name+"（每条含 "+researchProposalCandidateSourceIDField+" 与 "+researchProposalCandidateSourceReasonField+"；"+researchProposalCandidateSourceIDField+" 只能取本轮上下文给出的来源ID）")
		case researchProposalReasonsField:
			fields = append(fields, name+"（整份提案为什么这么定：目标为什么是这个目标、范围为什么划到这里）")
		case researchProposalGapsField:
			fields = append(fields, name+"（每条含 "+researchProposalGapDescriptionField+"（缺什么）与 "+researchProposalGapExpectedEvidenceField+"（期望补什么）；只有「没有来源」这种空话不算）")
		default:
			fields = append(fields, name)
		}
	}
	return strings.Join(fields, "、")
}

// ResearchProposalAbsent reports whether the raw payload states no proposal at
// all: the key is missing, or its value is JSON null.
func ResearchProposalAbsent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// DecodeResearchProposal decodes and validates a research proposal against
// the context the turn was given.
//
// The context is part of the contract, not an optional extra: a proposal that
// names a source this turn was not given cannot be checked against what the
// user actually allowed, so it is refused.
//
// A proposal that does not decode is refused as a whole. A half-understood
// proposal is worse than no proposal: the user would edit fields the server
// never promised, and the difference between "the model asked for this" and
// "the server guessed" would be lost.
func DecodeResearchProposal(raw json.RawMessage, pack AgentContextPackage) (ResearchProposal, error) {
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
	if err := validateResearchProposalReasonsAndGaps(proposal); err != nil {
		return ResearchProposal{}, err
	}
	if err := validateResearchProposalCandidateSources(proposal, pack); err != nil {
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

// validateResearchProposalReasonsAndGaps holds the two columns that say why
// the plan looks like this, and what it still misses.
//
// A gap is two parts or it is nothing: what is missing, and what would fill
// it. A gap that carries only one of them tells the user something they
// cannot act on - "没有来源" is the shape of that emptiness, and it is
// refused with the rest of the proposal rather than shown as if it were a
// finding. The count and the length of both columns are bounded like the
// rest of the shape, so a proposal nobody can read is refused whole.
func validateResearchProposalReasonsAndGaps(proposal ResearchProposal) error {
	if len(proposal.Reasons) > ResearchProposalMaximumReasons {
		return ErrInvalidRequest
	}
	for _, reason := range proposal.Reasons {
		if strings.TrimSpace(reason) == "" || len(reason) > ResearchProposalMaximumReasonLength {
			return ErrInvalidRequest
		}
	}
	if len(proposal.Gaps) > ResearchProposalMaximumGaps {
		return ErrInvalidRequest
	}
	for _, gap := range proposal.Gaps {
		if strings.TrimSpace(gap.Description) == "" || len(gap.Description) > ResearchProposalMaximumGapDescriptionLength {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(gap.ExpectedEvidence) == "" || len(gap.ExpectedEvidence) > ResearchProposalMaximumGapExpectedEvidenceLength {
			return ErrInvalidRequest
		}
	}
	return nil
}

// validateResearchProposalCandidateSources holds every candidate source to the
// authorization this turn actually had.
//
// The rule is not "the source exists": it is "this turn was given this
// source, and the user allowed the model to use it". A revoked source, a
// source of another project and a source the model was never shown all fail
// here.
//
// They fail loudly, and that is the point. A proposal that quietly dropped
// an unreadable source would leave the user believing the model never asked
// for it - the difference between "the agent wanted this" and "this is not
// available" would be lost, and the user would look for the missing source
// in the wrong place.
func validateResearchProposalCandidateSources(proposal ResearchProposal, pack AgentContextPackage) error {
	if len(proposal.CandidateSources) > ResearchProposalMaximumCandidateSources {
		return ErrInvalidRequest
	}
	allowed := map[string]bool{}
	for _, ref := range pack.SourceRefs {
		if ref.ModelUseAllowed {
			allowed[ref.SourceID] = true
		}
	}
	for _, source := range proposal.CandidateSources {
		if strings.TrimSpace(source.SourceID) == "" || len(source.SourceID) > ResearchProposalMaximumCandidateSourceID {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(source.Reason) == "" || len(source.Reason) > ResearchProposalMaximumCandidateSourceReason {
			return ErrInvalidRequest
		}
		if !allowed[source.SourceID] {
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
//
// The turn carries the context it was given, so the judgement also asks
// whether the proposal's candidate sources were readable in that very turn.
func ResearchProposalStateForTurn(turn AgentTurn) (string, error) {
	raw, err := researchProposalFromSuggestions(turn.Suggestions)
	if err != nil {
		return "", err
	}
	if ResearchProposalAbsent(raw) {
		return ResearchProposalStateAbsent, nil
	}
	if _, err = DecodeResearchProposal(raw, turn.Context); err != nil {
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
//
// The pack is the context the model was given. It is what makes a candidate
// source checkable: a source the turn did not carry cannot be cited.
func researchProposalError(agent string, structured json.RawMessage, pack AgentContextPackage) error {
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
	_, err = DecodeResearchProposal(raw, pack)
	return err
}
