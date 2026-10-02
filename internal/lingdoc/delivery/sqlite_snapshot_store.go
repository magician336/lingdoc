package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SQLiteSnapshotStore 是交付快照的落地实现：同一台服务里冻下的快照，重启之后还在。
//
// 它放 `package delivery` 而不是另立一层，与本仓既有做法一致（candidateadoption /
// generation 都是 store 与领域同包），也是不得不如此：ReleaseSnapshot 里的
// SnapshotChapter.SectionID 与 CheckIssue.Code/ChapterID 都是 json:"-" 的包内细节，
// 跨包的 store 连「该存哪些字段」都看不出来。
//
// 已知限制：接口里的五个方法都没有 ctx（见 SnapshotStore/FreezeRecorder/SnapshotLister），
// 所以这些查询不随请求取消。加 ctx 要同时改接口、两个实现与 ReleaseService 的全部调用点，
// 本轮不做——但这是一处真实的债，不是可以忘掉的事。
type SQLiteSnapshotStore struct {
	db *gorm.DB
}

// 三项能力一个都不能少：装配处（container.go）把 SnapshotStore 交给 T13，交给 T14 的
// 是同一个实例；少一项就在构造器那里拿到 nil，服务整体不工作。
var (
	_ SnapshotStore  = (*SQLiteSnapshotStore)(nil)
	_ FreezeRecorder = (*SQLiteSnapshotStore)(nil)
	_ SnapshotLister = (*SQLiteSnapshotStore)(nil)
)

func NewSQLiteSnapshotStore(db *gorm.DB) *SQLiteSnapshotStore {
	return &SQLiteSnapshotStore{db: db}
}

// releaseSnapshotRow 是 lingdoc_release_snapshots 的一行。
//
// 每列都写了 column 标签：这张表由手写的迁移建（000024 / 000103），不经 AutoMigrate，
// 所以「gorm 猜出来的列名」与「迁移写下的列名」没有任何机制保证一致。标签让它们
// 只能靠读这一处对上，而不是靠运行时的猜测对不对。
//
// **动作记录与快照是同一行**（action_* 三列）。理由是这个不变式：索引与实体在
// 同一行上，「索引在、实体不在」的半截状态结构上不可能存在。三列为可空，是因为
// Save 落下的快照不属于任何一次动作——而 NULL 在 UNIQUE 索引里互不相等
// （SQLite 与 PostgreSQL 同一条），所以这些行不会互相撞车。
type releaseSnapshotRow struct {
	ID                string    `gorm:"column:id;primaryKey;size:64"`
	ProjectID         string    `gorm:"column:project_id;not null;size:64"`
	FrozenInputJSON   string    `gorm:"column:frozen_input_json;not null;type:text"`
	SnapshotDigest    string    `gorm:"column:snapshot_digest;not null;size:64"`
	CheckJSON         string    `gorm:"column:check_json;not null;type:text"`
	IsCurrent         bool      `gorm:"column:is_current;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null"`
	ActionActorID     *string   `gorm:"column:action_actor_id;size:128"`
	ActionKey         *string   `gorm:"column:action_key;size:128"`
	ActionRequestHash *string   `gorm:"column:action_request_hash;size:64"`
}

func (releaseSnapshotRow) TableName() string { return "lingdoc_release_snapshots" }

// Save 是**建或替换**，与内存实现的 `map[k] = v` 同语义。
//
// upsert 只更新 payload 列，绝不碰 action_* 三列：内存实现里实体与动作是两个 map，
// Save 动不了动作那一份，这里也必须动不了。把它们写进 DO UPDATE 的后果是：
// 一次 Save 会把这个键下的动作记录抹掉，同一个键的重试于是变成一次新动作。
func (s *SQLiteSnapshotStore) Save(snapshot ReleaseSnapshot) error {
	row, err := snapshotRowOf(snapshot)
	if err != nil {
		return err
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"project_id", "frozen_input_json", "snapshot_digest", "check_json", "is_current", "created_at"}),
	}).Create(&row).Error
}

// Get 按项目取一份快照。两个条件都要判：project_id 不是身份的一部分（不透明的 ID
// 已经全局唯一），而是**作用域**——换个项目问同一个 ID，答的必须是「没有」。
func (s *SQLiteSnapshotStore) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	return s.readSnapshot(projectID, snapshotID)
}

