package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDefaultProjectAuthorizerEnforcesTenantRoleCeiling(t *testing.T) {
	ctx := context.Background()
	viewer := Actor{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer}
	if _, _, _, err := NewService(fakeWorkspace()).CreateProject(ctx, viewer, "viewer-create", CreateProjectInput{Name: "拒绝", TemplateID: "template-demo"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer create error = %v, want ErrNotFound", err)
	}
	contributor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor}
	repository := fakeWorkspace()
	service := NewService(repository)
	if _, _, replayed, err := service.CreateProject(ctx, contributor, "role-create", CreateProjectInput{Name: "允许", TemplateID: "template-demo"}); err != nil || replayed {
		t.Fatalf("contributor create: replayed=%v err=%v", replayed, err)
	}
	if _, _, _, err := service.SaveSpec(ctx, Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleViewer}, "p", "viewer-write", SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "subject"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer write error = %v, want ErrNotFound", err)
	}
}

func TestTenantRoleMatrixCapsProjectMemberPermissions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		role       types.TenantRole
		wantManage bool
	}{
		{name: "viewer", role: types.TenantRoleViewer},
		{name: "contributor", role: types.TenantRoleContributor},
		{name: "admin", role: types.TenantRoleAdmin, wantManage: true},
		{name: "owner", role: types.TenantRoleOwner, wantManage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(fakeWorkspace())
			actor := Actor{TenantID: 1, UserID: "owner", Role: tt.role}
			_, _, _, err := service.SaveMembers(ctx, actor, "p", "matrix-"+tt.name, SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{}})
			if tt.wantManage && err != nil {
				t.Fatalf("manage: %v", err)
			}
			if !tt.wantManage && !errors.Is(err, ErrNotFound) {
				t.Fatalf("manage error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestTenantRoleDowngradeDeniesIdempotentReplay(t *testing.T) {
	repository := fakeWorkspace()
	service := NewService(repository)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor}
	input := CreateProjectInput{Name: "重放", TemplateID: "template-demo"}
	if _, _, _, err := service.CreateProject(ctx, actor, "downgrade-key", input); err != nil {
		t.Fatalf("initial create: %v", err)
	}
	downgraded := actor
	downgraded.Role = types.TenantRoleViewer
	if _, _, _, err := service.CreateProject(ctx, downgraded, "downgrade-key", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("downgraded replay error = %v, want ErrNotFound", err)
	}
}

func TestProjectCapabilityAndChapterScopeAreIndependentFromTenantRole(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{
		UserID: "author", Role: "collaborator", GovernanceRole: "member",
		FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {"c"}},
	})
	service := NewService(repository)
	ctx := context.Background()
	author := Actor{TenantID: 1, UserID: "author", Role: types.TenantRoleContributor}
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "scoped", SourceIDs: []string{}}
	if _, _, _, err := service.SaveChapter(ctx, author, "p", "c", "scoped-write", input); err != nil {
		t.Fatalf("scoped author write: %v", err)
	}
	if _, _, _, err := service.SaveChapter(ctx, author, "p", "other", "out-of-scope", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope write = %v, want ErrNotFound", err)
	}
	admin := Actor{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin}
	repository.project.Members = append(repository.project.Members, Member{UserID: "admin", Role: "collaborator", GovernanceRole: "admin"})
	if err := service.Authorize(ctx, admin, "p", "manage"); err != nil {
		t.Fatalf("admin member management: %v", err)
	}
	for i := range repository.project.Members {
		if repository.project.Members[i].UserID == "admin" {
			repository.project.Members[i].Status = "suspended"
		}
	}
	if err := service.Authorize(ctx, admin, "p", "manage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspended admin authorization = %v, want ErrNotFound", err)
	}
}

func TestMemberAssignmentsPersistCapabilityAndScopeInTransaction(t *testing.T) {
	repository := fakeWorkspace()
	audit := &auditSinkStub{}
	service := NewServiceWithAudit(repository, ContractDemoTemplate{}, nil, nil, audit)
	input := SaveMembersInput{ExpectedProjectVersion: 1, Members: []Member{{
		UserID: "author", GovernanceRole: "member", FunctionRoles: []string{"author"},
		FunctionScopes: map[string][]string{"author": {"c"}},
	}}}
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := service.SaveMembers(context.Background(), actor, "p", "assignments-1", input); err != nil {
		t.Fatalf("save assignments: %v", err)
	}
	var found bool
	for _, got := range repository.project.Members {
		if got.UserID == "author" {
			found = true
			if len(got.FunctionRoles) != 1 || got.FunctionRoles[0] != "author" || got.FunctionScopes["author"][0] != "c" {
				t.Fatalf("assignment not persisted: %+v", got)
			}
		}
	}
	if !found {
		t.Fatal("assignment member missing")
	}
	if len(audit.events) < 2 || audit.events[len(audit.events)-1].Capability != "manage:members" || audit.events[len(audit.events)-1].Details["change"] != "member_permissions_replaced" {
		t.Fatalf("member change audit = %+v", audit.events)
	}
}

func TestTransferOwnerIsAtomicAndClearsFunctionAxis(t *testing.T) {
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{UserID: "recipient", Role: "collaborator", GovernanceRole: "member", FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {"c"}}})
	service := NewService(repository)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, _, err := service.TransferOwner(context.Background(), actor, "p", "transfer-1", TransferOwnerInput{ExpectedProjectVersion: 1, NewOwnerUserID: "recipient"}); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	for _, member := range repository.project.Members {
		if member.UserID == "recipient" && (member.Role != "owner" || member.GovernanceRole != "owner" || len(member.FunctionRoles) != 0) {
			t.Fatalf("recipient not promoted: %+v", member)
		}
		if member.UserID == "owner" && member.Role != "collaborator" {
			t.Fatalf("old owner not demoted: %+v", member)
		}
	}
}

type sourcePolicyStub struct {
	err   error
	calls int
}

type auditSinkStub struct{ events []AuditEvent }

func (s *auditSinkStub) Record(_ context.Context, event AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

type failingAuditSink struct{}

func (failingAuditSink) Record(context.Context, AuditEvent) error {
	return errors.New("audit unavailable")
}

func TestAuthorizationAuditRecordsAllowAndDenyDecisions(t *testing.T) {
	audit := &auditSinkStub{}
	service := NewServiceWithAudit(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, audit)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if err := service.Authorize(context.Background(), actor, "p", "read"); err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(context.Background(), Actor{TenantID: 1, UserID: "missing", Role: types.TenantRoleViewer}, "p", "read"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deny = %v", err)
	}
	if len(audit.events) != 2 || audit.events[0].Decision != "allow" || audit.events[1].Decision != "deny" {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if audit.events[0].Role != types.TenantRoleAdmin || a