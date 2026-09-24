package evidence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestStore 建一个真实（文件型）SQLite 库并跑 AutoMigrate。
//
// 用文件而不是 :memory:：GORM 会开连接池，:memory: 下每条连接各是一个库，
// 事务里写的行在另一条连接上会「消失」。测试要证明的正是落库后的读写，
// 所以这里不能图快。
func newTestStore(t *testing.T) *Bindings {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "evidence.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	// Windows 上文件被连接池占着就删不掉，temp 目录清理会报 unlinkat 失败。
	// 注册在 t.TempDir 之后：cleanup 是后进先出，这条会先跑。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&ProjectAsset{}, &ProjectAssetRevision{}); err != nil {
		t.Fatalf("建表: %v", err)
	}
	return NewBindings(db)
}

func bindInput(projectID, knowledgeID string, mutate func(*BindInput)) BindInput {
	in := BindInput{
		TenantID:    1,
		ProjectID:   projectID,
		KnowledgeID: knowledgeID,
		Title:       "合成资料 " + knowledgeID,
		CreatedBy:   "u-verifier",
		Signal:      signal(func(s *KnowledgeSignal) { s.KnowledgeID = knowledgeID }),
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

// bindInputKB 在 bindInput 之上带上知识库与租户：判权要用这两个事实，
// 而 actor 的租户是 7，绑定行也取 7，别让两边对不上。
func bindInputKB(projectID, knowledgeID, kbID string, mutate func(*BindInput)) BindInput {
	in := bindInput(projectID, knowledgeID, mutate)
	in.TenantID = 7
	in.KnowledgeBaseID = kbID
	return in
}

func TestBindCreatesRevisionOneAndRepeatsIdempotently(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	first, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if first.AssetRevision != 1 {
		t.Fatalf("首次绑定 revision = %d, want 1", first.AssetRevision)
	}
	if first.ProcessingState != AssetStateReady {
		t.Fatalf("已解析完成的资料 state = %q, want ready", first.ProcessingState)
	}
	if first.ID == "" || first.ProjectID != "p-1" || first.KnowledgeID != "k-1" {
		t.Fatalf("绑定结果字段不全: %+v", first)
	}

	again, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("重复 Bind: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("同一（项目,资料）重复绑定产生了新 asset：%s → %s", first.ID, again.ID)
	}
	if again.AssetRevision != 1 {
		t.Fatalf("重复绑定把 revision 抬到了 %d", again.AssetRevision)
	}

	// 未就绪资料同样允许绑定（F03 第一步返回 201 + processing），
	// 就绪与否在使用时才拦。
	busy, err := store.Bind(ctx, bindInput("p-1", "k-busy", func(in *BindInput) {
		in.Signal = signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-busy"
			s.ParseStatus = types.ParseStatusProcessing
		})
	}))
	if err != nil {
		t.Fatalf("绑定未就绪资料: %v", err)
	}
	if busy.ProcessingState != AssetStateProcessing {
		t.Fatalf("未就绪资料的 state = %q, want processing", busy.ProcessingState)
	}
}

func TestBoundAssetsIsScopedToProject(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.Bind(ctx, bindInput("p-1", "k-1", nil)); err != nil {
		t.Fatalf("Bind p-1: %v", err)
	}
	if _, err := store.Bind(ctx, bindInput("p-2", "k-2", nil)); err != nil {
		t.Fatalf("Bind p-2: %v", err)
	}

	got, err := store.BoundAssets(ctx, "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("p-1 的绑定资料数 = %d, want 1", len(got))
	}
	if got[0].KnowledgeID != "k-1" {
		t.Fatalf("p-1 返回了别的项目的资料: %+v", got[0])
	}

	empty, err := store.BoundAssets(ctx, "p-none")
	if err != nil {
		t.Fatalf("BoundAssets(空项目): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("没有绑定的项目返回了 %d 条", len(empty))
	}
}

func TestObserveBumpsRevisionAndSupersedesPrevious(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	asset, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	same, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) { s.KnowledgeID = "k-1" }))
	if err != nil {
		t.Fatalf("ObserveAsset(同内容): %v", err)
	}
	if same.Changed || same.Next.Revision != 1 {
		t.Fatalf("同内容观测改了版本: %+v", same)
	}

	changed, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-1"
		s.FileHash = "hash-v2"
	}))
	if err != nil {
		t.Fatalf("ObserveAsset(内容变化): %v", err)
	}
	if !changed.Changed || changed.Next.Revision != 2 {
		t.Fatalf("内容变化没有递增版本: %+v", changed)
	}

	// 递增必须落库，而且旧修订要标 superseded——否则「旧定位是否还有效」
	// 这个问题在数据上无从回答。
	after, err := store.BoundAssets(ctx, "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(after) != 1 || after[0].AssetRevision != 2 {
		t.Fatalf("落库后的 revision = %+v, want 2", after)
	}
	revisions, err := store.Revisions(ctx, asset.ID)
	if err != nil {
		t.Fatalf("revisionsFor: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("修订行数 = %d, want 2", len(revisions))
	}
	if revisions[0].Status != RevisionSuperseded || revisions[0].RevisionNo != 1 {
		t.Fatalf("旧修订未被标 superseded: %+v", revisions[0])
	}
	if revisions[1].Status != RevisionActive || revisions[1].RevisionNo != 2 {
		t.Fatalf("新修订不是 active: %+v", revisions[1])
	}
	if revisions[1].ContentHash == "" {
		t.Error("新修订没有记下当次指纹——下次观测就无从比较")
	}
}

