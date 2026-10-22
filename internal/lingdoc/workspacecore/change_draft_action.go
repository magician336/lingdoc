package workspacecore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// AgentChangeDraftSuggestion is candidate output only. The model receives no
// additional project fields and cannot choose old values, identity or consent.
type AgentChangeDraftSuggestion struct {
	ExpectedContextRevision int64             `json:"expected_context_revision"`
	Reason                  string            `json:"reason"`
	Fields                  map[string]string `json:"fields"`
	AffectedChapterIDs      []string          `json:"affected_chapter_ids"`
}

// AgentChangeDraftInput extends only the existing opaque ActionProposal input.
// The public ExtendedChangeSetInput and its HTTP route remain unchanged.
type AgentChangeDraftInput struct {
	ExtendedChangeSetInput
	FieldMetadata map[string]SpecFieldInput `json:"field_metadata"`
}

func strictAgentDraftJSON(raw json.RawMessage, value any) error {
	if len(raw) == 0 || len(raw) > 60000 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}

func DecodeAgentChangeDraftSuggestion(raw json.RawMessage, revision int64) (AgentChangeDraftSuggestion, error) {
	var input AgentChangeDraftSuggestion
	if err := strictAgentDraftJSON(raw, &input); err != nil {
		return input, err
	}
	if revision < 1 || input.ExpectedContextRevision != revision || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 || len(input.Fields) == 0 || len(input.Fields) > 100 {
		return input, ErrInvalidRequest
	}
	for key, value := range input.Fields {
		if strings.TrimSpace(key) == "" || len(key) > 80 || strings.TrimSpace(value) == "" || len(value) > 10000 {
			return input, ErrInvalidRequest
		}
	}
	return input, nil
}

func DecodeAgentChangeDraftInput(projectID, targetID string, revision int64, raw json.RawMessage) (AgentChangeDraftInput, error) {
	var input AgentChangeDraftInput
	if projectID == "" || targetID != projectID {
		return input, ErrInvalidRequest
	}
	if err := strictAgentDraftJSON(raw, &input); err != nil {
		return input, err
	}
	if input.ExpectedContextRevision != revision {
		return input, ErrVersionConflict
	}
	// This assistant slice edits research-condition values only. Existing formal-change
	// manual semantic-delta workflows remain available and unchanged.
	if len(input.SemanticDeltas) != 0 || len(input.Fields) == 0 || len(input.Fields) > 100 || len(input.FieldMetadata) != len(input.Fields) {
		return input, ErrInvalidRequest
	}
	if err := validateExtendedChangeInput(input.ExtendedChangeSetInput); err != nil {
		return input, err
	}
	selectedTurn, selectedVersion := "", ""
	for key := range input.Fields {
		metadata, ok := input.FieldMetadata[key]
		if !ok || metadata.Provenance == nil || (metadata.Origin != "ai_generated" && metadata.Origin != "ai_assisted_human") || metadata.Status != "pending_confirmation" {
			return input, ErrInvalidRequest
		}
		record := metadata.Provenance
		if record.SourceType != "agent_turn" || record.SourceID == "" || record.SourceVersion == "" || record.Purpose != "project_spec" || record.Locator != "reply" || record.Accessible || record.NeedsReview || record.CreatedBy != "" || !record.CreatedAt.IsZero() || record.AuthorizationScope != "" {
			return input, ErrInvalidRequest
		}
		if selectedTurn == "" {
			selectedTurn, selectedVersion = record.SourceID, record.SourceVersion
		}
		if record.SourceID != selectedTurn || record.SourceVersion != selectedVersion {
			return input, ErrInvalidRequest
		}
	}
	return input, nil
}

func agentChangeDraftSources(tx Transaction, actor Actor, p Project, input AgentChangeDraftInput) ([]string, error) {
	return agentChangeDraftSourcesAtBasis(tx, actor, p, input, true)
}