// ListSnapshots 返回该项目下的快照，新的在前。
//
// 排序的兜底是 `id DESC`：created_at 相同（固定时钟的测试里会遇到，生产里也会——
// 墙上时钟的分辨率不是无限的）时，少了兜底，顺序就由存储引擎的返回次序决定，
// 同一份历史两次读到两个样子。内存实现用**同一个**次序，两份实现才可能被同一组
// 断言跑过去。已知的一处方言差异：PG 的默认 collation 与 Go 的字节序严格说不完全
// 同规则，对 `snapshot-<hex>` 这种「同前缀 + 十六进制」实际无差；这是
// candidate_adoption_store.go 已有的性质，接受。
func (s *SQLiteSnapshotStore) ListSnapshots(projectID string) ([]ReleaseSnapshot, error) {
	var rows []releaseSnapshotRow
	if err := s.db.Where("project_id = ?", projectID).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	// 显式 make：接口注释要求没有历史时返回 `[]` 而不是 nil，而 gorm 往 nil 切片里
	// Find 出零行，长度是 0、值仍是 nil。调用方要能区分「没有历史」与「读失败」。
	snapshots := make([]ReleaseSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := snapshotFromRow(row)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (s *SQLiteSnapshotStore) ReplayFreeze(attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	row, err := s.lookupFreeze(attempt)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReleaseSnapshot{}, false, nil
	}
	if err != nil {
		return ReleaseSnapshot{}, false, err
	}
	return s.replay(row, requestHash)
}

// RecordFreeze 把快照与「这次动作冻了它」放进**同一次写入**，并按 RowsAffected
// 判定谁落笔。
//
// 冲突目标是那条动作索引，而不是无目标的 `DoNothing`（两者编出来都是
// `ON CONFLICT ... DO NOTHING`，区别在目标）：有了目标，「RowsAffected == 0」与
// 「这条动作已经记过」就是同一件事，一一对应，不留一条不可达的分支，
// 也没有「跳过写入的原因说不上来」这种状态。
//
// 不选「事务里先读再插」：SQLite 只有一把写锁，读-插-回滚的窗口里输的一方会撞上
// 锁升级而拿到一个假错误，而契约 §6 要的是重放。
//
// 冲突目标只覆盖那条动作索引，代价是：调用方若**先把这份快照 Save 进库、再拿它
// RecordFreeze**，这条 INSERT 撞的是主键，驱动会直接报约束错误。那是一个违反契约的
// 调用——Freeze 不落存，正是为了让 RecordFreeze 成为「快照与动作一起落」的那唯一
// 一次写入——所以宁可让它响亮地失败，也不在这里加一条「把动作挂到已存在行上」的
// 分支：那条分支还要回答「那一行已经记着另一次动作怎么办」，而那个问题在内存实现
// （两个 map，动作可以指同一个快照）与这一张表（一行一动作）之间没有同一个答案。
func (s *SQLiteSnapshotStore) RecordFreeze(snapshot ReleaseSnapshot, attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	row, err := snapshotRowOf(snapshot)
	if err != nil {
		return ReleaseSnapshot{}, false, err
	}
	row.ActionActorID, row.ActionKey, row.ActionRequestHash = &attempt.ActorID, &attempt.Key, &requestHash
	result := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "action_actor_id"}, {Name: "action_key"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return ReleaseSnapshot{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		// 输给了并发的那一次，或者这个键早就记过了：两种情形问的是同一件事，
		// 都要走**同一条**重放路径（含请求指纹不符即冲突的判定），
		// 而且必须交出**库里那一份**，不是自己手上这份——CreatedAt/IsCurrent
		// 可能是赢家写定的。
		winner, err := s.lookupFreeze(attempt)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ReleaseSnapshot{}, false, fmt.Errorf("%w: freeze %s was skipped without an action record", ErrSnapshotNotFound, attempt.Key)
		}
		if err != nil {
			return ReleaseSnapshot{}, false, err
		}
		return s.replay(winner, requestHash)
	}
	// 落笔的是这一次：把**库里那一份**交回去，而不是手上这份——它对这次写入的
	// 唯一凭据就是刚跑完的那条 INSERT。
	stored, err := s.readSnapshot(snapshot.ProjectID, snapshot.ID)
	if err != nil {
		return ReleaseSnapshot{}, false, err
	}
	return stored, false, nil
}

