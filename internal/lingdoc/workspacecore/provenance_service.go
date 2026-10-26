package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// prepareProvenance releases the short authorized snapshot before asking the existing
// source/gateway adapter. No external port is ever called from its final tx.
func (s *Service) prepareProvenance(ctx context.Context, actor Actor, pid string, requests []provenanceRequest, forModel bool) (context.Context, *provenanceProof, error) {
	if !provenanceIdentityMatches(ctx, actor) {
		return ctx, nil, ErrNotFound
	}
	proof := &provenanceProof{ActorID: actor.UserID, TenantID: actor.TenantID, Identity: candidateIdentityHash(ctx), ForModel: forModel, Pins: map[string]provenancePin{}, States: map[string]bool{}, Requests: map[string]provenancePin{}, FreshPins: map[string]provenancePin{}, NativeBefore: map[string]string{}}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		proof.Project = p
		if requests == nil {
			requests = provenanceFieldRequests(p)
		}
		for _, request := range requests {
			if request.Record == nil && (request.Origin == "" || request.Origin == "human") {
				continue
			}
			var pin provenancePin
			if request.Fresh {
				pin, err = s.resolveProvenancePin(ctx, tx, actor, p, request)
			} else {
				var found bool
				pin, found, err = loadProvenancePin(tx, pid, request.Value, request.Record)
				if err == nil && !found {
					pin = unknownProvenancePin(p, Actor{UserID: request.Creator}, request)
					if request.Record != nil {
						pin.Record = *request.Record
						pin.Record.CreatedBy = request.Creator
						pin.Record.Accessible = false
						pin.Record.NeedsReview = true
					}
				}
			}
			if err != nil {
				return err
			}
			proof.Pins[pin.ID] = pin
			proof.Requests[provenanceRequestKey(request.Record, request.Value)] = pin
			currentDigest, digestErr := nativeSourceDigest(tx, pid, pin.SourceIDs)
			if digestErr != nil {
				currentDigest = "unavailable"
			}
			proof.NativeBefore[pin.ID] = currentDigest
			if request.Fresh {
				proof.FreshPins[pin.ID] = pin
			}
		}
		return nil
	})
	if err != nil {
		return ctx, nil, err
	}
	for id, pin := range proof.Pins {
		allowed := pin.Verified
		for _, sourceID := range pin.SourceIDs {
			if s.agentRuntime.Sources == nil {
				allowed = false
				break
			}
			source, err := s.agentRuntime.Sources.ReadResearchSource(context.WithValue(ctx, provenanceSourceIOKey{}, true), actor, pid, sourceID)
			if err != nil || source.SourceID != sourceID || source.Status != string(evidence.SourceAvailable) || source.ProcessingState != "ready" || source.QuotedTextHash == "" || source.QuotedTextHash != sha256Hex(source.QuotedText) || forModel && !source.ModelUseAllowed {
				allowed = false
				break
			}
			matched := len(pin.Sources) == 0
			for _, ref := range pin.Sources {
				if ref.ID == sourceID && ref.AssetID == source.AssetID && ref.Revision == source.AssetRevision && ref.Hash == source.QuotedTextHash {
					matched = true
				}
			}
			if !matched {
				allowed = false
				break
			}
		}
		if allowed && len(pin.SourceIDs) > 0 && s.checkSources(context.WithValue(ctx, provenanceSourceIOKey{}, true), actor, pid, pin.SourceIDs) != nil {
			allowed = false
		}
		proof.States[id] = allowed
	}
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		current, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if proof.Identity != candidateIdentityHash(ctx) || provenanceProjectHash(current) != provenanceProjectHash(proof.Project) {
			return ErrVersionConflict
		}
		for id, pin := range proof.Pins {
			if pin.Verified {
				// A source/object mutation while real IO was in flight invalidates the
				// complete snapshot; it cannot be silently committed as a verified pin.
				currentDigest, err := nativeSourceDigest(tx, pid, pin.SourceIDs)
				if err != nil {
					currentDigest = "unavailable"
				}
				if currentDigest != proof.NativeBefore[id] {
					return ErrVersionConflict
				}
				if !s.provenancePinCurrent(ctx, tx, actor, current, pin, forModel) {
					proof.States[id] = false
				}
			}
		}
		return nil
	})
	if err != nil {
		return ctx, nil, err
	}
	proof.Snapshot = provenanceSnapshot(proof.Project, proof.Requests)
	return context.WithValue(ctx, provenanceProofKey{}, proof), proof, nil
}
func (s *Service) projectProvenanceProjection(ctx context.Context, actor Actor, p Project) (Project, error) {
	if !provenanceIdentityMatches(ctx, actor) {
		return Project{}, ErrNotFound
	}
	if nested, _ := ctx.Value(provenanceSourceIOKey{}).(bool); nested {
		p.Spec = map[string]string{}
		p.SpecFields = map[string]SpecField{}
		return p, nil
	}
	if err := s.Authorize(ctx, actor, p.ID, "read"); err != nil {
		return Project{}, err
	}
	if p.DiscardedAt != nil {
		p.Spec = map[string]string{}
		p.SpecFields = map[string]SpecField{}
		return p, nil
	}
	requests := provenanceFieldRequests(p)
	needed := false
	for _, request := range requests {
		needed = needed || request.Record != nil || request.Origin != "" && request.Origin != "human"
	}
	if !needed {
		return p, nil
	}
	_, proof, err := s.prepareProvenance(ctx, actor, p.ID, requests, false)
	if err != nil {
		return Project{}, err
	}
	return redactProvenanceProject(p, proof), nil
}
func (s *Service) draftProvenanceProjection(ctx context.Context, actor Actor, pid string, candidates []DraftCandidate) ([]DraftCandidate, error) {
	requests := []provenanceRequest{}
	for _, candidate := range candidates {
		if candidate.Provenance != nil {
			requests = append(requests, provenanceRequest{Value: candidate.Content, Record: candidate.Provenance, Creator: candidate.CreatedBy})
		}
	}
	if len(requests) == 0 {
		return candidates, nil
	}
	_, proof, err := s.prepareProvenance(ctx, actor, pid, requests, false)
	if err != nil {
		return nil, err
	}
	result := slices.Clone(candidates)
	for i, candidate := range result {
		if candidate.Provenance == nil {
			continue
		}
		pin := proof.Requests[provenanceRequestKey(candidate.Provenance, candidate.Content)]
		record := pin.Record
		record.Accessible = proof.States[pin.ID]
		record.NeedsReview = !record.Accessible
		candidate.Provenance = &record
		if !record.Accessible {
			redactProvenanceRecord(candidate.Provenance, pin.Verified)
			candidate.Content = ""
			candidate.Title = "来源待复核"
		}
		result[i] = candidate
	}
	return result, nil
}
func (s *Service) prepareProvenanceOperation(ctx context.Context, actor Actor, op, target, pid string, body any) (context.Context, error) {
	if pid == "" {
		return ctx, nil
	}
	if proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof); ok && proof.ActorID == actor.UserID && proof.TenantID == actor.TenantID && proof.Project.ID == pid && proof.Operation == op && proof.OperationBody == hashImpactValue(body) {
		return ctx, nil
	}
	if op == string(operationEditChangeDraft) {
		input, ok := body.(ExtendedChangeSetInput)
		if !ok {
			return ctx, ErrInvalidRequest
		}
		prepared, handled, err := s.prepareAgentChangeDraftEdit(ctx, actor, pid, target, input)
		if err != nil || handled {
			return prepared, err
		}
	}
	requests := []provenanceRequest{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		requests = provenanceFieldRequests(p)
		return nil
	})
	if err != nil {
		return ctx, err
	}
	ctx, err = s.prepareInverseOperationProvenance(ctx, actor, op, target, pid, body)
	if err != nil {
		return ctx, err
	}
	if op == "applyChangeSet" || op == "reassessChangeSet" || op == "assessChangeSet" || op == "stabilizeRevision" {
		var change ChangeSet
		err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			store, ok := tx.(changeSetTransaction)
			if !ok {
				return ErrInvalidState
			}
			parts := strings.Split(target, "/")
			if len(parts) < 2 {
				return ErrInvalidRequest
			}
			var e error
			change, e = store.ChangeSet(pid, parts[len(parts)-1])
			return e
		})
		if err != nil {
			return ctx, err
		}
		if change.Status != ChangeSetStatusDraft || op != "applyChangeSet" {
			ctx, err = s.prepareChangeProvenance(ctx, actor, change, map[string]bool{})
			if err != nil {
				return ctx, err
			}
		}
	}
	switch input := body.(type) {
	case SaveSpecInput:
		for key, metadata := range input.FieldMetadata {
			requests = append(requests, provenanceRequest{Key: key, Value: input.Fields[key], Record: metadata.Provenance, Origin: metadata.Origin, Creator: actor.UserID, Fresh: true})
		}
	case DraftCandidateInput:
		if input.Provenance != nil {
			requests = append(requests, provenanceRequest{Value: input.Content, Record: input.Provenance, Creator: actor.UserID, Fresh: true})
		}
	}
	needed := op == "activateProject"
	for _, request := range requests {
		needed = needed || request.Record != nil || request.Origin != "" && request.Origin != "human"
	}
	if !needed {
		if proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof); ok && proof.Frozen {
			proof.Operation = op
			proof.OperationBody = hashImpactValue(body)
		}
		return ctx, nil
	}
	oldProof, _ := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	prepared, proof, err := s.prepareProvenance(ctx, actor, pid, requests, false)
	if err == nil {
		proof.Operation = op
		proof.OperationBody = hashImpactValue(body)
		if oldProof != nil && oldProof.Frozen {
			if provenanceProjectHash(oldProof.Project) != provenanceProjectHash(proof.Project) {
				return ctx, ErrVersionConflict
			}
			for id, pin := range oldProof.Pins {
				proof.Pins[id] = pin
				proof.States[id] = oldProof.States[id]
				proof.NativeBefore[id] = oldProof.NativeBefore[id]
			}
		}
	}
	return prepared, err
}
func (s *Service) validateProvenanceOperation(ctx context.Context, tx Transaction, actor Actor, p Project, op string) error {
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		if op == "activateProject" {
			for _, request := range provenanceFieldRequests(p) {
				if request.Record != nil || request.Origin != "" && request.Origin != "human" {
					return ErrSourceUnavailable
				}
			}
		}
		return nil
	}
	if proof.ActorID != actor.UserID || proof.TenantID != actor.TenantID || proof.Project.ID != p.ID || proof.Identity != candidateIdentityHash(ctx) || provenanceProjectHash(p) != provenanceProjectHash(proof.Project) {
		return ErrVersionConflict
	}
	for id, pin := range proof.Pins {
		if proof.States[id] && !s.provenancePinCurrent(ctx, tx, actor, p, pin, false) {
			return ErrVersionConflict
		}
	}
	if op == "activateProject" {
		for _, request := range provenanceFieldRequests(p) {
			if request.Record == nil && (request.Origin == "" || request.Origin == "human") {
				continue
			}
			pin := proof.Requests[provenanceRequestKey(request.Record, request.Value)]
			if !proof.States[pin.ID] {
				return ErrSourceUnavailable
			}
		}
	}
	return nil
}
func (s *Service) storeOperationPins(ctx context.Context, tx Transaction, pid string) error {
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || len(proof.FreshPins) == 0 {
		return nil
	}
	if proof.Project.ID != pid {
		return ErrSourceUnavailable
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	for id, pin := range proof.FreshPins {
		// The stored pin never inherits a client accessibility assertion. A denied
		// or unidentified source is saved only as an unresolved draft pin.
		pin.Record.Accessible = proof.States[id]
		pin.Record.NeedsReview = !proof.States[id]
		var existing provenancePin
		_, err := store.GetProjectRecord(pid, "g1_provenance", id, &existing)
		if err == nil {
			existing.Record.Accessible = false
			existing.Record.NeedsReview = false
			comparison := pin
			comparison.Record.Accessible = false
			comparison.Record.NeedsReview = false
			if hashImpactValue(existing) != hashImpactValue(comparison) {
				return ErrVersionConflict
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err = store.PutProjectRecord(pid, "g1_provenance", id, 0, pin); err != nil {
			return err
		}
	}
	return nil
}
func serverProvenance(ctx context.Context, record *ProvenanceRecord, value, origin, status string) (*ProvenanceRecord, string, string) {
	if record == nil && (origin == "" || origin == "human") {
		return nil, origin, status
	}
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		return record, origin, status
	}
	pin, found := proof.Requests[provenanceRequestKey(record, value)]
	if !found {
		return record, origin, status
	}
	server := pin.Record
	server.Accessible = proof.States[pin.ID]
	server.NeedsReview = !server.Accessible
	if pin.Kind == "AgentTurn" && server.Accessible {
		origin = "ai_assisted_human"
		if slices.Contains(pin.OriginalAIHashes, hashImpactValue(strings.TrimSpace(value))) {
			origin = "ai_generated"
		}
		status = "pending_confirmation"
	} else if origin == "ai_generated" || origin == "ai_assisted_human" {
		origin = "human"
		status = "pending_confirmation"
	}
	if !server.Accessible {
		status = "pending_confirmation"
	}
	return &server, origin, status
}
func (s *Service) operationProvenanceProjection(ctx context.Context, actor Actor, op, pid string, body json.RawMessage) (json.RawMessage, error) {
	if len(body) > 0 && body[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(body, &values); err != nil {
			return nil, err
		}
		for i, value := range values {
			visible, err := s.operationProvenanceProjection(ctx, actor, op, pid, value)
			if err != nil {
				return nil, err
			}
			values[i] = visible
		}
		return json.Marshal(values)
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(body, &shape) != nil {
		return body, nil
	}
	if shape["action"] != nil && shape["input_hash"] != nil && shape["project_id"] != nil {
		var proposal ActionProposal
		if err := json.Unmarshal(body, &proposal); err != nil {
			return nil, err
		}
		visible, err := s.inheritedActionProjection(ctx, actor, proposal)
		if err != nil {
			return nil, err
		}
		return json.Marshal(visible)
	}
	if shape["spec"] != nil && shape["id"] != nil && shape["spec_revision"] != nil {
		var p Project
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		visible, err := s.projectProvenanceProjection(ctx, actor, p)
		if err != nil {
			return nil, err
		}
		return json.Marshal(visible)
	}
	if shape["base_spec_revision"] != nil && shape["fields"] != nil && shape["project_id"] != nil {
		var change ChangeSet
		if err := json.Unmarshal(body, &change); err != nil {
			return nil, err
		}
		if visible, handled, err := s.cachedChangeProvenanceProjection(ctx, actor, pid, body, change); handled || err != nil {
			return visible, err
		}
		visible, err := s.changeProvenanceProjection(ctx, actor, change)
		if err != nil {
			return nil, err
		}
		return json.Marshal(visible)
	}
	if shape["provenance"] != nil && shape["content"] != nil && shape["kind"] != nil {
		var candidate DraftCandidate
		if err := json.Unmarshal(body, &candidate); err != nil {
			return nil, err
		}
		visible, err := s.draftProvenanceProjection(ctx, actor, candidate.ProjectID, []DraftCandidate{candidate})
		if err != nil {
			return nil, err
		}
		return json.Marshal(visible[0])
	}
	for key, value := range shape {
		visible, err := s.operationProvenanceProjection(ctx, actor, op, pid, value)
		if err != nil {
			return nil, err
		}
		shape[key] = visible
	}
	if shape != nil {
		return json.Marshal(shape)
	}
	return body, nil
}

func (s *Service) PrepareProjectProvenance(ctx context.Context, actor Actor, pid string, forModel bool) (context.Context, ProjectProvenanceSnapshot, error) {
	prepared, proof, err := s.prepareProvenance(ctx, actor, pid, nil, forModel)
	if err != nil {
		return ctx, ProjectProvenanceSnapshot{}, err
	}
	for _, ref := range proof.Snapshot.Fields {
		if ref.PinID != "" && !proof.States[ref.PinID] {
			return ctx, ProjectProvenanceSnapshot{}, ErrSourceUnavailable
		}
	}
	err = s.repository.Transaction(prepared, TransactionOptions{}, func(tx Transaction) error {
		if _, ok := tx.(projectRecordTransaction); !ok {
			return nil
		}
		current, e := s.authorizeProject(prepared, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if e != nil {
			return e
		}
		if e = s.ValidateProjectProvenanceInTransaction(prepared, tx, actor, current, proof.Snapshot, forModel); e != nil {
			return e
		}
		persisted, e := persistProvenanceSnapshot(tx, current)
		if e != nil {
			return e
		}
		if hashImpactValue(persisted) != hashImpactValue(proof.Snapshot) {
			return ErrVersionConflict
		}
		return nil
	})
	if err != nil {
		return ctx, ProjectProvenanceSnapshot{}, err
	}
	return prepared, proof.Snapshot, nil
}
func (s *Service) AssertProjectProvenance(ctx context.Context, actor Actor, pid string, forModel bool) error {
	_, _, err := s.PrepareProjectProvenance(ctx, actor, pid, forModel)
	return err
}
func (s *Service) ValidateProjectProvenanceInTransaction(ctx context.Context, tx Transaction, actor Actor, p Project, snapshot ProjectProvenanceSnapshot, forModel bool) error {
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || proof.Frozen || proof.ActorID != actor.UserID || proof.TenantID != actor.TenantID || !provenanceIdentityMatches(ctx, actor) || proof.Identity != candidateIdentityHash(ctx) || proof.ForModel != forModel || proof.Project.ID != p.ID || provenanceProjectHash(proof.Project) != provenanceProjectHash(p) || hashImpactValue(proof.Snapshot) != hashImpactValue(snapshot) {
		return ErrSourceUnavailable
	}
	current, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, p.ID, AuthorizationRead, ""))
	if err != nil {
		return err
	}
	if provenanceProjectHash(current) != provenanceProjectHash(p) {
		return ErrVersionConflict
	}
	if err := ValidateFrozenProvenanceInput(p.Spec, snapshot); err != nil {
		return err
	}
	for _, ref := range snapshot.Fields {
		if ref.PinID != "" {
			pin, found := proof.Pins[ref.PinID]
			if !found || !proof.States[ref.PinID] || !s.provenancePinCurrent(ctx, tx, actor, p, pin, forModel) {
				return ErrSourceUnavailable
			}
		}
	}
	return nil
}
func (s *Service) ValidateProjectProvenanceSnapshot(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error {
	if err := ValidateFrozenProvenanceInput(spec, snapshot); err != nil {
		return err
	}
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		return s.ValidateProjectProvenanceInTransaction(ctx, tx, actor, p, snapshot, forModel)
	})
}
func (s *Service) AssertFrozenProjectProvenance(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error {
	_, err := s.PrepareFrozenProjectProvenance(ctx, actor, pid, spec, snapshot, forModel)
	return err
}
func (s *Service) PrepareFrozenProjectProvenance(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) (context.Context, error) {
	if len(spec) == 0 && snapshot.Digest == "" {
		prepared, proof, err := s.prepareProvenance(ctx, actor, pid, []provenanceRequest{}, forModel)
		if err != nil {
			return ctx, err
		}
		proof.Frozen = true
		proof.Snapshot = snapshot
		return prepared, nil
	}
	if snapshot.Digest == "" {
		return ctx, ErrSourceUnavailable
	}
	if snapshot.ProjectID != pid || ValidateFrozenProvenanceInput(spec, snapshot) != nil {
		return ctx, ErrSourceUnavailable
	}
	requests := []provenanceRequest{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrSourceUnavailable
		}
		var persisted ProjectProvenanceSnapshot
		if _, err := store.GetProjectRecord(pid, "g1_snapshot", snapshot.Digest, &persisted); err != nil || hashImpactValue(persisted) != hashImpactValue(snapshot) {
			return ErrSourceUnavailable
		}
		for _, ref := range snapshot.Fields {
			if ref.PinID == "" {
				continue
			}
			var pin provenancePin
			_, err := store.GetProjectRecord(pid, "g1_provenance", ref.PinID, &pin)
			if err != nil || pin.ID != ref.PinID || pin.ValueHash != ref.ValueHash || !pin.Verified {
				return ErrSourceUnavailable
			}
			record := pin.Record
			requests = append(requests, provenanceRequest{Key: ref.FieldKey, Value: spec[ref.FieldKey], Record: &record, Creator: record.CreatedBy})
		}
		return nil
	})
	if err != nil {
		return ctx, err
	}
	prepared, proof, err := s.prepareProvenance(ctx, actor, pid, requests, forModel)
	if err != nil {
		return ctx, err
	}
	for _, pin := range proof.Requests {
		if !proof.States[pin.ID] {
			return ctx, ErrSourceUnavailable
		}
	}
	proof.Frozen = true
	proof.Snapshot = snapshot
	return prepared, nil
}
func (s *Service) ValidateFrozenProjectProvenanceInTransaction(ctx context.Context, tx Transaction, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error {
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || !proof.Frozen || proof.ActorID != actor.UserID || proof.TenantID != actor.TenantID || proof.ForModel != forModel || proof.Project.ID != pid || proof.Identity != candidateIdentityHash(ctx) || !provenanceIdentityMatches(ctx, actor) || hashImpactValue(proof.Snapshot) != hashImpactValue(snapshot) || (len(spec) > 0 || snapshot.Digest != "") && ValidateFrozenProvenanceInput(spec, snapshot) != nil {
		return ErrSourceUnavailable
	}
	current, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
	if err != nil {
		return err
	}
	if provenanceProjectHash(current) != provenanceProjectHash(proof.Project) {
		return ErrVersionConflict
	}
	for id, pin := range proof.Pins {
		if !proof.States[id] || !s.provenancePinCurrent(ctx, tx, actor, current, pin, forModel) {
			return ErrSourceUnavailable
		}
	}
	return nil
}
