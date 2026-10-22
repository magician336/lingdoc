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
// stated a proposal that does not decode.
//
// The code says what happened to the proposal, not to the answer: the reply
// the model wrote stays, and the turn still completes. The interface reads
// the code and tells the user that this turn carries no proposal.
const ResearchProposalFailureCode = "invalid_proposal"

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

// ResearchProposalTaskFields are the six fields that make a research task.
//
// They are the shape of the assistant's editable task suggestion too: a
// proposal is a research task plus three additions, and the assistant's card
// is the task part on its own. The two cards have different rules - the
// proposal contract judges the research agent's card and does not touch the
// assistant's - so they are rendered apart.
var ResearchProposalTaskFields = []string{
	researchProposalGoalField,
	researchProposalQuestionTypeField,
	researchProposalScopeField,
	researchProposalExclusionsField,
	researchProposalQueriesField,
	researchProposalExpectedOutputField,
}

// ResearchProposalAdditionFields are the three fields a proposal adds on top
// of the task fields: the candidate sources, the reasons and the gaps.
//
// They are the research agent's. The assistant's task suggestion has never
// carried them, and asking for them would change what the assistant answers.
var ResearchProposalAdditionFields = []string{
	researchProposalCandidateSourcesField,
	researchProposalReasonsField,
	researchProposalGapsField,
}

// ResearchProposalFields are the field names of the whole shape, in the order
// a prompt reads them. This list is the only place the shape is written down.
var ResearchProposalFields = append(append([]string{}, ResearchProposalTaskFields...), ResearchProposalAdditionFields...)

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

// The two renderings say the queries rule in their own words, because they
// are two different cards.
//
// The assistant's task suggestion has always asked for at least two queries
// ("至少两项"), and it must keep asking for exactly that: the proposal
// contract does not judge the assistant's card, so a wording change there
// would change the assistant's answer without anyone deciding to change it.
// The proposal states the same bound as "至少两条" and adds where a synonym
// goes.
const (
	researchProposalTaskQueriesRule     = "至少两项"
	researchProposalProposalQueriesRule = "至少两条（同义词并入其中）"
)

// ResearchProposalTaskShapeText renders the shape of the editable research
// task suggestion - the six fields that make a research task - for the
// prompt that asks for one.
//
// It is a rendering of its own because the task suggestion is a different
// card from the research proposal: the assistant's card has its own rules
// and the proposal contract does not judge it. A prompt that spells the
// shape out itself is a second copy of the contract, and two copies drift;
// the prompt asks for this rendering instead.
func ResearchProposalTaskShapeText() string {
	return renderResearchProposalShape(ResearchProposalTaskFields, researchProposalTaskQueriesRule)
}

// ResearchProposalShapeText renders the shape of a research proposal for a
// prompt. A prompt that spells the shape out itself is a second copy of the
// contract, and two copies drift; the prompt asks for this rendering
// instead.
//
// The rendering carries the rules that belong to the shape: a proposal needs
// at least two queries, a synonym is an extra query rather than a field of
// its own, a candidate source names a source of this turn, a reason says why
// the plan looks like this, and a gap says what is missing and what would
// fill it.
func ResearchProposalShapeText() string {
	return renderResearchProposalShape(ResearchProposalFields, researchProposalProposalQueriesRule)
}

// renderResearchProposalShape renders the given fields of the shape, with
// the rules that belong to them.
func renderResearchProposalShape(fields []string, queriesRule string) string {
	rendered := make([]string, 0, len(fields))
	for _, name := range fields {
		switch name {
		case researchProposalQueriesField:
			rendered = append(rendered, name+queriesRule)
		case researchProposalCandidateSourcesField:
			rendered = append(rendered, name+"（每条含 "+researchProposalCandidateSourceIDField+" 与 "+researchProposalCandidateSourceReasonField+"；"+researchProposalCandidateSourceIDField+" 只能取本轮上下文给出的来源ID）")
		case researchProposalReasonsField:
			rendered = append(rendered, name+"（整份提案为什么这么定：目标为什么是这个目标、范围为什么划到这里）")
		case researchProposalGapsField:
			rendered = append(rendered, name+"（每条含 "+researchProposalGapDescriptionField+"（缺什么）与 "+researchProposalGapExpectedEvidenceField+"（期望补什么）；只有「没有来源」这种空话不算）")
		default:
			rendered = append(rendered, name)
		}
	}
	return strings.Join(rendered, "、")
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
//
// A turn whose proposal the server refused says so in its failure code: the
// refused proposal never reached the turn, so the turn itself cannot be
// re-judged into "present", and it must not be mistaken for a turn that
// simply carried no proposal.
func ResearchProposalStateForTurn(turn AgentTurn) (string, error) {
	if turn.FailureCode == ResearchProposalFailureCode {
		return ResearchProposalStateInvalid, nil
	}
	if turn.Agent != AgentResearch {
		// The contract judges the research agent's proposal. The assistant's
		// editable task suggestion is a different card with its own rules,
		// and this contract does not touch it.
		return ResearchProposalStateAbsent, nil
	}
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

// researchProposalJudgement judges the proposal a turn's model answer carries
// and returns the suggestions the turn may store.
//
// The three states are facts about one answer, not verdicts about the project:
//
//   - present: the answer carries a proposal that decodes;
//   - absent:  the answer carries no proposal at all;
//   - invalid: the answer carries a proposal that does not decode.
//
// What is refused is the proposal, not the answer. An invalid proposal is
// taken out of the suggestions and the rest of the answer - the reply the
// user asked for - stays: a broken suggestion must not cost the user their
// answer.
//
// The pack is the context the model was given. It is what makes a candidate
// source checkable: a source the turn did not carry cannot be cited.
func researchProposalJudgement(agent string, structured json.RawMessage, pack AgentContextPackage) (string, json.RawMessage) {
	if agent != AgentResearch {
		// Only the research agent's answers are a proposal. The assistant's
		// editable task suggestion is a different card with its own rules,
		// so this contract does not judge it and does not touch it.
		return ResearchProposalStateAbsent, structured
	}
	raw, err := researchProposalFromSuggestions(structured)
	if err != nil {
		// The answer is not even a JSON object: none of it can be kept, and
		// none of it can be a proposal.
		return ResearchProposalStateInvalid, nil
	}
	if ResearchProposalAbsent(raw) {
		return ResearchProposalStateAbsent, structured
	}
	if _, err = DecodeResearchProposal(raw, pack); err != nil {
		cleaned, stripErr := researchProposalWithoutProposal(structured)
		if stripErr != nil {
			return ResearchProposalStateInvalid, nil
		}
		return ResearchProposalStateInvalid, cleaned
	}
	return ResearchProposalStatePresent, structured
}

// researchProposalWithoutProposal takes the refused proposal out of a model
// answer and leaves the rest of it alone.
//
// The answer is the model's own JSON: the reply the user asked for lives
// under its own key, and only the proposal is taken out. A suggestion the
// server cannot stand behind must not reach the interface - not as a
// proposal, and not as a leftover fragment either.
func researchProposalWithoutProposal(structured json.RawMessage) (json.RawMessage, error) {
	if len(structured) == 0 {
		return nil, ErrInvalidRequest
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(structured, &fields); err != nil {
		return nil, ErrInvalidRequest
	}
	if _, stated := fields[ResearchProposalKey]; !stated {
		return structured, nil
	}
	delete(fields, ResearchProposalKey)
	cleaned, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return cleaned, nil
}