// lookupFreeze 读这次动作记下的那一行。gorm.ErrRecordNotFound 原样透出：两个调用方
// 对「查不到」的处置不同，由它们各自决定（读侧答「这个动作还没做过」，写侧把它
// 当成一次说不清原因的跳过）。
func (s *SQLiteSnapshotStore) lookupFreeze(attempt FreezeAttempt) (releaseSnapshotRow, error) {
	var row releaseSnapshotRow
	err := s.db.Where("project_id = ? AND action_actor_id = ? AND action_key = ?",
		attempt.ProjectID, attempt.ActorID, attempt.Key).First(&row).Error
	return row, err
}

// replay 把一个已记下的动作换回它当初冻出来的那一份。
func (s *SQLiteSnapshotStore) replay(row releaseSnapshotRow, requestHash string) (ReleaseSnapshot, bool, error) {
	if row.ActionRequestHash == nil || *row.ActionRequestHash != requestHash {
		// 同一个键换了请求是冲突，不是重试，也不能把新内容当成旧动作的结果交出去。
		return ReleaseSnapshot{}, false, ErrIdempotencyConflict
	}
	snapshot, err := s.readSnapshot(row.ProjectID, row.ID)
	if err != nil {
		return ReleaseSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *SQLiteSnapshotStore) readSnapshot(projectID, snapshotID string) (ReleaseSnapshot, error) {
	var row releaseSnapshotRow
	if err := s.db.Where("id = ? AND project_id = ?", snapshotID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ReleaseSnapshot{}, fmt.Errorf("%w: %s", ErrSnapshotNotFound, snapshotID)
		}
		return ReleaseSnapshot{}, err
	}
	return snapshotFromRow(row)
}

func snapshotRowOf(snapshot ReleaseSnapshot) (releaseSnapshotRow, error) {
	frozen, err := encodeFrozenInput(snapshot.FrozenInput)
	if err != nil {
		return releaseSnapshotRow{}, err
	}
	check, err := encodeCheck(snapshot.Check)
	if err != nil {
		return releaseSnapshotRow{}, err
	}
	return releaseSnapshotRow{
		ID: snapshot.ID, ProjectID: snapshot.ProjectID,
		FrozenInputJSON: frozen, SnapshotDigest: snapshot.SnapshotDigest, CheckJSON: check,
		IsCurrent: snapshot.IsCurrent,
		// 写侧归一成 UTC：SQLite 把 time.Time 存成带偏移的字符串，而
		// `ORDER BY created_at` 是**字符串**比较。混进一行非零偏移，次序就错位，
		// 而「新的在前」是接口承诺。
		CreatedAt: snapshot.CreatedAt.UTC(),
	}, nil
}

func snapshotFromRow(row releaseSnapshotRow) (ReleaseSnapshot, error) {
	frozen, err := decodeFrozenInput(row.FrozenInputJSON)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	check, err := decodeCheck(row.CheckJSON)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	return ReleaseSnapshot{
		ID: row.ID, ProjectID: row.ProjectID, FrozenInput: frozen,
		SnapshotDigest: row.SnapshotDigest, Check: check, IsCurrent: row.IsCurrent,
		// 读侧也归一，且不只是为了排序：两个实现的 DeepEqual 会因为
		// time.Location 指针不同而撒谎，.UTC() 让两边落在同一个 location 上。
		CreatedAt: row.CreatedAt.UTC(),
	}, nil
}

// storageInput 是 DeliveryInput 的存储形状。它存在的唯一理由：领域类型里有三个
// json:"-" 的字段——SnapshotChapter.SectionID 与 CheckIssue.Code/ChapterID——
// 整块 marshal 会把它们**静默**丢掉，而 Evaluate 正是按 chapter.SectionID 判
// 「模板必填章节齐了没有」。丢了的症状是：一份冻结时 passed 的快照读回来会多出
// required_section 阻断项，而没有任何一条按新输入算检查的用例会红。
//
// 严格说只有这三个字段需要另存（别处都跟领域类型同形），但那份「与主结构体逐字段
// 对应的影子结构体」正是 canonicalJSON 的注释里已经否掉的做法：主结构体新增字段时
// 影子不会跟着长，会**静默**漏掉那个字段。所以这里不追求逐字段对应，只追求
// 「新字段照抄一份」这件事发生在一处、且被往返测试盯着（sqlite_store_test.go）。
type storageInput struct {
	ProjectID      string            `json:"project_id"`
	ProjectName    string            `json:"project_name"`
	ProjectVersion int               `json:"project_version"`
	SpecRevision   int               `json:"spec_revision"`
	Spec           map[string]string `json:"spec"`
	Template       Template          `json:"template"`
	Chapters       []storageChapter  `json:"chapters"`
	Sources        []FrozenSource    `json:"sources"`
	AssetVersions  []AssetVersion    `json:"asset_versions"`
	PolicyAssetIDs []string          `json:"policy_asset_ids"`
	DeliveryKind   string            `json:"delivery_kind"`
}

