package workspacecore

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

// readAcademicCardSnapshots validates stored refs under the current project,
// KB, asset and model permissions, checks chapter scope through the existing
// source port, then repeats the transactional checks after that external IO.
// Only the stored snapshot text is returned to the caller.
func (s *Service) readAcademicCardSnapshots(ctx context.Context, actor Actor, projectID, chapterID string, card EvidenceCard, forModel bool) (map[string]AgentSourceRef, error) {
	if !confirmationTupleCurrent(card) || card.ProjectID != projectID || forModel && chapterID == "" || chapterID != "" && !slices.Contains(card.Confirmation.ScopeChapterIDs, chapterID) {
		return nil, ErrEvidenceIncomplete
	}
	read := func() (map[string]AgentSourceRef, error) {
		var out map[string]AgentSourceRef
		err := s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, p Project) error {
			current, _, e := loadStoredObject[EvidenceCard](tx, projectID, EvidenceCardKind, card.ID)
			if e != nil || !reflect.DeepEqual(current, card) || !confirmationTupleCurrent(current) || !versionBasisCurrent(p, current.Basis) {
				return ErrEvidenceIncomplete
			}
			out, e = s.validateAcademicEvidenceSnapshots(ctx, tx, actor, p, current, current.Confirmation.AcademicSnapshots, forModel)
			return e
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	first, err := read()
	if err != nil {
		return nil, err
	}
	for sourceID, snapshot := range first {
		current, e := s.readResearchSource(ctx, actor, projectID, chapterID, sourceID)
		if e != nil {
			return nil, e
		}
		// Do not use the live body or locator as model input. The reader is
		// consulted here solely for current scope, status and model consent.
		knowledgeID := ""
		for _, binding := range card.Confirmation.AcademicSnapshots {
			if binding.SourceID == sourceID {
				knowledgeID = binding.KnowledgeID
				break
			}
		}
		if current.SourceID != sourceID || current.KnowledgeID != knowledgeID || current.Status != string(evidence.SourceAvailable) || current.ProcessingState != "ready" || current.AssetID != snapshot.AssetID || current.AssetRevision != snapshot.AssetRevision || current.ModelUseAllowed != snapshot.ModelUseAllowed || forModel && !current.ModelUseAllowed {
			return nil, ErrEvidenceIncomplete
		}
	}
	second, err := read()
	if err != nil || !reflect.DeepEqual(first, second) {
		if err != nil {
			return nil, err
		}
		return nil, ErrEvidenceIncomplete
	}
	return second, nil
}

// validateAcademicEvidenceSnapshots rechecks every durable binding in the
// caller's already authorized transaction. It reads only immutable snapshot
// bytes, never the live file. forModel additionally requires model-use consent.
// Callers must separately enforce the current chapter-scoped authorization.
func (s *Service) validateAcademicEvidenceSnapshots(ctx context.Context, tx Transaction, actor Actor, p Project, card EvidenceCard, snapshots []AcademicEvidenceSnapshot, forModel bool) (map[string]AgentSourceRef, error) {
	return s.validateAcademicEvidenceSnapshotsAtBasis(ctx, tx, actor, p, card, snapshots, forModel, false)
}

// historicalAdoption is used only after the persisted adoption transaction is
// verified. The one project-version step is that transaction's own commit.
func (s *Service) validateAcademicEvidenceSnapshotsAtBasis(ctx context.Context, tx Transaction, actor Actor, p Project, card EvidenceCard, snapshots []AcademicEvidenceSnapshot, forModel, historicalAdoption bool) (map[string]AgentSourceRef, error) {
	if p.ID == "" || actor.TenantID == 0 || p.Status != "active" || card.ProjectID != p.ID || len(card.AcademicAssociationRefs) == 0 || len(snapshots) == 0 || len(snapshots) > 20 {
		return nil, ErrEvidenceIncomplete
	}
	current, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, p.ID, AuthorizationRead, ""))
	if err != nil || current.Status != "active" || current.ProjectVersion != p.ProjectVersion || current.CurrentContextRevision != p.CurrentContextRevision || current.SpecRevision != p.SpecRevision {
		return nil, ErrEvidenceIncomplete
	}
	basisCurrent := versionBasisCurrent(p, card.Basis)
	if historicalAdoption {
		basisCurrent = card.Basis.ProjectVersion+1 == p.ProjectVersion && card.Basis.SpecRevision == p.SpecRevision && card.Basis.ContextRevision == p.CurrentContextRevision
	}
	if card.ClaimRef == nil || !basisCurrent {
		return nil, ErrEvidenceIncomplete
	}
	claim, _, err := loadStoredObject[ResearchClaim](tx, p.ID, ResearchClaimKind, card.ClaimRef.ID)
	if err != nil || claim.Version != card.ClaimRef.Version || claim.Status == "superseded" || claim.Basis.SpecRevision != p.SpecRevision {
		return nil, ErrEvidenceIncomplete
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return nil, ErrDependencyUnavailable
	}
	catalog, err := currentAcademicSourceCatalog(tx, p.ID)
	if err != nil {
		return nil, err
	}
	gate, ok := s.agentRuntime.Research.(ResearchExecutionGate)
	if !ok {
		return nil, ErrAcademicGlobalCatalogUnavailable
	}
	refs := make(map[string]VersionRef, len(card.AcademicAssociationRefs))
	for _, ref := range card.AcademicAssociationRefs {
		if ref.ID == "" || ref.Version < 1 || ref.Kind != "" && ref.Kind != "AcademicSourceAssociation" {
			return nil, ErrEvidenceIncomplete
		}
		if _, exists := refs[ref.ID]; exists {
			return nil, ErrEvidenceIncomplete
		}
		refs[ref.ID] = ref
	}
	anchors := make(map[string]SourceAnchor, len(card.AnchorRefs))
	for _, ref := range card.AnchorRefs {
		if ref.ID == "" || ref.Version < 1 || ref.Kind != "" && ref.Kind != "SourceAnchor" {
			return nil, ErrEvidenceIncomplete
		}
		if _, exists := anchors[ref.ID]; exists {
			return nil, ErrEvidenceIncomplete
		}
		anchor, _, e := loadStoredObject[SourceAnchor](tx, p.ID, SourceAnchorKind, ref.ID)
		if e != nil || anchor.Version != ref.Version || anchor.Status != "CURRENT" {
			return nil, ErrEvidenceIncomplete
		}
		anchors[ref.ID] = anchor
	}
	out := make(map[string]AgentSourceRef, len(snapshots))
	seenAnchors := map[string]bool{}
	seenAssociations := map[string]bool{}
	for _, snapshot := range snapshots {
		if snapshot.SourceID == "" || snapshot.KnowledgeID == "" || snapshot.AssetID == "" || snapshot.AssetRevision < 1 || snapshot.Locator == "" || len(snapshot.AnchorRefs) == 0 || len(snapshot.Associations) == 0 || !slices.Contains(card.SourceIDs, snapshot.SourceID) {
			return nil, ErrEvidenceIncomplete
		}
		if _, exists := out[snapshot.SourceID]; exists {
			return nil, ErrEvidenceIncomplete
		}
		if err = nativeSourceScope(ctx, tx, actor, p, []string{snapshot.SourceID}, forModel); err != nil {
			return nil, err
		}
		var asset evidence.ProjectAsset
		if err = db.Where("id = ? AND project_id = ? AND tenant_id = ? AND deleted_at IS NULL", snapshot.AssetID, p.ID, actor.TenantID).Take(&asset).Error; err != nil || asset.KnowledgeID != snapshot.KnowledgeID || asset.AssetRevision != int64(snapshot.AssetRevision) || asset.ProcessingState != "ready" {
			return nil, ErrEvidenceIncomplete
		}
		policy, _, _, e := ReadProjectAssetPolicy(tx, p, asset.ID)
		if e != nil || forModel && !policy.ModelUseAllowed {
			return nil, ErrSourceUnavailable
		}
		var revision evidence.ProjectAssetRevision
		if err = db.Where("asset_id = ? AND revision_no = ? AND status = ?", asset.ID, asset.AssetRevision, evidence.RevisionActive).Take(&revision).Error; err != nil || revision.KnowledgeID != snapshot.KnowledgeID {
			return nil, ErrEvidenceIncomplete
		}
		var metadata struct {
			OriginalContentSHA256 string `json:"original_content_sha256"`
		}
		if json.Unmarshal([]byte(revision.Metadata), &metadata) != nil || metadata.OriginalContentSHA256 == "" || metadata.OriginalContentSHA256 != snapshot.RawRef.SHA256 {
			return nil, ErrEvidenceIncomplete
		}
		var knowledge types.Knowledge
		if err = db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", snapshot.KnowledgeID, actor.TenantID).Take(&knowledge).Error; err != nil || knowledge.Type != "file" || knowledge.ParseStatus != types.ParseStatusCompleted || knowledge.KnowledgeBaseID != asset.KnowledgeBaseID {
			return nil, ErrEvidenceIncomplete
		}
		var chunk types.Chunk
		if err = db.Where("id = ? AND tenant_id = ? AND knowledge_id = ? AND deleted_at IS NULL AND is_enabled = ?", snapshot.SourceID, actor.TenantID, snapshot.KnowledgeID, true).Take(&chunk).Error; err != nil || chunk.ChunkType != types.ChunkTypeText && chunk.ChunkType != types.ChunkTypeParentText || chunk.KnowledgeBaseID != asset.KnowledgeBaseID || chunk.ContentRevision != 0 || chunk.SourceContent != "" && chunk.SourceContent != chunk.Content {
			return nil, ErrEvidenceIncomplete
		}
		var origin types.ChunkOriginSHA256
		if err = db.Where("chunk_id = ? AND tenant_id = ? AND knowledge_id = ?", chunk.ID, actor.TenantID, snapshot.KnowledgeID).Take(&origin).Error; err != nil || origin.SHA256 != snapshot.RawRef.SHA256 {
			return nil, ErrEvidenceIncomplete
		}
		raw, e := ReadAcademicOriginalSnapshot(tx, actor.TenantID, p.ID, snapshot.RawRef)
		if e != nil {
			return nil, ErrEvidenceIncomplete
		}
		textBytes, e := ReadAcademicOriginalSnapshot(tx, actor.TenantID, p.ID, snapshot.TextRef)
		if e != nil || !utf8.Valid(textBytes) || string(textBytes) != chunk.Content {
			return nil, ErrEvidenceIncomplete
		}
		text := string(textBytes)
		uploadHash := md5.Sum(raw)
		if !strings.EqualFold(knowledge.FileHash, hex.EncodeToString(uploadHash[:])) {
			return nil, ErrEvidenceIncomplete
		}
		switch strings.ToLower(strings.TrimPrefix(knowledge.FileType, ".")) {
		case "pdf":
			var page types.ChunkPDFPage
			if len(raw) < 5 || string(raw[:5]) != "%PDF-" || snapshot.Page < 1 || db.Where("chunk_id = ? AND tenant_id = ? AND knowledge_id = ?", chunk.ID, actor.TenantID, snapshot.KnowledgeID).Take(&page).Error != nil || page.Page != snapshot.Page || page.TextSHA256 != snapshot.TextRef.SHA256 {
				return nil, ErrEvidenceIncomplete
			}
		case "txt", "text", "md", "markdown":
			if snapshot.Page != 0 || !utf8.Valid(raw) || !evidence.VerbatimAt(chunker.NormalizeLineEndings(string(raw)), chunk.StartAt, chunk.EndAt, text) {
				return nil, ErrEvidenceIncomplete
			}
		default:
			return nil, ErrEvidenceIncomplete
		}
		for _, ref := range snapshot.AnchorRefs {
			anchor, exists := anchors[ref.ID]
			if !exists || seenAnchors[ref.ID] || ref.Version != anchor.Version || ref.Kind != "" && ref.Kind != "SourceAnchor" || anchor.SourceID != snapshot.SourceID || anchor.AssetID != snapshot.AssetID || anchor.AssetRevision != snapshot.AssetRevision || anchor.Locator != snapshot.Locator || anchor.Page != snapshot.Page || !anchorMatchesWholeSource(anchor, text) {
				return nil, ErrEvidenceIncomplete
			}
			runes := []rune(text)
			if anchor.SelectionStart < 0 || anchor.SelectionEnd <= anchor.SelectionStart || anchor.SelectionEnd > len(runes) || sha256Hex(string(runes[anchor.SelectionStart:anchor.SelectionEnd])) != anchor.QuotedTextHash || anchor.ExtractionType != "native_text" && !anchor.HumanVerified {
				return nil, ErrEvidenceIncomplete
			}
			seenAnchors[ref.ID] = true
		}
		for _, binding := range snapshot.Associations {
			ref, exists := refs[binding.AssociationRef.ID]
			if !exists || seenAssociations[ref.ID] || binding.AssociationRef.Version != ref.Version || binding.AssociationRef.Kind != "" && binding.AssociationRef.Kind != "AcademicSourceAssociation" || binding.CandidateRef.ID == "" || binding.CandidateRef.Version < 1 || binding.CandidateRef.Kind != "" && binding.CandidateRef.Kind != "ImportCandidate" {
				return nil, ErrEvidenceIncomplete
			}
			association, _, e := loadStoredObject[AcademicSourceAssociation](tx, p.ID, AcademicSourceAssociationKind, ref.ID)
			if e != nil || association.Version != ref.Version || association.Status != "recorded" || association.SourceID != snapshot.SourceID || association.AssetID != snapshot.AssetID || association.AssetRevision != snapshot.AssetRevision || association.Locator != snapshot.Locator || association.QuotedTextHash != snapshot.TextRef.SHA256 || association.DOI != binding.DOI || association.CandidateRef.ID != binding.CandidateRef.ID || association.CandidateRef.Version != binding.CandidateRef.Version || association.CatalogVersion != binding.CatalogVersion || association.PolicySnapshotVersion != binding.PolicySnapshotVersion || association.CatalogVersion != catalog.SourceCatalogVersion {
				return nil, ErrEvidenceIncomplete
			}
			candidate, _, e := loadStoredObject[ImportCandidate](tx, p.ID, ImportCandidateKind, binding.CandidateRef.ID)
			if e != nil || candidate.Version != binding.CandidateRef.Version || candidate.Status != "imported" || candidate.SelectionType != "academic_metadata" || candidate.SaveMode != "metadata_only" || candidate.AcquisitionStatus != "metadata_only" || candidate.DOI != binding.DOI || candidate.CatalogVersion != binding.CatalogVersion || candidate.PolicySnapshotVersion != binding.PolicySnapshotVersion || !academicCandidatePolicyEligible(candidate.PolicyStatus, candidate.RetractionStatus, candidate.DuplicateStatus) {
				return nil, ErrEvidenceIncomplete
			}
			if known, e := knownAcademicRetraction(tx, p.ID, binding.DOI); e != nil || known {
				return nil, ErrEvidenceIncomplete
			}
			if e := gate.ValidateAcademicResearchResult(ctx, actor, p.ID, ResearchResult{Provider: AcademicCrossrefResultProvider, PolicySnapshotVersion: binding.PolicySnapshotVersion}, catalog); e != nil {
				return nil, e
			}
			seenAssociations[ref.ID] = true
		}
		out[snapshot.SourceID] = AgentSourceRef{SourceID: snapshot.SourceID, AssetID: snapshot.AssetID, AssetRevision: snapshot.AssetRevision, QuotedTextHash: snapshot.TextRef.SHA256, Locator: snapshot.Locator, QuotedText: text, ModelUseAllowed: policy.ModelUseAllowed}
	}
	if len(seenAssociations) != len(refs) {
		return nil, ErrEvidenceIncomplete
	}
	for id, anchor := range anchors {
		if _, associated := out[anchor.SourceID]; associated && !seenAnchors[id] {
			return nil, ErrEvidenceIncomplete
		}
	}
	return out, nil
}
