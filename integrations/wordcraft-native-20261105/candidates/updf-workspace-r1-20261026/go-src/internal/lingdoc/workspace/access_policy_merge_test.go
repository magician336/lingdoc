package workspace

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
	"testing"
)

type mergeDeliveryReader struct {
	input candidateadoption.WorkspaceDeliveryInput
}

func (r mergeDeliveryReader) ReadDeliveryInput(context.Context, string) (candidateadoption.WorkspaceDeliveryInput, error) {
	return r.input, nil
}

type mergeDeliveryCurrentness struct {
	seen  delivery.DeliveryInput
	calls int
	err   error
}

func (c *mergeDeliveryCurrentness) Current(_ context.Context, _ string, f delivery.DeliveryInput) (bool, error) {
	c.seen = f
	c.calls++
	return c.err == nil, c.err
}

func TestSharedPolicyRetainsEmptySourceProvenanceAndRevokeError(t *testing.T) {
	proof := &core.ProjectProvenanceSnapshot{ProjectID: "project", SpecRevision: 2, ContextRevision: 3, TemplateCopyVersion: 4, Digest: "digest", Fields: []core.SpecProvenanceRef{{FieldKey: "topic", ValueHash: "hash", PinID: "pin"}}}
	frozen := delivery.DeliveryInput{ProjectID: "project", ProjectVersion: 5, SpecRevision: 2, Spec: map[string]string{"topic": "synthetic"}, SpecProvenance: proof, TemplateCopyID: "copy", TemplateCopyVersion: 4, TemplateCopyContentHash: "copy-hash"}
	current := &mergeDeliveryCurrentness{}
	p := &accessPolicy{projects: &g2GenerationCurrentWorkspace{}, reader: mergeDeliveryReader{input: candidateadoption.WorkspaceDeliveryInput{ProjectID: "project", ProjectVersion: 5, SpecRevision: 2, ContextRevision: 3, TemplateCopyID: "copy", TemplateCopyVersion: 4, TemplateCopyContentHash: "copy-hash"}}, sources: current}
	actor := LingDocAccessContext{TenantID: 1, UserID: "owner", ProjectID: "project"}
	ok, err := p.Current(context.Background(), actor, delivery.AccessCurrentnessInput(frozen))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, current.calls)
	require.Equal(t, frozen.Spec, current.seen.Spec)
	require.Equal(t, proof, current.seen.SpecProvenance)
	require.Equal(t, "copy", current.seen.TemplateCopyID)
	current.err = candidateadoption.ErrSourceAccessDenied
	ok, err = p.Current(context.Background(), actor, delivery.AccessCurrentnessInput(frozen))
	require.False(t, ok)
	require.True(t, errors.Is(err, candidateadoption.ErrSourceAccessDenied))
	actor.ProjectID = "another-project"
	_, err = p.Current(context.Background(), actor, delivery.AccessCurrentnessInput(frozen))
	require.Error(t, err)
	require.Equal(t, 2, current.calls)
}

func TestSharedPolicyUsesProjectCopyRuleHashForGeneration(t *testing.T) {
	project := g2BoundProject()
	_, hash, err := generationBoundTemplate(project, nil)
	require.NoError(t, err)
	w := &g2GenerationCurrentWorkspace{project: project, context: core.GenerationContext{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1, TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion}}
	gateway := &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{{ID: "asset", AssetRevision: 1}}}}
	p := &accessPolicy{projects: w, reader: mergeDeliveryReader{}, gateway: gateway, templates: core.ContractDemoTemplate{}}
	input := access.CurrentnessInput{Kind: access.GenerationInput, ProjectID: "project", ChapterID: "chapter", SpecRevision: 1, TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion, RulesetHash: hash, TemplateCopyID: project.TemplateCopy.ID, TemplateCopyVersion: 3, TemplateCopyContentHash: project.TemplateCopy.ContentHash, ContextRevision: 7, AssetVersions: []access.AssetVersion{{AssetID: "asset", Revision: 1}}}
	actor := LingDocAccessContext{TenantID: 1, UserID: "owner", ProjectID: "project"}
	current, err := p.Current(context.Background(), actor, input)
	require.NoError(t, err)
	require.True(t, current)
	w.project.TemplateCopy.Version++
	w.project.TemplateCopyVersion++
	current, err = p.Current(context.Background(), actor, input)
	require.NoError(t, err)
	require.False(t, current)
}
