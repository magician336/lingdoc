package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// These tests use isolated SQLite databases created by firstPassMemberFixture.
// They exercise persisted domain results; none represents a real human receipt.
func authReviewProject(t *testing.T, s *Service, a Actor, pid string) Project {
	t.Helper()
	p, err := s.GetProject(context.Background(), a, pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func authReviewAcceptMember(t *testing.T, s *Service, member Actor, pid string, invitation MemberInvitation) Project {
	t.Helper()
	if _, _, _, err := s.AcceptProjectInvitation(context.Background(), member, pid, invitation.ID,
		"auth-review-accept-member", AcceptMemberInput{ExpectedInvitationVersion: invitation.Version}); err != nil {
		t.Fatal(err)
	}
	return authReviewProject(t, s, member, pid)
}

func authReviewCount(t *testing.T, s *Service, table, where string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := s.testDB().Table(table).Where(where, args...).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func authReviewOwner(t *testing.T, p Project, expected string) {
	t.Helper()
	var owners []string
	for _, m := range p.Members {
		if governanceRole(m) == "owner" {
			owners = append(owners, m.UserID)
		}
	}
	if len(owners) != 1 || owners[0] != expected {
		t.Fatalf("expected one Owner %q, got %v", expected, owners)
	}
}

func authReviewDropAuditTrigger(t *testing.T, s *Service) {
	t.Helper()
	if err := s.testDB().Exec("DROP TRIGGER IF EXISTS g5_review_block_audit").Error; err != nil {
		t.Fatal(err)
	}
}

func authReviewBlockAudit(t *testing.T, s *Service, action string) {
	t.Helper()
	if action != "owner.transfer.request" && action != "owner.transfer.accept" {
		t.Fatalf("unsupported audit injection action: %q", action)
	}
	// Restrict the failure to the business audit. A fail-open authorization log
	// must not be enough to satisfy the critical transfer audit requirement.
	sql := "CREATE TRIGGER g5_review_block_audit BEFORE INSERT ON lingdoc_project_audits " +
		"WHEN NEW.action = '" + action + "' BEGIN SELECT RAISE(ABORT, 'g5-review-audit-failure'); END"
	if err := s.testDB().Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.testDB().Exec("DROP TRIGGER IF EXISTS g5_review_block_audit").Error; err != nil {
			t.Errorf("remove isolated audit trigger: %v", err)
		}
	})
}

func TestAuthReviewListOmitsInvitedRemovedAndPendingTenantRecovery(t *testing.T) {
	s, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	const sentinel = "G5_REVIEW_PRIVATE_SPEC_8f16"
	raw, _, _, err := s.SaveSpec(ctx, owner, p.ID, "auth-review-private-spec",
		SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": sentinel}})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)

	assertList := func(a Actor, visible bool) {
		t.Helper()
		list, truncated, err := s.ListProjects(ctx, a)
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

	invitation, _ := firstPassInvite(t, s, owner, member, p)
	assertList(member, false)
	p = authReviewAcceptMember(t, s, member, p.ID, invitation)
	assertList(member, true)

	if err := s.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?",
		member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.CaptureTenantSuspension(ctx, member.TenantID, member.UserID); err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?",
		member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	assertList(member, false)
	if _, err := s.GetProject(ctx, member, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant restoration silently granted project read: %v", err)
	}
	p = authReviewProject(t, s, owner, p.ID)
	if _, _, _, err := s.ChangeMemberState(ctx, owner, p.ID, member.UserID, "auth-review-explicit-restore",
		MemberLifecycleInput{ExpectedProjectVersion: p.ProjectVersion, Action: "restore_tenant", Reason: "synthetic explicit recovery"}); err != nil {
		t.Fatal(err)
	}
	assertList(member, true)

	p = authReviewProject(t, s, owner, p.ID)
	if _, _, _, err := s.ChangeMemberState(ctx, owner, p.ID, member.UserID, "auth-review-remove-member",
		MemberLifecycleInput{ExpectedProjectVersion: p.ProjectVersion, Action: "remove", Reason: "synthetic revocation"}); err != nil {
		t.Fatal(err)
	}
	assertList(member, false)
	assertList(owner, true)
}

func TestAuthReviewEmptyAuthorCannotWriteGlobalSpec(t *testing.T) {
	s, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	invitation, _ := firstPassInvite(t, s, owner, member, p)
	p = authReviewAcceptMember(t, s, member, p.ID, invitation)
	before := authReviewProject(t, s, owner, p.ID)

	if err := s.Authorize(ctx, member, p.ID, "write"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty Author scope authorized global write: %v", err)
	}
	if _, _, _, err := s.SaveSpec(ctx, member, p.ID, "auth-review-forbidden-spec",
		SaveSpecInput{ExpectedSpecRevision: before.SpecRevision, Fields: map[string]string{"research_subject": "forbidden overwrite"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty Author scope changed Spec: %v", err)
	}
	if _, _, _, err := s.CreateDraftCandidate(ctx, member, p.ID, "auth-review-forbidden-global-draft",
		DraftCandidateInput{Kind: "research", Title: "unscoped", Content: "forbidden draft"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty Author scope created project-wide draft: %v", err)
	}
	after := authReviewProject(t, s, owner, p.ID)
	if after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision ||
		after.Spec["research_subject"] != before.Spec["research_subject"] {
		t.Fatalf("denied writes changed persisted project: before=%+v after=%+v", before, after)
	}
	if n := authReviewCount(t, s, "lingdoc_operations", "user_id=? AND operation='saveSpec'", member.UserID); n != 0 {
		t.Fatalf("denied global write recorded a successful replay: %d", n)
	}
}

func TestAuthReviewRecoveryChecksCurrentPredecessorAndRequester(t *testing.T) {
	for _, change := range []string{"predecessor-restored", "requester-downgraded"} {
		t.Run(change, func(t *testing.T) {
			s, owner, member, p := firstPassMemberFixture(t)
			ctx := context.Background()
			invitation, _ := firstPassInvite(t, s, owner, member, p)
			p = authReviewAcceptMember(t, s, member, p.ID, invitation)
			s.RequireActivationRead(true)
			requester := Actor{TenantID: owner.TenantID, UserID: "recovery-requester", Role: types.TenantRoleOwner}
			seedTenantMember(t, s, requester)

			if err := s.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?",
				owner.TenantID, owner.UserID).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.CaptureTenantSuspension(ctx, owner.TenantID, owner.UserID); err != nil {
				t.Fatal(err)
			}
			raw, _, _, err := s.RequestOwnerTransfer(ctx, requester, p.ID, "auth-review-recovery-request",
				OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: member.UserID})
			if err != nil {
				t.Fatal(err)
			}
			transfer := rawOwnerTransfer(t, raw)
			wantError := ErrInvalidState
			if change == "predecessor-restored" {
				if err := s.testDB().Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?",
					owner.TenantID, owner.UserID).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.testDB().Exec("UPDATE tenant_members SET role='contributor' WHERE tenant_id=? AND user_id=?",
					requester.TenantID, requester.UserID).Error; err != nil {
					t.Fatal(err)
				}
				wantError = ErrNotFound
			}
			if _, _, _, err := s.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID,
				"auth-review-stale-recovery-accept"); !errors.Is(err, wantError) {
				t.Fatalf("stale recovery accepted: %v, want %v", err, wantError)
			}
			after := authReviewProject(t, s, member, p.ID)
			authReviewOwner(t, after, owner.UserID)
			if after.ProjectVersion != p.ProjectVersion {
				t.Fatalf("denied recovery advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
			}
			var stored ownerTransferRow
			if err := s.testDB().Where("id=?", transfer.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.Status != "pending" || stored.AcceptedAt != nil {
				t.Fatalf("denied recovery modified pending transfer: %+v", stored)
			}
			if n := authReviewCount(t, s, "lingdoc_operations", "key=?", "auth-review-stale-recovery-accept"); n != 0 {
				t.Fatalf("denied recovery stored successful replay: %d", n)
			}
		})
	}
}

func TestAuthReviewOwnerTransferBusinessAuditRollsBackAtomically(t *testing.T) {
	for _, action := range []string{"owner.transfer.request", "owner.transfer.accept"} {
		t.Run(action, func(t *testing.T) {
			s, owner, member, p := firstPassMemberFixture(t)
			ctx := context.Background()
			invitation, _ := firstPassInvite(t, s, owner, member, p)
			p = authReviewAcceptMember(t, s, member, p.ID, invitation)
			s.RequireActivationRead(true)
			input := OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: member.UserID}
			var transfer OwnerTransfer

			if action == "owner.transfer.accept" {
				raw, _, _, err := s.RequestOwnerTransfer(ctx, owner, p.ID, "auth-review-audit-request", input)
				if err != nil {
					t.Fatal(err)
				}
				transfer = rawOwnerTransfer(t, raw)
			}
			authReviewBlockAudit(t, s, action)
			const retryKey = "auth-review-audit-failed-operation"
			var err error
			if action == "owner.transfer.request" {
				_, _, _, err = s.RequestOwnerTransfer(ctx, owner, p.ID, retryKey, input)
			} else {
				_, _, _, err = s.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID, retryKey)
			}
			if err == nil || !strings.Contains(err.Error(), "g5-review-audit-failure") {
				t.Fatalf("critical audit failure did not abort transfer: %v", err)
			}
			after := authReviewProject(t, s, owner, p.ID)
			authReviewOwner(t, after, owner.UserID)
			if after.ProjectVersion != p.ProjectVersion {
				t.Fatalf("audit failure advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
			}
			m, ok := projectMember(after, member.UserID)
			if !ok || m.Role != "collaborator" || len(m.FunctionRoles) != 1 || m.FunctionRoles[0] != "author" {
				t.Fatalf("audit failure partially changed recipient roles: %+v", m)
			}
			if n := authReviewCount(t, s, "lingdoc_operations", "key=?", retryKey); n != 0 {
				t.Fatalf("audit failure stored a successful replay: %d", n)
			}
			if action == "owner.transfer.request" {
				if n := authReviewCount(t, s, "lingdoc_owner_transfers", "project_id=?", p.ID); n != 0 {
					t.Fatalf("failed request persisted pending transfer: %d", n)
				}
				if n := authReviewCount(t, s, "lingdoc_g5_records",
					"project_id=? AND kind='owner_transfer_origin'", p.ID); n != 0 {
					t.Fatalf("failed request persisted origin proof: %d", n)
				}
			} else {
				var stored ownerTransferRow
				if err := s.testDB().Where("id=?", transfer.ID).Take(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if stored.Status != "pending" || stored.AcceptedAt != nil {
					t.Fatalf("failed acceptance partially committed transfer: %+v", stored)
				}
			}

			authReviewDropAuditTrigger(t, s)
			var raw json.RawMessage
			var replay bool
			if action == "owner.transfer.request" {
				raw, _, replay, err = s.RequestOwnerTransfer(ctx, owner, p.ID, retryKey, input)
			} else {
				raw, _, replay, err = s.AcceptOwnerTransfer(ctx, member, p.ID, transfer.ID, retryKey)
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
				authReviewOwner(t, authReviewProject(t, s, member, p.ID), member.UserID)
				if receipt.Status != "accepted" || receipt.AcceptedAt == nil {
					t.Fatalf("accept retry receipt: %+v", receipt)
				}
			}
			if n := authReviewCount(t, s, "lingdoc_project_audits",
				"project_id=? AND action=?", p.ID, action); n != 1 {
				t.Fatalf("expected one business audit after retry, got %d", n)
			}
		})
	}
}

func TestAuthReviewFormerSuccessorDoesNotRegainOldAuthorScope(t *testing.T) {
	s, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	invitation, _ := firstPassInvite(t, s, owner, member, p)
	p = authReviewAcceptMember(t, s, member, p.ID, invitation)
	raw, _, _, err := s.SaveSpec(ctx, owner, p.ID, "auth-review-axis-spec",
		SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "synthetic subject", "research_goal": "synthetic goal"}})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = s.ActivateProject(ctx, owner, p.ID, "auth-review-axis-activate",
		ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	chapters, err := s.ListChapters(ctx, owner, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("active project chapter fixture: %v %v", chapters, err)
	}
	chapter := chapters[0]
	raw, _, _, err = s.SetMemberAssignment(ctx, owner, p.ID, member.UserID, "auth-review-axis-assign",
		MemberAssignmentInput{ExpectedProjectVersion: p.ProjectVersion, Reason: "synthetic scoped author",
			Assignment: Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"},
				FunctionScopes: map[string][]string{"author": {chapter.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = s.SaveChapter(ctx, member, p.ID, chapter.ID, "auth-review-axis-positive-write",
		SaveChapterInput{ExpectedSpecRevision: p.SpecRevision, ExpectedChapterVersionID: chapter.CurrentVersionID,
			BodyMarkdown: "authorized before ownership", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &chapter); err != nil {
		t.Fatal(err)
	}
	p = authReviewProject(t, s, owner, p.ID)
	s.RequireActivationRead(true)

	transfer := func(from, to Actor, prefix string) Project {
		t.Helper()
		current := authReviewProject(t, s, from, p.ID)
		raw, _, _, err := s.RequestOwnerTransfer(ctx, from, p.ID, prefix+"-request",
			OwnerTransferInput{ExpectedProjectVersion: current.ProjectVersion, ToUserID: to.UserID})
		if err != nil {
			t.Fatal(err)
		}
		pending := rawOwnerTransfer(t, raw)
		if _, _, _, err := s.AcceptOwnerTransfer(ctx, to, p.ID, pending.ID, prefix+"-accept"); err != nil {
			t.Fatal(err)
		}
		next := authReviewProject(t, s, to, p.ID)
		authReviewOwner(t, next, to.UserID)
		return next
	}
	p = transfer(owner, member, "auth-review-axis-first")
	successor, ok := projectMember(p, member.UserID)
	if !ok || len(successor.FunctionRoles) != 0 || len(successor.FunctionScopes) != 0 {
		t.Fatalf("successor kept former function grants: %+v", successor)
	}
	p = transfer(member, owner, "auth-review-axis-second")
	former, ok := projectMember(p, member.UserID)
	if !ok || governanceRole(former) != "member" || len(former.FunctionRoles) != 0 || len(former.FunctionScopes) != 0 {
		t.Fatalf("former successor recovered previous function grants: %+v", former)
	}
	if err := s.Authorize(ctx, member, p.ID, "write:"+chapter.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("demoted successor regained old Author scope: %v", err)
	}
	if _, _, _, err := s.SaveChapter(ctx, member, p.ID, chapter.ID, "auth-review-axis-forbidden-write",
		SaveChapterInput{ExpectedSpecRevision: p.SpecRevision, ExpectedChapterVersionID: chapter.CurrentVersionID,
			BodyMarkdown: "forbidden after demotion", SourceIDs: []string{}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("demoted successor wrote old chapter: %v", err)
	}
	after := authReviewProject(t, s, owner, p.ID)
	if after.ProjectVersion != p.ProjectVersion {
		t.Fatalf("denied old-scope write advanced project: %d -> %d", p.ProjectVersion, after.ProjectVersion)
	}
	if n := authReviewCount(t, s, "lingdoc_chapter_versions", "project_id=? AND chapter_id=?", p.ID, chapter.ID); n != 1 {
		t.Fatalf("denied old-scope write changed persisted chapter versions: %d", n)
	}
}
