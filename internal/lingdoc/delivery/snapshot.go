package delivery

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSnapshotStaleInput = errors.New("delivery input is no longer current")
	// ErrSnapshotNotFound 让传输层把「这里没有这份快照」答成 404。取不到与读失败
	// 是两件事：前者是调用方写错了 ID，后者才是服务端出了问题。
	ErrSnapshotNotFound = errors.New("release snapshot not found")
	// ErrIdempotencyConflict 是一个动作键被换了请求复用。契约 §6：同键不同请求
	// 返回冲突，而不是当成新动作再做一次。
	ErrIdempotencyConflict = errors.New("idempotency key reused for a different request")
)

// DeliveryInput is the immutable value captured for checking and export.
type DeliveryInput struct {
	ProjectID      string            `json:"project_id"`
	ProjectName    string            `json:"project_name"`
	ProjectVersion int               `json:"project_version"`
	SpecRevision   int               `json:"spec_revision"`
	Spec           map[string]string `json:"spec"`
	Template       Template          `json:"template"`
	Chapters       []SnapshotChapter `json:"chapters"`
	Sources        []FrozenSource    `json:"sources"`
	AssetVersions  []AssetVersion    `json:"asset_versions"`
	PolicyAssetIDs []string          `json:"policy_asset_ids"`
	DeliveryKind   string            `json:"delivery_kind"`
}
type SnapshotChapter struct {
	ChapterID        string        `json:"chapter_id"`
	ChapterVersionID *string       `json:"chapter_version_id"`
	SectionID        string        `json:"-"`
	Title            string        `json:"title"`
	BodyMarkdown     string        `json:"body_markdown"`
	SourceIDs        []string      `json:"source_ids"`
	ReviewItems      []ReviewItem  `json:"review_items"`
	Confirmation     *Confirmation `json:"confirmation,omitempty"`
}
type ReviewItem struct {
	ID                string `json:"id"`
	Statement         string `json:"statement"`
	OriginCandidateID string `json:"origin_candidate_id"`
}
type ReviewDecision struct {
	ReviewItemID string `json:"review_item_id"`
	Disposition  string `json:"disposition"`
	Reason       string `json:"reason"`
}

// Confirmation freezes the T12 durable record, including the exact source asset versions.
type Confirmation struct {
	ID               string           `json:"id"`
	ChapterID        string           `json:"chapter_id"`
	ChapterVersionID string           `json:"chapter_version_id"`
	SpecRevision     int              `json:"spec_revision"`
	AssetVersions    []AssetVersion   `json:"asset_versions"`
	TemplateVersion  string           `json:"template_version"`
	ActorUserID      string           `json:"actor_user_id"`
	CreatedAt        time.Time        `json:"created_at"`
	Decisions        []ReviewDecision `json:"review_decisions"`
}
type FrozenSource struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	AssetID        string `json:"asset_id"`
	AssetRevision  int    `json:"asset_revision"`
	Locator        string `json:"locator"`
	QuotedText     string `json:"quoted_text"`
	QuotedTextHash string `json:"quoted_text_hash"`
	DisplayTitle   string `json:"display_title"`
}
type AssetVersion struct {
	AssetID  string `json:"asset_id"`
	Revision int    `json:"asset_revision"`
}
type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckBlocked CheckStatus = "blocked"
	// CheckNotEvaluated is the published status for a check that had no ruleset
	// to run. Reporting "passed" there would claim a check that never happened.
	CheckNotEvaluated CheckStatus = "not_evaluated"
)

// DeliveryKindInternalDemo is the only delivery kind this demo can release.
const DeliveryKindInternalDemo = "internal_demo"

// unattributedTarget covers a project-scoped finding that has no project ID to
// point at. ValidationIssue.target_id is published as non-empty, and an empty
// target would hide the defect from the caller.
const unattributedTarget = "unresolved-project"

type CheckResult struct {
	ProjectVersion int          `json:"project_version"`
	RulesetHash    string       `json:"ruleset_hash"`
	Status         CheckStatus  `json:"status"`
	Issues         []CheckIssue `json:"issues"`
}

