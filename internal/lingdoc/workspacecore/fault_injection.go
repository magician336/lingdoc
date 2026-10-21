package workspacecore

import (
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// 受控故障注入（#256 / AC-G3-03）。
//
// 目标：让「应用过程中任务写入失败或权限失效 → 项目保持原 revision 与旧模板引用」
// 这条原子性边界可以在真实 HTTP 面被实跑，而不是只由后端 Go 集成测试覆盖。
//
// 做法：不在产品 API 上新增故障注入端点（那会把「如何制造写入失败」这条测试
// 知识泄漏进公开契约）。改为在组合根装配一个会失败的窄 port 实现：HTTP 路由、
// handler 与领域规则零改动，apply 走真事务、失败真发生，HTTP 面照样观测到整体
// 回滚。
//
// 作用域：仅本进程、仅被点名的演示租户。开关默认关闭——没有点名注入点或没有
// 点名租户时，注入配置一律是惰性的。

// FaultPoint names one controlled failure seam.
type FaultPoint string

const (
	// FaultPointImpactTaskWrite fails the ImpactTask write inside the
	// change-set apply transaction. The project revision has already been
	// advanced at that point, so the rollback proof is not trivial.
	FaultPointImpactTaskWrite FaultPoint = "impact_task_write"
	// FaultPointAuditWrite fails the business audit row of the apply
	// transaction. That row is the last write of the transaction, so the
	// rollback also has to undo the revision advance and the task rows.
	FaultPointAuditWrite FaultPoint = "audit_write"
	// FaultPointAuthorization makes the authorization decision fail for the
	// named tenant, which is what a lapsed permission looks like.
	FaultPointAuthorization FaultPoint = "authorization"
)

// ErrInjectedFault marks an error produced by a controlled fault injection
// rather than by a business rule.
var ErrInjectedFault = errors.New("injected_fault")

// FaultInjection is the composition-root configuration of one controlled
// failure.
//
// It stays inert unless both a point and a tenant are named: an injection
// without a tenant scope would reach every tenant in the process, and an
// injection without a point has nothing to fail.
type FaultInjection struct {
	// Point is the seam to fail.
	Point FaultPoint
	// TenantID is the only tenant the injection may reach.
	TenantID uint64
	// Action narrows the injection to one authorization action or one audit
	// action. Empty means every action of that point is affected.
	Action string
	// Reason is the readable cause the caller is told about.
	Reason string
}

// Enabled reports whether the configuration may install a fault.
func (f FaultInjection) Enabled() bool { return f.Point != "" && f.TenantID != 0 }

// KnownPoint reports whether the named point is one this build understands.
// An unknown point is treated as "off" so a typo cannot arm an unintended
// failure.
func (f FaultInjection) KnownPoint() bool {
	switch f.Point {
	case FaultPointImpactTaskWrite, FaultPointAuditWrite, FaultPointAuthorization:
		return true
	default:
		return false
	}
}

func (f FaultInjection) reason() string {
	if f.Reason == "" {
		return "受控故障注入已启用"
	}
	return f.Reason
}

// InjectedFaultError is the error a controlled failure returns.
//
// It states its own client presentation, so the caller can tell an injected
// failure from any other error without a new product endpoint and without
// touching the shared error mapping table (the transport mapper honours a
// domain error that knows its own presentation).
type InjectedFaultError struct {
	Point  FaultPoint
	Reason string
}

func (e *InjectedFaultError) Error() string {
	return fmt.Sprintf("injected_fault[%s]: %s", e.Point, e.Reason)
}

func (e *InjectedFaultError) Unwrap() error { return ErrInjectedFault }

// ClientForm states the outward shape of an injected failure: the caller is
// told the operation did not take effect, and the failure is retryable because
// nothing was written.
func (e *InjectedFaultError) ClientForm() clienterror.Form {
	return clienterror.Form{
		Status:  503,
		Code:    "injected_fault",
		Message: fmt.Sprintf("受控故障注入已生效（%s）：%s。本次操作整体未生效，状态保持原样。", e.Point, e.Reason),
	}
}

// IsInjectedFault reports whether err came from a controlled fault injection.
func IsInjectedFault(err error) bool { return errors.Is(err, ErrInjectedFault) }

// ImpactTaskWriter is the narrow writer port for ImpactTask rows.
//
// The change-set apply path stores its impact tasks through this port only, so
// a composition root can substitute a controlled failure implementation and
// observe the atomicity boundary of the apply transaction.
type ImpactTaskWriter interface {
	WriteImpactTask(Transaction, ImpactTask) error
}

// BusinessAuditWriter is the narrow writer port for the business audit row of
// a mutation transaction.
type BusinessAuditWriter interface {
	WriteBusinessAudit(Transaction, AuditEvent) error
}

// transactionImpactTaskWriter is the production ImpactTask writer: it writes
// through the caller's transaction, so the row joins that transaction's
// atomic boundary.
type transactionImpactTaskWriter struct{}

func (transactionImpactTaskWriter) WriteImpactTask(tx Transaction, task ImpactTask) error {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	return store.InsertImpactTask(task)
}

// transactionBusinessAuditWriter is the production business audit writer.
type transactionBusinessAuditWriter struct{}

func (transactionBusinessAuditWriter) WriteBusinessAudit(tx Transaction, event AuditEvent) error {
	audit, ok := tx.(AuditTransaction)
	if !ok {
		return ErrInvalidState
	}
	return audit.RecordAudit(event)
}

// faultImpactTaskWriter fails the ImpactTask write for one tenant only. Other
// tenants keep the real write: the injection is a demonstration tool scoped to
// the demo tenant, not a global outage.
type faultImpactTaskWriter struct {
	fault FaultInjection
	inner ImpactTaskWriter
}

func (w faultImpactTaskWriter) WriteImpactTask(tx Transaction, task ImpactTask) error {
	if !faultInProjectScope(tx, w.fault, task.ProjectID) {
		return w.inner.WriteImpactTask(tx, task)
	}
	return &InjectedFaultError{Point: w.fault.Point, Reason: w.fault.reason()}
}

// faultBusinessAuditWriter fails the business audit write for one tenant only.
// When the fault names one audit action, every other audit row is still
// written: the injection then lands on the last write of the apply
// transaction and the rollback has to undo the revision advance and the task
// rows as well.
type faultBusinessAuditWriter struct {
	fault FaultInjection
	inner BusinessAuditWriter
}

func (w faultBusinessAuditWriter) WriteBusinessAudit(tx Transaction, event AuditEvent) error {
	if w.fault.Action != "" && event.Action != w.fault.Action {
		return w.inner.WriteBusinessAudit(tx, event)
	}
	if !faultInProjectScope(tx, w.fault, event.ProjectID) {
		return w.inner.WriteBusinessAudit(tx, event)
	}
	return &InjectedFaultError{Point: w.fault.Point, Reason: w.fault.reason()}
}

// faultAuthorizationPolicy makes the authorization decision fail for one
// tenant only. The decision is made inside the caller's transaction, so a
// lapsed permission aborts the operation instead of leaving half a write
// behind.
type faultAuthorizationPolicy struct {
	fault FaultInjection
	inner AuthorizationPolicy
}

func (p faultAuthorizationPolicy) AuthorizeTenant(tx Transaction, request AuthorizationRequest) error {
	if p.inScope(request) {
		return &InjectedFaultError{Point: p.fault.Point, Reason: p.fault.reason()}
	}
	return p.inner.AuthorizeTenant(tx, request)
}

func (p faultAuthorizationPolicy) AuthorizeProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	if p.inScope(request) {
		return Project{}, &InjectedFaultError{Point: p.fault.Point, Reason: p.fault.reason()}
	}
	return p.inner.AuthorizeProject(tx, request)
}

