package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// indexStatusRoundTripMigrations 是本用例的库形态来源：基线 000000 建出
// knowledges/chunks 的生产形状，000057 加 index_status 列并按 chunk 证据回填
// ——夹具不得再手抄生产表结构（scripts/ci/test_lingdoc_fixture_schema_source.py）。
var indexStatusRoundTripMigrations = []string{
	"000000_init.up.sql",
	"000057_knowledge_index_status.up.sql",
}

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
	for _, name := range indexStatusRoundTripMigrations {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		require.NoError(t, err, "read migration %s", name)
		require.NoError(t, db.Exec(string(body)).Error, "apply migration %s", name)
	}
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
