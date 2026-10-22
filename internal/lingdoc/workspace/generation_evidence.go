package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"io"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func evidenceCardVersionRefs(refs []generation.EvidenceCardRef) []core.VersionRef {
	out := make([]core.VersionRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, core.VersionRef{Kind: "EvidenceCard", ID: ref.ID, Version: ref.Version})
	}
	return out
}

// selectFormalEvidence resolves the optional formal-evidence selector.
//
// The selector is an optional capability, and a workspace that does not have
// it has to keep failing as a missing dependency (the 503-shaped
// generation.ErrDependencyUnavailable). It used to be discovered by asserting
// the workspace value to a local interface: that assertion was a back door,
// because the value it asked about was the aggregate, and the assertion could
// hand back any operation the interface happened to name. The port is now an
// explicit nullable parameter, and a nil port fails the same way the failed
// assertion did.
func selectFormalEvidence(ctx context.Context, selector core.EvidenceSelectionApplication, actor Actor, projectID, chapterID string, refs []core.VersionRef) (core.EvidenceSelection, error) {
	if selector == nil {
		return core.EvidenceSelection{}, generation.ErrDependencyUnavailable
	}
	selected, err := selector.SelectGenerationEvidence(ctx, actor, projectID, chapterID, refs, true)
	if err != nil {
		return selected, generation.ErrSourceAccessDenied
	}
	if len(selected.Cards) != len(refs) || len(selected.Sources) == 0 || len(selected.Sources) > maxGenerationSources || len(selected.Pending) > 0 {
		return selected, generation.ErrSourceAccessDenied
	}
	for _, source := range selected.Sources {
		if !source.ModelUseAllowed {
			return selected, generation.ErrSourceAccessDenied
		}
	}
	return selected, nil
}

// generationEvidenceValidator resolves the optional formal-evidence selector.
//
// The port it carries stays an interface type: the selector is an optional
// capability, and a workspace that does not implement it must keep failing as a
// missing dependency (the 503-shaped generation.ErrDependencyUnavailable).
type generationEvidenceValidator struct {
	selector core.EvidenceSelectionApplication
}

func (v generationEvidenceValidator) ValidateGenerationEvidence(ctx context.Context, actor generation.Actor, projectID, chapterID string, refs []generation.EvidenceCardRef, sources []generation.Source) error {
	selected, err := selectFormalEvidence(ctx, v.selector, generationWorkspaceActor(ctx, actor), projectID, chapterID, evidenceCardVersionRefs(refs))
	if err != nil {
		return err
	}
	if len(selected.Sources) != len(sources) {
		return generation.ErrSourceAccessDenied
	}
	for _, source := range sources {
		index := slices.IndexFunc(selected.Sources, func(ref core.AgentSourceRef) bool { return ref.SourceID == source.ID })
		if index < 0 {
			return generation.ErrSourceAccessDenied
		}
		match := selected.Sources[index]
		if source.ProjectID != projectID || source.AssetID != match.AssetID || source.AssetRevision != match.AssetRevision || source.Locator != match.Locator || researchTextHash(source.QuotedText) != match.QuotedTextHash {
			return generation.ErrSourceAccessDenied
		}
	}
	return nil
}
func (r *generationInputResolver) resolveFormalEvidence(ctx context.Context, actor generation.Actor, projectID string, request generation.Request) ([]generation.Source, []string, error) {
	if r.sources == nil {
		return nil, nil, generation.ErrDependencyUnavailable
	}
	selected, err := selectFormalEvidence(ctx, r.selector, generationWorkspaceActor(ctx, actor), projectID, request.ChapterID, evidenceCardVersionRefs(request.EvidenceCardRefs))
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	sources := []generation.Source{}
	for _, ref := range selected.Sources {
		source, err := r.sources.GetSource(ctx, generationWorkspaceActor(ctx, actor), projectID, ref.SourceID)
		if err != nil || source.AssetID != ref.AssetID || source.AssetRevision != ref.AssetRevision || source.QuotedTextHash != ref.QuotedTextHash || string(source.Status) != "available" {
			return nil, nil, generation.ErrSourceAccessDenied
		}
		sources = append(sources, generationSourceFromEvidence(source))
		ids = append(ids, ref.AssetID)
	}
	sort.Strings(ids)
	ids = slices.Compact(ids)
	if len(request.AssetIDs) > 0 && !slices.Equal(stringSet(request.AssetIDs), ids) {
		return nil, nil, generation.ErrSourceAccessDenied
	}
	return sources, ids, nil
}
func (a generationWorkspaceAuthorizer) AuthorizeGenerationChapter(ctx context.Context, actor generation.Actor, projectID, chapterID string) error {
	if chapterID == "" {
		return generation.ErrInvalidRequest
	}
	err := a.authorizer.Authorize(ctx, generationWorkspaceActor(ctx, actor), projectID, "write:"+chapterID)
	if err != nil {
		return generation.ErrNotFound
	}
	return nil
}
func rewriteModelScope(ctx context.Context, sources SourceApplicationService, actor Actor, projectID string, items []RewriteSourceSnapshot) error {
	runtime, ok := sources.(*SourceRuntime)
	if !ok {
		return nil
	}
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.AssetID)
	}
	sort.Strings(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return nil
	}
	result, err := runtime.generationGateway().ResolveAllowed(evidenceContext(ctx, generation.Actor{TenantID: actor.TenantID, UserID: actor.UserID}), projectID, evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}, ids)
	if err != nil || result == nil || len(result.Denied) > 0 || len(result.Allowed) != len(ids) {
		return ErrRewriteSourceDenied
	}
	return nil
}

