package workspace

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type deliveryGuardProjectAuthorizer struct{ projects *core.Service }

func (a deliveryGuardProjectAuthorizer) AuthorizeProject(ctx context.Context, user, projectID string) error {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != user {
		return core.ErrNotFound
	}
	return a.projects.Authorize(ctx, core.Actor{TenantID: caller.TenantID, UserID: user, Role: caller.Role}, projectID, "read")
}

type deliveryGuardFixture struct {
	db         *gorm.DB
	projects   *core.Service
	runtime    *SourceRuntime
	governance *TemplateGovernanceRuntime
	releases   *GuardedDeliveryRelease
	exports    *GuardedDeliveryExport
	actor      core.Actor
	project    core.Project
	ctx        context.Context
	chapters   []core.Chapter
}

// Actual activation, native source gateway, core authorization, confirmation,
// SQLite release/export stores and DOCX adapters share one database connection.
// No model or service is started. The source file contains synthetic text only.
func newDeliveryGuardFixture(t *testing.T, withSource bool, withSpecSource ...bool) deliveryGuardFixture {
	t.Helper()
	_, db, actor, p, _ := governanceAdapterFixture(t)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT,is_active INTEGER NOT NULL,deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT OR IGNORE INTO users(id,username,is_active) VALUES(?,?,1)`, actor.UserID, actor.UserID).Error)
	// governanceAdapterFixture already applied every production LingDoc migration.
	ctx := governanceCallerContext(context.Background(), actor)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, actor.Role)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: actor.UserID})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(cancel)
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, adapterSharesStub{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: NewResearchAdapter(db, projects, runtime, adapterSharesStub{}, nil, nil)})
	governance, err := NewTemplateGovernanceRuntime(db, projects, runtime, nil)
	require.NoError(t, err)
	projects.SetTemplateCatalogResolver(governance)
	projects.SetImpactRuleChecker(governance.ImpactRuleChecker())
	if len(withSpecSource) > 0 && withSpecSource[0] {
		_, ref := adapterSourceFixture(t, governance, db, p, actor)
		projects.RequireActivationRead(true)
		raw, _, _, err := projects.SaveSpec(ctx, actor, p.ID, "delivery-spec-source", core.SaveSpecInput{
			ExpectedSpecRevision: p.SpecRevision,
			Fields:               map[string]string{"research_subject": "合成源派生对象", "research_goal": "合成软件目标"},
			FieldMetadata:        map[string]core.SpecFieldInput{"research_subject": {Origin: "human", Status: "draft", Provenance: &core.ProvenanceRecord{SourceType: "source", SourceID: ref.ID, SourceVersion: ref.Version, Purpose: "project_spec"}}},
		})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &p))
		read, err := projects.RecordActivationRead(ctx, actor, p.ID, p.ProjectVersion)
		require.NoError(t, err)
		raw, _, _, err = projects.ActivateProject(ctx, actor, p.ID, "delivery-spec-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion, ActivationReadID: read.ID})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &p))
	} else {
		p = activateProjectFixture(t, projects, actor, p, ctx)
	}
	chapters := writeConfirmedChaptersFixture(t, projects, db, actor, p, ctx)
	if withSource {
		_, ref := adapterSourceFixture(t, governance, db, p, actor)
		chapter := chapters[0]
		raw, _, _, err := projects.SaveChapter(ctx, actor, p.ID, chapter.ID, "delivery-guard-source-body", core.SaveChapterInput{ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "合成来源用于检验撤权读取 [[source:" + ref.ID + "]]", SourceIDs: []string{ref.ID}})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &chapter))
		confirm := candidateadoption.NewConfirmationService(candidateadoption.NewSQLiteCandidateAdoptionStore(db), runtime.CandidateAdoptionSourcePolicy().(candidateadoption.ConfirmationSourcePolicy), adapterChapterAuthorizerStub{projects})
		_, _, err = confirm.ConfirmChapter(ctx, candidateadoption.ConfirmChapterInput{ProjectID: p.ID, ChapterID: chapter.ID, ActorID: actor.UserID, IdempotencyKey: "delivery-guard-confirm", ExpectedChapterVersionID: *chapter.CurrentVersionID, ExpectedSpecRevision: int(p.SpecRevision), Decisions: []candidateadoption.ReviewDecision{}})
		require.NoError(t, err)
		chapters, err = projects.ListChapters(ctx, actor, p.ID)
		require.NoError(t, err)
	}
	p, err = projects.GetProject(ctx, actor, p.ID)
	require.NoError(t, err)
	projects.SetRuntimeMode("real")
	guard := NewFullWorkflowGuard(fullWorkflowGuardPortsFor(projects), runtime)
	inputs := &candidateadoption.DeliveryInputService{Reader: candidateadoption.NewSQLiteCandidateAdoptionStore(db), Authorizer: deliveryGuardProjectAuthorizer{projects}}
	builder := runtime.DeliveryInputBuilder()
	snapshots := delivery.NewSQLiteSnapshotStore(db)
	releases := GuardFullWorkflowRelease(NewDeliveryReleaseService(inputs, builder, snapshots, runtime.CandidateAdoptionSourcePolicy()), guard)
	exports := GuardFullWorkflowExport(NewDeliveryExportServiceWithCurrentness(snapshots, delivery.NewSQLiteExportStore(db), DeliveryDocument{}, DeliveryDocument{}, inputs, runtime.WorkspaceSourcePolicy(), builder.(DeliveryCurrentness)), guard)
	pool, err := db.DB()
	require.NoError(t, err)
	require.Equal(t, 1, pool.Stats().MaxOpenConnections)
	return deliveryGuardFixture{db, projects, runtime, governance, releases, exports, actor, p, ctx, chapters}
}

func (f deliveryGuardFixture) requestContext(user string, role types.TenantRole, requestID string) context.Context {
	ctx := governanceCallerContext(f.ctx, core.Actor{TenantID: f.actor.TenantID, UserID: user, Role: role})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
	ctx = context.WithValue(ctx, types.RequestIDContextKey, requestID)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: user})
}

func TestFullWorkflowDeliveryHistoricalSnapshotListAndReplayRecheckSources(t *testing.T) {
	f := newDeliveryGuardFixture(t, true)
	snapshot, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-history-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.False(t, replay)
	require.NotEmpty(t, snapshot.FrozenInput.Sources)
	// Current chapters cease to use the historical source. This is an ordinary
	// actual chapter edit, so the current revision's stabilization gate passes.
	chapter := f.chapters[0]
	raw, _, _, err := f.projects.SaveChapter(f.ctx, f.actor, f.project.ID, chapter.ID, "delivery-withdraw-citation", core.SaveChapterInput{ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: f.project.SpecRevision, BodyMarkdown: "新版本使用独立人工说明，不引用旧来源。", SourceIDs: []string{}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &chapter))
	confirm := candidateadoption.NewConfirmationService(candidateadoption.NewSQLiteCandidateAdoptionStore(f.db), f.runtime.CandidateAdoptionSourcePolicy().(candidateadoption.ConfirmationSourcePolicy), adapterChapterAuthorizerStub{f.projects})
	_, _, err = confirm.ConfirmChapter(f.ctx, candidateadoption.ConfirmChapterInput{ProjectID: f.project.ID, ChapterID: chapter.ID, ActorID: f.actor.UserID, IdempotencyKey: "delivery-confirm-withdrawn-citation", ExpectedChapterVersionID: *chapter.CurrentVersionID, ExpectedSpecRevision: int(f.project.SpecRevision), Decisions: []candidateadoption.ReviewDecision{}})
	require.NoError(t, err)
	require.NoError(t, f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID))
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
	list, _, err := f.releases.List(f.ctx, f.actor.UserID, f.project.ID)
	require.Error(t, err)
	require.Empty(t, list, "denied history must not contain frozen excerpts")
	blocked, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-history-freeze", f.project.ProjectVersion)
	require.Error(t, err)
	require.False(t, replay)
	require.Empty(t, blocked.ID)
	require.Empty(t, blocked.FrozenInput.Chapters)
	require.Empty(t, blocked.FrozenInput.Sources)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Reauthorization recovers the same persisted object; denial did not erase it.
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id='kb-runtime'").Error)
	restored, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-history-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, snapshot.ID, restored.ID)
	require.False(t, restored.IsCurrent)
}

func TestFullWorkflowDeliveryExportRequiresCurrentHumanOwnerAndDownloadRetainsRead(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-owner-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)
	for _, user := range []string{"assistant", "observer"} {
		require.NoError(t, f.db.Exec("INSERT INTO users(id,username,is_active) VALUES(?,?,1)", user, user).Error)
		require.NoError(t, f.db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,?,'active')", f.actor.TenantID, user, types.TenantRoleContributor).Error)
		require.NoError(t, f.db.Exec("INSERT INTO lingdoc_members(project_id,user_id,role) VALUES(?,?,'collaborator')", f.project.ID, user).Error)
	}
	require.NoError(t, f.db.Exec(`INSERT INTO lingdoc_member_permissions(project_id,user_id,governance_role,function_roles_json,function_scopes_json,status) VALUES(?,?,'admin','[]','{}','active'),(?,?,'member','["observer"]','{}','active')`, f.project.ID, "assistant", f.project.ID, "observer").Error)
	for _, user := range []string{"assistant", "observer"} {
		t.Run(user, func(t *testing.T) {
			ctx := f.requestContext(user, types.TenantRoleContributor, "deny-export-"+user)
			artifact, _, err := f.exports.Start(ctx, user, f.project.ID, snapshot.ID, "deny-export-"+user)
			require.Error(t, err)
			require.Empty(t, artifact.ID)
		})
	}
	for _, principal := range []types.Principal{{Type: types.PrincipalAPITenant, ID: "7"}, {Type: types.PrincipalEmbedVisitor, ID: "visitor"}, {Type: types.PrincipalWebUser, ID: "another-user"}} {
		t.Run(principal.Type+principal.ID, func(t *testing.T) {
			ctx := types.WithPrincipal(f.ctx, principal)
			artifact, _, err := f.exports.Start(ctx, f.actor.UserID, f.project.ID, snapshot.ID, "machine-export-"+principal.Type)
			require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
			require.Empty(t, artifact.ID)
		})
	}
	artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-human-export")
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	readerCtx := f.requestContext("observer", types.TenantRoleContributor, "observer-download")
	downloaded, bytes, err := f.exports.Download(readerCtx, "observer", f.project.ID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, artifact.ID, downloaded.ID)
	require.NotEmpty(t, bytes)
	// A stale elevated token cannot re-export after the current tenant role shrinks.
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?", types.TenantRoleViewer, f.actor.TenantID, f.actor.UserID).Error)
	denied, _, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-human-export")
	require.Error(t, err)
	require.Empty(t, denied.ID)
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?", f.actor.Role, f.actor.TenantID, f.actor.UserID).Error)
	require.NoError(t, f.db.Exec("UPDATE users SET is_active=0 WHERE id=?", f.actor.UserID).Error)
	denied, _, err = f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "inactive-human-export")
	require.Error(t, err)
	require.Empty(t, denied.ID)
	require.NoError(t, f.db.Exec("UPDATE users SET is_active=1 WHERE id=?", f.actor.UserID).Error)
	machineCtx := types.WithTenantAPIKeyScope(f.ctx, types.TenantAPIKeyScope{KeyID: 71, FullAccess: true})
	denied, _, err = f.exports.Start(machineCtx, f.actor.UserID, f.project.ID, snapshot.ID, "scope-must-not-be-human")
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
	require.Empty(t, denied.ID)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "denials do not render or create another export")
}

func TestFullWorkflowDeliveryRuntimeUsesActualSnapshotExportAndServerCausation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	checkCtx := f.requestContext(f.actor.UserID, f.actor.Role, "actual-check-request")
	check, err := f.releases.Check(checkCtx, f.actor.UserID, f.project.ID, f.project.ProjectVersion)
	require.NoError(t, err)
	require.NotEmpty(t, check.ReceiptID)
	snapshot, _, err := f.releases.Prepare(f.requestContext(f.actor.UserID, f.actor.Role, "actual-freeze-request"), f.actor.UserID, f.project.ID, "delivery-trace-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.NotNil(t, snapshot.FrozenInput.SpecProvenance)
	artifact, _, err := f.exports.Start(f.requestContext(f.actor.UserID, f.actor.Role, "actual-export-request"), f.actor.UserID, f.project.ID, snapshot.ID, "delivery-trace-export")
	require.NoError(t, err)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	_, bytes, err := f.exports.Download(f.requestContext(f.actor.UserID, f.actor.Role, "actual-download-request"), f.actor.UserID, f.project.ID, artifact.ID)
	require.NoError(t, err)
	require.NotEmpty(t, bytes)
	events, err := f.projects.ListRuntimeEvents(f.ctx, f.actor, f.project.ID, "")
	require.NoError(t, err)
	byRequest := map[string]core.RuntimeEvent{}
	for _, event := range events {
		byRequest[event.RequestID] = event
	}
	preflight := byRequest["actual-check-request"]
	freeze := byRequest["actual-freeze-request"]
	export := byRequest["actual-export-request"]
	download := byRequest["actual-download-request"]
	require.Equal(t, check.ReceiptID, preflight.CorrelationID)
	require.Equal(t, check.ReceiptID, preflight.CausationID)
	require.Equal(t, "snapshot", freeze.Category)
	require.Equal(t, snapshot.ID, freeze.SnapshotID)
	require.Equal(t, preflight.ID, freeze.CausationID)
	require.Equal(t, freeze.ID, export.CausationID)
	require.Equal(t, export.ID, download.CausationID)
	for _, event := range []core.RuntimeEvent{freeze, export, download} {
		require.Equal(t, preflight.CorrelationID, event.CorrelationID)
		require.Equal(t, "runtime", event.Origin)
		require.Equal(t, "real", event.RuntimeMode)
		require.Equal(t, "success", event.Status)
	}
	require.Equal(t, snapshot.ID, export.SnapshotID)
	require.Equal(t, artifact.ID, export.ExportID)
	require.Equal(t, snapshot.ID, download.SnapshotID)
	require.Equal(t, artifact.ID, download.ExportID)
	// The SQLite reader retains provenance without borrowing current Spec refs.
	stored, err := delivery.NewSQLiteSnapshotStore(f.db).Get(f.project.ID, snapshot.ID)
	require.NoError(t, err)
	require.Equal(t, snapshot.FrozenInput.SpecProvenance, stored.FrozenInput.SpecProvenance)
}
func TestFullWorkflowDeliveryPauseAndActualRecoveryKeepCurrentReadAuthority(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-pause-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	artifact, _, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-pause-export")
	require.NoError(t, err)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	scope := core.RuntimeScope{Kind: "project", Value: f.project.ID}
	_, _, _, err = f.projects.ControlRuntime(f.ctx, f.actor, f.project.ID, "delivery-actual-pause", core.RuntimeControlInput{Action: "pause", Scope: scope, Reason: "合成暂停回归"})
	require.NoError(t, err)
	denied, _, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-pause-export")
	require.ErrorIs(t, err, core.ErrRuntimePaused)
	require.Empty(t, denied.ID)
	_, bytes, err := f.exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
	require.ErrorIs(t, err, core.ErrRuntimePaused)
	require.Empty(t, bytes)
	// Historical reads retain their existing current permission/source checks.
	visible, err := f.releases.Get(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID)
	require.NoError(t, err)
	require.Equal(t, snapshot.ID, visible.ID)
	_, _, _, err = f.projects.ControlRuntime(f.ctx, f.actor, f.project.ID, "delivery-actual-resume", core.RuntimeControlInput{Action: "resume", Scope: scope, ExpectedVersion: 1, Reason: "实际权限、快照和下载复验", SnapshotID: snapshot.ID, ExportID: artifact.ID})
	require.NoError(t, err)
	_, bytes, err = f.exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
	require.NoError(t, err)
	require.NotEmpty(t, bytes)
}

func TestFullWorkflowDeliveryFrozenSpecProvenanceRechecksSourceWithoutChapterCitation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false, true)
	check, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, f.project.ProjectVersion)
	require.NoError(t, err)
	require.Equal(t, delivery.CheckPassed, check.Status)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-spec-only-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.Empty(t, snapshot.FrozenInput.Sources, "source is used only by frozen Spec, not chapters")
	require.NotNil(t, snapshot.FrozenInput.SpecProvenance)
	pinned := false
	for _, field := range snapshot.FrozenInput.SpecProvenance.Fields {
		if field.FieldKey == "research_subject" {
			pinned = field.PinID != ""
		}
	}
	require.True(t, pinned, "derived Spec field retains its immutable server pin")
	artifact, _, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-spec-only-export")
	require.NoError(t, err)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
	visible, err := f.releases.Get(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID)
	require.Error(t, err)
	require.Empty(t, visible.FrozenInput.Spec)
	list, _, err := f.releases.List(f.ctx, f.actor.UserID, f.project.ID)
	require.Error(t, err)
	require.Empty(t, list)
	replay, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-spec-only-freeze", f.project.ProjectVersion)
	require.Error(t, err)
	require.Empty(t, replay.FrozenInput.Spec)
	_, bytes, err := f.exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
	require.Error(t, err)
	require.Empty(t, bytes)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id='kb-runtime'").Error)
	restored, err := f.releases.Get(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID)
	require.NoError(t, err)
	require.Equal(t, snapshot.ID, restored.ID)
	require.Equal(t, snapshot.FrozenInput.Spec, restored.FrozenInput.Spec)
}

func TestFullWorkflowDeliveryLegacySpecWithoutProvenanceCannotReturnBody(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-legacy-fixture-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	legacy := snapshot.FrozenInput
	legacy.SpecProvenance = nil // Reproduce JSON written before provenance pins.
	stored, err := delivery.NewReleaseService(delivery.NewMemorySnapshotStore()).Freeze(legacy)
	require.NoError(t, err)
	require.NoError(t, delivery.NewSQLiteSnapshotStore(f.db).Save(stored))
	denied, err := f.releases.Get(f.ctx, f.actor.UserID, f.project.ID, stored.ID)
	require.Error(t, err)
	require.Empty(t, denied.FrozenInput.Spec)
	list, _, err := f.releases.List(f.ctx, f.actor.UserID, f.project.ID)
	require.Error(t, err)
	require.Empty(t, list, "a partial list must not leak the legacy Spec body")
}

type deliveryGuardRenderHook struct{ after func() }

func (r deliveryGuardRenderHook) RenderFrozen(input delivery.DeliveryInput) ([]byte, error) {
	bytes, err := (DeliveryDocument{}).RenderFrozen(input)
	if err == nil {
		r.after()
	}
	return bytes, err
}

func TestFullWorkflowDeliveryRenderRevocationDoesNotPersistArtifact(t *testing.T) {
	for _, kind := range []string{"owner", "source"} {
		t.Run(kind, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, kind == "source")
			snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-render-race-freeze", f.project.ProjectVersion)
			require.NoError(t, err)
			require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)
			rendered := false
			f.exports.renderer = deliveryGuardRenderHook{after: func() {
				rendered = true
				if kind == "owner" {
					require.NoError(t, f.db.Exec("UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?", types.TenantRoleViewer, f.actor.TenantID, f.actor.UserID).Error)
				} else {
					require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
				}
			}}
			artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-render-race-export")
			require.True(t, rendered, "actual authorization and DOCX rendering must precede withdrawal")
			require.Error(t, err)
			require.False(t, replay)
			require.Empty(t, artifact.ID)
			var count int64
			require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&count).Error)
			require.Zero(t, count, "withdrawal cannot leave an authorized artifact/action row")
			if kind == "owner" {
				require.NoError(t, f.db.Exec("UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?", f.actor.Role, f.actor.TenantID, f.actor.UserID).Error)
			} else {
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id='kb-runtime'").Error)
			}
			f.exports.renderer = DeliveryDocument{}
			artifact, replay, err = f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-render-race-export")
			require.NoError(t, err)
			require.False(t, replay)
			require.Equal(t, delivery.ExportVerified, artifact.Status)
		})
	}
}

func TestFullWorkflowDeliveryCriticalExportAuditFailureRollsBackArtifactAndAction(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "delivery-audit-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(`CREATE TRIGGER delivery_guard_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action='delivery.export' BEGIN SELECT RAISE(ABORT,'synthetic delivery audit unavailable'); END`).Error)
	artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-audit-export")
	require.Error(t, err)
	require.False(t, replay)
	require.Empty(t, artifact.ID)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, f.db.Exec("DROP TRIGGER delivery_guard_audit_failure").Error)
	artifact, replay, err = f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "delivery-audit-export")
	require.NoError(t, err)
	require.False(t, replay, "failed transaction cannot preserve an export action")
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	require.NoError(t, f.db.Table("lingdoc_project_audits").Where("project_id=? AND action='delivery.export' AND target=?", f.project.ID, artifact.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
