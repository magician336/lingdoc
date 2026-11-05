package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type transactionOriginBytes struct{ db *gorm.DB }

func (r transactionOriginBytes) OriginBytes(ctx context.Context, id string) ([]byte, bool, error) {
	var row struct{ Content string }
	err := r.db.WithContext(ctx).Table("origin_file_bytes").Where("id = ?", id).First(&row).Error
	return []byte(row.Content), err == nil, err
}

func TestSourceRuntimeCloneKeepsTransactionOriginReader(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "origin.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeBase{}))
	require.NoError(t, db.Exec("CREATE TABLE origin_file_bytes (id TEXT PRIMARY KEY, content TEXT)").Error)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb", TenantID: 1}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "source", TenantID: 1, KnowledgeBaseID: "kb", FilePath: "resource://source", FileType: "txt"}).Error)
	runtime := NewGORMSourceRuntime(db, nil, nil, nil, nil)
	runtime.ConnectOriginFiles(func(db *gorm.DB) OriginFileReader { return transactionOriginBytes{db: db} })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("INSERT INTO origin_file_bytes VALUES (?,?)", "source", "transaction-local original").Error)
		clone := runtime.CloneWithDatabase(tx)
		text, available, err := clone.origins().OriginText(ctx, "source")
		require.NoError(t, err)
		require.True(t, available)
		require.Equal(t, "transaction-local original", text)
		return nil
	}))
}
