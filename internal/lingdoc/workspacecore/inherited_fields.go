package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The references are created from server-owned history only. Client-origin and
// accessibility assertions cannot erase a restored source binding.
func (s *Service) restoreInheritedFields(ctx context.Context, tx Transaction, actor Actor, change ChangeSet, next *Project) error {
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	var history changeProvenance
	_, err := store.GetProjectRecord(change.ProjectID, "g1_change_provenance", change.ID, &history)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(history.InheritedFields) == 0 && len(history.ProposedFields) == 0 {
		return nil
	}
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || proof.ActorID != actor.UserID || proof.TenantID != actor.TenantID || proof.Identity != candidateIdentityHash(ctx) {
		return fmt.Errorf("%w: inverse provenance context is missing or mismatched", ErrSourceUnavailable)
	}
	fields := map[string]SpecProvenanceRef{}
	maps.Copy(fields, history.InheritedFields)
	maps.Copy(fields, history.ProposedFields)
	for key, ref := range fields {
		value, present := next.Spec[key]
		// A subsequently edited inverse is new human input, not a restoration.
		if !present || hashImpactValue(value) != ref.ValueHash || ref.PinID == "" {
			continue
		}
		pin, present := proof.Pins[ref.PinID]
		if !present {
			return fmt.Errorf("%w: inverse source pin missing", ErrSourceUnavailable)
		}
		if !proof.States[pin.ID] {
			return fmt.Errorf("%w: inverse source authorization expired", ErrSourceUnavailable)
		}
		if pin.ProjectID != change.ProjectID || pin.ValueHash != ref.ValueHash {
			return fmt.Errorf("%w: inverse source value identity changed", ErrSourceUnavailable)
		}
		if !s.provenancePinCurrent(ctx, tx, actor, proof.Project, pin, false) {
			return fmt.Errorf("%w: inverse source native descriptor changed", ErrSourceUnavailable)
		}
		field := next.SpecFields[key]
		record := pin.Record
		record.Accessible = true
		record.NeedsReview = false
		field.Provenance = &record
		field.Origin = "human"
		if pin.Kind == "AgentTurn" {
			field.Origin = "ai_assisted_human"
			if slices.Contains(pin.OriginalAIHashes, hashImpactValue(strings.TrimSpace(value))) {
				field.Origin = "ai_generated"
			}
		}
		if proposed, exists := history.ProposedFields[key]; exists && proposed == ref {
			// Per-field server origin is exact; a different suggestion string or
			// whitespace edit cannot become generated merely by matching elsewhere.
			field.Origin = agentProposedOrigin(history.ProposedOrigins[key])
		}
		field.Status = "pending_confirmation"
		next.SpecFields[key] = field
	}
	return nil
}

func storeActionProvenance(tx Transaction, p Project, proposal ActionProposal) error {
	snapshot, err := persistProvenanceSnapshot(tx, p)
	if err != nil {
		return err
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	return store.PutProjectRecord(p.ID, "g1_action_provenance", proposal.ID, 0, changeProvenance{Snapshot: snapshot, Spec: maps.Clone(p.Spec)})
}

func (s *Service) assertInheritedActionProvenance(ctx context.Context, actor Actor, proposal ActionProposal) error {
	var history changeProvenance
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, proposal.ProjectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrSourceUnavailable
		}
		_, err := store.GetProjectRecord(proposal.ProjectID, "g1_action_provenance", proposal.ID, &history)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return ErrSourceUnavailable
	}
	if err != nil {
		return err
	}
	if history.Snapshot.SpecRevision != proposal.Basis.SpecRevision || history.Snapshot.ContextRevision != proposal.Basis.ContextRevision {
		return ErrSourceUnavailable
	}
	if err := s.AssertFrozenProjectProvenance(ctx, actor, proposal.ProjectID, history.Spec, history.Snapshot, false); err != nil {
		return err
	}
	if proposal.Action == "create_change_set" {
		input, err := DecodeAgentChangeDraftInput(proposal.ProjectID, proposal.TargetID, proposal.Basis.ContextRevision, proposal.Input)
		if err != nil {
			return err
		}
		return s.assertAgentChangeDraftProvenance(ctx, actor, proposal.ProjectID, input)
	}
	return nil
}

func (s *Service) inheritedActionProjection(ctx context.Context, actor Actor, proposal ActionProposal) (ActionProposal, error) {
	err := s.assertInheritedActionProvenance(ctx, actor, proposal)
	if err != nil {
		if !errors.Is(err, ErrSourceUnavailable) {
			return ActionProposal{}, err
		}
		proposal.Input = nil
		proposal.Result = nil
		proposal.ExpectedEffect = ""
		proposal.Status = "restricted"
		return proposal, nil
	}
	input, err := s.operationProvenanceProjection(ctx, actor, "actionInput", proposal.ProjectID, proposal.Input)
	if err != nil {
		return ActionProposal{}, err
	}
	result, err := s.operationProvenanceProjection(ctx, actor, "actionResult", proposal.ProjectID, proposal.Result)
	if err != nil {
		return ActionProposal{}, err
	}
	// Keep the stored action intact. An output redaction also hides its input and
	// explanatory text because those may contain the same historical old value.
	var beforeInput, afterInput, beforeResult, afterResult any
	_ = json.Unmarshal(proposal.Input, &beforeInput)
	_ = json.Unmarshal(input, &afterInput)
	_ = json.Unmarshal(proposal.Result, &beforeResult)
	_ = json.Unmarshal(result, &afterResult)
	if hashImpactValue(beforeInput) != hashImpactValue(afterInput) || hashImpactValue(beforeResult) != hashImpactValue(afterResult) {
		proposal.Input = nil
		proposal.Result = nil
		proposal.ExpectedEffect = ""
		proposal.Status = "restricted"
	} else {
		proposal.Input = input
		proposal.Result = result
	}
	return proposal, nil
}