func (p faultAuthorizationPolicy) inScope(request AuthorizationRequest) bool {
	if request.Actor.TenantID != p.fault.TenantID {
		return false
	}
	if p.fault.Action != "" && string(request.Action) != p.fault.Action {
		return false
	}
	return true
}

// faultInProjectScope reports whether the project belongs to the tenant the
// fault is scoped to. The tenant filter of the project read is the scope
// check: a project of another tenant is simply not found here, so its writes
// are never touched by the injection.
func faultInProjectScope(tx Transaction, fault FaultInjection, projectID string) bool {
	if fault.TenantID == 0 || projectID == "" {
		return false
	}
	if _, err := tx.Project(fault.TenantID, projectID); err != nil {
		return false
	}
	return true
}

// InstallFaultInjection wires one controlled failure into the service's writer
// ports.
//
// This is the composition root's only entry point; the service itself never
// reads the environment. The installation is inert unless the fault names a
// known point and a tenant, so the default composition is byte-for-byte the
// previous behaviour.
func (s *Service) InstallFaultInjection(fault FaultInjection) {
	if s == nil || !fault.Enabled() || !fault.KnownPoint() {
		return
	}
	switch fault.Point {
	case FaultPointImpactTaskWrite:
		s.impactTasks = faultImpactTaskWriter{fault: fault, inner: transactionImpactTaskWriter{}}
	case FaultPointAuditWrite:
		s.businessAudit = faultBusinessAuditWriter{fault: fault, inner: transactionBusinessAuditWriter{}}
	}
}

// NewFaultInjectingAuthorizationPolicy wraps an authorization policy so that
// the named tenant observes a permission lapse.
//
// A nil base means the default policy, exactly as the plain constructor uses
// it. A fault that is not the authorization point leaves the policy untouched.
func NewFaultInjectingAuthorizationPolicy(base AuthorizationPolicy, fault FaultInjection) AuthorizationPolicy {
	if base == nil {
		base = transactionProjectAuthorizer{}
	}
	if !fault.Enabled() || !fault.KnownPoint() || fault.Point != FaultPointAuthorization {
		return base
	}
	return faultAuthorizationPolicy{fault: fault, inner: base}
}

// writeBusinessAudit persists one business audit row through the service's
// business audit writer port, falling back to the caller's transactional
// audit record.
func (s *Service) writeBusinessAudit(tx Transaction, fallback AuditTransaction, event AuditEvent) error {
	if s.businessAudit != nil {
		return s.businessAudit.WriteBusinessAudit(tx, event)
	}
	return fallback.RecordAudit(event)
}
