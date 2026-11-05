package rulesetstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/gin-gonic/gin"
)

type RouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
	POST(string, ...gin.HandlerFunc) gin.IRoutes
}

type Application interface {
	CreateDraft(context.Context, Actor, template.RuleSet) error
	GetVersion(context.Context, Actor, string, string) (template.RuleSet, error)
	ListVersions(context.Context, Actor, string) ([]template.RuleSet, error)
	ListRuleSetUseAudits(context.Context, Actor, string) ([]template.RuleSetUseAuditEvent, error)
	Publish(context.Context, Actor, string, string) (template.RuleSet, error)
	Withdraw(context.Context, Actor, string, string) (template.RuleSet, error)
	Recall(context.Context, Actor, string, string) (template.RuleSet, error)
}

// AuditPageApplication is the paginated extension of Application. Keeping the
// legacy list method on Application lets small adapters remain source
// compatible while production stores expose the bounded history contract.
type AuditPageApplication interface {
	ListRuleSetUseAuditsPage(context.Context, Actor, string, RuleSetUseAuditQuery) (RuleSetUseAuditPage, error)
}

// MetricsApplication exposes persisted audit and composition evidence through
// the same authorized application seam as the audit history endpoint.
type MetricsApplication interface {
	ReadRuleSetMetrics(context.Context, Actor, string, RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error)
}

// RequestInfo extracts identity established by the application's auth
// middleware, plus its request ID, without coupling this transport adapter to
// the wider application context package.
type RequestInfo func(*gin.Context) (Actor, string, bool)

type HTTPHandler struct {
	application Application
	requestInfo RequestInfo
}

func NewHTTPHandler(application Application, requestInfo RequestInfo) *HTTPHandler {
	return &HTTPHandler{application: application, requestInfo: requestInfo}
}

// Register keeps catalog reads behind the LingDoc viewer policy and all
// lifecycle writes behind the stricter admin route group.
func (h *HTTPHandler) Register(read, write RouteRegistrar) {
	if h == nil || h.application == nil {
		return
	}
	read.GET("/rule-sets/definitions", h.listDefinitions)
	read.GET("/rule-sets/:ruleSetID/versions", h.listVersions)
	read.GET("/rule-sets/:ruleSetID/versions/:version", h.getVersion)
	read.GET("/projects/:projectId/rule-set-use-audits", h.listRuleSetUseAudits)
	read.GET("/projects/:projectId/rule-set-use-audit-metrics", h.readRuleSetMetrics)
	write.POST("/rule-sets", h.createDraft)
	write.POST("/rule-sets/:ruleSetID/versions/:version/publish", h.publish)
	write.POST("/rule-sets/:ruleSetID/versions/:version/withdraw", h.withdraw)
	write.POST("/rule-sets/:ruleSetID/versions/:version/recall", h.recall)
}

func (h *HTTPHandler) identity(c *gin.Context) (Actor, string, bool) {
	if h.requestInfo == nil {
		return Actor{}, "", false
	}
	return h.requestInfo(c)
}

func (h *HTTPHandler) listDefinitions(c *gin.Context) {
	_, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"items": template.RuleDefinitions()}, requestID)
}

func (h *HTTPHandler) listVersions(c *gin.Context) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	versions, err := h.application.ListVersions(c.Request.Context(), actor, c.Param("ruleSetID"))
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"items": versions}, requestID)
}

func (h *HTTPHandler) getVersion(c *gin.Context) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	version, err := h.application.GetVersion(c.Request.Context(), actor, c.Param("ruleSetID"), c.Param("version"))
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusOK, version, requestID)
}

func (h *HTTPHandler) listRuleSetUseAudits(c *gin.Context) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	query, err := parseRuleSetUseAuditQuery(c)
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	if paged, ok := h.application.(AuditPageApplication); ok {
		page, err := paged.ListRuleSetUseAuditsPage(c.Request.Context(), actor, c.Param("projectId"), query)
		if err != nil {
			writeApplicationError(c, err, requestID)
			return
		}
		nextCursor := any(nil)
		if page.NextCursor != "" {
			nextCursor = page.NextCursor
		}
		writeSuccess(c, http.StatusOK, gin.H{"items": page.Items, "next_cursor": nextCursor}, requestID)
		return
	}
	if query.Cursor != "" || query.Action != "" || query.From != nil || query.To != nil || query.Limit != RuleSetUseAuditDefaultLimit {
		writeApplicationError(c, ErrInvalidRuleSetUseAudit, requestID)
		return
	}
	events, err := h.application.ListRuleSetUseAudits(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"items": events, "next_cursor": nil}, requestID)
}

func (h *HTTPHandler) readRuleSetMetrics(c *gin.Context) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	metrics, ok := h.application.(MetricsApplication)
	if !ok {
		writeApplicationError(c, ErrRuleSetMetricsUnavailable, requestID)
		return
	}
	query, err := parseRuleSetMetricsQuery(c)
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	snapshot, err := metrics.ReadRuleSetMetrics(c.Request.Context(), actor, c.Param("projectId"), query)
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusOK, snapshot, requestID)
}

