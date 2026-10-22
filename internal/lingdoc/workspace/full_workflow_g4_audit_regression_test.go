package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// The fixed RuleSet reader controls only the immutable definition under test.
// Project activation, source/provenance checks, evaluation and receipt storage
// continue through the existing real SQLite full-workflow fixture.
type wrapperG4RuleSetReader struct{ set lingdoctemplate.RuleSet }

func (r wrapperG4RuleSetReader) GetVersion(_ context.Context, _ core.Actor, id, version string) (lingdoctemplate.RuleSet, error) {
	if id != r.set.ID || version != r.set.Version {
		return lingdoctemplate.RuleSet{}, core.ErrNotFound
	}
	return r.set, nil
}

func (r wrapperG4RuleSetReader) GetVersionInTransaction(ctx context.Context, _ core.Transaction, actor core.Actor, id, version string) (lingdoctemplate.RuleSet, error) {
	return r.GetVersion(ctx, actor, id, version)
}

type wrapperG4UseAuditor struct {
	events []lingdoctemplate.RuleSetUseAuditEvent
	err    error
	before func()
}

func (a *wrapperG4UseAuditor) RecordRuleSetUse(_ context.Context, event lingdoctemplate.RuleSetUseAuditEvent) error {
	if a.before != nil {
		a.before()
	}
	a.events = append(a.events, event)
	return a.err
}

func wrapperG4ReceiptCount(t *testing.T, f deliveryGuardFixture) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Table("lingdoc_g6_records").Where("tenant_id=? AND project_id=? AND kind=?", f.actor.TenantID, f.project.ID, "preflight_check").Count(&count).Error)
	return count
}

func newWrapperG4AuditFixture(t *testing.T) (deliveryGuardFixture, *wrapperG4UseAuditor) {
	t.Helper()
	f := newDeliveryGuardFixture(t, false)
	// A new draft exercises the real Bind API before activation. The original
	// fixture project stays untouched, and the wrapper/core share the same DB.
	raw, _, _, err := f.projects.CreateProject(f.ctx, f.actor, "wrapper-g4-project", core.CreateProjectInput{
		Name: "G4包装入口审计回归", TemplateID: delivery.DemoTemplate().ID, TemplateVersion: delivery.DemoTemplate().Version,
	})
	require.NoError(t, err)
	var project core.Project
	require.NoError(t, json.Unmarshal(raw, &project))
	draft := lingdoctemplate.RuleSet{
		ID: "wrapper-g4-audit", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{{
			RuleID: "wrapper-goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal",
			Severity: delivery.SeverityBlocking, TriggerScenes: []string{"write_check", "preflight"},
			Enabled: true, Owner: "fixture", Parameters: map[string]any{"pattern": "^unoverridden$"},
		}},
	}
	// Freeze/export now verify the immutable source in the real RuleSet store.
	// Publish the fixture through that store rather than bypassing this check.
	catalog := rulesetstore.NewStore(f.db, rulesetstore.TenantMembershipAuthorizer{})
	catalogActor := rulesetstore.Actor{TenantID: f.actor.TenantID, UserID: f.actor.UserID, Role: string(f.actor.Role)}
	require.NoError(t, catalog.CreateDraft(f.ctx, catalogActor, draft))
	set, err := catalog.Publish(f.ctx, catalogActor, draft.ID, draft.Version)
	require.NoError(t, err)
	f.projects.WithRuleSetSnapshotReader(wrapperG4RuleSetReader{set})
	raw, _, _, err = f.projects.BindTemplateCopyRuleSet(f.ctx, f.actor, project.ID, "wrapper-g4-bind", core.BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
		Overrides:  []lingdoctemplate.RuleBindingOverride{{RuleID: "wrapper-goal-pattern", Parameters: map[string]any{"pattern": ".+"}}},
	})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &project))
	project = activateProjectFixture(t, f.projects, f.actor, project, f.ctx)
	f.chapters = writeConfirmedChaptersFixture(t, f.projects, f.db, f.actor, project, f.ctx)
	f.project, err = f.projects.GetProject(f.ctx, f.actor, project.ID)
	require.NoError(t, err)
	require.NotNil(t, f.project.TemplateCopy.Definition.RuleSetSnapshot)
	require.NotEmpty(t, f.project.TemplateCopy.Definition.RuleBindingOverrides)
	auditor := &wrapperG4UseAuditor{}
	f.releases.WithRuleSetUseAuditor(auditor)
	return f, auditor
}

