package delivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 这一组用例问的是两件不同的事：
//
//  1. 两个实现是不是同一件事（下面 forEachSnapshotStore / forEachStorePair 的那组
//     断言，一份代码跑两遍）；
//  2. 落库实现独有的性质：重启还在、字节逐字节、列表不拉字节、次序稳定。
//
// 第 2 组内存实现没有对应物，所以它只跑一次。

// snapshotStoreUnderTest / exportStoreUnderTest 是一致性套件实际拿到的形状。
//
// 两个真实现都必须具备**全部**能力：少一项，装配处（container.go）就会拿到 nil，
// 服务整体不工作。所以测试里直接按完整形状要，而不是每处断言一个再跳过——
// 「这一项能力有没有」本身就是要测的东西，不该在夹具里被吞掉。
type (
	snapshotStoreUnderTest interface {
		SnapshotStore
		FreezeRecorder
		SnapshotLister
	}
	exportStoreUnderTest interface {
		ExportStore
		ExportRecorder
		ExportLister
	}
)

// storePair 是一组配套的存储。SQLite 侧两个库共用同一个连接：产物表的 snapshot_id
// 指向快照表，分成两个库文件就不是同一个库了。
type storePair struct {
	snapshots snapshotStoreUnderTest
	exports   exportStoreUnderTest
}

type storeFactory struct {
	name string
	open func(t *testing.T) storePair
}

// storeFactories 是同一组断言的两种实现：内存那份是参照实现（语义定义在它身上），
// 落库那份是生产装配用的那一份。两边都由同一组断言跑过，分家会立刻红。
func storeFactories() []storeFactory {
	return []storeFactory{
		{name: "memory", open: func(*testing.T) storePair {
			return storePair{snapshots: NewMemorySnapshotStore(), exports: NewMemoryExportStore()}
		}},
		{name: "sqlite", open: func(t *testing.T) storePair {
			db := openDeliveryTestDB(t)
			return storePair{snapshots: NewSQLiteSnapshotStore(db), exports: NewSQLiteExportStore(db)}
		}},
	}
}

func forEachSnapshotStore(t *testing.T, assert func(t *testing.T, store snapshotStoreUnderTest)) {
	t.Helper()
	for _, factory := range storeFactories() {
		t.Run(factory.name, func(t *testing.T) { assert(t, factory.open(t).snapshots) })
	}
}

func forEachStorePair(t *testing.T, assert func(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest)) {
	t.Helper()
	for _, factory := range storeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			pair := factory.open(t)
			assert(t, pair.snapshots, pair.exports)
		})
	}
}

// openDeliveryTestDB 建一个测试库。建表跑**生产迁移文件**，不用 AutoMigrate。
//
// 这两个库的正确性有一半落在列的形状上（is_current 落不落列、file_blob 是不是 NULL、
// UNIQUE 里有没有那三列动作），而 AutoMigrate 会按 gorm 自己的理解另建一份 schema：
// 测试于是对着一个生产里不存在的表撒谎，且每一处都对得上。
//
// 先跑 000018（提供外键父表 lingdoc_projects）再跑 000024，顺带证明两份迁移能在
// 同一个库上连着跑。
func openDeliveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return openDeliveryTestDBAt(t, filepath.Join(t.TempDir(), "delivery.db"))
}

func openDeliveryTestDBAt(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db := openDeliveryDBAt(t, path)
	for _, name := range []string{"000018_lingdoc_workspace.up.sql", "000024_lingdoc_delivery_stores.up.sql"} {
		applyDeliveryMigration(t, db, name)
	}
	return db
}

// reopenDeliveryTestDBAt 重开一个**已经建好**的库文件，不再跑迁移。生产里也是
// 如此：迁移由版本表判重，只在启动时跑一次；重开一次连接不该再建一次表。
// 演「重启」的用例要走它，否则第二次 CREATE TABLE 会撞在表名上。
func reopenDeliveryTestDBAt(t *testing.T, path string) *gorm.DB {
	t.Helper()
	return openDeliveryDBAt(t, path)
}

