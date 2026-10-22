package candidateadoption

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// concurrencyStore 造一个 PostgreSQL 上的候选采纳存储。
//
// 并发语义只能在真并发上验：SQLite 的写锁让两个写事务排队，顺序执行不是
// 并发，拿它当并发证据是假的。所以这组用例只在隔离的 PostgreSQL 上跑，没
// 配 DSN 就明确跳过——跳过不是通过。
func concurrencyStore(t *testing.T) (*SQLiteCandidateAdoptionStore, *gorm.DB) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	rootSQL, err := root.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rootSQL.Close() })
	schema := "pf2355_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, root.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("清理隔离 schema 失败：%v", err)
		}
	})
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 真并发需要两个写事务同时在飞：连接池不能只留一条连接。
	sqlDB.SetMaxOpenConns(8)
	store := NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, store.AutoMigrate(context.Background()))
	require.NoError(t, db.AutoMigrate(&adoptionWorkingCopyRow{}))
	return store, db
}

// seedConcurrencyWorkspace 造一个项目、章节与工作副本。
func seedConcurrencyWorkspace(t *testing.T, store *SQLiteCandidateAdoptionStore, db *gorm.DB, chapterID string) {
	t.Helper()
	ctx := context.Background()
	projectID := "project-1"
	require.NoError(t, store.UpsertProject(ctx, projectID, 0))
	require.NoError(t, store.UpsertChapter(ctx, Chapter{ID: chapterID, ProjectID: projectID, SectionID: "question", Title: "问题"}, 1))
	require.NoError(t, db.Create(&adoptionWorkingCopyRow{
		ProjectID: projectID, ChapterID: chapterID, SpecRevision: 1, WorkingCopyRevision: 1,
		BodyMarkdown: "", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error)
}

// seedConcurrencyCandidate 造一个候选。
func seedConcurrencyCandidate(t *testing.T, store *SQLiteCandidateAdoptionStore, chapterID, candidateID, body string, sources []string) {
	t.Helper()
	require.NoError(t, store.UpsertCandidate(context.Background(), Candidate{
		ID: candidateID, ProjectID: "project-1", ChapterID: chapterID, RunID: "run-" + candidateID,
		BodyMarkdown: body, SourceIDs: sources,
		Basis: Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"}, Validity: ValidityFresh,
		WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}))
}

// adoptionInput 造一份采纳请求。
func adoptionInput(chapterID, candidateID, actor, key string) AcceptCandidateInput {
	return AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: chapterID, CandidateID: candidateID, ActorID: actor,
		IdempotencyKey: key, ExpectedSpecRevision: 1, ReplaceExisting: true,
		// 夹具里的工作副本是修订号 1、正文为空。
		ExpectedWorkingCopyRevision: 1,
		ExpectedBodyHash:            workingCopyBodyHash(""),
		Confirmation:                fixtureConfirmation(actor),
	}
}

// TestConcurrentAdoptionOfTheSameCandidateCreatesOneVersion 覆盖 A40：双人
// 同时采纳同一候选，只建一个版本。
//
// 第二个拿到的是版本冲突，不是第二个正式版本——正式版本链不能分叉。
func TestConcurrentAdoptionOfTheSameCandidateCreatesOneVersion(t *testing.T) {
	store, db := concurrencyStore(t)
	seedConcurrencyWorkspace(t, store, db, "chapter-1")
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-1", "正文 [[source:source-1]]", []string{"source-1"})
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})

	inputs := []AcceptCandidateInput{
		adoptionInput("chapter-1", "candidate-1", "user-1", "concurrent-key-0001"),
		adoptionInput("chapter-1", "candidate-1", "user-2", "concurrent-key-0002"),
	}
	results := make([]error, len(inputs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range inputs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, results[index] = service.AcceptCandidate(context.Background(), inputs[index])
		}(index)
	}
	close(start)
	wg.Wait()

	succeeded, conflicted := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("并发采纳得到意外错误：%v", err)
		}
	}
	require.Equal(t, 1, succeeded, "只允许一个并发采纳成功")
	require.Equal(t, 1, conflicted, "另一个必须拿到版本冲突")

	var versionCount int64
	require.NoError(t, db.Model(&chapterVersionRow{}).Where("chapter_id = ?", "chapter-1").Count(&versionCount).Error)
	require.Equal(t, int64(1), versionCount, "并发采纳不得建出第二个正式版本")

	var adoptionCount int64
	require.NoError(t, db.Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Equal(t, int64(1), adoptionCount, "失败的那一次采纳事务回滚，不留下幂等行")
}

// TestConcurrentAdoptionLeavesTheWorkingCopyUnwritten 覆盖 A41：工作副本
// 并发写，一个成功一个版本冲突，工作副本不出现半写。
//
// 半写的意思是工作副本正文变成了两段拼接——那比写不进去更糟。
func TestConcurrentAdoptionLeavesTheWorkingCopyUnwritten(t *testing.T) {
	store, db := concurrencyStore(t)
	seedConcurrencyWorkspace(t, store, db, "chapter-1")
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-1", "第一份正文 [[source:source-1]]", []string{"source-1"})
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-2", "第二份正文 [[source:source-2]]", []string{"source-2"})
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})

	inputs := []AcceptCandidateInput{
		adoptionInput("chapter-1", "candidate-1", "user-1", "concurrent-copy-key-1"),
		adoptionInput("chapter-1", "candidate-2", "user-2", "concurrent-copy-key-2"),
	}
	results := make([]error, len(inputs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range inputs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, results[index] = service.AcceptCandidate(context.Background(), inputs[index])
		}(index)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrVersionConflict, "并发写工作副本的失败方必须是版本冲突")
	}
	require.Equal(t, 1, succeeded, "只允许一个并发写成功")

	var working adoptionWorkingCopyRow
	require.NoError(t, db.Where("project_id = ? AND chapter_id = ?", "project-1", "chapter-1").First(&working).Error)
	require.Contains(t, []string{"第一份正文 [[source:source-1]]", "第二份正文 [[source:source-2]]"}, working.BodyMarkdown,
		"工作副本正文必须是完整的一份，不能是两段拼接")
	require.Equal(t, int64(2), working.WorkingCopyRevision, "工作副本修订号只推进一次")
}

