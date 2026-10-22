package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"sort"
	"time"
)

// Production composition owns this guard; the HTTP layer cannot install or
// substitute a successful preflight/recovery result.
type FullWorkflowGuard struct {
	ports    FullWorkflowGuardPorts
	Sources  SourceApplicationService
	releases *DeliveryReleaseService
	exports  *DeliveryExportService
}

// NewFullWorkflowGuard 装出全流程守卫，并把守卫自己装进运行时。
//
// 守卫要不再持有聚合体具体类型，「把自己装进运行时」这件事就得由装配口承载：
// 那三个安装动作本来就是守卫做的，端口收窄不改变这个事实。
func NewFullWorkflowGuard(ports FullWorkflowGuardPorts, sources SourceApplicationService) *FullWorkflowGuard {
	g := &FullWorkflowGuard{ports: ports, Sources: sources}
	ports.Compose.SetImpactPreflightChecker(fullPreflightChecker{g})
	ports.Compose.SetRuntimeRecoveryChecker(fullRecoveryChecker{g})
	return g
}

type fullPreflightChecker struct{ guard *FullWorkflowGuard }

func (c fullPreflightChecker) Recheck(ctx context.Context, a core.Actor, pid, id string) (core.ImpactPreflightEvidence, error) {
	r, err := c.guard.ports.Impact.ReadImpactComputationReceipt(ctx, a, pid, id)
	if err != nil {
		return core.ImpactPreflightEvidence{}, err
	}
	return core.ImpactPreflightEvidence{ProofID: r.ID, ProofKind: "check_receipt", SnapshotDigest: r.InputHash, ProjectVersion: r.ProjectVersion, ContextRevision: r.ContextRevision, TemplateCopyVersion: r.TemplateCopyVersion, RulesetHash: r.RulesetHash, Status: r.Status, IsCurrent: true, SourceIDs: r.SourceIDs}, nil
}

type fullRecoveryChecker struct{ guard *FullWorkflowGuard }

func (c fullRecoveryChecker) Recheck(ctx context.Context, a core.Actor, pid, snapshot, artifact string) error {
	g := c.guard
	if g.releases == nil || g.exports == nil {
		return core.ErrRuntimeRecoveryRequired
	}
	release, err := g.releases.Get(ctx, a.UserID, pid, snapshot)
	if err != nil {
		return err
	}
	if !release.IsCurrent || release.Check.Status != delivery.CheckPassed {
		return core.ErrRuntimeRecoveryRequired
	}
	export, err := g.exports.Get(ctx, a.UserID, pid, artifact)
	if err != nil {
		return err
	}
	if export.SnapshotID != snapshot {
		return core.ErrRuntimeRecoveryRequired
	}
	_, bytes, err := g.exports.Download(ctx, a.UserID, pid, artifact)
	if err != nil {
		return err
	}
	if len(bytes) == 0 {
		return core.ErrRuntimeRecoveryRequired
	}
	return g.ports.Impact.AssertRevisionStabilized(ctx, a, pid)
}
func (g *FullWorkflowGuard) before(ctx context.Context, user, pid, operation string, stable bool) (core.Actor, error) {
	a, ok := fullWorkflowActor(ctx, user)
	// 迁移前这里检查的是聚合体指针为 nil；现在束里六个口都要在位，才等价于
	// 「工作区能力都在」。少一个口就整条拒绝，而不是在请求里解引用空口。
	if !ok || g == nil || !g.ports.wired() {
		return a, ErrNotFound
	}
	if err := g.ports.Runtime.CheckRuntimeOperation(ctx, a, pid, operation, ""); err != nil {
		return a, err
	}
	if stable {
		if err := g.ports.Impact.AssertRevisionStabilized(ctx, a, pid); err != nil {
			return a, err
		}
	}
	return a, nil
}
func (g *FullWorkflowGuard) observe(ctx context.Context, a core.Actor, pid, operation string, started time.Time, err error, objects ...core.DeliveryRuntimeObjects) {
	status, code := "success", ""
	if err != nil {
		status, code = "failed", "operation_failed"
		switch {
		case errors.Is(err, core.ErrVersionConflict), errors.Is(err, delivery.ErrExportStaleInput), errors.Is(err, delivery.ErrSnapshotStaleInput):
			status, code = "conflict", "stale_input"
		case errors.Is(err, core.ErrRuntimePaused):
			status, code = "rejected", "runtime_paused"
		case errors.Is(err, core.ErrSourceUnavailable), errors.Is(err, candidateadoption.ErrSourceAccessDenied):
			status, code = "revoked", "source_access_denied"
		case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrHumanDecisionRequired):
			status, code = "rejected", "access_denied"
		}
	}
	trace := core.RuntimeTraceFromContext(ctx)
	trace.ErrorCode = code
	ctx = core.WithRuntimeTrace(ctx, trace)
	if len(objects) > 0 {
		_ = g.ports.Runtime.ObserveDeliveryRuntime(ctx, a, pid, operation, status, time.Since(started).Milliseconds(), objects[0])
	} else {
		_ = g.ports.Runtime.ObserveRuntime(ctx, a, pid, operation, status, time.Since(started).Milliseconds())
	}
}

