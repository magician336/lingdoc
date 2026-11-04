package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func seedAcademicEvidenceInvalidation(t *testing.T, f agentFixture, id, doi, catalogVersion string) EvidenceCard {
	t.Helper()
	association := AcademicSourceAssociation{ID: id + "-association", ProjectID: f.project.ID, Version: 1, DOI: doi, CatalogVersion: catalogVersion, Status: "recorded"}
	card := EvidenceCard{ID: id, ProjectID: f.project.ID, Version: 1, Status: "CONFIRMED", AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmation: &EvidenceConfirmation{ConfirmedBy: f.owner.UserID, CardVersion: 1}}
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, AcademicSourceAssociationKind, association.ID, 1, association); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, EvidenceCardKind, card.ID, 1, card)
	})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func readAcademicCardVersion(t *testing.T, f agentFixture, cardID string, version int64) EvidenceCard {
	t.Helper()
	var value EvidenceCard
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		store, err := agentObjectStore(tx)
		if err != nil {
			return err
		}
		row, err := store.ObjectVersion(f.project.ID, EvidenceCardKind, cardID, version)
		if err != nil {
			return err
		}
		return json.Unmarshal(row.Payload, &value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAcademicCatalogUpdateInvalidatesAssociatedCardsAtomically(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	initial, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := seedAcademicEvidenceInvalidation(t, f, "catalog-card", "10.5555/catalog", initial.SourceCatalogVersion)
	input := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "journal", SourceType: "journal", VenueName: "Example", VenueIDs: []string{"1234-5678"}}}}
	_, _, replay, err := f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "catalog-transition", input)
	if err != nil || replay {
		t.Fatalf("catalog update: replay=%v err=%v", replay, err)
	}
	previous := readAcademicCardVersion(t, f, card.ID, 1)
	current := readAcademicCardVersion(t, f, card.ID, 2)
	if previous.Status != "CONFIRMED" || previous.Confirmation == nil || current.Status != "NEEDS_REVIEW" || current.Confirmation != nil || current.InvalidReason != "academic_catalog_changed" {
		t.Fatalf("expected retained confirmation and review transition: old=%+v new=%+v", previous, current)
	}
	_, _, replay, err = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "catalog-transition", input)
	if err != nil || !replay {
		t.Fatalf("idempotent replay: replay=%v err=%v", replay, err)
	}
	var count int64
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, EvidenceCardKind, card.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("unexpected versions after replay count=%d err=%v", count, err)
	}
	events, err := (gormTransaction{f.svc.testDB()}).AuditEvents(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Action == "g2.evidence.invalidate" && event.Target == card.ID && event.ActorID == f.owner.UserID && event.Reason == "academic_catalog_changed" && !event.CreatedAt.IsZero() {
			found = true
		}
	}
	if !found {
		t.Fatal("missing attributed invalidation audit")
	}
}

func TestAcademicCatalogInvalidationRollsBackOnCardWriteFailure(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	initial, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := seedAcademicEvidenceInvalidation(t, f, "rollback-card", "10.5555/rollback", initial.SourceCatalogVersion)
	if err = f.svc.testDB().Exec(`CREATE TRIGGER fail_academic_card_history BEFORE INSERT ON lingdoc_g2_object_versions WHEN NEW.kind = 'evidence_card' AND NEW.version > 1 BEGIN SELECT RAISE(ABORT, 'synthetic invalidation failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	input := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "journal", SourceType: "journal", VenueName: "Example", VenueIDs: []string{"1234-5678"}}}}
	if _, _, _, err = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "catalog-rollback", input); err == nil {
		t.Fatal("expected invalidation failure")
	}
	current, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil || current.Version != 0 || current.SourceCatalogVersion != initial.SourceCatalogVersion {
		t.Fatalf("catalog mutation survived failed invalidation: %+v %v", current, err)
	}
	previous := readAcademicCardVersion(t, f, card.ID, 1)
	if previous.Status != "CONFIRMED" || previous.Confirmation == nil {
		t.Fatalf("confirmed history changed on rollback: %+v", previous)
	}
}

func TestAcademicCatalogInvalidationRollsBackWhenAuditFails(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	initial, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := seedAcademicEvidenceInvalidation(t, f, "audit-rollback", "10.5555/audit", initial.SourceCatalogVersion)
	if err = f.svc.testDB().Exec(`CREATE TRIGGER fail_academic_invalidation_audit BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'g2.evidence.invalidate' BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	input := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "journal", SourceType: "journal", VenueName: "Example", VenueIDs: []string{"1234-5678"}}}}
	if _, _, _, err = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "catalog-audit-rollback", input); err == nil {
		t.Fatal("expected audit write failure")
	}
	current, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil || current.Version != 0 || current.SourceCatalogVersion != initial.SourceCatalogVersion {
		t.Fatalf("catalog mutation survived failed audit: %+v %v", current, err)
	}
	var count int64
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, EvidenceCardKind, card.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("card mutation survived failed audit: count=%d err=%v", count, err)
	}
}

