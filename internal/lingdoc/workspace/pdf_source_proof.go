package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// pdfSourcePage checks one original snapshot against the parser-input digest
// and the server-owned page proof written with the chunk. It never invents
// coordinates, uses OCR text, or upgrades a legacy chunk without proof.
func (r *ResearchAdapter) pdfSourcePage(ctx context.Context, asset evidence.Asset, chunk types.Chunk, source evidence.Source) (int, bool, error) {
	reader, ok := r.originReader().(evidence.PDFOriginReader)
	if !ok || source.Status != evidence.SourceAvailable || source.QuotedText == "" || source.QuotedText != chunk.Content || asset.OriginalContentSHA256 == "" {
		return 0, false, nil
	}
	raw, ok, err := reader.PDFOriginBytes(ctx, chunk.KnowledgeID)
	if err != nil || !ok {
		return 0, false, err
	}
	return r.verifyPDFSourcePage(ctx, asset, chunk, source, raw)
}

// verifyPDFSourcePage binds the supplied exact bytes to persisted parse proof.
// Supplying bytes rather than rereading storage keeps snapshots consistent.
func (r *ResearchAdapter) verifyPDFSourcePage(ctx context.Context, asset evidence.Asset, chunk types.Chunk, source evidence.Source, raw []byte) (int, bool, error) {
	if source.Status != evidence.SourceAvailable || source.QuotedText == "" || source.QuotedText != chunk.Content || asset.OriginalContentSHA256 == "" || len(raw) == 0 || len(raw) > evidence.MaxOriginBytes {
		return 0, false, nil
	}
	digest := sha256.Sum256(raw)
	inputHash := hex.EncodeToString(digest[:])
	if inputHash != asset.OriginalContentSHA256 || !r.db.Migrator().HasTable(&types.ChunkOriginSHA256{}) || !r.db.Migrator().HasTable(&types.ChunkPDFPage{}) {
		return 0, false, nil
	}
	query := "chunk_id = ? AND tenant_id = ? AND knowledge_id = ?"
	var origin types.ChunkOriginSHA256
	err := r.db.WithContext(ctx).Where(query, chunk.ID, chunk.TenantID, chunk.KnowledgeID).First(&origin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if origin.SHA256 != inputHash {
		return 0, false, nil
	}
	var page types.ChunkPDFPage
	err = r.db.WithContext(ctx).Where(query, chunk.ID, chunk.TenantID, chunk.KnowledgeID).First(&page).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	textHash := sha256.Sum256([]byte(source.QuotedText))
	if page.Page < 1 || page.TextSHA256 != hex.EncodeToString(textHash[:]) {
		return 0, false, nil
	}
	return page.Page, true, nil
}
