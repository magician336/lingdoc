package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"testing"
)

type rewriteWorkspaceStub struct {
	*inheritedRewriteWorkspace
	denied       bool
	modelAllowed bool
	denyWrite    bool
	cardScope    string
	formal       bool
	current      Project
}

func (w *rewriteWorkspaceStub) Authorize(_ context.Context, _ Actor, _, capability string) error {
	if w.denyWrite && capability == "write:chapter" {
		return core.ErrNotFound
	}
	return nil
}
func (w *rewriteWorkspaceStub) GetProject(context.Context, Actor, string) (Project, error) {
	if w.current.ID != "" {
		return w.current, nil
	}
	return Project{ID: "project", Status: "active", SpecRevision: 1}, nil
}
func (w *rewriteWorkspaceStub) SelectGenerationEvidence(_ context.Context, _ core.Actor, _, _ string, refs []core.G2VersionRef, formal bool) (core.G2EvidenceSelection, error) {
	w.formal = formal
	if w.denied || len(refs) != 1 || refs[0].ID != "card" || refs[0].Version != 2 || refs[0].Kind != "EvidenceCard" {
		return core.G2EvidenceSelection{}, core.ErrInvalidState
	}
	return core.G2EvidenceSelection{Cards: []core.EvidenceCard{{ID: "card", Version: 2, SupportType: "refutes", Scope: func() string {
		if w.cardScope != "" {
			return w.cardScope
		}
		return "仅合成反例"
	}(), Limitations: "不证明总体效果", SourceIDs: []string{"existing"}}}, Sources: []core.AgentSourceRef{{SourceID: "existing", AssetID: "asset", AssetRevision: 1, Locator: "page/1", QuotedTextHash: "hash", ModelUseAllowed: w.modelAllowed}}}, nil
}

type changingRewriteModelStub struct {
	base   *inheritedRewriteModel
	onCall func()
}

