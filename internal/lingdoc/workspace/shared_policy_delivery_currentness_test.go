package workspace

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

func TestSharedDeliveryCurrentnessMappingsRetainFrozenBasis(t *testing.T) {
	frozen := delivery.DeliveryInput{
		ProjectID: "project", ProjectVersion: 5, SpecRevision: 2,
		Template:       delivery.Template{ID: "template", Version: "version", RulesetHash: "rules"},
		TemplateCopyID: "copy", TemplateCopyVersion: 4, TemplateCopyContentHash: "copy-hash",
		AssetVersions:  []delivery.AssetVersion{{AssetID: "asset", Revision: 9}},
		SpecProvenance: &core.ProjectProvenanceSnapshot{ContextRevision: 7},
	}
	input := delivery.AccessCurrentnessInput(frozen)
	require.Equal(t, frozen.Template.ID, input.TemplateID)
	require.Equal(t, frozen.Template.Version, input.TemplateVersion)
	require.Equal(t, frozen.Template.RulesetHash, input.RulesetHash)
	require.EqualValues(t, 7, input.ContextRevision, "legacy frozen provenance supplies its own captured context, never today's context")
	require.Len(t, input.AssetVersions, 1)
	require.Equal(t, "asset", input.AssetVersions[0].AssetID)
	require.Equal(t, 9, input.AssetVersions[0].Revision)
	input.AssetVersions[0].Revision = 10
	require.Equal(t, 9, frozen.AssetVersions[0].Revision, "policy DTO must not mutate frozen asset revisions")
	current := &mergeDeliveryCurrentness{}
	policy := &accessPolicy{
		authorizer: &generationContextWorkspaceStub{},
		projects:   &generationContextWorkspaceStub{},
		context:    &generationContextWorkspaceStub{},
		reader: mergeDeliveryReader{input: candidateadoption.WorkspaceDeliveryInput{
			ProjectID: "project", ProjectVersion: 5, SpecRevision: 2, ContextRevision: 7,
			TemplateID: "template", TemplateVersion: "version", TemplateCopyID: "copy",
			TemplateCopyVersion: 4, TemplateCopyContentHash: "copy-hash", TemplateCopyRulesetHash: "rules",
		}},
		sources: current,
	}
	ok, err := policy.Current(context.Background(), LingDocAccessContext{TenantID: 1, UserID: "owner", ProjectID: "project"}, delivery.AccessCurrentnessInput(frozen))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, frozen.Template.ID, current.seen.Template.ID)
	require.Equal(t, frozen.Template.Version, current.seen.Template.Version)
	require.Equal(t, frozen.Template.RulesetHash, current.seen.Template.RulesetHash)
	require.EqualValues(t, 7, current.seen.ContextRevision)
	require.Equal(t, frozen.AssetVersions, current.seen.AssetVersions)
}

func TestSharedPolicyRejectsStaleFrozenDelivery(t *testing.T) {
	for _, change := range []string{"template_id", "template_version", "copy_content_hash", "ruleset_hash", "context_revision", "asset_revision"} {
		t.Run(change, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, change == "asset_revision")
			reader := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
			inputs := &candidateadoption.DeliveryInputService{Reader: reader, Authorizer: deliveryGuardProjectAuthorizer{f.projects}}
			policy := NewAccessPolicy(ProjectPorts{Authorizer: f.projects, Projects: f.projects, Context: f.projects}, f.runtime, reader)
			require.NotNil(t, policy)
			snapshots := delivery.NewSQLiteSnapshotStore(f.db)
			artifacts := delivery.NewSQLiteExportStore(f.db)
			releases := NewDeliveryReleaseServiceWithPolicy(inputs, f.runtime.DeliveryInputBuilder(), snapshots, f.runtime.CandidateAdoptionSourcePolicy(), policy, nil)
			exports := NewDeliveryExportServiceWithPolicy(snapshots, artifacts, DeliveryDocument{}, DeliveryDocument{}, inputs, f.runtime.WorkspaceSourcePolicy(), policy)
			snapshot, replay, err := releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "basis-freeze-"+change, f.project.ProjectVersion)
			require.NoError(t, err)
			require.False(t, replay)
			require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)
			artifact, _, err := exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "basis-export-before-"+change)
			require.NoError(t, err)
			require.Equal(t, delivery.ExportVerified, artifact.Status)
			_, bytes, err := exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
			require.NoError(t, err)
			require.NotEmpty(t, bytes)

			// Hold project/spec/chapter versions unchanged to isolate each frozen
			// basis dimension. These updates touch only this test's SQLite file.
			switch change {
			case "template_id":
				require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET template_id='changed-template' WHERE id=?", f.project.ID).Error)
			case "template_version":
				require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET template_version='changed-version' WHERE id=?", f.project.ID).Error)
			case "copy_content_hash":
				require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET content_hash='changed-content' WHERE project_id=?", f.project.ID).Error)
			case "ruleset_hash":
				require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET ruleset_hash='changed-rules' WHERE project_id=?", f.project.ID).Error)
			case "context_revision":
				require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET current_context_revision=current_context_revision+1 WHERE id=?", f.project.ID).Error)
			case "asset_revision":
				require.NotEmpty(t, snapshot.FrozenInput.AssetVersions)
				require.NoError(t, f.db.Exec("UPDATE lingdoc_project_assets SET asset_revision=asset_revision+1 WHERE project_id=? AND id=?", f.project.ID, snapshot.FrozenInput.AssetVersions[0].AssetID).Error)
			}
			current, err := policy.Current(f.ctx, LingDocAccessContext{TenantID: f.actor.TenantID, UserID: f.actor.UserID, ProjectID: f.project.ID}, delivery.AccessCurrentnessInput(snapshot.FrozenInput))
			require.True(t, err != nil || !current, "changed frozen basis remained current")
			stale, _, err := exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "basis-export-after-"+change)
			require.Error(t, err)
			require.Empty(t, stale.ID)
			historical, bytes, err := exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
			if change == "copy_content_hash" || change == "ruleset_hash" {
				require.Error(t, err, "tampered current template integrity must not authorize a historical download")
				require.Empty(t, bytes)
			} else {
				require.NoError(t, err, "ordinary frozen-basis drift must not disable a verified historical export")
				require.Equal(t, artifact.ID, historical.ID)
				require.NotEmpty(t, bytes, "historical export bytes remain available while project access is valid")
			}
			var count int64
			require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&count).Error)
			require.EqualValues(t, 1, count, "stale request must not create another artifact")
		})
	}
}

