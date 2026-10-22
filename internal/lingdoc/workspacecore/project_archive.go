package workspacecore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Archiving a project.
//
// Archiving is the Owner's way of saying that a project is finished. It is
// not the same fact as discarding: a discarded project is a draft that never
// took shape (discarded_at), an archived project is one that ran its course
// (status = archived). The domain documentation keeps the two apart, so this
// file keeps them apart too:
//
//   - only a draft project may be discarded, and only an active project may be
//     archived;
//   - an archived project accepts no further change -- it is a terminal state,
//     not a pause.
//
// The action is Owner-only, and it answers to the same gate as the other
// Owner-only actions: while the Owner's tenant identity has lapsed, archiving
// is frozen and the refusal says why. The judgement lives in one place
// (owner_unavailable.go); this file asks it and obeys it.

// ProjectStatusArchived is the terminal status of an archived project.
const ProjectStatusArchived = "archived"

// ArchiveProject ends a project.
//
// The project version the caller read is part of the request: a decision taken
// on a project that has since changed must not land on the changed project.
// The idempotency key makes a retried request one action, so a retry cannot
// produce two archive events.
func (s *Service) ArchiveProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	// The freeze is asked before the ordinary authorization: the ordinary
	// authorization answers a lapsed Owner with a plain "not found", and that
	// is exactly the answer the caller cannot act on.
	if err := s.requireOwnerDecision(ctx, actor, projectID); err != nil {
		return nil, 0, false, err
	}
	if err := s.Authorize(ctx, actor, projectID, "manage"); err != nil {
		return nil, 0, false, err
	}
	input := projectVersionInput{ExpectedProjectVersion: expectedProjectVersion}
	return runOperation(s, ctx, actor, operationInput(operationArchiveProject, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[Project]{}, featureErr
		}
		// The authoritative Owner decision, taken on the project the write
		// lands on: the Owner may not be the actor, and the Owner may no
		// longer be able to act.
		if err := ownerDecisionInTransaction(featureTx, p, actor); err != nil {
			return operationResult[Project]{}, err
		}
		if p.ProjectVersion != expectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		// A draft may only be discarded, and an archived project is finished.
		if p.Status != "active" || p.DiscardedAt != nil {
			return operationResult[Project]{}, ErrInvalidState
		}
		now := time.Now().UTC()
		next := p
		next.Status = ProjectStatusArchived
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		// The audit row is part of the same transaction as the write: an
		// archive that is not traceable has not happened.
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.archive", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion, "previous_status": p.Status}, CreatedAt: now}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// ArchiveProjectResult exposes the archived project without transport fields.
func (s *Service) ArchiveProjectResult(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.ArchiveProject(ctx, actor, projectID, key, expectedProjectVersion)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}