// openDeliveryDBAt 打开库文件并挂上关闭清理。两个包装函数的差别只在跑不跑迁移。
func openDeliveryDBAt(t *testing.T, path string) *gorm.DB {
	t.Helper()
	// 不开 PRAGMA foreign_keys：生产的 DSN 是一个裸路径（RunMigrationsWithOptions 里
	// sql.Open("sqlite3", opts.SQLiteDBPath)），外键**没有**打开。测试开了就会测出
	// 一条生产里不存在的约束，反而掩盖真问题。
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func applyDeliveryMigration(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(stripSQLComments(string(migration)), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("production SQLite migration %s: %v", name, err)
		}
	}
}

func stripSQLComments(script string) string {
	var uncommented strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		uncommented.WriteString(line)
		uncommented.WriteByte('\n')
	}
	return uncommented.String()
}

// closeDeliveryTestDB 关掉一个测试库，用来演「重启」。t.Cleanup 里那次关闭会拿到
// 一个「已经关了」的错误，忽略即可。
func closeDeliveryTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

// preparedSnapshotAt 冻一份快照并落存，时钟钉住——顺序与时刻的断言都要靠它。
func preparedSnapshotAt(t *testing.T, store SnapshotStore, at time.Time) ReleaseSnapshot {
	t.Helper()
	service := NewReleaseService(store)
	service.now = func() time.Time { return at }
	snapshot, err := service.Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// preparedSnapshotOn 是 preparedSnapshotAt 在该包既有用例里用的那一枚固定时刻，
// 与 export_test.go 的 preparedSnapshot 走同一条路径。
func preparedSnapshotOn(t *testing.T, store SnapshotStore) ReleaseSnapshot {
	t.Helper()
	return preparedSnapshotAt(t, store, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
}

// listExports 走**对外契约**读一遍产物列表，而不是去摸内存实现的内部 map：
// 摸内部就只能跑在内存那一份上，而这条断言（这次动作到底落下几份产物）正是两个
// 实现最该被对照着看的地方。
func listExports(t *testing.T, exports exportStoreUnderTest, projectID string) []ExportArtifact {
	t.Helper()
	artifacts, err := exports.ListExports(projectID)
	if err != nil {
		t.Fatal(err)
	}
	return artifacts
}

// 冻结输入与检查结果各有一份存储形状（storageInput / storageCheck），它们存在的
// 唯一理由是领域类型里有三个 json:"-" 的字段。这条断言是整个 DTO 的验收点：
// 往返之后逐字段相同，加上那三个字段确实活着。
func TestFrozenInputAndCheckRoundTripThroughStorage(t *testing.T) {
	input := validDeliveryInput()
	raw, err := encodeFrozenInput(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFrozenInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 比对的那一份是 cloneInput(input)：ReleaseService.Freeze 第一行就把输入规整过了，
	// 进库的、算摘要的都是它。直接拿 input 比会红，但红的原因是 fixture 里那两个空集合
	// 写的是 nil、而契约要求空集合落成 []——那是 cloneInput 的活，不是 codec 的活
	//（%+v 也看不出 nil 与空切片的差别，这个坑值得摆在明处）。
	frozen := cloneInput(input)
	if !reflect.DeepEqual(decoded, frozen) {
		t.Fatalf("读回来的冻结输入与冻结的那份不是同一个东西:\n%+v\nvs\n%+v", decoded, frozen)
	}
	// SectionID 是三个 json:"-" 里最贵的一个：Evaluate 按它判「模板必填章节齐了没有」，
	// 它丢了的话，一份冻结时 passed 的快照读回来会多出 required_chapter_missing。
	for index, chapter := range decoded.Chapters {
		if chapter.SectionID != input.Chapters[index].SectionID || chapter.SectionID == "" {
			t.Fatalf("第 %d 章的 SectionID = %q, want %q", index, chapter.SectionID, input.Chapters[index].SectionID)
		}
	}

	blocked := validDeliveryInput()
	blocked.Chapters[0].ChapterVersionID = nil
	blocked.Chapters[0].Confirmation = nil
	check := Evaluate(blocked)
	if len(check.Issues) == 0 {
		t.Fatal("这条用例需要一个带阻断项的检查结果")
	}
	rawCheck, err := encodeCheck(check)
	if err != nil {
		t.Fatal(err)
	}
	decodedCheck, err := decodeCheck(rawCheck)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodedCheck, check) {
		t.Fatalf("读回来的检查结果与算出来的那份不是同一个东西:\n%+v\nvs\n%+v", decodedCheck, check)
	}
	// Code 与 ChapterID 是另外两个 json:"-"：少了它们，同一条阻断项在重放与首次
	// 发生时长得不一样。
	if code, chapterID := decodedCheck.Issues[0].Code, decodedCheck.Issues[0].ChapterID; code == "" || chapterID == "" {
		t.Fatalf("检查结果丢了内部标签：code=%q chapterID=%q", code, chapterID)
	}
}

// 一个**规整过**的值再编一遍，字节逐字节相同。
//
// 起点是 cloneInput(validDeliveryInput())，不是那个原始 fixture：进这一列的值都来自
// ReleaseService.Freeze，而它落下的第一件事就是 cloneInput。没规整过的输入编出来的
// 字节里留着 null，读回来时解侧的规整把它变成 []，于是前后两串字节不同——那一次差别
// 是正常的（上一条用例盯的就是它），这里问的是第二次之后还动不动。
//
// 这条比 DeepEqual 多管一件事：DeepEqual 不会告诉你「读回来的那份少了东西，于是再编
// 出来的字节也不同」。解码侧丢掉 SectionID 的话，这里会看到 "section_id":"" 与原串
// 对不上——字段丢在哪一侧，两条用例各管一边。
func TestStorageEncodingIsStableAcrossARoundTrip(t *testing.T) {
	raw, err := encodeFrozenInput(cloneInput(validDeliveryInput()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFrozenInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeFrozenInput(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if again != raw {
		t.Fatalf("两次编码出的字节不同:\n%s\nvs\n%s", again, raw)
	}
}

// 落库的那一份必须能算出与冻结时同一枚摘要，并且算出同一份检查结论。
//
// 这条比上面那条更值钱：它走的是「存进去 → 服务重启 → 读出来」，也就是 DTO 与
// 存储形状真正被用到的路径。SectionID 之类丢了，Evaluate 立刻多出阻断项。
func TestStoredSnapshotEvaluatesAndDigestsTheSame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.db")
	first := openDeliveryTestDBAt(t, path)
	frozen := preparedSnapshotOn(t, NewSQLiteSnapshotStore(first))
	closeDeliveryTestDB(t, first)

	reopened := reopenDeliveryTestDBAt(t, path)
	stored, err := NewSQLiteSnapshotStore(reopened).Get(frozen.ProjectID, frozen.ID)
	if err != nil {
		t.Fatalf("重启之后取不到刚冻下的快照: %v", err)
	}
	if !reflect.DeepEqual(Evaluate(stored.FrozenInput), Evaluate(frozen.FrozenInput)) {
		t.Fatalf("读回来的冻结输入算出了另一份检查结论:\n%+v\nvs\n%+v",
			Evaluate(stored.FrozenInput), Evaluate(frozen.FrozenInput))
	}
	if digest, err := digestFrozenInput(stored.FrozenInput); err != nil || digest != frozen.SnapshotDigest {
		t.Fatalf("读回来的冻结输入算出了另一枚摘要: %q, %v; want %q", digest, err, frozen.SnapshotDigest)
	}
	if !stored.IsCurrent {
		t.Fatal("is_current 没有落列：重启之后每一份历史快照都会被当成不再代表工作区")
	}
	// 时刻用 .Equal 而不是 DeepEqual：这里问的是同一个瞬间，不是同一个 location 指针。
	if !stored.CreatedAt.Equal(frozen.CreatedAt) {
		t.Fatalf("created_at = %s, want %s", stored.CreatedAt, frozen.CreatedAt)
	}
}

// 冻结与「哪一次动作冻了它」在同一行上，所以重启之后同键重放仍然换回原来那一份。
// 动作记录要是没落库，重启后的重试会被当成新动作，一次用户动作在交付历史里变成两条。
func TestFreezeActionSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.db")
	// 走 Freeze 而不是 Prepare：冻结本身不落存（见 ReleaseService.Freeze 的注释），
	// 于是库里那一行只可能来自 RecordFreeze——「动作记录活着没有」这个问题就没有
	// 第二种解释。这也是生产的那条顺序（DeliveryReleaseService.Prepare 同样是
	// Freeze 之后直接 RecordFreeze）。
	releases := NewReleaseService(NewSQLiteSnapshotStore(openDeliveryTestDBAt(t, path)))
	frozen, err := releases.Freeze(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}

	reopened := NewSQLiteSnapshotStore(reopenDeliveryTestDBAt(t, path))
	if _, err := reopened.Get(frozen.ProjectID, frozen.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Freeze 自己把快照落了存: %v", err)
	}
	attempt := freezeAttempt()
	if recorded, replayed, err := reopened.RecordFreeze(frozen, attempt, "hash-1"); err != nil || replayed || recorded.ID != frozen.ID {
		t.Fatalf("RecordFreeze = %s replayed=%v err=%v", recorded.ID, replayed, err)
	}

	// 再重开一次库才问：库才是那条动作记录的唯一去处，这一问跨了两次连接。
	again := NewSQLiteSnapshotStore(reopenDeliveryTestDBAt(t, path))
	replayed, found, err := again.ReplayFreeze(attempt, "hash-1")
	if err != nil || !found {
		t.Fatalf("重启后重放 = found=%v err=%v", found, err)
	}
	if replayed.ID != frozen.ID {
		t.Fatalf("重启后重放换回了 %s, want %s", replayed.ID, frozen.ID)
	}
	if _, _, err := again.ReplayFreeze(attempt, "hash-2"); err != ErrIdempotencyConflict {
		t.Fatalf("重启后同键换请求 = %v, want ErrIdempotencyConflict", err)
	}
}

// ZIP 里 NUL 遍地，DOCX 的高位字节也不罕见。字节要是过一遍 text 处理（截断在
// 第一个 NUL、或按 UTF-8 改写），下载回来的文件就打不开了，而 sha256 也对不上——
// 那是「库里说校验通过、交出去的文件却是坏的」。
func TestExportStoresFileBytesByteForByte(t *testing.T) {
	db := openDeliveryTestDB(t)
	snapshots, exports := NewSQLiteSnapshotStore(db), NewSQLiteExportStore(db)
	frozen := preparedSnapshotOn(t, snapshots)
	file := append([]byte("PK\x03\x04"), 0x00, 0xff, 0xfe, 0x80, '\r', '\n', 0x00)
	file = append(file, []byte("docx payload 中文")...)

	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return file, nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, _, err := service.Start("owner", frozen.ProjectID, frozen.ID, exportActionKey)
	if err != nil || artifact.Status != ExportVerified {
		t.Fatalf("start = %+v, %v", artifact, err)
	}
	sum := sha256.Sum256(file)
	if artifact.FileSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("文件指纹 = %s, want %s", artifact.FileSHA256, hex.EncodeToString(sum[:]))
	}

	_, downloaded, err := service.Download("owner", frozen.ProjectID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, file) {
		t.Fatalf("下载回来的字节与产出的不同：%d bytes vs %d bytes", len(downloaded), len(file))
	}
	// 换一个 store 实例再读一次：上面那次读可能沿用了服务手里那份产物，
	// 这里问的是「字节真的写进库了没有」。
	stored, err := NewSQLiteExportStore(db).GetExport(frozen.ProjectID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.file, file) {
		t.Fatalf("从库里读出的字节与产出的不同：%d bytes vs %d bytes", len(stored.file), len(file))
	}
}

// 列表读的是**不带 file_blob 的投影**。一页 50 份产物，每份几 MB，用带字节的行去读
// 就是把整页的文件都拉进内存，而列表根本不显示它们。
func TestExportListDoesNotReadTheFileBytes(t *testing.T) {
	db := openDeliveryTestDB(t)
	snapshots, exports := NewSQLiteSnapshotStore(db), NewSQLiteExportStore(db)
	frozen := preparedSnapshotOn(t, snapshots)
	file := []byte("PK\x03\x04 listed but not loaded")

	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return file, nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, _, err := service.Start("owner", frozen.ProjectID, frozen.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := exports.ListExports(frozen.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("列出 %d 份产物, want 1", len(listed))
	}
	if len(listed[0].file) != 0 {
		t.Fatalf("列表把 %d 字节的文件也读了出来", len(listed[0].file))
	}
	// 投影读出来的其余字段必须与完整读一致——两条路径对同一行给出同一个状态。
	// 比的时候把字节补回投影那一份，剩下的字段就要求逐个相等。
	full, err := exports.GetExport(frozen.ProjectID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	projection := listed[0]
	projection.file = full.file
	if !reflect.DeepEqual(projection, full) {
		t.Fatalf("列表读出的行与完整读出的行不同:\n%+v\nvs\n%+v", projection, full)
	}
	// 反过来确认字节仍在库里：列表没读它，不等于它没在。
	if _, downloaded, err := service.Download("owner", frozen.ProjectID, artifact.ID); err != nil || !bytes.Equal(downloaded, file) {
		t.Fatalf("列表读不到字节，下载也读不到: %v", err)
	}
}

// 两条记录的 created_at 相同时，次序必须由 id 兜底且稳定——生产的墙上时钟分辨率
// 不是无限的，这不是只在固定时钟的测试里才会遇到。
func TestListsOrderEqualTimestampsByIdDescending(t *testing.T) {
	db := openDeliveryTestDB(t)
	snapshots, exports := NewSQLiteSnapshotStore(db), NewSQLiteExportStore(db)
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	base := preparedSnapshotAt(t, snapshots, at)
	for _, id := range []string{"snapshot-a", "snapshot-e"} {
		row := base
		row.ID = id
		if err := snapshots.Save(row); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := snapshots.ListSnapshots(base.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("列出 %d 份快照, want 3", len(listed))
	}
	// base 自己的 ID 是 snapshot-<hex>，与上面两枚都在同一时刻；三者的次序按 ID 降序，
	// 与内存实现同一条规则。
	for _, snapshot := range listed {
		if !snapshot.CreatedAt.Equal(at) {
			t.Fatalf("%s 的 created_at = %s, want %s", snapshot.ID, snapshot.CreatedAt, at)
		}
	}
	if !sortedByIDDescending(listed) {
		t.Fatalf("按 ID 降序排列的兜底没生效: %v", snapshotIDs(listed))
	}

	for _, id := range []string{"export-a", "export-e"} {
		if err := exports.SaveExport(ExportArtifact{ID: id, ProjectID: base.ProjectID, SnapshotID: base.ID, Status: ExportFailed, FailureCode: FailureRenderFailed, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, err := exports.ListExports(base.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || !sortedByIDDescendingArtifacts(artifacts) {
		t.Fatalf("产物列表次序 = %v", artifactIDs(artifacts))
	}
}

func sortedByIDDescending(snapshots []ReleaseSnapshot) bool {
	for index := 1; index < len(snapshots); index++ {
		if snapshots[index-1].ID < snapshots[index].ID {
			return false
		}
	}
	return true
}

func sortedByIDDescendingArtifacts(artifacts []ExportArtifact) bool {
	for index := 1; index < len(artifacts); index++ {
		if artifacts[index-1].ID < artifacts[index].ID {
			return false
		}
	}
	return true
}

func snapshotIDs(snapshots []ReleaseSnapshot) []string {
	out := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, snapshot.ID)
	}
	return out
}

func artifactIDs(artifacts []ExportArtifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, artifact.ID)
	}
	return out
}
