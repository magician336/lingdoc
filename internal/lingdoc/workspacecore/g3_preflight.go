package workspacecore

import (
	"context"
	"encoding/json"
	"time"
)

type ImpactPreflightEvidence struct {
	ProofID, ProofKind                                   string
	SnapshotID                                           string
	SnapshotDigest                                       string
	ProjectVersion, ContextRevision, TemplateCopyVersion int64
	RulesetHash                                          string
	Status                                               string
	IsCurrent                                            bool
	SourceIDs                                            []string
}
type ImpactPreflightChecker interface {
	Recheck(context.Context, Actor, string, string) (ImpactPreflightEvidence, error)
}

func (s *Service) SetImpactPreflightChecker(checker ImpactPreflightChecker) {
	s.impactPreflight = checker
}
func impactPreflightProofID(proof ImpactPreflightEvidence) string {
	if proof.ProofID != "" {
		return proof.ProofID
	}
	return proof.SnapshotID
}
func validImpactPreflight(p Project, snapshotID string, proof ImpactPreflightEvidence) bool {
	return impactPreflightProofID(proof) == snapshotID && proof.SnapshotDigest != "" && proof.Status == "passed" && proof.IsCurrent && proof.ProjectVersion == p.ProjectVersion && proof.ContextRevision == p.CurrentContextRevision && proof.TemplateCopyVersion == p.TemplateCopyVersion && p.TemplateCopy != nil && proof.RulesetHash == p.TemplateCopy.RulesetHash
}
func (s *Service) recheckImpactPreflight(ctx context.Context, actor Actor, projectID, snapshotID string) (ImpactPreflightEvidence, error) {
	if s.impactPreflight == nil {
		return ImpactPreflightEvidence{}, ErrInvalidState
	}
	p, err := s.GetProject(ctx, actor, projectID)
	if err != nil {
		return ImpactPreflightEvidence{}, err
	}
	proof, err := s.impactPreflight.Recheck(ctx, actor, projectID, snapshotID)
	if err != nil {
		return proof, err
	}
	if !validImpactPreflight(p, snapshotID, proof) {
		return proof, ErrVersionConflict
	}
	if err := s.checkSources(ctx, actor, projectID, proof.SourceIDs); err != nil {
		return proof, err
	}
	return proof, nil
}

// Call after a real current-input Delivery Preflight. The public input is only
// a persisted check receipt or snapshot ID; the server adapter supplies the actual current outcome.
func (s *Service) AcknowledgeImpactPreflight(ctx context.Context, actor Actor, projectID, changeID, snapshotID, key string) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if snapshotID == "" || changeID == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := s.Authorize(ctx, actor, projectID, "manage"); err != nil {
		return nil, 0, false, err
	}
	proof, err := s.recheckImpactPreflight(ctx, actor, projectID, snapshotID)
	if err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, sourceOperationInput(operationAcknowledgeImpactPreflight, projectID+"/"+changeID, key, map[string]string{"change_set_id": changeID, "proof_id": snapshotID}, authorizationRequest(actor, projectID, AuthorizationManage, ""), impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ImpactPreflightAcknowledgement], error) {
		if !validImpactPreflight(p, snapshotID, proof) {
			return operationResult[ImpactPreflightAcknowledgement]{}, ErrVersionConflict
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return operationResult[ImpactPreflightAcknowledgement]{}, ErrInvalidState
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ImpactPreflightAcknowledgement]{}, ErrInvalidState
		}
		change, err := changes.ChangeSet(projectID, changeID)
		if err != nil {
			return operationResult[ImpactPreflightAcknowledgement]{}, err
		}
		if change.TargetContextRevision == nil || *change.TargetContextRevision != p.CurrentContextRevision {
			return operationResult[ImpactPreflightAcknowledgement]{}, ErrVersionConflict
		}
		tasks, err := store.ImpactTasks(projectID)
		if err != nil {
			return operationResult[ImpactPreflightAcknowledgement]{}, err
		}
		closed := []string{}
		for _, task := range tasks {
			if task.ChangeSetID != changeID || task.TargetRevision != p.CurrentContextRevision || task.Action != "BLOCK_RELEASE" || task.Status == "DONE" {
				continue
			}
			old := task
			task.Status = "DONE"
			task.Version++
			kind := "ReleaseSnapshot"
			if proof.ProofKind == "check_receipt" {
				kind = "DeliveryPreflight"
			}
			task.ClosureEvidence = []ObjectVersionRef{{Kind: kind, ID: snapshotID, Version: proof.SnapshotDigest}}
			task.Disposition = "当前领域Preflight通过"
			task.UpdatedAt = time.Now().UTC()
			if err := store.UpdateImpactTask(old, task); err != nil {
				return operationResult[ImpactPreflightAcknowledgement]{}, err
			}
			closed = append(closed, task.ID)
		}
		if err := g3Audit(tx, actor, projectID, "impact_task.preflight", changeID, map[string]any{"proof_id": snapshotID, "snapshot_digest": proof.SnapshotDigest, "closed_task_ids": closed}); err != nil {
			return operationResult[ImpactPreflightAcknowledgement]{}, err
		}
		return operationResult[ImpactPreflightAcknowledgement]{Value: ImpactPreflightAcknowledgement{ClosedTaskIDs: closed, ProofID: snapshotID, ContextRevision: p.CurrentContextRevision}, Status: operationStatusOK}, nil
	})
}