func decodeRewriteEvidenceRefs(raw string) ([]generation.EvidenceCardRef, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var refs []generation.EvidenceCardRef
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&refs); err != nil {
		return nil, ErrRewriteSourceDenied
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrRewriteSourceDenied
	}
	if !validRewriteEvidenceRefs(refs) {
		return nil, ErrRewriteSourceDenied
	}
	return refs, nil
}
func validRewriteEvidenceRefs(refs []generation.EvidenceCardRef) bool {
	if len(refs) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" || ref.Version < 1 || seen[ref.ID] {
			return false
		}
		seen[ref.ID] = true
	}
	return true
}
func (s *SelectedRewriteService) checkEvidenceRefs(ctx context.Context, actor Actor, projectID, chapterID string, refs []generation.EvidenceCardRef, snapshots []RewriteSourceSnapshot) error {
	if len(refs) == 0 {
		return nil
	}
	selected, err := selectFormalEvidence(ctx, s.selector, actor, projectID, chapterID, evidenceCardVersionRefs(refs))
	if err != nil {
		return ErrRewriteSourceDenied
	}
	for _, snapshot := range snapshots {
		index := slices.IndexFunc(selected.Sources, func(ref core.AgentSourceRef) bool { return ref.SourceID == snapshot.SourceID })
		if index < 0 {
			return ErrRewriteSourceDenied
		}
		current := selected.Sources[index]
		if current.AssetID != snapshot.AssetID || current.AssetRevision != snapshot.AssetRevision || current.Locator != snapshot.Locator || current.QuotedTextHash != snapshot.QuotedTextHash {
			return ErrRewriteSourceDenied
		}
	}
	return nil
}
func (s *SelectedRewriteService) checkRowEvidence(ctx context.Context, actor Actor, projectID string, row selectedRewriteRow) error {
	refs, err := decodeRewriteEvidenceRefs(row.EvidenceCardRefsJSON)
	if err != nil {
		return err
	}
	if err := s.checkRowEvidenceConstraints(ctx, actor, projectID, row, refs); err != nil {
		return err
	}
	if err := s.checkRowTemplate(ctx, actor, projectID, row); err != nil {
		return err
	}
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if err := rewriteModelScope(ctx, s.sources, actor, projectID, snapshots); err != nil {
		return err
	}
	return s.checkEvidenceRefs(ctx, actor, projectID, row.ChapterID, refs, snapshots)
}

func generationWorkspaceActor(ctx context.Context, actor generation.Actor) Actor {
	out := Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role}
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == actor.TenantID && caller.UserID == actor.UserID && caller.Role.IsValid() {
		out.Role = caller.Role
	}
	return out
}

func (a generationWorkspaceAuthorizer) AuthorizeHumanGeneration(ctx context.Context) error {
	if err := core.RequireHumanDecision(ctx); err != nil {
		return errors.Join(generation.ErrForbidden, err)
	}
	return nil
}
func generationBoundTemplate(p core.Project, legacy core.TemplateReader) (core.Template, string, error) {
	if p.TemplateCopy == nil {
		if legacy == nil {
			return core.Template{}, "", generation.ErrDependencyUnavailable
		}
		definition, err := legacy.Get(p.TemplateID, p.TemplateVersion)
		return definition, definition.RulesetHash, err
	}
	copy := p.TemplateCopy
	if copy.ID == "" || copy.Version != p.TemplateCopyVersion || copy.ProjectID != p.ID || copy.Status != core.TemplateCopyBound || copy.ContentHash == "" || copy.RulesetHash == "" {
		return core.Template{}, "", generation.ErrDependencyUnavailable
	}
	ruleHash := copy.RulesetHash
	if copy.RuleSetSnapshot != nil {
		if copy.RuleSetSnapshot.RulesetHash == "" || copy.RuleSetSnapshot.RulesetHash != ruleHash {
			return core.Template{}, "", generation.ErrDependencyUnavailable
		}
		ruleHash = copy.RuleSetSnapshot.RulesetHash
	}
	return lingdoctemplate.Clone(copy.Definition), ruleHash, nil
}