func TestFullWorkflowG4WrapperCheckAuditsExactOverrideBeforeReceipt(t *testing.T) {
	f, auditor := newWrapperG4AuditFixture(t)
	before := wrapperG4ReceiptCount(t, f)
	auditor.before = func() {
		require.Equal(t, before, wrapperG4ReceiptCount(t, f), "use audit must precede the new durable receipt")
	}
	result, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, f.project.ProjectVersion)
	require.NoError(t, err)
	require.Equal(t, delivery.CheckPassed, result.Status)
	require.NotEmpty(t, result.ReceiptID)
	require.Len(t, auditor.events, 1, "wrapper must use the shared auditor exactly once")
	event := auditor.events[0]
	copy := f.project.TemplateCopy
	ref := copy.Definition.RuleSetRef
	require.NotNil(t, ref)
	require.Equal(t, ref.ID, event.RuleSetID)
	require.Equal(t, ref.Version, event.RuleSetVersion)
	require.Equal(t, ref.Hash, event.RuleSetHash)
	require.Equal(t, copy.RulesetHash, event.EffectiveRulesetHash)
	require.NotEqual(t, event.RuleSetHash, event.EffectiveRulesetHash)
	require.Equal(t, copy.ID, event.TemplateCopyID)
	require.Equal(t, copy.Version, event.TemplateCopyVersion)
	require.Equal(t, f.project.ProjectVersion, event.ProjectVersion)
	require.Equal(t, "preflight", event.Action)
	require.Equal(t, string(result.Status), event.Outcome)
	receipt, err := f.projects.ReadImpactComputationReceipt(f.ctx, f.actor, f.project.ID, result.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "passed", receipt.Status)
	require.Equal(t, event.EffectiveRulesetHash, receipt.RulesetHash)
	found := false
	for _, evaluation := range receipt.Evaluations {
		if evaluation.RuleID == "wrapper-goal-pattern" {
			found = true
			require.Equal(t, lingdoctemplate.EvaluationPass, evaluation.Status, "nonempty override must change the source pattern's failing result")
			require.Equal(t, ref.ID, evaluation.RuleSetID)
			require.Equal(t, ref.Version, evaluation.RuleSetVersion)
		}
	}
	require.True(t, found, "receipt must contain the real bound override evaluation")
	require.Equal(t, before+1, wrapperG4ReceiptCount(t, f))
}

func TestFullWorkflowG4WrapperAuditFailureLeavesNoReceipt(t *testing.T) {
	for _, kind := range []string{"write-error", "missing-auditor"} {
		t.Run(kind, func(t *testing.T) {
			f, auditor := newWrapperG4AuditFixture(t)
			before := wrapperG4ReceiptCount(t, f)
			failure := errors.New("synthetic RuleSet use audit unavailable")
			if kind == "missing-auditor" {
				f.releases.WithRuleSetUseAuditor(nil)
			} else {
				auditor.err = failure
			}
			result, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, f.project.ProjectVersion)
			if kind == "write-error" {
				require.ErrorIs(t, err, failure)
				require.Len(t, auditor.events, 1, "failure must come from the real wrapper audit call")
			} else {
				require.Error(t, err)
				require.Empty(t, auditor.events)
			}
			require.Empty(t, result.ReceiptID)
			require.Equal(t, before, wrapperG4ReceiptCount(t, f), "audit rejection must not persist a computation receipt")
			var audits int64
			require.NoError(t, f.db.Table("lingdoc_project_audits").Where("project_id=? AND action=?", f.project.ID, "preflight.compute").Count(&audits).Error)
			require.Zero(t, audits)
		})
	}
}

func wrapperG4ExportAuditCounts(t *testing.T, f deliveryGuardFixture, key string) (artifacts, actions, uses, critical int64) {
	t.Helper()
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&artifacts).Error)
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=? AND action_actor_id=? AND action_key=?", f.project.ID, f.actor.UserID, key).Count(&actions).Error)
	require.NoError(t, f.db.Table("lingdoc_ruleset_use_audits").Where("project_id=? AND action=?", f.project.ID, "export").Count(&uses).Error)
	require.NoError(t, f.db.Table("lingdoc_project_audits").Where("project_id=? AND action=?", f.project.ID, "delivery.export").Count(&critical).Error)
	return
}