// CheckIssue is the shared ValidationIssue shape. Code and ChapterID stay internal for tests.
type CheckIssue struct {
	ID            string  `json:"id"`
	RuleID        string  `json:"rule_id"`
	RulesetHash   string  `json:"ruleset_hash"`
	Severity      string  `json:"severity"`
	TargetID      string  `json:"target_id"`
	TargetVersion *string `json:"target_version"`
	Message       string  `json:"message"`
	Code          string  `json:"-"`
	ChapterID     string  `json:"-"`
}
type ReleaseSnapshot struct {
	ID             string        `json:"id"`
	ProjectID      string        `json:"project_id"`
	FrozenInput    DeliveryInput `json:"frozen_input"`
	SnapshotDigest string        `json:"snapshot_digest"`
	Check          CheckResult   `json:"check"`
	IsCurrent      bool          `json:"is_current"`
	CreatedAt      time.Time     `json:"created_at"`
}

type SnapshotStore interface {
	Save(ReleaseSnapshot) error
	Get(projectID, snapshotID string) (ReleaseSnapshot, error)
}

// FreezeAttempt 是「哪一次用户动作要求了这次冻结」。契约 §6 记的范围是租户、
// 操作者、操作、目标资源路径与键；目标路径就是这个项目，操作由路由定死
// （只有 prepareRelease 冻结），所以动作身份落成这三项。
type FreezeAttempt struct {
	ActorID   string
	ProjectID string
	Key       string
}

// FreezeRecorder 是 SnapshotStore 的可选能力：把一份冻结记到要求它的那次动作名下。
//
// 两个方法各司其职，都不是多余的：ReplayFreeze 是**读**，它必须能在版本比较之前
// 跑（契约 §6：「重放完成结果必须先于首次写入的旧版本比较」，否则一次
// 「已经冻好了但响应丢了」的重试会被误报成版本冲突）；RecordFreeze 是**写**，
// 它把「这份快照」与「这次动作冻了它」放在同一次写入里，让并发重试只有一个能落笔。
type FreezeRecorder interface {
	// ReplayFreeze 返回这次动作已经冻出来的那一份。found=false 表示这是个新动作；
	// 记下的请求指纹与 requestHash 不符则是键被复用，返回 ErrIdempotencyConflict。
	ReplayFreeze(attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error)
	// RecordFreeze 把快照与它的动作一起落存，并回报**这次调用是不是落笔的那一次**。
	// 并发下输的一方拿回赢家的那一份（replayed=true），而不是另冻一份：
	// 一次用户动作在交付历史里只能有一条。
	RecordFreeze(snapshot ReleaseSnapshot, attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error)
}

// freezeRecord 是一次动作冻出来的东西。请求指纹用来把「重试」与「换了请求却
// 复用同一个键」分开——契约 §6 要求对前者换回原结果、对后者报冲突。
type freezeRecord struct {
	SnapshotID  string
	RequestHash string
}

type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string]ReleaseSnapshot
	freezes   map[string]freezeRecord
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		snapshots: make(map[string]ReleaseSnapshot),
		freezes:   make(map[string]freezeRecord),
	}
}

func snapshotIndex(projectID, snapshotID string) string { return projectID + "/" + snapshotID }

func freezeIndex(attempt FreezeAttempt) string {
	return attempt.ProjectID + "/" + attempt.ActorID + "/" + attempt.Key
}

func (s *MemorySnapshotStore) Save(snapshot ReleaseSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snapshotIndex(snapshot.ProjectID, snapshot.ID)] = cloneSnapshot(snapshot)
	return nil
}
func (s *MemorySnapshotStore) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.snapshots[snapshotIndex(projectID, snapshotID)]
	if !ok {
		return ReleaseSnapshot{}, fmt.Errorf("%w: %s", ErrSnapshotNotFound, snapshotID)
	}
	return cloneSnapshot(snapshot), nil
}