type rewriteTemplateBasis struct {
	CopyID          string `json:"copy_id,omitempty"`
	CopyVersion     int64  `json:"copy_version,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	RulesetHash     string `json:"ruleset_hash,omitempty"`
	ContextRevision int64  `json:"context_revision,omitempty"`
}

func rewriteProjectBasis(project core.Project) (rewriteTemplateBasis, error) {
	if project.TemplateCopy == nil {
		if project.TemplateCopyVersion > 0 {
			return rewriteTemplateBasis{}, ErrRewriteStale
		}
		return rewriteTemplateBasis{}, nil
	}
	_, ruleHash, err := generationBoundTemplate(project, nil)
	if err != nil {
		return rewriteTemplateBasis{}, ErrRewriteStale
	}
	return rewriteTemplateBasis{CopyID: project.TemplateCopy.ID, CopyVersion: project.TemplateCopy.Version, ContentHash: project.TemplateCopy.ContentHash, RulesetHash: ruleHash, ContextRevision: project.CurrentContextRevision}, nil
}
func (s *SelectedRewriteService) checkRowTemplate(ctx context.Context, actor Actor, projectID string, row selectedRewriteRow) error {
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		return selectedRewriteContextError(err)
	}
	var saved rewriteTemplateBasis
	raw := strings.TrimSpace(row.TemplateBasisJSON)
	if raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&saved); err != nil {
			return ErrRewriteStale
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return ErrRewriteStale
		}
	}
	current, err := rewriteProjectBasis(project)
	if err != nil || current != saved {
		return ErrRewriteStale
	}
	if current.CopyID != "" && (project.ID != projectID || project.Status != "active" || project.SpecRevision != row.SpecRevision) {
		return ErrRewriteStale
	}
	return nil
}

func selectedEvidenceConstraints(selected core.EvidenceSelection, refs []generation.EvidenceCardRef) ([]generation.EvidenceConstraint, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(selected.Cards) != len(refs) {
		return nil, generation.ErrSourceAccessDenied
	}
	allowed := map[string]bool{}
	for _, source := range selected.Sources {
		allowed[source.SourceID] = source.ModelUseAllowed
	}
	constraints := make([]generation.EvidenceConstraint, 0, len(refs))
	for _, ref := range refs {
		index := slices.IndexFunc(selected.Cards, func(card core.EvidenceCard) bool { return card.ID == ref.ID && card.Version == ref.Version })
		if index < 0 {
			return nil, generation.ErrSourceAccessDenied
		}
		card := selected.Cards[index]
		ids := stringSet(card.SourceIDs)
		if card.Scope == "" || len(ids) == 0 {
			return nil, generation.ErrSourceAccessDenied
		}
		if card.SupportType != "supports" && card.SupportType != "refutes" && card.SupportType != "qualifies" && card.SupportType != "background" {
			return nil, generation.ErrSourceAccessDenied
		}
		for _, id := range ids {
			if !allowed[id] {
				return nil, generation.ErrSourceAccessDenied
			}
		}
		constraints = append(constraints, generation.EvidenceConstraint{CardRef: ref, SupportType: card.SupportType, Scope: card.Scope, Limitations: card.Limitations, SourceIDs: append([]string(nil), ids...)})
	}
	return constraints, nil
}
func (v generationEvidenceValidator) ValidateGenerationEvidenceConstraints(ctx context.Context, actor generation.Actor, projectID, chapterID string, refs []generation.EvidenceCardRef, saved []generation.EvidenceConstraint) error {
	selected, err := selectFormalEvidence(ctx, v.selector, generationWorkspaceActor(ctx, actor), projectID, chapterID, evidenceCardVersionRefs(refs))
	if err != nil {
		return err
	}
	current, err := selectedEvidenceConstraints(selected, refs)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, saved) {
		return generation.ErrSourceAccessDenied
	}
	return nil
}
func decodeRewriteEvidenceConstraints(raw string) ([]generation.EvidenceConstraint, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var saved []generation.EvidenceConstraint
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return nil, ErrRewriteSourceDenied
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrRewriteSourceDenied
	}
	return saved, nil
}
func (s *SelectedRewriteService) checkRowEvidenceConstraints(ctx context.Context, actor Actor, projectID string, row selectedRewriteRow, refs []generation.EvidenceCardRef) error {
	saved, err := decodeRewriteEvidenceConstraints(row.EvidenceConstraintsJSON)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		if len(saved) > 0 {
			return ErrRewriteSourceDenied
		}
		return nil
	}
	selected, err := selectFormalEvidence(ctx, s.selector, actor, projectID, row.ChapterID, evidenceCardVersionRefs(refs))
	if err != nil {
		return ErrRewriteSourceDenied
	}
	current, err := selectedEvidenceConstraints(selected, refs)
	if err != nil || !reflect.DeepEqual(current, saved) {
		return ErrRewriteSourceDenied
	}
	return nil
}

const evidenceModelInstructions = "资料可能提供支持、反对、限制条件或背景；不得把反对材料写成支持结论，不得把局部事实推广为总体或因果效果。人工核对来源不表示科学结论已经成立。适用范围或限制不明、材料不足和冲突时应保留待核表述并列入 review_items。"