func TestSharedPolicyHistoricalDownloadRechecksFrozenSources(t *testing.T) {
	f := newDeliveryGuardFixture(t, true)
	reader := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
	inputs := &candidateadoption.DeliveryInputService{Reader: reader, Authorizer: deliveryGuardProjectAuthorizer{f.projects}}
	policy := NewAccessPolicy(ProjectPorts{Authorizer: f.projects, Projects: f.projects, Context: f.projects}, f.runtime, reader)
	snapshots := delivery.NewSQLiteSnapshotStore(f.db)
	artifacts := delivery.NewSQLiteExportStore(f.db)
	releases := NewDeliveryReleaseServiceWithPolicy(inputs, f.runtime.DeliveryInputBuilder(), snapshots, f.runtime.CandidateAdoptionSourcePolicy(), policy, nil)
	exports := NewDeliveryExportServiceWithPolicy(snapshots, artifacts, DeliveryDocument{}, DeliveryDocument{}, inputs, f.runtime.WorkspaceSourcePolicy(), policy)
	snapshot, _, err := releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "shared-source-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	artifact, _, err := exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "shared-source-export")
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
	_, bytes, err := exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
	require.Error(t, err, "source revocation must still block historical file access")
	require.Empty(t, bytes)
}

type sharedBasisChangeAssembler struct {
	actual DeliveryInputAssembler
	change func()
}

func (b sharedBasisChangeAssembler) Build(ctx context.Context, actor string, input candidateadoption.WorkspaceDeliveryInput) (delivery.DeliveryInput, error) {
	frozen, err := b.actual.Build(ctx, actor, input)
	if err == nil {
		b.change()
	}
	return frozen, err
}

func TestSharedPolicyFreezeRejectsBasisChangedDuringAssembly(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	reader := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
	inputs := &candidateadoption.DeliveryInputService{Reader: reader, Authorizer: deliveryGuardProjectAuthorizer{f.projects}}
	policy := NewAccessPolicy(ProjectPorts{Authorizer: f.projects, Projects: f.projects, Context: f.projects}, f.runtime, reader)
	builder := sharedBasisChangeAssembler{actual: f.runtime.DeliveryInputBuilder(), change: func() {
		require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET content_hash='changed-during-assembly' WHERE project_id=?", f.project.ID).Error)
	}}
	snapshots := delivery.NewSQLiteSnapshotStore(f.db)
	releases := NewDeliveryReleaseServiceWithPolicy(inputs, builder, snapshots, f.runtime.CandidateAdoptionSourcePolicy(), policy, nil)
	snapshot, _, err := releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "basis-race-freeze", f.project.ProjectVersion)
	require.Error(t, err)
	require.Empty(t, snapshot.ID)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.Zero(t, count, "changed basis must be refused before freeze persistence")
}

func TestSharedPolicyDeliveryReadsKeepCurrentTenantFloorAndNoWriteGrant(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	policy := NewAccessPolicy(ProjectPorts{Authorizer: f.projects, Projects: f.projects, Context: f.projects}, f.runtime, candidateadoption.NewSQLiteCandidateAdoptionStore(f.db))
	owner := LingDocAccessContext{TenantID: f.actor.TenantID, UserID: f.actor.UserID, Role: string(f.actor.Role), ProjectID: f.project.ID}
	require.NoError(t, policy.Authorize(f.ctx, owner, "contributor"))
	// The captured owner token does not survive a reduced current tenant role.
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET role='viewer' WHERE tenant_id=? AND user_id=?", f.actor.TenantID, f.actor.UserID).Error)
	require.Error(t, policy.Authorize(f.ctx, owner, "contributor"))
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?", f.actor.Role, f.actor.TenantID, f.actor.UserID).Error)
	const observer = "basis-observer"
	require.NoError(t, f.db.Exec("INSERT INTO users(id,username,is_active) VALUES(?,?,1)", observer, observer).Error)
	require.NoError(t, f.db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,'contributor','active')", f.actor.TenantID, observer).Error)
	require.NoError(t, f.db.Exec("INSERT INTO lingdoc_members(project_id,user_id,role) VALUES(?,?,'collaborator')", f.project.ID, observer).Error)
	require.NoError(t, f.db.Exec(`INSERT INTO lingdoc_member_permissions(project_id,user_id,governance_role,function_roles_json,function_scopes_json,status) VALUES(?,?,'member','["observer"]','{}','active')`, f.project.ID, observer).Error)
	ctx := f.requestContext(observer, "contributor", "basis-read-floor")
	reader := LingDocAccessContext{TenantID: f.actor.TenantID, UserID: observer, Role: "contributor", ProjectID: f.project.ID}
	require.NoError(t, policy.Authorize(ctx, reader, "contributor"))
	require.Error(t, policy.Authorize(ctx, reader, "write:"+f.chapters[0].ID), "delivery read must not grant chapter write")
}
