package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// Force an actual SQLite WAL snapshot upgrade: progress has read the run and
// task, then a real card save commits before progress asks for its write lock.
func TestAcademicRetractionRetriesStaleSQLiteSnapshot(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	anchor := f.anchor(t, "source-1", "wal-retraction-anchor")
	association := seedDraftAssociation(t, f, "wal-retraction-association", f.source.sources["source-1"])
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
	task := ResearchTask{ID: "wal-retraction-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "wal-retraction-run", Status: "running"}
	run := ResearchRun{ID: task.LatestRunID, ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: association.DOI, RetractionStatus: "retracted"}}}
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, 1, task); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, 1, run)
	}); err != nil {
		t.Fatal(err)
	}
	read, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	db := f.svc.testDB()
	if err := db.Callback().Query().After("gorm:query").Register("test:retraction_snapshot_barrier", func(tx *gorm.DB) {
		for _, arg := range tx.Statement.Vars {
			if id, ok := arg.(string); ok && id == task.ID {
				once.Do(func() { close(read); <-release })
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:retraction_snapshot_barrier") })
	done := make(chan error, 1)
	go func() { done <- f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run) }()
	select {
	case <-read:
	case err := <-done:
		t.Fatalf("progress returned before barrier: %v", err)
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("progress did not read task")
	}
	saved, _, _, saveErr := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "wal-retraction-card", input)
	close(release)
	if saveErr != nil {
		t.Fatalf("competing card save: %v", saveErr)
	}
	if len(saved) == 0 {
		t.Fatal("card save returned no record")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("progress failed after concurrent card commit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("progress did not finish")
	}
	if run.Version != 2 {
		t.Fatalf("run advanced %d times; want version 2", run.Version-1)
	}
	var card EvidenceCard
	if err := json.Unmarshal(saved, &card); err != nil {
		t.Fatal(err)
	}
	var current EvidenceCard
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var e error
		current, _, e = loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if current.Status != "NEEDS_REVIEW" || current.Confirmation != nil {
		t.Fatalf("retracted card remained eligible: %+v", current)
	}
	var count int64
	if err := db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, academicRetractionKind).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retraction observation: count=%d err=%v", count, err)
	}
	if err := db.Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, ResearchRunKind, run.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("run history duplicated: count=%d err=%v", count, err)
	}
	if err := db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", f.project.ID, "g2.research.progress", run.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("progress audit duplicated: count=%d err=%v", count, err)
	}
}

