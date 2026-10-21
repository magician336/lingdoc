package membership

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// These tests use isolated SQLite databases created by firstPassMemberFixture.
// They exercise persisted domain results; none represents a real human receipt.
func g5AuthReviewProject(t *testing.T, w *testWorkspace, a core.Actor, pid string) core.Project {
	t.Helper()
	p, err := w.svc.GetProject(context.Background(), a, pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func g5AuthReviewAcceptMember(
	t *testing.T,
	w *testWorkspace,
	member core.Actor,
	pid string,
	invitation core.MemberInvitation,
) core.Project {
	t.Helper()
	if _, _, _, err := w.member.AcceptProjectInvitation(context.Background(), member, pid, invitation.ID,
		"auth-review-accept-member",
		core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version},
	); err != nil {
		t.Fatal(err)
	}
	return g5AuthReviewProject(t, w, member, pid)
}

func g5AuthReviewCount(t *testing.T, w *testWorkspace, table, where string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := w.db.Table(table).Where(where, args...).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func g5AuthReviewOwner(t *testing.T, p core.Project, expected string) {
	t.Helper()
	var owners []string
	for _, m := range p.Members {
		if core.GovernanceRole(m) == "owner" {
			owners = append(owners, m.UserID)
		}
	}
	if len(owners) != 1 || owners[0] != expected {
		t.Fatalf("expected one Owner %q, got %v", expected, owners)
	}
}

func g5AuthReviewDropAuditTrigger(t *testing.T, w *testWorkspace) {
	t.Helper()
	if err := w.db.Exec("DROP TRIGGER IF EXISTS g5_review_block_audit").Error; err != nil {
		t.Fatal(err)
	}
}

func g5AuthReviewBlockAudit(t *testing.T, w *testWorkspace, action string) {
	t.Helper()
	if action != "owner.transfer.request" && action != "owner.transfer.accept" {
		t.Fatalf("unsupported audit injection action: %q", action)
	}
	// Restrict the failure to the business audit. A fail-open authorization log
	// must not be enough to satisfy the critical transfer audit requirement.
	sql := "CREATE TRIGGER g5_review_block_audit BEFORE INSERT ON lingdoc_project_audits " +
		"WHEN NEW.action = '" + action + "' BEGIN SELECT RAISE(ABORT, 'g5-review-audit-failure'); END"
	if err := w.db.Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.db.Exec("DROP TRIGGER IF EXISTS g5_review_block_audit").Error; err != nil {
			t.Errorf("remove isolated audit trigger: %v", err)
		}
	})
}

