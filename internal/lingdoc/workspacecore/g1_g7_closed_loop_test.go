package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"testing"
)

func firstPassReviewedProject(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	s, a, _, p := firstPassMemberFixture(t)
	s.RequireActivationRead(true)
	raw, _, _, err := s.SaveSpec(context.Background(), a, p.ID, "full-spec", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "合成主题", "research_goal": "合成目标"}})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	read, err := s.RecordActivationRead(context.Background(), a, p.ID, p.ProjectVersion)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = s.ActivateProject(context.Background(), a, p.ID, "full-activate", ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion, ActivationReadID: read.ID})
	if err != nil {
		t.Fatal(err)
	}
	return s, a, asProject(t, raw)
}
func TestFirstPassReviewAndCommentsUseExactImmutableVersion(t *testing.T) {
	s, a, p := firstPassReviewedProject(t)
	ctx := context.Background()
	chapters, err := s.ListChapters(ctx, a, p.ID)
	if err != nil || len(chapters) < 1 {
		t.Fatal(err)
	}
	c := chapters[0]
	raw, _, _, err := s.SaveChapter(ctx, a, p.ID, c.ID, "review-body", SaveChapterInput{ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "段落 😀 合成证据", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var saved Chapter
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	review := ChapterReviewInput{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision, ChapterVersionID: *saved.CurrentVersionID, ExpectedReviewVersion: 0, Action: "submit"}
	raw, _, _, err = s.ChangeChapterReview(ctx, a, p.ID, c.ID, "review-submit", review)
	if err != nil {
		t.Fatal(err)
	}
	var state ChapterReviewState
	if err := json.Unmarshal(raw, &state); err != nil || state.Status != "IN_REVIEW" {
		t.Fatalf("%s %v", raw, err)
	}
	comment := ChapterCommentInput{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision, ChapterVersionID: *saved.CurrentVersionID, Body: "核对这一段", Selection: &reviewanchor.Selection{StartUTF16: 3, EndUTF16: 5, QuoteMarkdown: "😀"}}
	if _, _, _, err := s.SaveChapterComment(ctx, a, p.ID, c.ID, "comment-emoji", comment); err != nil {
		t.Fatal(err)
	}
	comment.Selection.EndUTF16 = 4
	if _, _, _, err := s.SaveChapterComment(ctx, a, p.ID, c.ID, "comment-surrogate", comment); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("surrogate split accepted: %v", err)
	}
	review.Action = "return"
	review.ExpectedReviewVersion = state.Version
	review.Reason = "请补充依据"
	if _, _, _, err := s.ChangeChapterReview(ctx, a, p.ID, c.ID, "review-return", review); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChapterReview(ctx, a, p.ID, c.ID)
	if err != nil || got.Status != "DRAFT" || got.Reason != "请补充依据" {
		t.Fatalf("return state: %+v %v", got, err)
	}
	if _, _, _, err := s.SaveChapter(ctx, a, p.ID, c.ID, "review-next-body", SaveChapterInput{ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "第二个版本", SourceIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetChapterReview(ctx, a, p.ID, c.ID)
	if err != nil || got.Status != "STALE" {
		t.Fatalf("old version incorrectly current: %+v %v", got, err)
	}
	old, err := s.ListChapterComments(ctx, a, p.ID, c.ID, *saved.CurrentVersionID)
	if err != nil || len(old) != 1 {
		t.Fatalf("historical comments lost: %+v %v", old, err)
	}
}
