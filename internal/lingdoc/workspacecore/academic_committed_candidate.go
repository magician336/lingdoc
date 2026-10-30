package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"gorm.io/gorm/clause"
)

// ValidateCommittedAcademicCandidate only reads a specific server-persisted
// formal adoption. It cannot authorize a new adoption or write any content.
// The digest is independently checked by the adoption store against the full
// request before invoking this method.
func (s *Service) ValidateCommittedAcademicCandidate(ctx context.Context, tx Transaction, actor Actor, projectID, chapterID, candidateID, key, digest, versionID string, refs []VersionRef) (EvidenceSelection, error) {
	empty := EvidenceSelection{}
	db, err := TransactionDatabase(tx)
	if err != nil || versionID == "" || digest == "" {
		return empty, ErrEvidenceIncomplete
	}
	var adoption struct{ ChapterVersionID, RequestHash string }
	if err = db.Table("lingdoc_candidate_adoptions").Select("chapter_version_id", "request_hash").Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", projectID, chapterID, actor.UserID, key).Take(&adoption).Error; err != nil || adoption.ChapterVersionID != versionID || adoption.RequestHash != digest {
		return empty, ErrEvidenceIncomplete
	}
	var version struct{ CandidateID, BodyMarkdown, SourceIDsJSON string }
	if err = db.Table("lingdoc_chapter_versions").Select("candidate_id", "body_markdown", "source_ids_json").Where("id = ? AND project_id = ? AND chapter_id = ?", versionID, projectID, chapterID).Take(&version).Error; err != nil || version.CandidateID != candidateID {
		return empty, ErrEvidenceIncomplete
	}
	var stored struct{ BasisJSON, BodyMarkdown, SourceIDsJSON string }
	if err = db.Table("lingdoc_candidates").Select("basis_json", "body_markdown", "source_ids_json").Where("id = ? AND project_id = ? AND chapter_id = ?", candidateID, projectID, chapterID).Take(&stored).Error; err != nil || version.BodyMarkdown != stored.BodyMarkdown {
		return empty, ErrEvidenceIncomplete
	}
	var oldIDs, newIDs []string
	if json.Unmarshal([]byte(version.SourceIDsJSON), &oldIDs) != nil || json.Unmarshal([]byte(stored.SourceIDsJSON), &newIDs) != nil || !slices.Equal(sortedUniqueIDs(oldIDs), sortedUniqueIDs(newIDs)) {
		return empty, ErrEvidenceIncomplete
	}
	var basis struct {
		EvidenceCardRefs []VersionRef `json:"evidence_card_refs"`
	}
	if json.Unmarshal([]byte(stored.BasisJSON), &basis) != nil || candidateRefsHash(basis.EvidenceCardRefs) != candidateRefsHash(refs) || len(refs) == 0 {
		return empty, ErrEvidenceIncomplete
	}
	selected := EvidenceSelection{}
	for _, ref := range refs {
		card, _, e := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, ref.ID)
		if e != nil || card.Version != ref.Version || !confirmationTupleCurrent(card) || !slices.Contains(card.Confirmation.ScopeChapterIDs, chapterID) {
			return empty, ErrEvidenceIncomplete
		}
		selected.Cards = append(selected.Cards, card)
	}
	p, err := s.lockCandidateAcademicEvidence(ctx, tx, actor, projectID, chapterID, selected)
	if err != nil {
		return empty, err
	}
	var ordinary committedOrdinaryProof
	if mixedCandidateCards(selected.Cards) {
		var ok bool
		ordinary, ok = ctx.Value(committedOrdinaryProofKey{}).(committedOrdinaryProof)
		if !ok || ordinary.actor != actor || ordinary.projectID != projectID || ordinary.chapterID != chapterID || ordinary.identityHash != candidateIdentityHash(ctx) || ordinary.refsHash != candidateRefsHash(refs) {
			return empty, ErrEvidenceIncomplete
		}
		if err = s.lockMixedOrdinarySources(ctx, tx, actor, p, chapterID, selected.Cards, ordinary.sources); err != nil {
			return empty, err
		}
	} else if !slices.ContainsFunc(selected.Cards, func(card EvidenceCard) bool { return len(card.AcademicAssociationRefs) > 0 }) {
		return empty, ErrEvidenceIncomplete
	}
	// Re-read and lock the records that bind this result after taking the
	// project lock: none of the earlier optimistic reads is an authorization.
	var lockedAdoption struct{ ChapterVersionID, RequestHash string }
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Table("lingdoc_candidate_adoptions").Select("chapter_version_id", "request_hash").Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", projectID, chapterID, actor.UserID, key).Take(&lockedAdoption).Error; err != nil || lockedAdoption != adoption {
		return empty, ErrEvidenceIncomplete
	}
	var lockedVersion struct{ CandidateID, BodyMarkdown, SourceIDsJSON string }
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Table("lingdoc_chapter_versions").Select("candidate_id", "body_markdown", "source_ids_json").Where("id = ? AND project_id = ? AND chapter_id = ?", versionID, projectID, chapterID).Take(&lockedVersion).Error; err != nil || lockedVersion != version {
		return empty, ErrEvidenceIncomplete
	}
	var lockedCandidate struct{ BasisJSON, BodyMarkdown, SourceIDsJSON string }
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Table("lingdoc_candidates").Select("basis_json", "body_markdown", "source_ids_json").Where("id = ? AND project_id = ? AND chapter_id = ?", candidateID, projectID, chapterID).Take(&lockedCandidate).Error; err != nil || lockedCandidate != stored {
		return empty, ErrEvidenceIncomplete
	}
	chapter, err := tx.Chapter(projectID, chapterID)
	if err != nil || chapter.CurrentVersionID == nil || *chapter.CurrentVersionID != versionID {
		return empty, ErrEvidenceIncomplete
	}
	selected.ContextRevision = p.CurrentContextRevision
	for _, card := range selected.Cards {
		current, _, e := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, card.ID)
		if e != nil || !reflect.DeepEqual(current, card) {
			return empty, ErrEvidenceIncomplete
		}
		frozen := map[string]AgentSourceRef{}
		if len(card.AcademicAssociationRefs) > 0 {
			frozen, e = s.validateAcademicEvidenceSnapshotsAtBasis(ctx, tx, actor, p, card, card.Confirmation.AcademicSnapshots, true, true)
			if e != nil || len(frozen) != len(sortedUniqueIDs(card.SourceIDs)) {
				return empty, ErrEvidenceIncomplete
			}
		} else {
			index := slices.IndexFunc(ordinary.cards, func(v EvidenceCard) bool { return v.ID == card.ID })
			reader, ok := tx.(interface {
				CurrentEvidenceObject(string, string, string) (ImpactObjectState, error)
			})
			if !ok || index < 0 || !reflect.DeepEqual(ordinary.cards[index], card) {
				return empty, ErrEvidenceIncomplete
			}
			state, e := reader.CurrentEvidenceObject(projectID, "EvidenceCard", card.ID)
			if e != nil || !state.Confirmed || state.Ref.Version != fmt.Sprint(card.Version) {
				return empty, ErrEvidenceIncomplete
			}
			for _, id := range card.SourceIDs {
				index := slices.IndexFunc(ordinary.sources, func(v AgentSourceRef) bool { return v.SourceID == id })
				if index < 0 {
					return empty, ErrEvidenceIncomplete
				}
				frozen[id] = ordinary.sources[index]
			}
		}
		for _, ref := range card.AnchorRefs {
			anchor, _, e := loadStoredObject[SourceAnchor](tx, projectID, SourceAnchorKind, ref.ID)
			if e != nil || anchor.Version != ref.Version {
				return empty, ErrEvidenceIncomplete
			}
			if !slices.ContainsFunc(selected.Anchors, func(a SourceAnchor) bool { return a.ID == anchor.ID && a.Version == anchor.Version }) {
				selected.Anchors = append(selected.Anchors, anchor)
			}
		}
		for _, id := range card.SourceIDs {
			source, ok := frozen[id]
			if !ok {
				return empty, ErrEvidenceIncomplete
			}
			index := slices.IndexFunc(selected.Sources, func(v AgentSourceRef) bool { return v.SourceID == id })
			if index < 0 {
				selected.Sources = append(selected.Sources, source)
			} else if selected.Sources[index] != source {
				return empty, ErrEvidenceIncomplete
			}
		}
	}
	return selected, nil
}