// 回归：修订行缺失时，基线必须**补写**进修订表。
//
// 早先的实现只在版本递增时才建修订行，而「修订行缺失」走的恰恰是建立基线
// 那条路（不递增）：指纹算出来就被丢掉，此后每次观测都重新建立基线，
// 内容变化永远检测不到，asset_revision 再不前进——注释说「只建立基线」，
// 行为却是「什么也没建立」。这条钉住补齐那一步。
func TestObserveRestoresAMissingBaselineRevision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	asset, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// 模拟历史数据/被手工清过：资产行在，修订行不在。
	if err := store.db.Where("asset_id = ?", asset.ID).Delete(&ProjectAssetRevision{}).Error; err != nil {
		t.Fatalf("清掉修订行: %v", err)
	}

	first, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.FileHash = "hash-after-history"
	}))
	if err != nil {
		t.Fatalf("ObserveAsset(补基线): %v", err)
	}
	if first.Changed {
		t.Fatalf("补建基线不该递增版本: %+v", first)
	}
	revisions, err := store.Revisions(ctx, asset.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("修订行数 = %d, want 1——基线没有补写进修订表，这次指纹已经丢了", len(revisions))
	}
	if revisions[0].RevisionNo != 1 || revisions[0].Status != RevisionActive || revisions[0].ContentHash == "" {
		t.Fatalf("补出来的基线不对: %+v", revisions[0])
	}

	// 补齐之后，内容变化必须检测得到——这正是旧实现永远做不到的那一步。
	changed, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.FileHash = "hash-v2"
	}))
	if err != nil {
		t.Fatalf("ObserveAsset(内容变化): %v", err)
	}
	if !changed.Changed || changed.Next.Revision != 2 {
		t.Fatalf("补上基线后内容变化仍未递增版本: %+v", changed)
	}
	after, err := store.BoundAssets(ctx, "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(after) != 1 || after[0].AssetRevision != 2 {
		t.Fatalf("落库后的 revision = %+v, want 2", after)
	}
}

func TestObserveFillsAnExistingEmptyBaselineWithoutCreatingADuplicate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	asset, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := store.db.Model(&ProjectAssetRevision{}).
		Where("asset_id = ? AND revision_no = ?", asset.ID, 1).
		Updates(map[string]any{"content_hash": "", "weknora_knowledge_id": ""}).Error; err != nil {
		t.Fatalf("清空历史基线: %v", err)
	}

	observed, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.FileHash = "hash-restored"
	}))
	if err != nil {
		t.Fatalf("ObserveAsset(空基线): %v", err)
	}
	if observed.Next.Revision != 1 || observed.Changed {
		t.Fatalf("填充空基线不该递增版本: %+v", observed)
	}
	revisions, err := store.Revisions(ctx, asset.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].ContentHash == "" || revisions[0].KnowledgeID == "" {
		t.Fatalf("空基线填充结果 = %+v, want 唯一且完整的第 1 版", revisions)
	}
}

// 重解析后底座会给出新的知识 ID，那一版修订就该指向新的那一份：
// 列名 weknora_knowledge_id 说的是「这一版对应底座的哪一份」。
func TestObserveRecordsTheKnowledgeOfThatRevision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	asset, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-reparsed"
		s.FileHash = "hash-v2"
	})); err != nil {
		t.Fatalf("ObserveAsset: %v", err)
	}

	revisions, err := store.Revisions(ctx, asset.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("修订行数 = %d, want 2", len(revisions))
	}
	if got := revisions[0].KnowledgeID; got != "k-1" {
		t.Errorf("第一版的知识 ID = %q, want k-1（绑定时的那一份）", got)
	}
	if got := revisions[1].KnowledgeID; got != "k-reparsed" {
		t.Errorf("第二版的知识 ID = %q, want k-reparsed（重解析后的那一份）", got)
	}
}

