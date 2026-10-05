package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
)

type g3ConfirmationAuthorizer struct{}

func (g3ConfirmationAuthorizer) Authorize(context.Context, string, string, string) error { return nil }

type g3FrozenRenderer struct{}

func (g3FrozenRenderer) RenderFrozen(delivery.DeliveryInput) ([]byte, error) {
	return []byte("g3-export"), nil
}

// TestG3ChangeReviewBlocksAndRestoresDelivery exercises the two-chapter seam
// through the real workspace service, confirmation store, delivery checker,
// release store, and export service. It proves that invalidation is a live
// delivery precondition rather than only a ChangeSet row decoration.
func TestG3ChangeReviewBlocksAndRestoresDelivery(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS lingdoc_operations (
			tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, operation TEXT NOT NULL,
			target TEXT NOT NULL, key TEXT NOT NULL, body_hash TEXT NOT NULL,
			response_json TEXT NOT NULL, response_code INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, user_id, operation, target, key)
		)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_project_audits (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, actor_id TEXT NOT NULL,
			action TEXT NOT NULL, target TEXT NOT NULL, details_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_change_sets (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, created_by TEXT NOT NULL,
			reason TEXT NOT NULL, status TEXT NOT NULL, base_context_revision INTEGER NOT NULL,
			target_context_revision INTEGER, base_spec_revision INTEGER NOT NULL,
			target_spec_revision INTEGER, fields_json TEXT NOT NULL DEFAULT '[]',
			impacts_json TEXT NOT NULL DEFAULT '[]', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			applied_at DATETIME
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed G3 table: %v", err)
		}
	}
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}

	service, ok := handler.Service().(*Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *Service", handler.Service())
	}
	actor := Actor{TenantID: 7, UserID: deliveryActorID}
	project, err := service.GetProject(context.Background(), actor, "project-1")
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	if project.CurrentContextRevision != 1 || project.SpecRevision != deliverySpecRevision {
		t.Fatalf("initial revisions = context %d spec %d", project.CurrentContextRevision, project.SpecRevision)
	}

	changeRaw, status, replay, err := service.CreateChangeSet(context.Background(), actor, "project-1", "g3-delivery-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "公开的合成调查样本", NewValue: "新的研究对象"}},
		AffectedChapterIDs:      []string{deliveryChapterID, "chapter-2"},
		Reason:                  "研究对象发生变化",
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("create changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if len(change.Impacts) != 2 || change.Status != "assessed" {
		t.Fatalf("preview = %+v", change)
	}
	if _, status, replay, err := service.ApplyChangeSet(context.Background(), actor, "project-1", change.ID, "g3-delivery-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply changeset: status=%d replay=%v err=%v", status, replay, err)
	}

	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	inputs := &candidateadoption.DeliveryInputService{Reader: store, Authorizer: deliveryTestAuthorizer{}}
	releaseStore := delivery.NewMemorySnapshotStore()
	release := NewDeliveryReleaseService(inputs, handler.DeliveryInputBuilder(), releaseStore)
	if release == nil {
		t.Fatal("delivery release service did not assemble")
	}
	project, err = service.GetProject(context.Background(), actor, "project-1")
	if err != nil {
		t.Fatalf("read changed project: %v", err)
	}
	blocked, err := release.Check(policyContext(), deliveryActorID, "project-1", project.ProjectVersion)
	if err != nil {
		t.Fatalf("blocked delivery check: %v", err)
	}
	if blocked.Status != delivery.CheckBlocked || !containsIssueCode(blocked, "confirmation_stale") {
		t.Fatalf("blocked check = %s issues=%v", blocked.Status, blocked.Issues)
	}
	blockedSnapshot, _, err := release.Prepare(policyContext(), deliveryActorID, "project-1", "g3-delivery-blocked", project.ProjectVersion)
	if err != nil {
		t.Fatalf("blocked freeze: %v", err)
	}
	if blockedSnapshot.Check.Status != delivery.CheckBlocked {
		t.Fatalf("blocked snapshot status = %s", blockedSnapshot.Check.Status)
	}
	exports := delivery.NewMemoryExportStore()
	exporter := NewDeliveryExportServiceWithPorts(
		releaseStore, exports, g3FrozenRenderer{}, delivery.FrozenValidatorFunc(func(delivery.DeliveryInput, []byte) error { return nil }),
		inputs, handler.CandidateAdoptionSourcePolicy(),
	)
	if exporter == nil {
		t.Fatal("delivery export service did not assemble")
	}
	if _, _, err := exporter.Start(policyContext(), deliveryActorID, "project-1", blockedSnapshot.ID, "g3-export-blocked"); !errors.Is(err, delivery.ErrExportPreflightBlocked) {
		t.Fatalf("blocked export error = %v, want ErrExportPreflightBlocked", err)
	}

	confirmations := candidateadoption.NewConfirmationService(store, handler.CandidateAdoptionSourcePolicy().(candidateadoption.ConfirmationSourcePolicy), g3ConfirmationAuthorizer{})
	currentInput, err := store.ReadDeliveryInput(context.Background(), "project-1")
	if err != nil {
		t.Fatalf("read current delivery input: %v", err)
	}
	for _, chapter := range currentInput.Chapters {
		if chapter.ChapterVersionID == nil {
			t.Fatalf("affected chapter %s has no current version", chapter.ChapterID)
		}
		decisions := make([]candidateadoption.ReviewDecision, 0, len(chapter.ReviewItems))
		for _, item := range chapter.ReviewItems {
			decisions = append(decisions, candidateadoption.ReviewDecision{ReviewItemID: item.ID, Disposition: "retained_warning", Reason: "重新核对后保留提示"})
		}
		if _, replayed, err := confirmations.ConfirmChapter(policyContext(), candidateadoption.ConfirmChapterInput{
			ProjectID: "project-1", ChapterID: chapter.ChapterID, ActorID: deliveryActorID,
			IdempotencyKey: "g3-review-" + chapter.ChapterID, ExpectedChapterVersionID: *chapter.ChapterVersionID,
			ExpectedSpecRevision: currentInput.SpecRevision, Decisions: decisions,
		}); err != nil || replayed {
			t.Fatalf("confirm %s: replayed=%v err=%v", chapter.ChapterID, replayed, err)
		}
	}
	project, err = service.GetProject(context.Background(), actor, "project-1")
	if err != nil {
		t.Fatalf("read re-confirmed project: %v", err)
	}
	passed, err := release.Check(policyContext(), deliveryActorID, "project-1", project.ProjectVersion)
	if err != nil {
		t.Fatalf("reconfirmed delivery check: %v", err)
	}
	if passed.Status != delivery.CheckPassed {
		t.Fatalf("reconfirmed check = %s issues=%v", passed.Status, passed.Issues)
	}
	snapshot, replayed, err := release.Prepare(policyContext(), deliveryActorID, "project-1", "g3-delivery-passed", project.ProjectVersion)
	if err != nil || replayed || snapshot.Check.Status != delivery.CheckPassed {
		t.Fatalf("passed freeze: replayed=%v status=%s err=%v", replayed, snapshot.Check.Status, err)
	}
	artifact, replayed, err := exporter.Start(policyContext(), deliveryActorID, "project-1", snapshot.ID, "g3-export-passed")
	if err != nil || replayed || artifact.Status != delivery.ExportVerified {
		t.Fatalf("passed export: artifact=%+v replayed=%v err=%v", artifact, replayed, err)
	}
	_, bytes, err := exporter.Download(policyContext(), deliveryActorID, "project-1", artifact.ID)
	if err != nil || string(bytes) != "g3-export" {
		t.Fatalf("downloaded export = %q err=%v", string(bytes), err)
	}
}
