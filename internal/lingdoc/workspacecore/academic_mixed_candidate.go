package workspacecore

import (
	"context"
	"reflect"
	"slices"

	"github.com/Tencent/WeKnora/internal/evidence"
	"gorm.io/gorm/clause"
)

type committedOrdinaryProofKey struct{}
type committedOrdinaryProof struct {
	actor                                        Actor
	projectID, chapterID, identityHash, refsHash string
	cards                                        []EvidenceCard
	sources                                      []AgentSourceRef
}

func mixedCandidateCards(cards []EvidenceCard) bool {
	academic, ordinary := false, false
	for _, card := range cards {
		if len(card.AcademicAssociationRefs) > 0 {
			academic = true
		} else {
			ordinary = true
		}
	}
	return academic && ordinary
}

// The preview performs external source reads outside the SQLite transaction.
// It is never itself authorization: the consumer must bind it to the stored
// adoption and repeat all native decisions while holding the write locks.
func (s *Service) PrepareCommittedCandidateEvidence(ctx context.Context, actor Actor, projectID, chapterID string, refs []VersionRef) (context.Context, error) {
	if err := s.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		return ctx, err
	}
	proof := committedOrdinaryProof{actor: actor, projectID: projectID, chapterID: chapterID, identityHash: candidateIdentityHash(ctx), refsHash: candidateRefsHash(refs)}
	for _, ref := range refs {
		var card EvidenceCard
		err := s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, _ Project) error {
			var e error
			card, _, e = loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, ref.ID)
			return e
		})
		if err != nil || card.Version != ref.Version {
			return ctx, ErrEvidenceIncomplete
		}
		if len(card.AcademicAssociationRefs) > 0 {
			continue
		}
		card, err = s.loadCheckedEvidenceCard(ctx, actor, projectID, ref.ID, true)
		if err != nil || !confirmationTupleCurrent(card) || !slices.Contains(card.Confirmation.ScopeChapterIDs, chapterID) {
			return ctx, ErrEvidenceIncomplete
		}
		proof.cards = append(proof.cards, card)
		for _, id := range card.SourceIDs {
			source, e := s.readResearchSource(ctx, actor, projectID, chapterID, id)
			if e != nil || source.SourceID != id || source.Status != string(evidence.SourceAvailable) || source.ProcessingState != "ready" || !source.StableOriginal || !source.ModelUseAllowed || source.QuotedTextHash != sha256Hex(source.QuotedText) {
				return ctx, ErrEvidenceIncomplete
			}
			value := AgentSourceRef{SourceID: source.SourceID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, QuotedTextHash: source.QuotedTextHash, Locator: source.Locator, QuotedText: source.QuotedText, ModelUseAllowed: true}
			index := slices.IndexFunc(proof.sources, func(v AgentSourceRef) bool { return v.SourceID == id })
			if index < 0 {
				proof.sources = append(proof.sources, value)
			} else if !reflect.DeepEqual(proof.sources[index], value) {
				return ctx, ErrEvidenceIncomplete
			}
		}
	}
	return context.WithValue(ctx, committedOrdinaryProofKey{}, proof), nil
}

// The academic project and actor locks must already be held by the caller.
func (s *Service) lockMixedOrdinarySources(ctx context.Context, tx Transaction, actor Actor, p Project, chapterID string, cards []EvidenceCard, sources []AgentSourceRef) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return err
	}
	for _, card := range cards {
		if len(card.AcademicAssociationRefs) > 0 {
			continue
		}
		for _, ref := range card.AnchorRefs {
			anchor, _, e := loadStoredObject[SourceAnchor](tx, p.ID, SourceAnchorKind, ref.ID)
			if e != nil || anchor.Version != ref.Version {
				return ErrEvidenceIncomplete
			}
			index := slices.IndexFunc(sources, func(v AgentSourceRef) bool { return v.SourceID == anchor.SourceID })
			if index < 0 || sources[index].Locator != anchor.Locator || sources[index].AssetID != anchor.AssetID || sources[index].AssetRevision != anchor.AssetRevision || !anchorMatchesWholeSource(anchor, sources[index].QuotedText) {
				return ErrEvidenceIncomplete
			}
		}
		for _, id := range card.SourceIDs {
			index := slices.IndexFunc(sources, func(v AgentSourceRef) bool { return v.SourceID == id })
			if index < 0 {
				return ErrEvidenceIncomplete
			}
			source := sources[index]
			if !source.ModelUseAllowed || source.QuotedTextHash != sha256Hex(source.QuotedText) {
				return ErrEvidenceIncomplete
			}
			var asset evidence.ProjectAsset
			if err = db.Where("id = ? AND project_id = ? AND tenant_id = ? AND deleted_at IS NULL", source.AssetID, p.ID, actor.TenantID).Take(&asset).Error; err != nil {
				return ErrEvidenceIncomplete
			}
			if err = validateAssociationSourceInTransaction(ctx, tx, actor, p, ResearchSource{SourceID: id, AssetID: source.AssetID, AssetRevision: source.AssetRevision, KnowledgeID: asset.KnowledgeID, QuotedTextHash: source.QuotedTextHash}); err != nil {
				return err
			}
			var revision evidence.ProjectAssetRevision
			if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("asset_id = ? AND revision_no = ?", source.AssetID, source.AssetRevision).Take(&revision).Error; err != nil {
				return ErrEvidenceIncomplete
			}
			if err = s.authorizeAcademicEvidenceSourceInTransaction(ctx, tx, actor, p.ID, chapterID, source.AssetID); err != nil {
				return err
			}
		}
	}
	_, err = candidateNativePolicyDigest(ctx, tx, actor, p, sources)
	return err
}
