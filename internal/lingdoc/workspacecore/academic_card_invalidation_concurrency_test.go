package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// Both operations enter through their actual service methods; SQLite can
// serialize or reject a racing write, but it must never commit an eligible
// old-catalog or retracted card after the invalidation commits.
func TestConcurrentAcademicCardSaveAndInvalidation(t *testing.T) {
	for _, kind := range []string{"catalog", "retraction"} {
		t.Run(kind, func(t *testing.T) {
			f := newAgentFixture(t, false)
			ctx := context.Background()
			anchor := f.anchor(t, "source-1", "race-card-anchor")
			association := seedDraftAssociation(t, f, "race-card-association", f.source.sources["source-1"])
			input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
			catalogInput := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "new-journal", SourceType: "journal", VenueName: "New journal", VenueIDs: []string{"1234-5678"}}}}
			task := ResearchTask{ID: "race-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "race-run", Status: "running"}
			run := ResearchRun{ID: "race-run", ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: association.DOI, RetractionStatus: "retracted"}}}
			if kind == "retraction" {
				if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
					if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, task.Version, task); err != nil {
						return err
					}
					return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, run.Version, run)
				}); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			var saveErr, invalidateErr error
			var saved json.RawMessage
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				saved, _, _, saveErr = f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "race-card", input)
			}()
			go func() {
				defer wg.Done()
				<-start
				if kind == "catalog" {
					_, _, _, invalidateErr = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "race-catalog", catalogInput)
				} else {
					invalidateErr = f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run)
				}
			}()
			close(start)
			wg.Wait()
			if invalidateErr != nil {
				t.Fatalf("invalidation failed: %v (save: %v)", invalidateErr, saveErr)
			}
			committedUnreadable := errors.Is(saveErr, ErrAcademicCardSaveCommittedUnreadable)
			if saveErr != nil && !errors.Is(saveErr, ErrEvidenceIncomplete) && !errors.Is(saveErr, ErrVersionConflict) && !committedUnreadable {
				t.Fatalf("unexpected save failure: %v", saveErr)
			}
			var card EvidenceCard
			if saveErr == nil {
				if err := json.Unmarshal(saved, &card); err != nil {
					t.Fatal(err)
				}
			}
			err := f.svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
				store, err := agentObjectStore(tx)
				if err != nil {
					return err
				}
				rows, err := store.Objects(f.project.ID, EvidenceCardKind)
				if err != nil {
					return err
				}
				if (saveErr == nil || committedUnreadable) && len(rows) != 1 {
					t.Fatalf("successful save left %d cards", len(rows))
				}
				if saveErr != nil && !committedUnreadable && len(rows) != 0 {
					t.Fatalf("failed save left %d cards", len(rows))
				}
				for _, row := range rows {
					var current EvidenceCard
					if err := json.Unmarshal(row.Payload, &current); err != nil {
						return err
					}
					if (!committedUnreadable && current.ID != card.ID) || current.Status != "NEEDS_REVIEW" || current.Confirmation != nil {
						t.Fatalf("eligible card survived %s: %+v", kind, current)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAcademicCardSaveReplayDoesNotReturnStaleCatalogCard(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	anchor := f.anchor(t, "source-1", "replay-card-anchor")
	association := seedDraftAssociation(t, f, "replay-card-association", f.source.sources["source-1"])
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
	if _, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-card", input); err != nil {
		t.Fatal(err)
	}
	update := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "updated-journal", SourceType: "journal", VenueName: "Updated journal", VenueIDs: []string{"1234-5678"}}}}
	if _, _, _, err := f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "replay-catalog-update", update); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-card", input)
	if err == nil {
		var card EvidenceCard
		if err := json.Unmarshal(raw, &card); err != nil {
			t.Fatal(err)
		}
		if card.Status != "NEEDS_REVIEW" {
			t.Fatalf("replay returned stale eligible card after catalog update: %+v", card)
		}
	}
}

func TestAcademicCardSaveReplayDoesNotReturnRetractedCard(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	anchor := f.anchor(t, "source-1", "replay-retraction-anchor")
	association := seedDraftAssociation(t, f, "replay-retraction-association", f.source.sources["source-1"])
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
	if _, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-retracted-card", input); err != nil {
		t.Fatal(err)
	}
	task := ResearchTask{ID: "replay-retraction-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "replay-retraction-run", Status: "running"}
	run := ResearchRun{ID: task.LatestRunID, ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: association.DOI, RetractionStatus: "retracted"}}}
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, task.Version, task); err != nil {
			return err
		}
		return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, run.Version, run)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-retracted-card", input)
	if err == nil {
		var card EvidenceCard
		if err := json.Unmarshal(raw, &card); err != nil {
			t.Fatal(err)
		}
		if card.Status != "NEEDS_REVIEW" {
			t.Fatalf("replay returned eligible card after retraction: %+v", card)
		}
	}
}

func TestAcademicCardSaveReplayRequiresCurrentPermissions(t *testing.T) {
	for _, revoked := range []string{"tenant_role", "source"} {
		t.Run(revoked, func(t *testing.T) {
			f := newAgentFixture(t, false)
			ctx := context.Background()
			anchor := f.anchor(t, "source-1", "replay-permission-anchor")
			association := seedDraftAssociation(t, f, "replay-permission-association", f.source.sources["source-1"])
			input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
			if _, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-permission-card", input); err != nil {
				t.Fatal(err)
			}
			if _, _, replay, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-permission-card", input); err != nil || !replay {
				t.Fatalf("control replay: replay=%v err=%v", replay, err)
			}
			if revoked == "tenant_role" {
				if err := f.svc.testDB().Exec("UPDATE tenant_members SET role='viewer' WHERE tenant_id=? AND user_id=?", f.owner.TenantID, f.owner.UserID).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				f.source.denied["source-1"] = true
			}
			raw, _, _, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "replay-permission-card", input)
			if err == nil || len(raw) != 0 {
				t.Fatalf("revoked %s returned card: %s (err=%v)", revoked, raw, err)
			}
		})
	}
}
