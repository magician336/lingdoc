package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// knowledgesIndexStatusDDL mirrors the knowledges columns a full-struct Save
// (UpdateKnowledge) writes, plus the index_status column from migration
// 000136 (sqlite 000057). custom_metadata is absent on purpose: UpdateKnowledge
// omits it when the struct value is nil (same as knowledgesTestDDL).
const knowledgesIndexStatusDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT,
    source VARCHAR(2048) NOT NULL DEFAULT '',
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    folder_path VARCHAR(1024) NOT NULL DEFAULT '',
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path TEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata TEXT,
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result TEXT DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    index_status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    processed_at DATETIME,
    error_message TEXT,
    deleted_at DATETIME
);
`

// The row-level index marker must survive the real repository seam: what the
// parse run stamps on the struct must be what a reader gets back from
// GetKnowledgeByID, and it must ride along WITHOUT touching parse_status —
// the two signals stay independent at the persistence layer too (#599).
func TestKnowledgeIndexStatusRoundTrip(t *testing.T) {
	dsn := "file:indexstatus-" + t.Name() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(knowledgesIndexStatusDDL).Error)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()

	// A row whose parse finished but whose index never got built — the exact
	// #599 shape the backfill in migration 000136 also produces.
	require.NoError(t, db.Exec(`INSERT INTO knowledges
		(id, tenant_id, knowledge_base_id, type, title, source, parse_status,
		 enable_status, channel, index_status)
		VALUES ('k-1', 1, 'kb-1', 'file', 'doc', 'upload', 'completed',
		 'enabled', 'web', 'pending')`).Error)

	fetched, err := repo.GetKnowledgeByID(ctx, 1, "k-1")
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeIndexStatusPending, fetched.IndexStatus,
		"the stored marker must be readable through the repository")

	// The parse run's update: the row reaches its terminal parse state AND
	// carries the index-failure marker in the same Save.
	fetched.ParseStatus = types.ParseStatusCompleted
	fetched.IndexStatus = types.KnowledgeIndexStatusFailed
	require.NoError(t, repo.UpdateKnowledge(ctx, fetched))

	reread, err := repo.GetKnowledgeByID(ctx, 1, "k-1")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusCompleted, reread.ParseStatus,
		"the parse itself still reads as completed")
	require.Equal(t, types.KnowledgeIndexStatusFailed, reread.IndexStatus,
		"the non-fatal index marker must persist through UpdateKnowledge")
}
