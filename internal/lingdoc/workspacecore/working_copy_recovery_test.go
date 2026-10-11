package workspacecore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestLegacyAdoptionRecoversOnlyUntouchedWorkingCopy(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t, t.TempDir()+"/recovery.db")
			a := Actor{TenantID: 33, UserID: "owner"}
			seedTenantMember(t, s, a)
			raw, _, _, err := s.CreateProject(ctx, a, "create-recovery", CreateProjectInput{Name: "recovery", TemplateID: "template-demo"})
			if err != nil {
				t.Fatal(err)
			}
			p := asProject(t, raw)
			_, _, _, err = s.SaveSpec(ctx, a, p.ID, "spec-recovery", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "test", "research_goal": "test"}})
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = s.ActivateProject(ctx, a, p.ID, "activate-recovery", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
			if err != nil {
				t.Fatal(err)
			}
			chapters, err := s.ListChapters(ctx, a, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			id := chapters[0].ID
			if dirty {
				_, _, _, err = s.SaveWorkingCopy(ctx, a, p.ID, id, "draft-recovery", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "unsaved human draft", SourceIDs: []string{}})
				if err != nil {
					t.Fatal(err)
				}
			}
			// Reproduce the pre-fix T11 writer: append formal chapter without draft sync.
			err = s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
				old, e := tx.Chapter(p.ID, id)
				if e != nil {
					return e
				}
				next := old
				v := "adopted"
				next.CurrentVersionID = &v
				next.BodyMarkdown = "adopted formal [[source:source-one]]"
				next.SourceIDs = []string{"source-one"}
				next.ReviewItems = []ReviewItem{{ID: "r", Statement: "check", OriginCandidateID: "candidate"}}
				return tx.AppendChapter(old, next, 1)
			})
			if err != nil {
				t.Fatal(err)
			}
			db, err := s.repository.(*GORMRepository).db.DB()
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			s.sources = recoveryDBSourcePolicy{db: db}
			limited, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			copy, err := s.GetWorkingCopy(limited, a, p.ID, id)
			if dirty {
				if !errors.Is(err, ErrVersionConflict) {
					t.Fatalf("dirty draft was overwritten: %v", err)
				}
				return
			}
			if err != nil || copy.BodyMarkdown != "adopted formal [[source:source-one]]" || copy.BaseChapterVersionID == nil || *copy.BaseChapterVersionID != "adopted" || copy.WorkingCopyRevision != 2 || len(copy.ReviewItems) != 1 {
				t.Fatalf("recovered=%+v err=%v", copy, err)
			}
			again, err := s.GetWorkingCopy(ctx, a, p.ID, id)
			if err != nil || again.WorkingCopyRevision != copy.WorkingCopyRevision {
				t.Fatalf("read repeated recovery: %+v %v", again, err)
			}
		})
	}
}

type recoveryDBSourcePolicy struct{ db *sql.DB }

func (p recoveryDBSourcePolicy) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	var n int
	return p.db.QueryRowContext(ctx, "SELECT 1").Scan(&n)
}
