package container

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTenantSkillReaperRequiresLedgerTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "candidate.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	cleaner := NewResourceCleaner().(*ResourceCleaner)
	svc := service.NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	startTenantSkillReaper(svc, cleaner, db)
	require.Empty(t, cleaner.cleanups, "SQLite without the optional skill ledger must not schedule a failing reaper")
}

func TestTenantSkillReaperStartsWhenLedgerTablesExist(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "candidate.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.Exec("CREATE TABLE tenant_skills (id TEXT PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE tenant_skill_snapshots (id TEXT PRIMARY KEY)").Error)

	cleaner := NewResourceCleaner().(*ResourceCleaner)
	svc := service.NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	startTenantSkillReaper(svc, cleaner, db)
	require.Len(t, cleaner.cleanups, 1, "the reaper must keep its shutdown hook when the schema is ready")
	require.Empty(t, cleaner.Cleanup(context.Background()))
}
