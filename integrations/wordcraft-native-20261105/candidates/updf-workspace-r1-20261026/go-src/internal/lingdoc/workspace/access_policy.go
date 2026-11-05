package workspace

import (
	"context"
	"errors"
	"strconv"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// AccessPolicy is the application seam shared by generation and delivery.
// Keeping the alias in workspace makes the composition root discoverable while
// the implementation remains in the dependency-free access package.
type AccessPolicy = access.Policy
type LingDocAccessContext = access.LingDocAccessContext

// NewAccessPolicy creates the one production policy adapter. The workspace
// service remains the source of project authorization; the source runtime and
// delivery reader provide the live revision checks. No use case reconstructs
// these rules independently.
func NewAccessPolicy(
	projects ApplicationService,
	runtime *SourceRuntime,
	reader candidateadoption.DeliveryInputReader,
) AccessPolicy {
	if projects == nil || runtime == nil || reader == nil {
		return nil
	}
	currentness, _ := runtime.DeliveryInputBuilder().(DeliveryCurrentness)
	return &accessPolicy{
		projects:  projects,
		gateway:   runtime.generationGateway(),
		templates: runtime.templates,
		reader:    reader,
		sources:   currentness,
	}
}

type accessPolicy struct {
	projects  ApplicationService
	gateway   evidence.AssetGateway
	templates core.TemplateReader
	reader    candidateadoption.DeliveryInputReader
	sources   DeliveryCurrentness
}

func (p *accessPolicy) Authorize(ctx context.Context, accessContext LingDocAccessContext, capability string) error {
	if p == nil || p.projects == nil || accessContext.UserID == "" || accessContext.ProjectID == "" {
		return errors.New("lingdoc access policy is unavailable")
	}
	actor := generationWorkspaceActor(ctx, generation.Actor{TenantID: accessContext.TenantID, UserID: accessContext.UserID, Role: types.TenantRole(accessContext.Role)})
	if capability == "contributor" {
		capability = "delivery-read"
	}
	return p.projects.Authorize(ctx, actor, accessContext.ProjectID, capability)
}

func (p *accessPolicy) Current(ctx context.Context, accessContext LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	if p == nil || p.projects == nil || p.reader == nil || accessContext.UserID == "" || input.ProjectID == "" || input.ProjectID != accessContext.ProjectID {
		return false, errors.New("lingdoc access policy is unavailable")
	}
	switch input.Kind {
	case access.GenerationInput:
		return p.generationCurrent(ctx, accessContext, input)
	case access.DeliveryInput:
		return p.deliveryCurrent(ctx, accessContext, input)
	default:
		return false, errors.New("unknown LingDoc currentness input")
	}
}

func (p *accessPolicy) AuthorizeHumanGeneration(ctx context.Context) error {
	return generationWorkspaceAuthorizer{}.AuthorizeHumanGeneration(ctx)
}

func (p *accessPolicy) generationCurrent(ctx context.Context, a LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	checker := generationCurrentnessChecker{workspace: p.projects, projectCopies: p.projects, gateway: p.gateway, templates: p.templates}
	versions := make([]candidateadoption.AssetVersion, 0, len(input.AssetVersions))
	for _, v := range input.AssetVersions {
		versions = append(versions, candidateadoption.AssetVersion{AssetID: v.AssetID, AssetRevision: v.Revision})
	}
	return checker.GenerationInputIsCurrent(ctx, generation.Actor{TenantID: a.TenantID, UserID: a.UserID, Role: types.TenantRole(a.Role)}, input.ProjectID, input.ChapterID, candidateadoption.Basis{
		SpecRevision: int(input.SpecRevision), ChapterVersionID: input.ChapterVersionID, TemplateID: input.TemplateID, TemplateVersion: input.TemplateVersion, RulesetHash: input.RulesetHash, AssetVersions: versions,
		TemplateCopyID: input.TemplateCopyID, TemplateCopyVersion: input.TemplateCopyVersion, TemplateCopyContentHash: input.TemplateCopyContentHash, ContextRevision: input.ContextRevision,
	})
}

func (p *accessPolicy) deliveryCurrent(ctx context.Context, accessContext LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	current, err := p.reader.ReadDeliveryInput(ctx, input.ProjectID)
	if err != nil {
		return false, err
	}
	if current.ProjectID != input.ProjectID || current.ProjectVersion != input.ProjectVersion || int64(current.SpecRevision) != input.SpecRevision || len(current.Chapters) != len(input.Chapters) {
		return false, nil
	}
	if current.TemplateID != input.TemplateID || current.TemplateVersion != input.TemplateVersion ||
		current.TemplateCopyID != input.TemplateCopyID || current.TemplateCopyVersion != input.TemplateCopyVersion ||
		current.TemplateCopyContentHash != input.TemplateCopyContentHash || current.ContextRevision != input.ContextRevision {
		return false, nil
	}
	currentRulesetHash := current.TemplateCopyRulesetHash
	if current.TemplateCopyID == "" && current.TemplateID != "" {
		if p.templates == nil {
			return false, candidateadoption.ErrDependencyUnavailable
		}
		template, err := p.templates.Get(current.TemplateID, current.TemplateVersion)
		if err != nil {
			return false, err
		}
		currentRulesetHash = template.RulesetHash
	}
	if currentRulesetHash != input.RulesetHash {
		return false, nil
	}
	versions := make(map[string]*string, len(current.Chapters))
	for _, chapter := range current.Chapters {
		versions[chapter.ChapterID] = chapter.ChapterVersionID
	}
	for _, chapter := range input.Chapters {
		version, exists := versions[chapter.ChapterID]
		if !exists || !sameString(version, chapter.ChapterVersionID) {
			return false, nil
		}
	}
	if p.sources == nil {
		return false, candidateadoption.ErrDependencyUnavailable
	}
	frozen := delivery.DeliveryInput{
		ProjectID:               input.ProjectID,
		ProjectVersion:          int(input.ProjectVersion),
		SpecRevision:            int(input.SpecRevision),
		ContextRevision:         input.ContextRevision,
		Template:                delivery.Template{ID: input.TemplateID, Version: input.TemplateVersion, RulesetHash: input.RulesetHash},
		Spec:                    input.Spec,
		TemplateCopyID:          input.TemplateCopyID,
		TemplateCopyVersion:     input.TemplateCopyVersion,
		TemplateCopyContentHash: input.TemplateCopyContentHash,
		Chapters:                make([]delivery.SnapshotChapter, 0, len(input.Chapters)),
		Sources:                 make([]delivery.FrozenSource, 0, len(input.Sources)),
	}
	for _, asset := range input.AssetVersions {
		frozen.AssetVersions = append(frozen.AssetVersions, delivery.AssetVersion{AssetID: asset.AssetID, Revision: asset.Revision})
	}
	if p := input.SpecProvenance; p != nil {
		frozen.SpecProvenance = &core.ProjectProvenanceSnapshot{ProjectID: p.ProjectID, SpecRevision: p.SpecRevision, ContextRevision: p.ContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, Digest: p.Digest}
		for _, f := range p.Fields {
			frozen.SpecProvenance.Fields = append(frozen.SpecProvenance.Fields, core.SpecProvenanceRef{FieldKey: f.FieldKey, ValueHash: f.ValueHash, PinID: f.PinID})
		}
	}
	for _, chapter := range input.Chapters {
		frozen.Chapters = append(frozen.Chapters, delivery.SnapshotChapter{ChapterID: chapter.ChapterID, ChapterVersionID: chapter.ChapterVersionID})
	}
	for _, source := range input.Sources {
		frozen.Sources = append(frozen.Sources, delivery.FrozenSource{ID: source.ID, ProjectID: source.ProjectID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash})
	}
	currentSource, err := p.sources.Current(ctx, accessContext.UserID, frozen)
	if errors.Is(err, candidateadoption.ErrStaleInput) {
		return false, nil
	}
	return currentSource, err
}

// The legacy checker is also used by isolated adapters; production calls it
// only through accessPolicy, retaining one currentness implementation.
type generationCurrentnessChecker struct {
	projectCopies ApplicationService
	workspace     ApplicationService
	gateway       evidence.AssetGateway
	templates     core.TemplateReader
}

func (c generationCurrentnessChecker) GenerationInputIsCurrent(ctx context.Context, actor generation.Actor, projectID, chapterID string, basis candidateadoption.Basis) (bool, error) {
	current, err := c.workspace.GenerationContext(ctx, generationWorkspaceActor(ctx, actor), projectID, chapterID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if int(current.SpecRevision) != basis.SpecRevision || !sameString(current.ChapterVersionID, basis.ChapterVersionID) ||
		current.TemplateID != basis.TemplateID || current.TemplateVersion != basis.TemplateVersion {
		return false, nil
	}
	ruleHash := ""
	if c.projectCopies != nil {
		project, readErr := c.projectCopies.GetProject(ctx, generationWorkspaceActor(ctx, actor), projectID)
		if readErr != nil {
			return false, readErr
		}
		if project.TemplateCopy == nil || project.Status != "active" || project.ID != projectID || project.SpecRevision != int64(basis.SpecRevision) || project.TemplateID != basis.TemplateID || project.TemplateVersion != basis.TemplateVersion || project.CurrentContextRevision != basis.ContextRevision || project.TemplateCopy.ID != basis.TemplateCopyID || project.TemplateCopy.Version != basis.TemplateCopyVersion || project.TemplateCopy.ContentHash != basis.TemplateCopyContentHash {
			return false, nil
		}
		_, ruleHash, err = generationBoundTemplate(project, nil)
	} else {
		template, readErr := c.templates.Get(current.TemplateID, current.TemplateVersion)
		err = readErr
		ruleHash = template.RulesetHash
	}
	if err != nil {
		return false, err
	}
	if basis.RulesetHash != ruleHash {
		return false, nil
	}
	requested := make([]string, 0, len(basis.AssetVersions))
	for _, asset := range basis.AssetVersions {
		requested = append(requested, asset.AssetID)
	}
	assets, err := c.gateway.ResolveAllowed(evidenceContext(ctx, actor), projectID,
		evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}, requested)
	if err != nil {
		return false, err
	}
	if assets == nil || len(assets.Denied) != 0 || len(assets.Allowed) != len(basis.AssetVersions) {
		return false, nil
	}
	currentVersions := make(map[string]int, len(assets.Allowed))
	for _, asset := range assets.Allowed {
		currentVersions[asset.ID] = asset.AssetRevision
	}
	for _, asset := range basis.AssetVersions {
		if currentVersions[asset.AssetID] != asset.AssetRevision {
			return false, nil
		}
	}
	return true, nil
}
