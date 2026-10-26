package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// #656：未评估（draft）的变更集被 apply 时，服务端答 404 not_found，
// 而状态机要求的语义是 422 invalid_state；更关键的是状态机检查
// （ChangeSetStatusCanMove("apply", status)）从未被执行到——拒绝发生在
// 来源解码阶段（读评估记录）之前。
//
// 本文件钉住修复后的对外语义，全部走真实 HTTP 路由 + 真实 SQLite，
// 不使用假 handler：
//
//	L1-1 draft（未评估）apply ⇒ 422 invalid_state（不得是 404）
//	L1-2 变更集不存在 ⇒ 404 not_found（两种语义可区分）
//	L1-3 拒绝后零业务副作用
//	L1-4 合法前态 assessed ⇒ 200 且 status=applied
//	L1-5 零 provider 调用（本夹具不装配任何模型网关）

type unassessedApplyFixture struct {
	engine  *gin.Engine
	db      *gorm.DB
	service *core.Service
}

// newUnassessedApplyFixture 装出真实路由 + 真实本地 SQLite。
// 装配方式与 change_set_http_test.go 一致，让两条用例的差异只来自本票的改动。
func newUnassessedApplyFixture(t *testing.T) unassessedApplyFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "unassessed-apply-copy")
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	// 草稿路由与 impact 路由组不在 handler.Register 里，与生产装配一致地单独注册。
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))
	return unassessedApplyFixture{engine: engine, db: db, service: service}
}

// unassessedApplyError 读出错信封：error.code / error.retryable / request_id。
type unassessedApplyError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type unassessedApplyEnvelope struct {
	Error     unassessedApplyError `json:"error"`
	RequestID string               `json:"request_id"`
}

func (f unassessedApplyFixture) post(t *testing.T, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.WithValue(actorContext("reader"), types.RequestIDContextKey, "request-"+key)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	return response
}

