package workspace

// 语言评审：评审报告、预览、拒绝与显式应用。
//
// 评审与其他动作根本不同：它的产出是一份**报告**，不是一份候选。报告没有可采纳
// 物（动作的许可位是空集），所以它不进候选表，也进不了采纳那条路。用户在预览
// 之后显式确认，才由一个**独立**的应用入口把它写进工作副本。
//
// 共享的机制不重写：人工门禁（workspacecore.RequireHumanDecision）、工作副本
// 的四元 CAS（workspacecore.ApplyRewrite）、幂等键、事务内的授权复核，以及逐条
// 处置的既有模型（candidateadoption.ReviewDecision）。这一票只做「评审」自己的
// 那一半：报告的生命周期、基线、报告版本与撤权状态。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

var (
	// ErrReviewInvalidRequest 说评审请求的字段不符合约定。
	ErrReviewInvalidRequest = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request",
		Message: "评审请求字段不符合约定。", Retryable: false}, "review_report_invalid_request")
	// ErrReviewNotFound 说这份报告不存在或不可访问。
	ErrReviewNotFound = clienterror.New(clienterror.Form{Status: 404, Code: "not_found",
		Message: "评审报告不存在或不可访问。", Retryable: false}, "review_report_not_found")
	// ErrReviewBaselineConflict 说基线变了：报告是针对哪一份正文写的，现在
	// 那一份已经不在了。
	//
	// 它是冲突而不是覆盖：评审意见挂在具体的措辞上，正文变了，那些意见说的
	// 已经不是同一句话了。
	ErrReviewBaselineConflict = clienterror.New(clienterror.Form{Status: 409, Code: "version_conflict",
		Message: "正文或基线已变化，请重新运行语言评审。", Retryable: false}, "review_report_baseline_conflict")
	// ErrReviewNotAuthorized 说报告所依据的授权已经失效。
	//
	// 下一步写在文案里：恢复授权后重新运行评审。
	ErrReviewNotAuthorized = clienterror.New(clienterror.Form{Status: 403, Code: "source_access_denied",
		Message: "评审报告所依据的授权已失效，请恢复授权后重新运行语言评审。", Retryable: false},
		"review_report_not_authorized")
	// ErrReviewAlreadyApplied 说这份报告已经被应用过了。
	ErrReviewAlreadyApplied = clienterror.New(clienterror.Form{Status: 409, Code: "invalid_state",
		Message: "这份评审报告已经被应用过了。", Retryable: false}, "review_report_already_applied")
)

// ReviewReportState 是报告的生命周期状态。
//
// 它是**生命周期**，不是有效性：有效性回答「这个产出还能不能被用」，由候选层
// 的枚举承载（fresh/stale/not_authorized）。这里回答的是这份报告走到了哪一步。
type ReviewReportState string

const (
	// ReviewReportReady 报告已生成，可以预览、可以应用。
	ReviewReportReady ReviewReportState = "ready"
	// ReviewReportApplied 报告已被显式应用。
	ReviewReportApplied ReviewReportState = "applied"
	// ReviewReportRejected 用户拒绝了这份报告。
	ReviewReportRejected ReviewReportState = "rejected"
)

// ReviewBaseline 是应用报告时要复核的那个基线。
//
// 它是「用户当时看到的是哪一份」：工作副本的修订号、正文摘要、章节的基线版本
// 与研究条件版本。基线变了 ⇒ 报告作废，不覆盖。
type ReviewBaseline struct {
	// WorkingCopyRevision 是报告所依据的工作副本修订号。
	WorkingCopyRevision int64 `json:"working_copy_revision"`
	// BaseChapterVersionID 是报告所依据的章节版本。
	BaseChapterVersionID *string `json:"base_chapter_version_id"`
	// SpecRevision 是报告所依据的研究条件版本。
	SpecRevision int64 `json:"spec_revision"`
	// BodyHash 是报告所依据的正文摘要（正文 UTF-8 字节的 SHA-256，小写十六进制）。
	BodyHash string `json:"body_hash"`
}

