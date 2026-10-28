package candidateadoption

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// CandidateEvidencePolicy is an internal current-evidence adapter. Requests
// cannot supply a PASS result; explicit card references require both ports.
type CandidateEvidencePolicy interface {
	PrepareCandidateEvidence(context.Context, string, string, string, []core.VersionRef, []string) (context.Context, core.EvidenceSelection, error)
	ValidateCandidateEvidence(context.Context, string, string, string, []core.VersionRef, []string) (core.EvidenceSelection, error)
	ValidateCandidateEvidenceInTransaction(context.Context, *gorm.DB, string, string, string, []core.VersionRef, []string) (core.EvidenceSelection, error)
}

func validateNewBasis(current, proposed Basis) error {
	if proposed.ContextRevision < 0 || proposed.TemplateCopyVersion < 0 {
		return ErrInvalidRequest
	}
	if proposed.ContextRevision != 0 && proposed.ContextRevision != current.ContextRevision {
		return ErrStaleInput
	}
	if proposed.TemplateCopyID != "" && proposed.TemplateCopyID != current.TemplateCopyID {
		return ErrStaleInput
	}
	if proposed.TemplateCopyVersion != 0 && proposed.TemplateCopyVersion != current.TemplateCopyVersion {
		return ErrStaleInput
	}
	if proposed.TemplateCopyContentHash != "" && proposed.TemplateCopyContentHash != current.TemplateCopyContentHash {
		return ErrStaleInput
	}
	// A partial new copy basis cannot be interpreted as the actual pinned copy.
	if proposed.TemplateCopyID != "" || proposed.TemplateCopyVersion != 0 || proposed.TemplateCopyContentHash != "" {
		if proposed.TemplateCopyID == "" || proposed.TemplateCopyVersion == 0 || proposed.TemplateCopyContentHash == "" || proposed.RulesetHash == "" {
			return ErrStaleInput
		}
	}
	seen := map[string]bool{}
	for _, ref := range proposed.EvidenceCardRefs {
		if strings.TrimSpace(ref.ID) == "" || ref.Version < 1 || (ref.Kind != "" && ref.Kind != "EvidenceCard") || seen[ref.ID] {
			return ErrInvalidRequest
		}
		seen[ref.ID] = true
	}
	return nil
}

func candidateHasNewBasis(b Basis) bool {
	return b.ContextRevision != 0 || b.TemplateCopyID != "" || b.TemplateCopyVersion != 0 || b.TemplateCopyContentHash != "" || len(b.EvidenceCardRefs) > 0
}

func sameCandidateSnapshot(a, b Candidate) bool {
	a.SourceIDs = slices.Compact(normalizeIDs(a.SourceIDs))
	b.SourceIDs = slices.Compact(normalizeIDs(b.SourceIDs))
	return reflect.DeepEqual(a, b)
}

func currentAdoptionBasis(tx *gorm.DB, project projectRow, chapter Chapter) (Basis, error) {
	basis := Basis{SpecRevision: int(project.SpecRevision), ChapterVersionID: chapter.CurrentVersionID, TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion, ContextRevision: project.CurrentContextRevision}
	if project.TemplateCopyVersion > 0 {
		var copy projectTemplateCopyRow
		if err := tx.Where("project_id = ? AND version = ?", project.ID, project.TemplateCopyVersion).First(&copy).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return basis, ErrStaleInput
			}
			return basis, err
		}
		if copy.ID == "" || copy.Status != core.TemplateCopyBound || copy.ContentHash == "" || copy.RulesetHash == "" {
			return basis, ErrStaleInput
		}
		basis.TemplateCopyID = copy.ID
		basis.TemplateCopyVersion = copy.Version
		basis.TemplateCopyContentHash = copy.ContentHash
		basis.RulesetHash = copy.RulesetHash // persisted effective project rules, not source catalog rules
	}
	var assets []projectAssetRow
	if err := tx.Where("project_id = ? AND deleted_at IS NULL", project.ID).Order("id ASC").Find(&assets).Error; err != nil {
		return basis, err
	}
	basis.AssetVersions = make([]AssetVersion, 0, len(assets))
	for _, asset := range assets {
		basis.AssetVersions = append(basis.AssetVersions, AssetVersion{AssetID: asset.ID, AssetRevision: asset.AssetRevision})
	}
	return basis, nil
}

// The card list must represent evidence actually used in the chapter; a
// confirmed but unrelated card cannot manufacture a dependency edge.
func validateEvidenceSelection(refs []core.VersionRef, sourceIDs []string, selected core.EvidenceSelection) error {
	if len(refs) == 0 {
		return nil
	}
	if len(selected.Cards) != len(refs) || len(selected.Pending) > 0 || len(sourceIDs) == 0 {
		return ErrSourceAccessDenied
	}
	sources := map[string]bool{}
	for _, source := range selected.Sources {
		if !source.ModelUseAllowed {
			return ErrSourceAccessDenied
		}
		sources[source.SourceID] = true
	}
	for _, id := range sourceIDs {
		if !sources[id] {
			return ErrSourceAccessDenied
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" || ref.Version < 1 || (ref.Kind != "" && ref.Kind != "EvidenceCard") || seen[ref.ID] {
			return ErrInvalidRequest
		}
		seen[ref.ID] = true
		index := slices.IndexFunc(selected.Cards, func(card core.EvidenceCard) bool { return card.ID == ref.ID && card.Version == ref.Version })
		if index < 0 {
			return ErrSourceAccessDenied
		}
		card := selected.Cards[index]
		if card.Status != "CONFIRMED" || card.Confirmation == nil || card.Confirmation.CardVersion != card.Version || card.Confirmation.ConfirmedBy == "" {
			return ErrSourceAccessDenied
		}
		used := false
		for _, id := range card.SourceIDs {
			if slices.Contains(sourceIDs, id) {
				used = true
			}
		}
		if !used {
			return ErrSourceAccessDenied
		}
	}
	return nil
}