func (s *MemorySnapshotStore) ReplayFreeze(attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.freezes[freezeIndex(attempt)]
	if !ok {
		return ReleaseSnapshot{}, false, nil
	}
	return s.replayed(attempt.ProjectID, record, requestHash)
}

func (s *MemorySnapshotStore) RecordFreeze(snapshot ReleaseSnapshot, attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := freezeIndex(attempt)
	if record, ok := s.freezes[index]; ok {
		return s.replayed(attempt.ProjectID, record, requestHash)
	}
	stored := cloneSnapshot(snapshot)
	s.snapshots[snapshotIndex(snapshot.ProjectID, snapshot.ID)] = stored
	s.freezes[index] = freezeRecord{SnapshotID: snapshot.ID, RequestHash: requestHash}
	return cloneSnapshot(stored), false, nil
}

// replayed 由调用方持锁调用。索引与快照永远在同一次写入里成对落下，所以
// 「索引在、快照不在」只可能来自别的实现——那种情况下宁可说找不到，
// 也不要交出一份谁也读不回来的快照。
func (s *MemorySnapshotStore) replayed(projectID string, record freezeRecord, requestHash string) (ReleaseSnapshot, bool, error) {
	if record.RequestHash != requestHash {
		return ReleaseSnapshot{}, false, ErrIdempotencyConflict
	}
	snapshot, ok := s.snapshots[snapshotIndex(projectID, record.SnapshotID)]
	if !ok {
		return ReleaseSnapshot{}, false, ErrSnapshotNotFound
	}
	return cloneSnapshot(snapshot), true, nil
}

// CurrentnessChecker compares frozen versions with current workspace and source state.
type CurrentnessChecker interface {
	IsCurrent(DeliveryInput) (bool, error)
}
type CurrentnessFunc func(DeliveryInput) (bool, error)

func (f CurrentnessFunc) IsCurrent(input DeliveryInput) (bool, error) { return f(input) }

type alwaysCurrent struct{}

func (alwaysCurrent) IsCurrent(DeliveryInput) (bool, error) { return true, nil }

type ReleaseService struct {
	store       SnapshotStore
	currentness CurrentnessChecker
	now         func() time.Time
}

func NewReleaseService(store SnapshotStore, checkers ...CurrentnessChecker) *ReleaseService {
	checker := CurrentnessChecker(alwaysCurrent{})
	if len(checkers) > 0 && checkers[0] != nil {
		checker = checkers[0]
	}
	return &ReleaseService{store: store, currentness: checker, now: time.Now}
}

// Freeze 算出冻结快照，但不落存。
//
// 分开是因为冻结与「哪一次动作冻了它」必须一起落：由调用方拿着这份快照去
// RecordFreeze，两件事才是同一次写入。先存后记的话，两次写之间就存在一个
// 「快照在、动作不在」的窗口，重试正好落在那里就会再冻一份。
func (s *ReleaseService) Freeze(input DeliveryInput) (ReleaseSnapshot, error) {
	frozen := cloneInput(input)
	current, err := s.currentness.IsCurrent(frozen)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	if !current {
		return ReleaseSnapshot{}, ErrSnapshotStaleInput
	}
	digest, err := digestFrozenInput(frozen)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	id, err := opaqueID("snapshot")
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	return ReleaseSnapshot{ID: id, ProjectID: frozen.ProjectID, FrozenInput: frozen, SnapshotDigest: digest, Check: Evaluate(frozen), IsCurrent: true, CreatedAt: s.now().UTC()}, nil
}

func (s *ReleaseService) Prepare(input DeliveryInput) (ReleaseSnapshot, error) {
	snapshot, err := s.Freeze(input)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	if err := s.store.Save(snapshot); err != nil {
		return ReleaseSnapshot{}, err
	}
	return cloneSnapshot(snapshot), nil
}

// Get recalculates a dynamic flag without mutating frozen history or digest.
func (s *ReleaseService) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	snapshot, err := s.store.Get(projectID, snapshotID)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	current, err := s.currentness.IsCurrent(snapshot.FrozenInput)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	snapshot.IsCurrent = current
	return snapshot, nil
}

