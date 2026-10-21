package workspacecore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The audit action column carries a **category**. Two shapes exist in the
// history:
//
//   - the current shape: `authorization.<kind>` — the category only;
//   - the older shape: `authorization.<kind>:<resource>` — the category with
//     the resource it applied to, written before #253 moved the resource into
//     the details.
//
// The write side only produces the current shape (#253, and the width guard in
// audit_action_width_test.go). The read side must stay correct across both
// shapes: a reader that matches the full action string silently loses the old
// rows.
//
// These tests pin that: both shapes come back from the read side, with their
// action strings passed through unchanged, and a reader that matches the
// action **family** sees both.
//
// The dialect does not matter here — the read side is the same query on both
// — so the fixture is the SQLite one.
func TestAuditReadSideReturnsBothActionShapes(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-format.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-format-project", CreateProjectInput{
		Name: "audit format", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	// The current shape: the category only.
	const currentAction = "authorization.issue-dismiss-blocking"
	// The older shape: the category with the resource it applied to. It is
	// short enough to stay inside the bounded column, so a historical row of
	// this shape is still writable — and still has to be readable.
	const legacyAction = "authorization.issue-dismiss-blocking:5013efc8"

	for _, action := range []string{currentAction, legacyAction} {
		if err := insertAuditCategory(t, svc.testDB(), projectID, actor.UserID, action); err != nil {
			t.Fatalf("insert audit row %q: %v", action, err)
		}
	}

	// The read side returns both, with their action strings unchanged. A
	// reader that filtered on the full action string would lose the old row.
	events, err := gormTransaction{svc.testDB()}.AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, event := range events {
		seen[event.Action]++
	}
	if seen[currentAction] != 1 {
		t.Fatalf("current-shape audit rows = %d, want 1 (seen %v)", seen[currentAction], seen)
	}
	if seen[legacyAction] != 1 {
		t.Fatalf("legacy-shape audit rows = %d, want 1 (seen %v)", seen[legacyAction], seen)
	}

	// A reader that matches the action **family** sees both shapes. This is
	// the rule the ADR states for every consumer of the audit feed.
	var familyRows int64
	if err := svc.testDB().Table("lingdoc_project_audits").
		Where("project_id = ? AND action LIKE ?", projectID, "authorization.issue-dismiss-blocking%").
		Count(&familyRows).Error; err != nil {
		t.Fatal(err)
	}
	if familyRows != 2 {
		t.Fatalf("rows matched by action family = %d, want 2", familyRows)
	}
}

// The write side produces the current shape only, and never carries the
// resource in the category column. The resource stays in the details, so a
// reader that needs it can still get it.
//
// This is the write half of the guarantee in
// docs/adr/0010-audit-action-format-and-read-sides.md; the width bound itself
// is pinned in audit_action_width_test.go.
func TestAuditWriterEmitsTheCurrentShapeOnly(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-write-shape.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-write-shape-project", CreateProjectInput{
		Name: "audit write shape", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID
	svc.audit = &auditSinkStub{}
	if err := svc.Authorize(context.Background(), actor, projectID, "read"); err != nil {
		t.Fatal(err)
	}

	events, err := gormTransaction{svc.testDB()}.AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if !strings.HasPrefix(event.Action, "authorization.") {
			continue
		}
		found = true
		if strings.Contains(event.Action, ":") {
			t.Fatalf("authorization audit action %q carries a resource", event.Action)
		}
		if event.Details["capability"] == nil {
			t.Fatalf("authorization audit action %q lost its capability in the details: %+v", event.Action, event.Details)
		}
	}
	if !found {
		t.Fatal("no authorization audit row landed")
	}
}

// The audit feed must not carry a category that a reader cannot match: the
// column is bounded, and the writer keeps the category short.
func TestAuditActionColumnStaysWithinItsBound(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-bound.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-bound-project", CreateProjectInput{
		Name: "audit bound", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID
	svc.audit = &auditSinkStub{}
	if err := svc.Authorize(context.Background(), actor, projectID, "read"); err != nil {
		t.Fatal(err)
	}
	events, err := gormTransaction{svc.testDB()}.AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if len(event.Action) > auditCategoryWidth {
			t.Fatalf("audit action %q is %d characters, the column holds %d", event.Action, len(event.Action), auditCategoryWidth)
		}
	}
}
