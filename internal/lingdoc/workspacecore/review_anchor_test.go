package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"path/filepath"
	"testing"
	"time"
)

func anchorFixture(t *testing.T) (*Service, Actor, Project, string, WorkingCopy) {
	t.Helper()
	s := testStore(t, filepath.Join(t.TempDir(), "anchors.db"))
	ctx := context.Background()
	a := Actor{TenantID: 29, UserID: "writer"}
	seedTenantMember(t, s, a)
	raw, _, _, err := s.CreateProject(ctx, a, "anchor-create", CreateProjectInput{Name: "anchor test", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := asProject(t, raw)
	_, _, _, err = s.SaveSpec(ctx, a, p.ID, "anchor-spec", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "synthetic", "research_goal": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.ActivateProject(ctx, a, p.ID, "anchor-activate", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := s.ListChapters(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := chapters[0].ID
	copy, err := s.GetWorkingCopy(ctx, a, p.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = s.ApplyRewrite(ctx, a, p.ID, id, "anchor-seed", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BodyMarkdown: "甲😀引用。后文", SourceIDs: []string{}}, []ReviewItem{{ID: "r1", Statement: "核实引用", OriginCandidateID: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return s, a, p, id, copy
}
func TestAnchorCASReplayRebaseCommitAndHistory(t *testing.T) {
	s, a, p, id, copy := anchorFixture(t)
	ctx := context.Background()
	input := SetReviewAnchorInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Anchor: &reviewanchor.Selection{StartUTF16: 3, EndUTF16: 5, QuoteMarkdown: "引用"}}
	raw, _, replay, err := s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "anchor-attach-test", input)
	if err != nil || replay {
		t.Fatalf("attach: %v %v", err, replay)
	}
	replayRaw, _, replay, err := s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "anchor-attach-test", input)
	if err != nil || !replay || string(raw) != string(replayRaw) {
		t.Fatalf("replay %v %v", err, replay)
	}
	if _, _, _, err = s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "stale-cas", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale accepted %v", err)
	}
	if err = json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	if copy.ReviewItems[0].Anchor.StartUTF16 != 3 || copy.ReviewItems[0].Anchor.WorkingCopyRevision != copy.WorkingCopyRevision {
		t.Fatal("anchor provenance missing")
	}
	save := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BodyMarkdown: "前文\n" + copy.BodyMarkdown, SourceIDs: []string{}}
	raw, _, _, err = s.SaveWorkingCopy(ctx, a, p.ID, id, "anchor-move-test", save)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &copy)
	if copy.ReviewItems[0].Anchor.Status != "located" || copy.ReviewItems[0].Anchor.StartUTF16 != 6 {
		t.Fatalf("exact quote lost: %+v", copy)
	}
	raw, _, _, err = s.CommitWorkingCopy(ctx, a, p.ID, id, "anchor-commit-test", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision})
	if err != nil {
		t.Fatal(err)
	}
	var committed CommittedChapterVersion
	_ = json.Unmarshal(raw, &committed)
	copy, err = s.GetWorkingCopy(ctx, a, p.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ReviewItems[0].Anchor.ChapterVersionID == nil || *copy.ReviewItems[0].Anchor.ChapterVersionID != committed.ChapterVersionID || copy.ReviewItems[0].Anchor.WorkingCopyRevision != copy.WorkingCopyRevision {
		t.Fatal("commit anchor basis stale")
	}
	save.BaseChapterVersionID = copy.BaseChapterVersionID
	save.ExpectedWorkingCopyRevision = copy.WorkingCopyRevision
	save.BodyMarkdown = "引用。引用"
	raw, _, _, err = s.SaveWorkingCopy(ctx, a, p.ID, id, "anchor-ambiguous-test", save)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &copy)
	if copy.ReviewItems[0].Anchor.Status != "stale" {
		t.Fatal("ambiguous text incorrectly located")
	}
	versions, err := s.ListChapterVersions(ctx, a, p.ID, id)
	if err != nil || len(versions) != 1 || versions[0].ReviewItems[0].Anchor.Status != "located" {
		t.Fatal("immutable historical anchor changed")
	}
	input.ExpectedChapterVersionID = copy.BaseChapterVersionID
	input.ExpectedWorkingCopyRevision = copy.WorkingCopyRevision
	input.Anchor = nil
	raw, _, _, err = s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "anchor-detach-test", input)
	if err != nil {
		t.Fatal(err)
	}
	var detached WorkingCopy
	if err := json.Unmarshal(raw, &detached); err != nil {
		t.Fatal(err)
	}
	if detached.ReviewItems[0].Anchor != nil {
		t.Fatal("explicit detach failed")
	}

}
func TestAnchorCannotInventItemsOrBypassPermission(t *testing.T) {
	s, a, p, id, copy := anchorFixture(t)
	ctx := context.Background()
	input := SetReviewAnchorInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Anchor: &reviewanchor.Selection{StartUTF16: 2, EndUTF16: 5, QuoteMarkdown: "引用"}}
	if _, _, _, err := s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "split-surrogate", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe UTF16 accepted %v", err)
	}
	input.Anchor = nil
	if _, _, _, err := s.SetReviewAnchor(ctx, a, p.ID, id, "invented", "anchor-invent-test", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invented item %v", err)
	}
	outsider := Actor{TenantID: a.TenantID, UserID: "outsider"}
	seedTenantMember(t, s, outsider)
	if _, _, _, err := s.SetReviewAnchor(ctx, outsider, p.ID, id, "r1", "unauthorized", input); err == nil {
		t.Fatal("nonmember could change anchor")
	}
	if _, err := s.GetCurrentConfirmation(ctx, outsider, p.ID, id); err == nil {
		t.Fatal("nonmember could read confirmation")
	}
}
func TestCurrentConfirmationReadsCompleteRecordAndRejectsOldBasis(t *testing.T) {
	s, a, p, id, copy := anchorFixture(t)
	ctx := context.Background()
	raw, _, _, err := s.CommitWorkingCopy(ctx, a, p.ID, id, "confirmation-commit", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision})
	if err != nil {
		t.Fatal(err)
	}
	var committed CommittedChapterVersion
	_ = json.Unmarshal(raw, &committed)
	db := s.repository.(*GORMRepository).db
	details := map[string]any{"id": "c1", "chapter_id": id, "chapter_version_id": committed.ChapterVersionID, "spec_revision": 1, "template_version": "0.1.0", "valid": true, "actor_user_id": a.UserID, "review_decisions": []map[string]string{{"review_item_id": "r1", "disposition": "retained_warning", "reason": "synthetic reason"}}}
	current, err := s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	details["template_version"] = current.TemplateVersion
	encoded, _ := json.Marshal(details)
	if err := db.Model(&chapterVersionRow{}).Where("id = ?", committed.ChapterVersionID).Update("confirmation_valid", true).Error; err != nil {
		t.Fatal(err)
	}
	row := chapterConfirmationRow{ID: "c1", ChapterID: id, ChapterVersionID: committed.ChapterVersionID, Valid: true, DetailsJSON: string(encoded), CreatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCurrentConfirmation(ctx, a, p.ID, id)
	if err != nil || string(got) != string(encoded) {
		t.Fatalf("complete record missing: %s %v", got, err)
	}
	details["spec_revision"] = 0
	encoded, _ = json.Marshal(details)
	_ = db.Model(&chapterConfirmationRow{}).Where("id = ?", "c1").Update("details_json", string(encoded)).Error
	got, err = s.GetCurrentConfirmation(ctx, a, p.ID, id)
	if err != nil || string(got) != "null" {
		t.Fatalf("old confirmation returned: %s %v", got, err)
	}
}

