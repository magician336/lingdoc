package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestMissingPermissionSchemaFailsClosedOnRead(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "workspace.db"))
	actor := Actor{TenantID: 1, UserID: "owner"}
	seedTenantMember(t, svc, actor)

	raw, _, _, err := svc.CreateProject(context.Background(), actor, "permission-schema-read", CreateProjectInput{Name: "schema", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project := asProject(t, raw)
	if err := svc.testDB().Exec("DROP TABLE lingdoc_member_permissions").Error; err != nil {
		t.Fatalf("drop permission table: %v", err)
	}

	if _, err := svc.GetProject(context.Background(), actor, project.ID); !errors.Is(err, ErrPermissionSchemaUnavailable) {
		t.Fatalf("get project error = %v, want ErrPermissionSchemaUnavailable", err)
	}
}

func TestMissingPermissionSchemaFailsClosedOnWrite(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "workspace.db"))
	actor := Actor{TenantID: 1, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	if err := svc.testDB().Exec("DROP TABLE lingdoc_member_permissions").Error; err != nil {
		t.Fatalf("drop permission table: %v", err)
	}

	_, _, _, err := svc.CreateProject(context.Background(), actor, "permission-schema-write", CreateProjectInput{Name: "schema", TemplateID: "template-demo"})
	if !errors.Is(err, ErrPermissionSchemaUnavailable) {
		t.Fatalf("create project error = %v, want ErrPermissionSchemaUnavailable", err)
	}
}