func agentChangeDraftSourcesAtBasis(tx Transaction, actor Actor, p Project, input AgentChangeDraftInput, requireCurrentBasis bool) ([]string, error) {
	ids := []string{}
	for _, metadata := range input.FieldMetadata {
		record := metadata.Provenance
		turn, stored, err := loadStoredObject[AgentTurn](tx, p.ID, AgentTurnKind, record.SourceID)
		if err != nil {
			return nil, err
		}
		session, sessionRow, err := loadStoredObject[AgentSession](tx, p.ID, AgentSessionKind, turn.SessionID)
		if err != nil {
			return nil, err
		}
		if turn.ID != record.SourceID || turn.ProjectID != p.ID || turn.Version != stored.Version || !provenanceVersionMatches(record.SourceVersion, turn.Version) || turn.CreatedBy != actor.UserID || turn.Status != "completed" || turn.Agent != "task_assistant" || session.ID != turn.SessionID || session.Agent != "task_assistant" || session.ProjectID != p.ID || session.CreatedBy != actor.UserID || session.Version != sessionRow.Version || requireCurrentBasis && (turn.Context.ProjectVersion != p.ProjectVersion || turn.Context.SpecRevision != p.SpecRevision || turn.Context.ContextRevision != p.CurrentContextRevision) {
			return nil, ErrVersionConflict
		}
		var output struct {
			ChangeDraft json.RawMessage `json:"change_draft"`
		}
		if err := json.Unmarshal(turn.Suggestions, &output); err != nil {
			return nil, ErrInvalidRequest
		}
		suggestion, err := DecodeAgentChangeDraftSuggestion(output.ChangeDraft, turn.Context.ContextRevision)
		if err != nil {
			return nil, err
		}
		for key, field := range input.Fields {
			modelValue, exists := suggestion.Fields[key]
			if !exists {
				return nil, ErrInvalidRequest
			}
			metadata := input.FieldMetadata[key]
			metadata.Origin = "ai_assisted_human"
			if field.NewValue == modelValue {
				metadata.Origin = "ai_generated"
			}
			input.FieldMetadata[key] = metadata
		}
		for _, source := range turn.Context.SourceRefs {
			ids = append(ids, source.SourceID)
		}
	}
	return sortedUniqueIDs(ids), nil
}

func (s *Service) validateAgentChangeDraftProvenance(ctx context.Context, actor Actor, projectID string, basis VersionBasis, input AgentChangeDraftInput) (context.Context, error) {
	requests := []provenanceRequest{}
	err := s.readWithCapability(ctx, actor, projectID, "write", func(tx Transaction, p Project) error {
		if !versionBasisCurrent(p, basis) {
			return ErrVersionConflict
		}
		if _, err := agentChangeDraftSources(tx, actor, p, input); err != nil {
			return err
		}
		requests = provenanceFieldRequests(p)
		for key, field := range input.Fields {
			if _, present := p.Spec[key]; !present || p.Spec[key] != field.OldValue {
				return ErrVersionConflict
			}
			metadata := input.FieldMetadata[key]
			requests = append(requests, provenanceRequest{Key: key, Value: field.NewValue, Record: metadata.Provenance, Origin: metadata.Origin, Creator: actor.UserID, Fresh: true})
		}
		return nil
	})
	if err != nil {
		return ctx, err
	}
	prepared, proof, err := s.prepareProvenance(ctx, actor, projectID, requests, false)
	if err != nil {
		return ctx, err
	}
	for id := range proof.Pins {
		if !proof.States[id] {
			return ctx, ErrSourceUnavailable
		}
	}
	for key, metadata := range input.FieldMetadata {
		pin, ok := proof.Requests[provenanceRequestKey(metadata.Provenance, input.Fields[key].NewValue)]
		if !ok || pin.Kind != "AgentTurn" || !proof.States[pin.ID] {
			return ctx, ErrSourceUnavailable
		}
	}
	return prepared, nil
}

type agentChangeDraftContextKey struct{}
type agentChangeDraftProof struct {
	ProjectID, BodyHash string
	Input               AgentChangeDraftInput
}

func (s *Service) PrepareAgentChangeDraft(ctx context.Context, actor Actor, proposal ActionProposal, input AgentChangeDraftInput) (context.Context, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return ctx, err
	}
	if proposal.CreatedBy != actor.UserID || proposal.Status != "executing" || proposal.TargetID != proposal.ProjectID {
		return ctx, ErrInvalidState
	}
	prepared, err := s.validateAgentChangeDraftProvenance(ctx, actor, proposal.ProjectID, proposal.Basis, input)
	if err != nil {
		return ctx, err
	}
	// The assistant's two explanation groups are re-derived from the persisted
	// turn, never taken from the client. A turn whose explanation cannot cite a
	// basis is refused here, so the draft write fails with zero writes and the
	// unsupported object enters neither group.
	affected, err := s.assistantAffectedForDraft(ctx, actor, proposal.ProjectID, input)
	if err != nil {
		return ctx, err
	}
	proof := prepared.Value(provenanceProofKey{}).(*provenanceProof)
	proof.Operation = string(operationCreateChangeDraft)
	proof.OperationBody = hashImpactValue(input.ExtendedChangeSetInput)
	return withAssistantAffected(context.WithValue(prepared, agentChangeDraftContextKey{}, agentChangeDraftProof{ProjectID: proposal.ProjectID, BodyHash: proof.OperationBody, Input: input}), affected), nil
}

