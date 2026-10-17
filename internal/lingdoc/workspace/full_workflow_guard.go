package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
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
	Core     *core.Service
	Sources  SourceApplicationService
	releases *DeliveryReleaseService
	exports  *DeliveryExportService
}

func NewFullWorkflowGuard(service *core.Service, sources SourceApplicationService) *FullWorkflowGuard {
	g := &FullWorkflowGuard{Core: service, Sources: sources}
	service.SetImpactPreflightChecker(fullPreflightChecker{g})
	service.SetRuntimeRecoveryChecker(fullRecoveryChecker{g})
	return g
}

type fullPreflightChecker struct{ guard *FullWorkflowGuard }

func (c fullPreflightChecker) Recheck(ctx context.Context, a core.Actor, pid, id string) (core.ImpactPreflightEvidence, error) {
	r, err := c.guard.Core.ReadImpactComputationReceipt(ctx, a, pid, id)
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
	return g.Core.AssertRevisionStabilized(ctx, a, pid)
}
func (g *FullWorkflowGuard) before(ctx context.Context, user, pid, operation string, stable bool) (core.Actor, error) {
	a, ok := fullWorkflowActor(ctx, user)
	if !ok || g == nil || g.Core == nil {
		return a, ErrNotFound
	}
	if err := g.Core.CheckRuntimeOperation(ctx, a, pid, operation, ""); err != nil {
		return a, err
	}
	if stable {
		if err := g.Core.AssertRevisionStabilized(ctx, a, pid); err != nil {
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
		_ = g.Core.ObserveDeliveryRuntime(ctx, a, pid, operation, status, time.Since(started).Milliseconds(), objects[0])
	} else {
		_ = g.Core.ObserveRuntime(ctx, a, pid, operation, status, time.Since(started).Milliseconds())
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
	ctx, _, err = s.guard.Core.PrepareProjectProvenance(ctx, a, pid, false)
	if err != nil {
		return out, err
	}
	basis, err := s.guard.Core.ReadPreflightBasis(ctx, a, pid, nil)
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
	receipt, err := s.guard.Core.StoreImpactComputationReceipt(ctx, a, basis, hex.EncodeToString(sum[:]), receiptStatus, evaluations, basis.SourceIDs)
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
	ctx, _, err = s.guard.Core.PrepareProjectProvenance(ctx, a, pid, false)
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
	g.Core.SetG2ActionExecutor(NewG2ActionExecutor(g.Core, g.Sources, wrapped))
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
	if err = s.guard.Core.AuthorizeDeliveryExport(ctx, a, pid); err != nil {
		return out, false, err
	}
	if err = s.guard.Core.AssertRevisionStabilized(ctx, a, pid); err != nil {
		return out, false, err
	}
	// Scope Owner authority to Start, including its internal access/replay
	// checks. The shared service remains read-authorized for downloads.
	if s.DeliveryExportService == nil || s.inputs == nil {
		return out, false, candidateadoption.ErrInvalidState
	}
	exportService := *s.DeliveryExportService
	inputs := *s.inputs
	inputs.Authorizer = fullWorkflowExportAuthorizer{s.guard.Core, inputs.Authorizer}
	exportService.inputs = &inputs
	runtime, ok := s.guard.Sources.(*SourceRuntime)
	if !ok || runtime == nil || runtime.db == nil {
		return out, false, candidateadoption.ErrDependencyUnavailable
	}
	if _, ok := exportService.exports.(*delivery.SQLiteExportStore); !ok {
		return out, false, candidateadoption.ErrDependencyUnavailable
	}
	exportService.exports = &fullWorkflowExportRecorder{ExportStore: exportService.exports, base: s.DeliveryExportService, core: s.guard.Core, db: runtime.db, ctx: ctx, actor: a}
	out, replay, err = exportService.Start(ctx, user, pid, snapshot, key)
	if err == nil {
		if err = s.guard.Core.AuthorizeDeliveryExport(ctx, a, pid); err != nil {
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
	core *core.Service
	base candidateadoption.DeliveryInputProjectAuthorizer
}

func (a fullWorkflowExportAuthorizer) AuthorizeProject(ctx context.Context, user, projectID string) error {
	if a.base == nil || a.core == nil {
		return candidateadoption.ErrDependencyUnavailable
	}
	if err := a.base.AuthorizeProject(ctx, user, projectID); err != nil {
		return err
	}
	actor, ok := fullWorkflowActor(ctx, user)
	if !ok {
		return core.ErrHumanDecisionRequired
	}
	return a.core.AuthorizeDeliveryExport(ctx, actor, projectID)
}

var _ ReleaseApplication = (*GuardedDeliveryRelease)(nil)
var _ TemplateCheckApplication = (*GuardedDeliveryRelease)(nil)
var _ ExportApplication = (*GuardedDeliveryExport)(nil)

// File rendering and source/origin IO happen before this recorder opens the
// final parent transaction. Permission, native source basis, artifact/action
// persistence and the critical business audit then commit together.
type fullWorkflowExportRecorder struct {
	delivery.ExportStore
	base  *DeliveryExportService
	core  *core.Service
	db    *gorm.DB
	ctx   context.Context
	actor core.Actor
}

func (r *fullWorkflowExportRecorder) ReplayExport(attempt delivery.ExportAttempt, requestHash string) (delivery.ExportArtifact, bool, error) {
	return r.ExportStore.(delivery.ExportRecorder).ReplayExport(attempt, requestHash)
}

func (r *fullWorkflowExportRecorder) RecordExport(artifact delivery.ExportArtifact, attempt delivery.ExportAttempt, requestHash string) (out delivery.ExportArtifact, replay bool, err error) {
	if attempt.ActorID != r.actor.UserID || attempt.ProjectID != artifact.ProjectID {
		return out, false, core.ErrNotFound
	}
	snapshot, err := r.base.snapshots.Get(artifact.ProjectID, artifact.SnapshotID)
	if err != nil {
		return out, false, err
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
	prepared, err := r.core.PrepareDeliveryExportNativeBasis(r.ctx, r.actor, artifact.ProjectID, expected, refs)
	if err != nil {
		return out, false, err
	}
	provenance := core.ProjectProvenanceSnapshot{}
	if snapshot.FrozenInput.SpecProvenance != nil {
		provenance = *snapshot.FrozenInput.SpecProvenance
	}
	prepared, err = r.core.PrepareFrozenProjectProvenance(prepared, r.actor, artifact.ProjectID, snapshot.FrozenInput.Spec, provenance, false)
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
			if err := r.core.ValidateDeliveryExportNativeBasisInTransaction(prepared, port, r.actor, artifact.ProjectID, refs); err != nil {
				return err
			}
			return r.core.ValidateFrozenProjectProvenanceInTransaction(prepared, port, r.actor, artifact.ProjectID, snapshot.FrozenInput.Spec, provenance, false)
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