func TestAssetForKnowledgeOnlyResolvesTheCurrentRevision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	asset, err := store.Bind(ctx, bindInput("p-1", "k-old", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-new"
		s.FileHash = "hash-v2"
	})); err != nil {
		t.Fatalf("ObserveAsset: %v", err)
	}

	if _, err := store.AssetForKnowledge(ctx, "p-1", "k-old"); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("历史 revision 仍可解析为当前资料: %v", err)
	}
	current, err := store.AssetForKnowledge(ctx, "p-1", "k-new")
	if err != nil {
		t.Fatalf("AssetForKnowledge(当前): %v", err)
	}
	if current.ID != asset.ID || current.AssetRevision != 2 || current.KnowledgeID != "k-new" {
		t.Fatalf("当前 revision 归属 = %+v, want asset=%s revision=2 knowledge=k-new", current, asset.ID)
	}
}

func TestObserveUnknownAssetIsNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.ObserveAsset(ctx, "p-1", "a-missing", signal(nil)); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("观测不存在的资料返回 %v, want ErrAssetNotFound", err)
	}
	// 别的项目里的资料，从这个项目看也必须是不存在（不泄露存在性）。
	if _, err := store.Bind(ctx, bindInput("p-2", "k-2", nil)); err != nil {
		t.Fatalf("Bind p-2: %v", err)
	}
	other, _ := store.BoundAssets(ctx, "p-2")
	if _, err := store.ObserveAsset(ctx, "p-1", other[0].ID, signal(nil)); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("跨项目观测返回 %v, want ErrAssetNotFound", err)
	}
}

// 真实存储 + 真实网关 + 替身授权：T09 验收第 4 条「核对请求与实际返回的资料 ID 集合」。
//
// 这不是 T03 那条测试的重复：那里绑定存储是替身，这里走的是一次真写的库。
func TestGatewayAgainstRealStorePartitionsRequestedSet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	actor := Actor{UserID: "u-1", TenantID: "1"}

	ready, err := store.Bind(ctx, bindInput("p-1", "k-ready", nil))
	if err != nil {
		t.Fatalf("Bind ready: %v", err)
	}
	busy, err := store.Bind(ctx, bindInput("p-1", "k-busy", func(in *BindInput) {
		in.Signal = signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-busy"
			s.ParseStatus = types.ParseStatusFinalizing
		})
	}))
	if err != nil {
		t.Fatalf("Bind busy: %v", err)
	}
	if _, err := store.Bind(ctx, bindInput("p-2", "k-other", nil)); err != nil {
		t.Fatalf("Bind other project: %v", err)
	}
	otherAssets, err := store.BoundAssets(ctx, "p-2")
	if err != nil || len(otherAssets) != 1 {
		t.Fatalf("读另一个项目的绑定: %v (%d 条)", err, len(otherAssets))
	}
	otherID := otherAssets[0].ID

	// 授权替身只在这里扮演「当前授权」；它是 T07 合入前的固定实现。
	authorized := func(Asset) bool { return true }
	gateway := NewAssetGateway(store, authorizerFunc(func(_ context.Context, _ Actor, _ string, a Asset) (bool, error) {
		return authorized(a), nil
	}))

	requested := []string{ready.ID, busy.ID, "a-missing", otherID, ready.ID}
	res, err := gateway.ResolveAllowed(ctx, "p-1", actor, requested)
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}

	if len(res.Requested) != 4 {
		t.Fatalf("Requested 去重后 = %d 项, want 4（重复项应折叠）", len(res.Requested))
	}
	if len(res.Allowed) != 1 || res.Allowed[0].ID != ready.ID {
		t.Fatalf("Allowed = %v, want 仅 ready", assetIDs(res.Allowed))
	}
	if len(res.Denied) != 3 {
		t.Fatalf("Denied = %d 项, want 3", len(res.Denied))
	}
	want := map[string]DenyReason{
		busy.ID:     DenyNotReady,
		"a-missing": DenyNotFound,
		otherID:     DenyNotFound, // 别的项目的资料，从这个项目看与不存在收敛为同一个原因
	}
	for _, d := range res.Denied {
		if want[d.AssetID] != d.Reason {
			t.Errorf("%s 的拒绝原因 = %q, want %q", d.AssetID, d.Reason, want[d.AssetID])
		}
	}

	// 逐项核对：请求集合必须恰好等于允许 ∪ 拒绝，缺项就是「部分授权被当成了全部成功」。
	seen := map[string]bool{}
	for _, a := range res.Allowed {
		seen[a.ID] = true
	}
	for _, d := range res.Denied {
		seen[d.AssetID] = true
	}
	for _, id := range res.Requested {
		if !seen[id] {
			t.Errorf("请求的 %s 既没被允许也没被拒绝——这正是契约 §8 禁止的静默子集", id)
		}
	}
}