func TestFullWorkflowG4ExportRecorderCommitsExactUseAuditAndReplaysOnce(t *testing.T) {
	f, auditor := newWrapperG4AuditFixture(t)
	f.exports.WithRuleSetUseAuditor(auditor)
	snapshot, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "wrapper-g4-export-freeze", f.project.ProjectVersion)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)
	artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "wrapper-g4-export")
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	require.Equal(t, snapshot.RuleSetRef, artifact.RuleSetRef)
	var audit struct {
		SourceID, SourceVersion, SourceHash                       string
		EffectiveRulesetHash, TemplateCopyID, ResourceID, Outcome string
		TemplateCopyVersion, ProjectVersion                       int64
	}
	require.NoError(t, f.db.Table("lingdoc_ruleset_use_audits").
		Select("ruleset_id AS source_id, ruleset_version AS source_version, ruleset_hash AS source_hash, effective_ruleset_hash, template_copy_id, template_copy_version, project_version, resource_id, outcome").
		Where("project_id=? AND action=? AND resource_id=?", f.project.ID, "export", artifact.ID).Take(&audit).Error)
	require.Equal(t, snapshot.RuleSetRef.ID, audit.SourceID)
	require.Equal(t, snapshot.RuleSetRef.Version, audit.SourceVersion)
	require.Equal(t, snapshot.RuleSetRef.Hash, audit.SourceHash)
	require.Equal(t, snapshot.FrozenInput.Template.RulesetHash, audit.EffectiveRulesetHash)
	require.NotEqual(t, audit.SourceHash, audit.EffectiveRulesetHash)
	require.Equal(t, snapshot.FrozenInput.TemplateCopyID, audit.TemplateCopyID)
	require.Equal(t, snapshot.FrozenInput.TemplateCopyVersion, audit.TemplateCopyVersion)
	require.EqualValues(t, snapshot.FrozenInput.ProjectVersion, audit.ProjectVersion)
	require.Equal(t, artifact.ID, audit.ResourceID)
	require.Equal(t, string(artifact.Status), audit.Outcome)
	stored, err := delivery.NewSQLiteExportStore(f.db).GetExport(f.project.ID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, artifact.RuleSetRef, stored.RuleSetRef)
	replayed, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "wrapper-g4-export")
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, artifact.ID, replayed.ID)
	artifacts, actions, uses, critical := wrapperG4ExportAuditCounts(t, f, "wrapper-g4-export")
	for _, count := range []int64{artifacts, actions, uses, critical} {
		require.EqualValues(t, 1, count, "artifact/action/use audit/critical audit must commit and replay once")
	}
}

func TestFullWorkflowG4ExportEitherAuditFailureRollsBackArtifactActionAndBothAudits(t *testing.T) {
	for _, kind := range []string{"ruleset-use", "critical-business"} {
		t.Run(kind, func(t *testing.T) {
			f, auditor := newWrapperG4AuditFixture(t)
			f.exports.WithRuleSetUseAuditor(auditor)
			snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "wrapper-g4-rollback-freeze", f.project.ProjectVersion)
			require.NoError(t, err)
			trigger := `CREATE TRIGGER wrapper_g4_export_audit_failure BEFORE INSERT ON lingdoc_ruleset_use_audits WHEN NEW.action='export' BEGIN SELECT RAISE(ABORT,'synthetic governed export audit failure'); END`
			if kind == "critical-business" {
				trigger = `CREATE TRIGGER wrapper_g4_export_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action='delivery.export' BEGIN SELECT RAISE(ABORT,'synthetic governed export audit failure'); END`
			}
			require.NoError(t, f.db.Exec(trigger).Error)
			artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "wrapper-g4-export-rollback")
			require.Error(t, err)
			require.Contains(t, err.Error(), "synthetic governed export audit failure", "the real guarded recorder must reach the selected audit write")
			require.False(t, replay)
			require.Empty(t, artifact.ID)
			artifacts, actions, uses, critical := wrapperG4ExportAuditCounts(t, f, "wrapper-g4-export-rollback")
			for _, count := range []int64{artifacts, actions, uses, critical} {
				require.Zero(t, count, "audit failure must roll back artifact, action and both audits")
			}
			require.NoError(t, f.db.Exec("DROP TRIGGER wrapper_g4_export_audit_failure").Error)
			artifact, replay, err = f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "wrapper-g4-export-rollback")
			require.NoError(t, err)
			require.False(t, replay, "failed export cannot leave an idempotency action behind")
			require.Equal(t, delivery.ExportVerified, artifact.Status)
			artifacts, actions, uses, critical = wrapperG4ExportAuditCounts(t, f, "wrapper-g4-export-rollback")
			for _, count := range []int64{artifacts, actions, uses, critical} {
				require.EqualValues(t, 1, count)
			}
		})
	}
}
