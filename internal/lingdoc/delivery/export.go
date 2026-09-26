package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrExportPreflightBlocked means the snapshot is a deliberately saved
	// blocked check result. Callers must show its issues instead of offering a
	// fake download.
	ErrExportPreflightBlocked = errors.New("release snapshot is blocked")
	// ErrExportStaleInput prevents a new export from being created from a
	// snapshot that no longer represents the current project input.
	ErrExportStaleInput = errors.New("release snapshot is no longer current")
	// ErrExportUnavailable is returned for failed or unknown artifacts.
	ErrExportUnavailable = errors.New("export file is not available")
	// ErrExportNotFound 让传输层把「这里没有这个导出」答成 404。与
	// ErrSnapshotNotFound 同一条：取不到是调用方写错了 ID，读失败才是服务端的问题。
	ErrExportNotFound = errors.New("export artifact not found")
	// ErrInvalidRequest 是交付包自己的入参判定。它与 workspace、candidateadoption
	// 里同名的 sentinel 是**不同的值**，传输层必须逐个列出——漏一个就是把 400
	// 答成 500。口径统一在传输层做，领域层不为了对上 HTTP 而改自己的错误。
	ErrInvalidRequest = errors.New("invalid export request")
)

type ExportStatus string

const (
	ExportVerified ExportStatus = "verified"
	ExportFailed   ExportStatus = "failed"
)

// 失败原因。render_failed 是渲染器自己报错；validation_failed 是文件渲染出来了
// 但过不了 §7 的文件校验（结构、正文顺序、关键数字、引用、待核附录）。两者恢复
// 动作不同——前者换个新动作重渲一次，后者要改输入——所以不并成一个码。
//
// 导出是因为传输层要按它决定响应里的 message 与 retryable。让它抄一遍字面量，
// 就等于把「哪些码存在」这件事变成两处各说各话。
const (
	FailureRenderFailed     = "render_failed"
	FailureEmptyFile        = "empty_file"
	FailureValidationFailed = "validation_failed"
)