type GuardedDeliveryRelease struct {
	*DeliveryReleaseService
	guard *FullWorkflowGuard
}

func GuardFullWorkflowRelease(base *DeliveryReleaseService, g *FullWorkflowGuard) *GuardedDeliveryRelease {
	g.releases = base
	return &GuardedDeliveryRelease{base, g}
}
func (s *GuardedDeliveryRelease) Check(ctx context.Context, user, pid string, expected int64) (out delivery.CheckResult, err error) {
	started := time.Now()
	a, err := s.guard.before(ctx, user, pid, "preflight", false)
	defer func() {
		s.guard.observe(ctx, a, pid, "preflight", started, err, core.DeliveryRuntimeObjects{ReceiptID: out.ReceiptID})
	}()
	if err != nil {
		return out, err
	}
	ctx, _, err = s.guard.ports.Provenance.PrepareProjectProvenance(ctx, a, pid, false)
	if err != nil {
		return out, err
	}
	basis, err := s.guard.ports.Impact.ReadPreflightBasis(ctx, a, pid, nil)
	if err != nil {
		return out, err
	}
	if basis.Project.ProjectVersion != expected {
		return out, ErrVersionConflict
	}
	input, err := s.assemble(ctx, user, pid, expected)
	if err != nil {
		return out, err
	}
	out = delivery.Evaluate(input)
	if err = s.attachIssueDispositions(ctx, user, pid, &out); err != nil {
		return out, err
	}
	delivery.ApplyDispositions(&out)
	preflight, err := delivery.EvaluateTemplateScene(input, "preflight")
	if err != nil {
		return out, err
	}
	editing, err := delivery.EvaluateTemplateScene(input, "write_check")
	if err != nil {
		return out, err
	}
	evaluations := append(append([]template.Evaluation{}, preflight.Evaluations...), editing.Evaluations...)
	// Actual evaluation may outlive its initial provenance read. Recheck the
	// exact evaluated Spec pins outside the receipt's write transaction.
	if provenance, ok := s.builder.(interface {
		AuthorizeFrozenProvenance(context.Context, string, string, delivery.DeliveryInput) error
	}); ok {
		if err = provenance.AuthorizeFrozenProvenance(ctx, user, pid, input); err != nil {
			return delivery.CheckResult{}, err
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(raw)
	receiptStatus := string(out.Status)
	if receiptStatus != "passed" {
		receiptStatus = "blocked"
	}
	for _, value := range evaluations {
		if value.Status == template.EvaluationUnknown {
			receiptStatus = "blocked"
		}
	}
	// This wrapper evaluates directly rather than calling base.Check; use the
	// same governed audit path before any durable computation receipt is written.
	if err = s.recordRuleSetUse(ctx, user, input, "preflight", "", string(out.Status)); err != nil {
		return delivery.CheckResult{}, err
	}
	receipt, err := s.guard.ports.Impact.StoreImpactComputationReceipt(ctx, a, basis, hex.EncodeToString(sum[:]), receiptStatus, evaluations, basis.SourceIDs)
	if err != nil {
		return out, err
	}
	out.ReceiptID = receipt.ID
	return out, nil
}
func (s *GuardedDeliveryRelease) TemplateCheck(ctx context.Context, user, pid string, expected int64) (out delivery.TemplateCheckResult, err error) {
	started := time.Now()
	a, err := s.guard.before(ctx, user, pid, "write_check", false)
	defer func() { s.guard.observe(ctx, a, pid, "write_check", started, err) }()
	if err != nil {
		return out, err
	}
	return s.DeliveryReleaseService.TemplateCheck(ctx, user, pid, expected)
}
func (s *GuardedDeliveryRelease) Prepare(ctx context.Context, user, pid, key string, expected int64) (out delivery.ReleaseSnapshot, replay bool, err error) {
	started := time.Now()
	a, err := s.guard.before(ctx, user, pid, "freeze", true)
	defer func() {
		objects := core.DeliveryRuntimeObjects{
			SnapshotID: out.ID, ProjectVersion: int64(out.FrozenInput.ProjectVersion),
			TemplateCopyVersion: out.FrozenInput.TemplateCopyVersion, RulesetHash: delivery.FrozenRuleSetHash(out.FrozenInput),
		}
		if out.FrozenInput.SpecProvenance != nil {
			objects.ContextRevision = out.FrozenInput.SpecProvenance.ContextRevision
		}
		s.guard.observe(ctx, a, pid, "freeze", started, err, objects)

	}()
	if err != nil {
		return out, false, err
	}
	ctx, _, err = s.guard.ports.Provenance.PrepareProjectProvenance(ctx, a, pid, false)
	if err != nil {
		return out, false, err
	}
	return s.DeliveryReleaseService.Prepare(ctx, user, pid, key, expected)
}

type GuardedDeliveryExport struct {
	*DeliveryExportService
	guard *FullWorkflowGuard
}

func GuardFullWorkflowExport(base *DeliveryExportService, g *FullWorkflowGuard) *GuardedDeliveryExport {
	g.exports = base
	wrapped := &GuardedDeliveryExport{base, g}
	g.ports.Compose.SetActionExecutor(NewG2ActionExecutor(g.ports.Actions, g.Sources, wrapped))
	return wrapped
}
func (s *GuardedDeliveryExport) Start(ctx context.Context, user, pid, snapshot, key string) (out delivery.ExportArtifact, replay bool, err error) {
	// Machine envelopes never acquire a human decision through a user alias.
	if err = core.RequireHumanDecision(ctx); err != nil {
		return out, false, err
	}
	started := time.Now()
	a, err := s.guard.before(ctx, user, pid, "export_start", false)
	defer func() {
		s.guard.observe(ctx, a, pid, "export_start", started, err, core.DeliveryRuntimeObjects{SnapshotID: out.SnapshotID, ExportID: out.ID})
	}()
	if err != nil {
		return out, false, err
	}
	if err = s.guard.ports.Delivery.AuthorizeDeliveryExport(ctx, a, pid); err != nil {
		return out, false, err
	}
	if err = s.guard.ports.Impact.AssertRevisionStabilized(ctx, a, pid); err != nil {
		return out, false, err
	}
	// Scope Owner authority to Start, including its internal access/replay
	// checks. The shared service remains read-authorized for downloads.
	if s.DeliveryExportService == nil || s.inputs == nil {
		return out, false, candidateadoption.ErrInvalidState
	}
	exportService := *s.DeliveryExportService
	inputs := *s.inputs
	inputs.Authorizer = fullWorkflowExportAuthorizer{s.guard.ports.Delivery, inputs.Authorizer}
	exportService.inputs = &inputs
	runtime, ok := s.guard.Sources.(*SourceRuntime)
	if !ok || runtime == nil || runtime.db == nil {
		return out, false, candidateadoption.ErrDependencyUnavailable
	}
	if _, ok := exportService.exports.(*delivery.SQLiteExportStore); !ok {
		return out, false, candidateadoption.ErrDependencyUnavailable
	}
	exportService.exports = &fullWorkflowExportRecorder{ExportStore: exportService.exports, base: s.DeliveryExportService, delivery: s.guard.ports.Delivery, provenance: s.guard.ports.Provenance, db: runtime.db, ctx: ctx, actor: a}
	out, replay, err = exportService.Start(ctx, user, pid, snapshot, key)
	if err == nil {
		if err = s.guard.ports.Delivery.AuthorizeDeliveryExport(ctx, a, pid); err != nil {
			return delivery.ExportArtifact{}, false, err
		}
	}
	return out, replay, err
}
func (s *GuardedDeliveryExport) Download(ctx context.Context, user, pid, id string) (out delivery.ExportArtifact, bytes []byte, err error) {
	started := time.Now()
	a, err := s.guard.before(ctx, user, pid, "download", true)
	defer func() {
		s.guard.observe(ctx, a, pid, "download", started, err, core.DeliveryRuntimeObjects{SnapshotID: out.SnapshotID, ExportID: out.ID})
	}()
	if err != nil {
		return out, nil, err
	}
	return s.DeliveryExportService.Download(ctx, user, pid, id)
}

// This adapter tightens formal export only; it never changes Caller/Principal.
type fullWorkflowExportAuthorizer struct {
	delivery core.DeliveryGuardApplication
	base     candidateadoption.DeliveryInputProjectAuthorizer
}

func (a fullWorkflowExportAuthorizer) AuthorizeProject(ctx context.Context, user, projectID string) error {
	if a.base == nil || nilPort(a.delivery) {
		return candidateadoption.ErrDependencyUnavailable
	}
	if err := a.base.AuthorizeProject(ctx, user, projectID); err != nil {
		return err
	}
	actor, ok := fullWorkflowActor(ctx, user)
	if !ok {
		return core.ErrHumanDecisionRequired
	}
	return a.delivery.AuthorizeDeliveryExport(ctx, actor, projectID)
}

var _ ReleaseApplication = (*GuardedDeliveryRelease)(nil)
var _ TemplateCheckApplication = (*GuardedDeliveryRelease)(nil)
var _ ExportApplication = (*GuardedDeliveryExport)(nil)

// File rendering and source/origin IO happen before this recorder opens the
// final parent transaction. Permission, native source basis, artifact/action
// persistence and the critical business audit then commit together.
type fullWorkflowExportRecorder struct {
	delivery.ExportStore
	base       *DeliveryExportService
	delivery   core.DeliveryGuardApplication
	provenance core.SourceProvenanceApplication
	db         *gorm.DB
	ctx        context.Context
	actor      core.Actor
}

func (r *fullWorkflowExportRecorder) ReplayExport(attempt delivery.ExportAttempt, requestHash string) (delivery.ExportArtifact, bool, error) {
	return r.ExportStore.(delivery.ExportRecorder).ReplayExport(attempt, requestHash)
}

var _ delivery.ExportRecorder = (*fullWorkflowExportRecorder)(nil)
var _ delivery.RuleSetUseExportRecorder = (*fullWorkflowExportRecorder)(nil)

func (r *fullWorkflowExportRecorder) RecordExport(artifact delivery.ExportArtifact, attempt delivery.ExportAttempt, requestHash string) (delivery.ExportArtifact, bool, error) {
	return r.recordExport(artifact, attempt, requestHash, nil)
}

func (r *fullWorkflowExportRecorder) RecordExportWithRuleSetUse(artifact delivery.ExportArtifact, attempt delivery.ExportAttempt, requestHash string, event template.RuleSetUseAuditEvent) (delivery.ExportArtifact, bool, error) {
	return r.recordExport(artifact, attempt, requestHash, &event)
}

func (r *fullWorkflowExportRecorder) recordExport(artifact delivery.ExportArtifact, attempt delivery.ExportAttempt, requestHash string, useEvent *template.RuleSetUseAuditEvent) (out delivery.ExportArtifact, replay bool, err error) {
	if attempt.ActorID != r.actor.UserID || attempt.ProjectID != artifact.ProjectID {
		return out, false, core.ErrNotFound
	}
	snapshot, err := r.base.snapshots.Get(artifact.ProjectID, artifact.SnapshotID)
	if err != nil {
		return out, false, err
	}
	if useEvent == nil {
		if artifact.RuleSetRef != nil {
			return out, false, core.ErrInvalidState
		}
	} else {
		ref := template.RuleSetReferenceFor(snapshot.FrozenInput.Template)
		if ref == nil || artifact.RuleSetRef == nil || *artifact.RuleSetRef != *ref ||
			useEvent.TenantID != r.actor.TenantID || useEvent.ActorID != r.actor.UserID || useEvent.ActorRole != string(r.actor.Role) ||
			useEvent.ProjectID != artifact.ProjectID || useEvent.ResourceID != artifact.ID || useEvent.Action != "export" || useEvent.Outcome != string(artifact.Status) ||
			useEvent.RuleSetID != ref.ID || useEvent.RuleSetVersion != ref.Version || useEvent.RuleSetHash != ref.Hash ||
			useEvent.EffectiveRulesetHash != snapshot.FrozenInput.Template.RulesetHash ||
			useEvent.TemplateID != snapshot.FrozenInput.Template.ID || useEvent.TemplateVersion != snapshot.FrozenInput.Template.Version ||
			useEvent.TemplateCopyID != snapshot.FrozenInput.TemplateCopyID || useEvent.TemplateCopyVersion != snapshot.FrozenInput.TemplateCopyVersion ||
			useEvent.ProjectVersion != int64(snapshot.FrozenInput.ProjectVersion) {
			return out, false, core.ErrInvalidRequest
		}
	}
	byID := map[string]core.DeliveryExportSourceRef{}
	for _, source := range snapshot.FrozenInput.Sources {
		byID[source.ID] = core.DeliveryExportSourceRef{SourceID: source.ID, AssetID: source.AssetID, AssetRevision: int64(source.AssetRevision)}
	}
	for _, chapter := range snapshot.FrozenInput.Chapters {
		for _, id := range chapter.SourceIDs {
			if _, exists := byID[id]; !exists {
				return out, false, core.ErrSourceUnavailable
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	refs := make([]core.DeliveryExportSourceRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, byID[id])
	}
	expected := int64(0)
	if artifact.Status == delivery.ExportVerified {
		expected = int64(snapshot.FrozenInput.ProjectVersion)
	}
	prepared, err := r.delivery.PrepareDeliveryExportNativeBasis(r.ctx, r.actor, artifact.ProjectID, expected, refs)
	if err != nil {
		return out, false, err
	}
	frozenProvenance := core.ProjectProvenanceSnapshot{}
	if snapshot.FrozenInput.SpecProvenance != nil {
		frozenProvenance = *snapshot.FrozenInput.SpecProvenance
	}
	prepared, err = r.provenance.PrepareFrozenProjectProvenance(prepared, r.actor, artifact.ProjectID, snapshot.FrozenInput.Spec, frozenProvenance, false)
	if err != nil {
		return out, false, err
	}
	// Both real source policies remain outside the transaction, including
	// historical Spec pins and the exact frozen chapter/quoted-source inputs.
	if err := r.base.authorizeSnapshotSources(prepared, r.actor.UserID, artifact.ProjectID, artifact.SnapshotID); err != nil {
		return out, false, err
	}
	err = r.db.WithContext(prepared).Transaction(func(tx *gorm.DB) error {
		port := core.NewTransactionAdapter(tx)
		validate := func() error {
			if err := r.delivery.ValidateDeliveryExportNativeBasisInTransaction(prepared, port, r.actor, artifact.ProjectID, refs); err != nil {
				return err
			}
			return r.provenance.ValidateFrozenProjectProvenanceInTransaction(prepared, port, r.actor, artifact.ProjectID, snapshot.FrozenInput.Spec, frozenProvenance, false)
		}
		if err := validate(); err != nil {
			return err
		}
		var err error
		out, replay, err = delivery.NewSQLiteExportStore(tx).RecordExport(artifact, attempt, requestHash)
		if err != nil {
			return err
		}
		if err := validate(); err != nil {
			return err
		}
		if !replay {
			// The governed use audit joins the existing parent transaction, so
			// either audit failure rolls back artifact, action and both audits.
			if useEvent != nil {
				if err := rulesetstore.RecordRuleSetUseInTransaction(tx, *useEvent); err != nil {
					return err
				}
			}
			audit, ok := port.(interface{ RecordAudit(core.AuditEvent) error })
			if !ok {
				return core.ErrInvalidState
			}
			if err := audit.RecordAudit(core.AuditEvent{ID: uuid.NewString(), ProjectID: artifact.ProjectID, ActorID: r.actor.UserID, TenantID: r.actor.TenantID, UserID: r.actor.UserID, Action: "delivery.export", Target: out.ID, Decision: "allow", Details: map[string]any{"snapshot_id": out.SnapshotID, "export_id": out.ID, "status": out.Status}, CreatedAt: time.Now().UTC()}); err != nil {
				return err
			}
			return validate()
		}
		return nil
	})
	if err != nil {
		return delivery.ExportArtifact{}, false, err
	}
	return out, replay, nil
}
