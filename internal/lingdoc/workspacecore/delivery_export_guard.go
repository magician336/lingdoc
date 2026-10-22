package workspacecore

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// AuthorizeDeliveryExport checks the authenticated human and current Owner in
// one database snapshot. Read/download authority is deliberately separate.
func (s *Service) AuthorizeDeliveryExport(ctx context.Context, actor Actor, projectID string) error {
	if err := RequireHumanDecision(ctx); err != nil {
		return err
	}
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		return s.AuthorizeDeliveryExportInTransaction(ctx, tx, actor, projectID)
	})
}

// AuthorizeDeliveryExportInTransaction is the final formal-export write gate.
// It only reads the supplied parent transaction, never the main pool or sources.
func (s *Service) AuthorizeDeliveryExportInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID string) error {
	if err := RequireHumanDecision(ctx); err != nil {
		return err
	}
	principal, ok := types.PrincipalFromContext(ctx)
	caller := types.CallerFromContext(ctx)
	tenant, tenantOK := types.TenantIDFromContext(ctx)
	if !ok || principal.Type != types.PrincipalWebUser || principal.ID != actor.UserID ||
		!tenantOK || tenant != actor.TenantID || caller.TenantID != actor.TenantID || caller.UserID != actor.UserID {
		return ErrHumanDecisionRequired
	}
	actor.Role = caller.Role
	p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationOpsControl, ""))
	if err != nil {
		return err
	}
	if p.Status != "active" || !ownerCurrentlyAvailable(tx, p, actor.TenantID, actor.UserID) {
		return ErrNotFound
	}
	if err := RuntimeGate(tx, actor, p, "export_start", effectiveRuntimeMode(s.runtimeMode)); err != nil {
		return err
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return err
	}
	var active int64
	if err := db.Table("users").Where("id=? AND is_active=? AND deleted_at IS NULL", actor.UserID, true).Count(&active).Error; err != nil {
		return err
	}
	if active != 1 {
		return ErrNotFound
	}
	return nil
}

// DeliveryRuntimeObjects is populated only by trusted delivery adapters from
// an actual persisted receipt/snapshot/artifact, never by an HTTP event body.
// Version fields describe the actual snapshot, including historical replays.
type DeliveryRuntimeObjects struct {
	ReceiptID, SnapshotID, ExportID, RulesetHash         string
	ProjectVersion, ContextRevision, TemplateCopyVersion int64
}