func storeAgentChangeDraftFields(ctx context.Context, tx Transaction, p Project, change ChangeSet, input ExtendedChangeSetInput) error {
	request, present := ctx.Value(agentChangeDraftContextKey{}).(agentChangeDraftProof)
	if !present {
		return nil
	}
	if request.ProjectID != p.ID || request.BodyHash != hashImpactValue(input) {
		return ErrSourceUnavailable
	}
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		return ErrSourceUnavailable
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	var history changeProvenance
	version, err := store.GetProjectRecord(p.ID, "g1_change_provenance", change.ID, &history)
	if err != nil {
		return err
	}
	history.ProposedFields = map[string]SpecProvenanceRef{}
	history.ProposedOrigins = map[string]string{}
	for key, metadata := range request.Input.FieldMetadata {
		value := input.Fields[key].NewValue
		pin, ok := proof.Requests[provenanceRequestKey(metadata.Provenance, value)]
		if !ok || !proof.States[pin.ID] {
			return ErrSourceUnavailable
		}
		history.ProposedFields[key] = SpecProvenanceRef{FieldKey: key, ValueHash: hashImpactValue(value), PinID: pin.ID}
		history.ProposedOrigins[key] = metadata.Origin
	}
	return store.PutProjectRecord(p.ID, "g1_change_provenance", change.ID, version, history)
}

func (s *Service) prepareProposedChangeFields(ctx context.Context, actor Actor, change ChangeSet, refs map[string]SpecProvenanceRef) (context.Context, error) {
	if len(refs) == 0 {
		return ctx, nil
	}
	values := map[string]string{}
	for _, field := range change.Fields {
		values[field.Key] = field.NewValue
	}
	requests := []provenanceRequest{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, change.ProjectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrSourceUnavailable
		}
		for key, ref := range refs {
			value, present := values[key]
			if !present || hashImpactValue(value) != ref.ValueHash || ref.PinID == "" {
				return ErrSourceUnavailable
			}
			var pin provenancePin
			if _, err := store.GetProjectRecord(change.ProjectID, "g1_provenance", ref.PinID, &pin); err != nil {
				return err
			}
			if pin.ProjectID != change.ProjectID || pin.ValueHash != ref.ValueHash || pin.Kind != "AgentTurn" {
				return ErrSourceUnavailable
			}
			record := pin.Record
			requests = append(requests, provenanceRequest{Key: key, Value: value, Record: &record, Creator: record.CreatedBy})
		}
		return nil
	})
	if err != nil {
		return ctx, err
	}
	_, proposed, err := s.prepareProvenance(ctx, actor, change.ProjectID, requests, false)
	if err != nil {
		return ctx, err
	}
	base, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || provenanceProjectHash(base.Project) != provenanceProjectHash(proposed.Project) {
		return ctx, ErrVersionConflict
	}
	for id, pin := range proposed.Pins {
		if !proposed.States[id] {
			return ctx, ErrSourceUnavailable
		}
		base.Pins[id], base.States[id], base.NativeBefore[id] = pin, proposed.States[id], proposed.NativeBefore[id]
	}
	return ctx, nil
}

func (s *Service) assertAgentChangeDraftProvenance(ctx context.Context, actor Actor, projectID string, input AgentChangeDraftInput) error {
	requests := []provenanceRequest{}
	for key, metadata := range input.FieldMetadata {
		requests = append(requests, provenanceRequest{Key: key, Value: input.Fields[key].NewValue, Record: metadata.Provenance, Creator: actor.UserID, Fresh: true})
	}
	_, proof, err := s.prepareProvenance(ctx, actor, projectID, requests, false)
	if err != nil {
		return err
	}
	for key, metadata := range input.FieldMetadata {
		pin, ok := proof.Requests[provenanceRequestKey(metadata.Provenance, input.Fields[key].NewValue)]
		if !ok || pin.Kind != "AgentTurn" || !proof.States[pin.ID] {
			return ErrSourceUnavailable
		}
	}
	return nil
}