func TestG5AuthReviewListOmitsInvitedRemovedAndPendingTenantRecovery(t *testing.T) {
	w, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	const sentinel = "G5_REVIEW_PRIVATE_SPEC_8f16"
	raw, _, _, err := w.svc.SaveSpec(ctx, owner, p.ID, "auth-review-private-spec",
		core.SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": sentinel}})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)

	assertList := func(a core.Actor, visible bool) {
		t.Helper()
		list, truncated, err := w.svc.ListProjects(ctx, a)
		if err != nil || truncated {
			t.Fatalf("list: truncated=%v err=%v", truncated, err)
		}
		data, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		if visible {
			if len(list) != 1 || list[0].ID != p.ID || !strings.Contains(string(data), sentinel) {
				t.Fatalf("authorized project missing from list: %s", data)
			}
		} else if len(list) != 0 || strings.Contains(string(data), sentinel) {
			t.Fatalf("non-active project membership leaked Spec: %s", data)
		}
	}
	assertList(owner, true)
	assertList(member, false)

	invitation, _ := firstPassInvite(t, w, owner, member, p)
	assertList(member, false)
	p = g5AuthReviewAcceptMember(t, w, member, p.ID, invitation)
	assertList(member, true)

	if err := w.db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?",
		member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := w.svc.CaptureTenantSuspension(ctx, member.TenantID, member.UserID); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?",
		member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	assertList(member, false)
	if _, err := w.svc.GetProject(ctx, member, p.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("tenant restoration silently granted project read: %v", err)
	}
	p = g5AuthReviewProject(t, w, owner, p.ID)
	if _, _, _, err := w.member.ChangeMemberState(ctx, owner, p.ID, member.UserID, "auth-review-explicit-restore",
		core.MemberLifecycleInput{
			ExpectedProjectVersion: p.ProjectVersion,
			Action:                 "restore_tenant",
			Reason:                 "synthetic explicit recovery",
		},
	); err != nil {
		t.Fatal(err)
	}
	assertList(member, true)

	p = g5AuthReviewProject(t, w, owner, p.ID)
	if _, _, _, err := w.member.ChangeMemberState(ctx, owner, p.ID, member.UserID, "auth-review-remove-member",
		core.MemberLifecycleInput{
			ExpectedProjectVersion: p.ProjectVersion,
			Action:                 "remove",
			Reason:                 "synthetic revocation",
		},
	); err != nil {
		t.Fatal(err)
	}
	assertList(member, false)
	assertList(owner, true)
}

func TestG5AuthReviewEmptyAuthorCannotWriteGlobalSpec(t *testing.T) {
	w, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	invitation, _ := firstPassInvite(t, w, owner, member, p)
	p = g5AuthReviewAcceptMember(t, w, member, p.ID, invitation)
	before := g5AuthReviewProject(t, w, owner, p.ID)

	if err := w.svc.Authorize(ctx, member, p.ID, "write"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("empty Author scope authorized global write: %v", err)
	}
	if _, _, _, err := w.svc.SaveSpec(ctx, member, p.ID, "auth-review-forbidden-spec",
		core.SaveSpecInput{
			ExpectedSpecRevision: before.SpecRevision,
			Fields:               map[string]string{"research_subject": "forbidden overwrite"},
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("empty Author scope changed Spec: %v", err)
	}
	if _, _, _, err := w.svc.CreateDraftCandidate(ctx, member, p.ID, "auth-review-forbidden-global-draft",
		core.DraftCandidateInput{
			Kind:    "research",
			Title:   "unscoped",
			Content: "forbidden draft",
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("empty Author scope created project-wide draft: %v", err)
	}
	after := g5AuthReviewProject(t, w, owner, p.ID)
	if after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision ||
		after.Spec["research_subject"] != before.Spec["research_subject"] {
		t.Fatalf("denied writes changed persisted project: before=%+v after=%+v", before, after)
	}
	if n := g5AuthReviewCount(t, w, "lingdoc_operations", "user_id=? AND operation='saveSpec'", member.UserID); n != 0 {
		t.Fatalf("denied global write recorded a successful replay: %d", n)
	}
}

func TestG5AuthReviewRecoveryChecksCurrentPredecessorAndRequester(t *testing.T) {
	for _, change := range []string{"predecessor-restored", "requester-downgraded"} {
		t.Run(change, func(t *testing.T) {
			w, owner, member, p := firstPassMemberFixture(t)
			ctx := context.Background()
			invitation, _ := firstPassInvite(t, w, owner, member, p)
			p = g5AuthReviewAcceptMember(t, w, member, p.ID, invitation)
			w.svc.RequireActivationRead(true)
			requester := core.Actor{TenantID: owner.TenantID, UserID: "recovery-requester", Role: types.TenantRoleOwner}
			seedTenantMember(t, w.db, requester)

			if err := w.db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?",
				owner.TenantID, owner.UserID).Error; err != nil {
				t.Fatal(err)
			}
			if err := w.svc.CaptureTenantSuspension(ctx, owner.TenantID, owner.UserID); err != nil {
				t.Fatal(err)
			}
			raw, _, _, err := w.member.RequestOwnerTransfer(ctx, requester, p.ID, "auth-review-recovery-request",
				core.OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: member.UserID})
			if err != nil {
				t.Fatal(err)
			}
			transfer := rawOwnerTransfer(t, raw)
			wantError := core.ErrInvalidState
			if change == "predecessor-restored" {
				if err := w.db.Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?",
					owner.TenantID, owner.UserID).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := w.db.Exec("UPDATE tenant_members SET role='contributor' WHERE tenant_id=? AND user_id=?",
					requester.TenantID, requester.UserID).Error; err != nil {
					t.Fatal(err)
				}
				wantError = core.ErrNotFound
			}
			if _, _, _, err := w.member.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID,
				"auth-review-stale-recovery-accept"); !errors.Is(err, wantError) {
				t.Fatalf("stale recovery accepted: %v, want %v", err, wantError)
			}
			after := g5AuthReviewProject(t, w, member, p.ID)
			g5AuthReviewOwner(t, after, owner.UserID)
			if after.ProjectVersion != p.ProjectVersion {
				t.Fatalf("denied recovery advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
			}
			var stored ownerTransferRow
			if err := w.db.Where("id=?", transfer.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.Status != "pending" || stored.AcceptedAt != nil {
				t.Fatalf("denied recovery modified pending transfer: %+v", stored)
			}
			if n := g5AuthReviewCount(t, w, "lingdoc_operations", "key=?",
				"auth-review-stale-recovery-accept"); n != 0 {
				t.Fatalf("denied recovery stored successful replay: %d", n)
			}
		})
	}
}

func TestG5AuthReviewOwnerTransferBusinessAuditRollsBackAtomically(t *testing.T) {
	for _, action := range []string{"owner.transfer.request", "owner.transfer.accept"} {
		t.Run(action, func(t *testing.T) {
			w, owner, member, p := firstPassMemberFixture(t)
			ctx := context.Background()
			invitation, _ := firstPassInvite(t, w, owner, member, p)
			p = g5AuthReviewAcceptMember(t, w, member, p.ID, invitation)
			w.svc.RequireActivationRead(true)
			input := core.OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: member.UserID}
			var transfer core.OwnerTransfer

			if action == "owner.transfer.accept" {
				raw, _, _, err := w.member.RequestOwnerTransfer(ctx, owner, p.ID, "auth-review-audit-request", input)
				if err != nil {
					t.Fatal(err)
				}
				transfer = rawOwnerTransfer(t, raw)
			}
			g5AuthReviewBlockAudit(t, w, action)
			const retryKey = "auth-review-audit-failed-operation"
			var err error
			if action == "owner.transfer.request" {
				_, _, _, err = w.member.RequestOwnerTransfer(ctx, owner, p.ID, retryKey, input)
			} else {
				_, _, _, err = w.member.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID, retryKey)
			}
			if err == nil || !strings.Contains(err.Error(), "g5-review-audit-failure") {
				t.Fatalf("critical audit failure did not abort transfer: %v", err)
			}
			after := g5AuthReviewProject(t, w, owner, p.ID)
			g5AuthReviewOwner(t, after, owner.UserID)
			if after.ProjectVersion != p.ProjectVersion {
				t.Fatalf("audit failure advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
			}
			m, ok := core.ProjectMember(after, member.UserID)
			if !ok || m.Role != "collaborator" || len(m.FunctionRoles) != 1 || m.FunctionRoles[0] != "author" {
				t.Fatalf("audit failure partially changed recipient roles: %+v", m)
			}
			if n := g5AuthReviewCount(t, w, "lingdoc_operations", "key=?", retryKey); n != 0 {
				t.Fatalf("audit failure stored a successful replay: %d", n)
			}
			if action == "owner.transfer.request" {
				if n := g5AuthReviewCount(t, w, "lingdoc_owner_transfers", "project_id=?", p.ID); n != 0 {
					t.Fatalf("failed request persisted pending transfer: %d", n)
				}
				if n := g5AuthReviewCount(t, w, "lingdoc_g5_records",
					"project_id=? AND kind='owner_transfer_origin'", p.ID); n != 0 {
					t.Fatalf("failed request persisted origin proof: %d", n)
				}
			} else {
				var stored ownerTransferRow
				if err := w.db.Where("id=?", transfer.ID).Take(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if stored.Status != "pending" || stored.AcceptedAt != nil {
					t.Fatalf("failed acceptance partially committed transfer: %+v", stored)
				}
			}

			g5AuthReviewDropAuditTrigger(t, w)
			var raw json.RawMessage
			var replay bool
			if action == "owner.transfer.request" {
				raw, _, replay, err = w.member.RequestOwnerTransfer(ctx, owner, p.ID, retryKey, input)
			} else {
				raw, _, replay, err = w.member.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID, retryKey)
			}
			if err != nil || replay {
				t.Fatalf("same-key retry after rollback: replay=%v err=%v", replay, err)
			}
			receipt := rawOwnerTransfer(t, raw)
			if action == "owner.transfer.request" {
				if receipt.Status != "pending" {
					t.Fatalf("request retry receipt: %+v", receipt)
				}
			} else {
				g5AuthReviewOwner(t, g5AuthReviewProject(t, w, member, p.ID), member.UserID)
				if receipt.Status != "accepted" || receipt.AcceptedAt == nil {
					t.Fatalf("accept retry receipt: %+v", receipt)
				}
			}
			if n := g5AuthReviewCount(t, w, "lingdoc_project_audits",
				"project_id=? AND action=?", p.ID, action); n != 1 {
				t.Fatalf("expected one business audit after retry, got %d", n)
			}
		})
	}
}

