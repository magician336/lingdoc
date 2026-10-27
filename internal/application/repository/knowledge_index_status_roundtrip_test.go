package repository

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// applyFullSQLiteChain 把 migrations/sqlite 的全部 up 迁移按序应用到空库：
// 夹具的库形态完全来自生产迁移链本身（000000 基线 + 后续 ALTER/回填，含
// 000057 的 index_status），不得手抄生产表结构
// （scripts/ci/test_lingdoc_fixture_schema_source.py 守护的形状）。
func applyFullSQLiteChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "migrations", "sqlite")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "read migration %s", name)
		require.NoError(t, db.Exec(string(body)).Error, "apply migration %s", name)
	}
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
	applyFullSQLiteChain(t, db)
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
