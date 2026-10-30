package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type cardSavePostCommitReader struct {
	*testSourceReader
	f           agentFixture
	onCommitted func() error
	once        sync.Once
	err         error
}

func (r *cardSavePostCommitReader) ReadResearchSource(ctx context.Context, actor Actor, projectID, sourceID string) (ResearchSource, error) {
	var count int64
	if err := r.f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id=? AND kind=?", projectID, EvidenceCardKind).Count(&count).Error; err != nil {
		return ResearchSource{}, err
	}
	if count > 0 {
		r.once.Do(func() { r.err = r.onCommitted() })
		if r.err != nil {
			return ResearchSource{}, r.err
		}
	}
	return r.testSourceReader.ReadResearchSource(ctx, actor, projectID, sourceID)
}

func TestAcademicCardSavePostCommitRevocation(t *testing.T) {
	for _, mode := range []string{"tenant", "source", "source_policy", "retraction"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newAgentFixture(t, false)
			anchor := f.anchor(t, "source-1", "postcommit-save-anchor")
			association := seedDraftAssociation(t, f, "postcommit-save-association", f.source.sources["source-1"])
			input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: association.ID, Version: association.Version}}, Confirmed: true}
			task := ResearchTask{ID: "postcommit-task", ProjectID: f.project.ID, Version: 1, ProviderID: AcademicCrossrefProviderID, LatestRunID: "postcommit-run", Status: "running"}
			run := ResearchRun{ID: task.LatestRunID, ProjectID: f.project.ID, Version: 1, TaskID: task.ID, Basis: versionBasisFor(f.project, Chapter{}), Status: "running", Results: []ResearchResult{{Provider: AcademicCrossrefResultProvider, DOI: association.DOI, RetractionStatus: "retracted"}}}
			if mode == "retraction" {
				if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
					if err := storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchTaskKind, task.ID, 1, task); err != nil {
						return err
					}
					return storeStoredObject(tx, StoredObject{}, f.project.ID, ResearchRunKind, run.ID, 1, run)
				}); err != nil {
					t.Fatal(err)
				}
			}
			reader := &cardSavePostCommitReader{testSourceReader: f.source, f: f}
			reader.onCommitted = func() error {
				switch mode {
				case "tenant":
					return f.svc.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", f.owner.TenantID, f.owner.UserID).Error
				case "source":
					f.source.denied["source-1"] = true
					return nil
				case "source_policy":
					f.policy.verdict = ErrSourceUnavailable
					return nil
				default:
					return f.svc.persistResearchProgress(ctx, f.owner, f.project.ID, &run)
				}
			}
			f.svc.agentRuntime.Sources = reader
			body, _, replay, err := f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "postcommit-save", input)
			if !errors.Is(err, ErrAcademicCardSaveCommittedUnreadable) || replay || len(body) != 0 {
				t.Fatalf("post-commit change disclosed result or hid commit: replay=%v body=%s err=%v", replay, body, err)
			}
			var rows []struct {
				ID          string
				PayloadJSON string
			}
			if err = f.svc.testDB().Table("lingdoc_g2_objects").Select("id, payload_json").Where("project_id=? AND kind=?", f.project.ID, EvidenceCardKind).Scan(&rows).Error; err != nil || len(rows) != 1 {
				t.Fatalf("saved card is not present exactly once: %v %v", rows, err)
			}
			var current EvidenceCard
			if err = json.Unmarshal([]byte(rows[0].PayloadJSON), &current); err != nil {
				t.Fatal(err)
			}
			if mode == "retraction" && current.Status != "NEEDS_REVIEW" {
				t.Fatalf("retracted card still eligible: %s", current.Status)
			}
			if mode == "retraction" {
				return
			}
			f.source.denied["source-1"] = false
			f.policy.verdict = nil
			if mode == "tenant" {
				if err = f.svc.testDB().Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?", f.owner.TenantID, f.owner.UserID).Error; err != nil {
					t.Fatal(err)
				}
			}
			body, _, replay, err = f.svc.SaveEvidenceCard(ctx, f.owner, f.project.ID, "postcommit-save", input)
			if err != nil || !replay {
				t.Fatalf("restored retry was not idempotent: replay=%v err=%v", replay, err)
			}
			var recovered EvidenceCard
			if err = json.Unmarshal(body, &recovered); err != nil || recovered.ID != current.ID || recovered.Version != current.Version {
				t.Fatalf("wrong replay result: %v", err)
			}
			var count int64
			if err = f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id=? AND kind=? AND id=?", f.project.ID, EvidenceCardKind, current.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("replay duplicated card history: %d %v", count, err)
			}
		})
	}
}
