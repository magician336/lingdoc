package workspacecore

import (
	"context"
	"testing"
)

// An imported academic metadata candidate must surface a durable retraction
// observation at read time even when no explicit invalidation transaction ran
// against the candidate object itself.
func TestListImportCandidatesRevalidatesKnownRetraction(t *testing.T) {
	f, run, _ := setupAcademicResearchRun(t)
	ctx := context.Background()
	input := academicCandidateInput(f, run)
	raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "list-retraction-candidate", input)
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	confirm := ConfirmationInput{VersionExpectation: f.expectation(candidate.Version), Confirmed: true, Reason: "synthetic review"}
	if _, _, _, err = f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, candidate.ID, "confirm-list-retraction", confirm); err != nil {
		t.Fatal(err)
	}
	// Seed only the durable observation: candidate metadata intentionally stays
	// unchanged to prove the read path rechecks the observation independently.
	if err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, f.project.ID, academicRetractionKind, academicRetractionID(candidate.DOI), 1, academicRetractionRecord{DOI: candidate.DOI})
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := f.svc.ListImportCandidates(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range listed {
		if got.ID != candidate.ID {
			continue
		}
		if got.RetractionStatus != "retracted" || got.Status != "restricted" {
			t.Fatalf("imported candidate survived known retraction read-time revalidation: %+v", got)
		}
		if got.Locator != "" || got.QuotedTextHash != "" || got.Purpose != "" {
			t.Fatalf("restricted candidate leaked source location metadata: %+v", got)
		}
		return
	}
	t.Fatalf("candidate missing from list")
}

// An imported academic metadata candidate whose catalog version no longer
// matches the current catalog must surface as restricted at read time.
func TestListImportCandidatesRevalidatesChangedCatalog(t *testing.T) {
	f, run, catalog := setupAcademicResearchRun(t)
	ctx := context.Background()
	input := academicCandidateInput(f, run)
	raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "list-catalog-candidate", input)
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	confirm := ConfirmationInput{VersionExpectation: f.expectation(candidate.Version), Confirmed: true, Reason: "synthetic review"}
	if _, _, _, err = f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, candidate.ID, "confirm-list-catalog", confirm); err != nil {
		t.Fatal(err)
	}
	updateAcademicCatalogForTest(t, f, catalog, "changed-before-list")
	listed, err := f.svc.ListImportCandidates(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range listed {
		if got.ID != candidate.ID {
			continue
		}
		if got.Status != "restricted" {
			t.Fatalf("imported candidate survived catalog change read-time revalidation: %+v", got)
		}
		return
	}
	t.Fatalf("candidate missing from list")
}
