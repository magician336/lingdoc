package workspace

import (
	"context"
	"errors"
	"strconv"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
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
		gateway:   runtime.gateway,
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
	return p.projects.Authorize(ctx, Actor{
		TenantID: accessContext.TenantID,
		UserID:   accessContext.UserID,
	}, accessContext.ProjectID, capability)
}

func (p *accessPolicy) Current(ctx context.Context, accessContext LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	if p == nil || p.projects == nil || p.reader == nil || accessContext.UserID == "" || input.ProjectID == "" {
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

func (p *accessPolicy) generationCurrent(ctx context.Context, accessContext LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	current, err := p.projects.GenerationContext(ctx, Actor{TenantID: accessContext.TenantID, UserID: accessContext.UserID}, input.ProjectID, input.ChapterID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if current.SpecRevision != input.SpecRevision || !sameString(current.ChapterVersionID, input.ChapterVersionID) || current.TemplateID != input.TemplateID || current.TemplateVersion != input.TemplateVersion {
		return false, nil
	}
	if p.templates == nil || p.gateway == nil {
		return false, errors.New("lingdoc generation currentness dependencies are unavailable")
	}
	template, err := p.templates.Get(current.TemplateID, current.TemplateVersion)
	if err != nil {
		return false, err
	}
	if template.RulesetHash != input.RulesetHash {
		return false, nil
	}
	requested := make([]string, 0, len(input.AssetVersions))
	for _, asset := range input.AssetVersions {
		requested = append(requested, asset.AssetID)
	}
	resolved, err := p.gateway.ResolveAllowed(ctx, input.ProjectID, evidence.Actor{UserID: accessContext.UserID, TenantID: strconv.FormatUint(accessContext.TenantID, 10)}, requested)
	if err != nil {
		return false, err
	}
	if resolved == nil || len(resolved.Denied) != 0 || len(resolved.Allowed) != len(input.AssetVersions) {
		return false, nil
	}
	versions := make(map[string]int, len(resolved.Allowed))
	for _, asset := range resolved.Allowed {
		versions[asset.ID] = asset.AssetRevision
	}
	for _, expected := range input.AssetVersions {
		if versions[expected.AssetID] != expected.Revision {
			return false, nil
		}
	}
	return true, nil
}

func (p *accessPolicy) deliveryCurrent(ctx context.Context, accessContext LingDocAccessContext, input access.CurrentnessInput) (bool, error) {
	current, err := p.reader.ReadDeliveryInput(ctx, input.ProjectID)
	if err != nil {
		return false, err
	}
	if current.ProjectVersion != input.ProjectVersion || int64(current.SpecRevision) != input.SpecRevision || len(current.Chapters) != len(input.Chapters) {
		return false, nil
	}
	versions := make(map[string]*string, len(current.Chapters))
	for _, chapter := range current.Chapters {
		versions[chapter.ChapterID] = chapter.ChapterVersionID
	}
	for _, chapter := range input.Chapters {
		if !sameString(versions[chapter.ChapterID], chapter.ChapterVersionID) {
			return false, nil
		}
	}
	if p.sources == nil || len(input.Sources) == 0 {
		return true, nil
	}
	frozen := delivery.DeliveryInput{
		ProjectID:      input.ProjectID,
		ProjectVersion: int(input.ProjectVersion),
		SpecRevision:   int(input.SpecRevision),
		Chapters:       make([]delivery.SnapshotChapter, 0, len(input.Chapters)),
		Sources:        make([]delivery.FrozenSource, 0, len(input.Sources)),
	}
	for _, chapter := range input.Chapters {
		frozen.Chapters = append(frozen.Chapters, delivery.SnapshotChapter{ChapterID: chapter.ChapterID, ChapterVersionID: chapter.ChapterVersionID})
	}
	for _, source := range input.Sources {
		frozen.Sources = append(frozen.Sources, delivery.FrozenSource{ID: source.ID, ProjectID: source.ProjectID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash})
	}
	currentSource, err := p.sources.Current(ctx, accessContext.UserID, frozen)
	if errors.Is(err, candidateadoption.ErrSourceAccessDenied) || errors.Is(err, candidateadoption.ErrStaleInput) {
		return false, nil
	}
	return currentSource, err
}