// issueFunc reports one finding against the frozen delivery input. ruleID must
// name a rule of the frozen template; the internal code labels the diagnostic
// for tests and never reaches the wire.
type issueFunc func(ruleID, code, chapterID, message string)

func Evaluate(input DeliveryInput) CheckResult {
	result := CheckResult{ProjectVersion: input.ProjectVersion, RulesetHash: input.Template.RulesetHash, Status: CheckPassed, Issues: []CheckIssue{}}
	severities := ruleSeverities(input.Template.Rules)
	if len(severities) == 0 {
		result.Status = CheckNotEvaluated
		return result
	}
	versions := chapterVersionIDs(input.Chapters)
	add := func(severity, ruleID, code, chapterID, message string) {
		target, targetVersion := issueTarget(input, versions, chapterID)
		result.Issues = append(result.Issues, CheckIssue{ID: fmt.Sprintf("%s-%s-%d", code, target, len(result.Issues)+1), RuleID: ruleID, RulesetHash: input.Template.RulesetHash, Severity: severity, TargetID: target, TargetVersion: targetVersion, Message: message, Code: code, ChapterID: chapterID})
		if severity == SeverityBlocking {
			result.Status = CheckBlocked
		}
	}
	issue := func(ruleID, code, chapterID, message string) {
		add(ruleSeverity(severities, ruleID), ruleID, code, chapterID, message)
	}
	// advisory reports an acknowledged finding: the rule is satisfied, but the
	// result must still carry the item forward to the exported file.
	advisory := func(ruleID, code, chapterID, message string) {
		add(SeverityWarning, ruleID, code, chapterID, message)
	}

	if input.ProjectID == "" || input.DeliveryKind != DeliveryKindInternalDemo {
		issue(RuleRequiredFields, "invalid_delivery_input", "", "project_id 与交付类型 internal_demo 均为必填")
	}
	for _, field := range input.Template.RequiredFields {
		if strings.TrimSpace(input.Spec[field]) == "" {
			issue(RuleRequiredFields, "required_field_missing", "", "缺少必填研究条件："+field)
		}
	}
	validateFrozenCollections(input, issue)
	chapters := map[string]SnapshotChapter{}
	for _, chapter := range input.Chapters {
		if chapter.SectionID == "" || chapters[chapter.SectionID].SectionID != "" {
			issue(RuleChapterNonempty, "invalid_chapter_section", chapter.ChapterID, "章节必须对应唯一的模板章节")
		}
		chapters[chapter.SectionID] = chapter
		checkChapter(chapter, input, issue, advisory)
	}
	for _, section := range input.Template.Sections {
		if section.Required && chapters[section.ID].SectionID == "" {
			issue(RuleRequiredFields, "required_chapter_missing", "", "缺少必填模板章节："+section.ID)
		}
	}
	return result
}

func ruleSeverities(rules []Rule) map[string]string {
	out := make(map[string]string, len(rules))
	for _, rule := range rules {
		out[rule.ID] = rule.Severity
	}
	return out
}

// ruleSeverity keeps an undeclared rule blocking: a rule the frozen template
// does not describe must never be silently downgraded.
func ruleSeverity(severities map[string]string, ruleID string) string {
	if severity, ok := severities[ruleID]; ok && severity != "" {
		return severity
	}
	return SeverityBlocking
}

// chapterVersionIDs indexes the immutable version a chapter-scoped finding must
// cite, so ValidationIssue.target_version can pin the exact reviewed version.
func chapterVersionIDs(chapters []SnapshotChapter) map[string]*string {
	out := make(map[string]*string, len(chapters))
	for _, chapter := range chapters {
		if chapter.ChapterID == "" || chapter.ChapterVersionID == nil {
			continue
		}
		out[chapter.ChapterID] = cloneVersion(chapter.ChapterVersionID)
	}
	return out
}

// issueTarget names what the caller must act on. Project-scoped findings point
// at the project; chapter-scoped findings point at the chapter and, when one
// exists, at the exact version the finding is about.
func issueTarget(input DeliveryInput, versions map[string]*string, chapterID string) (string, *string) {
	if chapterID == "" {
		if input.ProjectID != "" {
			return input.ProjectID, nil
		}
		return unattributedTarget, nil
	}
	return chapterID, cloneVersion(versions[chapterID])
}

