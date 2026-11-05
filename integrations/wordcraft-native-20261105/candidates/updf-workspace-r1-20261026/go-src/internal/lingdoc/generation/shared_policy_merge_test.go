package generation

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

type mergePolicy struct {
	capability string
	input      access.CurrentnessInput
	humanErr   error
}

func (p *mergePolicy) Authorize(_ context.Context, _ access.LingDocAccessContext, capability string) error {
	p.capability = capability
	return nil
}
func (p *mergePolicy) Current(_ context.Context, _ access.LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	p.input = input
	return true, nil
}
func (p *mergePolicy) AuthorizeHumanGeneration(context.Context) error { return p.humanErr }

func TestSharedPolicyCarriesProjectCopyBasisAndChapterScope(t *testing.T) {
	p := &mergePolicy{}
	s := &Service{Access: p}
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor}
	if err := s.authorizeWrite(context.Background(), actor, "project", "chapter"); err != nil || p.capability != "write:chapter" {
		t.Fatalf("chapter scope: %s, %v", p.capability, err)
	}
	b := candidateadoption.Basis{SpecRevision: 3, TemplateCopyID: "copy", TemplateCopyVersion: 4, TemplateCopyContentHash: "hash", ContextRevision: 7}
	if current, err := s.current(context.Background(), actor, "project", "chapter", b); err != nil || !current {
		t.Fatalf("current: %v %v", current, err)
	}
	if p.input.TemplateCopyID != b.TemplateCopyID || p.input.TemplateCopyVersion != b.TemplateCopyVersion || p.input.TemplateCopyContentHash != b.TemplateCopyContentHash || p.input.ContextRevision != b.ContextRevision {
		t.Fatalf("lost project copy basis: %+v", p.input)
	}
}

func TestSharedPolicyRetainsHumanInitiationGate(t *testing.T) {
	legacy, _ := generationFixture()
	p := &mergePolicy{humanErr: ErrForbidden}
	s := NewServiceWithAccessPolicy(p, legacy.Inputs, legacy.Sources, legacy.Model, legacy.Repository, legacy.Enqueuer)
	s.RequireHumanInitiation = true
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft", ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
	if _, err := s.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "human-policy-gate", request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("human gate: %v", err)
	}
	p.humanErr = nil
	if _, err := s.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "human-policy-allowed", request); err != nil {
		t.Fatalf("allowed human without legacy authorizer: %v", err)
	}
}
