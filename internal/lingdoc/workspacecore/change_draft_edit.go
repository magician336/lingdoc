package workspacecore

import (
	"context"
	"strings"
)

// Missing legacy private origin is conservatively AI assisted, never human.
func agentProposedOrigin(value string) string {
	if value == "ai_generated" {
		return value
	}
	return "ai_assisted_human"
}

type agentChangeDraftEditContextKey struct{}
type agentChangeDraftEditProof struct {
	ProjectID, ChangeID, OldChangeHash, HistoryHash, BodyHash string
	HistoryVersion                                            int64
	Fields                                                    map[string]SpecProvenanceRef
	Origins                                                   map[string]string
}

// Called by the existing prepareProvenanceOperation before its mutation transaction.
// Native descriptors are read in short snapshots; all external source checks
// remain in existing G1 preparation, outside the eventual edit transaction.
func (s *Service) prepareAgentChangeDraftEdit(ctx context.Context, actor Actor, projectID, target string, input ExtendedChangeSetInput) (context.Context, bool, error) {
	changeID := strings.TrimPrefix(target, projectID+"/")
	if changeID == target || changeID == "" || strings.Contains(changeID, "/") {
		return ctx, false, ErrInvalidRequest
	}
	var p Project
	var old ChangeSet
	var history changeProvenance
	var historyVersion int64
	handled := false
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		p, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, ""))
		if err != nil {
			return err
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		old, err = changes.ChangeSet(projectID, changeID)
		if err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return nil
		} // Existing non-G1 manual repository port.
		historyVersion, err = store.GetProjectRecord(projectID, "g1_change_provenance", changeID, &history)
		if err != nil {
			return err
		}
		if len(history.ProposedFields) == 0 {
			return nil
		}
		handled = true
		// Preparation also runs before idempotent replay lookup. State,
		// revision and input old-value checks belong to the cache-miss
		// mutation callback; current access and source proofs still apply.
		// The creator may edit their own draft, and the Owner may edit any.
		// The Owner's tenant identity lapsing removes the Owner's own
		// alternative and nothing else, so this use is never frozen.
		if old.CreatedBy != actor.UserID && !OwnerMayAct(p, actor) {
			return ErrNotFound
		}
		return nil
	})
	if err != nil || !handled {
		return ctx, handled, err
	}
	// Recheck every old proposed source even if this edit removes its field.
	prepared, err := s.prepareChangeProvenance(ctx, actor, old, map[string]bool{})
	if err != nil {
		return ctx, true, err
	}
	previous, ok := prepared.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || provenanceProjectHash(previous.Project) != provenanceProjectHash(p) {
		return ctx, true, ErrVersionConflict
	}
	requests := provenanceFieldRequests(p)
	fields := map[string]SpecProvenanceRef{}
	origins := map[string]string{}
	changedRequests := map[string]string{}
	for key, ref := range history.ProposedFields {
		field, retained := input.Fields[key]
		if !retained {
			continue
		}
		pin, exists := previous.Pins[ref.PinID]
		if !exists || !previous.States[ref.PinID] || pin.Kind != "AgentTurn" || pin.ProjectID != projectID || pin.ValueHash != ref.ValueHash {
			return ctx, true, ErrSourceUnavailable
		}
		record := pin.Record
		if hashImpactValue(field.NewValue) == ref.ValueHash {
			fields[key] = ref
			origins[key] = agentProposedOrigin(history.ProposedOrigins[key])
			requests = append(requests, provenanceRequest{Key: key, Value: field.NewValue, Record: &record, Creator: record.CreatedBy})
			continue
		}
		// A manual edit keeps its exact AI turn association, but gets a new
		// value-bound server pin and AI-assisted origin. No client metadata.
		record.Accessible, record.NeedsReview = false, false
		requests = append(requests, provenanceRequest{Key: key, Value: field.NewValue, Record: &record, Origin: "ai_assisted_human", Creator: actor.UserID, Fresh: true})
		changedRequests[key] = provenanceRequestKey(&record, field.NewValue)
		origins[key] = "ai_assisted_human"
	}
	prepared, proof, err := s.prepareProvenance(prepared, actor, projectID, requests, false)
	if err != nil {
		return ctx, true, err
	}
	if provenanceProjectHash(proof.Project) != provenanceProjectHash(previous.Project) {
		return ctx, true, ErrVersionConflict
	}
	for id, pin := range previous.Pins {
		proof.Pins[id], proof.States[id], proof.NativeBefore[id] = pin, previous.States[id], previous.NativeBefore[id]
	}
	for id := range proof.Pins {
		if !proof.States[id] {
			return ctx, true, ErrSourceUnavailable
		}
	}
	for key, requestKey := range changedRequests {
		pin, exists := proof.Requests[requestKey]
		if !exists || pin.Kind != "AgentTurn" || !proof.States[pin.ID] {
			return ctx, true, ErrSourceUnavailable
		}
		fields[key] = SpecProvenanceRef{FieldKey: key, ValueHash: hashImpactValue(input.Fields[key].NewValue), PinID: pin.ID}
	}
	proof.Operation = string(operationEditChangeDraft)
	proof.OperationBody = hashImpactValue(input)
	edit := agentChangeDraftEditProof{ProjectID: projectID, ChangeID: changeID, OldChangeHash: hashImpactValue(old), HistoryVersion: historyVersion, HistoryHash: hashImpactValue(history), BodyHash: proof.OperationBody, Fields: fields, Origins: origins}
	return context.WithValue(prepared, agentChangeDraftEditContextKey{}, edit), true, nil
}

