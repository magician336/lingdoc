package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func acceptanceNativePDF() []byte {
	stream := "BT /F1 12 Tf 72 720 Td (current synthetic excerpt) Tj ET"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	pdf := "%PDF-1.4\n"
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, len(pdf))
		pdf += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := len(pdf)
	pdf += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		pdf += fmt.Sprintf("%010d 00000 n \n", offset)
	}
	pdf += fmt.Sprintf("trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return []byte(pdf)
}

// Optional local integration: the address must point to an isolated current
// DocReader. The uploaded-file storage and indexing providers are test ports.
func TestLiveNativePDFConvertChunkAndProof(t *testing.T) {
	addr := os.Getenv("LINGDOC_TEST_DOCREADER_ADDR")
	if addr == "" {
		t.Skip("isolated DocReader address not provided")
	}
	reader, err := docparser.NewGRPCDocumentReader(addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	raw := acceptanceNativePDF()
	files := &webPageMemoryFiles{objects: map[string]string{"source.pdf": string(raw)}}
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file", ParseStatus: types.ParseStatusProcessing}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7, IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true}}
	svc := &knowledgeService{fileSvc: files, documentReader: reader, tenantService: &stubTenantServiceForModelDelete{}}
	result, err := svc.convert(context.Background(), types.DocumentProcessPayload{FilePath: "source.pdf", FileName: "source.pdf", FileType: "pdf"}, kb, knowledge, types.EffectiveProcessConfig{}, true)
	require.NoError(t, err)
	require.NotNil(t, result)
	bytesHash := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(bytesHash[:]), result.OriginInputSHA256)
	require.Len(t, result.PDFPageSpans, 1)
	require.Equal(t, 1, result.PDFPageSpans[0].Page)
	require.Equal(t, "current synthetic excerpt", result.MarkdownContent)
	split := chunker.Split(result.MarkdownContent, buildSplitterConfigFromChunking(types.ChunkingConfig{}))
	var parsed []types.ParsedChunk
	for _, c := range split {
		parsed = append(parsed, types.ParsedChunk{Content: c.Content, Seq: c.Seq, Start: c.Start, End: c.End})
	}
	require.NotEmpty(t, parsed)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	for _, name := range []string{"000061_chunk_origin_sha256.up.sql", "000062_chunk_pdf_page.up.sql"} {
		migration, e := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		require.NoError(t, e)
		require.NoError(t, db.Exec(string(migration)).Error)
	}
	svc = &knowledgeService{repo: &parentChildKnowledgeRepo{knowledge: knowledge}, chunkRepo: repository.NewChunkRepository(db), modelService: &unavailableEmbeddingModelService{}, retrieveEngine: parentChildRetrieveRegistry{engine: &keywordOnlyRetrieveEngine{}}, graphEngine: parentChildGraphRepo{}, tenantRepo: parentChildTenantRepo{}, task: parentChildTaskEnqueuer{}}
	tenant := keywordOnlyTestTenant()
	tenant.ID = 7
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	svc.processChunks(ctx, kb, knowledge, parsed, ProcessChunksOptions{OriginInputSHA256: result.OriginInputSHA256, PDFPageSpans: result.PDFPageSpans, PDFIndexText: result.MarkdownContent})
	var origins []types.ChunkOriginSHA256
	var pages []types.ChunkPDFPage
	require.NoError(t, db.Find(&origins).Error)
	require.NoError(t, db.Find(&pages).Error)
	require.NotEmpty(t, origins)
	require.Len(t, pages, 1)
	require.Equal(t, result.OriginInputSHA256, origins[0].SHA256)
	require.Equal(t, 1, pages[0].Page)
	if output := os.Getenv("LINGDOC_TEST_PDF_CAPTURE"); output != "" {
		var chunk types.Chunk
		require.NoError(t, db.Where("id = ?", pages[0].ChunkID).Take(&chunk).Error)
		capture, e := json.Marshal(struct {
			Raw       []byte `json:"raw"`
			ChunkID   string `json:"chunk_id"`
			Text      string `json:"text"`
			OriginSHA string `json:"origin_sha"`
			Page      int    `json:"page"`
			TextSHA   string `json:"text_sha"`
		}{raw, chunk.ID, chunk.Content, origins[0].SHA256, pages[0].Page, pages[0].TextSHA256})
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, capture, 0600))
	}
}
