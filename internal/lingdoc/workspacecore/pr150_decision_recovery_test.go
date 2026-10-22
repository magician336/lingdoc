package workspacecore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// These are application regressions on the existing production-migration SQLite
// fixture. They do not replace current authorization, transactions or audits.
type pr150RecoveryFixture struct {
	service   *Service
	owner     Actor
	project   Project
	chapterID string
	path      string
}

func pr150NewRecoveryFixture(t *testing.T) *pr150RecoveryFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pr150-decision-recovery.sqlite")
	s := testStore(t, path)
	owner := Actor{TenantID: 15150, UserID: "pr150-original-owner", Role: types.TenantRoleOwner}
	p := seedActiveImpactProject(t, s, owner)
	chapters, err := s.ListChapters(context.Background(), owner, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("read fixture chapters: chapters=%d err=%v", len(chapters), err)
	}
	if _, _, _, err := s.SaveChapter(context.Background(), owner, p.ID, chapters[0].ID,
		"pr150-fixture-body", SaveChapterInput{
			ExpectedSpecRevision: p.SpecRevision,
			BodyMarkdown:         "Synthetic chapter with an actual immutable version.",
			SourceIDs:            []string{},
		}); err != nil {
		t.Fatalf("save fixture chapter version: %v", err)
	}
	p, err = s.GetProject(context.Background(), owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Activation was seeded by the shared fixture. Subsequent membership and
	// transfer actions use the existing full-flow invitation/origin checks.
	s.RequireActivationRead(true)
	return &pr150RecoveryFixture{s, owner, p, chapters[0].ID, path}
}

func (f *pr150RecoveryFixture) reopen(t *testing.T) {
	t.Helper()
	db, err := f.service.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close original SQLite connection: %v", err)
	}
	f.service = testStore(t, f.path)
	f.service.RequireActivationRead(true)
}

func pr150DecodeChange(t *testing.T, raw json.RawMessage) ChangeSet {
	t.Helper()
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil || change.ID == "" {
		t.Fatalf("decode ChangeSet response: change=%+v err=%v", change, err)
	}
	return change
}

func (f *pr150RecoveryFixture) assessed(t *testing.T) ChangeSet {
	t.Helper()
	raw, status, replayed, err := f.service.CreateChangeSet(context.Background(), f.owner,
		f.project.ID, "pr150-assessed-proposal", CreateChangeSetInput{
			ExpectedContextRevision: f.project.CurrentContextRevision,
			Fields: map[string]ChangeFieldInput{
				"research_goal": {OldValue: f.project.Spec["research_goal"], NewValue: "Revised synthetic goal"},
			},
			AffectedChapterIDs: []string{f.chapterID},
			Reason:             "Evaluate the exact synthetic goal change.",
		})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create assessed proposal: status=%d replayed=%v err=%v", status, replayed, err)
	}
	change := pr150DecodeChange(t, raw)
	if change.Status != "assessed" {
		t.Fatalf("proposal was not assessed: %+v", change)
	}
	return change
}

func pr150Decide(s *Service, actor Actor, projectID, changeID, key, action string) (ChangeSetMutationResult, error) {
	if action == "apply" {
		return s.ApplyChangeSetResult(context.Background(), actor, projectID, changeID, key)
	}
	return s.RejectChangeSetResult(context.Background(), actor, projectID, changeID, key)
}

// Read the public JSON properties so this file compiles before the a6 model
// fields are integrated. Missing metadata is a behavioral failure, not a build
// dependency on an unmerged Go struct field.
type pr150DecisionMetadata struct {
	ActorID string     `json:"decision_by"`
	At      *time.Time `json:"decided_at"`
}

