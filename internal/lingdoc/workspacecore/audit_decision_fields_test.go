package workspacecore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// The five decision facts of an audit event are first-class columns (#415).
//
// `AuditEvent` declared TenantID / UserID / Role / Capability / Decision for a
// long time, but the table had no column for them: they only rode along inside
// details_json, and the read side did not restore them. Two consequences, both
// measured here:
//
//   - a decision could not be reconstructed from a row without parsing JSON
//     (Spec #130, User Story 6);
//   - the read side's role filter compared an **always empty** `UserID`, so it
//     silently fell back to `actor_id` alone (User Stories 3 and 4).
//
// The second one is why this file writes an event whose `ActorID` is empty and
// whose `UserID` carries the actor: that is the shape the platform write path
// produces (`recordAudit`), and it is the event a member could not see before
// the columns existed.

func TestAuditEventDecisionFactsLandInColumnsAndComeBack(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-decision.db"))
	actor := Actor{TenantID: 91, UserID: "owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, actor)
	svc.audit = &auditSinkStub{}
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-decision-project", CreateProjectInput{
		Name: "audit decision", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	event := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
		Action: "authorization.read", Target: projectID,
		TenantID: actor.TenantID, UserID: actor.UserID, Role: types.TenantRoleAdmin,
		Capability: "read", Decision: "deny", Reason: "not permitted",
		CreatedAt: time.Now().UTC(),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(event); err != nil {
		t.Fatalf("record the audit event: %v", err)
	}

	// The facts live in columns of their own, not inside the JSON details.
	var row auditEventRow
	if err := svc.testDB().Where("id = ?", event.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TenantID != event.TenantID || row.UserID != event.UserID ||
		row.Role != string(event.Role) || row.Capability != event.Capability ||
		row.Decision != event.Decision {
		t.Fatalf("the audit row does not carry the decision facts in columns: %+v", row)
	}

	// And they come back on the read path, so a reader does not have to parse
	// the details JSON to know who asked for what and how it was decided.
	events, err := (gormTransaction{svc.testDB()}).AuditEvents(projectID)
	if err != nil {
		t.Fatalf("read the audit events: %v", err)
	}
	var found *AuditEvent
	for i := range events {
		if events[i].ID == event.ID {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("the written audit event did not come back: %+v", events)
	}
	if found.TenantID != event.TenantID || found.UserID != event.UserID ||
		found.Role != event.Role || found.Capability != event.Capability ||
		found.Decision != event.Decision {
		t.Fatalf("the read back event lost the decision facts: %+v", found)
	}
}

func TestAuditEventReadSideRoleFilterUsesTheRestoredUserID(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-decision-filter.db"))
	owner := Actor{TenantID: 92, UserID: "owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, owner)
	svc.audit = &auditSinkStub{}
	raw, _, _, err := svc.CreateProject(context.Background(), owner, "audit-decision-filter-project", CreateProjectInput{
		Name: "audit decision filter", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	member := Actor{TenantID: owner.TenantID, UserID: "member-x"}
	seedTenantMember(t, svc, member)
	if err := (gormTransaction{svc.testDB()}).EnsureMember(projectID, member.UserID, "collaborator"); err != nil {
		t.Fatalf("add the project member: %v", err)
	}

	// The shape the platform write path produces: the actor lives in UserID,
	// ActorID is empty. Before the columns existed this event was invisible to
	// the member that wrote it.
	mine := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, Action: "authorization.read",
		Target: projectID, TenantID: member.TenantID, UserID: member.UserID,
		Role: types.TenantRoleContributor, Capability: "read", Decision: "allow",
		CreatedAt: time.Now().UTC(),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(mine); err != nil {
		t.Fatalf("record the member's own audit event: %v", err)
	}
	// Somebody else's event: the member must not see it.
	other := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, Action: "authorization.read",
		Target: projectID, TenantID: owner.TenantID, UserID: owner.UserID,
		Role: types.TenantRoleOwner, Capability: "read", Decision: "allow",
		CreatedAt: time.Now().UTC().Add(time.Second),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(other); err != nil {
		t.Fatalf("record the owner's audit event: %v", err)
	}

	visible, err := svc.ListAuditEvents(context.Background(), member, projectID)
	if err != nil {
		t.Fatalf("read the audit events as a member: %v", err)
	}
	sawMine, sawOther := false, false
	for _, item := range visible {
		if item.ID == mine.ID {
			sawMine = true
		}
		if item.ID == other.ID {
			sawOther = true
		}
	}
	if !sawMine {
		t.Fatalf("the member cannot see the event it wrote: %+v", visible)
	}
	if sawOther {
		t.Fatalf("the member sees another member's event: %+v", visible)
	}
}

func TestAuditEventDecisionColumnsKeepTheirDefaultsForOlderRows(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-decision-defaults.db"))
	actor := Actor{TenantID: 93, UserID: "owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, actor)
	svc.audit = &auditSinkStub{}
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-decision-defaults-project", CreateProjectInput{
		Name: "audit decision defaults", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	// A row that predates the columns carries the defaults, not zeros that
	// pretend the decision was "deny" by an empty role.
	event := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
		Action: "authorization.read", Target: projectID, CreatedAt: time.Now().UTC(),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(event); err != nil {
		t.Fatalf("record the audit event: %v", err)
	}
	var row auditEventRow
	if err := svc.testDB().Where("id = ?", event.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TenantID != 0 || row.UserID != "" || row.Role != "" ||
		row.Capability != "" || row.Decision != "" {
		t.Fatalf("the audit row did not keep the column defaults: %+v", row)
	}
	events, err := (gormTransaction{svc.testDB()}).AuditEvents(projectID)
	if err != nil {
		t.Fatalf("read the audit events: %v", err)
	}
	var found *AuditEvent
	for i := range events {
		if events[i].ID == event.ID {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("the written audit event did not come back: %+v", events)
	}
	if found.TenantID != 0 || found.UserID != "" || found.Role != "" ||
		found.Capability != "" || found.Decision != "" {
		t.Fatalf("the read back event invented decision facts: %+v", found)
	}
}