func (m *changingRewriteModelStub) RunMode() string { return "real_api_fake_model" }
func (m *changingRewriteModelStub) RewriteSelected(ctx context.Context, actor Actor, prompt SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	output, err := m.base.RewriteSelected(ctx, actor, prompt)
	output.RunMode = "real_api_fake_model"
	if m.onCall != nil {
		m.onCall()
	}
	return output, err
}
func formalRewriteFixture(t *testing.T) (*SelectedRewriteService, *rewriteWorkspaceStub, *inheritedRewriteModel, SelectedRewriteRequest) {
	s, w, _, model, input := inheritedRewriteFixture(t)
	port := &rewriteWorkspaceStub{inheritedRewriteWorkspace: w, modelAllowed: true}
	s.workspace = port
	// The fake answers the formal-evidence selection too. It used to be found
	// by asserting the workspace port; the selector is its own port now.
	s.selector = port
	input.EvidenceCardRefs = []generation.EvidenceCardRef{{ID: "card", Version: 2}}
	return s, port, model, input
}
func TestFormalRewriteUsesRefsOnlyImmutableSourcesAndRevalidatesGetReplayApply(t *testing.T) {
	s, w, model, input := formalRewriteFixture(t)
	actor := Actor{TenantID: 1, UserID: "owner"}
	candidate, err := s.Create(context.Background(), actor, "project", "chapter", "g2-refs-rewrite", input)
	require.NoError(t, err)
	require.Equal(t, "ready", candidate.Status)
	require.True(t, w.formal)
	require.Equal(t, []string{"existing"}, candidate.SourceIDs)
	require.Empty(t, model.prompt.ContextBefore)
	require.Empty(t, model.prompt.ContextAfter)
	require.Len(t, candidate.EvidenceCardRefs, 1)
	input.EvidenceCardRefs[0].Version = 99
	row, err := s.store.byID(context.Background(), 1, "project", "chapter", candidate.CandidateID)
	require.NoError(t, err)
	var saved []generation.EvidenceCardRef
	require.NoError(t, json.Unmarshal([]byte(row.EvidenceCardRefsJSON), &saved))
	require.Equal(t, int64(2), saved[0].Version)
	w.denied = true
	_, err = s.Get(context.Background(), actor, "project", candidate.CandidateID)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	input.EvidenceCardRefs = saved
	_, err = s.Create(context.Background(), actor, "project", "chapter", "g2-refs-rewrite", input)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	_, _, err = s.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID, "g2-apply-denied", ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1})
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	require.Equal(t, 1, model.calls)
}
func TestFormalRewriteMissingModelConsentOrChapterWriteNeverInvokesModel(t *testing.T) {
	for _, scenario := range []string{"model_consent", "observer", "unselected_original"} {
		t.Run(scenario, func(t *testing.T) {
			s, w, model, input := formalRewriteFixture(t)
			switch scenario {
			case "model_consent":
				w.modelAllowed = false
			case "observer":
				w.denyWrite = true
			case "unselected_original":
				input.SourceIDs = []string{"outside"}
			}
			_, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "g2-denied-"+scenario, input)
			require.Error(t, err)
			require.Zero(t, model.calls)
			var rows int64
			require.NoError(t, s.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows)
		})
	}
}
func TestFormalRewriteCardChangeDuringModelClearsCandidateBody(t *testing.T) {
	s, w, model, input := formalRewriteFixture(t)
	s.model = &changingRewriteModelStub{base: model, onCall: func() { w.denied = true }}
	candidate, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "g2-change-during-rewrite", input)
	require.NoError(t, err)
	require.Equal(t, "stale", candidate.Status)
	require.Equal(t, "evidence_changed", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.Equal(t, 1, model.calls)
}
func boundProject() Project {
	definition := lingdoctemplate.DemoTemplate()
	definition.ID = "custom-template"
	definition.Version = "custom-v1"
	return Project{ID: "project", Status: "active", SpecRevision: 1, CurrentContextRevision: 7, TemplateID: definition.ID, TemplateVersion: definition.Version, TemplateCopyVersion: 3, TemplateCopy: &core.ProjectTemplateCopy{ID: "copy-custom", ProjectID: "project", Version: 3, Status: core.TemplateCopyBound, ContentHash: "copy-content", RulesetHash: "independent-rule-hash", RuleSetSnapshot: &lingdoctemplate.RuleSet{RulesetHash: "independent-rule-hash"}, Definition: definition}}
}
func TestRewritePinsCustomTemplateAndDetectsChangeDuringModelAndRead(t *testing.T) {
	s, w, model, input := formalRewriteFixture(t)
	w.current = boundProject()
	s.model = &changingRewriteModelStub{base: model, onCall: func() { w.current.TemplateCopy.ContentHash = "changed" }}
	candidate, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "g2-template-race", input)
	require.NoError(t, err)
	require.Equal(t, "stale", candidate.Status)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.Equal(t, "copy-custom", model.prompt.TemplateBasis.CopyID)
	require.Equal(t, "independent-rule-hash", model.prompt.TemplateBasis.RulesetHash)
	_, err = s.Get(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", candidate.CandidateID)
	require.ErrorIs(t, err, ErrRewriteStale)
}
func TestGenerationUsesProjectCopyAndIndependentRuleHashForCurrentness(t *testing.T) {
	project := boundProject()
	definition, hash, err := generationBoundTemplate(project, nil)
	require.NoError(t, err)
	require.Equal(t, "custom-template", definition.ID)
	require.Equal(t, "independent-rule-hash", hash)
	require.NotEqual(t, definition.RulesetHash, hash)
	w := &generationContextWorkspaceStub{project: project, context: core.GenerationContext{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1, TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion}}
	gateway := &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{{ID: "asset", AssetRevision: 1}}}}
	checker := generationCurrentnessChecker{workspace: w, projectCopies: w, gateway: gateway, templates: core.ContractDemoTemplate{}}
	basis := candidateadoption.Basis{SpecRevision: 1, TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion, RulesetHash: hash, TemplateCopyID: project.TemplateCopy.ID, TemplateCopyVersion: 3, TemplateCopyContentHash: project.TemplateCopy.ContentHash, ContextRevision: 7, AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset", AssetRevision: 1}}}
	current, err := checker.GenerationInputIsCurrent(context.Background(), generation.Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", basis)
	require.NoError(t, err)
	require.True(t, current)
	w.project.TemplateCopy.Version++
	w.project.TemplateCopyVersion++
	current, err = checker.GenerationInputIsCurrent(context.Background(), generation.Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", basis)
	require.NoError(t, err)
	require.False(t, current)
}

type generationContextWorkspaceStub struct {
	ProjectApplication
	core.GenerationContextApplication
	project Project
	context core.GenerationContext
}

func (w *generationContextWorkspaceStub) Authorize(context.Context, Actor, string, string) error {
	return nil
}
func (w *generationContextWorkspaceStub) GenerationContext(context.Context, Actor, string, string) (core.GenerationContext, error) {
	return w.context, nil
}
func (w *generationContextWorkspaceStub) GetProject(context.Context, Actor, string) (Project, error) {
	return w.project, nil
}
func TestModelContextPreservesCallerPrincipalAndAPIKeyScope(t *testing.T) {
	caller := types.Caller{TenantID: 1, UserID: "machine", Role: types.TenantRoleContributor}
	ctx := types.WithCaller(context.Background(), caller)
	scope := types.TenantAPIKeyScope{KeyID: 44, KnowledgeBaseIDs: types.StringArray{"kb-only"}, Capabilities: types.StringArray{"read", "write"}}
	ctx = types.WithTenantAPIKeyScope(ctx, scope)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "key-44"})
	resolved := modelContextForActor(ctx, generation.Actor{TenantID: 1, UserID: "machine", Role: types.TenantRoleOwner})
	require.Equal(t, caller, types.CallerFromContext(resolved))
	actual, ok := types.TenantAPIKeyScopeFromContext(resolved)
	require.True(t, ok)
	require.Equal(t, scope.KeyID, actual.KeyID)
	require.Equal(t, scope.KnowledgeBaseIDs, actual.KnowledgeBaseIDs)
	principal, ok := types.PrincipalFromContext(resolved)
	require.True(t, ok)
	require.Equal(t, types.PrincipalAPITenant, principal.Type)
	err := generationWorkspaceAuthorizer{}.AuthorizeHumanGeneration(resolved)
	require.True(t, errors.Is(err, generation.ErrForbidden))
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
}
func TestRewriteMachineCannotCreateOrApplyHumanCandidate(t *testing.T) {
	s, _, model, input := formalRewriteFixture(t)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalEmbedSession, ID: "embed"})
	_, err := s.Create(ctx, Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "g2-machine-rewrite", input)
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
	require.Zero(t, model.calls)
	_, _, err = s.Apply(ctx, Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "any", "g2-machine-apply", ApplySelectedRewriteInput{})
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
}

func TestRewriteStoredConstraintMetadataChangeIsDeniedWithoutModelRepeat(t *testing.T) {
	s, w, model, input := formalRewriteFixture(t)
	candidate, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "g2-meta-current", input)
	require.NoError(t, err)
	row, err := s.store.byID(context.Background(), 1, "project", "chapter", candidate.CandidateID)
	require.NoError(t, err)
	constraints, err := decodeRewriteEvidenceConstraints(row.EvidenceConstraintsJSON)
	require.NoError(t, err)
	require.Len(t, constraints, 1)
	require.Equal(t, "refutes", constraints[0].SupportType)
	require.Equal(t, "仅合成反例", constraints[0].Scope)
	require.Equal(t, []string{"existing"}, constraints[0].SourceIDs)
	w.cardScope = "伪称总体效果"
	_, err = s.Get(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", candidate.CandidateID)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	require.Equal(t, 1, model.calls)
}
