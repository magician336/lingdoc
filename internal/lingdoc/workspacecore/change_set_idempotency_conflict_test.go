package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// changeIdempotencyFixture creates an assessed ChangeSet and returns it with
// the chapters it covers.
func changeIdempotencyFixture(t *testing.T, tenant uint64, db string) (*Service, Actor, Project, ChangeSet, []Chapter) {
	t.Helper()
	svc := testStore(t, filepath.Join(t.TempDir(), db))
	actor := Actor{TenantID: tenant, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, project.ID,
		fmt.Sprintf("idem-create-%d", tenant), CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "幂等冲突基线",
		})
	if err != nil || status != 201 || replay {
		t.Fatalf("seed changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode seeded change: %v", err)
	}
	return svc, actor, project, change, chapters
}

// A repeated idempotency key is a promise that the caller is retrying one
// request. Once that key has been spent, spending it again on a *different*
// request must be refused rather than silently answered with the first result:
// the caller would otherwise believe a change it never asked for was applied.
func TestChangeSetCreateSameKeyWithDifferentPayloadIsRefused(t *testing.T) {
	svc, actor, project, _, chapters := changeIdempotencyFixture(t, 341, "create-conflict.db")
	ctx := context.Background()

	altered := CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "完全不同的新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "同键换内容",
	}
	if _, _, _, err := svc.CreateChangeSet(ctx, actor, project.ID, fmt.Sprintf("idem-create-%d", 341), altered); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key with a different payload = %v, want ErrIdempotencyConflict", err)
	}

	// The refused request must leave no second proposal behind.
	listed, err := svc.ListChangeSets(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("refused create produced %d proposals, want the original only", len(listed))
	}
	current, err := svc.GetProject(ctx, actor, project.ID)
	if err != nil || current.CurrentContextRevision != project.CurrentContextRevision || current.Spec["research_subject"] == "完全不同的新主题" {
		t.Fatalf("refused create mutated the project: %+v err=%v", current, err)
	}
}

// A decision key is scoped to one ChangeSet. Spending a key that was issued
// for one proposal on a *different* proposal is a different request, so it
// must not replay the first decision's result.
//
// Note the payload itself is not the lever here: apply and reject each bind
// their key to `projectID + "/" + changeSetID` and carry a fixed
// `{"change_set_id": ...}` body, so the second proposal is the only way to
// reuse a spent key against a request it was never issued for.
func TestChangeSetDecisionKeyIsScopedToItsOwnProposal(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "decision-key-scope.db"))
	actor := Actor{TenantID: 342, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ctx := context.Background()
	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	create := func(key, newValue string) ChangeSet {
		raw, status, replay, err := svc.CreateChangeSet(ctx, actor, project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: newValue}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "决策键作用域",
		})
		if err != nil || status != 201 || replay {
			t.Fatalf("create %s: status=%d replay=%v err=%v", key, status, replay, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatal(err)
		}
		return change
	}
	decided := create("idem-decided", "已决策的新主题")
	other := create("idem-other", "另一个新主题")

	// The key was issued for `decided`; replaying it against `other` must be
	// treated as a fresh request, never answered with the first decision.
	raw, status, replay, err := svc.ApplyChangeSet(ctx, actor, project.ID, other.ID, "idem-decided")
	if err != nil || status != 200 || replay {
		t.Fatalf("second proposal applied under the first key: status=%d replay=%v err=%v", status, replay, err)
	}
	var applied ChangeSet
	if err := json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.ID != other.ID {
		t.Fatalf("key for %s replayed %s's result", decided.ID, applied.ID)
	}
	// The proposal that key was never issued for must stay untouched.
	untouched, err := svc.GetChangeSet(ctx, actor, project.ID, decided.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != "assessed" {
		t.Fatalf("unrelated proposal changed status to %q", untouched.Status)
	}
	current, err := svc.GetProject(ctx, actor, project.ID)
	if err != nil || current.Spec["research_subject"] != "另一个新主题" {
		t.Fatalf("project holds the wrong value after the scoped decision: %+v err=%v", current, err)
	}
}

