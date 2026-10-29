package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestChunkOriginDigestIsCommittedWithChunkOrNeither(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	chunk := makeChunk("kb", "knowledge", types.ChunkTypeText)
	sum := sha256.Sum256([]byte("original bytes"))
	chunk.OriginInputSHA256 = hex.EncodeToString(sum[:])

	// A missing migration must not leave a chunk without its claimed proof.
	require.Error(t, repo.CreateChunks(context.Background(), []*types.Chunk{chunk}))
	var count int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Count(&count).Error)
	require.Zero(t, count)

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000060_chunk_origin_sha256.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	require.NoError(t, repo.CreateChunks(context.Background(), []*types.Chunk{chunk}))
	var proof types.ChunkOriginSHA256
	require.NoError(t, db.Where("chunk_id = ?", chunk.ID).First(&proof).Error)
	require.Equal(t, chunk.TenantID, proof.TenantID)
	require.Equal(t, chunk.KnowledgeID, proof.KnowledgeID)
	require.Equal(t, chunk.OriginInputSHA256, proof.SHA256)

	bad := makeChunk("kb", "knowledge", types.ChunkTypeText)
	bad.OriginInputSHA256 = "not-a-sha256"
	require.Error(t, repo.CreateChunks(context.Background(), []*types.Chunk{bad}))
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", bad.ID).Count(&count).Error)
	require.Zero(t, count)
}

func TestChunkOriginDigestSurvivesSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "origin.sqlite")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000060_chunk_origin_sha256.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	chunk := makeChunk("kb", "knowledge", types.ChunkTypeText)
	sum := sha256.Sum256([]byte("原始资料"))
	chunk.OriginInputSHA256 = hex.EncodeToString(sum[:])
	require.NoError(t, NewChunkRepository(db).CreateChunks(context.Background(), []*types.Chunk{chunk}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlReopened, err := reopened.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlReopened.Close() })
	var proof types.ChunkOriginSHA256
	require.NoError(t, reopened.Where("chunk_id = ?", chunk.ID).First(&proof).Error)
	require.Equal(t, chunk.OriginInputSHA256, proof.SHA256)
	require.Equal(t, chunk.TenantID, proof.TenantID)
	require.Equal(t, chunk.KnowledgeID, proof.KnowledgeID)
}