// ObserveDeliveryRuntime resolves causation through existing server events in
// the same transaction as the new event. It performs no source/main-pool IO.
func (s *Service) ObserveDeliveryRuntime(ctx context.Context, actor Actor, projectID, operation, status string, durationMS int64, objects DeliveryRuntimeObjects) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.ListRuntimeRecords(actor.TenantID, projectID, "event")
		if err != nil {
			return err
		}
		trace := RuntimeTraceFromContext(ctx)
		// These associations come from the delivery result, not supplied trace.
		trace.SnapshotID, trace.ExportID = objects.SnapshotID, objects.ExportID
		trace.CausationID, trace.TaskID, trace.ChangeSetID, trace.ImpactTaskID = "", "", "", ""
		trace.RuntimeMode = effectiveRuntimeMode(s.runtimeMode)
		trace.TargetRevision = objects.ContextRevision
		if trace.TargetRevision == 0 && objects.ProjectVersion == p.ProjectVersion {
			trace.TargetRevision = p.CurrentContextRevision
		}
		switch operation {
		case "preflight":
			trace.Category = "preflight"
			if objects.ReceiptID != "" {
				receipt, err := readImpactComputation(tx, p, objects.ReceiptID)
				if err != nil {
					return err
				}
				trace.CorrelationID, trace.CausationID = receipt.ID, receipt.ID
			}
		case "freeze":
			trace.Category = "snapshot"
		case "export_start":
			trace.Category = "export"
		case "download":
			trace.Category = "download"
		}
		var parent RuntimeEvent
		for _, row := range rows {
			var event RuntimeEvent
			if err := json.Unmarshal([]byte(row.BodyJSON), &event); err != nil {
				return err
			}
			if event.Origin != "runtime" || event.Status != "success" {
				continue
			}
			matches := false
			switch operation {
			case "freeze":
				if objects.SnapshotID != "" && event.Category == "snapshot" && event.SnapshotID == objects.SnapshotID {
					matches = true // Preserve the original lineage on replay.
				} else if objects.SnapshotID != "" && event.Category == "preflight" && event.CausationID != "" {
					receipt, err := readImpactComputation(tx, p, event.CausationID)
					matches = err == nil && receipt.Status == "passed" && receipt.ProjectVersion == objects.ProjectVersion && receipt.TemplateCopyVersion == objects.TemplateCopyVersion && receipt.RulesetHash == objects.RulesetHash
				}
			case "export_start":
				matches = objects.SnapshotID != "" && event.Category == "snapshot" && event.SnapshotID == objects.SnapshotID
			case "download":
				matches = objects.ExportID != "" && event.Category == "export" && event.ExportID == objects.ExportID && event.SnapshotID == objects.SnapshotID
			}
			if matches && (parent.ID == "" || event.CreatedAt.After(parent.CreatedAt)) {
				parent = event
			}
		}
		if parent.ID != "" {
			trace.CorrelationID, trace.CausationID = parent.CorrelationID, parent.ID
		} else if objects.SnapshotID != "" {
			// Direct freeze is allowed by the delivery contract. Its actual
			// snapshot starts a new lineage when no matching check was observed.
			trace.CorrelationID = objects.SnapshotID
			if operation != "freeze" {
				trace.CausationID = objects.SnapshotID
			}
		}
		return RecordRuntimeOperationContext(WithRuntimeTrace(ctx, trace), tx, actor, p, operation, status, durationMS)
	})
}

// DeliveryExportSourceRef describes an actual frozen excerpt, without its text.
type DeliveryExportSourceRef struct {
	SourceID, AssetID string
	AssetRevision     int64
}
type deliveryExportNativeKey struct{}
type deliveryExportNativeProof struct {
	identity, projectID, refsHash, basis, sourceDigest string
	projectVersion                                     int64
}

func deliveryExportNativeDigest(tx Transaction, p Project, actor Actor, refs []DeliveryExportSourceRef) (string, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return "", err
	}
	ids := []string{}
	policies := []any{}
	for _, ref := range refs {
		if ref.SourceID == "" || ref.AssetID == "" || ref.AssetRevision < 1 {
			return "", ErrSourceUnavailable
		}
		var asset evidence.ProjectAsset
		if err := db.Where("id=? AND project_id=? AND tenant_id=? AND deleted_at IS NULL", ref.AssetID, p.ID, actor.TenantID).Take(&asset).Error; err != nil {
			return "", storageError(err)
		}
		if asset.AssetRevision != ref.AssetRevision || asset.ProcessingState != "ready" {
			return "", ErrSourceUnavailable
		}
		var chunk types.Chunk
		if err := db.Where("id=? AND tenant_id=? AND knowledge_id=? AND deleted_at IS NULL AND is_enabled=?", ref.SourceID, actor.TenantID, asset.KnowledgeID, true).Take(&chunk).Error; err != nil {
			return "", storageError(err)
		}
		var kb types.KnowledgeBase
		if err := db.Where("id=? AND tenant_id=? AND deleted_at IS NULL", asset.KnowledgeBaseID, actor.TenantID).Take(&kb).Error; err != nil {
			return "", storageError(err)
		}
		if chunk.KnowledgeBaseID != kb.ID {
			return "", ErrSourceUnavailable
		}
		policy, _, provenance, err := ReadProjectAssetPolicy(tx, p, ref.AssetID)
		if err != nil {
			return "", err
		}
		if policy.AssetRevision != ref.AssetRevision {
			return "", ErrSourceUnavailable
		}
		var revision evidence.ProjectAssetRevision
		if err := db.Where("asset_id=? AND revision_no=? AND status=?", ref.AssetID, ref.AssetRevision, evidence.RevisionActive).Take(&revision).Error; err != nil {
			return "", storageError(err)
		}
		policies = append(policies, struct {
			Ref                        DeliveryExportSourceRef
			KBID, KBCreatorID          string
			KBTenant                   uint64
			Revision                   evidence.ProjectAssetRevision
			ChunkIndex, StartAt, EndAt int
			Policy                     ProjectAssetPolicy
			Provenance                 string
		}{ref, kb.ID, kb.CreatorID, kb.TenantID, revision, chunk.ChunkIndex, chunk.StartAt, chunk.EndAt, policy, provenance})
		ids = append(ids, ref.SourceID)
	}
	digest, ok := tx.(impactComputationSourceTransaction)
	if !ok {
		return "", ErrInvalidState
	}
	sourceDigest, err := digest.ImpactSourceDigest(p.ID, ids)
	if err != nil {
		return "", err
	}
	return hashImpactValue(struct {
		Sources  string
		Policies []any
	}{sourceDigest, policies}), nil
}