func TestAcademicRetractionInvalidatesOnlyMatchingDOI(t *testing.T) {
	f := newAgentFixture(t, false)
	matched := seedAcademicEvidenceInvalidation(t, f, "retracted-card", "10.5555/retracted", "snapshot")
	unrelated := seedAcademicEvidenceInvalidation(t, f, "other-card", "10.5555/other", "snapshot")
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return invalidateAcademicEvidenceForRetractions(tx, f.owner, f.project.ID, []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/RETRACTED", RetractionStatus: "retracted"}, {Provider: AcademicCrossrefResultProvider, DOI: "10.5555/other", RetractionStatus: "retraction_notice"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if current := readAcademicCardVersion(t, f, matched.ID, 2); current.Status != "NEEDS_REVIEW" || current.InvalidReason != "academic_source_retracted" {
		t.Fatalf("matching DOI not invalidated: %+v", current)
	}
	if current := readAcademicCardVersion(t, f, unrelated.ID, 1); current.Status != "CONFIRMED" {
		t.Fatalf("unrelated DOI was invalidated: %+v", current)
	}
	if _, _, _, err := f.svc.UpdateAcademicSourceCatalog(context.Background(), Actor{TenantID: f.owner.TenantID, UserID: "unknown"}, f.project.ID, "unauthorized-catalog", UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0)}); err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown actor could mutate catalog: %v", err)
	}
}