func pr150Metadata(t *testing.T, change ChangeSet) pr150DecisionMetadata {
	t.Helper()
	raw, err := json.Marshal(change)
	if err != nil {
		t.Fatal(err)
	}
	var metadata pr150DecisionMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func pr150JSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Capture public application projections, not individual persistence columns.
// Authorization observations are separate from the critical business audits.
func pr150RecoveryState(t *testing.T, s *Service, actor Actor, projectID, changeID string) string {
	t.Helper()
	ctx := context.Background()
	p, err := s.GetProject(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	change, err := s.GetChangeSet(ctx, actor, projectID, changeID)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := s.ListChapters(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(chapters, func(i, j int) bool { return chapters[i].ID < chapters[j].ID })
	tasks, err := s.ListImpactTasks(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	revisions, err := s.ListContextRevisions(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ListAuditEvents(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	business := make([]AuditEvent, 0, len(events))
	for _, event := range events {
		if !strings.HasPrefix(event.Action, "authorization.") {
			business = append(business, event)
		}
	}
	sort.Slice(business, func(i, j int) bool { return business[i].ID < business[j].ID })
	return pr150JSON(t, struct {
		ProjectVersion  int64                   `json:"project_version"`
		SpecRevision    int64                   `json:"spec_revision"`
		ContextRevision int64                   `json:"context_revision"`
		Spec            map[string]string       `json:"spec"`
		Change          ChangeSet               `json:"change"`
		Chapters        []Chapter               `json:"chapters"`
		Tasks           []ImpactTask            `json:"tasks"`
		Revisions       []ContextRevisionRecord `json:"revisions"`
		BusinessAudits  []AuditEvent            `json:"business_audits"`
	}{p.ProjectVersion, p.SpecRevision, p.CurrentContextRevision, p.Spec, change, chapters, tasks, revisions, business})
}

func pr150AssertDecision(t *testing.T, s *Service, reader Actor, projectID string, change ChangeSet, action, expectedActor string) {
	t.Helper()
	wantStatus := "rejected"
	if action == "apply" {
		wantStatus = "applied"
	}
	metadata := pr150Metadata(t, change)
	if change.Status != wantStatus || metadata.ActorID != expectedActor || metadata.At == nil || metadata.At.IsZero() {
		t.Fatalf("decision lacks current Owner identity/time: status=%s metadata=%+v", change.Status, metadata)
	}
	stored, err := s.GetChangeSet(context.Background(), reader, projectID, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedMetadata := pr150Metadata(t, stored)
	if stored.Status != wantStatus || storedMetadata.ActorID != metadata.ActorID || storedMetadata.At == nil || !storedMetadata.At.Equal(*metadata.At) {
		t.Fatalf("decision readback differs: response=%+v stored=%+v", metadata, storedMetadata)
	}
	events, err := s.ListAuditEvents(context.Background(), reader, projectID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == "project.change_set."+action && event.Target == change.ID {
			count++
			if event.ActorID != expectedActor || event.Reason != change.Reason {
				t.Fatalf("decision audit lost actor/reason: %+v", event)
			}
		}
	}
	if count != 1 {
		t.Fatalf("decision business audit count=%d, want one", count)
	}
}

func TestPR150DecisionRecoveryEditedDraftReasonSurvivesRestart(t *testing.T) {
	f := pr150NewRecoveryFixture(t)
	input := ExtendedChangeSetInput{
		ExpectedContextRevision: f.project.CurrentContextRevision,
		Reason:                  "Original proposal explanation.",
		Fields: map[string]ChangeFieldInput{
			"research_goal": {OldValue: f.project.Spec["research_goal"], NewValue: "First proposed goal"},
		},
		AffectedChapterIDs: []string{f.chapterID},
	}
	raw, _, _, err := f.service.CreateChangeDraft(context.Background(), f.owner, f.project.ID, "pr150-create-draft", input)
	if err != nil {
		t.Fatal(err)
	}
	draft := pr150DecodeChange(t, raw)
	input.Reason = "Updated explanation that must survive the restart."
	input.Fields["research_goal"] = ChangeFieldInput{OldValue: f.project.Spec["research_goal"], NewValue: "Edited proposed goal"}
	raw, _, replayed, err := f.service.EditChangeDraft(context.Background(), f.owner, f.project.ID, draft.ID, "pr150-edit-draft", input)
	if err != nil || replayed {
		t.Fatalf("edit draft: replayed=%v err=%v", replayed, err)
	}
	edited := pr150DecodeChange(t, raw)
	if edited.Reason != input.Reason {
		t.Fatalf("edit response reason=%q, want %q", edited.Reason, input.Reason)
	}
	f.reopen(t)
	stored, err := f.service.GetChangeSet(context.Background(), f.owner, f.project.ID, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Reason != input.Reason || stored.Status != "draft" || len(stored.Fields) != 1 || stored.Fields[0].NewValue != "Edited proposed goal" {
		t.Fatalf("edited proposal did not survive reopen: %+v", stored)
	}
	raw, _, replayed, err = f.service.EditChangeDraft(context.Background(), f.owner, f.project.ID, draft.ID, "pr150-edit-draft", input)
	if err != nil || !replayed || pr150JSON(t, pr150DecodeChange(t, raw)) != pr150JSON(t, edited) {
		t.Fatalf("lost edit response did not replay after reopen: replayed=%v err=%v", replayed, err)
	}
	if _, _, _, err := f.service.AssessChangeSet(context.Background(), f.owner, f.project.ID, draft.ID, "pr150-assess-edited"); err != nil {
		t.Fatal(err)
	}
	result, err := pr150Decide(f.service, f.owner, f.project.ID, draft.ID, "pr150-apply-edited", "apply")
	if err != nil || result.Replayed || result.ChangeSet.Reason != input.Reason {
		t.Fatalf("apply lost edited explanation: result=%+v err=%v", result, err)
	}
	pr150AssertDecision(t, f.service, f.owner, f.project.ID, result.ChangeSet, "apply", f.owner.UserID)
}

func TestPR150DecisionRecoveryDecisionAuditFailureIsAtomic(t *testing.T) {
	for _, action := range []string{"apply", "reject"} {
		t.Run(action, func(t *testing.T) {
			f := pr150NewRecoveryFixture(t)
			change := f.assessed(t)
			metadata := pr150Metadata(t, change)
			if metadata.ActorID != "" || metadata.At != nil {
				t.Fatalf("assessed proposal already claims a decision: %+v", metadata)
			}
			before := pr150RecoveryState(t, f.service, f.owner, f.project.ID, change.ID)
			// action is selected only from the fixed two-case table above. Fail
			// the critical business audit, not an authorization observation.
			trigger := "CREATE TRIGGER pr150_fail_decision_audit BEFORE INSERT ON lingdoc_project_audits " +
				"WHEN NEW.action = 'project.change_set." + action + "' " +
				"BEGIN SELECT RAISE(ABORT, 'pr150-decision-audit-failure'); END"
			if err := f.service.testDB().Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			key := "pr150-audit-rollback-" + action
			failed, err := pr150Decide(f.service, f.owner, f.project.ID, change.ID, key, action)
			if err == nil || !strings.Contains(err.Error(), "pr150-decision-audit-failure") || failed.Replayed || failed.ChangeSet.ID != "" {
				t.Fatalf("critical decision audit did not abort: result=%+v err=%v", failed, err)
			}
			if after := pr150RecoveryState(t, f.service, f.owner, f.project.ID, change.ID); after != before {
				t.Fatalf("audit failure partially committed the decision\nbefore=%s\nafter=%s", before, after)
			}
			f.reopen(t)
			if after := pr150RecoveryState(t, f.service, f.owner, f.project.ID, change.ID); after != before {
				t.Fatalf("audit failure left durable partial state after restart\nbefore=%s\nafter=%s", before, after)
			}
			if err := f.service.testDB().Exec("DROP TRIGGER pr150_fail_decision_audit").Error; err != nil {
				t.Fatal(err)
			}
			result, err := pr150Decide(f.service, f.owner, f.project.ID, change.ID, key, action)
			if err != nil || result.Replayed {
				t.Fatalf("same key must remain retryable after audit rollback: result=%+v err=%v", result, err)
			}
			pr150AssertDecision(t, f.service, f.owner, f.project.ID, result.ChangeSet, action, f.owner.UserID)
			p, err := f.service.GetProject(context.Background(), f.owner, f.project.ID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "apply" {
				if p.ProjectVersion != f.project.ProjectVersion+1 || p.SpecRevision != f.project.SpecRevision+1 ||
					p.CurrentContextRevision != f.project.CurrentContextRevision+1 || p.Spec["research_goal"] != "Revised synthetic goal" {
					t.Fatalf("successful retry did not apply exactly one revision: %+v", p)
				}
			} else if p.ProjectVersion != f.project.ProjectVersion || p.SpecRevision != f.project.SpecRevision ||
				p.CurrentContextRevision != f.project.CurrentContextRevision || p.Spec["research_goal"] != f.project.Spec["research_goal"] {
				t.Fatalf("rejection changed the formal project: %+v", p)
			}
			afterSuccess := pr150RecoveryState(t, f.service, f.owner, p.ID, change.ID)
			f.reopen(t)
			pr150AssertDecision(t, f.service, f.owner, p.ID, result.ChangeSet, action, f.owner.UserID)
			retry, err := pr150Decide(f.service, f.owner, p.ID, change.ID, key, action)
			if err != nil || !retry.Replayed || pr150JSON(t, retry.ChangeSet) != pr150JSON(t, result.ChangeSet) {
				t.Fatalf("persisted decision did not replay unchanged: result=%+v err=%v", retry, err)
			}
			if after := pr150RecoveryState(t, f.service, f.owner, p.ID, change.ID); after != afterSuccess {
				t.Fatalf("decision replay added formal work or business audit\nbefore=%s\nafter=%s", afterSuccess, after)
			}
		})
	}
}
