package workspacecore

import (
	"context"
	"errors"
	"testing"
)

func TestAcademicRetractionNoticeCannotBecomeCandidateOrBeConfirmed(t *testing.T) {
	f, run, _ := setupAcademicResearchRun(t)
	ctx := context.Background()
	input := academicCandidateInput(f, run)
	raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "notice-candidate-seed", input)
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		value, old, err := loadStoredObject[ResearchRun](tx, f.project.ID, ResearchRunKind, run.ID)
		if err != nil {
			return err
		}
		value.Results[0].RetractionStatus = "retraction_notice"
		value.Version++
		if err = storeStoredObject(tx, old, f.project.ID, ResearchRunKind, value.ID, value.Version, value); err != nil {
			return err
		}
		current, old, err := loadStoredObject[ImportCandidate](tx, f.project.ID, ImportCandidateKind, candidate.ID)
		if err != nil {
			return err
		}
		current.RetractionStatus = "retraction_notice"
		current.Version++
		return storeStoredObject(tx, old, f.project.ID, ImportCandidateKind, current.ID, current.Version, current)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "notice-candidate-denied", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("notice import creation was not blocked: %v", err)
	}
	_, _, _, err = f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, candidate.ID, "notice-confirm-denied", ConfirmationInput{VersionExpectation: f.expectation(candidate.Version + 1), Confirmed: true, Reason: "test"})
	if !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("notice confirmation was not blocked: %v", err)
	}
}
