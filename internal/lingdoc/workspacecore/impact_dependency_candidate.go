package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AI-proposed dependency candidates (G3 gap closure #252, AC-G3-15).
//
// A candidate is NOT a dependency edge. It is a proposal that carries the
// evidence a human needs in order to decide: the two objects, the object
// VERSIONS the proposal was made against, the excerpt that triggered it, the
// proposed rationale and a confidence.
//
// The invariants that make this a separate concept rather than a flavour of
// DependencyEdge:
//
//   - An undecided candidate never enters the dependency graph. It therefore
//     cannot invalidate a chapter and cannot block an export (AC-G3-13).
//   - Only a human decision produces an edge. "confirm" writes the candidate as
//     proposed; "modify" writes the corrected candidate and remembers the
//     replaced tuple in SupersededRelation/SupersededAction; "reject" writes
//     nothing at all.
//   - A human decision is final for the project version it was taken in. The
//     next automated run in the SAME version does not re-propose the tuple;
//     after the project version moves on, the candidate may appear again.
//   - The object versions the candidate carries are re-checked when the human
//     confirms. A candidate whose basis moved on requires re-evaluation; it is
//     never confirmed silently.

// Candidate lifecycle states.
const (
	// DependencyCandidateStatusProposed is a candidate waiting for a human
	// decision. It is not part of the dependency graph.
	DependencyCandidateStatusProposed = "proposed"
	// DependencyCandidateStatusConfirmed is a candidate the human confirmed as
	// proposed. The dependency edge carries it.
	DependencyCandidateStatusConfirmed = "confirmed"
	// DependencyCandidateStatusModified is a candidate the human confirmed
	// after correcting it. The dependency edge carries the corrected tuple and
	// remembers the replaced one.
	DependencyCandidateStatusModified = "modified"
	// DependencyCandidateStatusRejected is a candidate the human refused. No
	// edge is created for it.
	DependencyCandidateStatusRejected = "rejected"
	// DependencyCandidateStatusStale is a candidate whose object versions moved
	// on before a human decided. It requires re-evaluation.
	DependencyCandidateStatusStale = "stale"
)

// Reasons a candidate was not proposed again.
const (
	// DependencyCandidateSuppressedRejected means a human rejected this tuple
	// in the current project version.
	DependencyCandidateSuppressedRejected = "rejected_in_same_version"
	// DependencyCandidateSuppressedModified means a human corrected this tuple
	// in the current project version.
	DependencyCandidateSuppressedModified = "modified_in_same_version"
	// DependencyCandidateSuppressedConfirmed means the tuple is already a
	// confirmed dependency in the current project version.
	DependencyCandidateSuppressedConfirmed = "confirmed_in_same_version"
	// DependencyCandidateSuppressedProposed means the tuple is already waiting
	// for a decision in the current project version.
	DependencyCandidateSuppressedProposed = "already_proposed"
)

const (
	operationSubmitDependencyCandidates operationName = "submitDependencyCandidates"
	operationDecideDependencyCandidate  operationName = "decideDependencyCandidate"
)

// ErrDependencyCandidateStale reports that the object versions a candidate
// carries are no longer current, so the candidate must be re-evaluated instead
// of being confirmed.
var ErrDependencyCandidateStale = errors.New("dependency candidate requires re-evaluation")

// dependencyCandidateStaleError is the client-facing presentation of
// ErrDependencyCandidateStale. It states its own status and code so the
// shared transport error table does not grow a branch per feature.
type dependencyCandidateStaleError struct{ cause error }

func (e dependencyCandidateStaleError) Error() string     { return ErrDependencyCandidateStale.Error() }
func (e dependencyCandidateStaleError) Unwrap() error     { return e.cause }
func (e dependencyCandidateStaleError) ClientStatus() int { return 409 }
func (e dependencyCandidateStaleError) ClientCode() string {
	return "dependency_candidate_requires_reevaluation"
}
func (e dependencyCandidateStaleError) ClientMessage() string {
	return "候选依赖依据的对象版本已变化，请重新评估后再确认。"
}

// DependencyCandidateScope is the range the user selected before the AI
// proposed anything ("研究条件、证据卡和第 2-4 章"). It is recorded with the
// batch so a later reader can tell which slice of the project the proposal
// was derived from.
type DependencyCandidateScope struct {
	// Objects are the objects the proposal was derived from. When it is not
	// empty, both endpoints of a candidate must be inside it: an AI may not
	// read outside the range the user chose.
	Objects []ObjectVersionRef `json:"objects,omitempty"`
	// TargetKinds restricts which object kinds may be proposed as targets.
	TargetKinds []string `json:"target_kinds,omitempty"`
	// Reason records why the range was chosen.
	Reason string `json:"reason,omitempty"`
}

// DependencyCandidate is one proposed dependency.
type DependencyCandidate struct {
	ID        string           `json:"id"`
	ProjectID string           `json:"project_id"`
	BatchID   string           `json:"batch_id"`
	Source    ObjectVersionRef `json:"source"`
	Target    ObjectVersionRef `json:"target"`
	Relation  string           `json:"relation"`
	Action    string           `json:"action"`
	// Evidence is the excerpt that triggered the proposal (依据片段). A
	// candidate without it is refused: a proposal the user cannot check is
	// not a proposal.
	Evidence string `json:"evidence"`
	// Reason is the proposed rationale (建议理由).
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
	// Decision is the human conclusion: confirm, modify or reject.
	Decision       string     `json:"decision,omitempty"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	// EdgeID is the dependency edge a confirmed or modified candidate
	// produced. A rejected candidate has no edge.
	EdgeID string `json:"edge_id,omitempty"`
	// SupersededRelation/SupersededAction remember the tuple a human replaced
	// with decision="modify", so a later automated run that re-proposes it is
	// refused instead of silently resurrecting the corrected candidate.
	SupersededRelation string `json:"superseded_relation,omitempty"`
	SupersededAction   string `json:"superseded_action,omitempty"`
	// ProjectVersion is the project version the candidate was raised in. A
	// human decision binds to it: the same tuple may be proposed again only
	// after the project version moved on.
	ProjectVersion  int64     `json:"project_version"`
	ContextRevision int64     `json:"context_revision"`
	ModelRunID      string    `json:"model_run_id,omitempty"`
	ActorID         string    `json:"actor_id"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DependencyCandidateSuppression records a candidate that was NOT proposed
// again, and why.
type DependencyCandidateSuppression struct {
	Source   ObjectVersionRef `json:"source"`
	Target   ObjectVersionRef `json:"target"`
	Relation string           `json:"relation"`
	Action   string           `json:"action"`
	// Reason is one of the DependencyCandidateSuppressed* codes.
	Reason string `json:"reason"`
	// PreviousCandidateID is the candidate that carries the human decision.
	PreviousCandidateID    string `json:"previous_candidate_id,omitempty"`
	PreviousStatus         string `json:"previous_status,omitempty"`
	PreviousProjectVersion int64  `json:"previous_project_version,omitempty"`
}

// DependencyCandidateBatch is one batch of proposed dependencies.
type DependencyCandidateBatch struct {
	ID              string                           `json:"id"`
	ProjectID       string                           `json:"project_id"`
	IdempotencyKey  string                           `json:"idempotency_key"`
	Scope           DependencyCandidateScope         `json:"scope"`
	Status          string                           `json:"status"`
	Candidates      []DependencyCandidate            `json:"candidates"`
	Suppressed      []DependencyCandidateSuppression `json:"suppressed"`
	ProjectVersion  int64                            `json:"project_version"`
	ContextRevision int64                            `json:"context_revision"`
	ActorID         string                           `json:"actor_id"`
	Reason          string                           `json:"reason"`
	CreatedAt       time.Time                        `json:"created_at"`
	UpdatedAt       time.Time                        `json:"updated_at"`
	// ContentHash identifies the CONTENT of the batch. It is never exposed on
	// the wire: it exists so the same idempotency key with different content
	// is a conflict instead of a silent overwrite.
	ContentHash string `json:"-"`
}

// DependencyCandidateInput is one proposed dependency inside a batch.
type DependencyCandidateInput struct {
	Source     ObjectVersionRef `json:"source"`
	Target     ObjectVersionRef `json:"target"`
	Relation   string           `json:"relation"`
	Action     string           `json:"action"`
	Evidence   string           `json:"evidence"`
	Reason     string           `json:"reason"`
	Confidence float64          `json:"confidence"`
	ModelRunID string           `json:"model_run_id,omitempty"`
}

// DependencyCandidateBatchInput submits a batch of proposed dependencies.
type DependencyCandidateBatchInput struct {
	ExpectedContextRevision int64 `json:"expected_context_revision"`
	// IdempotencyKey identifies the batch itself. It defaults to the
	// transport idempotency key. Replaying the same key never produces a
	// second batch.
	IdempotencyKey string                     `json:"idempotency_key,omitempty"`
	Scope          DependencyCandidateScope   `json:"scope"`
	Reason         string                     `json:"reason"`
	Candidates     []DependencyCandidateInput `json:"candidates"`
}

// DependencyCandidateDecisionResult is what a human decision produced.
type DependencyCandidateDecisionResult struct {
	Candidate DependencyCandidate `json:"candidate"`
	// Edge is the dependency edge the decision produced. A rejected candidate
	// has no edge.
	Edge *DependencyEdge `json:"edge,omitempty"`
}

const (
	dependencyCandidateBatchLimit    = 200
	dependencyCandidateTextLimit     = 2000
	dependencyCandidateRelationLimit = 128
)

func validDependencyCandidateInput(candidate DependencyCandidateInput) bool {
	if !validObjectRef(candidate.Source) || !validObjectRef(candidate.Target) {
		return false
	}
	if strings.TrimSpace(candidate.Relation) == "" || len(candidate.Relation) > dependencyCandidateRelationLimit {
		return false
	}
	if !validImpactAction(candidate.Action) {
		return false
	}
	if strings.TrimSpace(candidate.Evidence) == "" || len(candidate.Evidence) > dependencyCandidateTextLimit {
		return false
	}
	if strings.TrimSpace(candidate.Reason) == "" || len(candidate.Reason) > dependencyCandidateTextLimit {
		return false
	}
	if math.IsNaN(candidate.Confidence) || candidate.Confidence < 0 || candidate.Confidence > 1 {
		return false
	}
	return true
}

// dependencyCandidateScopeAllows reports whether a candidate stays inside the
// range the user selected.
func dependencyCandidateScopeAllows(scope DependencyCandidateScope, candidate DependencyCandidateInput) bool {
	if len(scope.Objects) > 0 {
		source, target := false, false
		for _, object := range scope.Objects {
			if object == candidate.Source {
				source = true
			}
			if object == candidate.Target {
				target = true
			}
		}
		if !source || !target {
			return false
		}
	}
	if len(scope.TargetKinds) > 0 && !slices.Contains(scope.TargetKinds, candidate.Target.Kind) {
		return false
	}
	return true
}

func validateDependencyCandidateBatchInput(input DependencyCandidateBatchInput) error {
	if input.ExpectedContextRevision < 0 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > dependencyCandidateTextLimit {
		return ErrInvalidRequest
	}
	if len(input.Candidates) == 0 || len(input.Candidates) > dependencyCandidateBatchLimit {
		return ErrInvalidRequest
	}
	if len(input.IdempotencyKey) > 128 {
		return ErrInvalidRequest
	}
	for _, candidate := range input.Candidates {
		if !validDependencyCandidateInput(candidate) {
			return ErrInvalidRequest
		}
		if !dependencyCandidateScopeAllows(input.Scope, candidate) {
			return ErrInvalidRequest
		}
	}
	return nil
}

// dependencyCandidateContentHash identifies the CONTENT of a batch, so the
// same idempotency key with different content is a conflict rather than a
// silent overwrite.
func dependencyCandidateContentHash(input DependencyCandidateBatchInput) string {
	raw, err := json.Marshal(input.Candidates)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// dependencyCandidateTuple is the identity of a proposal: the two objects and
// the relation and action between them.
type dependencyCandidateTuple struct {
	source   ObjectVersionRef
	target   ObjectVersionRef
	relation string
	action   string
}

func dependencyCandidateTupleOf(candidate DependencyCandidateInput) dependencyCandidateTuple {
	return dependencyCandidateTuple{source: candidate.Source, target: candidate.Target, relation: candidate.Relation, action: candidate.Action}
}

func dependencyCandidateTupleOfRow(candidate DependencyCandidate) dependencyCandidateTuple {
	return dependencyCandidateTuple{source: candidate.Source, target: candidate.Target, relation: candidate.Relation, action: candidate.Action}
}

// suppressDependencyCandidate reports the human decision that already covers
// this tuple in the current project version.
//
// A decision binds to the project version it was taken in: the same tuple may
// be proposed again only after the project version moved on. That is what
// keeps a refused or corrected candidate from being recommended again and
// again inside the same version.
func suppressDependencyCandidate(prior []DependencyCandidate, projectVersion int64, candidate DependencyCandidateInput) (DependencyCandidateSuppression, bool) {
	tuple := dependencyCandidateTupleOf(candidate)
	var latest DependencyCandidate
	found := false
	for _, old := range prior {
		if dependencyCandidateTupleOfRow(old) != tuple {
			continue
		}
		if !found || old.ProjectVersion > latest.ProjectVersion ||
			(old.ProjectVersion == latest.ProjectVersion && old.CreatedAt.After(latest.CreatedAt)) {
			latest = old
			found = true
		}
	}
	if !found || latest.ProjectVersion != projectVersion {
		return DependencyCandidateSuppression{}, false
	}
	reason := DependencyCandidateSuppressedProposed
	switch latest.Status {
	case DependencyCandidateStatusRejected:
		reason = DependencyCandidateSuppressedRejected
	case DependencyCandidateStatusModified:
		reason = DependencyCandidateSuppressedModified
	case DependencyCandidateStatusConfirmed:
		reason = DependencyCandidateSuppressedConfirmed
	}
	return DependencyCandidateSuppression{
		Source:                 candidate.Source,
		Target:                 candidate.Target,
		Relation:               candidate.Relation,
		Action:                 candidate.Action,
		Reason:                 reason,
		PreviousCandidateID:    latest.ID,
		PreviousStatus:         latest.Status,
		PreviousProjectVersion: latest.ProjectVersion,
	}, true
}

// SubmitDependencyCandidateBatch records a batch of proposed dependencies.
//
// The batch is written in one transaction: either every candidate of the batch
// is stored, or none of them is. The idempotency key of the batch makes a
// replay return the same batch instead of a second one.
func (s *Service) SubmitDependencyCandidateBatch(ctx context.Context, actor Actor, projectID, key string, input DependencyCandidateBatchInput) (json.RawMessage, int, bool, error) {
	if err := validateDependencyCandidateBatchInput(input); err != nil {
		return nil, 0, false, err
	}
	batchKey := strings.TrimSpace(input.IdempotencyKey)
	if batchKey == "" {
		batchKey = key
	}
	refs := []ObjectVersionRef{}
	for _, candidate := range input.Candidates {
		refs = append(refs, candidate.Source, candidate.Target)
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationSubmitDependencyCandidates, projectID+"/dependency-candidates", key, input, refs, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[DependencyCandidateBatch], error) {
		if p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[DependencyCandidateBatch]{}, ErrVersionConflict
		}
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return operationResult[DependencyCandidateBatch]{}, ErrInvalidState
		}
		existing, err := store.DependencyCandidateBatchByKey(projectID, actor.UserID, batchKey)
		if err == nil {
			// The same batch key never produces a second batch. The same key
			// with different content is a conflict, not an overwrite.
			if existing.ContentHash != dependencyCandidateContentHash(input) {
				return operationResult[DependencyCandidateBatch]{}, ErrIdempotencyConflict
			}
			return operationResult[DependencyCandidateBatch]{Value: existing, Status: operationStatusOK}, nil
		}
		if err != ErrNotFound {
			return operationResult[DependencyCandidateBatch]{}, err
		}
		prior, err := store.DependencyCandidates(projectID)
		if err != nil {
			return operationResult[DependencyCandidateBatch]{}, err
		}
		now := time.Now().UTC()
		batch := DependencyCandidateBatch{
			ID:              uuid.NewString(),
			ProjectID:       projectID,
			IdempotencyKey:  batchKey,
			Scope:           input.Scope,
			Status:          DependencyCandidateStatusProposed,
			Candidates:      []DependencyCandidate{},
			Suppressed:      []DependencyCandidateSuppression{},
			ProjectVersion:  p.ProjectVersion,
			ContextRevision: p.CurrentContextRevision,
			ActorID:         actor.UserID,
			Reason:          input.Reason,
			CreatedAt:       now,
			UpdatedAt:       now,
			ContentHash:     dependencyCandidateContentHash(input),
		}
		for _, candidate := range input.Candidates {
			if suppression, blocked := suppressDependencyCandidate(prior, p.ProjectVersion, candidate); blocked {
				batch.Suppressed = append(batch.Suppressed, suppression)
				continue
			}
			row := DependencyCandidate{
				ID:              uuid.NewString(),
				ProjectID:       projectID,
				BatchID:         batch.ID,
				Source:          candidate.Source,
				Target:          candidate.Target,
				Relation:        candidate.Relation,
				Action:          candidate.Action,
				Evidence:        candidate.Evidence,
				Reason:          candidate.Reason,
				Confidence:      candidate.Confidence,
				Status:          DependencyCandidateStatusProposed,
				ProjectVersion:  p.ProjectVersion,
				ContextRevision: p.CurrentContextRevision,
				ModelRunID:      candidate.ModelRunID,
				ActorID:         actor.UserID,
				Version:         1,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := store.InsertDependencyCandidate(row); err != nil {
				return operationResult[DependencyCandidateBatch]{}, err
			}
			batch.Candidates = append(batch.Candidates, row)
			prior = append(prior, row)
		}
		if err := store.InsertDependencyCandidateBatch(batch); err != nil {
			return operationResult[DependencyCandidateBatch]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "dependency.candidates.submit", batch.ID, map[string]any{
			"candidates":      len(batch.Candidates),
			"suppressed":      len(batch.Suppressed),
			"project_version": p.ProjectVersion,
			"reason":          input.Reason,
		}); err != nil {
			return operationResult[DependencyCandidateBatch]{}, err
		}
		return operationResult[DependencyCandidateBatch]{Value: batch, Status: operationStatusCreated}, nil
	})
}

// ListDependencyCandidates reads the proposed dependencies of a project, with
// the human conclusions attached.
//
// The candidates the caller may not read are left out: a candidate names the
// objects it was derived from, and those objects may carry sources the caller
// has no access to.
func (s *Service) ListDependencyCandidates(ctx context.Context, actor Actor, projectID, batchID string) ([]DependencyCandidateBatch, error) {
	var p Project
	var batches []DependencyCandidateBatch
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		p, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return ErrInvalidState
		}
		batches, err = store.DependencyCandidateBatches(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	visible := []DependencyCandidateBatch{}
	for _, batch := range batches {
		if batchID != "" && batch.ID != batchID {
			continue
		}
		candidates := []DependencyCandidate{}
		for _, candidate := range batch.Candidates {
			allowed, err := s.dependencyCandidateReadable(ctx, actor, projectID, p, candidate)
			if err != nil {
				return nil, err
			}
			if allowed {
				candidates = append(candidates, candidate)
			}
		}
		next := batch
		next.Candidates = candidates
		visible = append(visible, next)
	}
	return visible, nil
}

// dependencyCandidateReadable reports whether the caller may see a candidate.
func (s *Service) dependencyCandidateReadable(ctx context.Context, actor Actor, projectID string, p Project, candidate DependencyCandidate) (bool, error) {
	var sources []string
	readable := true
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		for _, ref := range []ObjectVersionRef{candidate.Source, candidate.Target} {
			// The object state is read WITHOUT the version comparison: a
			// candidate whose basis moved on stays visible, because the user
			// has to see that it needs re-evaluation.
			state, err := readImpactObjectState(tx, p, ref)
			if err != nil {
				readable = false
				return nil
			}
			sources = append(sources, state.SourceIDs...)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if !readable {
		return false, nil
	}
	return s.checkSources(ctx, actor, projectID, sources) == nil, nil
}

// DecideDependencyCandidate applies a human decision to a proposed dependency.
//
// The three states are the ones the dependency edge decision already uses:
// "confirm" writes the candidate as proposed, "modify" writes the corrected
// candidate and remembers the replaced tuple, "reject" writes nothing.
//
// The object versions the candidate carries are re-checked before the
// decision is applied. A candidate whose basis moved on requires
// re-evaluation; it is never confirmed silently.
func (s *Service) DecideDependencyCandidate(ctx context.Context, actor Actor, projectID, candidateID, key string, input DependencyDecisionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"confirm", "reject", "modify"}, input.Decision) || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if input.Decision == "modify" {
		if (input.Relation == nil && input.Action == nil && input.Confidence == nil) ||
			(input.Relation != nil && (strings.TrimSpace(*input.Relation) == "" || len(*input.Relation) > dependencyCandidateRelationLimit)) ||
			(input.Action != nil && !validImpactAction(*input.Action)) ||
			(input.Confidence != nil && (math.IsNaN(*input.Confidence) || *input.Confidence < 0 || *input.Confidence > 1)) {
			return nil, 0, false, ErrInvalidRequest
		}
	} else if input.Relation != nil || input.Action != nil || input.Confidence != nil {
		return nil, 0, false, ErrInvalidRequest
	}
	candidate, err := s.readDependencyCandidate(ctx, actor, projectID, candidateID)
	if err != nil {
		return nil, 0, false, err
	}
	if candidate.Status != DependencyCandidateStatusProposed {
		return nil, 0, false, ErrInvalidState
	}
	if candidate.Version != input.ExpectedVersion {
		return nil, 0, false, ErrVersionConflict
	}
	stale, err := s.dependencyCandidateBasisMoved(ctx, actor, projectID, candidate)
	if err != nil {
		return nil, 0, false, err
	}
	if stale {
		// The candidate is marked so the user sees that it needs
		// re-evaluation. The marking is best effort: the decision itself
		// still fails.
		_ = s.markDependencyCandidateStale(ctx, actor, projectID, candidate)
		return nil, 0, false, dependencyCandidateStaleError{cause: ErrDependencyCandidateStale}
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationDecideDependencyCandidate, projectID+"/dependency-candidates/"+candidateID, key, input, []ObjectVersionRef{candidate.Source, candidate.Target}, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[DependencyCandidateDecisionResult], error) {
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return operationResult[DependencyCandidateDecisionResult]{}, ErrInvalidState
		}
		edges, ok := tx.(impactTransaction)
		if !ok {
			return operationResult[DependencyCandidateDecisionResult]{}, ErrInvalidState
		}
		current, err := store.DependencyCandidate(projectID, candidateID)
		if err != nil {
			return operationResult[DependencyCandidateDecisionResult]{}, err
		}
		if current.Version != input.ExpectedVersion {
			return operationResult[DependencyCandidateDecisionResult]{}, ErrVersionConflict
		}
		if current.Status != DependencyCandidateStatusProposed {
			return operationResult[DependencyCandidateDecisionResult]{}, ErrInvalidState
		}
		existing, err := edges.DependencyEdges(projectID)
		if err != nil {
			return operationResult[DependencyCandidateDecisionResult]{}, err
		}
		relation, action, confidence := current.Relation, current.Action, current.Confidence
		if input.Decision == "modify" {
			if input.Relation != nil {
				relation = *input.Relation
			}
			if input.Action != nil {
				action = *input.Action
			}
			if input.Confidence != nil {
				confidence = *input.Confidence
			}
		}
		// A tuple a human already replaced may not come back: the edge keeps
		// the replaced relation and action, and re-proposing it would undo the
		// recorded decision.
		for _, edge := range existing {
			if edge.Source != current.Source || edge.Target != current.Target {
				continue
			}
			if edge.SupersededRelation == current.Relation && edge.SupersededAction == current.Action {
				return operationResult[DependencyCandidateDecisionResult]{}, ErrDependencyDecided
			}
		}
		now := time.Now().UTC()
		next := current
		next.Decision = input.Decision
		next.DecisionReason = input.Reason
		next.DecidedBy = actor.UserID
		next.DecidedAt = &now
		next.UpdatedAt = now
		next.Version++
		var edge *DependencyEdge
		if input.Decision != "reject" {
			decided, err := decideDependencyCandidateEdge(edges, actor, projectID, current, relation, action, confidence, input)
			if err != nil {
				return operationResult[DependencyCandidateDecisionResult]{}, err
			}
			edge = &decided
			next.EdgeID = decided.ID
			if input.Decision == "modify" {
				next.Status = DependencyCandidateStatusModified
				next.SupersededRelation = current.Relation
				next.SupersededAction = current.Action
			} else {
				next.Status = DependencyCandidateStatusConfirmed
			}
		} else {
			next.Status = DependencyCandidateStatusRejected
		}
		if err := store.UpdateDependencyCandidate(current, next); err != nil {
			return operationResult[DependencyCandidateDecisionResult]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "dependency.candidate."+input.Decision, candidateID, map[string]any{
			"source":  current.Source,
			"target":  current.Target,
			"action":  current.Action,
			"status":  next.Status,
			"edge_id": next.EdgeID,
			"reason":  input.Reason,
		}); err != nil {
			return operationResult[DependencyCandidateDecisionResult]{}, err
		}
		return operationResult[DependencyCandidateDecisionResult]{Value: DependencyCandidateDecisionResult{Candidate: next, Edge: edge}, Status: operationStatusOK}, nil
	})
}

