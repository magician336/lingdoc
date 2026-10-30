package workspacecore

import (
	"context"
	"reflect"
	"slices"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// Legacy whole-chunk selections can prove the same snapshot with their
// existing excerpt hash. A partial legacy selection cannot prove the
// unselected text that would be included in a model context.
func anchorMatchesWholeSource(anchor SourceAnchor, text string) bool {
	wholeHash := sha256Hex(text)
	if anchor.SourceTextHash != "" {
		return anchor.SourceTextHash == wholeHash
	}
	return anchor.SelectionStart == 0 && anchor.SelectionEnd == len([]rune(text)) && anchor.QuotedTextHash == wholeHash
}

// validateAgentContextCards connects the confirmed evidence tuple to the exact
// authorized source snapshots already collected for this context. Reads of
// external originals remain outside the database transaction.
func (s *Service) validateAgentContextCards(ctx context.Context, actor Actor, projectID string, input AgentContextInput, pack AgentContextPackage) error {
	if len(input.CardRefs) == 0 {
		return nil
	}
	collected := make(map[string]AgentSourceRef, len(pack.SourceRefs))
	for _, source := range pack.SourceRefs {
		collected[source.SourceID] = source
	}
	checked := make([]EvidenceCard, 0, len(input.CardRefs))
	for _, ref := range input.CardRefs {
		card, err := s.loadCheckedEvidenceCard(ctx, actor, projectID, ref.ID, true)
		if err != nil {
			return err
		}
		if card.Version != ref.Version || !confirmationTupleCurrent(card) || input.ChapterID != "" && !slices.Contains(card.Confirmation.ScopeChapterIDs, input.ChapterID) {
			return ErrEvidenceIncomplete
		}
		var academic map[string]AgentSourceRef
		if len(card.AcademicAssociationRefs) > 0 {
			academic, err = s.readAcademicCardSnapshots(ctx, actor, projectID, input.ChapterID, card, true)
			if err != nil {
				return err
			}
			for id, snapshot := range academic {
				if actual, ok := collected[id]; !ok || !reflect.DeepEqual(actual, snapshot) {
					return ErrEvidenceIncomplete
				}
			}
		}
		anchors := make([]SourceAnchor, 0, len(card.AnchorRefs))
		err = s.readWithCapability(ctx, actor, projectID, "agent", func(tx Transaction, p Project) error {
			if !versionBasisCurrent(p, card.Basis) {
				return ErrVersionConflict
			}
			for _, anchorRef := range card.AnchorRefs {
				anchor, _, e := loadStoredObject[SourceAnchor](tx, projectID, SourceAnchorKind, anchorRef.ID)
				if e != nil || anchor.Version != anchorRef.Version {
					return ErrEvidenceIncomplete
				}
				anchors = append(anchors, anchor)
			}
			return nil
		})
		if err != nil {
			return err
		}
		anchorIDs := make([]string, 0, len(anchors))
		for _, anchor := range anchors {
			anchorIDs = append(anchorIDs, anchor.SourceID)
			if _, ok := academic[anchor.SourceID]; ok {
				continue
			}
			snapshot, ok := collected[anchor.SourceID]
			if !ok {
				return ErrEvidenceIncomplete
			}
			source, e := s.readResearchSource(ctx, actor, projectID, input.ChapterID, anchor.SourceID)
			if e != nil {
				return e
			}
			text := []rune(source.QuotedText)
			if source.Status != string(evidence.SourceAvailable) || source.ProcessingState != "ready" || !source.StableOriginal || anchor.Status != "CURRENT" || source.AssetID != anchor.AssetID || source.AssetRevision != anchor.AssetRevision || anchor.SelectionStart < 0 || anchor.SelectionEnd <= anchor.SelectionStart || anchor.SelectionEnd > len(text) || sha256Hex(string(text[anchor.SelectionStart:anchor.SelectionEnd])) != anchor.QuotedTextHash {
				return ErrEvidenceIncomplete
			}
			if !anchorMatchesWholeSource(anchor, source.QuotedText) {
				return ErrEvidenceIncomplete
			}
			if snapshot.AssetID != source.AssetID || snapshot.AssetRevision != source.AssetRevision || snapshot.QuotedTextHash != source.QuotedTextHash || snapshot.QuotedText != source.QuotedText || snapshot.Locator != source.Locator || snapshot.ModelUseAllowed != source.ModelUseAllowed {
				return ErrEvidenceIncomplete
			}
		}
		if !slices.Equal(sortedUniqueIDs(anchorIDs), sortedUniqueIDs(card.SourceIDs)) {
			return ErrEvidenceIncomplete
		}
		checked = append(checked, card)
	}
	// Evidence edits need not advance the project revision, so recheck the
	// actual cards after external reads rather than relying on project basis.
	return s.readWithCapability(ctx, actor, projectID, "agent", func(tx Transaction, p Project) error {
		for _, before := range checked {
			current, _, err := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, before.ID)
			if err != nil || !reflect.DeepEqual(current, before) || !confirmationTupleCurrent(current) || !versionBasisCurrent(p, current.Basis) {
				return ErrEvidenceIncomplete
			}
			for _, ref := range current.AnchorRefs {
				anchor, _, e := loadStoredObject[SourceAnchor](tx, projectID, SourceAnchorKind, ref.ID)
				if e != nil || anchor.Version != ref.Version || anchor.Status != "CURRENT" {
					return ErrEvidenceIncomplete
				}
			}
			claim, _, err := loadStoredObject[ResearchClaim](tx, projectID, ResearchClaimKind, current.ClaimRef.ID)
			if err != nil || claim.Version != current.ClaimRef.Version || claim.Status == "superseded" || claim.Basis.SpecRevision != p.SpecRevision {
				return ErrEvidenceIncomplete
			}
			if len(current.AcademicAssociationRefs) > 0 {
				snapshots, err := s.validateAcademicEvidenceSnapshots(ctx, tx, actor, p, current, current.Confirmation.AcademicSnapshots, true)
				if err != nil {
					return err
				}
				for id, snapshot := range snapshots {
					if actual, ok := collected[id]; !ok || !reflect.DeepEqual(actual, snapshot) {
						return ErrEvidenceIncomplete
					}
				}
			}
		}
		return nil
	})
}
