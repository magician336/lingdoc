package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrRuntimePaused = clienterror.New(clienterror.Form{Status: 423, Code: "runtime_paused", Message: "当前范围已暂停，请由负责人核对并恢复。", Retryable: false}, "runtime scope is paused")
var ErrRuntimeRecoveryRequired = clienterror.New(clienterror.Form{Status: 422, Code: "runtime_recovery_required", Message: "恢复前需要核对当前检查、快照和导出文件。", Retryable: false}, "runtime recovery rechecks are required")

type RuntimeScope struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
type RuntimeControl struct {
	ID               string       `json:"id"`
	ProjectID        string       `json:"project_id"`
	Scope            RuntimeScope `json:"scope"`
	Version          int64        `json:"version"`
	Status           string       `json:"status"`
	ActorID          string       `json:"actor_id"`
	Reason           string       `json:"reason"`
	SnapshotID       string       `json:"snapshot_id,omitempty"`
	ExportID         string       `json:"export_id,omitempty"`
	RecoveryVerified bool         `json:"recovery_verified"`
	UpdatedAt        time.Time    `json:"updated_at"`
}
type RuntimeControlInput struct {
	ExpectedVersion int64        `json:"expected_version"`
	Scope           RuntimeScope `json:"scope"`
	Action          string       `json:"action"`
	Reason          string       `json:"reason"`
	SnapshotID      string       `json:"snapshot_id,omitempty"`
	ExportID        string       `json:"export_id,omitempty"`
}
type RuntimeEvent struct {
	ID              string    `json:"id"`
	Origin          string    `json:"origin"`
	TenantAlias     string    `json:"tenant_alias"`
	ProjectAlias    string    `json:"project_alias"`
	CorrelationID   string    `json:"correlation_id"`
	CausationID     string    `json:"causation_id,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	ChangeSetID     string    `json:"changeset_id,omitempty"`
	ImpactTaskID    string    `json:"impact_task_id,omitempty"`
	SnapshotID      string    `json:"snapshot_id,omitempty"`
	ExportID        string    `json:"export_id,omitempty"`
	TemplateVersion string    `json:"template_version,omitempty"`
	RulesetHash     string    `json:"ruleset_hash,omitempty"`
	ContextRevision int64     `json:"context_revision"`
	TargetRevision  int64     `json:"target_revision"`
	RuntimeMode     string    `json:"runtime_mode"`
	Category        string    `json:"resource_category"`
	Status          string    `json:"status"`
	ErrorCode       string    `json:"error_code,omitempty"`
	DurationMS      int64     `json:"duration_ms"`
	QueueDepth      int64     `json:"queue_depth,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}
type RuntimeAlertRule struct {
	ID            string       `json:"id"`
	Signal        string       `json:"signal"`
	Severity      string       `json:"severity"`
	Action        string       `json:"action"`
	ResponsibleID string       `json:"responsible_id"`
	Scope         RuntimeScope `json:"scope"`
}
type RuntimeAlert struct {
	ID            string    `json:"id"`
	RuleID        string    `json:"rule_id"`
	EventID       string    `json:"event_id"`
	Severity      string    `json:"severity"`
	Action        string    `json:"action"`
	ResponsibleID string    `json:"responsible_id"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}
type RuntimeMetrics struct {
	EventCount int            `json:"event_count"`
	Counts     map[string]int `json:"counts"`
	P95MS      *int64         `json:"p95_ms"`
	P99MS      *int64         `json:"p99_ms"`
	QueueDepth int64          `json:"queue_depth"`
	Missing    []string       `json:"missing"`
}
type runtimeRecordRow struct {
	ID                         string
	TenantID                   uint64
	ProjectID, Kind, RecordKey string
	Version                    int64
	BodyJSON                   string
	CreatedAt, UpdatedAt       time.Time
}

func (runtimeRecordRow) TableName() string { return "lingdoc_g6_records" }

type runtimeTransaction interface {
	ReadRuntimeRecord(uint64, string, string, string, any) (int64, error)
	InsertRuntimeRecord(uint64, string, string, string, int64, any) error
	CompareAndSwapRuntimeRecord(uint64, string, string, string, int64, any) error
	ListRuntimeRecords(uint64, string, string) ([]runtimeRecordRow, error)
	RuntimeControls(uint64) ([]RuntimeControl, error)
}

func (t gormTransaction) ReadRuntimeRecord(tenant uint64, project, kind, key string, value any) (int64, error) {
	var row runtimeRecordRow
	if err := t.db.Where("tenant_id = ? AND project_id = ? AND kind = ? AND record_key = ?", tenant, project, kind, key).First(&row).Error; err != nil {
		return 0, storageError(err)
	}
	return row.Version, json.Unmarshal([]byte(row.BodyJSON), value)
}
func (t gormTransaction) InsertRuntimeRecord(tenant uint64, project, kind, key string, version int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return t.db.Create(&runtimeRecordRow{ID: uuid.NewString(), TenantID: tenant, ProjectID: project, Kind: kind, RecordKey: key, Version: version, BodyJSON: string(raw), CreatedAt: now, UpdatedAt: now}).Error
}
func (t gormTransaction) CompareAndSwapRuntimeRecord(tenant uint64, project, kind, key string, version int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return affected(t.db.Model(&runtimeRecordRow{}).Where("tenant_id = ? AND project_id = ? AND kind = ? AND record_key = ? AND version = ?", tenant, project, kind, key, version).Updates(map[string]any{"version": version + 1, "body_json": string(raw), "updated_at": time.Now().UTC()}))
}
func (t gormTransaction) ListRuntimeRecords(tenant uint64, project, kind string) ([]runtimeRecordRow, error) {
	var rows []runtimeRecordRow
	err := t.db.Where("tenant_id = ? AND project_id = ? AND kind = ?", tenant, project, kind).Order("created_at,id").Find(&rows).Error
	return rows, err
}
func (t gormTransaction) RuntimeControls(tenant uint64) ([]RuntimeControl, error) {
	var rows []runtimeRecordRow
	if err := t.db.Where("tenant_id = ? AND kind = ?", tenant, "control").Find(&rows).Error; err != nil {
		return nil, err
	}
	controls := make([]RuntimeControl, 0, len(rows))
	for _, row := range rows {
		var value RuntimeControl
		if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
			return nil, err
		}
		controls = append(controls, value)
	}
	return controls, nil
}
func (s *Service) SetRuntimeRecoveryChecker(checker RuntimeRecoveryChecker) {
	s.runtimeRecovery = checker
}
func (s *Service) SetRuntimeMode(mode string) {
	if validRuntimeMode(mode) {
		s.runtimeMode = mode
	}
}
func validRuntimeMode(mode string) bool {
	return slices.Contains([]string{"mock", "real_api_fake_model", "real"}, mode)
}
func effectiveRuntimeMode(mode string) string {
	if !validRuntimeMode(mode) {
		return "real_api_fake_model"
	}
	return mode
}
func runtimeScopeMatches(scope RuntimeScope, actor Actor, p Project, mode string) bool {
	switch scope.Kind {
	case "tenant":
		return scope.Value == strconv.FormatUint(actor.TenantID, 10)
	case "project":
		return scope.Value == p.ID
	case "template_version":
		return scope.Value == p.TemplateVersion
	case "ruleset_hash":
		return p.TemplateCopy != nil && scope.Value == p.TemplateCopy.RulesetHash
	case "runtime_mode":
		return scope.Value == effectiveRuntimeMode(mode)
	}
	return false
}
func RuntimeGate(tx Transaction, actor Actor, p Project, operation, mode string) error {
	if strings.HasPrefix(operation, "runtime") || strings.HasPrefix(operation, "trial") || operation == "reverseChangeSet" {
		return nil
	}
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return ErrInvalidState
	}
	controls, err := store.RuntimeControls(actor.TenantID)
	if err != nil {
		return err
	}
	for _, control := range controls {
		if control.Status == "PAUSED" && runtimeScopeMatches(control.Scope, actor, p, mode) {
			return ErrRuntimePaused
		}
	}
	return nil
}
func (s *Service) CheckRuntimeOperation(ctx context.Context, actor Actor, projectID, operation, mode string) error {
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		return RuntimeGate(tx, actor, p, operation, effectiveRuntimeMode(s.runtimeMode))
	})
}
func (s *Service) ControlRuntime(ctx context.Context, actor Actor, projectID, key string, input RuntimeControlInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 0 || strings.TrimSpace(input.Reason) == "" || !slices.Contains([]string{"pause", "resume"}, input.Action) || !slices.Contains([]string{"tenant", "project", "template_version", "ruleset_hash", "runtime_mode"}, input.Scope.Kind) || input.Scope.Value == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	var checked Project
	if input.Action == "resume" {
		if input.SnapshotID == "" || input.ExportID == "" || s.runtimeRecovery == nil {
			return nil, 0, false, ErrRuntimeRecoveryRequired
		}
		var err error
		checked, err = s.GetProject(ctx, actor, projectID)
		if err != nil {
			return nil, 0, false, err
		}
		if err := s.Authorize(ctx, actor, projectID, "manage"); err != nil {
			return nil, 0, false, err
		}
		if err := s.runtimeRecovery.Recheck(ctx, actor, projectID, input.SnapshotID, input.ExportID); err != nil {
			return nil, 0, false, err
		}
	}
	return runOperation(s, ctx, actor, operationInput(operationRuntimeControl, projectID+"/"+input.Scope.Kind+"/"+input.Scope.Value, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RuntimeControl], error) {
		if input.Scope.Kind == "project" {
			if input.Scope.Value != projectID || !projectOwner(p, actor.UserID) {
				return operationResult[RuntimeControl]{}, ErrNotFound
			}
		} else {
			if err := s.authorizeTenant(ctx, tx, authorizationRequest(actor, "", AuthorizationTenantManage, "")); err != nil {
				return operationResult[RuntimeControl]{}, err
			}
			if input.Scope.Kind == "tenant" && input.Scope.Value != strconv.FormatUint(actor.TenantID, 10) {
				return operationResult[RuntimeControl]{}, ErrNotFound
			}
			if input.Scope.Kind == "runtime_mode" && !validRuntimeMode(input.Scope.Value) {
				return operationResult[RuntimeControl]{}, ErrInvalidRequest
			}
			if !runtimeScopeMatches(input.Scope, actor, p, input.Scope.Value) && input.Scope.Kind != "runtime_mode" {
				return operationResult[RuntimeControl]{}, ErrNotFound
			}
		}
		if input.Action == "resume" && p.ProjectVersion != checked.ProjectVersion {
			return operationResult[RuntimeControl]{}, ErrVersionConflict
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return operationResult[RuntimeControl]{}, ErrInvalidState
		}
		recordKey := input.Scope.Kind + "/" + input.Scope.Value
		recordProject := projectID
		if input.Scope.Kind != "project" {
			recordProject = ""
		}
		var previous RuntimeControl
		version, err := store.ReadRuntimeRecord(actor.TenantID, recordProject, "control", recordKey, &previous)
		if err != nil && err != ErrNotFound {
			return operationResult[RuntimeControl]{}, err
		}
		if err == ErrNotFound {
			version = 0
		}
		if version != input.ExpectedVersion {
			return operationResult[RuntimeControl]{}, ErrVersionConflict
		}
		if input.Action == "resume" && (version == 0 || previous.Status != "PAUSED") {
			return operationResult[RuntimeControl]{}, ErrInvalidState
		}
		next := RuntimeControl{ID: previous.ID, ProjectID: recordProject, Scope: input.Scope, Version: version + 1, Status: "PAUSED", ActorID: actor.UserID, Reason: input.Reason, UpdatedAt: time.Now().UTC()}
		if next.ID == "" {
			next.ID = uuid.NewString()
		}
		if input.Action == "resume" {
			next.Status = "ACTIVE"
			next.RecoveryVerified = true
			next.SnapshotID = input.SnapshotID
			next.ExportID = input.ExportID
		}
		if version == 0 {
			err = store.InsertRuntimeRecord(actor.TenantID, recordProject, "control", recordKey, 1, next)
		} else {
			err = store.CompareAndSwapRuntimeRecord(actor.TenantID, recordProject, "control", recordKey, version, next)
		}
		if err != nil {
			return operationResult[RuntimeControl]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "runtime."+input.Action, next.ID, map[string]any{"scope": next.Scope, "version": next.Version, "recovery_verified": next.RecoveryVerified, "snapshot_id": next.SnapshotID, "export_id": next.ExportID}); err != nil {
			return operationResult[RuntimeControl]{}, err
		}
		return operationResult[RuntimeControl]{Value: next, Status: operationStatusOK}, nil
	})
}
func (s *Service) ListRuntimeControls(ctx context.Context, actor Actor, projectID string) ([]RuntimeControl, error) {
	var result []RuntimeControl
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		controls, err := store.RuntimeControls(actor.TenantID)
		if err != nil {
			return err
		}
		result = []RuntimeControl{}
		for _, control := range controls {
			if control.ProjectID == "" || control.ProjectID == projectID {
				result = append(result, control)
			}
		}
		return nil
	})
	return result, err
}

var runtimeSafeCode = regexp.MustCompile(`^[a-zA-Z0-9_.:/-]{1,128}$`)

func validRuntimeEvent(event RuntimeEvent) bool {
	if event.CorrelationID == "" || event.DurationMS < 0 || event.QueueDepth < 0 || !validRuntimeMode(event.RuntimeMode) || !slices.Contains([]string{"request", "task", "changeset", "impact_task", "validation", "stabilization", "preflight", "snapshot", "export", "download", "audit", "dependency"}, event.Category) || !slices.Contains([]string{"success", "rejected", "conflict", "failed", "retry", "stale", "duplicate", "unavailable", "revoked", "loss"}, event.Status) {
		return false
	}
	for _, value := range []string{event.CorrelationID, event.CausationID, event.RequestID, event.TaskID, event.ChangeSetID, event.ImpactTaskID, event.SnapshotID, event.ExportID, event.TemplateVersion, event.RulesetHash, event.ErrorCode} {
		if value != "" && !runtimeSafeCode.MatchString(value) {
			return false
		}
		if len(strings.Split(value, ".")) == 3 && len(value) > 80 {
			return false
		}
	}
	return true
}
func RecordRuntimeOperation(tx Transaction, actor Actor, p Project, operation, status string, durationMS int64) error {
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return ErrInvalidState
	}
	event := RuntimeEvent{ID: uuid.NewString(), Origin: "runtime", TenantAlias: "tenant." + hashImpactValue(actor.TenantID)[:16], ProjectAlias: "project." + hashImpactValue(p.ID)[:16], CorrelationID: uuid.NewString(), Category: "request", Status: status, RuntimeMode: "real_api_fake_model", ContextRevision: p.CurrentContextRevision, TemplateVersion: p.TemplateVersion, DurationMS: durationMS, CreatedAt: time.Now().UTC()}
	return store.InsertRuntimeRecord(actor.TenantID, p.ID, "event", event.ID, 1, event)
}
func (s *Service) RecordRuntimeEvent(ctx context.Context, actor Actor, projectID, key string, input RuntimeEvent) (json.RawMessage, int, bool, error) {
	if !validRuntimeEvent(input) {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationRuntimeEvent, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RuntimeEvent], error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return operationResult[RuntimeEvent]{}, ErrInvalidState
		}
		event := input
		event.Origin = "reported"
		event.ID = uuid.NewString()
		event.TenantAlias = "tenant." + hashImpactValue(actor.TenantID)[:16]
		event.ProjectAlias = "project." + hashImpactValue(projectID)[:16]
		event.CreatedAt = time.Now().UTC()
		event.ContextRevision = p.CurrentContextRevision
		event.RuntimeMode = effectiveRuntimeMode(s.runtimeMode)
		if err := store.InsertRuntimeRecord(actor.TenantID, projectID, "event", event.ID, 1, event); err != nil {
			return operationResult[RuntimeEvent]{}, err
		}
		return operationResult[RuntimeEvent]{Value: event, Status: operationStatusCreated}, nil
	})
}
func (s *Service) ListRuntimeEvents(ctx context.Context, actor Actor, projectID, correlationID string) ([]RuntimeEvent, error) {
	var result []RuntimeEvent
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.ListRuntimeRecords(actor.TenantID, projectID, "event")
		if err != nil {
			return err
		}
		result = []RuntimeEvent{}
		for _, row := range rows {
			var event RuntimeEvent
			if err := json.Unmarshal([]byte(row.BodyJSON), &event); err != nil {
				return err
			}
			if correlationID == "" || event.CorrelationID == correlationID {
				result = append(result, event)
			}
		}
		return nil
	})
	return result, err
}
func (s *Service) GetRuntimeMetrics(ctx context.Context, actor Actor, projectID string) (RuntimeMetrics, error) {
	events, err := s.ListRuntimeEvents(ctx, actor, projectID, "")
	if err != nil {
		return RuntimeMetrics{}, err
	}
	result := RuntimeMetrics{EventCount: 0, Counts: map[string]int{}, Missing: []string{}}
	durations := []int64{}
	for _, event := range events {
		if event.Origin != "runtime" {
			continue
		}
		result.EventCount++
		result.Counts[event.Category+"/"+event.Status]++
		durations = append(durations, event.DurationMS)
		if event.QueueDepth > result.QueueDepth {
			result.QueueDepth = event.QueueDepth
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) == 0 {
		result.Missing = append(result.Missing, "latency_p95_p99")
	} else {
		p95 := durations[(len(durations)*95+99)/100-1]
		p99 := durations[(len(durations)*99+99)/100-1]
		result.P95MS = &p95
		result.P99MS = &p99
	}
	return result, nil
}
func (s *Service) SaveRuntimeAlertRule(ctx context.Context, actor Actor, projectID, key string, input RuntimeAlertRule) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if !runtimeSafeCode.MatchString(input.Signal) || input.ResponsibleID == "" || !slices.Contains([]string{"P0", "P1", "P2", "P3"}, input.Severity) || !slices.Contains([]string{"notify", "retry", "pause", "block_export", "rollback", "human_confirm"}, input.Action) {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationRuntimeAlertRule, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RuntimeAlertRule], error) {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return operationResult[RuntimeAlertRule]{}, ErrInvalidState
		}
		input.ID = uuid.NewString()
		if input.Scope.Kind == "" {
			input.Scope = RuntimeScope{Kind: "project", Value: p.ID}
		}
		if !runtimeScopeMatches(input.Scope, actor, p, s.runtimeMode) {
			return operationResult[RuntimeAlertRule]{}, ErrNotFound
		}
		recipient := actor
		recipient.UserID = input.ResponsibleID
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(recipient, p.ID, AuthorizationManage, "")); err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		if err := store.InsertRuntimeRecord(actor.TenantID, p.ID, "alert_rule", input.ID, 1, input); err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		if err := recordAuditEvent(tx, actor, p.ID, "runtime.alert_rule", input.ID, map[string]any{"signal": input.Signal, "action": input.Action}); err != nil {
			return operationResult[RuntimeAlertRule]{}, err
		}
		return operationResult[RuntimeAlertRule]{Value: input, Status: operationStatusCreated}, nil
	})
}
func applyRuntimeAlerts(tx Transaction, actor Actor, p Project, event RuntimeEvent) error {
	store := tx.(runtimeTransaction)
	rows, err := store.ListRuntimeRecords(actor.TenantID, p.ID, "alert_rule")
	if err != nil {
		return err
	}
	for _, row := range rows {
		var rule RuntimeAlertRule
		if err := json.Unmarshal([]byte(row.BodyJSON), &rule); err != nil {
			return err
		}
		if rule.Signal != event.Status && rule.Signal != event.Category+"/"+event.Status && rule.Signal != event.ErrorCode {
			continue
		}
		alert := RuntimeAlert{ID: uuid.NewString(), RuleID: rule.ID, EventID: event.ID, Severity: rule.Severity, Action: rule.Action, ResponsibleID: rule.ResponsibleID, Status: "WAITING_USER", CreatedAt: time.Now().UTC()}
		if rule.Action == "pause" || rule.Action == "block_export" {
			control := RuntimeControl{ID: uuid.NewString(), ProjectID: p.ID, Scope: RuntimeScope{Kind: "project", Value: p.ID}, Status: "PAUSED", Version: 1, ActorID: actor.UserID, Reason: "alert:" + rule.ID, UpdatedAt: time.Now().UTC()}
			var old RuntimeControl
			version, err := store.ReadRuntimeRecord(actor.TenantID, p.ID, "control", "project/"+p.ID, &old)
			switch err {
			case ErrNotFound:
				err = store.InsertRuntimeRecord(actor.TenantID, p.ID, "control", "project/"+p.ID, 1, control)
			case nil:
				control.Version = version + 1
				err = store.CompareAndSwapRuntimeRecord(actor.TenantID, p.ID, "control", "project/"+p.ID, version, control)
			}
			if err != nil {
				return err
			}
			alert.Status = "APPLIED"
		}
		if err := store.InsertRuntimeRecord(actor.TenantID, p.ID, "alert", rule.ID+"/"+event.ID, 1, alert); err != nil {
			return err
		}
		if err := store.InsertRuntimeRecord(actor.TenantID, p.ID, "alert_action", alert.ID, 1, map[string]any{"action": rule.Action, "responsible_id": rule.ResponsibleID, "status": alert.Status}); err != nil {
			return err
		}
	}
	return nil
}

// auditFailureErrorCode marks a failed audit write in the runtime event
// model. It is a code and not the error text: the runtime event model only
// accepts bounded codes, and an error message may carry research content.
const auditFailureErrorCode = "audit_write_failed"

// EmitAuditFailureAlert publishes one operational event for an audit write
// that failed on a fail-open path.
//
// It writes on its own transaction, never the caller's: the caller's
// transaction may be rolled back by the very failure being reported, and an
// alert written inside it would vanish exactly when it is needed. The caller
// therefore calls this after its own transaction has ended.
//
// It never changes an authorization decision and never blocks the read that
// reported it. A failure to write the alert is logged and dropped.
//
// The event carries no research content and no raw error text: only the
// failed audit action, the tenant and project aliases the runtime event model
// already uses, and a bounded error code.
func (s *Service) EmitAuditFailureAlert(ctx context.Context, actor Actor, projectID, auditAction string, cause error) error {
	// Alert rules are read per project, so an event without a project scope
	// can never match one. Publishing it would cost a transaction and change
	// nothing.
	if projectID == "" {
		return nil
	}
	err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		trace := RuntimeTraceFromContext(ctx)
		// The category is set explicitly: the operation name does not
		// produce "audit", and an event without it can never match the
		// "audit/failed" signal a rule is written against.
		trace.Category = "audit"
		trace.ErrorCode = auditFailureErrorCode
		trace.RuntimeMode = effectiveRuntimeMode(s.runtimeMode)
		return RecordRuntimeOperationContext(WithRuntimeTrace(ctx, trace), tx, actor,
			Project{ID: projectID}, auditAction, "failed", 0)
	})
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"audit_event":  "lingdoc_audit_failure",
			"tenant_id":    actor.TenantID,
			"project_id":   projectID,
			"audit_action": auditAction,
			"cause":        fmt.Sprintf("%v", cause),
		})
	}
	return nil
}

func (s *Service) ListRuntimeAlerts(ctx context.Context, actor Actor, projectID string) ([]RuntimeAlert, error) {
	var result []RuntimeAlert
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.ListRuntimeRecords(actor.TenantID, projectID, "alert")
		if err != nil {
			return err
		}
		result = []RuntimeAlert{}
		for _, row := range rows {
			var value RuntimeAlert
			if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
				return err
			}
			result = append(result, value)
		}
		return nil
	})
	return result, err
}