func parseRuleSetMetricsQuery(c *gin.Context) (RuleSetMetricsQuery, error) {
	if strings.TrimSpace(c.Query("cursor")) != "" || strings.TrimSpace(c.Query("action")) != "" || strings.TrimSpace(c.Query("limit")) != "" {
		return RuleSetMetricsQuery{}, ErrInvalidRuleSetUseAudit
	}
	parseTime := func(raw string) (*time.Time, error) {
		if strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, ErrInvalidRuleSetUseAudit
		}
		value = value.UTC()
		return &value, nil
	}
	from, err := parseTime(c.Query("from"))
	if err != nil {
		return RuleSetMetricsQuery{}, err
	}
	to, err := parseTime(c.Query("to"))
	if err != nil {
		return RuleSetMetricsQuery{}, err
	}
	if from != nil && to != nil && from.After(*to) {
		return RuleSetMetricsQuery{}, ErrInvalidRuleSetUseAudit
	}
	return RuleSetMetricsQuery{From: from, To: to}, nil
}

func parseRuleSetUseAuditQuery(c *gin.Context) (RuleSetUseAuditQuery, error) {
	query := RuleSetUseAuditQuery{Cursor: strings.TrimSpace(c.Query("cursor")), Limit: RuleSetUseAuditDefaultLimit, Action: strings.TrimSpace(c.Query("action"))}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return RuleSetUseAuditQuery{}, ErrInvalidRuleSetUseAudit
		}
		if limit == 0 {
			return RuleSetUseAuditQuery{}, ErrInvalidRuleSetUseAudit
		}
		query.Limit = limit
	}
	parseTime := func(raw string) (*time.Time, error) {
		if strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, ErrInvalidRuleSetUseAudit
		}
		value = value.UTC()
		return &value, nil
	}
	var err error
	if query.From, err = parseTime(c.Query("from")); err != nil {
		return RuleSetUseAuditQuery{}, err
	}
	if query.To, err = parseTime(c.Query("to")); err != nil {
		return RuleSetUseAuditQuery{}, err
	}
	return query, nil
}

func (h *HTTPHandler) createDraft(c *gin.Context) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	var draft template.RuleSet
	if !decodeRequest(c, &draft, requestID) {
		return
	}
	if draft.State != "" && draft.State != template.RuleSetDraft {
		writeError(c, http.StatusBadRequest, "invalid_request", "只能创建草稿规则集版本。", requestID)
		return
	}
	draft.State = template.RuleSetDraft
	if err := h.application.CreateDraft(c.Request.Context(), actor, draft); err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusCreated, draft, requestID)
}

func (h *HTTPHandler) publish(c *gin.Context) {
	h.transition(c, h.application.Publish)
}

func (h *HTTPHandler) withdraw(c *gin.Context) {
	h.transition(c, h.application.Withdraw)
}

func (h *HTTPHandler) recall(c *gin.Context) {
	h.transition(c, h.application.Recall)
}

func (h *HTTPHandler) transition(c *gin.Context, apply func(context.Context, Actor, string, string) (template.RuleSet, error)) {
	actor, requestID, ok := h.identity(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
		return
	}
	version, err := apply(c.Request.Context(), actor, c.Param("ruleSetID"), c.Param("version"))
	if err != nil {
		writeApplicationError(c, err, requestID)
		return
	}
	writeSuccess(c, http.StatusOK, version, requestID)
}

func decodeRequest(c *gin.Context, target any, requestID string) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "请求字段不符合约定。", requestID)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(c, http.StatusBadRequest, "invalid_request", "请求字段不符合约定。", requestID)
		return false
	}
	return true
}

func writeSuccess(c *gin.Context, status int, data any, requestID string) {
	c.JSON(status, gin.H{"data": data, "request_id": requestID,
		"meta": gin.H{"replayed": false, "refresh_required": false}})
}

func writeApplicationError(c *gin.Context, err error, requestID string) {
	switch {
	case errors.Is(err, ErrActorRequired), errors.Is(err, ErrRuleSetTenantRequired):
		writeError(c, http.StatusUnauthorized, "unauthenticated", "需要有效的租户身份。", requestID)
	case errors.Is(err, ErrRuleSetAccessDenied):
		writeError(c, http.StatusForbidden, "forbidden", "当前租户成员无权执行此规则集操作。", requestID)
	case errors.Is(err, ErrRuleSetNotFound):
		writeError(c, http.StatusNotFound, "not_found", "资源不存在或不可访问。", requestID)
	case errors.Is(err, ErrRuleSetVersionExists), errors.Is(err, ErrRuleSetStateConflict), errors.Is(err, template.ErrRuleSetNotDraft), errors.Is(err, template.ErrRuleSetNotPublished):
		writeError(c, http.StatusConflict, "state_conflict", "规则集版本状态已变化，请重新读取。", requestID)
	case errors.Is(err, ErrAuthorizerUnavailable):
		writeError(c, http.StatusServiceUnavailable, "authorization_unavailable", "授权服务暂不可用，请稍后重试。", requestID)
	case errors.Is(err, ErrDraftHashMustBeEmpty), errors.Is(err, template.ErrInvalidRuleSet):
		writeError(c, http.StatusBadRequest, "invalid_request", "规则集不符合安全约束。", requestID)
	case errors.Is(err, ErrInvalidRuleSetUseAudit):
		writeError(c, http.StatusBadRequest, "invalid_request", "规则集使用审计查询范围无效。", requestID)
	case errors.Is(err, ErrRuleSetMetricsUnavailable):
		writeError(c, http.StatusServiceUnavailable, "metrics_unavailable", "规则集运行指标暂不可用，请稍后再试。", requestID)
	default:
		writeError(c, http.StatusInternalServerError, "internal_error", "操作失败，请稍后再试。", requestID)
	}
}

func writeError(c *gin.Context, status int, code, message, requestID string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": status >= http.StatusInternalServerError}, "request_id": requestID})
}
