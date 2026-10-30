package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// Block the first post-commit chapter read until a competing access change is
// committed; the material read used by the write remains unchanged.
type postCommitChapterReader struct {
	syntheticAcademicMaterialReader
	f                 agentFixture
	cardID, chapterID string
	committed         chan struct{}
	revoked           chan struct{}
	closed            atomic.Bool
	once              sync.Once
}

func (r *postCommitChapterReader) ReadResearchSourceForChapter(ctx context.Context, actor Actor, projectID, chapterID, sourceID string) (ResearchSource, error) {
	if chapterID == r.chapterID {
		var row struct {
			PayloadJSON string `gorm:"column:payload_json"`
		}
		if err := r.f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id=? AND kind=? AND id=?", projectID, EvidenceCardKind, r.cardID).Take(&row).Error; err == nil {
			var card EvidenceCard
			if json.Unmarshal([]byte(row.PayloadJSON), &card) == nil && card.Status == "CONFIRMED" {
				r.once.Do(func() { close(r.committed); <-r.revoked })
			}
		}
		if r.closed.Load() {
			return ResearchSource{}, ErrSourceUnavailable
		}
	}
	return r.testSourceReader.ReadResearchSourceForChapter(ctx, actor, projectID, chapterID, sourceID)
}

func TestAcademicConfirmationPostCommitRevocationAndReplay(t *testing.T) {
	for _, mode := range []string{"chapter_source", "tenant_membership"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, card, _ := academicSnapshotReadFixture(t, false)
			chapters, err := f.svc.ListChapters(ctx, f.owner, f.project.ID)
			if err != nil || len(chapters) < 2 {
				t.Fatalf("need two chapters: %v", err)
			}
			scope := sortedUniqueIDs([]string{chapters[0].ID, chapters[1].ID})
			err = f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
				current, old, e := loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
				if e != nil {
					return e
				}
				current.Version++
				current.ScopeChapterIDs = scope
				card = current
				return storeStoredObject(tx, old, f.project.ID, EvidenceCardKind, card.ID, card.Version, card)
			})
			if err != nil {
				t.Fatal(err)
			}
			reader := &postCommitChapterReader{syntheticAcademicMaterialReader: syntheticAcademicMaterialReader{testSourceReader: f.source}, f: f, cardID: card.ID, chapterID: scope[1], committed: make(chan struct{}), revoked: make(chan struct{})}
			f.svc.agentRuntime.Sources = reader
			accessErr := make(chan error, 1)
			go func() {
				<-reader.committed
				var e error
				if mode == "chapter_source" {
					reader.closed.Store(true)
				} else {
					e = f.svc.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", f.owner.TenantID, f.owner.UserID).Error
				}
				accessErr <- e
				close(reader.revoked)
			}()
			input := ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "synthetic human review"}
			body, _, replay, err := f.svc.ConfirmEvidenceCard(ctx, f.owner, f.project.ID, card.ID, "postcommit-confirm", input)
			if !errors.Is(err, ErrAcademicConfirmationCommittedUnreadable) || replay || len(body) != 0 {
				t.Fatalf("post-commit revocation disclosed result or lost commit semantics: replay=%v err=%v", replay, err)
			}
			if err = <-accessErr; err != nil {
				t.Fatal(err)
			}
			current := readAcademicCardVersion(t, f, card.ID, card.Version+1)
			if current.Status != "CONFIRMED" || current.Confirmation == nil {
				t.Fatal("confirmation did not remain committed")
			}
			if mode == "chapter_source" {
				reader.closed.Store(false)
			} else if err = f.svc.testDB().Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?", f.owner.TenantID, f.owner.UserID).Error; err != nil {
				t.Fatal(err)
			}
			body, _, replay, err = f.svc.ConfirmEvidenceCard(ctx, f.owner, f.project.ID, card.ID, "postcommit-confirm", input)
			if err != nil || !replay {
				t.Fatalf("restored replay failed: replay=%v err=%v", replay, err)
			}
			var recovered EvidenceCard
			if err = json.Unmarshal(body, &recovered); err != nil || recovered.Version != current.Version {
				t.Fatalf("unexpected recovered card version: %v", err)
			}
			var count int64
			if err = f.svc.testDB().Table("lingdoc_g2_object_versions").Where("project_id=? AND kind=? AND id=?", f.project.ID, EvidenceCardKind, card.ID).Count(&count).Error; err != nil || count != card.Version+1 {
				t.Fatalf("replay wrote duplicate history: %d %v", count, err)
			}
		})
	}
}
