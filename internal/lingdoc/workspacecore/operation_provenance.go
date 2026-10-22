package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
)

const operationProvenanceKind = "g1_operation_provenance"

// A mutable draft's latest history describes its latest fields. Every cached
// response needs its own immutable copy, committed with that operation/result.
type operationProvenance struct {
	Identity     string           `json:"identity"`
	ProjectID    string           `json:"project_id"`
	ChangeID     string           `json:"change_id"`
	BodyHash     string           `json:"body_hash"`
	ResponseHash string           `json:"response_hash"`
	History      changeProvenance `json:"history"`
}

type cachedOperationProvenanceKey struct{}

func operationProvenanceID(id OperationIdentity) string {
	return hashImpactValue(struct {
		TenantID               uint64
		UserID, Operation      string
		Target, IdempotencyKey string
	}{id.Actor.TenantID, id.Actor.UserID, id.Operation, id.Target, id.Key})
}

func mutableChangeReceipt(op string) bool {
	switch op {
	case "createChangeSet", "createChangeDraft", "editChangeDraft", "assessChangeSet", "reassessChangeSet":
		return true
	default:
		return false
	}
}

// Current operation authorization is checked before even consulting the saved
// proof. External SourcePolicy checks still run outside native transactions.
func (s *Service) prepareCachedChangeOperation(ctx context.Context, actor Actor, request AuthorizationRequest, id OperationIdentity, body any) (context.Context, bool, error) {
	if !mutableChangeReceipt(id.Operation) {
		return ctx, false, nil
	}
	identity, bodyHash := operationProvenanceID(id), hashImpactValue(body)
	if cached, ok := ctx.Value(cachedOperationProvenanceKey{}).(operationProvenance); ok && cached.Identity == identity && cached.BodyHash == bodyHash {
		return ctx, true, nil
	}
	var saved operationProvenance
	var change ChangeSet
	found := false
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, request); err != nil {
			return err
		}
		store, supports := tx.(projectRecordTransaction)
		if !supports {
			return nil // Preserve existing non-G1 repository ports.
		}
		operations, err := mutationPort[OperationTransaction](tx)
		if err != nil {
			return err
		}
		previous, exists, err := operations.Operation(id)
		if err != nil || !exists {
			return err
		}
		found = true
		if previous.BodyHash != bodyHash {
			return ErrIdempotencyConflict
		}
		version, err := store.GetProjectRecord(request.ProjectID, operationProvenanceKind, identity, &saved)
		if errors.Is(err, ErrNotFound) {
			// A c4-era operation may have lost its AI association through a
			// later edit. Missing proof must never imply human provenance.
			return ErrSourceUnavailable
		}
		if err != nil {
			return err
		}
		if version != 1 || saved.Identity != identity || saved.ProjectID != request.ProjectID || saved.BodyHash != previous.BodyHash || saved.ResponseHash != sha256Hex(string(previous.Body)) {
			return ErrSourceUnavailable
		}
		if err := json.Unmarshal(previous.Body, &change); err != nil {
			return markReplayPayloadDecodeError(err)
		}
		if change.ID != saved.ChangeID || change.ProjectID != saved.ProjectID {
			return ErrSourceUnavailable
		}
		return nil
	})
	if err != nil || !found {
		return ctx, found, err
	}
	prepared, err := s.prepareChangeProvenanceHistory(ctx, actor, change, saved.History, map[string]bool{change.ID: true})
	if err != nil {
		return ctx, true, err
	}
	proof, ok := prepared.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		return ctx, true, ErrSourceUnavailable
	}
	proof.Operation, proof.OperationBody = id.Operation, bodyHash
	return context.WithValue(prepared, cachedOperationProvenanceKey{}, saved), true, nil
}

func storeOperationProvenance(tx Transaction, id OperationIdentity, result OperationResult, value any) error {
	if !mutableChangeReceipt(id.Operation) {
		return nil
	}
	store, supports := tx.(projectRecordTransaction)
	if !supports {
		return nil
	}
	change, ok := value.(ChangeSet)
	if !ok || change.ID == "" || change.ProjectID == "" {
		return ErrInvalidState
	}
	var history changeProvenance
	if _, err := store.GetProjectRecord(change.ProjectID, "g1_change_provenance", change.ID, &history); err != nil {
		return err
	}
	identity := operationProvenanceID(id)
	proof := operationProvenance{Identity: identity, ProjectID: change.ProjectID, ChangeID: change.ID, BodyHash: result.BodyHash, ResponseHash: sha256Hex(string(result.Body)), History: history}
	// Version zero is insert-only. A successful operation's proof is never
	// rewritten by an edit, reassessment, permission change or replay.
	return store.PutProjectRecord(change.ProjectID, operationProvenanceKind, identity, 0, proof)
}

func (s *Service) cachedChangeProvenanceProjection(ctx context.Context, actor Actor, pid string, body json.RawMessage, change ChangeSet) (json.RawMessage, bool, error) {
	saved, found := ctx.Value(cachedOperationProvenanceKey{}).(operationProvenance)
	if !found {
		return nil, false, nil
	}
	if saved.ProjectID != pid || saved.ChangeID != change.ID || saved.ResponseHash != sha256Hex(string(body)) {
		return nil, true, ErrSourceUnavailable
	}
	if _, err := s.prepareChangeProvenanceHistory(ctx, actor, change, saved.History, map[string]bool{change.ID: true}); err != nil {
		return nil, true, err
	}
	return body, true, nil
}