// ReviewReport 是一份语言评审报告。
type ReviewReport struct {
	ReportID  string `json:"report_id"`
	ProjectID string `json:"project_id"`
	ChapterID string `json:"chapter_id"`
	// Findings 是评审意见：待核事项与问题清单。
	//
	// 它们是候选层的那一种待核事项类型（开发计划 A39）：报告不是候选，但它
	// 交出去的每一条意见最后都要落进工作副本的待核事项里。
	Findings []candidateadoption.ReviewItem `json:"findings"`
	// SourceIDs 是这次评审所依据的来源。
	SourceIDs []string `json:"source_ids"`
	// AuthorizedSources 是这些来源在生成报告时的授权快照。
	AuthorizedSources []RewriteSourceSnapshot `json:"authorized_sources"`
	// Baseline 是应用时要复核的那个基线。
	Baseline ReviewBaseline `json:"baseline"`
	// ReportVersion 是报告自身的版本标识：它由报告的内容算出，因此可复算。
	ReportVersion string `json:"report_version"`
	// AuthorizationState 是生成报告时的授权状态。
	//
	// 应用时要按**当前**授权复核一遍：生成时还有权限，不等于现在还有。
	AuthorizationState candidateadoption.Validity `json:"authorization_state"`
	// RunMode 说这一份报告是怎么到模型面前的。
	RunMode candidateadoption.RunMode `json:"run_mode"`
	// WritingAction 是产生这份报告的动作。
	WritingAction writingaction.Action `json:"writing_action"`
	// Origin 是报告的来源。
	Origin writingaction.Origin `json:"origin"`
	// State 是报告走到了哪一步。
	State ReviewReportState `json:"state"`
	// ReviewDecisions 是应用时逐条处置的记录。
	ReviewDecisions []candidateadoption.ReviewDecision `json:"review_decisions"`
	// ErrorCode 是这次运行失败或失效的原因。
	ErrorCode string `json:"error_code"`
	// Scope 是这次评审的目标范围。
	Scope writingaction.Scope `json:"scope"`
	// Selection 是被评审的那一段（整章范围时是整份正文）。
	Selection RewriteSelection `json:"selection"`
	// DroppedContext 是用户选了、但没有进入模型请求的可选上下文条目。
	DroppedContext []DroppedContextItem `json:"dropped_context,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
}

// ReviewReportRequest 是发起一次语言评审的请求。
type ReviewReportRequest struct {
	// Scope 是这次评审的目标范围：整章或选段。
	Scope writingaction.Scope `json:"scope"`
	// Selection 是选段范围下的选区；整章范围不需要它。
	Selection *RewriteSelection `json:"selection,omitempty"`
	// Context 是用户对这次执行要纳入的可选上下文的选择。
	Context *RewriteContextSelection `json:"context,omitempty"`
	// ExpectedSpecRevision 是用户看到的研究条件版本。
	ExpectedSpecRevision int64 `json:"expected_spec_revision"`
	// ExpectedWorkingCopyRevision 是用户看到的工作副本修订号。
	ExpectedWorkingCopyRevision int64 `json:"expected_working_copy_revision"`
	// BaseChapterVersionID 是用户看到的章节版本。
	BaseChapterVersionID *string `json:"base_chapter_version_id"`
	// Instruction 是客户端的补充说明：它不参与动作判定。
	Instruction string `json:"instruction"`
}

// ApplyReviewInput 是应用一份评审报告的请求。
type ApplyReviewInput struct {
	// ExpectedSpecRevision 是用户看到的研究条件版本。
	ExpectedSpecRevision int64 `json:"expected_spec_revision"`
	// ExpectedWorkingCopyRevision 是用户看到的工作副本修订号。
	ExpectedWorkingCopyRevision int64 `json:"expected_working_copy_revision"`
	// BaseChapterVersionID 是用户看到的章节版本。
	BaseChapterVersionID *string `json:"base_chapter_version_id"`
	// ReviewDecisions 是逐条处置：每一条评审意见都要有处置与理由。
	ReviewDecisions []candidateadoption.ReviewDecision `json:"review_decisions"`
}

// ReviewReportApplication 是语言评审这一条路径的应用接口。
//
// 它与选段候选的应用接口分开：评审报告不是候选，走的是另一条落地路径。
type ReviewReportApplication interface {
	CreateReviewReport(context.Context, Actor, string, string, string, ReviewReportRequest) (ReviewReport, error)
	GetReviewReport(context.Context, Actor, string, string) (ReviewReport, error)
	RejectReviewReport(context.Context, Actor, string, string, string) (ReviewReport, error)
	ApplyReviewReport(context.Context, Actor, string, string, string, string, ApplyReviewInput) (core.WorkingCopy, bool, error)
}

// reviewReportRow 是一份评审报告的存储行。
type reviewReportRow struct {
	ID                      string  `gorm:"primaryKey;size:36"`
	TenantID                uint64  `gorm:"not null;index;uniqueIndex:idx_lingdoc_review_reports_idempotency,priority:1"`
	ActorID                 string  `gorm:"not null;size:64;uniqueIndex:idx_lingdoc_review_reports_idempotency,priority:2"`
	ProjectID               string  `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_review_reports_idempotency,priority:3"`
	ChapterID               string  `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_review_reports_idempotency,priority:4"`
	IdempotencyKey          string  `gorm:"not null;size:128;uniqueIndex:idx_lingdoc_review_reports_idempotency,priority:5"`
	RequestHash             string  `gorm:"not null;size:64"`
	State                   string  `gorm:"not null;size:16;index"`
	ReportVersion           string  `gorm:"not null;size:64"`
	Scope                   string  `gorm:"not null;size:16"`
	StartUTF16              int     `gorm:"not null"`
	EndUTF16                int     `gorm:"not null"`
	SelectedText            string  `gorm:"not null;type:text"`
	BaseChapterVersionID    *string `gorm:"size:36"`
	SpecRevision            int64   `gorm:"not null"`
	WorkingCopyRevision     int64   `gorm:"not null"`
	BodyHash                string  `gorm:"not null;size:64"`
	FindingsJSON            string  `gorm:"not null;type:text;default:'[]'"`
	SourceIDsJSON           string  `gorm:"not null;type:text;default:'[]'"`
	AuthorizedSourcesJSON   string  `gorm:"not null;type:text;default:'[]'"`
	EvidenceCardRefsJSON    string  `gorm:"not null;type:text;default:'[]'"`
	EvidenceConstraintsJSON string  `gorm:"not null;type:text;default:'[]'"`
	TemplateBasisJSON       string  `gorm:"not null;type:text;default:'{}'"`
	WritingAction           string  `gorm:"not null;size:64;default:''"`
	Origin                  string  `gorm:"not null;size:32;default:''"`
	RunMode                 string  `gorm:"not null;size:32;default:''"`
	AuthorizationState      string  `gorm:"not null;size:16;default:''"`
	ErrorCode               string  `gorm:"not null;size:64;default:''"`
	DecisionsJSON           string  `gorm:"not null;type:text;default:'[]'"`
	AppliedKey              string  `gorm:"not null;size:128;default:''"`
	AppliedResponseJSON     string  `gorm:"not null;type:text;default:''"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (reviewReportRow) TableName() string { return "lingdoc_review_reports" }

// reviewReportStore 是评审报告的存储。
type reviewReportStore struct{ db *gorm.DB }

// autoMigrate 在测试里建这张表。生产库由迁移建它（migrations/versioned/000129、
// migrations/sqlite/000050）。
func (s *reviewReportStore) autoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&reviewReportRow{})
}

// createOrReplay 按幂等键写入报告：同键同摘要 ⇒ 重放；同键不同摘要 ⇒ 冲突。
func (s *reviewReportStore) createOrReplay(ctx context.Context, row reviewReportRow) (reviewReportRow, bool, error) {
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
		var existing reviewReportRow
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

func (s *reviewReportStore) byID(ctx context.Context, tenantID uint64, projectID, reportID string) (reviewReportRow, error) {
	var row reviewReportRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, projectID, reportID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return reviewReportRow{}, ErrReviewNotFound
	}
	return row, err
}

func (s *reviewReportStore) update(ctx context.Context, row reviewReportRow) error {
	return s.db.WithContext(ctx).Model(&reviewReportRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"state": row.State, "authorization_state": row.AuthorizationState, "error_code": row.ErrorCode,
		"decisions_json": row.DecisionsJSON, "applied_key": row.AppliedKey,
		"applied_response_json": row.AppliedResponseJSON, "updated_at": row.UpdatedAt,
	}).Error
}

// CreateReviewReport 生成一份语言评审报告。
//
// 报告**不写工作副本**：它是一份待用户处置的意见清单。用户预览之后显式确认，
// 才由 ApplyReviewReport 把它写进工作副本的待核事项。
func (s *SelectedRewriteService) CreateReviewReport(ctx context.Context, actor Actor, projectID, chapterID, key string, input ReviewReportRequest) (ReviewReport, error) {
	auditDetails := map[string]any{"chapter_id": chapterID, "idempotency_key": key,
		"spec_revision": input.ExpectedSpecRevision, "working_copy_revision": input.ExpectedWorkingCopyRevision}
	auditDecision, auditReason := "allow", ""
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.review.create", auditDecision, auditReason, auditDetails)
	}()
	if err := core.RequireHumanDecision(ctx); err != nil {
		return ReviewReport{}, err
	}
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || s.model == nil ||
		actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" ||
		strings.TrimSpace(projectID) == "" || strings.TrimSpace(chapterID) == "" {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return ReviewReport{}, ErrRewriteUnavailable
	}
	key = strings.TrimSpace(key)
	if !operationcore.ValidKey(key) {
		auditDecision, auditReason = "deny", ErrReviewInvalidRequest.Error()
		return ReviewReport{}, ErrReviewInvalidRequest
	}
	// 动作语义一律由词汇包解析：执行方不按动作名分支，也不自己解释产出参数。
	plan, err := resolveReviewPlan(input)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, selectedRewriteContextError(err)
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, selectedRewriteContextError(err)
	}
	if project.Status != "active" || project.SpecRevision != input.ExpectedSpecRevision ||
		copy.SpecRevision != input.ExpectedSpecRevision ||
		copy.WorkingCopyRevision != input.ExpectedWorkingCopyRevision ||
		!sameOptionalVersion(copy.BaseChapterVersionID, input.BaseChapterVersionID) {
		auditDecision, auditReason = "deny", ErrRewriteConflict.Error()
		return ReviewReport{}, ErrRewriteConflict
	}
	// 评审对象是工作副本里的一段：整章范围是整份正文，选段范围是用户选中的
	// 那一段。范围之外的内容不进模型请求。
	target, err := reviewTarget(copy.BodyMarkdown, plan.Scope, input.Selection)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	before, after := "", ""
	if plan.Scope == writingaction.ScopeSection {
		before, after = selectedRewriteContext(copy.BodyMarkdown, target.start, target.end, 500)
	}
	// 被评审的正文里已有的来源标记仍是这次评审的依据：意见说的可能就是这些
	// 句子。继承来的来源先并进来，再按用户的上下文选择剔除。
	sourceIDs := stringSet(sourcemarker.IDs(target.selected))
	var dropped []DroppedContextItem
	var evidenceCards []generation.EvidenceCardRef
	if input.Context != nil {
		includedSources, includedCards, droppedItems, contextErr := s.resolveRewriteContext(ctx, actor, projectID, chapterID, *plan, *input.Context)
		if contextErr != nil {
			auditDecision, auditReason = "deny", contextErr.Error()
			return ReviewReport{}, contextErr
		}
		dropped = droppedItems
		evidenceCards = includedCards
		sourceIDs = stringSet(append(sourceIDs, includedSources...))
	}
	sourceIDs = s.removeRewriteExcludedSources(ctx, actor, projectID, sourceIDs, input.Context)
	sourceIDs = stringSet(sourceIDs)
	sources, snapshots, err := s.fetchSources(ctx, actor, projectID, sourceIDs)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	if err := g2RewriteModelScope(ctx, s.sources, actor, projectID, snapshots); err != nil {
		return ReviewReport{}, err
	}
	var constraints []generation.EvidenceConstraint
	if len(evidenceCards) > 0 {
		selected, selectErr := g2SelectFormal(ctx, s.workspace, actor, projectID, chapterID, g2GenerationRefs(evidenceCards))
		if selectErr != nil {
			auditDecision, auditReason = "deny", ErrRewriteSourceDenied.Error()
			return ReviewReport{}, ErrRewriteSourceDenied
		}
		constraints, selectErr = g2EvidenceConstraints(selected, evidenceCards)
		if selectErr != nil {
			auditDecision, auditReason = "deny", ErrRewriteSourceDenied.Error()
			return ReviewReport{}, ErrRewriteSourceDenied
		}
	}
	mode := candidateadoption.RunMode(s.model.RunMode())
	if !candidateadoption.ValidRunMode(mode) {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return ReviewReport{}, ErrRewriteUnavailable
	}
	templateBasis, err := g2RewriteProjectBasis(project)
	if err != nil {
		return ReviewReport{}, err
	}
	prompt, err := reviewPrompt(*plan, target, templateBasis, constraints, evidenceCards, before, after, sources, input.Instruction)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	output, modelErr := s.model.RewriteSelected(ctx, actor, prompt)
	if modelErr != nil {
		auditDecision, auditReason = "deny", "review_failed"
		auditDetails["error_code"] = "review_failed"
		return ReviewReport{}, ErrRewriteUnavailable
	}
	if output.RunMode != "" {
		if !candidateadoption.ValidRunMode(candidateadoption.RunMode(output.RunMode)) {
			auditDecision, auditReason = "deny", "invalid_run_mode"
			return ReviewReport{}, ErrReviewInvalidRequest
		}
		mode = candidateadoption.RunMode(output.RunMode)
	}
	// 报告的标识先定下来：每一条评审意见都要带着它作为出处。
	reportID := uuid.NewString()
	findings, err := reviewFindings(output, sourceIDs, reportID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	// 授权在生成之后可能就没了：应用时要按当前授权复核，所以这里先记下生成
	// 时的授权状态。
	authorizationState := candidateadoption.ValidityFresh
	if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
		authorizationState = candidateadoption.ValidityNotAuthorized
	}
	hash, err := reviewRequestHash(input)
	if err != nil {
		return ReviewReport{}, err
	}
	baseline := ReviewBaseline{
		WorkingCopyRevision:  copy.WorkingCopyRevision,
		BaseChapterVersionID: copy.BaseChapterVersionID,
		SpecRevision:         copy.SpecRevision,
		BodyHash:             reviewBodyHash(copy.BodyMarkdown),
	}
	version, err := reviewReportVersion(findings, baseline, mode, sourceIDs)
	if err != nil {
		return ReviewReport{}, err
	}
	findingsJSON, err := json.Marshal(findings)
	if err != nil {
		return ReviewReport{}, err
	}
	sourceJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return ReviewReport{}, err
	}
	snapshotJSON, err := json.Marshal(snapshots)
	if err != nil {
		return ReviewReport{}, err
	}
	templateJSON, err := json.Marshal(templateBasis)
	if err != nil {
		return ReviewReport{}, err
	}
	cardJSON, err := json.Marshal(evidenceCards)
	if err != nil {
		return ReviewReport{}, err
	}
	constraintJSON, err := json.Marshal(constraints)
	if err != nil {
		return ReviewReport{}, err
	}
	row := reviewReportRow{
		ID: reportID, TenantID: actor.TenantID, ActorID: actor.UserID,
		ProjectID: projectID, ChapterID: chapterID, IdempotencyKey: key, RequestHash: hash,
		State: string(ReviewReportReady), ReportVersion: version, Scope: string(plan.Scope),
		StartUTF16: target.start, EndUTF16: target.end, SelectedText: target.selected,
		BaseChapterVersionID: copy.BaseChapterVersionID, SpecRevision: copy.SpecRevision,
		WorkingCopyRevision: copy.WorkingCopyRevision, BodyHash: baseline.BodyHash,
		FindingsJSON: string(findingsJSON), SourceIDsJSON: string(sourceJSON),
		AuthorizedSourcesJSON: string(snapshotJSON), EvidenceCardRefsJSON: string(cardJSON),
		EvidenceConstraintsJSON: string(constraintJSON), TemplateBasisJSON: string(templateJSON),
		WritingAction: string(plan.Action),
		Origin:        string(writingaction.OriginGenerated), RunMode: string(mode),
		AuthorizationState: string(authorizationState), DecisionsJSON: "[]",
		CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	stored, _, err := s.reviewStore().createOrReplay(ctx, row)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	auditDetails["report_id"], auditDetails["run_mode"] = stored.ID, stored.RunMode
	report, err := reviewReportView(stored)
	if err != nil {
		return ReviewReport{}, err
	}
	report.DroppedContext = dropped
	return report, nil
}

// GetReviewReport 读回一份评审报告（预览）。
func (s *SelectedRewriteService) GetReviewReport(ctx context.Context, actor Actor, projectID, reportID string) (ReviewReport, error) {
	details := map[string]any{"report_id": reportID}
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.review.get", "allow", "", details)
	}()
	if s == nil || s.workspace == nil || actor.TenantID == 0 {
		return ReviewReport{}, ErrRewriteUnavailable
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "read"); err != nil {
		return ReviewReport{}, err
	}
	row, err := s.reviewStore().byID(ctx, actor.TenantID, projectID, reportID)
	if err != nil {
		return ReviewReport{}, err
	}
	// 读报告也要复核授权：一份依赖已经没了授权的报告，读出来就该说清楚。
	if err := s.recheckSources(ctx, actor, projectID, mustDecodeSnapshots(row.AuthorizedSourcesJSON)); err != nil {
		return ReviewReport{}, ErrReviewNotAuthorized
	}
	return reviewReportView(row)
}

// RejectReviewReport 拒绝一份评审报告。
//
// 拒绝**不改工作副本**：它只是把这份报告标记成不再可应用。
func (s *SelectedRewriteService) RejectReviewReport(ctx context.Context, actor Actor, projectID, reportID, key string) (ReviewReport, error) {
	details := map[string]any{"report_id": reportID, "idempotency_key": key}
	auditDecision, auditReason := "allow", ""
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.review.reject", auditDecision, auditReason, details)
	}()
	if err := core.RequireHumanDecision(ctx); err != nil {
		return ReviewReport{}, err
	}
	if s == nil || s.workspace == nil || actor.TenantID == 0 || !operationcore.ValidKey(strings.TrimSpace(key)) {
		return ReviewReport{}, ErrReviewInvalidRequest
	}
	row, err := s.reviewStore().byID(ctx, actor.TenantID, projectID, reportID)
	if err != nil {
		return ReviewReport{}, err
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+row.ChapterID); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	if row.State == string(ReviewReportApplied) {
		auditDecision, auditReason = "deny", ErrReviewAlreadyApplied.Error()
		return ReviewReport{}, ErrReviewAlreadyApplied
	}
	row.State = string(ReviewReportRejected)
	row.UpdatedAt = s.now()
	if err := s.reviewStore().update(ctx, row); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return ReviewReport{}, err
	}
	return reviewReportView(row)
}

// ApplyReviewReport 把一份评审报告显式应用到工作副本。
//
// 它复用的机制：人工门禁（RequireHumanDecision）、工作副本的四元 CAS
// （workspacecore.ApplyRewrite）、幂等键（报告行上的应用键）与事务内的授权
// 复核（工作副本那一侧的 operation pipeline 在事务里复核写权限）。
//
// 它**不改正文**：评审的产出是意见，不是改写。正文一个字不动，落进工作副本
// 的是这些意见（待核事项），每一条都带着用户的处置与理由。
func (s *SelectedRewriteService) ApplyReviewReport(ctx context.Context, actor Actor, projectID, chapterID, reportID, key string, input ApplyReviewInput) (core.WorkingCopy, bool, error) {
	auditDetails := map[string]any{"chapter_id": chapterID, "report_id": reportID, "idempotency_key": key}
	auditDecision, auditReason := "allow", ""
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.review.apply", auditDecision, auditReason, auditDetails)
	}()
	// 人工门禁先于一切：机器身份与代他人操作不得应用评审报告。
	if err := core.RequireHumanDecision(ctx); err != nil {
		return core.WorkingCopy{}, false, err
	}
	if s == nil || s.workspace == nil || s.sources == nil || actor.TenantID == 0 ||
		!operationcore.ValidKey(strings.TrimSpace(key)) {
		return core.WorkingCopy{}, false, ErrReviewInvalidRequest
	}
	key = strings.TrimSpace(key)
	if err := s.workspace.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		return core.WorkingCopy{}, false, err
	}
	row, err := s.reviewStore().byID(ctx, actor.TenantID, projectID, reportID)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	if row.ChapterID != chapterID {
		return core.WorkingCopy{}, false, ErrReviewNotFound
	}
	// 撤权先说：报告依据的授权没了，就说「授权没了」，不是「你的处置填错了」。
	if err := s.recheckSources(ctx, actor, projectID, mustDecodeSnapshots(row.AuthorizedSourcesJSON)); err != nil {
		auditDecision, auditReason = "deny", ErrReviewNotAuthorized.Error()
		return core.WorkingCopy{}, false, ErrReviewNotAuthorized
	}
	if row.State == string(ReviewReportRejected) {
		auditDecision, auditReason = "deny", ErrReviewInvalidRequest.Error()
		return core.WorkingCopy{}, false, ErrReviewInvalidRequest
	}
	if row.State == string(ReviewReportApplied) {
		if row.AppliedKey != key {
			auditDecision, auditReason = "deny", ErrReviewAlreadyApplied.Error()
			return core.WorkingCopy{}, false, ErrReviewAlreadyApplied
		}
		var saved core.WorkingCopy
		if err := json.Unmarshal([]byte(row.AppliedResponseJSON), &saved); err != nil {
			return core.WorkingCopy{}, false, err
		}
		return saved, true, nil
	}
	findings := mustDecodeReviewItems(row.FindingsJSON)
	// 逐条处置复用既有的处置模型：每一条意见都要有处置与理由，缺一条即拒。
	if err := candidateadoption.ValidateReviewDecisions(findings, input.ReviewDecisions); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return core.WorkingCopy{}, false, err
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	// 基线复核：报告是针对哪一份正文写的，现在那一份必须还在。基线变了就报
	// 版本冲突——不覆盖，也不拿旧意见去说新正文。
	baseline := ReviewBaseline{
		WorkingCopyRevision:  copy.WorkingCopyRevision,
		BaseChapterVersionID: copy.BaseChapterVersionID,
		SpecRevision:         copy.SpecRevision,
		BodyHash:             reviewBodyHash(copy.BodyMarkdown),
	}
	if baseline.WorkingCopyRevision != row.WorkingCopyRevision || baseline.SpecRevision != row.SpecRevision ||
		!sameOptionalVersion(baseline.BaseChapterVersionID, row.BaseChapterVersionID) ||
		baseline.BodyHash != row.BodyHash {
		auditDecision, auditReason = "deny", ErrReviewBaselineConflict.Error()
		return core.WorkingCopy{}, false, ErrReviewBaselineConflict
	}
	if input.ExpectedSpecRevision != row.SpecRevision ||
		input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
		!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) {
		auditDecision, auditReason = "deny", ErrReviewBaselineConflict.Error()
		return core.WorkingCopy{}, false, ErrReviewBaselineConflict
	}
	// 落进工作副本的是这些意见：正文一个字符不动。
	writeInput := core.SaveWorkingCopyInput{
		BaseChapterVersionID:        copy.BaseChapterVersionID,
		ExpectedSpecRevision:        copy.SpecRevision,
		ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown:                copy.BodyMarkdown,
		SourceIDs:                   stringSet(copy.SourceIDs),
	}
	result, err := s.workspace.ApplyRewriteResult(ctx, actor, projectID, chapterID,
		operationKeyForApply(key, row.ID), writeInput, reviewItemsForWorkingCopy(findings))
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return core.WorkingCopy{}, false, err
	}
	decisions := append([]candidateadoption.ReviewDecision{}, input.ReviewDecisions...)
	sortReviewDecisions(decisions)
	decisionsJSON, err := json.Marshal(decisions)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	saved, err := json.Marshal(result.WorkingCopy)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	row.State = string(ReviewReportApplied)
	row.DecisionsJSON = string(decisionsJSON)
	row.AppliedKey = key
	row.AppliedResponseJSON = string(saved)
	row.UpdatedAt = s.now()
	if err := s.reviewStore().update(context.WithoutCancel(ctx), row); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return core.WorkingCopy{}, false, err
	}
	return result.WorkingCopy, result.Replayed, nil
}

// reviewStore 是评审报告的存储入口。
func (s *SelectedRewriteService) reviewStore() *reviewReportStore {
	return &reviewReportStore{db: s.store.db}
}

// resolveReviewPlan 解析这次评审的执行计划。
//
// 动作身份来自管道声明：评审管道只实现一个动作，请求不必点名它。范围与
// 产出参数由词汇包判定——评审不用任何产出参数，多给一个就是非法声明。
func resolveReviewPlan(input ReviewReportRequest) (*writingaction.Plan, error) {
	action, err := writingaction.DefaultActionFor(writingaction.PipelineReview)
	if err != nil {
		return nil, err
	}
	pipelineActions, err := writingaction.ActionsFor(writingaction.PipelineReview)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pipelineActions, action) {
		return nil, ErrReviewInvalidRequest
	}
	scope := input.Scope
	if scope == "" {
		scope = writingaction.ScopeSection
	}
	plan, err := writingaction.PlanFor(action, scope, writingaction.OutputParams{})
	if err != nil {
		return nil, err
	}
	pipelineScopes, err := writingaction.ScopesFor(writingaction.PipelineReview)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pipelineScopes, plan.Scope) {
		return nil, ErrReviewInvalidRequest
	}
	return &plan, nil
}

// reviewTargetRange 是这次评审的对象在工作副本正文里的位置。
type reviewTargetRange struct {
	start    int
	end      int
	selected string
}

// reviewTarget 算出这次评审的对象：整章范围是整份正文，选段范围是那段选区。
func reviewTarget(body string, scope writingaction.Scope, selection *RewriteSelection) (reviewTargetRange, error) {
	if scope == writingaction.ScopeChapter {
		units := utf16.Encode([]rune(body))
		if len(units) == 0 {
			return reviewTargetRange{}, ErrReviewInvalidRequest
		}
		return reviewTargetRange{start: 0, end: len(units), selected: body}, nil
	}
	if selection == nil {
		return reviewTargetRange{}, ErrReviewInvalidRequest
	}
	start, end, ok := selectionByteRange(body, *selection)
	if !ok {
		return reviewTargetRange{}, ErrRewriteStale
	}
	return reviewTargetRange{start: start, end: end, selected: selection.SelectedText}, nil
}

// reviewPrompt 组装评审的提示。
//
// 指令只来自执行计划：评审的上下文按计划声明的类别组装，渲染出来的动作指令
// 交给模型。执行方不按动作名分支，也不自己写指令。
func reviewPrompt(plan writingaction.Plan, target reviewTargetRange, basis g2RewriteTemplateBasis,
	constraints []generation.EvidenceConstraint, cards []generation.EvidenceCardRef,
	before, after string, sources []evidence.Source, note string) (SelectedRewritePrompt, error) {
	var renderContext writingaction.RenderContext
	for _, item := range plan.Context.Required {
		switch item {
		case writingaction.ContextChapter:
			renderContext.Chapter = target.selected
		case writingaction.ContextSection:
			renderContext.Section = selectedRewriteSectionContext(
				RewriteSelection{StartUTF16: target.start, EndUTF16: target.end, SelectedText: target.selected}, before, after)
		case writingaction.ContextTemplate:
			renderContext.Template = selectedRewriteTemplateContext(basis)
		}
	}
	instruction, err := writingaction.RenderInstruction(plan, renderContext)
	if err != nil {
		return SelectedRewritePrompt{}, err
	}
	prompt := SelectedRewritePrompt{
		TemplateBasis: basis, EvidenceCardRefs: cards, EvidenceConstraints: constraints,
		Instruction: instruction, SupplementaryNote: note,
		Selection:     RewriteSelection{StartUTF16: target.start, EndUTF16: target.end, SelectedText: target.selected},
		ContextBefore: before, ContextAfter: after, Sources: sources,
		Produce: string(plan.Produce),
	}
	return prompt, nil
}

// reviewFindings 校验模型交回的评审意见。
//
// 评审交回的是意见，不是正文：正文必须为空，意见至少一条，每一条都要说清
// 一件事，来源不能超出这次评审允许的那一组。
//
// 意见的出处是这份报告：工作副本侧的待核事项要带着出处（它说清这条意见从哪
// 里来），评审意见的出处就是报告本身。
func reviewFindings(output SelectedRewriteOutput, allowed []string, reportID string) ([]candidateadoption.ReviewItem, error) {
	if strings.TrimSpace(output.ReplacementMarkdown) != "" {
		return nil, ErrReviewInvalidRequest
	}
	if len(output.ReviewItems) == 0 {
		return nil, ErrReviewInvalidRequest
	}
	permitted := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		permitted[id] = true
	}
	for _, id := range output.SourceIDs {
		if !permitted[id] {
			return nil, ErrRewriteSourceDenied
		}
	}
	findings := make([]candidateadoption.ReviewItem, 0, len(output.ReviewItems))
	for _, item := range output.ReviewItems {
		statement := strings.TrimSpace(item.Statement)
		if statement == "" {
			return nil, ErrReviewInvalidRequest
		}
		findings = append(findings, candidateadoption.ReviewItem{ID: uuid.NewString(), Statement: statement,
			OriginCandidateID: reportID})
	}
	return findings, nil
}

// reviewReportVersion 算出报告的版本标识。
//
// 它由报告自己的内容算出，因此可复算：同样的意见、同样的基线、同样的运行
// 模式 ⇒ 同样的版本号。摘要规则归 operationcore 一家。
func reviewReportVersion(findings []candidateadoption.ReviewItem, baseline ReviewBaseline,
	mode candidateadoption.RunMode, sourceIDs []string) (string, error) {
	statements := make([]string, 0, len(findings))
	for _, finding := range findings {
		statements = append(statements, finding.Statement)
	}
	return operationcore.RequestHash(struct {
		Findings  []string
		Baseline  ReviewBaseline
		RunMode   string
		SourceIDs []string
	}{statements, baseline, string(mode), sourceIDs})
}

// reviewRequestHash 算这次评审请求的摘要。
func reviewRequestHash(input ReviewReportRequest) (string, error) {
	hash, err := operationcore.RequestHash(input)
	if err != nil {
		return "", ErrReviewInvalidRequest
	}
	return hash, nil
}

// reviewBodyHash 是工作副本正文的摘要。
//
// 它是**内容摘要**（同一份正文），不是操作请求摘要（同一份请求）：后者归
// operationcore 一家，前者是「用户当时看到的是哪一份正文」的判据。
func reviewBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// reviewReportView 把存储行读成对外的报告。
func reviewReportView(row reviewReportRow) (ReviewReport, error) {
	findings := mustDecodeReviewItems(row.FindingsJSON)
	if findings == nil {
		findings = []candidateadoption.ReviewItem{}
	}
	sourceIDs := mustDecodeIDs(row.SourceIDsJSON)
	if sourceIDs == nil {
		sourceIDs = []string{}
	}
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if snapshots == nil {
		snapshots = []RewriteSourceSnapshot{}
	}
	var decisions []candidateadoption.ReviewDecision
	if err := json.Unmarshal([]byte(row.DecisionsJSON), &decisions); err != nil {
		return ReviewReport{}, err
	}
	if decisions == nil {
		decisions = []candidateadoption.ReviewDecision{}
	}
	return ReviewReport{
		ReportID: row.ID, ProjectID: row.ProjectID, ChapterID: row.ChapterID,
		Findings: findings, SourceIDs: sourceIDs, AuthorizedSources: snapshots,
		Baseline: ReviewBaseline{
			WorkingCopyRevision: row.WorkingCopyRevision, BaseChapterVersionID: row.BaseChapterVersionID,
			SpecRevision: row.SpecRevision, BodyHash: row.BodyHash,
		},
		ReportVersion: row.ReportVersion, AuthorizationState: candidateadoption.Validity(row.AuthorizationState),
		RunMode:       candidateadoption.RunMode(row.RunMode),
		WritingAction: writingaction.Action(row.WritingAction), Origin: writingaction.Origin(row.Origin),
		State: ReviewReportState(row.State), ReviewDecisions: decisions, ErrorCode: row.ErrorCode,
		Scope: writingaction.Scope(row.Scope),
		Selection: RewriteSelection{StartUTF16: row.StartUTF16, EndUTF16: row.EndUTF16,
			SelectedText: row.SelectedText},
		CreatedAt: row.CreatedAt,
	}, nil
}

// sortReviewDecisions 把逐条处置按事项编号排好，让回放与审计读到的顺序一样。
func sortReviewDecisions(decisions []candidateadoption.ReviewDecision) {
	slices.SortFunc(decisions, func(a, b candidateadoption.ReviewDecision) int {
		return strings.Compare(a.ReviewItemID, b.ReviewItemID)
	})
}