func (s *SQLiteCandidateAdoptionStore) validateEvidenceInTransaction(ctx context.Context, tx *gorm.DB, in AcceptCandidateInput, candidate Candidate) error {
	if s.evidencePolicy == nil {
		if len(candidate.Basis.EvidenceCardRefs) == 0 {
			return nil
		}
		return ErrDependencyUnavailable
	}
	selected, err := s.evidencePolicy.ValidateCandidateEvidenceInTransaction(ctx, tx, in.ProjectID, in.ChapterID, in.ActorID, candidate.Basis.EvidenceCardRefs, candidate.SourceIDs)
	if err != nil {
		return err
	}
	return validateCandidateSelection(candidate, selected)
}

func storeAdoptedEvidenceDependencies(tx *gorm.DB, in AcceptCandidateInput, candidate Candidate, versionID string) error {
	for _, ref := range candidate.Basis.EvidenceCardRefs {
		if err := core.StoreConfirmedDependency(core.NewTransactionAdapter(tx), core.DependencyEdge{ProjectID: in.ProjectID, Source: core.ObjectVersionRef{Kind: "EvidenceCard", ID: ref.ID, Version: strconv.FormatInt(ref.Version, 10)}, Target: core.ObjectVersionRef{Kind: "ChapterVersion", ID: in.ChapterID, Version: versionID}, Relation: "cited_evidence", Action: "INVALIDATE_CONFIRMATION", Origin: "user", ActorID: in.ActorID, Reason: "采纳候选所实际引用的当前人工确认资料卡"}); err != nil {
			return err
		}
	}
	return nil
}

type TransactionAuthorizer interface {
	AuthorizeCandidateInTransaction(context.Context, *gorm.DB, string, string, string) error
}
type acceptanceReplayProbe interface {
	CandidateAcceptancePending(context.Context, AcceptCandidateInput) (bool, error)
}

func (s *SQLiteCandidateAdoptionStore) authorizeCandidateInTransaction(ctx context.Context, tx *gorm.DB, in AcceptCandidateInput) error {
	if port, ok := s.authorizer.(TransactionAuthorizer); ok {
		return port.AuthorizeCandidateInTransaction(ctx, tx, in.ActorID, in.ProjectID, "write:"+in.ChapterID)
	}
	// Only explicit persistence fixtures may omit the domain authorization port.
	// Never call the main-pool Authorizer while this tx holds SQLite's connection.
	if s.focusedSchema {
		return nil
	}
	return ErrDependencyUnavailable
}
func (s *SQLiteCandidateAdoptionStore) CandidateAcceptancePending(ctx context.Context, in AcceptCandidateInput) (bool, error) {
	var row adoptionIdempotencyRow
	err := s.db.WithContext(ctx).Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := compareAdoptionDigest(row, requestHash(in)); err != nil {
		return false, err
	}
	return true, nil
}
func (s *CandidateAdoptionService) prepareCandidateEvidence(ctx context.Context, in AcceptCandidateInput, candidate Candidate) (context.Context, error) {
	if err := s.validateSources(ctx, in.ProjectID, in.ActorID, candidate.SourceIDs); err != nil {
		return ctx, err
	}
	if s.Evidence == nil {
		if len(candidate.Basis.EvidenceCardRefs) > 0 {
			return ctx, ErrDependencyUnavailable
		}
		return ctx, nil
	}
	prepared, selected, err := s.Evidence.PrepareCandidateEvidence(ctx, in.ProjectID, in.ChapterID, in.ActorID, candidate.Basis.EvidenceCardRefs, candidate.SourceIDs)
	if err != nil {
		return ctx, err
	}
	if err = validateCandidateSelection(candidate, selected); err != nil {
		return ctx, err
	}
	return prepared, nil
}
func (s *CandidateAdoptionService) recheckCandidateAcceptance(ctx context.Context, in AcceptCandidateInput, candidate Candidate) error {
	if err := s.authorize(ctx, in.ActorID, in.ProjectID, "write:"+in.ChapterID); err != nil {
		return err
	}
	if err := s.validateSources(ctx, in.ProjectID, in.ActorID, candidate.SourceIDs); err != nil {
		return err
	}
	_, err := s.prepareCandidateEvidence(ctx, in, candidate)
	return err
}
func validateCandidateSelection(candidate Candidate, selected core.EvidenceSelection) error {
	if err := validateEvidenceSelection(candidate.Basis.EvidenceCardRefs, candidate.SourceIDs, selected); err != nil {
		return err
	}
	if err := validateClaimEvidence(candidate, selected); err != nil {
		return err
	}
	if len(candidate.Basis.EvidenceCardRefs) == 0 && !candidateHasNewBasis(candidate.Basis) {
		return nil
	}
	versions := map[string]int{}
	for _, asset := range candidate.Basis.AssetVersions {
		if asset.AssetID == "" || asset.AssetRevision < 1 || versions[asset.AssetID] != 0 {
			return ErrStaleInput
		}
		versions[asset.AssetID] = asset.AssetRevision
	}
	for _, source := range selected.Sources {
		if source.AssetID == "" || versions[source.AssetID] != source.AssetRevision {
			return ErrStaleInput
		}
	}
	return nil
}
