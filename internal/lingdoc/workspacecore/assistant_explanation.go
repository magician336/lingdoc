package workspacecore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// AC-G3-14: the G3 impact assistant explains what a proposed change affects in
// two groups - "确定受影响" (definite) and "可能受影响" (possible) - and shows
// the basis of every explanation.
//
// This is the assistant's OWN explanation. It is not the impact resolver's
// verdict: the resolver writes `impacts[].certainty` on ImpactAssessment, and
// the two sides deliberately use different names so neither can be mistaken
// for the other. The resolver's certainty classification is not touched here.
//
// Every entry must be traceable: an object reference, a reason and a basis.
// An entry that cannot cite a basis is refused, so an unsupported object never
// enters either group and the draft write fails with zero writes.
//
// Issue #315: a model that proposes a change draft *without* the two groups is
// not refused any more. The missing groups are a fact about the assistant's
// answer ("助手未给两组说明"), not a reason to throw a reviewable draft away:
// the draft is written and carries no affected_explanation, so every reader
// downstream sees the gap instead of seeing nothing at all.

// AssistantAffectedObject is the object one explanation entry talks about.
// The three fields are the same versioned reference the impact graph uses, so
// an explanation points at exactly one version of one object.
type AssistantAffectedObject struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

// AssistantAffectedBasis cites what the assistant relied on. Spec §2.4 of the
// G3 plan requires the assistant to reference the ChangeSet, DependencyEdge,
// RuleSet, ImpactTask or version information it used; an assistant that cannot
// name a basis has nothing to show.
type AssistantAffectedBasis struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Detail     string `json:"detail,omitempty"`
}

// AssistantAffectedEntry is one explained object inside one group.
type AssistantAffectedEntry struct {
	Object AssistantAffectedObject `json:"object"`
	Reason string                  `json:"reason"`
	Basis  AssistantAffectedBasis  `json:"basis"`
}

// AssistantAffectedExplanation is the assistant's two-group explanation.
//
// The field names are the assistant's, on purpose: they never reuse
// `certainty`, which belongs to the impact resolver's own result. A reader
// can therefore assert the two sides separately.
type AssistantAffectedExplanation struct {
	DefiniteAffected []AssistantAffectedEntry `json:"definite_affected"`
	PossibleAffected []AssistantAffectedEntry `json:"possible_affected"`
}

// AssistantAffectedBasisTypes lists the records an explanation may cite.
// A basis outside this list is not a traceable basis.
var AssistantAffectedBasisTypes = []string{"change_set", "dependency_edge", "ruleset", "impact_task", "object_version"}

// AssistantAffectedExplanationKey is the key the assistant writes the two
// groups under, both in its structured output and on the draft it produces.
const AssistantAffectedExplanationKey = "affected_explanation"

// AssistantAffectedExplanationAbsent reports whether the raw payload states no
// two-group explanation at all: the key is missing, or its value is JSON null.
//
// A payload that DOES state an explanation is never "absent": it is either
// valid or invalid, and an invalid one (an entry that cannot cite a basis) is
// refused. Absent is only about the assistant having said nothing.
func AssistantAffectedExplanationAbsent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// DecodeAssistantAffectedExplanation decodes and validates the assistant's
// two-group explanation. A payload that carries an entry without a usable
// basis is refused as a whole: a half-cited explanation would let an
// unsupported object into a group.
func DecodeAssistantAffectedExplanation(raw json.RawMessage) (AssistantAffectedExplanation, error) {
	explanation := AssistantAffectedExplanation{}
	if len(raw) == 0 || len(raw) > 60000 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return explanation, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&explanation); err != nil {
		return explanation, ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return explanation, ErrInvalidRequest
	}
	if explanation.DefiniteAffected == nil || explanation.PossibleAffected == nil {
		return explanation, ErrInvalidRequest
	}
	if len(explanation.DefiniteAffected) > 100 || len(explanation.PossibleAffected) > 100 {
		return explanation, ErrInvalidRequest
	}
	for _, entry := range append(append([]AssistantAffectedEntry{}, explanation.DefiniteAffected...), explanation.PossibleAffected...) {
		if err := validateAssistantAffectedEntry(entry); err != nil {
			return AssistantAffectedExplanation{}, err
		}
	}
	return explanation, nil
}