func TestAnchorWriteAndReplayRecheckRevokedSources(t *testing.T) {
	s, a, projectID, chapterID, policy := operationSourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, _, _, err := s.ApplyRewrite(ctx, a, projectID, chapterID, "anchor-source-seed", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "draft [[source:one]]", SourceIDs: []string{"one"}}, []ReviewItem{{ID: "r1", Statement: "核实", OriginCandidateID: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	var copy WorkingCopy
	_ = json.Unmarshal(raw, &copy)
	input := SetReviewAnchorInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Anchor: &reviewanchor.Selection{StartUTF16: 0, EndUTF16: 5, QuoteMarkdown: "draft"}}
	_, _, _, err = s.SetReviewAnchor(ctx, a, projectID, chapterID, "r1", "anchor-source-attach", input)
	if err != nil {
		t.Fatal(err)
	}
	policy.denied = true
	if _, _, _, err = s.SetReviewAnchor(ctx, a, projectID, chapterID, "r1", "anchor-source-attach", input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("revoked source replay leaked content: %v", err)
	}
	input.ExpectedWorkingCopyRevision++
	input.Anchor = nil
	if _, _, _, err = s.SetReviewAnchor(ctx, a, projectID, chapterID, "r1", "anchor-source-detach", input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("revoked source write leaked content: %v", err)
	}
}
func TestLegacyChapterSaveRebasesAnchorAndInvalidatesConfirmation(t *testing.T) {
	s, a, p, id, copy := anchorFixture(t)
	ctx := context.Background()
	raw, _, _, err := s.SetReviewAnchor(ctx, a, p.ID, id, "r1", "legacy-anchor-attach", SetReviewAnchorInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Anchor: &reviewanchor.Selection{StartUTF16: 3, EndUTF16: 5, QuoteMarkdown: "引用"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &copy)
	raw, _, _, err = s.CommitWorkingCopy(ctx, a, p.ID, id, "legacy-anchor-commit", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision})
	if err != nil {
		t.Fatal(err)
	}
	var committed CommittedChapterVersion
	_ = json.Unmarshal(raw, &committed)
	raw, _, _, err = s.SaveChapter(ctx, a, p.ID, id, "legacy-anchor-save", SaveChapterInput{ExpectedChapterVersionID: &committed.ChapterVersionID, ExpectedSpecRevision: 1, BodyMarkdown: "另一段正文", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var chapter Chapter
	_ = json.Unmarshal(raw, &chapter)
	if chapter.ReviewItems[0].Anchor.Status != "stale" || chapter.ConfirmationValid {
		t.Fatal("formal rewrite retained unsafe located/confirmed state")
	}
	copy, err = s.GetWorkingCopy(ctx, a, p.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ReviewItems[0].Anchor.Status != "stale" || copy.ReviewItems[0].Anchor.WorkingCopyRevision != copy.WorkingCopyRevision || !sameVersion(copy.ReviewItems[0].Anchor.ChapterVersionID, copy.BaseChapterVersionID) {
		t.Fatalf("legacy copy basis not synchronized: %+v", copy)
	}
}

func TestLegacyEmptyConfirmationViewDoesNotRewriteStoredRecord(t *testing.T) {
	s, a, p, id, _ := operationSourceFixture(t)
	ctx := context.Background()
	_, _, _, err := s.SaveWorkingCopy(ctx, a, p, id, "empty-list-save", SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "手写无待核章", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := s.CommitWorkingCopy(ctx, a, p, id, "empty-list-commit", CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	var committed CommittedChapterVersion
	_ = json.Unmarshal(raw, &committed)
	project, err := s.GetProject(ctx, a, p)
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]any{"id": "empty-confirmation", "chapter_id": id, "chapter_version_id": committed.ChapterVersionID, "spec_revision": 1, "template_version": project.TemplateVersion, "valid": true, "review_decisions": nil}
	encoded, _ := json.Marshal(details)
	db := s.repository.(*GORMRepository).db
	_ = db.Model(&chapterVersionRow{}).Where("id = ?", committed.ChapterVersionID).Update("confirmation_valid", true).Error
	row := chapterConfirmationRow{ID: "empty-confirmation", ChapterID: id, ChapterVersionID: committed.ChapterVersionID, Valid: true, DetailsJSON: string(encoded), CreatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCurrentConfirmation(ctx, a, p, id)
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]json.RawMessage
	if err := json.Unmarshal(got, &view); err != nil {
		t.Fatal(err)
	}
	if string(view["review_decisions"]) != "[]" {
		t.Fatal("legacy empty array not normalized in read view")
	}
	var stored chapterConfirmationRow
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DetailsJSON != string(encoded) {
		t.Fatal("view changed stored record")
	}
}