// createDraftOverHTTP 走真实草稿路由建一份未评估的变更集，返回它的 ID。
func (f unassessedApplyFixture) createDraftOverHTTP(t *testing.T, key string) string {
	t.Helper()
	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"新的研究对象"}},"affected_chapter_ids":["chapter-1","chapter-2"],"reason":"研究对象发生变化"}`
	response := f.post(t, "/api/projects/project-1/change-sets/drafts", key, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create draft: status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data core.ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if created.Data.Status != core.ChangeSetStatusDraft {
		t.Fatalf("draft change set status = %q, want %q", created.Data.Status, core.ChangeSetStatusDraft)
	}
	return created.Data.ID
}

// applyOverHTTP 走真实 apply 路由，返回响应与错误信封。
func (f unassessedApplyFixture) applyOverHTTP(t *testing.T, changeSetID, key string) (*httptest.ResponseRecorder, unassessedApplyEnvelope) {
	t.Helper()
	response := f.post(t, "/api/projects/project-1/change-sets/"+changeSetID+"/apply", key, `{}`)
	var envelope unassessedApplyEnvelope
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	return response, envelope
}

// unassessedApplyState 是「拒绝之后必须保持原样」的那组事实。
type unassessedApplyState struct {
	ChangeSetStatus       string
	BaseContextRevision   int64
	BaseSpecRevision      int64
	TargetContextRevision sqlNullInt64
	AppliedAt             sqlNullString
	Operations            int64
	ApplyAudits           int64
	Assessments           int64
	Stabilizations        int64
	ContextRevision       int64
	SpecRevision          int64
	ProjectVersion        int64
	ModelCalls            int64
}

type sqlNullInt64 struct {
	Valid bool
	Value int64
}

type sqlNullString struct {
	Valid bool
	Value string
}

func (f unassessedApplyFixture) snapshot(t *testing.T, changeSetID string) unassessedApplyState {
	t.Helper()
	state := unassessedApplyState{}
	row := struct {
		Status                string
		BaseContextRevision   int64
		BaseSpecRevision      int64
		TargetContextRevision sql.NullInt64
		AppliedAt             sql.NullString
	}{}
	if err := f.db.Raw("SELECT status, base_context_revision, base_spec_revision, target_context_revision, applied_at FROM lingdoc_change_sets WHERE id = ?", changeSetID).Scan(&row).Error; err != nil {
		t.Fatalf("read change set row: %v", err)
	}
	state.ChangeSetStatus = row.Status
	state.BaseContextRevision = row.BaseContextRevision
	state.BaseSpecRevision = row.BaseSpecRevision
	state.TargetContextRevision = sqlNullInt64{Valid: row.TargetContextRevision.Valid, Value: row.TargetContextRevision.Int64}
	state.AppliedAt = sqlNullString{Valid: row.AppliedAt.Valid, Value: row.AppliedAt.String}
	state.Operations = f.count(t, "lingdoc_operations", "operation = ?", "applyChangeSet")
	state.ApplyAudits = f.count(t, "lingdoc_project_audits", "action = ?", "project.change_set.apply")
	state.Assessments = f.count(t, "lingdoc_g3_records", "kind = ? AND record_key = ?", "assessment", changeSetID)
	state.Stabilizations = f.count(t, "lingdoc_g5_records", "1 = 1")
	state.ModelCalls = f.count(t, "lingdoc_model_call_ledger", "1 = 1")
	project := struct {
		ContextRevision int64
		SpecRevision    int64
		ProjectVersion  int64
	}{}
	if err := f.db.Raw("SELECT current_context_revision, spec_revision, project_version FROM lingdoc_projects WHERE id = ?", "project-1").Scan(&project).Error; err != nil {
		t.Fatalf("read project row: %v", err)
	}
	state.ContextRevision = project.ContextRevision
	state.SpecRevision = project.SpecRevision
	state.ProjectVersion = project.ProjectVersion
	return state
}

func (f unassessedApplyFixture) count(t *testing.T, table, where string, args ...any) int64 {
	t.Helper()
	if !f.tableExists(t, table) {
		return 0
	}
	var n int64
	query := "SELECT COUNT(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	if err := f.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func (f unassessedApplyFixture) tableExists(t *testing.T, table string) bool {
	t.Helper()
	var n int64
	if err := f.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n).Error; err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return n > 0
}

// setChangeSetStatus 直接把变更集推到目标状态，用于同族状态的前置布置。
func (f unassessedApplyFixture) setChangeSetStatus(t *testing.T, changeSetID, status string) {
	t.Helper()
	if err := f.db.Exec("UPDATE lingdoc_change_sets SET status = ? WHERE id = ?", status, changeSetID).Error; err != nil {
		t.Fatalf("set change set status %s: %v", status, err)
	}
}

// L1-1 + L1-3：未评估的变更集被 apply 时答 422 invalid_state，且零业务副作用。
func TestUnassessedChangeSetApplyIsRefusedAsInvalidState(t *testing.T) {
	f := newUnassessedApplyFixture(t)
	draftID := f.createDraftOverHTTP(t, "unassessed-create-draft-1")

	before := f.snapshot(t, draftID)
	if before.Assessments != 0 {
		t.Fatalf("the draft already carries an assessment: %d rows", before.Assessments)
	}

	response, envelope := f.applyOverHTTP(t, draftID, "unassessed-apply-1")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply of an unassessed change set = %d, want 422; body=%s", response.Code, response.Body.String())
	}
	if envelope.Error.Code != "invalid_state" {
		t.Fatalf("refusal code = %q, want invalid_state; body=%s", envelope.Error.Code, response.Body.String())
	}
	if envelope.Error.Retryable {
		t.Fatalf("the refusal is retryable, want a terminal refusal; body=%s", response.Body.String())
	}
	if envelope.RequestID != "request-unassessed-apply-1" {
		t.Fatalf("the refusal did not carry the request id: %q; body=%s", envelope.RequestID, response.Body.String())
	}

	after := f.snapshot(t, draftID)
	if after != before {
		t.Fatalf("the refused apply changed state:\nbefore = %+v\nafter  = %+v", before, after)
	}
	if after.ChangeSetStatus != core.ChangeSetStatusDraft {
		t.Fatalf("the refused apply moved the change set to %q", after.ChangeSetStatus)
	}
	if after.Operations != 0 {
		t.Fatalf("the refused apply wrote an idempotency row: %d", after.Operations)
	}
	if after.ApplyAudits != 0 {
		t.Fatalf("the refused apply wrote an apply audit: %d", after.ApplyAudits)
	}
	if after.Assessments != 0 {
		t.Fatalf("the refused apply wrote an assessment: %d", after.Assessments)
	}
	if after.Stabilizations != before.Stabilizations {
		t.Fatalf("the refused apply wrote a stabilization record: %d -> %d", before.Stabilizations, after.Stabilizations)
	}
	// L1-5：这条路径上没有任何 provider 调用。
	if after.ModelCalls != 0 {
		t.Fatalf("the refused apply called a model: %d ledger rows", after.ModelCalls)
	}
}

// L1-2：变更集真的不存在时仍然答 404 not_found，与「存在但未评估」可区分。
func TestMissingChangeSetApplyIsStillNotFound(t *testing.T) {
	f := newUnassessedApplyFixture(t)
	response, envelope := f.applyOverHTTP(t, "change-set-that-does-not-exist", "missing-apply-1")
	if response.Code != http.StatusNotFound {
		t.Fatalf("apply of a missing change set = %d, want 404; body=%s", response.Code, response.Body.String())
	}
	if envelope.Error.Code != "not_found" {
		t.Fatalf("missing change set refusal code = %q, want not_found; body=%s", envelope.Error.Code, response.Body.String())
	}
	if envelope.Error.Retryable {
		t.Fatalf("the 404 is retryable, want a terminal refusal; body=%s", response.Body.String())
	}
}

// 同族状态：状态机不允许 apply 的前态，一律答 422 invalid_state。
// 期望值不写死：从状态机本身读出来（core.ChangeSetStatusCanMove）。
func TestChangeSetApplyRefusesEveryStatusTheStateMachineForbids(t *testing.T) {
	f := newUnassessedApplyFixture(t)
	changeID := f.createAssessedChangeSetOverHTTP(t, "state-machine-assessed-1")

	for _, status := range []string{
		core.ChangeSetStatusDraft,
		core.ChangeSetStatusStale,
		core.ChangeSetStatusSatisfied,
		core.ChangeSetStatusRejected,
		core.ChangeSetStatusStabilized,
	} {
		if core.ChangeSetStatusCanMove("apply", status) {
			t.Fatalf("the state machine allows apply from %q; this case is wrong", status)
		}
		f.setChangeSetStatus(t, changeID, status)
		before := f.snapshot(t, changeID)
		response, envelope := f.applyOverHTTP(t, changeID, "state-machine-apply-"+status)
		if response.Code != http.StatusUnprocessableEntity || envelope.Error.Code != "invalid_state" {
			t.Fatalf("apply from %q = %d %q, want 422 invalid_state; body=%s", status, response.Code, envelope.Error.Code, response.Body.String())
		}
		after := f.snapshot(t, changeID)
		if after != before {
			t.Fatalf("the refused apply from %q changed state:\nbefore = %+v\nafter  = %+v", status, before, after)
		}
	}
}

// L1-4：合法前态 assessed 的 apply 仍然 200 且 status=applied。
func TestAssessedChangeSetApplyStillSucceeds(t *testing.T) {
	f := newUnassessedApplyFixture(t)
	changeID := f.createAssessedChangeSetOverHTTP(t, "assessed-apply-1")
	response, _ := f.applyOverHTTP(t, changeID, "assessed-apply-key-1")
	if response.Code != http.StatusOK {
		t.Fatalf("apply of an assessed change set = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var applied struct {
		Data core.ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Data.Status != core.ChangeSetStatusApplied {
		t.Fatalf("apply response status = %q, want %q", applied.Data.Status, core.ChangeSetStatusApplied)
	}
}

// createAssessedChangeSetOverHTTP 走真实创建路由建一份已评估的变更集。
func (f unassessedApplyFixture) createAssessedChangeSetOverHTTP(t *testing.T, key string) string {
	t.Helper()
	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"新的研究对象"}},"affected_chapter_ids":["chapter-1","chapter-2"],"reason":"研究对象发生变化"}`
	response := f.post(t, "/api/projects/project-1/change-sets", key, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create change set: status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data core.ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode change set response: %v", err)
	}
	if created.Data.Status != core.ChangeSetStatusAssessed {
		t.Fatalf("change set status = %q, want %q", created.Data.Status, core.ChangeSetStatusAssessed)
	}
	return created.Data.ID
}