func validateAssistantAffectedEntry(entry AssistantAffectedEntry) error {
	if strings.TrimSpace(entry.Object.Kind) == "" || strings.TrimSpace(entry.Object.ID) == "" || strings.TrimSpace(entry.Object.Version) == "" {
		return ErrInvalidRequest
	}
	if len(entry.Object.Kind) > 64 || len(entry.Object.ID) > 128 || len(entry.Object.Version) > 128 {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(entry.Reason) == "" || len(entry.Reason) > 2000 {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(entry.Basis.SourceType) == "" || strings.TrimSpace(entry.Basis.SourceID) == "" {
		return ErrInvalidRequest
	}
	if len(entry.Basis.SourceID) > 128 || len(entry.Basis.Detail) > 2000 {
		return ErrInvalidRequest
	}
	for _, kind := range AssistantAffectedBasisTypes {
		if entry.Basis.SourceType == kind {
			return nil
		}
	}
	return ErrInvalidRequest
}

// AssistantAffectedFromTurn re-derives the assistant's two groups from a
// persisted turn. The stored turn is the only source: a draft that carries an
// explanation is verified against what the assistant actually proposed, never
// against a value the client supplied.
//
// A turn without an explanation yields no groups at all - the server never
// invents an affected object the assistant did not propose. A turn whose
// explanation cannot cite a basis is refused.
func AssistantAffectedFromTurn(turn AgentTurn) (*AssistantAffectedExplanation, error) {
	if len(turn.Suggestions) == 0 {
		return nil, nil
	}
	var output struct {
		AffectedExplanation json.RawMessage `json:"affected_explanation"`
	}
	if err := json.Unmarshal(turn.Suggestions, &output); err != nil {
		return nil, ErrInvalidRequest
	}
	if len(output.AffectedExplanation) == 0 {
		return nil, nil
	}
	explanation, err := DecodeAssistantAffectedExplanation(output.AffectedExplanation)
	if err != nil {
		return nil, err
	}
	return &explanation, nil
}

// The assistant's answer about what a change affects has three states. They
// are facts about the assistant's answer, not verdicts about the project.
//
//   - present: the assistant gave the two groups;
//   - missing: the assistant proposed a change but gave no two groups
//     ("助手未给两组说明");
//   - not_applicable: the turn proposed no change at all, so no group was due.
//
// A missing explanation is a fact that is recorded, not a reason to throw the
// draft away: a draft without the two groups is still reviewable by a human,
// and the missing groups are exactly what the reader has to be told.
const (
	AssistantAffectedStatePresent       = "present"
	AssistantAffectedStateMissing       = "missing"
	AssistantAffectedStateNotApplicable = "not_applicable"
)

// AssistantAffectedStateForTurn judges, from the persisted turn alone, whether
// the assistant gave the two explanation groups.
func AssistantAffectedStateForTurn(turn AgentTurn) (string, error) {
	state, _, err := assistantAffectedStateFromTurn(turn)
	return state, err
}

// assistantAffectedStateFromTurn is the internal form: the state and the
// decoded groups, if any.
func assistantAffectedStateFromTurn(turn AgentTurn) (string, *AssistantAffectedExplanation, error) {
	explanation, err := AssistantAffectedFromTurn(turn)
	if err != nil {
		return "", nil, err
	}
	if explanation != nil {
		return AssistantAffectedStatePresent, explanation, nil
	}
	if assistantTurnProposedChangeDraft(turn) {
		return AssistantAffectedStateMissing, nil, nil
	}
	return AssistantAffectedStateNotApplicable, nil, nil
}

// assistantAffectedContextKey carries the server-derived groups from the
// assistant turn into the draft write. It is deliberately not part of any
// client-supplied input: a client cannot state which objects are affected.
type assistantAffectedContextKey struct{}

// withAssistantAffected stores the derived groups on the operation context.
func withAssistantAffected(ctx context.Context, explanation *AssistantAffectedExplanation) context.Context {
	return context.WithValue(ctx, assistantAffectedContextKey{}, explanation)
}

// assistantAffectedFromContext reads the derived groups back.
func assistantAffectedFromContext(ctx context.Context) *AssistantAffectedExplanation {
	explanation, _ := ctx.Value(assistantAffectedContextKey{}).(*AssistantAffectedExplanation)
	return explanation
}

// assistantAffectedForDraft re-derives the two groups for an agent change
// draft from the persisted turn the draft's field provenance points at.
//
// The turn is the truth source, exactly like the draft field values are: the
// client states which values it proposes, and the server checks them against
// what the assistant stored. Here the client does not even state the groups -
// the server reads them back from the turn it already validated.
//
// A turn that proposed a change draft but carries no two-group explanation is
// not refused: the draft is written and carries no explanation, so the reader
// is told exactly that ("助手未给两组说明"). A missing annotation is a fact
// about the assistant's answer, not a reason to throw a reviewable draft away.
// A stored turn whose explanation cannot cite a basis is refused too, so the
// draft write fails with zero writes.
func (s *Service) assistantAffectedForDraft(ctx context.Context, actor Actor, projectID string, input AgentChangeDraftInput) (*AssistantAffectedExplanation, error) {
	var derived *AssistantAffectedExplanation
	err := s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, p Project) error {
		turnID := ""
		for _, metadata := range input.FieldMetadata {
			if metadata.Provenance == nil || metadata.Provenance.SourceType != "agent_turn" {
				return ErrInvalidRequest
			}
			if turnID == "" {
				turnID = metadata.Provenance.SourceID
				continue
			}
			if turnID != metadata.Provenance.SourceID {
				return ErrInvalidRequest
			}
		}
		if turnID == "" {
			return nil
		}
		turn, stored, err := loadStoredObject[AgentTurn](tx, p.ID, AgentTurnKind, turnID)
		if err != nil {
			return err
		}
		if turn.ProjectID != p.ID || turn.CreatedBy != actor.UserID || turn.Status != "completed" || turn.Version != stored.Version {
			return ErrVersionConflict
		}
		// The turn must still describe the current baseline, or its
		// explanation explains a change that is no longer being proposed.
		if turn.Context.ProjectVersion != p.ProjectVersion || turn.Context.SpecRevision != p.SpecRevision || turn.Context.ContextRevision != p.CurrentContextRevision {
			return ErrVersionConflict
		}
		state, explanation, err := assistantAffectedStateFromTurn(turn)
		if err != nil {
			return err
		}
		switch state {
		case AssistantAffectedStatePresent:
			// The assistant gave the two groups: they travel with the draft.
			derived = explanation
		case AssistantAffectedStateMissing:
			// The assistant proposed a change but did not say which objects it
			// affects. The draft is written anyway and carries no
			// affected_explanation, so the acceptance bench reads that back
			// as "助手未给两组说明" instead of crashing on a missing draft.
			derived = nil
		default:
			derived = nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return derived, nil
}

// assistantTurnProposedChangeDraft reports whether a turn carries a change
// draft candidate, which is what makes the two-group explanation mandatory.
func assistantTurnProposedChangeDraft(turn AgentTurn) bool {
	var output struct {
		ChangeDraft json.RawMessage `json:"change_draft"`
	}
	if err := json.Unmarshal(turn.Suggestions, &output); err != nil {
		return false
	}
	return len(output.ChangeDraft) > 0
}
