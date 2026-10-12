package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// A semantic project change invalidates confirmation, but must not strand an
// otherwise current draft on an obsolete spec or silently replace human text.
func TestSpecChangeRefreshesDraftBasisWithoutReplacingText(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "formal_equal", true: "independent_draft"}[dirty], func(t *testing.T) {
			s, a, pid, cid, _ := operationSourceFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			copy, err := s.GetWorkingCopy(ctx, a, pid, cid)
			if err != nil {
				t.Fatal(err)
			}
			raw, _, _, err := s.SaveWorkingCopy(ctx, a, pid, cid, "spec-initial-copy", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BaseChapterVersionID: copy.BaseChapterVersionID, BodyMarkdown: "formal plan [[source:one]]", SourceIDs: []string{"one"}})
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			_, _, _, err = s.CommitWorkingCopy(ctx, a, pid, cid, "spec-initial-commit", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision})
			if err != nil {
				t.Fatal(err)
			}
			copy, err = s.GetWorkingCopy(ctx, a, pid, cid)
			if err != nil {
				t.Fatal(err)
			}
			formal, err := s.ListChapters(ctx, a, pid)
			if err != nil {
				t.Fatal(err)
			}
			if dirty {
				raw, _, _, err = s.SaveWorkingCopy(ctx, a, pid, cid, "spec-independent-draft", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BaseChapterVersionID: copy.BaseChapterVersionID, BodyMarkdown: "independent human draft [[source:one]]", SourceIDs: []string{"one"}, CitationUsages: copy.CitationUsages})
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(raw, &copy); err != nil {
					t.Fatal(err)
				}
			}
			before := copy
			p, err := s.GetProject(ctx, a, pid)
			if err != nil {
				t.Fatal(err)
			}
			raw, _, _, err = s.CreateChangeSet(ctx, a, pid, "spec-change-create", CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "change research goal", Fields: map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: p.Spec["research_goal"] + " updated"}}, AffectedChapterIDs: []string{cid}})
			if err != nil {
				t.Fatal(err)
			}
			var change ChangeSet
			if err = json.Unmarshal(raw, &change); err != nil {
				t.Fatal(err)
			}
			_, _, _, err = s.ApplyChangeSet(ctx, a, pid, change.ID, "spec-change-apply")
			if err != nil {
				t.Fatal(err)
			}
			copy, err = s.GetWorkingCopy(ctx, a, pid, cid)
			if err != nil {
				t.Fatal(err)
			}
			if copy.SpecRevision != 2 || copy.WorkingCopyRevision != before.WorkingCopyRevision+1 {
				t.Fatalf("draft basis not refreshed: before=%+v after=%+v", before, copy)
			}
			if copy.BodyMarkdown != before.BodyMarkdown || !sameVersion(copy.BaseChapterVersionID, before.BaseChapterVersionID) || !reflect.DeepEqual(copy.SourceIDs, before.SourceIDs) || !reflect.DeepEqual(copy.ReviewItems, before.ReviewItems) || !reflect.DeepEqual(copy.CitationUsages, before.CitationUsages) {
				t.Fatalf("human draft or provenance replaced: before=%+v after=%+v", before, copy)
			}
			again, err := s.GetWorkingCopy(ctx, a, pid, cid)
			if err != nil || again.WorkingCopyRevision != copy.WorkingCopyRevision {
				t.Fatalf("refresh repeated: %+v %v", again, err)
			}
			afterFormal, err := s.ListChapters(ctx, a, pid)
			if err != nil {
				t.Fatal(err)
			}
			for i, ch := range afterFormal {
				if ch.BodyMarkdown != formal[i].BodyMarkdown || !sameVersion(ch.CurrentVersionID, formal[i].CurrentVersionID) || ch.ConfirmationValid {
					t.Fatalf("read refresh changed formal/confirmation: %+v", ch)
				}
			}
			stale := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: before.WorkingCopyRevision, BaseChapterVersionID: before.BaseChapterVersionID, BodyMarkdown: "stale writer [[source:one]]", SourceIDs: []string{"one"}}
			_, _, _, err = s.SaveWorkingCopy(ctx, a, pid, cid, "spec-old-client", stale)
			if !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("obsolete context write accepted: %v", err)
			}
			stale.ExpectedSpecRevision = 2
			_, _, _, err = s.SaveWorkingCopy(ctx, a, pid, cid, "spec-old-revision", stale)
			if !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("obsolete copy revision accepted: %v", err)
			}
			_, _, _, err = s.SaveWorkingCopy(ctx, a, pid, cid, "spec-current-save", SaveWorkingCopyInput{ExpectedSpecRevision: 2, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BaseChapterVersionID: copy.BaseChapterVersionID, BodyMarkdown: copy.BodyMarkdown + "; reviewed", SourceIDs: copy.SourceIDs, CitationUsages: copy.CitationUsages})
			if err != nil {
				t.Fatalf("current draft cannot resume editing: %v", err)
			}
		})
	}
}