// TestAdoptionWithRevokedAuthorizationInsideTheTransactionWritesNothing 覆盖
// A42 的前半：采纳事务中途撤权 ⇒ 写前被拒，零写入。
func TestAdoptionWithRevokedAuthorizationInsideTheTransactionWritesNothing(t *testing.T) {
	store, db := concurrencyStore(t)
	seedConcurrencyWorkspace(t, store, db, "chapter-1")
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-1", "正文 [[source:source-1]]", []string{"source-1"})
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
	// 事务内的授权端口在构造之后换掉：构造器会把它接成服务层那个放行的端口。
	store.authorizer = revokedTransactionAuthorizer{}

	_, err := service.AcceptCandidate(context.Background(), adoptionInput("chapter-1", "candidate-1", "user-1", "revoked-in-transaction-1"))
	require.Error(t, err)

	var versionCount int64
	require.NoError(t, db.Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Zero(t, versionCount, "事务内撤权必须零写入")
	var adoptionCount int64
	require.NoError(t, db.Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Zero(t, adoptionCount, "事务内撤权必须零写入")
	var working adoptionWorkingCopyRow
	require.NoError(t, db.Where("project_id = ? AND chapter_id = ?", "project-1", "chapter-1").First(&working).Error)
	require.Equal(t, int64(1), working.WorkingCopyRevision, "事务内撤权不得推进工作副本")
}

// TestAdoptionWithRevokedAuthorizationAfterCommitSaysCommitted 覆盖 A42 的
// 后半：提交后撤权 ⇒ 明确告知「已提交」。
//
// 这一条不是「零写入拒绝」：版本已经建好了。这时候最诚实的回答是「已经
// 提交了，但当前访问复核没过」，而不是假装什么都没发生。
func TestAdoptionWithRevokedAuthorizationAfterCommitSaysCommitted(t *testing.T) {
	store, db := concurrencyStore(t)
	seedConcurrencyWorkspace(t, store, db, "chapter-1")
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-1", "正文 [[source:source-1]]", []string{"source-1"})
	authorizer := &revokedAfterFirstAuthorizer{}
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), authorizer)

	_, err := service.AcceptCandidate(context.Background(), adoptionInput("chapter-1", "candidate-1", "user-1", "revoked-after-commit-1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "committed", "提交后撤权必须明确告知已提交")

	var versionCount int64
	require.NoError(t, db.Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Equal(t, int64(1), versionCount, "提交后撤权时版本已经落地")
}

// TestTheSameIdempotencyKeyAcrossActorsIsTwoOperations 覆盖 A43：同一幂等
// 键跨操作者视为两个独立操作，各自成功。
//
// 幂等键的唯一性带着操作者：它防的是「同一个操作者把一个键用在两个请求
// 上」，不是「两个人不能用同一个键」。
func TestTheSameIdempotencyKeyAcrossActorsIsTwoOperations(t *testing.T) {
	store, db := concurrencyStore(t)
	seedConcurrencyWorkspace(t, store, db, "chapter-1")
	seedConcurrencyCandidate(t, store, "chapter-1", "candidate-1", "第一份正文 [[source:source-1]]", []string{"source-1"})
	seedConcurrencyWorkspace(t, store, db, "chapter-2")
	seedConcurrencyCandidate(t, store, "chapter-2", "candidate-2", "第二份正文 [[source:source-2]]", []string{"source-2"})
	service := NewCandidateAdoptionService(store, sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})

	const sharedKey = "shared-cross-actor-key-1"
	first, err := service.AcceptCandidate(context.Background(), adoptionInput("chapter-1", "candidate-1", "user-1", sharedKey))
	require.NoError(t, err)
	second, err := service.AcceptCandidate(context.Background(), adoptionInput("chapter-2", "candidate-2", "user-2", sharedKey))
	require.NoError(t, err)
	require.NotEqual(t, first.Chapter.CurrentVersionID, second.Chapter.CurrentVersionID)

	var adoptionCount int64
	require.NoError(t, db.Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Equal(t, int64(2), adoptionCount, "跨操作者的同一个键是两个独立操作")
}

// revokedTransactionAuthorizer 在事务内一律拒绝授权。
type revokedTransactionAuthorizer struct{}

func (revokedTransactionAuthorizer) Authorize(context.Context, string, string, string) error {
	return nil
}

func (revokedTransactionAuthorizer) AuthorizeCandidateInTransaction(context.Context, *gorm.DB, string, string, string) error {
	return ErrForbidden
}

// revokedAfterFirstAuthorizer 第一次授权放行，之后一律拒绝。
type revokedAfterFirstAuthorizer struct {
	mu    sync.Mutex
	calls int
}

func (a *revokedAfterFirstAuthorizer) Authorize(context.Context, string, string, string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls == 1 {
		return nil
	}
	return ErrForbidden
}
