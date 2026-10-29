package membership

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// The member and owner context left the aggregate (Spec #47, ticket #268), so
// the invitation lifecycle tests live here, next to the operations they drive.
//
// The assertions are unchanged: only the owner of each call moved. A workspace
// operation is asked of the aggregate (w.svc), a member and owner operation of
// the member and owner service (w.member), and a row is read through the
// fixture's own database handle (w.db).

// memberRecordRow mirrors the workspace's own record row so the test can compare
// the invitation record before and after a denied acceptance.
//
// The workspace's row type is unexported and the member context reaches
// records through its own storage port, so the test states the columns it
// compares.
type memberRecordRow struct {
	ProjectID   string `gorm:"primaryKey"`
	Kind        string `gorm:"primaryKey"`
	ID          string `gorm:"primaryKey"`
	Version     int64
	PayloadJSON string
}

func (memberRecordRow) TableName() string { return "lingdoc_g5_records" }

// discardAfterReadRepository discards the project at the moment the test
// chooses: either before the acceptance starts, or after the read-only
// preflight transaction has returned.
//
// The transaction underneath is the production one; only the project's discarded
// timestamp changes, and it changes in the real table. The workspace's record
// port therefore keeps working, which is what lets the acceptance reach its
// write path at all.
//
// The archived status is not the state under test: the storage vocabulary does
// not allow it, so it can only be reached by replacing the transaction, and the
// workspace's record port asserts on the production transaction type that the
// member context cannot satisfy from outside the workspace module. The
// discarded project is the production-reachable form of the same fact.
type discardAfterReadRepository struct {
	core.Repository
	db        *gorm.DB
	projectID string
	afterRead bool
	fired     bool
}

func (r *discardAfterReadRepository) Transaction(ctx context.Context, options core.TransactionOptions, fn func(core.Transaction) error) error {
	if err := r.Repository.Transaction(ctx, options, fn); err != nil {
		return err
	}
	if options.ReadOnly && r.afterRead && !r.fired {
		r.fired = true
		if err := r.discard(); err != nil {
			return err
		}
	}
	return nil
}

// discard writes the discarded timestamp into the project row.
func (r *discardAfterReadRepository) discard() error {
	return r.db.Exec("UPDATE lingdoc_projects SET discarded_at = ? WHERE id = ?",
		time.Now().UTC(), r.projectID).Error
}

// useRepository rebuilds the aggregate and the member and owner service over a
// different repository, so a test can inject a repository that changes what the
// transaction reports.
//
// Both services share the one database, so a member write the aggregate reads
// back is still the same row.
func (w *testWorkspace) useRepository(repository core.Repository) {
	w.t.Helper()
	w.svc = core.NewServiceWithSources(repository, core.ContractDemoTemplate{}, &fakeSources{})
	w.member = newServiceForTest(w.t, w.svc, repository)
}

func TestG5InvitationIdempotencyVersionAndAuditRecovery(t *testing.T) {
	w, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	// CreateProject persists the template_binding sidecar (source-kind
	// persistence), so the rollback invariant is a DELTA of zero g5 rows over
	// the invitation, not an absolute zero.
	g5Before := authReviewCount(t, w, "lingdoc_g5_records", "project_id=?", p.ID)
	input := core.InviteMemberInput{ExpectedProjectVersion: p.ProjectVersion, Reason: "synthetic transaction",
		Assignment: core.Member{UserID: member.UserID, GovernanceRole: "admin", FunctionRoles: []string{"author"},
			FunctionScopes: map[string][]string{"author": {}}}}
	installAuditFailureTrigger(t, w.db, "member.invite")
	if _, _, _, err := w.member.InviteProjectMember(ctx, owner, p.ID, "invite-atomic", input); err == nil {
		t.Fatal("invitation committed despite failed business audit")
	}
	after := authReviewProject(t, w, owner, p.ID)
	if after.ProjectVersion != p.ProjectVersion || len(after.Members) != len(p.Members) {
		t.Fatal("failed invitation changed version or membership")
	}
	if n := authReviewCount(t, w, "lingdoc_g5_records", "project_id=?", p.ID); n != g5Before {
		t.Fatalf("failed invitation changed g5 rows: before=%d after=%d", g5Before, n)
	}
	if n := authReviewCount(t, w, "lingdoc_operations", "target=? AND key='invite-atomic'", p.ID); n != 0 {
		t.Fatalf("failed invitation left operations rows: %d", n)
	}
	if err := w.db.Exec("DROP TRIGGER fail_lingdoc_audit_member_invite").Error; err != nil {
		t.Fatal(err)
	}
	raw, _, replayed, err := w.member.InviteProjectMember(ctx, owner, p.ID, "invite-atomic", input)
	if err != nil || replayed {
		t.Fatalf("same key could not recover after rollback: %v replay=%v", err, replayed)
	}
	var invitation core.MemberInvitation
	if err := json.Unmarshal(raw, &invitation); err != nil {
		t.Fatal(err)
	}
	if _, _, replayed, err := w.member.InviteProjectMember(ctx, owner, p.ID, "invite-atomic", input); err != nil || !replayed {
		t.Fatalf("successful invitation did not replay: %v %v", replayed, err)
	}
	changed := input
	changed.Reason = "different body"
	if _, _, _, err := w.member.InviteProjectMember(ctx, owner, p.ID, "invite-atomic", changed); !errors.Is(err, core.ErrIdempotencyConflict) {
		t.Fatalf("key reuse error=%v", err)
	}
	if _, _, _, err := w.member.InviteProjectMember(ctx, owner, p.ID, "invite-stale-project", input); !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("stale project error=%v", err)
	}
	installAuditFailureTrigger(t, w.db, "member.accept")
	before := authReviewProject(t, w, owner, p.ID)
	accept := core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version}
	if _, _, _, err := w.member.AcceptProjectInvitation(ctx, member, p.ID, invitation.ID, "accept-atomic", accept); err == nil {
		t.Fatal("acceptance committed despite failed business audit")
	}
	after = authReviewProject(t, w, owner, p.ID)
	if after.ProjectVersion != before.ProjectVersion {
		t.Fatal("failed acceptance advanced project")
	}
	if _, err := w.svc.GetProject(ctx, member, p.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("failed acceptance granted content access: %v", err)
	}
	if err := w.db.Exec("DROP TRIGGER fail_lingdoc_audit_member_accept").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, replayed, err := w.member.AcceptProjectInvitation(ctx, member, p.ID, invitation.ID, "accept-atomic", accept); err != nil || replayed {
		t.Fatalf("acceptance recovery failed: %v replay=%v", err, replayed)
	}
	// Admin + Author is a union, not an implicit global writing grant.
	if err := w.svc.Authorize(ctx, member, p.ID, "manage"); err != nil {
		t.Fatalf("Admin lost governance capability: %v", err)
	}
	if err := w.svc.Authorize(ctx, member, p.ID, "write"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Admin + empty Author granted global write: %v", err)
	}
}

