package workspacecore

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestTypedRunnerExtendedMutationStillRequiresHumanDecision(t *testing.T) {
	service := NewService(fakeWorkspace())
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "machine"})
	called := false
	_, _, _, err := runOperation(service, ctx, actor, operationInput(operationInviteMember, "p", "typed-human-guard", InviteMemberInput{}, authorizationRequest(actor, "p", AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[MemberInvitation], error) {
		called = true
		return operationResult[MemberInvitation]{Value: MemberInvitation{}, Status: operationStatusCreated}, nil
	})
	if !errors.Is(err, ErrHumanDecisionRequired) || called {
		t.Fatalf("human guard err=%v callback=%v", err, called)
	}
}

func TestTypedRunnerRejectsMismatchedOutputBeforeSavingReplay(t *testing.T) {
	repository := fakeWorkspace()
	service := NewService(repository)
	actor := Actor{TenantID: 1, UserID: "owner"}
	_, _, _, err := runOperation(service, context.Background(), actor, operationInput(operationInviteMember, "p", "typed-output-guard", InviteMemberInput{}, authorizationRequest(actor, "p", AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[MemberInvitation], error) {
		return operationResult[MemberInvitation]{Value: MemberInvitation{}, Status: operationStatusCreated, Output: operationOutputChapter}, nil
	})
	if !errors.Is(err, ErrInvalidState) || repository.writes != 0 {
		t.Fatalf("output guard err=%v writes=%d", err, repository.writes)
	}
}

func TestTypedExtendedAuthorizationRetainsResourceWithoutCapabilityParsing(t *testing.T) {
	policy := &recordingAuthorizationPolicy{}
	service := NewServiceWithAuthorizationPolicy(fakeWorkspace(), ContractDemoTemplate{}, nil, policy, nil, AuthorizationModeEnforce)
	request := authorizationRequest(Actor{TenantID: 1, UserID: "owner"}, "p", AuthorizationEvidenceConfirm, "asset:asset-1")
	if err := service.AuthorizeRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(policy.projects) != 1 || policy.projects[0] != request {
		t.Fatalf("requests=%+v", policy.projects)
	}
}

func TestAuthorizeRequestCommitsDenyAuditBeforeReturningDecision(t *testing.T) {
	audit := &auditSinkStub{}
	service := NewServiceWithAudit(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, audit)
	request := authorizationRequest(Actor{TenantID: 1, UserID: "missing"}, "p", AuthorizationRead, "")
	err := service.AuthorizeRequest(context.Background(), request)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("authorization result = %v, want not found", err)
	}
	if len(audit.events) != 1 || audit.events[0].Decision != "deny" || audit.events[0].Capability != "read" {
		t.Fatalf("typed authorization audit = %+v, want one committed deny decision", audit.events)
	}
}

func TestExplicitRollbackUsesLegacyDecisionInsteadOfCurrentPolicy(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleViewer}
	service := NewServiceWithAuditMode(fakeWorkspace(), ContractDemoTemplate{}, nil, nil, nil, AuthorizationModeRollback)
	if _, _, _, err := service.CreateProject(context.Background(), actor, "explicit-rollback-create", CreateProjectInput{Name: "rollback", TemplateID: "template-demo"}); err != nil {
		t.Fatalf("rollback lost legacy allow: %v", err)
	}
	repository := fakeWorkspace()
	repository.project.Members = append(repository.project.Members, Member{UserID: "admin", Role: "collaborator", GovernanceRole: "admin", Status: "active"})
	service = NewServiceWithAuditMode(repository, ContractDemoTemplate{}, nil, nil, nil, AuthorizationModeRollback)
	if err := service.Authorize(context.Background(), Actor{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin}, "p", "manage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback adopted current allow: %v", err)
	}
}

type typedFocusedOnlyProjectPort struct{ ProjectMutationTransaction }

func TestExtendedFeaturePortFailsClosedForFocusedOnlyAdapter(t *testing.T) {
	if _, err := extendedTransaction(&typedFocusedOnlyProjectPort{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("focused-only adapter error=%v", err)
	}
}
