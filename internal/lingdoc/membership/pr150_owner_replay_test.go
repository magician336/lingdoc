package membership

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// These are application regressions on the existing production-migration SQLite
// fixture. They do not replace current authorization, transactions or audits.
//
// They are copies of the aggregate's own PR-150 fixture: the owner replay case
// reaches the member and owner operations, so it lives with them, and the two
// fixtures stay separate copies of the same shape.
type pr150RecoveryFixture struct {
	workspace *testWorkspace
	owner     core.Actor
	project   core.Project
	chapterID string
	path      string
}

func pr150NewRecoveryFixture(t *testing.T) *pr150RecoveryFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pr150-decision-recovery.sqlite")
	w := newTestWorkspace(t, path)
	owner := core.Actor{TenantID: 15150, UserID: "pr150-original-owner", Role: types.TenantRoleOwner}
	p := seedActiveImpactProject(t, w.svc, w.db, owner)
	chapters, err := w.svc.ListChapters(context.Background(), owner, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("read fixture chapters: chapters=%d err=%v", len(chapters), err)
	}
	if _, _, _, err := w.svc.SaveChapter(context.Background(), owner, p.ID, chapters[0].ID,
		"pr150-fixture-body", core.SaveChapterInput{
			ExpectedSpecRevision: p.SpecRevision,
			BodyMarkdown:         "Synthetic chapter with an actual immutable version.",
			SourceIDs:            []string{},
		}); err != nil {
		t.Fatalf("save fixture chapter version: %v", err)
	}
	p, err = w.svc.GetProject(context.Background(), owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Activation was seeded by the shared fixture. Subsequent membership and
	// transfer actions use the existing full-flow invitation/origin checks.
	w.svc.RequireActivationRead(true)
	return &pr150RecoveryFixture{w, owner, p, chapters[0].ID, path}
}

func (f *pr150RecoveryFixture) reopen(t *testing.T) {
	t.Helper()
	db, err := f.workspace.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close original SQLite connection: %v", err)
	}
	f.workspace = reopenTestWorkspace(t, f.workspace, f.path)
	f.workspace.svc.RequireActivationRead(true)
}

func pr150DecodeChange(t *testing.T, raw json.RawMessage) core.ChangeSet {
	t.Helper()
	var change core.ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil || change.ID == "" {
		t.Fatalf("decode ChangeSet response: change=%+v err=%v", change, err)
	}
	return change
}

