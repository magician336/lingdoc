package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestConvertBindsOnlyBuiltinDirectTextToActualInput(t *testing.T) {
	const text = "原文第一行\r\n第二行"
	files := &webPageMemoryFiles{objects: map[string]string{"source.txt": text, "source.csv": "a,b\n1,2"}}
	svc := &knowledgeService{fileSvc: files, tenantService: &stubTenantServiceForModelDelete{}}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 1}
	knowledge := &types.Knowledge{ID: "doc", TenantID: 1, Type: "file"}
	convert := func(path, ext string) *types.ReadResult {
		t.Helper()
		payload := types.DocumentProcessPayload{FilePath: path, FileName: path, FileType: ext}
		result, err := svc.convert(context.Background(), payload, kb, knowledge, types.EffectiveProcessConfig{}, true)
		require.NoError(t, err)
		require.NotNil(t, result)
		return result
	}
	result := convert("source.txt", "txt")
	sum := sha256.Sum256([]byte(text))
	require.Equal(t, hex.EncodeToString(sum[:]), result.OriginInputSHA256)
	require.Equal(t, text, result.MarkdownContent)
	require.Empty(t, convert("source.csv", "csv").OriginInputSHA256, "transformed formats are not verbatim original")

	// Carry the actual converter output through the application chunk stage.
	chunkRepo := &embeddingDecoupledChunkService{}
	retrieveEngine := &keywordOnlyRetrieveEngine{}
	knowledge.KnowledgeBaseID = "kb"
	knowledge.ParseStatus = types.ParseStatusProcessing
	chunkSvc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkRepo,
		modelService:   &unavailableEmbeddingModelService{},
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTestTenant())
	chunkSvc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks(), ProcessChunksOptions{OriginInputSHA256: result.OriginInputSHA256})
	require.NotEmpty(t, chunkRepo.created)
	for _, chunk := range chunkRepo.created {
		require.Equal(t, result.OriginInputSHA256, chunk.OriginInputSHA256)
	}

	// The actual repository persists the parser identity with every chunk.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000061_chunk_origin_sha256.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	chunkSvc.chunkRepo = repository.NewChunkRepository(db)
	chunkSvc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks(), ProcessChunksOptions{OriginInputSHA256: result.OriginInputSHA256})
	var chunks []types.Chunk
	var proofs []types.ChunkOriginSHA256
	require.NoError(t, db.Find(&chunks).Error)
	require.NoError(t, db.Find(&proofs).Error)
	require.Len(t, chunks, 2)
	require.Len(t, proofs, len(chunks))
	for _, proof := range proofs {
		require.Equal(t, result.OriginInputSHA256, proof.SHA256)
		require.Equal(t, knowledge.ID, proof.KnowledgeID)
	}
}