// ExportArtifact records the outcome without exposing any bytes for failed
// output. Authorization on download belongs to the caller's current access
// check; this small service enforces the independent verified-file boundary.
type ExportArtifact struct {
	ID          string       `json:"id"`
	ProjectID   string       `json:"project_id"`
	SnapshotID  string       `json:"snapshot_id"`
	Status      ExportStatus `json:"status"`
	FileSHA256  string       `json:"file_sha256,omitempty"`
	FailureCode string       `json:"failure_code,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	file        []byte
}

// FrozenRenderer is supplied by the T05 DOCX adapter. It receives only the
// saved input, never mutable chapter or source storage.
type FrozenRenderer interface {
	RenderFrozen(DeliveryInput) ([]byte, error)
}

// FrozenValidator 是 Downloads 之前的最后一道闸：§7 要求校验结构、正文顺序、
// 关键数字、引用与待核附录，「通过后才提供下载」。它拿到的是渲染器刚产出的字节，
// 而不是渲染器的自述——渲染器说成功不等于文件里有该有的东西（F13 正是这个形状）。
type FrozenValidator interface {
	ValidateFrozen(DeliveryInput, []byte) error
}

type FrozenValidatorFunc func(DeliveryInput, []byte) error

func (f FrozenValidatorFunc) ValidateFrozen(input DeliveryInput, file []byte) error {
	return f(input, file)
}

type ExportStore interface {
	SaveExport(ExportArtifact) error
	GetExport(projectID, exportID string) (ExportArtifact, error)
}

// ExportAttempt 是「哪一次用户动作要求了这次导出」。契约把 Idempotency-Key 标成
// /exports 的必填头，记录范围是租户、操作者、操作、目标资源路径与键；操作由路由
// 定死（只有 startExport 产生产物），目标路径就是这个项目，所以动作身份落成这三项。
type ExportAttempt struct {
	ActorID   string
	ProjectID string
	Key       string
}

// ExportRecorder 是 ExportStore 的可选能力：把一份产物记到要求它的那次动作名下。
//
// 与 FreezeRecorder 同构，理由也逐字相同：产物与「哪一次动作产生的」必须**一起**
// 落。分开写就有个窗口——产物已经在库里、动作还没记上，此时的重试会当作新动作
// 再渲一份，一次用户动作于是在交付历史里变成两条。
//
// 失败产物同样入账。§6 说「重试新任务是明确的新动作」，所以重试要换新键；同一个键
// 于是永远换回同一个结果，包括失败。契约给 render_failed 标的 retryable:true 指的
// 是「可以另起一个新动作」，不是「拿旧键重放会变成成功」。
type ExportRecorder interface {
	// ReplayExport 返回这次动作已经产出的那一份。found=false 表示这是个新动作；
	// 记下的请求指纹与 requestHash 不符则是键被复用，返回 ErrIdempotencyConflict。
	ReplayExport(attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error)
	// RecordExport 把产物与它的动作一起落存，并回报**这次调用是不是落笔的那一次**。
	// 并发下输的一方拿回赢家的那一份（replayed=true），而不是另存一份。
	RecordExport(artifact ExportArtifact, attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error)
}

// ExportLister 是 ExportStore 的可选能力：列出项目下导出过的产物。理由与
// SnapshotLister 逐字相同——交付页问的是「这个项目导出过什么」，不是
// 「哪一次动作产出了它」。
type ExportLister interface {
	// ListExports 返回该项目下的产物，新的在前。项目没有产物时返回空切片而不是
	// nil——调用方要区分「没有导出过」与「读失败」。
	ListExports(projectID string) ([]ExportArtifact, error)
}

// ExportAccessChecker enforces the caller's present project access at both
// export creation and download time. A previous successful export must not
// become a back door after membership is revoked.
type ExportAccessChecker interface {
	Authorize(actorUserID, projectID string) error
}

type ExportAccessFunc func(actorUserID, projectID string) error

func (f ExportAccessFunc) Authorize(actorUserID, projectID string) error {
	return f(actorUserID, projectID)
}

// exportRecord 是一次动作产出的东西。请求指纹用来把「重试」与「换了请求却复用
// 同一个键」分开。
type exportRecord struct {
	ExportID    string
	RequestHash string
}

type MemoryExportStore struct {
	mu      sync.RWMutex
	exports map[string]ExportArtifact
	actions map[string]exportRecord
}

func NewMemoryExportStore() *MemoryExportStore {
	return &MemoryExportStore{
		exports: make(map[string]ExportArtifact),
		actions: make(map[string]exportRecord),
	}
}

func exportIndex(projectID, exportID string) string { return projectID + "/" + exportID }

func exportActionIndex(attempt ExportAttempt) string {
	return attempt.ProjectID + "/" + attempt.ActorID + "/" + attempt.Key
}

func (s *MemoryExportStore) SaveExport(artifact ExportArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports[exportIndex(artifact.ProjectID, artifact.ID)] = cloneExport(artifact)
	return nil
}

func (s *MemoryExportStore) GetExport(projectID, exportID string) (ExportArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stored(projectID, exportID)
}

func (s *MemoryExportStore) ListExports(projectID string) ([]ExportArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := make([]ExportArtifact, 0, len(s.exports))
	for _, artifact := range s.exports {
		if artifact.ProjectID == projectID {
			matches = append(matches, cloneExport(artifact))
		}
	}
	// 新的在前，与 ListSnapshots 同一条：同一时刻产出的两份按 ID 降序定序，
	// 免得顺序随 map 遍历次序漂，也免得与落库实现的
	// `ORDER BY created_at DESC, id DESC` 排出两个样子。
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].CreatedAt.After(matches[j].CreatedAt)
		}
		return matches[i].ID > matches[j].ID
	})
	return matches, nil
}

func (s *MemoryExportStore) ReplayExport(attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.actions[exportActionIndex(attempt)]
	if !ok {
		return ExportArtifact{}, false, nil
	}
	return s.replayed(attempt.ProjectID, record, requestHash)
}

func (s *MemoryExportStore) RecordExport(artifact ExportArtifact, attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := exportActionIndex(attempt)
	if record, ok := s.actions[index]; ok {
		return s.replayed(attempt.ProjectID, record, requestHash)
	}
	stored := cloneExport(artifact)
	s.exports[exportIndex(artifact.ProjectID, artifact.ID)] = stored
	s.actions[index] = exportRecord{ExportID: artifact.ID, RequestHash: requestHash}
	return cloneExport(stored), false, nil
}

// replayed 由调用方持锁调用。索引与产物永远在同一次写入里成对落下，所以
// 「索引在、产物不在」只可能来自别的实现——那种情况下宁可说找不到，
// 也不要交出一份谁也读不回来的产物。
func (s *MemoryExportStore) replayed(projectID string, record exportRecord, requestHash string) (ExportArtifact, bool, error) {
	if record.RequestHash != requestHash {
		return ExportArtifact{}, false, ErrIdempotencyConflict
	}
	artifact, err := s.stored(projectID, record.ExportID)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	return artifact, true, nil
}

// stored 由调用方持锁调用。
func (s *MemoryExportStore) stored(projectID, exportID string) (ExportArtifact, error) {
	artifact, ok := s.exports[exportIndex(projectID, exportID)]
	if !ok {
		return ExportArtifact{}, fmt.Errorf("%w: %s", ErrExportNotFound, exportID)
	}
	return cloneExport(artifact), nil
}

type ExportService struct {
	snapshots   SnapshotStore
	exports     ExportStore
	actions     ExportRecorder
	renderer    FrozenRenderer
	validator   FrozenValidator
	currentness CurrentnessChecker
	access      ExportAccessChecker
	now         func() time.Time
}

// NewExportService 依赖不齐时返回 nil。五个依赖各自都是必需的：
//
//   - renderer 与 validator 分别是「产出文件」与「证明文件里确实有该有的东西」，
//     少任何一个都等于把下载入口架在没有证据的字节上；
//   - 产物库还必须能记「哪一次动作产出了它」（ExportRecorder）。契约把
//     Idempotency-Key 标成 /exports 的必填头，退化成「照渲不误」就等于收下了这个头
//     却不当回事：一次超时重试会在交付历史里多出一条，而调用方以为自己只导出过一次。
func NewExportService(snapshots SnapshotStore, exports ExportStore, renderer FrozenRenderer, validator FrozenValidator, currentness CurrentnessChecker, access ExportAccessChecker) *ExportService {
	if snapshots == nil || exports == nil || renderer == nil || validator == nil || currentness == nil || access == nil {
		return nil
	}
	actions, ok := exports.(ExportRecorder)
	if !ok {
		return nil
	}
	return &ExportService{
		snapshots: snapshots, exports: exports, actions: actions,
		renderer: renderer, validator: validator, currentness: currentness, access: access,
		now: time.Now,
	}
}

// Start renders a frozen, passing, current snapshot. Renderer errors, empty files
// and validation failures all become a persisted failed artifact so the UI can
// show a recovery state; none of them ever creates a downloadable file.
//
// key 是这次用户动作的幂等键。处理顺序照契约 §6：先判身份，再判是不是已经做过，
// 最后才比版本、才读工作区。撤权之后不能从「这个键做过」里把旧产物交出去。
func (s *ExportService) Start(actorUserID, projectID, snapshotID, key string) (ExportArtifact, bool, error) {
	if s == nil || s.actions == nil {
		return ExportArtifact{}, false, ErrExportUnavailable
	}
	if strings.TrimSpace(actorUserID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(snapshotID) == "" {
		return ExportArtifact{}, false, fmt.Errorf("%w: export requires actor, project and snapshot", ErrInvalidRequest)
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 128 {
		return ExportArtifact{}, false, fmt.Errorf("%w: idempotency key must be 8..128 characters", ErrInvalidRequest)
	}
	// 1. 身份与项目能力。
	if err := s.authorize(actorUserID, projectID); err != nil {
		return ExportArtifact{}, false, err
	}
	attempt := ExportAttempt{ActorID: actorUserID, ProjectID: projectID, Key: key}
	requestHash := exportRequestHash(actorUserID, projectID, snapshotID)
	// 2. 已经做过就换回原结果；同键不同请求是冲突，不是又一次导出。
	replay, found, err := s.actions.ReplayExport(attempt, requestHash)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	if found {
		return replay, true, nil
	}
	// 3. 只有新动作才读快照、才比版本、才渲染。
	artifact, err := s.produce(actorUserID, projectID, snapshotID)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	recorded, replayed, err := s.actions.RecordExport(artifact, attempt, requestHash)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	return recorded, replayed, nil
}

// produce 是一次新动作的全部工作。失败不返回 error 而是返回一份 failed 产物：
// 未通过前置检查才是错误，做过了但没做成是**一种结果**，界面要能显示它并给出
// 恢复动作。currentness 出错仍是 error——那是服务端读不到状态，不是文件的事。
func (s *ExportService) produce(actorUserID, projectID, snapshotID string) (ExportArtifact, error) {
	snapshot, err := s.snapshots.Get(projectID, snapshotID)
	if err != nil {
		return ExportArtifact{}, err
	}
	if snapshot.Check.Status != CheckPassed {
		return ExportArtifact{}, ErrExportPreflightBlocked
	}
	current, err := s.isCurrent(snapshot.FrozenInput)
	if err != nil {
		return ExportArtifact{}, err
	}
	if !snapshot.IsCurrent || !current {
		return ExportArtifact{}, ErrExportStaleInput
	}
	id, err := opaqueID("export")
	if err != nil {
		return ExportArtifact{}, err
	}
	artifact := ExportArtifact{ID: id, ProjectID: projectID, SnapshotID: snapshotID, CreatedAt: s.now().UTC()}

	data, failure := s.render(snapshot.FrozenInput)
	if failure != "" {
		artifact.Status, artifact.FailureCode = ExportFailed, failure
		return artifact, nil
	}
	// 渲染之后内容可能已经变了（源被撤权、章节被改）。校验读的是刚生成的字节，
	// 所以放在这两次判断之间：既晚于渲染，又早于落存。
	if err := s.validator.ValidateFrozen(snapshot.FrozenInput, data); err != nil {
		artifact.Status, artifact.FailureCode = ExportFailed, FailureValidationFailed
		return artifact, nil
	}
	current, err = s.isCurrent(snapshot.FrozenInput)
	if err != nil {
		return ExportArtifact{}, err
	}
	if !current {
		return ExportArtifact{}, ErrExportStaleInput
	}
	sum := sha256.Sum256(data)
	artifact.Status = ExportVerified
	artifact.FileSHA256 = hex.EncodeToString(sum[:])
	artifact.file = append([]byte(nil), data...)
	return cloneExport(artifact), nil
}

// render 把渲染与「产出为空」两步收成一句话：两者都是 failed 产物，区别只在
// 失败码。返回非空字符串即表示失败。
func (s *ExportService) render(input DeliveryInput) ([]byte, string) {
	data, err := s.renderer.RenderFrozen(input)
	if err != nil {
		return nil, FailureRenderFailed
	}
	if len(data) == 0 {
		return nil, FailureEmptyFile
	}
	return data, ""
}

// Download returns bytes only for a verified export. A transport layer must
// re-run its current authorization check before calling this method.
//
// 它把产物一并交回：响应头里的文件名要取自**产物自己的** ID，而不是请求路径上
// 那个字符串。两者其实总是相等（查不到就是 404），但让传输层从产物取，就用不着
// 在那里写一句「为什么直接拿路径参数是安全的」。
func (s *ExportService) Download(actorUserID, projectID, exportID string) (ExportArtifact, []byte, error) {
	if err := s.authorize(actorUserID, projectID); err != nil {
		return ExportArtifact{}, nil, err
	}
	artifact, err := s.exports.GetExport(projectID, exportID)
	if err != nil {
		return ExportArtifact{}, nil, err
	}
	if artifact.Status != ExportVerified || len(artifact.file) == 0 {
		return ExportArtifact{}, nil, ErrExportUnavailable
	}
	return artifact, append([]byte(nil), artifact.file...), nil
}

// Get 读一份产物的状态。它不返回字节——那要单独走 Download，因为下载必须
// 在**那一刻**重查一次授权。取不到报 ErrExportNotFound 让传输层答 404。
func (s *ExportService) Get(actorUserID, projectID, exportID string) (ExportArtifact, error) {
	if err := s.authorize(actorUserID, projectID); err != nil {
		return ExportArtifact{}, err
	}
	return s.exports.GetExport(projectID, exportID)
}

func (s *ExportService) authorize(actorUserID, projectID string) error {
	if s.access == nil {
		return errors.New("export access checker is required")
	}
	return s.access.Authorize(actorUserID, projectID)
}

func (s *ExportService) isCurrent(input DeliveryInput) (bool, error) {
	if s.currentness == nil {
		return false, ErrExportUnavailable
	}
	return s.currentness.IsCurrent(input)
}

// exportRequestHash 是这次动作请求体的规范指纹，与 freezeRequestHash 同构。
// 契约 §6 要求把它与键一起记下：它是把「重试」与「换了请求却复用同一个键」
// 分开的唯一依据。
func exportRequestHash(actorUserID, projectID, snapshotID string) string {
	hasher := sha256.New()
	for _, field := range []string{actorUserID, projectID, snapshotID, "docx"} {
		// 长度前缀是为了让不同的字段组合拼不出同一串字节——否则改一下字段边界
		// 就成了另一次「同一个请求」。"docx" 是 StartExport.format 的唯一取值。
		_, _ = hasher.Write([]byte(fmt.Sprintf("%d:%s", len(field), field)))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func cloneExport(artifact ExportArtifact) ExportArtifact {
	artifact.file = append([]byte(nil), artifact.file...)
	return artifact
}