// storageChapter 在外层补一个 SectionID。外层字段比嵌入的那一层浅，encoding/json
// 取的是它（嵌入类型里那个同名的是 json:"-"，本来也不参与）；读数时要把值写回
// 嵌入字段——SectionID 就藏在那里。
type storageChapter struct {
	SnapshotChapter
	SectionID string `json:"section_id"`
}

// storageCheck 与 storageIssue 同理：CheckIssue.Code 与 ChapterID 也是 json:"-"。
type storageCheck struct {
	ProjectVersion int            `json:"project_version"`
	RulesetHash    string         `json:"ruleset_hash"`
	Status         CheckStatus    `json:"status"`
	Issues         []storageIssue `json:"issues"`
}

type storageIssue struct {
	CheckIssue
	Code      string `json:"code"`
	ChapterID string `json:"chapter_id"`
}

func encodeFrozenInput(input DeliveryInput) (string, error) {
	stored := storageInput{
		ProjectID: input.ProjectID, ProjectName: input.ProjectName,
		ProjectVersion: input.ProjectVersion, SpecRevision: input.SpecRevision,
		Spec: input.Spec, Template: input.Template,
		Sources: input.Sources, AssetVersions: input.AssetVersions,
		PolicyAssetIDs: input.PolicyAssetIDs, DeliveryKind: input.DeliveryKind,
	}
	stored.Chapters = make([]storageChapter, 0, len(input.Chapters))
	for _, chapter := range input.Chapters {
		stored.Chapters = append(stored.Chapters, storageChapter{SnapshotChapter: chapter, SectionID: chapter.SectionID})
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode frozen input: %w", err)
	}
	return string(raw), nil
}

// decodeFrozenInput 读回来之后过一遍 cloneInput：它把「空集合」规整成 `[]` 而不是
// nil（契约里集合一律是数组，nil 切片编出来是 null），顺带交给调用方一份不与行结构
// 共享的副本。领域已经有一处定义这件事了，不再写第二份。
func decodeFrozenInput(raw string) (DeliveryInput, error) {
	var stored storageInput
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return DeliveryInput{}, fmt.Errorf("decode frozen input: %w", err)
	}
	input := DeliveryInput{
		ProjectID: stored.ProjectID, ProjectName: stored.ProjectName,
		ProjectVersion: stored.ProjectVersion, SpecRevision: stored.SpecRevision,
		Spec: stored.Spec, Template: stored.Template,
		Sources: stored.Sources, AssetVersions: stored.AssetVersions,
		PolicyAssetIDs: stored.PolicyAssetIDs, DeliveryKind: stored.DeliveryKind,
	}
	input.Chapters = make([]SnapshotChapter, 0, len(stored.Chapters))
	for _, chapter := range stored.Chapters {
		restored := chapter.SnapshotChapter
		restored.SectionID = chapter.SectionID
		input.Chapters = append(input.Chapters, restored)
	}
	return cloneInput(input), nil
}

func encodeCheck(check CheckResult) (string, error) {
	stored := storageCheck{ProjectVersion: check.ProjectVersion, RulesetHash: check.RulesetHash, Status: check.Status}
	stored.Issues = make([]storageIssue, 0, len(check.Issues))
	for _, issue := range check.Issues {
		stored.Issues = append(stored.Issues, storageIssue{CheckIssue: issue, Code: issue.Code, ChapterID: issue.ChapterID})
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode check result: %w", err)
	}
	return string(raw), nil
}

func decodeCheck(raw string) (CheckResult, error) {
	var stored storageCheck
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return CheckResult{}, fmt.Errorf("decode check result: %w", err)
	}
	check := CheckResult{ProjectVersion: stored.ProjectVersion, RulesetHash: stored.RulesetHash, Status: stored.Status}
	check.Issues = make([]CheckIssue, 0, len(stored.Issues))
	for _, issue := range stored.Issues {
		restored := issue.CheckIssue
		restored.Code, restored.ChapterID = issue.Code, issue.ChapterID
		check.Issues = append(check.Issues, restored)
	}
	// 与 decodeFrozenInput 同一条：issues 是数组，空集也不该落成 null。
	return CheckResult{ProjectVersion: check.ProjectVersion, RulesetHash: check.RulesetHash, Status: check.Status, Issues: cloneIssues(check.Issues)}, nil
}