func TestG5InvitationArchiveRejectsPreflightWriteRaceAndReplay(t *testing.T) {
	for _, stage := range []string{"before-preflight", "after-preflight", "before-replay"} {
		t.Run(stage, func(t *testing.T) {
			w, owner, member, p := firstPassMemberFixture(t)
			ctx := context.Background()
			invitation, p := firstPassInvite(t, w, owner, member, p)
			db := w.db
			const key = "archive-accept-invitation"
			input := core.AcceptMemberInput{ExpectedInvitationVersion: invitation.Version}
			if stage == "before-replay" {
				if _, _, _, err := w.member.AcceptProjectInvitation(ctx, member, p.ID, invitation.ID, key, input); err != nil {
					t.Fatal(err)
				}
				p = authReviewProject(t, w, owner, p.ID)
			}
			var invitationBefore memberRecordRow
			if err := db.Where("project_id=? AND kind='invitation' AND id=?", p.ID, invitation.ID).Take(&invitationBefore).Error; err != nil {
				t.Fatal(err)
			}
			count := func(table string) int64 {
				t.Helper()
				var n int64
				query := db.Table(table)
				if table == "lingdoc_operations" {
					query = query.Where("tenant_id=? AND (target=? OR target LIKE ?)", owner.TenantID, p.ID, p.ID+"/%")
				} else {
					query = query.Where("project_id=?", p.ID)
				}
				if err := query.Count(&n).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}
			auditsBefore, operationsBefore := count("lingdoc_project_audits"), count("lingdoc_operations")
			discarding := &discardAfterReadRepository{Repository: core.NewGORMRepository(w.db),
				db: w.db, projectID: p.ID, afterRead: stage == "after-preflight"}
			w.useRepository(discarding)
			if stage != "after-preflight" {
				if err := discarding.discard(); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, replayed, err := w.member.AcceptProjectInvitation(ctx, member, p.ID, invitation.ID, key, input); !errors.Is(err, core.ErrInvalidState) || replayed {
				t.Fatalf("archived acceptance: err=%v replayed=%v", err, replayed)
			}
			after := authReviewProject(t, w, owner, p.ID)
			if after.DiscardedAt == nil || after.ProjectVersion != p.ProjectVersion || !reflect.DeepEqual(after.Members, p.Members) {
				t.Fatalf("acceptance changed archived project: before=%+v after=%+v", p, after)
			}
			var invitationAfter memberRecordRow
			if err := db.Where("project_id=? AND kind='invitation' AND id=?", p.ID, invitation.ID).Take(&invitationAfter).Error; err != nil {
				t.Fatal(err)
			}
			if invitationAfter.Version != invitationBefore.Version || invitationAfter.PayloadJSON != invitationBefore.PayloadJSON {
				t.Fatalf("denied acceptance changed invitation: before=%+v after=%+v", invitationBefore, invitationAfter)
			}
			if count("lingdoc_project_audits") != auditsBefore || count("lingdoc_operations") != operationsBefore {
				t.Fatal("denied acceptance wrote a business audit or successful replay")
			}
		})
	}
}