func validateFrozenCollections(input DeliveryInput, issue issueFunc) {
	sources := map[string]bool{}
	for _, source := range input.Sources {
		if source.ID == "" || sources[source.ID] || source.ProjectID == "" || source.AssetID == "" || source.AssetRevision < 1 || source.Locator == "" || source.DisplayTitle == "" {
			issue(RuleSourceAvailable, "invalid_frozen_source", "", "冻结来源必须具有唯一 ID 与完整溯源信息")
		}
		sources[source.ID] = true
	}
	assets := map[string]bool{}
	for _, asset := range input.AssetVersions {
		if asset.AssetID == "" || asset.Revision < 1 || assets[asset.AssetID] {
			issue(RuleSourceAvailable, "invalid_asset_versions", "", "冻结资料版本必须具有唯一 asset_id 与正整数 revision")
		}
		assets[asset.AssetID] = true
	}
	policy := map[string]bool{}
	for _, id := range input.PolicyAssetIDs {
		if id == "" || policy[id] {
			issue(RuleSourceAvailable, "invalid_policy_assets", "", "授权资料 ID 不得为空且必须唯一")
		}
		policy[id] = true
	}
}
func checkChapter(chapter SnapshotChapter, input DeliveryInput, issue, advisory issueFunc) {
	if chapter.ChapterVersionID == nil {
		issue(RuleChapterNonempty, "chapter_version_missing", chapter.ChapterID, "章节尚无不可变版本")
		return
	}
	if strings.TrimSpace(chapter.BodyMarkdown) == "" {
		issue(RuleChapterNonempty, "chapter_empty", chapter.ChapterID, "章节正文为空")
	}
	ids := map[string]bool{}
	for _, id := range chapter.SourceIDs {
		if id == "" || ids[id] {
			issue(RuleSourceAvailable, "invalid_source_ids", chapter.ChapterID, "来源 ID 不得为空且必须唯一")
			break
		}
		ids[id] = true
	}
	if !sameStringSet(ids, citationIDs(chapter.BodyMarkdown)) {
		issue(RuleSourceAvailable, "citation_mismatch", chapter.ChapterID, "正文引用标记必须与 source_ids 完全一致")
	}
	sources, assets, allowed := sourcesByID(input.Sources), assetVersions(input.AssetVersions), stringSet(input.PolicyAssetIDs)
	expectedAssets := map[string]int{}
	for id := range ids {
		source, ok := sources[id]
		if !ok || source.ProjectID != input.ProjectID || !allowed[source.AssetID] || assets[source.AssetID] != source.AssetRevision || sha256Text(source.QuotedText) != source.QuotedTextHash {
			issue(RuleSourceAvailable, "source_invalid", chapter.ChapterID, "来源不是完整、冻结且已授权的记录："+id)
			continue
		}
		expectedAssets[source.AssetID] = source.AssetRevision
	}
	confirmation := chapter.Confirmation
	if confirmation == nil || confirmation.ID == "" || confirmation.ChapterID != chapter.ChapterID || confirmation.ChapterVersionID != *chapter.ChapterVersionID || confirmation.SpecRevision != input.SpecRevision || confirmation.TemplateVersion != input.Template.Version || confirmation.ActorUserID == "" || confirmation.CreatedAt.IsZero() {
		issue(RuleChapterConfirmed, "confirmation_stale", chapter.ChapterID, "确认记录与冻结章节、研究条件及模板不一致")
		return
	}
	checkDecisions(chapter, issue, advisory)
	if !sameAssetVersions(confirmation.AssetVersions, expectedAssets) {
		issue(RuleChapterConfirmed, "confirmation_asset_versions_mismatch", chapter.ChapterID, "确认记录的资料版本必须与章节冻结来源完全一致")
	}
}
func checkDecisions(chapter SnapshotChapter, issue, advisory issueFunc) {
	items, decisions := map[string]bool{}, map[string]bool{}
	for _, item := range chapter.ReviewItems {
		if item.ID == "" || item.Statement == "" || item.OriginCandidateID == "" || items[item.ID] {
			issue(RuleReviewItemsDecided, "review_items_invalid", chapter.ChapterID, "待核项必须具备唯一且完整的 ID")
			return
		}
		items[item.ID] = true
	}
	retained := make([]string, 0, len(chapter.Confirmation.Decisions))
	for _, decision := range chapter.Confirmation.Decisions {
		if decisions[decision.ReviewItemID] || !items[decision.ReviewItemID] || (decision.Disposition != DispositionResolved && decision.Disposition != DispositionRetainedWarning) || strings.TrimSpace(decision.Reason) == "" {
			issue(RuleReviewItemsDecided, "review_decisions_invalid", chapter.ChapterID, "待核项处置必须逐项覆盖且给出理由")
			return
		}
		decisions[decision.ReviewItemID] = true
		if decision.Disposition == DispositionRetainedWarning {
			retained = append(retained, decision.ReviewItemID)
		}
	}
	if len(items) != len(decisions) {
		issue(RuleReviewItemsDecided, "review_decisions_incomplete", chapter.ChapterID, "每个待核项都需要一条处置记录")
		return
	}
	// A retained item does not block: it is reported so the caller keeps it in
	// the released file instead of dropping it silently.
	for _, id := range retained {
		advisory(RuleReviewItemsDecided, "retained_review_item", chapter.ChapterID, "存在经负责人选择保留的待核项，须随内部演示文件呈现："+id)
	}
}