func TestResearchProgressRollbackDoesNotMutateCallerRun(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	task := ResearchTask{ID: "rollback-retry-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "rollback-retry-run", Status: "running"}
	run := ResearchRun{ID: task.LatestRunID, ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "completed", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/rollback-test", RetractionStatus: "retracted"}}}
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, 1, task); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, 1, run)
	}); err != nil {
		t.Fatal(err)
	}
	db := f.svc.testDB()
	if err := db.Exec(`CREATE TRIGGER fail_progress_retry_audit BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.research.progress' BEGIN SELECT RAISE(ABORT, 'synthetic progress audit failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before := run
	if err := f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run); err == nil {
		t.Fatal("expected audit failure")
	}
	if !reflect.DeepEqual(before, run) {
		t.Fatalf("rolled-back transaction mutated run: before=%+v after=%+v", before, run)
	}
	var count int64
	if err := db.Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, ResearchRunKind, run.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed progress wrote history: count=%d err=%v", count, err)
	}
	if err := db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, academicRetractionKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed progress persisted a retraction: count=%d err=%v", count, err)
	}
}

// Reverse the ordering: the card request has authorized from an old WAL
// snapshot, then a real retraction commits before the card's first write.
type retryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
	fail   bool
}

func (a *retryAuditSink) Record(_ context.Context, event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	if a.fail {
		return errors.New("synthetic shadow audit sink failure")
	}
	return nil
}

func TestAcademicCardSaveRetriesStaleSQLiteSnapshot(t *testing.T) {
	for _, scenario := range []string{"retraction", "permission", "source permission", "shadow audit failure"} {
		t.Run(scenario, func(t *testing.T) { testAcademicCardSaveRetriesStaleSQLiteSnapshot(t, scenario) })
	}
}

func testAcademicCardSaveRetriesStaleSQLiteSnapshot(t *testing.T, scenario string) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	anchor := f.anchor(t, "source-1", "wal-card-anchor")
	association := seedDraftAssociation(t, f, "wal-card-association", f.source.sources["source-1"])
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
	task := ResearchTask{ID: "wal-card-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "wal-card-run", Status: "running"}
	run := ResearchRun{ID: task.LatestRunID, ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: association.DOI, RetractionStatus: "retracted"}}}
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, 1, task); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, 1, run)
	}); err != nil {
		t.Fatal(err)
	}
	audit := &retryAuditSink{fail: scenario == "shadow audit failure"}
	f.svc.mode = AuthorizationModeLog
	f.svc.audit = audit
	read, release := make(chan struct{}), make(chan struct{})
	var authReads atomic.Int32
	db := f.svc.testDB()
	if err := db.Callback().Query().After("gorm:query").Register("test:card_snapshot_barrier", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "SELECT `role` FROM `tenant_members`") && authReads.Add(1) == 3 {
			close(read)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:card_snapshot_barrier") })
	done := make(chan error, 1)
	go func() {
		_, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "wal-card-save", input)
		done <- err
	}()
	select {
	case <-read:
	case err := <-done:
		t.Fatalf("card returned before barrier: %v", err)
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("card did not reach final authorization")
	}
	var progressErr error
	switch scenario {
	case "permission":
		progressErr = db.Exec("UPDATE tenant_members SET role = ? WHERE tenant_id = ? AND user_id = ?", "viewer", f.owner.TenantID, f.owner.UserID).Error
	case "source permission":
		f.policy.verdict = ErrSourceUnavailable
		// Force a stale SQLite snapshot independently of the external source
		// permission change, which is not stored in this database.
		progressErr = db.Exec("UPDATE tenant_members SET role = ? WHERE tenant_id = ? AND user_id = ?", "viewer", f.owner.TenantID, f.owner.UserID).Error
		if progressErr == nil {
			progressErr = db.Exec("UPDATE tenant_members SET role = ? WHERE tenant_id = ? AND user_id = ?", "owner", f.owner.TenantID, f.owner.UserID).Error
		}
	default:
		progressErr = f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run)
	}
	close(release)
	if progressErr != nil {
		t.Fatalf("competing retraction: %v", progressErr)
	}
	select {
	case err := <-done:
		switch scenario {
		case "permission":
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("revoked permission must not leak existence, got: %v", err)
			}
		case "source permission":
			if err == nil {
				t.Fatal("source permission revoked during retry was not rechecked")
			}
		default:
			if !errors.Is(err, ErrEvidenceIncomplete) && !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("card must reject changed retraction basis, got: %v", err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("card did not finish")
	}
	var count int64
	if err := db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, EvidenceCardKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed card request persisted a card: count=%d err=%v", count, err)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	allows, denies := 0, 0
	for _, event := range audit.events {
		if event.Capability == "shadow:evidence-write" {
			if event.Decision == "allow" {
				allows++
			} else if event.Decision == "deny" {
				denies++
			}
		}
	}
	if scenario == "permission" && (allows != 0 || denies != 1) {
		t.Fatalf("shadow audit must omit rolled-back attempt and record final denial once: allow=%d deny=%d", allows, denies)
	}
	if scenario == "source permission" && (allows != 2 || denies != 0) {
		t.Fatalf("unexpected source-revocation shadow events: allow=%d deny=%d", allows, denies)
	}
	if scenario != "permission" && scenario != "source permission" && (allows != 4 || denies != 0) {
		t.Fatalf("shadow audit must record only the completed attempt: allow=%d deny=%d", allows, denies)
	}
}
