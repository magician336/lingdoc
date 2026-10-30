package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

var _ core.AcademicSnapshotMaterialReader = (*ResearchAdapter)(nil)

// ReadAcademicSnapshotMaterial returns bounded uploaded-file bytes and the
// exact native chunk text after proving both against ingestion and the current
// asset. No model permission is granted or inferred by capturing the material.
func (r *ResearchAdapter) ReadAcademicSnapshotMaterial(ctx context.Context, actor core.Actor, projectID, chapterID, sourceID string) (core.AcademicSnapshotMaterial, error) {
	var empty core.AcademicSnapshotMaterial
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(projectID) != projectID || strings.TrimSpace(sourceID) != sourceID || strings.TrimSpace(chapterID) != chapterID {
		return empty, core.ErrInvalidRequest
	}
	if r.db == nil || r.sources == nil || r.projects == nil {
		return empty, core.ErrDependencyUnavailable
	}
	ctx = researchCallerContext(ctx, actor)
	first, err := r.readResearchSource(ctx, actor, projectID, chapterID, sourceID)
	if err != nil {
		return empty, err
	}
	if !first.StableOriginal || first.Status != string(evidence.SourceAvailable) || first.ProcessingState != "ready" || first.QuotedText == "" || researchTextHash(first.QuotedText) != first.QuotedTextHash {
		return empty, core.ErrEvidenceIncomplete
	}
	assets, err := r.sources.ListAssets(ctx, actor, projectID)
	if err != nil {
		return empty, err
	}
	var asset evidence.Asset
	for _, item := range assets {
		if item.ID == first.AssetID && item.KnowledgeID == first.KnowledgeID && item.AssetRevision == first.AssetRevision {
			asset = item
			break
		}
	}
	if asset.ID == "" || asset.OriginalContentSHA256 == "" {
		return empty, core.ErrEvidenceIncomplete
	}
	if err = r.authorizeAssetScope(ctx, actor, projectID, chapterID, []string{asset.ID}); err != nil {
		return empty, err
	}
	var chunk types.Chunk
	if err = r.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND knowledge_id = ?", sourceID, actor.TenantID, asset.KnowledgeID).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, core.ErrSourceUnavailable
		}
		return empty, err
	}
	if !expandableChunkType(chunk.ChunkType) {
		return empty, core.ErrEvidenceIncomplete
	}
	var knowledge types.Knowledge
	if err = r.db.WithContext(ctx).Select("id", "type", "file_type").Where("id = ? AND tenant_id = ?", asset.KnowledgeID, actor.TenantID).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, core.ErrSourceUnavailable
		}
		return empty, err
	}
	if knowledge.Type != "file" || knowledge.IsManual() {
		return empty, core.ErrEvidenceIncomplete
	}
	source, err := r.sources.GetSource(ctx, actor, projectID, sourceID)
	if err != nil {
		return empty, err
	}
	if source.ID != first.SourceID || source.AssetID != first.AssetID || source.AssetRevision != first.AssetRevision || source.Status != evidence.SourceAvailable || source.QuotedText != first.QuotedText || source.QuotedTextHash != first.QuotedTextHash {
		return empty, core.ErrEvidenceIncomplete
	}
	origin := r.originReader()
	var raw []byte
	if first.VerifiedPDFPage > 0 {
		reader, ok := origin.(evidence.PDFOriginReader)
		if !ok {
			return empty, core.ErrEvidenceIncomplete
		}
		var present bool
		raw, present, err = reader.PDFOriginBytes(ctx, asset.KnowledgeID)
		if err != nil {
			return empty, err
		}
		if !present || len(raw) == 0 || len(raw) > evidence.MaxOriginBytes {
			return empty, core.ErrEvidenceIncomplete
		}
		page, proven, proofErr := r.verifyPDFSourcePage(ctx, asset, chunk, source, raw)
		if proofErr != nil {
			return empty, proofErr
		}
		if !proven || page != first.VerifiedPDFPage {
			return empty, core.ErrEvidenceIncomplete
		}
	} else {
		reader, ok := origin.(evidence.OriginSnapshotReader)
		if !ok {
			return empty, core.ErrEvidenceIncomplete
		}
		var original string
		var present bool
		original, raw, present, err = reader.OriginSnapshot(ctx, asset.KnowledgeID)
		if err != nil {
			return empty, err
		}
		if !present || len(raw) == 0 || len(raw) > evidence.MaxOriginBytes || len(original) > evidence.MaxOriginBytes || !evidence.VerbatimAt(original, chunk.StartAt, chunk.EndAt, source.QuotedText) {
			return empty, core.ErrEvidenceIncomplete
		}
	}
	digest := sha256.Sum256(raw)
	hexDigest := hex.EncodeToString(digest[:])
	if hexDigest != asset.OriginalContentSHA256 || !r.db.Migrator().HasTable(&types.ChunkOriginSHA256{}) {
		return empty, core.ErrEvidenceIncomplete
	}
	var proof types.ChunkOriginSHA256
	if err = r.db.WithContext(ctx).Where("chunk_id = ? AND tenant_id = ? AND knowledge_id = ?", chunk.ID, actor.TenantID, asset.KnowledgeID).First(&proof).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, core.ErrEvidenceIncomplete
		}
		return empty, err
	}
	if proof.SHA256 != hexDigest {
		return empty, core.ErrEvidenceIncomplete
	}
	// Reauthorize and re-read at the end so a source/permission change during
	// storage IO cannot produce a successfully returned mixed snapshot.
	last, err := r.readResearchSource(ctx, actor, projectID, chapterID, sourceID)
	if err != nil {
		return empty, err
	}
	if !last.StableOriginal || last.Status != string(evidence.SourceAvailable) || last.ProcessingState != "ready" || last.SourceID != first.SourceID || last.AssetID != first.AssetID || last.AssetRevision != first.AssetRevision || last.KnowledgeID != first.KnowledgeID || last.Locator != first.Locator || last.QuotedText != source.QuotedText || last.QuotedTextHash != source.QuotedTextHash || last.VerifiedPDFPage != first.VerifiedPDFPage || last.ModelUseAllowed != first.ModelUseAllowed {
		return empty, core.ErrEvidenceIncomplete
	}
	assets, err = r.sources.ListAssets(ctx, actor, projectID)
	if err != nil {
		return empty, err
	}
	matched := false
	for _, current := range assets {
		if current.ID == asset.ID && current.KnowledgeID == asset.KnowledgeID && current.AssetRevision == asset.AssetRevision && current.OriginalContentSHA256 == hexDigest {
			matched = true
			break
		}
	}
	if !matched {
		return empty, core.ErrEvidenceIncomplete
	}
	return core.AcademicSnapshotMaterial{Source: last, Raw: append([]byte(nil), raw...), SourceText: source.QuotedText, RawSHA256: hexDigest}, nil
}
