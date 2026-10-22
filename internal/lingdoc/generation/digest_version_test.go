package generation

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestGenerationIdempotencyDigestVersionDecidesReplayConflictAndExpiry 覆盖
// 生成侧的三种幂等键语义。
//
// 三条判据共用同一个比较函数：版本相同且摘要相同 ⇒ 重放；版本相同但摘要不同 ⇒
// 冲突；版本不同 ⇒ 键已过期（调用方换一个新键重试，而不是去找一份根本没变过
// 的请求）。
func TestGenerationIdempotencyDigestVersionDecidesReplayConflictAndExpiry(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	input := Input{Actor: actor, Request: Request{ChapterID: "chapter-1"},
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1"}}
	first, err := repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-1", "hash-1", input)
	require.NoError(t, err)

	// 同样的键、同样的摘要 ⇒ 重放。
	replayed, err := repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-1", "hash-1", input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.ID, replayed.ID)

	// 同样的键、不同的摘要 ⇒ 冲突。
	_, err = repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-1", "hash-2", input)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	require.NotErrorIs(t, err, ErrIdempotencyKeyExpired)

	// 这一行是旧规则写的 ⇒ 键已过期。过期先于冲突判定：调用方要做的下一步是
	// 换一个新键，不是去找一份根本没变过的请求。
	require.NoError(t, db.Model(&generationRow{}).Where("id = ?", first.ID).
		Update("digest_version", generationDigestVersion-1).Error)
	_, err = repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-1", "hash-1", input)
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired)
	require.NotErrorIs(t, err, ErrIdempotencyConflict)

	// 回放路径说同一件事。
	_, found, err := repo.FindReplay(ctx, actor, "project-1", "digest-key-1", "hash-1")
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired)
	require.False(t, found)
}

// TestGenerationDigestComparisonPointsShareOneRule 断言三处摘要比对点共用同一
// 条规则。
//
// 回放路径与写事务里的两处必须给出同一个答案：同一个键要么三处都重放，要么
// 三处都拒。规则写在 compareGenerationDigest 里，别处不再自己比摘要。
func TestGenerationDigestComparisonPointsShareOneRule(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	input := Input{Actor: actor, Request: Request{ChapterID: "chapter-1"},
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1"}}
	created, err := repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-2", "hash-1", input)
	require.NoError(t, err)
	require.NoError(t, db.Model(&generationRow{}).Where("id = ?", created.ID).
		Update("digest_version", 0).Error)

	// 回放路径。
	_, found, err := repo.FindReplay(ctx, actor, "project-1", "digest-key-2", "hash-1")
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired)
	require.False(t, found)

	// 写事务里的两处：先查已有行的那一处，与插入冲突后回查的那一处。
	_, err = repo.CreateOrReplay(ctx, actor, "project-1", "digest-key-2", "hash-1", input)
	require.ErrorIs(t, err, ErrIdempotencyKeyExpired)
}
