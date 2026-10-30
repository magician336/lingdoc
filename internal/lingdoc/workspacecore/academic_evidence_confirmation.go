package workspacecore

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// The operation ledger and card have already committed, but a subsequent
// permission/currentness check prevents disclosure of the recorded response.
var ErrAcademicConfirmationCommittedUnreadable = clienterror.New(clienterror.Form{Status: 409, Code: "confirmation_committed_unreadable", Message: "确认已提交，但当前无法读取结果；请核对来源和权限，恢复后使用相同请求重试。", Retryable: false}, "lingdoc: confirmation committed but result is not currently readable")

var ErrAcademicCardSaveCommittedUnreadable = clienterror.New(clienterror.Form{Status: 409, Code: "evidence_card_save_committed_unreadable", Message: "证据卡已保存，但当前无法读取结果；请核对来源和权限，恢复后使用相同请求重试。", Retryable: false}, "lingdoc: evidence card save committed but result is not currently readable")

// This recheck is read-only in effect but must permit row locks. PostgreSQL
// disallows FOR UPDATE in SQL READ ONLY transactions.
func (s *Service) withAcademicConfirmationAuthority(ctx context.Context, actor Actor, projectID string, chapters []string, read func(Transaction, Project) error) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := lockAcademicActorRows(tx, actor, projectID); err != nil {
			return err
		}
		if err := lockAcademicEvidenceChapters(tx, projectID, chapters); err != nil {
			return err
		}
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		return read(tx, p)
	})
}