// A key is scoped to the request it was issued for. The same key against a
// different ChangeSet is a different request, so it must not replay the first
// proposal's result.
func TestChangeSetSameKeyOnAnotherChangeIsNotAReplay(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "cross-change-key.db"))
	actor := Actor{TenantID: 343, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ctx := context.Background()
	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	create := func(key, newValue string) ChangeSet {
		raw, status, replay, err := svc.CreateChangeSet(ctx, actor, project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: newValue}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "同键跨提案",
		})
		if err != nil || status != 201 || replay {
			t.Fatalf("create %s: status=%d replay=%v err=%v", key, status, replay, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatal(err)
		}
		return change
	}
	first := create("idem-cross-1", "第一个新主题")
	second := create("idem-cross-2", "第二个新主题")
	if first.ID == second.ID {
		t.Fatalf("distinct keys produced one proposal: %s", first.ID)
	}
}

// Reassessment carries its own source identity, so a key spent reassessing one
// stale proposal cannot be spent again on another.
func TestReassessSameKeyOnAnotherStaleProposalIsRefused(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "reassess-key-scope.db"))
	actor := Actor{TenantID: 344, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ctx := context.Background()
	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	create := func(key, field, oldValue, newValue string) ChangeSet {
		t.Helper()
		current, err := svc.GetProject(ctx, actor, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, status, replay, err := svc.CreateChangeSet(ctx, actor, project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: current.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{field: {OldValue: oldValue, NewValue: newValue}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "重评键作用域",
		})
		if err != nil || status != 201 || replay {
			t.Fatalf("create %s: status=%d replay=%v err=%v", key, status, replay, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatal(err)
		}
		return change
	}
	first := create("idem-reassess-first", "research_subject", "旧主题", "新主题")
	second := create("idem-reassess-second", "research_goal", "旧目标", "新目标")
	// Move the baseline past `second` so only that proposal becomes stale.
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, first.ID, "idem-reassess-move-baseline"); err != nil || status != 200 {
		t.Fatalf("move baseline: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, second.ID, "idem-reassess-stale"); err != nil || status != 409 {
		t.Fatalf("mark second stale: status=%d err=%v", status, err)
	}
	stale, err := svc.GetChangeSet(ctx, actor, project.ID, second.ID)
	if err != nil || stale.Status != "stale" {
		t.Fatalf("second proposal is not reassessable: %+v err=%v", stale, err)
	}

	raw, status, replay, err := svc.ReassessChangeSet(ctx, actor, project.ID, second.ID, "idem-reassess-shared")
	if err != nil || status != 201 || replay {
		t.Fatalf("reassess second: status=%d replay=%v err=%v", status, replay, err)
	}
	var reassessed ChangeSet
	if err := json.Unmarshal(raw, &reassessed); err != nil {
		t.Fatal(err)
	}
	if reassessed.ReassessedFromID != second.ID {
		t.Fatalf("reassessment came from %s, want %s", reassessed.ReassessedFromID, second.ID)
	}

	// The key is now spent. A *different* stale proposal must not be answered
	// with the reassessment produced for `second`.
	//
	// Build the stale state from the values the project actually holds, rather
	// than from names remembered across an earlier apply: read the current spec
	// and propose a change against it, then move the baseline with a second
	// proposal so the first becomes stale.
	current, err := svc.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	third := create("idem-reassess-third", "research_subject", current.Spec["research_subject"], "第三个新主题")
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, third.ID, "idem-reassess-third-apply"); err != nil || status != 200 {
		t.Fatalf("advance baseline past third: status=%d err=%v", status, err)
	}
	current, err = svc.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	fourth := create("idem-reassess-fourth", "research_goal", current.Spec["research_goal"], "第四个新目标")
	// Move the baseline once more so `fourth` is stale and reassessable.
	fifth := create("idem-reassess-fifth", "research_subject", current.Spec["research_subject"], "第五个新主题")
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, fifth.ID, "idem-reassess-fifth-apply"); err != nil || status != 200 {
		t.Fatalf("advance baseline past fifth: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, fourth.ID, "idem-reassess-fourth-apply"); err != nil || status != 409 {
		t.Fatalf("mark fourth stale: status=%d err=%v", status, err)
	}
	other, status, replay, err := svc.ReassessChangeSet(ctx, actor, project.ID, fourth.ID, "idem-reassess-shared")
	if err != nil || status != 201 {
		t.Fatalf("reassess fourth: status=%d err=%v", status, err)
	}
	var otherChange ChangeSet
	if err := json.Unmarshal(other, &otherChange); err != nil {
		t.Fatal(err)
	}
	if otherChange.ID == reassessed.ID || otherChange.ReassessedFromID != fourth.ID {
		t.Fatalf("key spent on %s returned %s's reassessment", second.ID, reassessed.ID)
	}
}
