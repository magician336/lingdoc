package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// #130 US-03/US-04/US-11: the audit query is role filtered — the Owner sees
// the whole project history, an ordinary member only their own rows — and a
// stranger is refused by the project authorization with the same not-found
// shape used everywhere else. The writer shape is pinned by the
// workspacecore audit format tests; this fixture only needs rows from two
// distinct actors to exercise the read-side filter.
func TestAuditQueryFiltersByRoleAndRefusesStrangers(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g5-audit-query.db"))
	owner := core.Actor{TenantID: 93, UserID: "owner"}
	member := core.Actor{TenantID: 93, UserID: "member"}
	stranger := core.Actor{TenantID: 93, UserID: "stranger"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, member)
	seedTenantMember(t, w.db, stranger)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "g5-audit-query", core.CreateProjectInput{
		Name:       "G5 audit query",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := w.member.SaveMembers(context.Background(), owner, project.ID, "g5-audit-query-members", core.SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members:                []core.Member{{UserID: member.UserID, GovernanceRole: "member"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, actorID := range []string{owner.UserID, member.UserID} {
		if err := w.db.Exec(
			"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?)",
			uuid.NewString(), project.ID, actorID, "project_spec.save", project.ID, "{}", time.Now().UTC(),
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	ownerEvents, err := w.svc.ListAuditEvents(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerEvents) < 3 {
		t.Fatalf("owner audit rows = %d, want the whole project history", len(ownerEvents))
	}
	memberEvents, err := w.svc.ListAuditEvents(context.Background(), member, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberEvents) != 1 || memberEvents[0].ActorID != member.UserID {
		t.Fatalf("member audit rows = %+v, want only their own row", memberEvents)
	}
	if _, err := w.svc.ListAuditEvents(context.Background(), stranger, project.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stranger audit query error = %v, want %v", err, core.ErrNotFound)
	}
}
