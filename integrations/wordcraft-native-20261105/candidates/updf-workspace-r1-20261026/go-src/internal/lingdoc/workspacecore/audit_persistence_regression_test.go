package workspacecore

import (
	"context"
	"path/filepath"
	"testing"
)

// Reproduces repeated authorization reads on the real SQLite repository.
// The G5 event previously lost its identity and decision in the G1 audit row.
func TestRepeatedAuthorizationPersistsDistinctAuditDecisions(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-project", CreateProjectInput{Name: "audit", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID
	svc.audit = &auditSinkStub{}
	for i := 0; i < 2; i++ {
		if err := svc.Authorize(context.Background(), actor, projectID, "read"); err != nil {
			t.Fatal(err)
		}
	}
	var rows []auditEventRow
	if err := svc.testDB().Where("project_id = ? AND action = ?", projectID, "authorization.read").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d persisted authorization events, want 2", len(rows))
	}
	if rows[0].ID == "" || rows[0].ID == rows[1].ID {
		t.Fatal("audit identities missing or reused")
	}
	for _, row := range rows {
		if row.ActorID != actor.UserID || row.Target != projectID || row.CreatedAt.IsZero() {
			t.Fatalf("incomplete audit: %+v", row)
		}
	}
	events, err := gormTransaction{svc.testDB()}.AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Action == "authorization.read" && event.Details["decision"] != "allow" {
			t.Fatalf("decision not retained: %+v", event)
		}
	}
}

func TestCreateProjectDoesNotWriteTenantAuditWithEmptyProjectID(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "create-audit.db"))
	svc.audit = &auditSinkStub{}
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)

	if _, _, _, err := svc.CreateProject(context.Background(), actor, "create-audit-project", CreateProjectInput{
		Name: "create audit", TemplateID: "template-demo",
	}); err != nil {
		t.Fatalf("create project with audit enabled: %v", err)
	}

	var emptyProjectID int64
	if err := svc.testDB().Model(&auditEventRow{}).Where("project_id = ?", "").Count(&emptyProjectID).Error; err != nil {
		t.Fatalf("count empty-project audit rows: %v", err)
	}
	if emptyProjectID != 0 {
		t.Fatalf("created %d audit rows without a project foreign key", emptyProjectID)
	}
}