// PrepareDeliveryExportNativeBasis releases each snapshot before the actual
// source gateway/origin IO. The private proof closes mutations during that IO.
func (s *Service) PrepareDeliveryExportNativeBasis(ctx context.Context, actor Actor, projectID string, expectedProjectVersion int64, refs []DeliveryExportSourceRef) (context.Context, error) {
	proof := deliveryExportNativeProof{identity: candidateIdentityHash(ctx), projectID: projectID, refsHash: hashImpactValue(refs)}
	capture := func(tx Transaction) error {
		if err := s.AuthorizeDeliveryExportInTransaction(ctx, tx, actor, projectID); err != nil {
			return err
		}
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if expectedProjectVersion > 0 && p.ProjectVersion != expectedProjectVersion {
			return ErrVersionConflict
		}
		proof.projectVersion, proof.basis = p.ProjectVersion, candidateFormalBasisHash(p)
		proof.sourceDigest, err = deliveryExportNativeDigest(tx, p, actor, refs)
		return err
	}
	if err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, capture); err != nil {
		return ctx, err
	}
	ids := []string{}
	for _, ref := range refs {
		ids = append(ids, ref.SourceID)
	}
	if err := s.checkSources(ctx, actor, projectID, sortedImpactSourceIDs(ids)); err != nil {
		return ctx, err
	}
	prepared := context.WithValue(ctx, deliveryExportNativeKey{}, proof)
	if err := s.repository.Transaction(prepared, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		return s.ValidateDeliveryExportNativeBasisInTransaction(prepared, tx, actor, projectID, refs)
	}); err != nil {
		return ctx, err
	}
	return prepared, nil
}

func (s *Service) ValidateDeliveryExportNativeBasisInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID string, refs []DeliveryExportSourceRef) error {
	proof, ok := ctx.Value(deliveryExportNativeKey{}).(deliveryExportNativeProof)
	if !ok || proof.identity != candidateIdentityHash(ctx) || proof.projectID != projectID || proof.refsHash != hashImpactValue(refs) {
		return ErrSourceUnavailable
	}
	if err := s.AuthorizeDeliveryExportInTransaction(ctx, tx, actor, projectID); err != nil {
		return err
	}
	p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
	if err != nil {
		return err
	}
	if p.ProjectVersion != proof.projectVersion || candidateFormalBasisHash(p) != proof.basis {
		return ErrVersionConflict
	}
	digest, err := deliveryExportNativeDigest(tx, p, actor, refs)
	if err != nil {
		return err
	}
	if digest != proof.sourceDigest {
		return ErrSourceUnavailable
	}
	return nil
}
