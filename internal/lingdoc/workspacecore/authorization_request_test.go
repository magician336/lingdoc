package workspacecore

import (
	"context"
	"errors"
	"testing"
)

type recordingAuthorizationPolicy struct {
	tenant   []AuthorizationRequest
	projects []AuthorizationRequest
}

func (p *recordingAuthorizationPolicy) AuthorizeTenant(_ Transaction, request AuthorizationRequest) error {
	p.tenant = append(p.tenant, request)
	return nil
}

func (p *recordingAuthorizationPolicy) AuthorizeProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	p.projects = append(p.projects, request)
	return tx.Project(request.Actor.TenantID, request.ProjectID)
}

func TestWorkspaceUsesStructuredAuthorizationRequests(t *testing.T) {
	policy := &recordingAuthorizationPolicy{}
	actor := Actor{TenantID: 1, UserID: "owner"}
	service := NewServiceWithAuthorizationPolicy(fakeWorkspace(), ContractDemoTemplate{}, nil, policy, nil, AuthorizationModeEnforce)

	if _, _, _, err := service.CreateProject(context.Background(), actor, "structured-create", CreateProjectInput{Name: "结构化", TemplateID: "template-demo"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(policy.tenant) != 1 || policy.tenant[0].Action != AuthorizationCreate || policy.tenant[0].ProjectID != "" {
		t.Fatalf("tenant requests = %+v, want create without project resource", policy.tenant)
	}

	policy = &recordingAuthorizationPolicy{}
	service = NewServiceWithAuthorizationPolicy(fakeWorkspace(), ContractDemoTemplate{}, nil, policy, nil, AuthorizationModeEnforce)
	if _, _, _, err := service.SaveChapter(context.Background(), actor, "p", "c", "structured-write", SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "body", SourceIDs: []string{}}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	// G1 now reads the current project provenance before the existing write
	// preflight and commit. Every request must remain structured and scoped;
	// the extra read must not replace either write authorization check.
	reads, writes := 0, 0
	for _, request := range policy.projects {
		if request.Actor != actor || request.ProjectID != "p" {
			t.Fatalf("authorization changed actor/project scope: %+v", request)
		}
		switch request.Action {
		case AuthorizationRead:
			reads++
			if request.Resource != "" {
				t.Fatalf("project provenance read has unexpected resource: %+v", request)
			}
		case AuthorizationWrite:
			writes++
			if request.Resource != "c" {
				t.Fatalf("chapter write has wrong resource: %+v", request)
			}
		default:
			t.Fatalf("unexpected authorization request: %+v", request)
		}
	}
	if reads != 1 || writes != 2 {
		t.Fatalf("project requests = %+v, want one provenance read plus preflight and commit writes", policy.projects)
	}
}

func TestLegacyCapabilityIsConvertedOnlyAtCompatibilityBoundary(t *testing.T) {
	request, err := authorizationRequestFromCapability(Actor{TenantID: 1, UserID: "u"}, "p", "write:chapter-1")
	if err != nil {
		t.Fatalf("parse legacy capability: %v", err)
	}
	if request.Action != AuthorizationWrite || request.Resource != "chapter-1" || request.capability() != "write:chapter-1" {
		t.Fatalf("request = %+v, want structured write/chapter-1", request)
	}
	if _, err := authorizationRequestFromCapability(Actor{TenantID: 1, UserID: "u"}, "p", "write:chapter:extra"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed capability error = %v, want ErrInvalidRequest", err)
	}
	if _, err := authorizationRequestFromCapability(Actor{TenantID: 1, UserID: "u"}, "p", "grant"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown capability error = %v, want ErrInvalidRequest", err)
	}
}

var _ AuthorizationPolicy = transactionProjectAuthorizer{}