// Only native descriptor checks and writes occur here. Existing runOperation
// already stored FreshPins in this same transaction and repeats G1 currentness.
func storeAgentChangeDraftEditFields(ctx context.Context, tx Transaction, p Project, old, next ChangeSet, input ExtendedChangeSetInput) error {
	edit, prepared := ctx.Value(agentChangeDraftEditContextKey{}).(agentChangeDraftEditProof)
	store, supports := tx.(projectRecordTransaction)
	if !supports {
		if prepared {
			return ErrInvalidState
		}
		return nil
	}
	var history changeProvenance
	version, err := store.GetProjectRecord(p.ID, "g1_change_provenance", old.ID, &history)
	if err != nil {
		return err
	}
	if !prepared {
		if len(history.ProposedFields) != 0 {
			return ErrSourceUnavailable
		}
		return nil
	}
	if edit.ProjectID != p.ID || edit.ChangeID != old.ID || edit.BodyHash != hashImpactValue(input) || edit.OldChangeHash != hashImpactValue(old) || edit.HistoryVersion != version || edit.HistoryHash != hashImpactValue(history) {
		return ErrVersionConflict
	}
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		return ErrSourceUnavailable
	}
	values := map[string]string{}
	for _, field := range next.Fields {
		values[field.Key] = field.NewValue
	}
	for key, ref := range edit.Fields {
		value, exists := values[key]
		pin, pinned := proof.Pins[ref.PinID]
		if !exists || hashImpactValue(value) != ref.ValueHash || !pinned || !proof.States[pin.ID] || pin.Kind != "AgentTurn" || pin.ProjectID != p.ID || pin.ValueHash != ref.ValueHash {
			return ErrSourceUnavailable
		}
	}
	// Reason-only edits reuse pins and do not touch the private history row.
	if hashImpactValue(history.ProposedFields) == hashImpactValue(edit.Fields) && hashImpactValue(history.ProposedOrigins) == hashImpactValue(edit.Origins) {
		return nil
	}
	history.ProposedFields, history.ProposedOrigins = edit.Fields, edit.Origins
	return store.PutProjectRecord(p.ID, "g1_change_provenance", old.ID, version, history)
}