// confirmAcademicEvidenceCard freezes the reviewed original and extracted
// text in the same transaction as the new confirmation. The snapshot is a
// historical copy, never an independent grant to read or use the source.
func (s *Service) confirmAcademicEvidenceCard(ctx context.Context, actor Actor, projectID, cardID, key string, input ConfirmationInput, initial EvidenceCard) (json.RawMessage, int, bool, error) {
	materials := map[string]AcademicSnapshotMaterial{}
	if initial.Status != "CONFIRMED" {
		checked, err := s.loadCheckedEvidenceCard(ctx, actor, projectID, cardID, false)
		if err != nil || !reflect.DeepEqual(checked, initial) {
			return nil, 0, false, ErrEvidenceIncomplete
		}
		reader, ok := s.agentRuntime.Sources.(AcademicSnapshotMaterialReader)
		if !ok {
			return nil, 0, false, ErrEvidenceIncomplete
		}
		if len(initial.ScopeChapterIDs) == 0 {
			return nil, 0, false, ErrEvidenceIncomplete
		}
		for _, sourceID := range sortedUniqueIDs(initial.SourceIDs) {
			for _, chapterID := range sortedUniqueIDs(initial.ScopeChapterIDs) {
				material, err := reader.ReadAcademicSnapshotMaterial(ctx, actor, projectID, chapterID, sourceID)
				if err != nil {
					return nil, 0, false, err
				}
				if material.Source.SourceID != sourceID || !material.Source.StableOriginal || material.Source.QuotedText != material.SourceText || material.Source.QuotedTextHash != sha256Hex(material.SourceText) || material.RawSHA256 != sha256Hex(string(material.Raw)) {
					return nil, 0, false, ErrEvidenceIncomplete
				}
				if previous, exists := materials[sourceID]; exists && !reflect.DeepEqual(previous, material) {
					return nil, 0, false, ErrEvidenceIncomplete
				}
				materials[sourceID] = material
			}
		}
	}
	body, status, replay, err := s.runAgentOperation(ctx, actor, projectID, cardConfirmationCapability(initial), "confirmEvidenceCard", projectID+"/"+cardID, key, input, initial.SourceIDs, operationcore.HumanGateRequired, func(tx Transaction, _ Project) (any, int, error) {
		if err := lockAcademicActorRows(tx, actor, projectID); err != nil {
			return nil, 0, err
		}
		if err := lockAcademicEvidenceProject(tx, actor, projectID); err != nil {
			return nil, 0, err
		}
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return nil, 0, err
		}
		value, old, err := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, cardID)
		if err != nil {
			return nil, 0, err
		}
		if p.Status != "active" || !expectationMatches(p, input.VersionExpectation) || value.Version != input.ExpectedVersion || !reflect.DeepEqual(value, initial) {
			return nil, 0, ErrVersionConflict
		}
		if value.Status != "CANDIDATE" && value.Status != "NEEDS_REVIEW" && value.Status != "INSUFFICIENT" {
			return nil, 0, ErrInvalidState
		}
		if value.ClaimRef == nil || value.Scope == "" || value.SupportType == "" || len(value.ScopeChapterIDs) == 0 || len(materials) == 0 {
			return nil, 0, ErrEvidenceIncomplete
		}
		claim, _, err := loadStoredObject[ResearchClaim](tx, projectID, ResearchClaimKind, value.ClaimRef.ID)
		if err != nil || claim.Version != value.ClaimRef.Version || claim.Status == "superseded" || claim.Basis.SpecRevision != p.SpecRevision {
			return nil, 0, ErrEvidenceIncomplete
		}
		for _, chapterID := range value.ScopeChapterIDs {
			if _, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationEvidenceConfirm, chapterID)); err != nil {
				return nil, 0, err
			}
		}
		if err = lockAcademicEvidenceChapters(tx, projectID, value.ScopeChapterIDs); err != nil {
			return nil, 0, err
		}
		snapshots := make([]AcademicEvidenceSnapshot, 0, len(materials))
		for _, sourceID := range sortedUniqueIDs(value.SourceIDs) {
			material, ok := materials[sourceID]
			if !ok {
				return nil, 0, ErrEvidenceIncomplete
			}
			// Serialize native source state with edits and policy changes before
			// binding the captured bytes to the human confirmation.
			if err = validateAssociationSourceInTransaction(ctx, tx, actor, p, material.Source); err != nil {
				return nil, 0, err
			}
			for _, chapterID := range sortedUniqueIDs(value.ScopeChapterIDs) {
				if err = s.authorizeAcademicEvidenceSourceInTransaction(ctx, tx, actor, projectID, chapterID, material.Source.AssetID); err != nil {
					return nil, 0, err
				}
			}
			rawRef, err := PutAcademicOriginalSnapshot(tx, actor.TenantID, projectID, material.Raw)
			if err != nil {
				return nil, 0, err
			}
			textRef, err := PutAcademicOriginalSnapshot(tx, actor.TenantID, projectID, []byte(material.SourceText))
			if err != nil {
				return nil, 0, err
			}
			snapshot := AcademicEvidenceSnapshot{SourceID: sourceID, KnowledgeID: material.Source.KnowledgeID, AssetID: material.Source.AssetID, AssetRevision: material.Source.AssetRevision, Locator: material.Source.Locator, Page: material.Source.VerifiedPDFPage, RawRef: rawRef, TextRef: textRef}
			for _, ref := range value.AnchorRefs {
				anchor, _, err := loadStoredObject[SourceAnchor](tx, projectID, SourceAnchorKind, ref.ID)
				if err != nil {
					return nil, 0, err
				}
				if anchor.SourceID == sourceID {
					snapshot.AnchorRefs = append(snapshot.AnchorRefs, ref)
				}
			}
			for _, ref := range value.AcademicAssociationRefs {
				association, _, err := loadStoredObject[AcademicSourceAssociation](tx, projectID, AcademicSourceAssociationKind, ref.ID)
				if err != nil {
					return nil, 0, err
				}
				if association.SourceID == sourceID {
					snapshot.Associations = append(snapshot.Associations, AcademicSnapshotAssociation{AssociationRef: ref, CandidateRef: association.CandidateRef, DOI: association.DOI, CatalogVersion: association.CatalogVersion, PolicySnapshotVersion: association.PolicySnapshotVersion})
				}
			}
			snapshots = append(snapshots, snapshot)
		}
		if _, err = s.validateAcademicEvidenceSnapshots(ctx, tx, actor, p, value, snapshots, false); err != nil {
			return nil, 0, err
		}
		value.Version++
		value.Status = "CONFIRMED"
		value.InvalidReason = ""
		value.Basis = versionBasisFor(p, Chapter{})
		value.Confirmation = &EvidenceConfirmation{ConfirmedBy: actor.UserID, ConfirmedAt: time.Now().UTC(), CardVersion: value.Version, ClaimRef: *value.ClaimRef, AnchorRefs: slices.Clone(value.AnchorRefs), ContextRevision: p.CurrentContextRevision, ScopeChapterIDs: slices.Clone(value.ScopeChapterIDs), Reason: input.Reason, Meaning: "来源片段及历史原件快照在指定范围内经人工核对；不表示研究主张已被证明。", AcademicSnapshots: snapshots}
		if err = storeStoredObject(tx, old, projectID, EvidenceCardKind, value.ID, value.Version, value); err != nil {
			return nil, 0, err
		}
		return value, 200, publishCardDependencies(tx, actor, p, value)
	})
	if err != nil {
		return body, status, replay, err
	}
	// Both newly committed results and replays must pass current source and
	// chapter access before their recorded card can be returned. Failure here
	// cannot roll back the already committed operation.
	var recorded EvidenceCard
	if err = json.Unmarshal(body, &recorded); err != nil || replay && !reflect.DeepEqual(recorded, initial) {
		return nil, 0, replay, ErrAcademicConfirmationCommittedUnreadable
	}
	for _, chapterID := range recorded.ScopeChapterIDs {
		if _, err = s.readAcademicCardSnapshots(ctx, actor, projectID, chapterID, recorded, false); err != nil {
			return nil, 0, replay, ErrAcademicConfirmationCommittedUnreadable
		}
	}
	// The chapter-citation fallback locks its cited chunks. Use a regular
	// transaction: PostgreSQL rejects FOR UPDATE inside READ ONLY. Keep the
	// same actor -> project -> chapters lock order as the confirmation write.
	err = s.withAcademicConfirmationAuthority(ctx, actor, projectID, recorded.ScopeChapterIDs, func(tx Transaction, p Project) error {
		current, _, e := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, cardID)
		if e != nil || !confirmationTupleCurrent(current) || !reflect.DeepEqual(current, recorded) {
			return ErrEvidenceIncomplete
		}
		for _, chapterID := range current.ScopeChapterIDs {
			if _, e = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationEvidenceConfirm, chapterID)); e != nil {
				return e
			}
			for _, snapshot := range current.Confirmation.AcademicSnapshots {
				if e = s.authorizeAcademicEvidenceSourceInTransaction(ctx, tx, actor, projectID, chapterID, snapshot.AssetID); e != nil {
					return e
				}
			}
		}
		if _, e = s.validateAcademicEvidenceSnapshots(ctx, tx, actor, p, current, current.Confirmation.AcademicSnapshots, false); e != nil {
			return e
		}
		body, e = json.Marshal(current)
		return e
	})
	if err != nil {
		return nil, 0, replay, ErrAcademicConfirmationCommittedUnreadable
	}
	return body, status, replay, nil
}