// digestFrozenInput 按契约发布的那套规范形式取摘要。发布的那对
// contracts/frozen-input.canonical.json 与 .sha256 就是它的输出，prepareRelease
// 样例里的 snapshot_digest 就是这一枚：重算得到同一个值，那对文件才有核对的意义，
// 否则消费者拿到快照无从判断它是不是自己以为的那一份。
//
// 值先过一遍 cloneInput，因为摘要只该取决于内容：契约里「没有来源」写作 []，
// 而 Go 里同一件事的自然写法是 nil 切片，编出来是 null。两者会落成两枚摘要，
// 可它们在契约里是同一个值。工作区侧那些真会是空的集合（引用、待核项、资料版本、
// 研究条件）都由 cloneInput 一并规整，走它就不会漏。
func digestFrozenInput(input DeliveryInput) (string, error) {
	data, err := canonicalJSON(cloneInput(input))
	if err != nil {
		return "", fmt.Errorf("canonicalize frozen input: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON 把值编成契约发布的规范形式。判据是发布侧那台生成器，
// contracts/validate_artifacts.py 的 canon()：
//
//	json.dumps(x, ensure_ascii=False, sort_keys=True, separators=(',',':'))
//
// 即对象键**递归按字母序**、紧凑分隔符、非 ASCII 原样输出。
//
// 做法是先按 Go 的规则编一次，拿到一个合法的 JSON 值，再解成通用结构重编一次：
// 第二轮里对象是 map[string]any，encoding/json 对 map 的键一律排序，键序于是只
// 取决于内容，与字段在 Go 里怎么排无关。
//
// 早先这里另立了一组与主结构体逐字段对应的影子结构体，想靠书写顺序复刻契约的
// 字段序。那既是对 sort_keys 的误读（排的是字母序，不是某份文档里的出现顺序），
// 又是个复制品：主结构体新增字段时影子不会跟着长，摘要会**静默**漏掉那个字段。
func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// 解成 json.Number 而不是 float64：float64 会改掉大整数与超过 17 位的精度，
	// 而摘要只该取决于内容，不该取决于数字多大。
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	// 发布侧 ensure_ascii=False 的意思是不转义非 ASCII，它也从不动 < > &；
	// Go 默认的 HTML 转义会把它们写成 < 之类。两边不是同一套字节，
	// 就永远对不上同一枚摘要。
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(generic); err != nil {
		return nil, err
	}
	// Encode 会补一个换行，canon() 没有。
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

func cloneInput(input DeliveryInput) DeliveryInput {
	copy := input
	copy.Spec = map[string]string{}
	for key, value := range input.Spec {
		copy.Spec[key] = value
	}
	copy.Template = cloneTemplate(input.Template)
	copy.Chapters = make([]SnapshotChapter, len(input.Chapters))
	for i, chapter := range input.Chapters {
		copy.Chapters[i] = cloneChapter(chapter)
	}
	copy.Sources = cloneSlice(input.Sources)
	copy.AssetVersions = cloneSlice(input.AssetVersions)
	copy.PolicyAssetIDs = cloneSlice(input.PolicyAssetIDs)
	return copy
}
func cloneChapter(chapter SnapshotChapter) SnapshotChapter {
	copy := chapter
	copy.ChapterVersionID = cloneVersion(chapter.ChapterVersionID)
	copy.SourceIDs = cloneSlice(chapter.SourceIDs)
	copy.ReviewItems = cloneSlice(chapter.ReviewItems)
	if chapter.Confirmation != nil {
		confirmation := *chapter.Confirmation
		confirmation.AssetVersions = cloneSlice(chapter.Confirmation.AssetVersions)
		confirmation.Decisions = cloneSlice(chapter.Confirmation.Decisions)
		copy.Confirmation = &confirmation
	}
	return copy
}

// cloneSlice 复制一份切片，并让「空」落成 []T{} 而不是 nil。契约里集合一律是
// 数组（[] 与 null 是两个不同的值），而 nil 切片编出来正是 null——空集在
// 契约里只有一种写法，服务端产出的那份也得是它。
func cloneSlice[T any](values []T) []T {
	out := make([]T, len(values))
	copy(out, values)
	return out
}
func cloneSnapshot(snapshot ReleaseSnapshot) ReleaseSnapshot {
	snapshot.FrozenInput = cloneInput(snapshot.FrozenInput)
	snapshot.Check.Issues = cloneIssues(snapshot.Check.Issues)
	return snapshot
}

// cloneIssues keeps a saved snapshot from sharing issue targets with the copy
// handed to the caller, and keeps "issues" an empty array rather than null so
// every served CheckResult matches the published schema.
func cloneIssues(issues []CheckIssue) []CheckIssue {
	out := make([]CheckIssue, len(issues))
	for i, issue := range issues {
		out[i] = issue
		out[i].TargetVersion = cloneVersion(issue.TargetVersion)
	}
	return out
}
func cloneVersion(version *string) *string {
	if version == nil {
		return nil
	}
	value := *version
	return &value
}
func opaqueID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}
func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
func sourcesByID(values []FrozenSource) map[string]FrozenSource {
	out := map[string]FrozenSource{}
	for _, value := range values {
		out[value.ID] = value
	}
	return out
}
func assetVersions(values []AssetVersion) map[string]int {
	out := map[string]int{}
	for _, value := range values {
		out[value.AssetID] = value.Revision
	}
	return out
}
func sameStringSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if !right[value] {
			return false
		}
	}
	return true
}
func sameAssetVersions(actual []AssetVersion, expected map[string]int) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range actual {
		if value.AssetID == "" || value.Revision < 1 || seen[value.AssetID] || expected[value.AssetID] != value.Revision {
			return false
		}
		seen[value.AssetID] = true
	}
	return true
}
func citationIDs(body string) map[string]bool {
	out := map[string]bool{}
	for remaining := body; ; {
		start := strings.Index(remaining, "[[source:")
		if start < 0 {
			return out
		}
		remaining = remaining[start+9:]
		end := strings.Index(remaining, "]]")
		if end < 0 {
			return map[string]bool{"__invalid__": true}
		}
		id := remaining[:end]
		if id == "" {
			return map[string]bool{"__invalid__": true}
		}
		out[id] = true
		remaining = remaining[end+2:]
	}
}
func sortedIssueCodes(issues []CheckIssue) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = issue.Code
	}
	sort.Strings(out)
	return out
}
