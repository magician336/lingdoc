package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/docx"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRewriteInvalidRequest = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "改写请求字段不符合约定。", Retryable: false}, "selected_rewrite_invalid_request")
	ErrRewriteNotFound       = clienterror.New(clienterror.Form{Status: 404, Code: "not_found", Message: "候选不存在或不可访问。", Retryable: false}, "selected_rewrite_not_found")
	ErrRewriteStale          = clienterror.New(clienterror.Form{Status: 409, Code: "stale_input", Message: "选区或工作副本已变化，请重新运行该改写动作。", Retryable: false}, "selected_rewrite_stale")
	ErrRewriteConflict       = clienterror.New(clienterror.Form{Status: 409, Code: "stale_input", Message: "选区或工作副本已变化，请重新读取后再试。", Retryable: false}, "selected_rewrite_conflict")
	ErrRewriteUnavailable    = clienterror.New(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "改写模型暂不可用，请稍后重试。", Retryable: true}, "selected_rewrite_unavailable")
	ErrRewriteSourceDenied   = clienterror.New(clienterror.Form{Status: 403, Code: "source_access_denied", Message: "改写候选所依据的授权已失效，请恢复授权后重新运行该改写动作。", Retryable: false}, "selected_rewrite_source_denied")
	ErrRewriteKeyConflict    = clienterror.New(clienterror.Form{Status: 409, Code: "idempotency_conflict", Message: "同一个操作键对应不同请求。", Retryable: false}, "selected_rewrite_idempotency_conflict")
	// ErrRewriteContextNotAllowed 说清「这个动作不读你选的那一类上下文」。
	//
	// 它是请求错误：客户端要求纳入的东西这个动作根本不读，服务端不会替它
	// 猜一个意思，也不会把这一类上下文悄悄塞进模型请求。
	ErrRewriteContextNotAllowed = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "这个动作不读所选的那一类上下文，请调整上下文选择后再试。", Retryable: false}, "selected_rewrite_context_not_allowed")
	// ErrRewriteContextRequired 说清「这一类上下文这个动作必须用」。
	//
	// 用户可以点名它、可以选它，只是不能把它拿掉：「这一类必须用」是执行方
	// 的事实，不是用户的偏好。
	ErrRewriteContextRequired = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "这一类上下文是本次动作必须使用的，不能剔除。", Retryable: false}, "selected_rewrite_context_required")
	// ErrRewriteConfirmationRequired 说清「这次采纳没有确认」。
	//
	// 候选记下了动作 ⇒ 采纳必须确认它。没有载荷的采纳请求在服务层被拒，不是
	// 前端禁用一个按钮。
	ErrRewriteConfirmationRequired = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "采纳动作化候选必须携带确认载荷：动作、采纳目标与目标范围。", Retryable: false}, "selected_rewrite_confirmation_required")
	// ErrRewriteConfirmationForbidden 说清「这次确认不是你自己做的」。
	//
	// 代他人确认一律拒：确认人是这次请求的认证身份。
	ErrRewriteConfirmationForbidden = clienterror.New(clienterror.Form{Status: 403, Code: "forbidden", Message: "确认人必须是这次请求的认证身份。", Retryable: false}, "selected_rewrite_confirmation_forbidden")
)

type RewriteSelection struct {
	StartUTF16   int    `json:"start_utf16"`
	EndUTF16     int    `json:"end_utf16"`
	SelectedText string `json:"selected_text"`
}

type SelectedRewriteRequest struct {
	EvidenceCardRefs            []generation.EvidenceCardRef `json:"evidence_card_refs,omitempty"`
	BaseChapterVersionID        *string                      `json:"base_chapter_version_id"`
	ExpectedSpecRevision        int64                        `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64                        `json:"expected_working_copy_revision"`
	Selection                   RewriteSelection             `json:"selection"`
	Instruction                 string                       `json:"instruction"`
	SourceIDs                   []string                     `json:"source_ids"`

	// 下面三个字段把这次请求从「自由文本说明」变成「按动作发起」。
	//
	// 它们都是**指针**加 omitempty：指针为 nil 时字段整个不出现，请求的 JSON
	// 与它的摘要值逐字不变——历史幂等键的重放语义不能被这次改动碰坏。
	// 用非指针的结构体字段做不到这一点：omitempty 对结构体不起作用，旧请求
	// 会多出一个 "output_params":{...}，摘要全变，重试从「重放」变成「冲突」。
	//
	// 三个字段全部缺省 ⇒ 旧路径：自由文本说明驱动，不记录动作。
	// 任一字段出现 ⇒ 动作路径：动作与产出参数必齐，缺一即拒。
	WritingAction *writingaction.Action       `json:"writing_action,omitempty"`
	OutputParams  *writingaction.OutputParams `json:"output_params,omitempty"`
	Scope         *writingaction.Scope        `json:"scope,omitempty"`
	// Context 是用户对这次执行要纳入的可选上下文的选择。
	//
	// 它是**指针**加 omitempty：缺省即「没选」，旧请求的 JSON 与它的摘要
	// 值逐字不变。它只在动作路径下有意义——旧路径带上它会被拒。
	Context *writingaction.ContextSelection `json:"context,omitempty"`
}

// isLegacyPath 判定这次请求是不是旧路径。
func (r SelectedRewriteRequest) isLegacyPath() bool {
	return r.WritingAction == nil && r.OutputParams == nil && r.Scope == nil && r.Context == nil
}

type RewriteSourceSnapshot struct {
	SourceID       string    `json:"source_id"`
	AssetID        string    `json:"asset_id"`
	AssetRevision  int       `json:"asset_revision"`
	Locator        string    `json:"locator"`
	QuotedTextHash string    `json:"quoted_text_hash"`
	AuthorizedAt   time.Time `json:"authorized_at"`
}

type SelectedRewriteCandidate struct {
	EvidenceCardRefs []generation.EvidenceCardRef `json:"evidence_card_refs,omitempty"`
	CandidateID      string                       `json:"candidate_id"`
	ProjectID        string                       `json:"project_id"`
	ChapterID        string                       `json:"chapter_id"`
	// Validity 是这个候选还能不能被采纳。
	//
	// 它是**候选层的那个枚举**：选段候选与生成候选用同一份词表、同一组取值，
	// 不再各说各的（开发计划 §3.4 的归一）。空值表示「这次运行没有交出可采纳的
	// 产出」——运行中是这样，一次失败也是这样；失败的原因在 ErrorCode 里。
	Validity candidateadoption.Validity `json:"validity"`
	// LifecycleStage 是这个候选走到了哪一步。
	//
	// 它是**生命周期阶段**（queued / ready / applying / failed / stale），不是
	// 失效判据：失效判据只有 Validity 一个拥有者（fresh / stale /
	// not_authorized）。两者回答的不是同一个问题——「走到哪一步」是时间上的
	// 位置，「还能不能被采纳」是此刻的可用性。
	//
	// JSON 键仍是 `status`：保留路径（frontend/src/views/lingdoc/Workspace.vue）
	// 还在读它，线上形状逐字不变。这个字段是 LifecycleStage 的渲染结果，不是
	// 第二套状态机——它由 rewriteLifecycleStage 算出。那些消费者改读
	// validity 之后，这个字段与那段代码一起删掉。
	LifecycleStage       string                         `json:"status"`
	RunMode              candidateadoption.RunMode      `json:"run_mode"`
	BaseChapterVersionID *string                        `json:"base_chapter_version_id"`
	SpecRevision         int64                          `json:"spec_revision"`
	WorkingCopyRevision  int64                          `json:"working_copy_revision"`
	Selection            RewriteSelection               `json:"selection"`
	ReplacementMarkdown  string                         `json:"replacement_markdown"`
	SourceIDs            []string                       `json:"source_ids"`
	AuthorizedSources    []RewriteSourceSnapshot        `json:"authorized_sources"`
	ReviewItems          []candidateadoption.ReviewItem `json:"review_items"`
	ErrorCode            string                         `json:"error_code"`
	CreatedAt            time.Time                      `json:"created_at"`
	// WritingAction 是产生这个候选的动作。旧路径没有动作，字段为空。
	//
	// 它挂在结构体末尾并带 omitempty：旧路径的候选 JSON 逐字不变。
	WritingAction writingaction.Action `json:"writing_action,omitempty"`
	// Origin 是候选正文的来源。改写是模型产出，所以是 ai_generated；
	// 旧路径没有来源标注，字段为空。
	Origin writingaction.Origin `json:"origin,omitempty"`
	// DroppedContext 是用户选了、但没有进入模型请求的可选上下文条目。
	//
	// 丢弃是可见的：静默丢弃会让用户以为它进去了。它只在发起的那一刻
	// 有值——之后读回来的是候选本身。
	DroppedContext []writingaction.DroppedContextEntry `json:"dropped_context,omitempty"`
}

type SelectedRewritePrompt struct {
	TemplateBasis       g2RewriteTemplateBasis
	EvidenceCardRefs    []generation.EvidenceCardRef
	EvidenceConstraints []generation.EvidenceConstraint
	Selection           RewriteSelection
	// Instruction 是给模型的动作指令。旧路径下它是客户端原文，逐字不变；
	// 动作路径下它由执行计划渲染。
	Instruction string
	// SupplementaryNote 是客户端的补充说明。旧路径恒为空；动作路径下它不参与
	// 动作判定，也不与动作指令混成一段。
	SupplementaryNote string
	ContextBefore     string
	ContextAfter      string
	Sources           []evidence.Source
	// Produce 是这次执行的产出形状：候选正文，还是评审意见。
	//
	// 旧路径下它为空：旧路径不记录动作，模型适配器按「改写」那一种提示语
	// 说话。动作路径下它来自执行计划，模型适配器据此换一套提示语——评审
	// 要的是意见清单，不是改好的正文。
	Produce string
}

type SelectedRewriteOutput struct {
	ReplacementMarkdown string                         `json:"replacement_markdown"`
	SourceIDs           []string                       `json:"source_ids"`
	ReviewItems         []candidateadoption.ReviewItem `json:"review_items"`
	RunMode             string                         `json:"-"`
}

type SelectedRewriteModel interface {
	RunMode() string
	RewriteSelected(context.Context, Actor, SelectedRewritePrompt) (SelectedRewriteOutput, error)
}

type SelectedRewriteApplication interface {
	Create(context.Context, Actor, string, string, string, SelectedRewriteRequest) (SelectedRewriteCandidate, error)
	Get(context.Context, Actor, string, string) (SelectedRewriteCandidate, error)
	Apply(context.Context, Actor, string, string, string, string, ApplySelectedRewriteInput) (core.WorkingCopy, bool, error)
}

type ApplySelectedRewriteInput struct {
	ExpectedSpecRevision        int64   `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64   `json:"expected_working_copy_revision"`
	BaseChapterVersionID        *string `json:"base_chapter_version_id"`
	// Confirmation 是采纳的显式确认载荷。
	//
	// 动作化路径的候选必须带它：采纳不是「点一下就写进去」，用户要确认这次
	// 采纳的是哪个动作的结果、写到哪里、写到哪一段。载荷缺了，服务层拒。
	//
	// 它是**指针**：缺省即「没确认」，与「确认了但字段填错」要能分开——两者
	// 的恢复动作不同。旧路径的候选没有动作可确认，它不带这个字段，行为逐字
	// 不变。
	Confirmation *candidateadoption.AdoptionConfirmation `json:"confirmation,omitempty"`
}