// decideDependencyCandidateEdge writes the dependency edge a confirmed
// candidate produces.
//
// The edge is written the way the existing dependency decision writes it: the
// proposal becomes a "suggested" edge first (version 1) and the human
// decision turns it into a "confirmed" edge (version 2). A modification
// therefore carries the corrected reason and version, and remembers the
// replaced relation and action.
func decideDependencyCandidateEdge(edges impactTransaction, actor Actor, projectID string, candidate DependencyCandidate, relation, action string, confidence float64, input DependencyDecisionInput) (DependencyEdge, error) {
	existing, err := edges.DependencyEdges(projectID)
	if err != nil {
		return DependencyEdge{}, err
	}
	now := time.Now().UTC()
	edge := DependencyEdge{}
	found := false
	for _, candidateEdge := range existing {
		if candidateEdge.Source == candidate.Source && candidateEdge.Target == candidate.Target &&
			candidateEdge.Relation == relation && candidateEdge.Action == action {
			edge = candidateEdge
			found = true
			break
		}
	}
	if !found {
		edge = DependencyEdge{
			ID:         uuid.NewString(),
			ProjectID:  projectID,
			Source:     candidate.Source,
			Target:     candidate.Target,
			Relation:   candidate.Relation,
			Action:     candidate.Action,
			Origin:     "ai",
			Reason:     candidate.Reason,
			Confidence: candidate.Confidence,
			Status:     "suggested",
			ActorID:    actor.UserID,
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := edges.PutDependencyEdge(edge); err != nil {
			return DependencyEdge{}, err
		}
	}
	next := edge
	next.Version++
	next.ActorID = actor.UserID
	next.Reason = input.Reason
	next.UpdatedAt = now
	if input.Decision == "modify" {
		next.Relation = relation
		next.Action = action
		next.Confidence = confidence
		next.SupersededRelation = candidate.Relation
		next.SupersededAction = candidate.Action
	}
	next.Status = "confirmed"
	if err := edges.UpdateDependencyEdge(edge, next); err != nil {
		return DependencyEdge{}, err
	}
	return next, nil
}

// readDependencyCandidate reads one candidate with write authorization.
func (s *Service) readDependencyCandidate(ctx context.Context, actor Actor, projectID, candidateID string) (DependencyCandidate, error) {
	var candidate DependencyCandidate
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, "")); err != nil {
			return err
		}
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return ErrInvalidState
		}
		var err error
		candidate, err = store.DependencyCandidate(projectID, candidateID)
		return err
	})
	if err != nil {
		return DependencyCandidate{}, err
	}
	return candidate, nil
}

