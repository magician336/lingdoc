package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
)

const g1OperationProvenanceKind = "g1_operation_provenance"

// A mutable draft's latest history describes its latest fields. Every cached
// response needs its own immutable copy, committed with that operation/result.
type g1OperationProvenance struct {
	Identity     string             `json:"identity"`
	ProjectID    string             `json:"project_id"`
	ChangeID     string             `json:"change_id"`
	BodyHash     string             `json:"body_hash"`
	ResponseHash string             `json:"response_hash"`
	History      g1ChangeProvenance `json:"history"`
}

type g1CachedOperationProvenanceKey struct{}

func g1OperationProvenanceID(id OperationIdentity) string {
	return hashImpactValue(struct {
		TenantID               uint64
		UserID, Operation      string
		Target, IdempotencyKey string
	}{id.Actor.TenantID, id.Actor.UserID, id.Operation, id.Target, id.Key})
}

func g1MutableChangeReceipt(op string) bool {
	switch op {
	case "createChangeSet", "createChangeDraft", "editChangeDraft", "assessChangeSet":
		return true
	default:
		return false
	}
}

// Current operation authorization is checked before even consulting the saved
// proof. External SourcePolicy checks still run outside native transactions.
func (s *Service) g1PrepareCachedChangeOperation(ctx context.Context, actor Actor, request AuthorizationRequest, id OperationIdentity, body any) (context.Context, bool, error) {
	if !g1MutableChangeReceipt(id.Operation) {
		return ctx, false, nil
	}
	identity, bodyHash := g1OperationProvenanceID(id), hashImpactValue(body)
	if cached, ok := ctx.Value(g1CachedOperationProvenanceKey{}).(g1OperationProvenance); ok && cached.Identity == identity && cached.BodyHash == bodyHash {
		return ctx, true, nil
	}
	var saved g1OperationProvenance
	var change ChangeSet
	found := false
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, request); err != nil {
			return err
		}
		store, supports := tx.(g5Transaction)
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
		version, err := store.G5Get(request.ProjectID, g1OperationProvenanceKind, identity, &saved)
		if errors.Is(err, ErrNotFound) {
			// A c4-era operation may have lost its AI association through a
			// later edit. Missing proof must never imply human provenance.
			return ErrSourceUnavailable
		}
		if err != nil {
			return err
		}
		if version != 1 || saved.Identity != identity || saved.ProjectID != request.ProjectID || saved.BodyHash != previous.BodyHash || saved.ResponseHash != g2Hash(string(previous.Body)) {
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
	prepared, err := s.g1PrepareChangeProvenanceHistory(ctx, actor, change, saved.History, map[string]bool{change.ID: true})
	if err != nil {
		return ctx, true, err
	}
	proof, ok := prepared.Value(g1ProvenanceProofKey{}).(*g1ProvenanceProof)
	if !ok {
		return ctx, true, ErrSourceUnavailable
	}
	proof.Operation, proof.OperationBody = id.Operation, bodyHash
	return context.WithValue(prepared, g1CachedOperationProvenanceKey{}, saved), true, nil
}

func g1StoreOperationProvenance(tx Transaction, id OperationIdentity, result OperationResult, value any) error {
	if !g1MutableChangeReceipt(id.Operation) {
		return nil
	}
	store, supports := tx.(g5Transaction)
	if !supports {
		return nil
	}
	change, ok := value.(ChangeSet)
	if !ok || change.ID == "" || change.ProjectID == "" {
		return ErrInvalidState
	}
	var history g1ChangeProvenance
	if _, err := store.G5Get(change.ProjectID, "g1_change_provenance", change.ID, &history); err != nil {
		return err
	}
	identity := g1OperationProvenanceID(id)
	proof := g1OperationProvenance{Identity: identity, ProjectID: change.ProjectID, ChangeID: change.ID, BodyHash: result.BodyHash, ResponseHash: g2Hash(string(result.Body)), History: history}
	// Version zero is insert-only. A successful operation's proof is never
	// rewritten by an edit, reassessment, permission change or replay.
	return store.G5Put(change.ProjectID, g1OperationProvenanceKind, identity, 0, proof)
}

func (s *Service) g1CachedChangeProjection(ctx context.Context, actor Actor, pid string, body json.RawMessage, change ChangeSet) (json.RawMessage, bool, error) {
	saved, found := ctx.Value(g1CachedOperationProvenanceKey{}).(g1OperationProvenance)
	if !found {
		return nil, false, nil
	}
	if saved.ProjectID != pid || saved.ChangeID != change.ID || saved.ResponseHash != g2Hash(string(body)) {
		return nil, true, ErrSourceUnavailable
	}
	if _, err := s.g1PrepareChangeProvenanceHistory(ctx, actor, change, saved.History, map[string]bool{change.ID: true}); err != nil {
		return nil, true, err
	}
	return body, true, nil
}