type selectedRewriteRow struct {
	EvidenceConstraintsJSON string `gorm:"column:evidence_constraints_json;not null;type:text;default:'[]'"`
	TemplateBasisJSON       string `gorm:"column:template_basis_json;not null;type:text;default:'{}'"`
	EvidenceCardRefsJSON    string `gorm:"column:evidence_card_refs_json;not null;type:text;default:'[]'"`
	ID                      string `gorm:"primaryKey;size:36"`
	TenantID                uint64 `gorm:"not null;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:1"`
	ActorID                 string `gorm:"not null;size:64;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:2"`
	ProjectID               string `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:3"`
	ChapterID               string `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:4"`
	IdempotencyKey          string `gorm:"not null;size:128;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:5"`
	RequestHash             string `gorm:"not null;size:64"`
	// Validity 是这个候选还能不能被采纳：与候选层同一个枚举、同一份词表。
	//
	// 空值表示「这次运行没有交出可采纳的产出」——运行中与失败都是这样；失败的
	// 原因在 ErrorCode 里。
	Validity              string                    `gorm:"column:validity;not null;size:16;index;default:''"`
	RunMode               candidateadoption.RunMode `gorm:"not null;size:32;default:''"`
	BaseChapterVersionID  *string                   `gorm:"size:36"`
	SpecRevision          int64                     `gorm:"not null"`
	WorkingCopyRevision   int64                     `gorm:"not null"`
	StartUTF16            int                       `gorm:"not null"`
	EndUTF16              int                       `gorm:"not null"`
	SelectedText          string                    `gorm:"not null;type:text"`
	Instruction           string                    `gorm:"not null;size:2000"`
	ReplacementMarkdown   string                    `gorm:"not null;type:text"`
	SourceIDsJSON         string                    `gorm:"not null;type:text"`
	AuthorizedSourcesJSON string                    `gorm:"not null;type:text"`
	ReviewItemsJSON       string                    `gorm:"not null;type:text"`
	WritingAction         string                    `gorm:"not null;size:64;default:''"`
	Origin                string                    `gorm:"not null;size:32;default:''"`
	ErrorCode             string                    `gorm:"not null;size:64;default:''"`
	AppliedKey            string                    `gorm:"not null;size:128;default:''"`
	AppliedResponseJSON   string                    `gorm:"not null;type:text;default:''"`
	ApplyPendingKey       string                    `gorm:"not null;size:128;default:''"`
	ApplyPendingInputJSON string                    `gorm:"not null;type:text;default:''"`
	// DroppedContextJSON 是这一次请求里被丢掉的可选上下文条目。
	//
	// 它不进库：丢弃只发生在发起的那一刻，之后读回来的是候选本身。
	DroppedContextJSON string `gorm:"-"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (selectedRewriteRow) TableName() string { return "lingdoc_selected_rewrites" }

type GORMSelectedRewriteStore struct{ db *gorm.DB }

func NewGORMSelectedRewriteStore(db *gorm.DB) *GORMSelectedRewriteStore {
	return &GORMSelectedRewriteStore{db: db}
}

func (s *GORMSelectedRewriteStore) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&selectedRewriteRow{})
}

func (s *GORMSelectedRewriteStore) createOrReplay(ctx context.Context, row selectedRewriteRow) (selectedRewriteRow, bool, error) {
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "actor_id"},
			{Name: "project_id"}, {Name: "chapter_id"}, {Name: "idempotency_key"}}, DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected == 1
		if created {
			return nil
		}
		var existing selectedRewriteRow
		if err := tx.Where("tenant_id = ? AND actor_id = ? AND project_id = ? AND chapter_id = ? AND idempotency_key = ?",
			row.TenantID, row.ActorID, row.ProjectID, row.ChapterID, row.IdempotencyKey).First(&existing).Error; err != nil {
			return err
		}
		if existing.RequestHash != row.RequestHash {
			return ErrRewriteKeyConflict
		}
		row = existing
		return nil
	})
	return row, created, err
}

func (s *GORMSelectedRewriteStore) byID(ctx context.Context, tenantID uint64, projectID, chapterID, candidateID string) (selectedRewriteRow, error) {
	var row selectedRewriteRow
	query := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, projectID, candidateID)
	if chapterID != "" {
		query = query.Where("chapter_id = ?", chapterID)
	}
	err := query.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return selectedRewriteRow{}, ErrRewriteNotFound
	}
	return row, err
}

func (s *GORMSelectedRewriteStore) byOperation(ctx context.Context, tenantID uint64, projectID, chapterID, key string) (selectedRewriteRow, error) {
	var row selectedRewriteRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND chapter_id = ? AND applied_key = ?",
		tenantID, projectID, chapterID, key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return selectedRewriteRow{}, ErrRewriteNotFound
	}
	return row, err
}

func (s *GORMSelectedRewriteStore) update(ctx context.Context, row selectedRewriteRow) error {
	return s.db.WithContext(ctx).Model(&selectedRewriteRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"validity": row.Validity, "run_mode": row.RunMode, "replacement_markdown": row.ReplacementMarkdown,
		"source_ids_json": row.SourceIDsJSON, "authorized_sources_json": row.AuthorizedSourcesJSON,
		"review_items_json": row.ReviewItemsJSON, "error_code": row.ErrorCode, "applied_key": row.AppliedKey,
		"applied_response_json": row.AppliedResponseJSON, "apply_pending_key": row.ApplyPendingKey,
		"apply_pending_input_json": row.ApplyPendingInputJSON, "updated_at": row.UpdatedAt,
	}).Error
}

// Only observation fields may be changed by a stale reader. A concurrently
// claimed/applied operation owns its receipt and cannot be overwritten here.
func (s *GORMSelectedRewriteStore) markStale(ctx context.Context, row selectedRewriteRow, now time.Time) (selectedRewriteRow, error) {
	result := s.db.WithContext(ctx).Model(&selectedRewriteRow{}).Where("id = ? AND tenant_id = ? AND applied_key = '' AND apply_pending_key = '' AND validity = ? AND updated_at = ?", row.ID, row.TenantID, row.Validity, row.UpdatedAt).Updates(map[string]any{"validity": string(candidateadoption.ValidityStale), "replacement_markdown": "", "error_code": "stale_input", "updated_at": now})
	if result.Error != nil {
		return selectedRewriteRow{}, result.Error
	}
	return s.byID(ctx, row.TenantID, row.ProjectID, row.ChapterID, row.ID)
}

func (s *GORMSelectedRewriteStore) claimApply(ctx context.Context, row selectedRewriteRow, key, inputJSON string, now time.Time) (selectedRewriteRow, error) {
	result := s.db.WithContext(ctx).Model(&selectedRewriteRow{}).
		Where("id = ? AND applied_key = '' AND apply_pending_key = '' AND validity = ?", row.ID, string(candidateadoption.ValidityFresh)).
		Updates(map[string]any{"apply_pending_key": key, "apply_pending_input_json": inputJSON, "updated_at": now})
	if result.Error != nil {
		return selectedRewriteRow{}, result.Error
	}
	if result.RowsAffected == 1 {
		row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = key, inputJSON, now
		return row, nil
	}
	return s.byID(ctx, row.TenantID, row.ProjectID, row.ChapterID, row.ID)
}

type SelectedRewriteService struct {
	store     *GORMSelectedRewriteStore
	workspace ApplicationService
	sources   SourceApplicationService
	model     SelectedRewriteModel
	audit     AuditSink
	now       func() time.Time
}

func NewSelectedRewriteService(db *gorm.DB, workspace ApplicationService, sources SourceApplicationService, model SelectedRewriteModel, audits ...AuditSink) *SelectedRewriteService {
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SelectedRewriteService{store: NewGORMSelectedRewriteStore(db), workspace: workspace, sources: sources, model: model, audit: audit, now: func() time.Time { return time.Now().UTC() }}
}

func (s *SelectedRewriteService) recordAudit(ctx context.Context, actor Actor, projectID, capability, decision, reason string, details map[string]any) {
	if s == nil || s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role,
		ProjectID: projectID, Capability: capability, Decision: decision, Reason: reason, Details: details})
}

func (s *SelectedRewriteService) Create(ctx context.Context, actor Actor, projectID, chapterID, key string, input SelectedRewriteRequest) (SelectedRewriteCandidate, error) {
	auditDetails := map[string]any{"chapter_id": chapterID, "idempotency_key": key, "spec_revision": input.ExpectedSpecRevision,
		"working_copy_revision": input.ExpectedWorkingCopyRevision}
	auditDecision := "allow"
	auditReason := ""
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.create", auditDecision, auditReason, auditDetails)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || s.model == nil ||
		actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(chapterID) == "" {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	if err := core.RequireHumanDecision(ctx); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	key = strings.TrimSpace(key)
	if err := validateSelectedRewriteInput(key, input); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	// 动作路径的声明先解析、再干活：产出参数与目标范围在模型调用之前就被拒，
	// 非法声明不产生 run，也不产生候选行（开发计划 I-2）。
	plan, err := resolveRewritePlan(input)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.SourceIDs = slices.Clone(input.SourceIDs)
	input.EvidenceCardRefs = append([]generation.EvidenceCardRef(nil), input.EvidenceCardRefs...)
	// 用户对可选上下文的选择先解析：类别越过策略的请求被拒；没授权的条目
	// 被丢掉并被点名；用户剔除的条目不进模型请求。
	var droppedContext []writingaction.DroppedContextEntry
	if plan != nil && input.Context != nil {
		includedSources, includedCards, dropped, contextErr := s.resolveRewriteContext(ctx, actor, projectID, chapterID, *plan, *input.Context)
		if contextErr != nil {
			auditDecision, auditReason = "deny", contextErr.Error()
			return SelectedRewriteCandidate{}, contextErr
		}
		droppedContext = dropped
		input.SourceIDs = append(input.SourceIDs, includedSources...)
		input.EvidenceCardRefs = append(input.EvidenceCardRefs, includedCards...)
		input.EvidenceCardRefs = removeRewriteExcludedCards(input.EvidenceCardRefs, input.Context)
	}
	var evidenceConstraints []generation.EvidenceConstraint
	if len(input.EvidenceCardRefs) > 0 {
		selected, selectErr := g2SelectFormal(ctx, s.workspace, actor, projectID, chapterID, g2GenerationRefs(input.EvidenceCardRefs))
		if selectErr != nil {
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		evidenceConstraints, selectErr = g2EvidenceConstraints(selected, input.EvidenceCardRefs)
		if selectErr != nil {
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		ids := []string{}
		for _, source := range selected.Sources {
			ids = append(ids, source.SourceID)
		}
		ids = stringSet(ids)
		if input.Context == nil {
			// 旧路径的来源集合就是卡片解析出来的那一组：客户端列的来源必须
			// 与它逐字一致，否则整个请求按「来源对不上」被拒。
			if len(input.SourceIDs) > 0 && !slices.Equal(stringSet(input.SourceIDs), ids) {
				return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
			}
			input.SourceIDs = ids
		} else {
			// 有上下文选择时，用户此刻正在显式地选资料：客户端列的来源与卡片
			// 解析出来的来源取并集，每一个都还要过取来源那一步的授权。
			input.SourceIDs = stringSet(append(input.SourceIDs, ids...))
		}
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
	}
	if project.Status != "active" || project.SpecRevision != input.ExpectedSpecRevision ||
		copy.SpecRevision != input.ExpectedSpecRevision || copy.WorkingCopyRevision != input.ExpectedWorkingCopyRevision ||
		!sameOptionalVersion(copy.BaseChapterVersionID, input.BaseChapterVersionID) {
		auditDecision, auditReason = "deny", ErrRewriteConflict.Error()
		return SelectedRewriteCandidate{}, ErrRewriteConflict
	}
	_, _, before, after, ok := selectedRewriteContextWindow(copy.BodyMarkdown, input.Selection)
	if !ok {
		auditDecision, auditReason = "deny", ErrRewriteStale.Error()
		return SelectedRewriteCandidate{}, ErrRewriteStale
	}
	// The selection has just been matched against the versioned working copy.
	// Existing citations in that trusted slice remain inputs to the rewrite even
	// when a freshly opened editor has no manually selected retrieval results.
	// Merge before authorization and hashing so explicit and inherited references
	// share one effective request identity; never infer permission from model output.
	for _, marker := range sourcemarker.IDs(input.Selection.SelectedText) {
		if !slices.Contains(copy.SourceIDs, marker) {
			auditDecision, auditReason = "deny", ErrRewriteSourceDenied.Error()
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		if len(input.EvidenceCardRefs) > 0 && !slices.Contains(input.SourceIDs, marker) {
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		input.SourceIDs = append(input.SourceIDs, marker)
	}
	// 用户剔除的来源不进模型请求。剔除在继承来源标记之后：用户剔除一条
	// 继承来的来源，意思正是「这一条不要」，它不该被标记再塞回来。剔除
	// 先于取来源——用户剔除一条来源，往往正是因为它已经不可用了。
	input.SourceIDs = s.removeRewriteExcludedSources(ctx, actor, projectID, input.SourceIDs, input.Context)
	input.SourceIDs = stringSet(input.SourceIDs)
	sources, snapshots, err := s.fetchSources(ctx, actor, projectID, input.SourceIDs)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	if err := g2RewriteModelScope(ctx, s.sources, actor, projectID, snapshots); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if err := s.checkEvidenceRefs(ctx, actor, projectID, chapterID, input.EvidenceCardRefs, snapshots); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	mode := candidateadoption.RunMode(s.model.RunMode())
	if !candidateadoption.ValidRunMode(mode) {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	hash, err := selectedRewriteRequestHash(input)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	seed, err := s.createRow(actor, projectID, chapterID, key, input, hash, snapshots, plan)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	seed.DroppedContextJSON = encodeDroppedContext(droppedContext)
	templateBasis, basisErr := g2RewriteProjectBasis(project)
	if basisErr != nil {
		return SelectedRewriteCandidate{}, basisErr
	}
	templateJSON, jsonErr := json.Marshal(templateBasis)
	if jsonErr != nil {
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	constraintsJSON, jsonErr := json.Marshal(evidenceConstraints)
	if jsonErr != nil {
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	seed.EvidenceConstraintsJSON = string(constraintsJSON)
	seed.TemplateBasisJSON = string(templateJSON)
	seed.RunMode = mode
	row, created, err := s.store.createOrReplay(ctx, seed)
	if err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if !created {
		if err := s.checkRowEvidence(ctx, actor, projectID, row); err != nil {
			return SelectedRewriteCandidate{}, err
		}
		row, err = s.staleIfNeeded(ctx, actor, projectID, chapterID, row)
		if err != nil {
			auditDecision, auditReason = "deny", err.Error()
			return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
		}
		if row.Validity == string(candidateadoption.ValidityFresh) && s.recheckSources(ctx, actor, projectID, mustDecodeSnapshots(row.AuthorizedSourcesJSON)) != nil {
			auditDecision, auditReason = "deny", ErrRewriteSourceDenied.Error()
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		auditDetails["candidate_id"], auditDetails["run_mode"], auditDetails["validity"] = row.ID, row.RunMode, row.Validity
		return selectedRewriteView(row)
	}
	if err := s.store.update(ctx, row); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	auditDetails["candidate_id"] = row.ID
	auditDetails["run_mode"] = row.RunMode
	if len(input.EvidenceCardRefs) > 0 {
		before = ""
		after = ""
	}
	prompt, promptErr := rewritePrompt(input, plan, templateBasis, evidenceConstraints, before, after, sources)
	if promptErr != nil {
		auditDecision, auditReason = "deny", promptErr.Error()
		return SelectedRewriteCandidate{}, promptErr
	}
	output, modelErr := s.model.RewriteSelected(ctx, actor, prompt)
	if modelErr != nil {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = "", "rewrite_failed", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if output.RunMode != "" {
		if !candidateadoption.ValidRunMode(candidateadoption.RunMode(output.RunMode)) {
			row.Validity, row.ErrorCode, row.ReplacementMarkdown = "", "invalid_run_mode", ""
			auditDecision, auditReason = "deny", row.ErrorCode
			auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
			return s.updateCandidate(context.WithoutCancel(ctx), row)
		}
		row.RunMode = candidateadoption.RunMode(output.RunMode)
	}
	if err := validateRewriteOutput(output, input.SourceIDs); err != nil {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = "", selectedRewriteFailureCode(err), ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = string(candidateadoption.ValidityNotAuthorized), "source_access_denied", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = string(candidateadoption.ValidityNotAuthorized), "authorization_changed", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	currentCopy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil || !workingCopyIsCandidateBasis(currentCopy, row) {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = string(candidateadoption.ValidityStale), "stale_input", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := s.checkRowEvidence(ctx, actor, projectID, row); err != nil {
		row.Validity = string(candidateadoption.ValidityStale)
		row.ErrorCode = "evidence_changed"
		row.ReplacementMarkdown = ""
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := writeRewriteResult(&row, output); err != nil {
		row.Validity, row.ErrorCode, row.ReplacementMarkdown = "", selectedRewriteFailureCode(err), ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	auditDetails["validity"], auditDetails["run_mode"] = row.Validity, row.RunMode
	return s.updateCandidate(context.WithoutCancel(ctx), row)
}

func (s *SelectedRewriteService) Get(ctx context.Context, actor Actor, projectID, candidateID string) (result SelectedRewriteCandidate, err error) {
	details := map[string]any{"candidate_id": candidateID}
	defer func() {
		decision, reason := "allow", ""
		if err != nil {
			decision, reason = "deny", err.Error()
		}
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.get", decision, reason, details)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || actor.TenantID == 0 {
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "read"); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	row, err := s.store.byID(ctx, actor.TenantID, projectID, "", candidateID)
	if err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if evidenceErr := s.checkRowEvidence(ctx, actor, projectID, row); evidenceErr != nil {
		return SelectedRewriteCandidate{}, evidenceErr
	}
	details["run_mode"], details["validity"] = row.RunMode, row.Validity
	details["spec_revision"], details["working_copy_revision"] = row.SpecRevision, row.WorkingCopyRevision
	if row.ApplyPendingKey == "" {
		row, err = s.staleIfNeeded(ctx, actor, projectID, row.ChapterID, row)
		if err != nil {
			return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
		}
	}
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if len(snapshots) > 0 {
		if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
	}
	return selectedRewriteView(row)
}

func (s *SelectedRewriteService) Apply(ctx context.Context, actor Actor, projectID, chapterID, candidateID, key string, input ApplySelectedRewriteInput) (result core.WorkingCopy, replayed bool, err error) {
	if err := core.RequireHumanDecision(ctx); err != nil {
		return core.WorkingCopy{}, false, err
	}
	details := map[string]any{"chapter_id": chapterID, "candidate_id": candidateID, "idempotency_key": key,
		"spec_revision": input.ExpectedSpecRevision, "working_copy_revision": input.ExpectedWorkingCopyRevision}
	defer func() {
		decision, reason := "allow", ""
		if err != nil {
			decision, reason = "deny", err.Error()
		}
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.apply", decision, reason, details)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || actor.TenantID == 0 || !operationcore.ValidKey(key) {
		return core.WorkingCopy{}, false, ErrRewriteInvalidRequest
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		return core.WorkingCopy{}, false, err
	}
	row, err := s.store.byID(ctx, actor.TenantID, projectID, chapterID, candidateID)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	if evidenceErr := s.checkRowEvidence(ctx, actor, projectID, row); evidenceErr != nil {
		return core.WorkingCopy{}, false, evidenceErr
	}
	details["run_mode"], details["validity"] = row.RunMode, row.Validity
	details["base_chapter_version_id"] = row.BaseChapterVersionID
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
		return core.WorkingCopy{}, false, ErrRewriteSourceDenied
	}
	// 撤权先说、确认后说：一个依据已经没了的候选，说的是「授权没了」，不是
	// 「你的确认填错了」——两种失效要能分开说。
	if err := validateRewriteAdoptionConfirmation(row, input.Confirmation, actor); err != nil {
		return core.WorkingCopy{}, false, err
	}
	// 确认载荷进审计：采纳之后要能说清「用户确认的是哪一次采纳、写到哪里」。
	if input.Confirmation != nil {
		details["writing_action"] = row.WritingAction
		details["confirmed_target"] = string(input.Confirmation.Target)
		details["confirmed_at"] = input.Confirmation.ConfirmedAt
		details["confirmed_by"] = input.Confirmation.ConfirmedBy
	}
	if row.AppliedKey == key && row.AppliedResponseJSON != "" {
		if input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
			!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		var saved core.WorkingCopy
		if err := json.Unmarshal([]byte(row.AppliedResponseJSON), &saved); err != nil {
			return core.WorkingCopy{}, false, err
		}
		return saved, true, nil
	}
	if row.ApplyPendingKey != "" {
		if row.ApplyPendingKey != key {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		if input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
			!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		return s.completePendingApply(ctx, actor, projectID, chapterID, key, row)
	}
	if row.Validity != string(candidateadoption.ValidityFresh) || row.ReplacementMarkdown == "" {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	if project.Status != "active" || project.SpecRevision != input.ExpectedSpecRevision ||
		input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
		!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) || !workingCopyIsCandidateBasis(copy, row) {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	start, end, ok := selectionByteRange(copy.BodyMarkdown, RewriteSelection{
		StartUTF16: row.StartUTF16, EndUTF16: row.EndUTF16, SelectedText: row.SelectedText,
	})
	if !ok {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	body := copy.BodyMarkdown[:start] + row.ReplacementMarkdown + copy.BodyMarkdown[end:]
	if err := sourcemarker.Validate(body); err != nil {
		return core.WorkingCopy{}, false, ErrRewriteInvalidRequest
	}
	refs := sourcemarker.IDs(body)
	reviewItems := mustDecodeReviewItems(row.ReviewItemsJSON)
	for i := range reviewItems {
		reviewItems[i].Anchor = &reviewanchor.Anchor{StartUTF16: row.StartUTF16, EndUTF16: row.StartUTF16 + len(utf16.Encode([]rune(row.ReplacementMarkdown))), QuoteMarkdown: row.ReplacementMarkdown, Status: "located"}
	}
	writeInput := core.SaveWorkingCopyInput{BaseChapterVersionID: row.BaseChapterVersionID,
		ExpectedSpecRevision: project.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: body, SourceIDs: stringSet(refs)}
	pendingJSON, err := json.Marshal(selectedRewritePendingApply{WorkingCopy: writeInput, ReviewItems: reviewItemsForWorkingCopy(reviewItems)})
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	row, err = s.store.claimApply(ctx, row, key, string(pendingJSON), s.now())
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	if row.ApplyPendingKey != key {
		if row.AppliedKey == key && row.AppliedResponseJSON != "" {
			var saved core.WorkingCopy
			if err := json.Unmarshal([]byte(row.AppliedResponseJSON), &saved); err != nil {
				return core.WorkingCopy{}, false, err
			}
			return saved, true, nil
		}
		return core.WorkingCopy{}, false, ErrRewriteKeyConflict
	}
	return s.completePendingApply(ctx, actor, projectID, chapterID, key, row)
}

type selectedRewritePendingApply struct {
	WorkingCopy core.SaveWorkingCopyInput `json:"working_copy"`
	ReviewItems []core.ReviewItem         `json:"review_items"`
}

func (s *SelectedRewriteService) completePendingApply(ctx context.Context, actor Actor, projectID, chapterID, key string, row selectedRewriteRow) (core.WorkingCopy, bool, error) {
	var pending selectedRewritePendingApply
	if err := json.Unmarshal([]byte(row.ApplyPendingInputJSON), &pending); err != nil {
		return core.WorkingCopy{}, false, err
	}
	operationKey := operationKeyForApply(key, row.ID)
	result, err := s.workspace.ApplyRewriteResult(ctx, actor, projectID, chapterID, operationKey, pending.WorkingCopy, pending.ReviewItems)
	if err != nil {
		if errors.Is(err, core.ErrVersionConflict) {
			row.Validity, row.ReplacementMarkdown, row.ErrorCode = string(candidateadoption.ValidityStale), "", "stale_input"
			row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = "", "", s.now()
			if persistErr := s.store.update(context.WithoutCancel(ctx), row); persistErr != nil {
				return core.WorkingCopy{}, false, persistErr
			}
		}
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	saved := result.WorkingCopy
	raw, err := json.Marshal(saved)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	row.AppliedKey, row.AppliedResponseJSON, row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = key, string(raw), "", "", s.now()
	if err := s.store.update(context.WithoutCancel(ctx), row); err != nil {
		return core.WorkingCopy{}, false, err
	}
	return saved, result.Replayed, nil
}

// rewriteAdoptionTarget 是选段采纳端点实现的采纳目标。
//
// 动作的许可位说「这个动作可以这样被采纳」，端点说的是「我能这样写」。两者
// 都要成立：这个端点只写工作副本的选段——它把候选正文写回候选记录的那一段
// 选区。
const rewriteAdoptionTarget = writingaction.TargetWorkingCopyPartial

// validateRewriteAdoptionConfirmation 校验采纳的显式确认载荷。
//
// 候选记下了动作 ⇒ 采纳必须确认它。确认载荷要说的六件事缺一不可：动作、采纳
// 目标、目标范围、确认时刻、确认人与来源。其中动作与来源必须与候选对得上，
// 采纳目标必须落在动作声明的许可位里（许可位查词汇包，执行方不按动作名
// 分支），目标范围必须就是候选的选区——这个端点写的就是那一段。
//
// 旧路径的候选没有动作，确认不了：它不带确认载荷，行为逐字不变；带了载荷
// 反而被拒，因为没有动作可对。
func validateRewriteAdoptionConfirmation(row selectedRewriteRow,
	confirmation *candidateadoption.AdoptionConfirmation, actor Actor) error {
	if row.WritingAction == "" {
		if confirmation != nil {
			return ErrRewriteInvalidRequest
		}
		return nil
	}
	if confirmation == nil {
		return ErrRewriteConfirmationRequired
	}
	if confirmation.WritingAction == "" || confirmation.Target == "" ||
		confirmation.ConfirmedAt.IsZero() || strings.TrimSpace(confirmation.ConfirmedBy) == "" ||
		confirmation.Origin == "" {
		return ErrRewriteInvalidRequest
	}
	// 代他人确认：确认人必须是这次请求的认证身份。
	if strings.TrimSpace(confirmation.ConfirmedBy) != strings.TrimSpace(actor.UserID) {
		return ErrRewriteConfirmationForbidden
	}
	// 确认的动作与来源必须与候选对得上：确认了但没确认对东西，不算确认。
	if confirmation.WritingAction != writingaction.Action(row.WritingAction) ||
		confirmation.Origin != writingaction.Origin(row.Origin) {
		return ErrRewriteInvalidRequest
	}
	allowed, err := writingaction.TargetsFor(writingaction.Action(row.WritingAction))
	if err != nil {
		return ErrRewriteInvalidRequest
	}
	if !slices.Contains(allowed, confirmation.Target) {
		return ErrRewriteInvalidRequest
	}
	if confirmation.Target != rewriteAdoptionTarget {
		return ErrRewriteInvalidRequest
	}
	// 部分采纳必须说清写到哪一段，而且那一段就是候选的选区。
	if confirmation.Scope == nil ||
		confirmation.Scope.StartUTF16 != row.StartUTF16 ||
		confirmation.Scope.EndUTF16 != row.EndUTF16 {
		return ErrRewriteInvalidRequest
	}
	return nil
}

func selectedRewriteView(row selectedRewriteRow) (SelectedRewriteCandidate, error) {
	var sourceIDs []string
	var snapshots []RewriteSourceSnapshot
	var reviewItems []candidateadoption.ReviewItem
	if err := json.Unmarshal([]byte(row.SourceIDsJSON), &sourceIDs); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if err := json.Unmarshal([]byte(row.AuthorizedSourcesJSON), &snapshots); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if err := json.Unmarshal([]byte(row.ReviewItemsJSON), &reviewItems); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if sourceIDs == nil {
		sourceIDs = []string{}
	}
	if snapshots == nil {
		snapshots = []RewriteSourceSnapshot{}
	}
	if reviewItems == nil {
		reviewItems = []candidateadoption.ReviewItem{}
	}
	evidenceRefs, refErr := decodeRewriteEvidenceRefs(row.EvidenceCardRefsJSON)
	if refErr != nil {
		return SelectedRewriteCandidate{}, refErr
	}
	var dropped []writingaction.DroppedContextEntry
	if row.DroppedContextJSON != "" {
		if err := json.Unmarshal([]byte(row.DroppedContextJSON), &dropped); err != nil {
			return SelectedRewriteCandidate{}, err
		}
	}
	return SelectedRewriteCandidate{EvidenceCardRefs: evidenceRefs, CandidateID: row.ID, ProjectID: row.ProjectID, ChapterID: row.ChapterID,
		Validity: candidateadoption.Validity(row.Validity), LifecycleStage: rewriteLifecycleStage(row), RunMode: row.RunMode,
		BaseChapterVersionID: row.BaseChapterVersionID,
		SpecRevision:         row.SpecRevision, WorkingCopyRevision: row.WorkingCopyRevision,
		Selection:           RewriteSelection{StartUTF16: row.StartUTF16, EndUTF16: row.EndUTF16, SelectedText: row.SelectedText},
		ReplacementMarkdown: row.ReplacementMarkdown, SourceIDs: sourceIDs, AuthorizedSources: snapshots,
		ReviewItems: reviewItems, ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt,
		WritingAction: writingaction.Action(row.WritingAction), Origin: writingaction.Origin(row.Origin),
		DroppedContext: dropped}, nil
}

// rewriteLifecycleStage 算出这个候选走到了哪一步。
//
// 它是**生命周期阶段**（queued / ready / applying / failed / stale），不是失效
// 判据：失效判据只有 `Validity` 一个拥有者。两者回答的不是同一个问题——
// 「走到哪一步」是时间上的位置，「还能不能被采纳」是此刻的可用性。
//
// 它是 Validity 的**渲染**，不是第二套状态机：取值由 Validity 枚举与行上的
// 应用进度派生。保留路径（frontend/src/views/lingdoc/Workspace.vue）还在读
// 这个字段；那些消费者改读 validity 之后，这个函数与那个字段一起删掉。
func rewriteLifecycleStage(row selectedRewriteRow) string {
	if row.ApplyPendingKey != "" && row.AppliedKey == "" {
		return "applying"
	}
	switch candidateadoption.Validity(row.Validity) {
	case candidateadoption.ValidityFresh:
		return "ready"
	case candidateadoption.ValidityStale, candidateadoption.ValidityNotAuthorized:
		return "stale"
	default:
		if row.ErrorCode != "" {
			return "failed"
		}
		return "queued"
	}
}

func stringSet(values []string) []string {
	result := slices.Clone(values)
	sort.Strings(result)
	return slices.Compact(result)
}

func selectionByteRange(body string, selection RewriteSelection) (int, int, bool) {
	if selection.StartUTF16 < 0 || selection.EndUTF16 <= selection.StartUTF16 || selection.SelectedText == "" {
		return 0, 0, false
	}
	units := utf16.Encode([]rune(body))
	if selection.EndUTF16 > len(units) {
		return 0, 0, false
	}
	selected := string(utf16.Decode(units[selection.StartUTF16:selection.EndUTF16]))
	if selected != selection.SelectedText {
		return 0, 0, false
	}
	start, end := -1, -1
	unitOffset, byteOffset := 0, 0
	for _, r := range body {
		if unitOffset == selection.StartUTF16 {
			start = byteOffset
		}
		unitOffset += len(utf16.Encode([]rune{r}))
		byteOffset += len(string(r))
		if unitOffset == selection.EndUTF16 {
			end = byteOffset
		}
	}
	if selection.StartUTF16 == 0 {
		start = 0
	}
	if selection.EndUTF16 == len(units) {
		end = len(body)
	}
	return start, end, start >= 0 && end >= start
}

func selectedRewriteContext(body string, start, end int, maxRunes int) (string, string) {
	beforeRunes, afterRunes := []rune(body[:start]), []rune(body[end:])
	if len(beforeRunes) > maxRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-maxRunes:]
	}
	if len(afterRunes) > maxRunes {
		afterRunes = afterRunes[:maxRunes]
	}
	return string(beforeRunes), string(afterRunes)
}

func sourceSnapshots(sources []evidence.Source, now time.Time) []RewriteSourceSnapshot {
	result := make([]RewriteSourceSnapshot, 0, len(sources))
	for _, source := range sources {
		result = append(result, RewriteSourceSnapshot{SourceID: source.ID, AssetID: source.AssetID,
			AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash, AuthorizedAt: now})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result
}

func validateRewriteOutput(output SelectedRewriteOutput, requested []string) error {
	if strings.TrimSpace(output.ReplacementMarkdown) == "" || len(output.ReplacementMarkdown) > 40000 ||
		(output.RunMode != "" && !candidateadoption.ValidRunMode(candidateadoption.RunMode(output.RunMode))) {
		return ErrRewriteInvalidRequest
	}
	allowed := make(map[string]bool, len(requested))
	for _, id := range requested {
		allowed[id] = true
	}
	if err := sourcemarker.Validate(output.ReplacementMarkdown); err != nil {
		return ErrRewriteInvalidRequest
	}
	refs := sourcemarker.IDs(output.ReplacementMarkdown)
	for _, id := range output.SourceIDs {
		if !allowed[id] {
			return ErrRewriteSourceDenied
		}
	}
	if !slices.Equal(stringSet(refs), stringSet(output.SourceIDs)) {
		return ErrRewriteInvalidRequest
	}
	for _, item := range output.ReviewItems {
		if strings.TrimSpace(item.Statement) == "" {
			return ErrRewriteInvalidRequest
		}
	}
	// Do not offer an AI result for adoption if the current DOCX renderer
	// cannot preserve its structure. Keep the same preflight/export boundary.
	return docx.ValidateMarkdown(output.ReplacementMarkdown)
}

func (s *SelectedRewriteService) fetchSources(ctx context.Context, actor Actor, projectID string, ids []string) ([]evidence.Source, []RewriteSourceSnapshot, error) {
	if len(ids) > 12 || !slices.Equal(ids, stringSet(ids)) {
		return nil, nil, ErrRewriteInvalidRequest
	}
	items := make([]evidence.Source, 0, len(ids))
	for _, id := range ids {
		source, err := s.sources.GetSource(ctx, actor, projectID, id)
		if err != nil || source.ProjectID != projectID || source.Status != evidence.SourceAvailable {
			return nil, nil, ErrRewriteSourceDenied
		}
		items = append(items, source)
	}
	return items, sourceSnapshots(items, s.now()), nil
}

func (s *SelectedRewriteService) recheckSources(ctx context.Context, actor Actor, projectID string, snapshots []RewriteSourceSnapshot) error {
	for _, snapshot := range snapshots {
		source, err := s.sources.GetSource(ctx, actor, projectID, snapshot.SourceID)
		if err != nil || source.ProjectID != projectID || source.Status != evidence.SourceAvailable ||
			source.AssetID != snapshot.AssetID || source.AssetRevision != snapshot.AssetRevision ||
			source.Locator != snapshot.Locator || source.QuotedTextHash != snapshot.QuotedTextHash {
			return ErrRewriteSourceDenied
		}
	}
	return nil
}

func workingCopyIsCandidateBasis(copy core.WorkingCopy, row selectedRewriteRow) bool {
	return copy.WorkingCopyRevision == row.WorkingCopyRevision && copy.SpecRevision == row.SpecRevision &&
		sameOptionalVersion(copy.BaseChapterVersionID, row.BaseChapterVersionID)
}

func sameOptionalVersion(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *SelectedRewriteService) staleIfNeeded(ctx context.Context, actor Actor, projectID, chapterID string, row selectedRewriteRow) (selectedRewriteRow, error) {
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		return selectedRewriteRow{}, err
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		return selectedRewriteRow{}, err
	}
	if !workingCopyIsCandidateBasis(copy, row) || project.SpecRevision != row.SpecRevision {
		return s.store.markStale(ctx, row, s.now())
	}
	return row, nil
}

func (s *SelectedRewriteService) createRow(actor Actor, projectID, chapterID, key string, input SelectedRewriteRequest, hash string, sources []RewriteSourceSnapshot, plan *writingaction.Plan) (selectedRewriteRow, error) {
	sourceJSON, _ := json.Marshal(input.SourceIDs)
	evidenceJSON, _ := json.Marshal(input.EvidenceCardRefs)
	snapshotJSON, _ := json.Marshal(sources)
	writingAction, origin := "", ""
	if plan != nil {
		writingAction = string(plan.Action)
		// 改写是模型产出：候选正文的来源是 AI 生成。
		origin = string(writingaction.OriginGenerated)
	}
	row := selectedRewriteRow{ID: uuid.NewString(), TenantID: actor.TenantID, ActorID: actor.UserID,
		ProjectID: projectID, ChapterID: chapterID, IdempotencyKey: key, RequestHash: hash,
		Validity: "", RunMode: "", BaseChapterVersionID: input.BaseChapterVersionID,
		SpecRevision: input.ExpectedSpecRevision, WorkingCopyRevision: input.ExpectedWorkingCopyRevision,
		StartUTF16: input.Selection.StartUTF16, EndUTF16: input.Selection.EndUTF16, SelectedText: input.Selection.SelectedText,
		EvidenceCardRefsJSON: string(evidenceJSON), Instruction: input.Instruction, SourceIDsJSON: string(sourceJSON), AuthorizedSourcesJSON: string(snapshotJSON),
		ReviewItemsJSON: "[]", WritingAction: writingAction, Origin: origin, CreatedAt: s.now(), UpdatedAt: s.now()}
	return row, nil
}

func writeRewriteResult(row *selectedRewriteRow, output SelectedRewriteOutput) error {
	if err := validateRewriteOutput(output, mustDecodeIDs(row.SourceIDsJSON)); err != nil {
		return err
	}
	items := make([]candidateadoption.ReviewItem, 0, len(output.ReviewItems))
	for _, item := range output.ReviewItems {
		// 编号由服务端给：模型只负责说清「有哪些待核事项」，身份是服务端的事实。
		items = append(items, candidateadoption.ReviewItem{ID: uuid.NewString(),
			Statement: strings.TrimSpace(item.Statement), OriginCandidateID: row.ID})
	}
	ids := stringSet(output.SourceIDs)
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	allowedSnapshots := make(map[string]RewriteSourceSnapshot)
	for _, snapshot := range mustDecodeSnapshots(row.AuthorizedSourcesJSON) {
		allowedSnapshots[snapshot.SourceID] = snapshot
	}
	usedSnapshots := make([]RewriteSourceSnapshot, 0, len(ids))
	for _, id := range ids {
		snapshot, ok := allowedSnapshots[id]
		if !ok {
			return ErrRewriteSourceDenied
		}
		usedSnapshots = append(usedSnapshots, snapshot)
	}
	snapshotsJSON, err := json.Marshal(usedSnapshots)
	if err != nil {
		return err
	}
	review, err := json.Marshal(items)
	if err != nil {
		return err
	}
	row.ReplacementMarkdown, row.SourceIDsJSON, row.AuthorizedSourcesJSON, row.ReviewItemsJSON = output.ReplacementMarkdown, string(idsJSON), string(snapshotsJSON), string(review)
	row.Validity, row.RunMode, row.ErrorCode = string(candidateadoption.ValidityFresh), candidateadoption.RunMode(output.RunMode), ""
	return nil
}

func mustDecodeIDs(raw string) []string {
	var ids []string
	_ = json.Unmarshal([]byte(raw), &ids)
	return ids
}

func mustDecodeSnapshots(raw string) []RewriteSourceSnapshot {
	var snapshots []RewriteSourceSnapshot
	_ = json.Unmarshal([]byte(raw), &snapshots)
	return snapshots
}

func mustDecodeReviewItems(raw string) []candidateadoption.ReviewItem {
	var items []candidateadoption.ReviewItem
	_ = json.Unmarshal([]byte(raw), &items)
	return items
}

// reviewItemsForWorkingCopy 把候选层的待核事项转成工作副本侧的类型。
//
// 转换点在 workspace 包内：候选层的待核事项只有一种类型，工作副本侧保留
// 自己的类型（开发计划 A39 / I-9）。两型的 JSON 形状逐字相同，所以落库的
// 格式不变，改的是 Go 类型。
func reviewItemsForWorkingCopy(items []candidateadoption.ReviewItem) []core.ReviewItem {
	converted := make([]core.ReviewItem, 0, len(items))
	for _, item := range items {
		converted = append(converted, core.ReviewItem{ID: item.ID, Statement: item.Statement,
			OriginCandidateID: item.OriginCandidateID, Anchor: item.Anchor})
	}
	return converted
}

func (s *SelectedRewriteService) updateCandidate(ctx context.Context, row selectedRewriteRow) (SelectedRewriteCandidate, error) {
	if err := s.store.update(ctx, row); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	return selectedRewriteView(row)
}

func selectedRewriteRequestHash(input SelectedRewriteRequest) (string, error) {
	digest, err := operationcore.RequestHash(input)
	if err != nil {
		return "", ErrRewriteInvalidRequest
	}
	return digest, nil
}

func selectedRewriteFailureCode(err error) string {
	switch {
	case errors.Is(err, docx.ErrUnsupportedMarkdown):
		return "unsupported_format"
	case errors.Is(err, ErrRewriteSourceDenied):
		return "source_access_denied"
	case errors.Is(err, ErrRewriteStale):
		return "stale_input"
	case errors.Is(err, ErrRewriteUnavailable):
		return "model_unavailable"
	default:
		return "rewrite_failed"
	}
}

func selectedRewriteContextError(err error) error {
	if errors.Is(err, core.ErrNotFound) {
		return ErrRewriteNotFound
	}
	if errors.Is(err, core.ErrVersionConflict) {
		return ErrRewriteConflict
	}
	if errors.Is(err, core.ErrSourceUnavailable) {
		return ErrRewriteSourceDenied
	}
	return err
}

func validateSelectedRewriteInput(key string, in SelectedRewriteRequest) error {
	if !operationcore.ValidKey(key) || in.ExpectedSpecRevision < 0 || in.ExpectedWorkingCopyRevision < 1 ||
		len([]rune(in.Instruction)) > 2000 || len(in.SourceIDs) > 12 || !validRewriteEvidenceRefs(in.EvidenceCardRefs) ||
		in.Selection.EndUTF16 <= in.Selection.StartUTF16 || in.Selection.StartUTF16 < 0 || in.Selection.SelectedText == "" {
		return ErrRewriteInvalidRequest
	}
	// 旧路径的说明是必填的自由文本；动作路径的说明只是补充说明，必填的是
	// 动作与产出参数（由执行计划校验），所以这里不为动作路径要求它。
	if in.isLegacyPath() && strings.TrimSpace(in.Instruction) == "" {
		return ErrRewriteInvalidRequest
	}
	ids := slices.Clone(in.SourceIDs)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return ErrRewriteInvalidRequest
		}
	}
	if !slices.Equal(ids, stringSet(ids)) {
		return ErrRewriteInvalidRequest
	}
	return nil
}

func operationKeyForApply(key, candidateID string) string {
	digest := sha256.Sum256([]byte(candidateID + "\x00" + key))
	return "g7-apply-" + hex.EncodeToString(digest[:])
}

// resolveRewritePlan 判定这次请求走哪条路径，并解析动作路径的执行计划。
//
// 三个新字段全部缺省 ⇒ 旧路径（plan 为 nil）：自由文本说明驱动，不记录动作。
// 任一字段出现 ⇒ 动作路径：动作必填、产出参数必齐、范围只能是选区。
//
// 动作语义一律由 writingaction.PlanFor 解析：执行方不按动作名分支，也不自己
// 解释产出参数。
func resolveRewritePlan(input SelectedRewriteRequest) (*writingaction.Plan, error) {
	if input.isLegacyPath() {
		return nil, nil
	}
	if input.WritingAction == nil {
		return nil, ErrRewriteInvalidRequest
	}
	scope := writingaction.ScopeSection
	if input.Scope != nil {
		scope = *input.Scope
	}
	params := writingaction.OutputParams{}
	if input.OutputParams != nil {
		params = *input.OutputParams
	}
	plan, err := writingaction.PlanFor(*input.WritingAction, scope, params)
	if err != nil {
		return nil, err
	}
	// 这个端点只实现选段管道声明的那几个动作（改写、续写、压缩、扩展与转写）。
	// 别的已知动作由它们自己的端点负责。动作身份引用管道常量，不写字符串字面量，
	// 也不写具体动作常量——执行方因此在语法上就没法按动作名分支（A33）。
	pipelineActions, err := writingaction.ActionsFor(writingaction.PipelineRewrite)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pipelineActions, plan.Action) {
		return nil, ErrRewriteInvalidRequest
	}
	// 这个端点只写选段：范围是**管道**的声明，不是动作的声明——同一个动作
	// （转写）在动作层面允许整章，在这条管道里只做选段。声明了整章的请求在
	// 模型调用之前就被拒。
	pipelineScopes, err := writingaction.ScopesFor(writingaction.PipelineRewrite)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pipelineScopes, scope) {
		return nil, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("invalid_request: pipeline %q does not allow scope %q",
				writingaction.PipelineRewrite, scope), writingaction.ErrScopeNotAllowed)
	}
	return &plan, nil
}

// rewritePrompt 按路径组装给模型的提示。
//
// 它自己不组装提示：两条路径各自组装，所以写给模型的指令只有一个来源。
func rewritePrompt(input SelectedRewriteRequest, plan *writingaction.Plan, basis g2RewriteTemplateBasis,
	constraints []generation.EvidenceConstraint, before, after string, sources []evidence.Source) (SelectedRewritePrompt, error) {
	if plan != nil {
		return actionRewritePrompt(input, *plan, basis, constraints, before, after, sources)
	}
	return legacyRewritePrompt(input, basis, constraints, before, after, sources), nil
}

// actionRewritePrompt 组装动作路径的提示。
//
// 指令只来自执行计划：执行方按 Plan.Context 声明的类别组装上下文，把渲染
// 出来的动作指令交给模型。它不按动作名分支，也不自己解释产出参数。
//
// 客户端的补充说明单独放一个字段：它不参与动作判定，也不与动作指令混成一段。
func actionRewritePrompt(input SelectedRewriteRequest, plan writingaction.Plan, basis g2RewriteTemplateBasis,
	constraints []generation.EvidenceConstraint, before, after string, sources []evidence.Source) (SelectedRewritePrompt, error) {
	instruction, err := writingaction.RenderInstruction(plan, writingaction.RenderContext{
		Section:  selectedRewriteSectionContext(input.Selection, before, after),
		Template: selectedRewriteTemplateContext(basis),
	})
	if err != nil {
		return SelectedRewritePrompt{}, err
	}
	return SelectedRewritePrompt{
		TemplateBasis: basis, EvidenceCardRefs: input.EvidenceCardRefs, EvidenceConstraints: constraints,
		Selection: input.Selection, Instruction: instruction, SupplementaryNote: input.Instruction,
		ContextBefore: before, ContextAfter: after, Sources: sources,
	}, nil
}

// legacyRewritePrompt 组装旧路径的提示。
//
// 客户端的自由文本说明原样作为指令：旧路径的行为逐字不变。
func legacyRewritePrompt(input SelectedRewriteRequest, basis g2RewriteTemplateBasis,
	constraints []generation.EvidenceConstraint, before, after string, sources []evidence.Source) SelectedRewritePrompt {
	return SelectedRewritePrompt{
		TemplateBasis: basis, EvidenceCardRefs: input.EvidenceCardRefs, EvidenceConstraints: constraints,
		Selection: input.Selection, Instruction: input.Instruction,
		ContextBefore: before, ContextAfter: after, Sources: sources,
	}
}

// selectedRewriteSectionContext 渲染「目标选区及其上下文」这一类上下文。
//
// 它是**上下文**，不是动作语义：动作说什么由词汇包渲染，这里只回答
// 「有没有选区上下文」。
func selectedRewriteSectionContext(selection RewriteSelection, before, after string) string {
	return strings.TrimSpace(selection.SelectedText + "\n" + before + "\n" + after)
}

// selectedRewriteTemplateContext 渲染「模板与规则」这一类上下文。
//
// 「没有绑定模板」也是一个事实——它告诉模型这次改写不受项目模板约束——
// 所以这一槽永远非空：否则「缺少必需上下文」与「没有模板」会分不开。
func selectedRewriteTemplateContext(basis g2RewriteTemplateBasis) string {
	if basis.CopyID == "" {
		return "本项目未绑定模板，改写不受项目模板约束。"
	}
	return fmt.Sprintf("模板副本 %s@%d，内容哈希 %s，规则集哈希 %s，上下文修订号 %d。",
		basis.CopyID, basis.CopyVersion, basis.ContentHash, basis.RulesetHash, basis.ContextRevision)
}

func selectedRewriteContextWindow(body string, selection RewriteSelection) (int, int, string, string, bool) {
	start, end, ok := selectionByteRange(body, selection)
	if !ok {
		return 0, 0, "", "", false
	}
	before, after := selectedRewriteContext(body, start, end, 500)
	return start, end, before, after, true
}

func (s *SelectedRewriteService) String() string {
	return fmt.Sprintf("selected-rewrite(%T)", s.model)
}