func (f *pr150RecoveryFixture) assessed(t *testing.T) core.ChangeSet {
	t.Helper()
	raw, status, replayed, err := f.workspace.svc.CreateChangeSet(context.Background(), f.owner,
		f.project.ID, "pr150-assessed-proposal", core.CreateChangeSetInput{
			ExpectedContextRevision: f.project.CurrentContextRevision,
			Fields: map[string]core.ChangeFieldInput{
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

func pr150Decide(
	s *core.Service,
	actor core.Actor,
	projectID, changeID, key, action string,
) (core.ChangeSetMutationResult, error) {
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

func pr150Metadata(t *testing.T, change core.ChangeSet) pr150DecisionMetadata {
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
func pr150RecoveryState(t *testing.T, s *core.Service, actor core.Actor, projectID, changeID string) string {
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
	business := make([]core.AuditEvent, 0, len(events))
	for _, event := range events {
		if !strings.HasPrefix(event.Action, "authorization.") {
			business = append(business, event)
		}
	}
	sort.Slice(business, func(i, j int) bool { return business[i].ID < business[j].ID })
	return pr150JSON(t, struct {
		ProjectVersion  int64                        `json:"project_version"`
		SpecRevision    int64                        `json:"spec_revision"`
		ContextRevision int64                        `json:"context_revision"`
		Spec            map[string]string            `json:"spec"`
		Change          core.ChangeSet               `json:"change"`
		Chapters        []core.Chapter               `json:"chapters"`
		Tasks           []core.ImpactTask            `json:"tasks"`
		Revisions       []core.ContextRevisionRecord `json:"revisions"`
		BusinessAudits  []core.AuditEvent            `json:"business_audits"`
	}{p.ProjectVersion, p.SpecRevision, p.CurrentContextRevision, p.Spec, change, chapters, tasks, revisions, business})
}

func pr150AssertDecision(
	t *testing.T,
	s *core.Service,
	reader core.Actor,
	projectID string,
	change core.ChangeSet,
	action, expectedActor string,
) {
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
	if stored.Status != wantStatus || storedMetadata.ActorID != metadata.ActorID || storedMetadata.At == nil ||
		!storedMetadata.At.Equal(*metadata.At) {
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

func TestPR150DecisionRecoveryOwnerReplayRequiresCurrentOwner(t *testing.T) {
	for _, action := range []string{"apply", "reject"} {
		t.Run(action, func(t *testing.T) {
			f := pr150NewRecoveryFixture(t)
			change := f.assessed(t)
			key := "pr150-current-owner-" + action
			result, err := pr150Decide(f.workspace.svc, f.owner, f.project.ID, change.ID, key, action)
			if err != nil || result.Replayed {
				t.Fatalf("initial Owner decision: replayed=%v err=%v", result.Replayed, err)
			}
			f.reopen(t)
			pr150AssertDecision(t, f.workspace.svc, f.owner, f.project.ID, result.ChangeSet, action, f.owner.UserID)
			original := pr150JSON(t, result.ChangeSet)
			retry, err := pr150Decide(f.workspace.svc, f.owner, f.project.ID, change.ID, key, action)
			if err != nil || !retry.Replayed || pr150JSON(t, retry.ChangeSet) != original {
				t.Fatalf("current Owner retry: replayed=%v err=%v", retry.Replayed, err)
			}

			// Use real invitation, acceptance and transfer applications rather than
			// rewriting a member row to manufacture the authority change.
			nextOwner := core.Actor{
				TenantID: f.owner.TenantID, UserID: "pr150-next-owner", Role: types.TenantRoleContributor,
			}
			seedTenantMember(t, f.workspace.db, nextOwner)
			p, err := f.workspace.svc.GetProject(context.Background(), f.owner, f.project.ID)
			if err != nil {
				t.Fatal(err)
			}
			invitation, _ := firstPassInvite(t, f.workspace, f.owner, nextOwner, p)
			if _, _, _, err := f.workspace.member.AcceptProjectInvitation(
				context.Background(), nextOwner, p.ID, invitation.ID,
				"pr150-accept-invitation",
				core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version},
			); err != nil {
				t.Fatal(err)
			}
			p, err = f.workspace.svc.GetProject(context.Background(), f.owner, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _, _, err := f.workspace.member.RequestOwnerTransfer(
				context.Background(), f.owner, p.ID, "pr150-request-owner-transfer",
				core.OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: nextOwner.UserID},
			)
			if err != nil {
				t.Fatal(err)
			}
			var transfer core.OwnerTransfer
			if err := json.Unmarshal(raw, &transfer); err != nil || transfer.ID == "" {
				t.Fatalf("decode owner transfer: %+v err=%v", transfer, err)
			}
			if _, _, _, err := f.workspace.member.AcceptOwnerTransfer(
				context.Background(), nextOwner, p.ID, transfer.ID, "pr150-accept-owner-transfer",
			); err != nil {
				t.Fatal(err)
			}
			p, err = f.workspace.svc.GetProject(context.Background(), nextOwner, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !core.ProjectOwner(p, nextOwner.UserID) || core.ProjectOwner(p, f.owner.UserID) {
				t.Fatalf("fixture did not transfer current Owner: %+v", p.Members)
			}
			before := pr150RecoveryState(t, f.workspace.svc, nextOwner, p.ID, change.ID)
			denied, err := pr150Decide(f.workspace.svc, f.owner, p.ID, change.ID, key, action)
			if !errors.Is(err, core.ErrNotFound) || denied.Replayed || denied.ChangeSet.ID != "" {
				t.Fatalf("former Owner replay was not denied: result=%+v err=%v", denied, err)
			}
			if after := pr150RecoveryState(t, f.workspace.svc, nextOwner, p.ID, change.ID); after != before {
				t.Fatalf("denied transferred-Owner replay changed formal state or business audit\nbefore=%s\nafter=%s",
					before, after)
			}

			if _, _, _, err := f.workspace.member.SetMemberAssignment(
				context.Background(), nextOwner, p.ID, f.owner.UserID,
				"pr150-former-owner-admin",
				core.MemberAssignmentInput{
					ExpectedProjectVersion: p.ProjectVersion,
					Assignment:             core.Member{UserID: f.owner.UserID, GovernanceRole: "admin"},
					Reason:                 "Retain coordination authority without Owner decisions.",
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := f.workspace.svc.Authorize(context.Background(), f.owner, p.ID, "manage"); err != nil {
				t.Fatalf("former Owner must still have current Admin manage authority: %v", err)
			}
			before = pr150RecoveryState(t, f.workspace.svc, nextOwner, p.ID, change.ID)
			denied, err = pr150Decide(f.workspace.svc, f.owner, p.ID, change.ID, key, action)
			if !errors.Is(err, core.ErrNotFound) || denied.Replayed || denied.ChangeSet.ID != "" {
				t.Fatalf("current Admin replay bypassed Owner-only decision authority: result=%+v err=%v", denied, err)
			}
			if after := pr150RecoveryState(t, f.workspace.svc, nextOwner, p.ID, change.ID); after != before {
				t.Fatalf("denied Admin replay changed formal state or business audit\nbefore=%s\nafter=%s",
					before, after)
			}
			pr150AssertDecision(t, f.workspace.svc, nextOwner, p.ID, result.ChangeSet, action, f.owner.UserID)
		})
	}
}