func TestG5AuthReviewFormerSuccessorDoesNotRegainOldAuthorScope(t *testing.T) {
	w, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	invitation, _ := firstPassInvite(t, w, owner, member, p)
	p = g5AuthReviewAcceptMember(t, w, member, p.ID, invitation)
	raw, _, _, err := w.svc.SaveSpec(ctx, owner, p.ID, "auth-review-axis-spec",
		core.SaveSpecInput{
			ExpectedSpecRevision: 0,
			Fields: map[string]string{
				"research_subject": "synthetic subject",
				"research_goal":    "synthetic goal",
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = w.svc.ActivateProject(ctx, owner, p.ID, "auth-review-axis-activate",
		core.ActivateProjectInput{
			ExpectedSpecRevision:   p.SpecRevision,
			ExpectedProjectVersion: p.ProjectVersion,
			ReviewedProjectVersion: p.ProjectVersion,
		})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	chapters, err := w.svc.ListChapters(ctx, owner, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("active project chapter fixture: %v %v", chapters, err)
	}
	chapter := chapters[0]
	raw, _, _, err = w.member.SetMemberAssignment(ctx, owner, p.ID, member.UserID, "auth-review-axis-assign",
		core.MemberAssignmentInput{
			ExpectedProjectVersion: p.ProjectVersion, Reason: "synthetic scoped author",
			Assignment: core.Member{
				UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"},
				FunctionScopes: map[string][]string{"author": {chapter.ID}},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = w.svc.SaveChapter(ctx, member, p.ID, chapter.ID, "auth-review-axis-positive-write",
		core.SaveChapterInput{
			ExpectedSpecRevision: p.SpecRevision, ExpectedChapterVersionID: chapter.CurrentVersionID,
			BodyMarkdown: "authorized before ownership", SourceIDs: []string{},
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &chapter); err != nil {
		t.Fatal(err)
	}
	p = g5AuthReviewProject(t, w, owner, p.ID)
	w.svc.RequireActivationRead(true)

	transfer := func(from, to core.Actor, prefix string) core.Project {
		t.Helper()
		current := g5AuthReviewProject(t, w, from, p.ID)
		raw, _, _, err := w.member.RequestOwnerTransfer(ctx, from, p.ID, prefix+"-request",
			core.OwnerTransferInput{ExpectedProjectVersion: current.ProjectVersion, ToUserID: to.UserID})
		if err != nil {
			t.Fatal(err)
		}
		pending := rawOwnerTransfer(t, raw)
		if _, _, _, err := w.member.AcceptOwnerTransfer(ctx, to, p.ID, pending.ID, prefix+"-accept"); err != nil {
			t.Fatal(err)
		}
		next := g5AuthReviewProject(t, w, to, p.ID)
		g5AuthReviewOwner(t, next, to.UserID)
		return next
	}
	p = transfer(owner, member, "auth-review-axis-first")
	successor, ok := core.ProjectMember(p, member.UserID)
	if !ok || len(successor.FunctionRoles) != 0 || len(successor.FunctionScopes) != 0 {
		t.Fatalf("successor kept former function grants: %+v", successor)
	}
	p = transfer(member, owner, "auth-review-axis-second")
	former, ok := core.ProjectMember(p, member.UserID)
	if !ok || core.GovernanceRole(former) != "member" || len(former.FunctionRoles) != 0 ||
		len(former.FunctionScopes) != 0 {
		t.Fatalf("former successor recovered previous function grants: %+v", former)
	}
	if err := w.svc.Authorize(ctx, member, p.ID, "write:"+chapter.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("demoted successor regained old Author scope: %v", err)
	}
	if _, _, _, err := w.svc.SaveChapter(ctx, member, p.ID, chapter.ID, "auth-review-axis-forbidden-write",
		core.SaveChapterInput{
			ExpectedSpecRevision: p.SpecRevision, ExpectedChapterVersionID: chapter.CurrentVersionID,
			BodyMarkdown: "forbidden after demotion", SourceIDs: []string{},
		}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("demoted successor wrote old chapter: %v", err)
	}
	after := g5AuthReviewProject(t, w, owner, p.ID)
	if after.ProjectVersion != p.ProjectVersion {
		t.Fatalf("denied old-scope write advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
	}
	if n := g5AuthReviewCount(t, w, "lingdoc_chapter_versions", "project_id=? AND chapter_id=?",
		p.ID, chapter.ID); n != 1 {
		t.Fatalf("denied old-scope write changed persisted chapter versions: %d", n)
	}
}