// 真实 DDL 与模型必须逐列对得上。AutoMigrate 建的表只能证明模型自洽，
// 证明不了 migrations/ 里那一份；而线上跑的是后者。这里把 000019 原样执行，
// 再用真实 SQL 走一遍写入、读取与观测——列名或约束漂移会在这里断。
func TestSQLiteMigrationSchemaSupportsBindings(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrated.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ddl, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", "000019_lingdoc_evidence_assets.up.sql"))
	if err != nil {
		t.Fatalf("读迁移文件: %v", err)
	}
	if err := db.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("执行迁移 DDL: %v", err)
	}

	store := NewBindings(db)
	asset, err := store.Bind(ctx, bindInput("p-1", "k-1", nil))
	if err != nil {
		t.Fatalf("迁移出的表上绑定: %v", err)
	}
	if asset.AssetRevision != 1 || asset.ProcessingState != AssetStateReady {
		t.Fatalf("绑定结果异常: %+v", asset)
	}

	// 迁移里的 CHECK 是真的在拦：processing_state 只认契约那五个值。
	if err := db.Exec(
		"UPDATE lingdoc_project_assets SET processing_state = 'ready-ish' WHERE id = ?", asset.ID,
	).Error; err == nil {
		t.Error("迁移的 CHECK 约束没有拦住非法 processing_state")
	}

	observed, err := store.ObserveAsset(ctx, "p-1", asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-1"
		s.FileHash = "hash-v2"
	}))
	if err != nil {
		t.Fatalf("迁移出的表上观测: %v", err)
	}
	if !observed.Changed || observed.Next.Revision != 2 {
		t.Fatalf("内容变化未递增版本: %+v", observed)
	}

	// 唯一约束 (project_id, knowledge_id) 是幂等绑定的兜底，必须在真实表上存在。
	dup := ProjectAsset{
		ID: "a-dup", TenantID: 1, ProjectID: "p-1", KnowledgeID: "k-1",
		Origin: OriginWeKnora, AssetRevision: 1, ProcessingState: string(AssetStateReady),
	}
	if err := db.Create(&dup).Error; err == nil {
		t.Error("重复绑定没有被唯一索引拦住——并发下的第二次绑定会写进第二行")
	}

	// 索引也必须对齐。模型上的 `gorm:"index"` / `uniqueIndex` 与迁移里的
	// CREATE INDEX / UNIQUE 是两处独立声明，谁多一条少一条，测试库（AutoMigrate）
	// 与线上 DDL 就分叉了。这条是补上来的：早先只看表名与 CHECK，
	// 漏掉过一次 deleted_at 索引的漂移。
	modelStore := newTestStore(t)
	for _, table := range []string{"lingdoc_project_assets", "lingdoc_asset_revisions"} {
		migrated := indexShapes(t, db, table)
		modelled := indexShapes(t, modelStore.db, table)
		if !reflect.DeepEqual(migrated, modelled) {
			t.Errorf("%s 的索引对不上：\n迁移  %v\n模型  %v", table, migrated, modelled)
		}
	}
}

// indexShapes 取一张表上所有索引的「形状」：唯一与否 + 列序列，排序后返回。
//
// 刻意不比名字：迁移里用表级 UNIQUE 声明的那两条，SQLite 建出来叫
// sqlite_autoindex_*，而 AutoMigrate 用 uniqueIndex 建出来是带名字的索引。
// 名字不同而约束相同，比名字只会造出假的红；要比的是「哪几列被索引了」。
func indexShapes(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	var indexes []struct {
		Name   string
		Unique int
	}
	if err := db.Raw("PRAGMA index_list(" + table + ")").Scan(&indexes).Error; err != nil {
		t.Fatalf("读 %s 的索引列表: %v", table, err)
	}
	out := make([]string, 0, len(indexes))
	for _, index := range indexes {
		var columns []struct {
			Name string
		}
		if err := db.Raw("PRAGMA index_info(" + index.Name + ")").Scan(&columns).Error; err != nil {
			t.Fatalf("读索引 %s 的列: %v", index.Name, err)
		}
		names := make([]string, 0, len(columns))
		for _, column := range columns {
			names = append(names, column.Name)
		}
		out = append(out, fmt.Sprintf("unique=%d(%s)", index.Unique, strings.Join(names, ",")))
	}
	sort.Strings(out)
	return out
}

// authorizerFunc 让测试就地给一个 Authorizer，而不必每处都定义结构体。
type authorizerFunc func(context.Context, Actor, string, Asset) (bool, error)

func (f authorizerFunc) CanAccessAsset(ctx context.Context, actor Actor, projectID string, a Asset) (bool, error) {
	return f(ctx, actor, projectID, a)
}