// dependencyCandidateBasisMoved reports whether the object versions the
// candidate carries are no longer current.
func (s *Service) dependencyCandidateBasisMoved(ctx context.Context, actor Actor, projectID string, candidate DependencyCandidate) (bool, error) {
	moved := false
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		for _, ref := range []ObjectVersionRef{candidate.Source, candidate.Target} {
			if _, err := currentImpactObject(tx, p, ref); err != nil {
				if errors.Is(err, ErrVersionConflict) {
					moved = true
					return nil
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return moved, nil
}

// markDependencyCandidateStale records that a candidate needs re-evaluation.
func (s *Service) markDependencyCandidateStale(ctx context.Context, actor Actor, projectID string, candidate DependencyCandidate) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return ErrInvalidState
		}
		current, err := store.DependencyCandidate(projectID, candidate.ID)
		if err != nil {
			return err
		}
		if current.Status != DependencyCandidateStatusProposed {
			return nil
		}
		next := current
		next.Status = DependencyCandidateStatusStale
		next.Version++
		next.UpdatedAt = time.Now().UTC()
		if err := store.UpdateDependencyCandidate(current, next); err != nil {
			return err
		}
		return recordAuditEvent(tx, actor, projectID, "dependency.candidate.stale", candidate.ID, map[string]any{
			"source": candidate.Source,
			"target": candidate.Target,
		})
	})
}