func TestResearchProgressPersistsRetractionAndCardTransitionTogether(t *testing.T) {
	f := newAgentFixture(t, false)
	card := seedAcademicEvidenceInvalidation(t, f, "run-retracted", "10.5555/paper", "snapshot")
	other := seedAcademicEvidenceInvalidation(t, f, "run-other", "10.5555/other", "snapshot")
	candidate := ImportCandidate{ID: "paper-candidate", ProjectID: f.project.ID, Version: 1, SelectionType: "academic_metadata", DOI: "10.5555/paper", RetractionStatus: "unknown", PolicyStatus: "approved"}
	task := ResearchTask{ID: "retraction-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "retraction-run", Status: "running"}
	run := ResearchRun{ID: "retraction-run", ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running"}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, task.Version, task); err != nil {
			return err
		}
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ImportCandidateKind, candidate.ID, candidate.Version, candidate); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, run.Version, run)
	}); err != nil {
		t.Fatal(err)
	}
	run.Results = []ResearchResult{
		{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/paper", RetractionStatus: "unknown"},
		{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/PAPER", RetractionStatus: "retracted", DuplicateStatus: "duplicate"},
		{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/paper", RetractionStatus: "retracted", DuplicateStatus: "duplicate"},
	}
	if err := f.svc.persistResearchProgress(context.Background(), f.owner, f.project.ID, &run); err != nil {
		t.Fatal(err)
	}
	if current := readAcademicCardVersion(t, f, card.ID, 2); current.Status != "NEEDS_REVIEW" || current.InvalidReason != "academic_source_retracted" {
		t.Fatalf("retraction not propagated: %+v", current)
	}
	if current := readAcademicCardVersion(t, f, other.ID, 1); current.Status != "CONFIRMED" {
		t.Fatalf("unrelated source invalidated: %+v", current)
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		known, err := knownAcademicRetraction(tx, f.project.ID, "10.5555/PAPER")
		if err != nil || !known {
			t.Fatalf("retraction observation was not retained: known=%v err=%v", known, err)
		}
		current, _, err := loadStoredObject[ImportCandidate](tx, f.project.ID, ImportCandidateKind, candidate.ID)
		if err != nil {
			return err
		}
		if current.Version != 2 || current.RetractionStatus != "retracted" || academicCandidatePolicyEligible(current.PolicyStatus, current.RetractionStatus, current.DuplicateStatus) {
			t.Fatalf("retracted candidate remained eligible: %+v", current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.persistResearchProgress(context.Background(), f.owner, f.project.ID, &run); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, EvidenceCardKind, card.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("repeat progress duplicated invalidation: count=%d err=%v", count, err)
	}
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, ImportCandidateKind, candidate.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("repeat progress duplicated candidate version: count=%d err=%v", count, err)
	}
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ?", f.project.ID, academicRetractionKind).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("repeat progress duplicated retraction observation: count=%d err=%v", count, err)
	}
}

func TestAssociationRevocationInvalidatesAssociatedCard(t *testing.T) {
	f := newAgentFixture(t, false)
	card := seedAcademicEvidenceInvalidation(t, f, "revoked-card", "10.5555/paper", "snapshot")
	input := ConfirmationInput{VersionExpectation: f.expectation(1), Confirmed: true, Reason: "synthetic review revoked"}
	if _, _, _, err := f.svc.RevokeAcademicSourceAssociation(context.Background(), f.owner, f.project.ID, "revoked-card-association", "revoke-association", input); err != nil {
		t.Fatal(err)
	}
	if current := readAcademicCardVersion(t, f, card.ID, 2); current.Status != "NEEDS_REVIEW" || current.InvalidReason != "academic_association_revoked" {
		t.Fatalf("revoked association not propagated: %+v", current)
	}
}

func TestResearchRetractionRollbackPreservesRunAndEvidence(t *testing.T) {
	f := newAgentFixture(t, false)
	card := seedAcademicEvidenceInvalidation(t, f, "run-rollback", "10.5555/rollback", "snapshot")
	candidate := ImportCandidate{ID: "rollback-candidate", ProjectID: f.project.ID, Version: 1, SelectionType: "academic_metadata", DOI: "10.5555/rollback", RetractionStatus: "unknown", PolicyStatus: "approved"}
	task := ResearchTask{ID: "rollback-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "rollback-run", Status: "running"}
	run := ResearchRun{ID: "rollback-run", ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running"}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, task.Version, task); err != nil {
			return err
		}
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ImportCandidateKind, candidate.ID, candidate.Version, candidate); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, run.Version, run)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec(`CREATE TRIGGER fail_retraction_card_history BEFORE INSERT ON lingdoc_g2_object_versions WHEN NEW.kind = 'evidence_card' AND NEW.version > 1 BEGIN SELECT RAISE(ABORT, 'synthetic invalidation failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	run.Results = []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: "10.5555/rollback", RetractionStatus: "retracted"}}
	if err := f.svc.persistResearchProgress(context.Background(), f.owner, f.project.ID, &run); err == nil {
		t.Fatal("expected retraction transaction to fail")
	}
	if old := readAcademicCardVersion(t, f, card.ID, 1); old.Status != "CONFIRMED" || old.Confirmation == nil {
		t.Fatalf("old evidence changed after rollback: %+v", old)
	}
	var count int64
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, ResearchRunKind, run.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed retraction wrote a run version: count=%d err=%v", count, err)
	}
	if err := f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id = ? AND kind = ? AND id = ?", f.project.ID, ImportCandidateKind, candidate.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed retraction wrote a candidate version: count=%d err=%v", count, err)
	}
	if err := f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, academicRetractionKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed retraction wrote an observation: count=%d err=%v", count, err)
	}
}

func TestKnownRetractionBlocksFutureUnknownAndProjectsIdempotentReplays(t *testing.T) {
	f, run, _ := setupAcademicResearchRun(t)
	ctx := context.Background()
	input := academicCandidateInput(f, run)
	raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "before-known-retraction", input)
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	confirm := ConfirmationInput{VersionExpectation: f.expectation(candidate.Version), Confirmed: true, Reason: "synthetic review"}
	if _, _, _, err = f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, candidate.ID, "confirm-before-retraction", confirm); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := lockAcademicEvidenceProject(tx, f.owner, f.project.ID); err != nil {
			return err
		}
		return invalidateAcademicEvidenceForRetractions(tx, f.owner, f.project.ID, []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: candidate.DOI, RetractionStatus: "retracted"}})
	}); err != nil {
		t.Fatal(err)
	}
	// An earlier search's lack of a notice is not a reason to resurrect a DOI.
	newRun := run
	newRun.ID = "later-search-unknown"
	newRun.Version = 1
	if err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, newRun.ID, 1, newRun)
	}); err != nil {
		t.Fatal(err)
	}
	newInput := input
	newInput.RunID = newRun.ID
	if _, _, _, err = f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "later-unknown-candidate", newInput); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("future unknown resurrected retracted DOI: %v", err)
	}
	// A repeated request may return an error or the current retracted record;
	// it must not advertise the stale originally-approved response as current.
	replayed, _, replay, replayErr := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "before-known-retraction", input)
	if replayErr == nil {
		current := decodeAgentJSON[ImportCandidate](t, replayed, nil)
		if !replay || current.ID != candidate.ID || current.RetractionStatus != "retracted" {
			t.Fatalf("create replay returned stale eligible candidate: replay=%v value=%+v", replay, current)
		}
	}
	replayed, _, replay, replayErr = f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, candidate.ID, "confirm-before-retraction", confirm)
	if replayErr == nil {
		current := decodeAgentJSON[ImportCandidate](t, replayed, nil)
		if !replay || current.RetractionStatus != "retracted" {
			t.Fatalf("confirmation replay returned stale eligible candidate: replay=%v value=%+v", replay, current)
		}
	}
}

func TestConcurrentRetractionAndCandidateCreationCannotLeaveEligibleCandidate(t *testing.T) {
	f, run, _ := setupAcademicResearchRun(t)
	input := academicCandidateInput(f, run)
	updated := run
	updated.Results = append(append([]ResearchResult(nil), run.Results...), ResearchResult{ID: "retraction-result", Provider: AcademicCrossrefResultProvider, DOI: run.Results[0].DOI, RetractionStatus: "retracted"})
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var progressErr, createErr error
	go func() {
		defer wg.Done()
		<-start
		progressErr = f.svc.persistResearchProgress(context.Background(), f.owner, f.project.ID, &updated)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _, _, createErr = f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "racing-create-candidate", input)
	}()
	close(start)
	wg.Wait()
	if progressErr != nil {
		t.Fatalf("retraction must commit: %v (create error: %v)", progressErr, createErr)
	}
	if createErr != nil && !errors.Is(createErr, ErrEvidenceIncomplete) && !errors.Is(createErr, ErrVersionConflict) {
		t.Fatalf("unexpected candidate create error: %v", createErr)
	}
	items, err := f.svc.ListImportCandidates(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range items {
		if candidate.DOI == run.Results[0].DOI && candidate.RetractionStatus != "retracted" {
			t.Fatalf("eligible candidate survived retraction race: %+v", candidate)
		}
	}
}

func TestKnownRetractionBlocksAssociationAndReplayAndMarksHistory(t *testing.T) {
	f, candidate := academicAssociationFixture(t)
	ctx := context.Background()
	source := f.source.sources["source-1"]
	input := ConfirmAcademicSourceAssociationInput{VersionExpectation: f.expectation(candidate.Version), CandidateID: candidate.ID, SourceID: source.SourceID, ExpectedAssetRevision: source.AssetRevision, ExpectedQuotedTextHash: source.QuotedTextHash, ExpectedLocator: source.Locator, Confirmed: true, Reason: "synthetic review"}
	raw, _, _, err := f.svc.ConfirmAcademicSourceAssociation(ctx, f.owner, f.project.ID, "association-before-known-retraction", input)
	association := decodeAgentJSON[AcademicSourceAssociation](t, raw, err)
	// Seed only the durable observation: candidate metadata intentionally stays
	// unchanged to prove each operation checks the observation independently.
	if err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, f.project.ID, academicRetractionKind, academicRetractionID(candidate.DOI), 1, academicRetractionRecord{DOI: candidate.DOI})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = f.svc.ConfirmAcademicSourceAssociation(ctx, f.owner, f.project.ID, "association-after-known-retraction", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("new association accepted known retraction: %v", err)
	}
	if _, _, _, err = f.svc.ConfirmAcademicSourceAssociation(ctx, f.owner, f.project.ID, "association-before-known-retraction", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("association replay ignored known retraction: %v", err)
	}
	views, err := f.svc.ListAcademicSourceAssociations(ctx, f.owner, f.project.ID)
	if err != nil || len(views) != 1 || views[0].ID != association.ID || views[0].ReviewState != "needs_review" {
		t.Fatalf("historical association did not become needs_review: %+v %v", views, err)
	}
}
