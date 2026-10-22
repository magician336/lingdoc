package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

// auditlessRepository masks the optional audit capability while keeping the
// normal workspace and ChangeSet persistence ports available to the service.
type auditlessRepository struct{ inner Repository }

func (r auditlessRepository) Transaction(ctx context.Context, options TransactionOptions, fn func(Transaction) error) error {
	return r.inner.Transaction(ctx, options, func(tx Transaction) error {
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		ports, ok := tx.(auditlessSourcePorts)
		if !ok {
			return ErrInvalidState
		}
		return fn(auditlessTransaction{Transaction: tx, changes: changes, auditlessSourcePorts: ports})
	})
}

// Preserve the real source/history/impact readers in the same transaction.
// Only audit capability is deliberately absent in this failure fixture.
type auditlessSourcePorts interface {
	projectRecordTransaction
	impactSourceReadTransaction
	impactTransaction
	semanticTransaction
}

type auditlessTransaction struct {
	Transaction
	auditlessSourcePorts
	changes changeSetTransaction
}

func (tx auditlessTransaction) InsertChangeSet(change ChangeSet) error {
	return tx.changes.InsertChangeSet(change)
}
func (tx auditlessTransaction) ChangeSet(projectID, changeSetID string) (ChangeSet, error) {
	return tx.changes.ChangeSet(projectID, changeSetID)
}
func (tx auditlessTransaction) ChangeSets(projectID string) ([]ChangeSet, error) {
	return tx.changes.ChangeSets(projectID)
}
func (tx auditlessTransaction) UpdateChangeSet(previous, next ChangeSet) error {
	return tx.changes.UpdateChangeSet(previous, next)
}
func (tx auditlessTransaction) InvalidateChapterConfirmations(projectID string, chapterIDs []string) error {
	return tx.changes.InvalidateChapterConfirmations(projectID, chapterIDs)
}

func seedActiveImpactProject(t *testing.T, svc *Service, actor Actor) Project {
	t.Helper()
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "change-create", CreateProjectInput{Name: "正式课题", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(context.Background(), actor, project.ID, "change-spec-1", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "旧主题", "research_goal": "旧目标"}})
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.ActivateProject(context.Background(), actor, project.ID, "change-activate", ActivateProjectInput{ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	return asProject(t, raw)
}

func TestChangeSetApplyAdvancesRevisionAndInvalidatesImpacts(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	input := CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs: []string{chapters[0].ID, chapters[1].ID}, Reason: "研究对象发生变化"}
	raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-change", input)
	if err != nil || status != 201 || replay {
		t.Fatalf("create changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil || change.Status != "assessed" || len(change.Impacts) != 2 {
		t.Fatalf("change preview: %+v %v", change, err)
	}
	raw, status, replay, err = svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-apply")
	if err != nil || status != 200 || replay {
		t.Fatalf("apply changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	firstApply := append([]byte(nil), raw...)
	replayedApply, replayStatus, replayed, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-apply")
	if err != nil || replayStatus != 200 || !replayed || string(replayedApply) != string(firstApply) {
		t.Fatalf("apply replay: status=%d replay=%v same_response=%v err=%v", replayStatus, replayed, string(replayedApply) == string(firstApply), err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || project.CurrentContextRevision != 2 || project.SpecRevision != 2 || project.Spec["research_subject"] != "新主题" {
		t.Fatalf("project after apply: %+v %v", project, err)
	}
	if field := project.SpecFields["research_subject"]; field.ModifiedProjectVersion != project.ProjectVersion || field.Status != "pending_confirmation" || field.Origin != "human" {
		t.Fatalf("applied field metadata: %+v", field)
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, chapter := range chapters {
		if chapter.ConfirmationValid {
			t.Fatalf("chapter %s retained confirmation after semantic change", chapter.ID)
		}
	}
	var applied ChangeSet
	if err := json.Unmarshal(raw, &applied); err != nil || applied.Status != "applied" || applied.TargetContextRevision == nil || *applied.TargetContextRevision != 2 {
		t.Fatalf("applied changeset: %+v %v", applied, err)
	}
}

func TestActiveTemplateCopyUpgradeUsesOwnerChangeSetAndKeepsHistory(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-template-upgrade.db"))
	owner := Actor{TenantID: 309, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	if project.TemplateCopy == nil || project.TemplateCopy.Status != TemplateCopyBound {
		t.Fatalf("active project has no bound template copy: %+v", project.TemplateCopy)
	}
	chapters, err := svc.ListChapters(context.Background(), owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
	fields[0].Label += "（升级）"
	upgrade := &TemplateUpgradeInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: fields, Sections: append([]lingdoctemplate.Section{}, project.TemplateCopy.Definition.Sections...),
		Terms:          append([]lingdoctemplate.Term{}, project.TemplateCopy.Definition.Terms...),
		RequiredFields: append([]string{}, project.TemplateCopy.Definition.RequiredFields...),
		Rules:          append([]lingdoctemplate.Rule{}, project.TemplateCopy.Definition.Rules...),
	}
	if _, _, _, err := svc.SaveTemplateCopy(context.Background(), owner, project.ID, "change-active-copy-write", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: upgrade.Fields, Sections: upgrade.Sections, Terms: upgrade.Terms,
		RequiredFields: upgrade.RequiredFields, Rules: upgrade.Rules,
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("direct active copy edit error = %v, want ErrInvalidState", err)
	}
	createdRaw, status, replay, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "change-template-upgrade-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "按团队评审更新章节和字段显示名", TemplateUpgrade: upgrade,
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("create template ChangeSet: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(createdRaw, &change); err != nil || change.TemplateUpgrade == nil || len(change.Impacts) != len(chapters) {
		t.Fatalf("template ChangeSet preview: impacts=%d upgrade=%+v err=%v", len(change.Impacts), change.TemplateUpgrade, err)
	}
	impact := change.TemplateUpgrade.Impact
	if len(impact.AffectedChapterIDs) != len(chapters) || len(impact.AffectedFieldIDs) != 1 || impact.AffectedFieldIDs[0] != fields[0].ID ||
		impact.ValidationIssueEffect == "" || impact.DeliverySnapshotEffect == "" {
		t.Fatalf("template upgrade impact is incomplete: %+v", impact)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "change-template-upgrade-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply template ChangeSet: status=%d replay=%v err=%v", status, replay, err)
	}
	updated, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil || updated.CurrentContextRevision != project.CurrentContextRevision+1 || updated.SpecRevision != project.SpecRevision+1 || updated.TemplateCopyVersion != project.TemplateCopyVersion+1 {
		t.Fatalf("project after template upgrade: revision=%d spec=%d copy=%d err=%v", updated.CurrentContextRevision, updated.SpecRevision, updated.TemplateCopyVersion, err)
	}
	if updated.TemplateCopy.Definition.Fields[0].Label != fields[0].Label || updated.TemplateCopy.Status != TemplateCopyBound {
		t.Fatalf("new template copy not bound: %+v", updated.TemplateCopy)
	}
	oldCopy, err := svc.GetTemplateCopy(context.Background(), owner, project.ID, project.TemplateCopyVersion)
	if err != nil || oldCopy.Status != TemplateCopySuperseded || oldCopy.Definition.Fields[0].Label == fields[0].Label {
		t.Fatalf("old template copy history was not retained: %+v err=%v", oldCopy, err)
	}
}

func TestActiveRuleSetMigrationUsesExactVersionedChangeSet(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-ruleset-migration.db"))
	owner := Actor{TenantID: 318, UserID: "owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, owner)
	v1, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
		ID: "grant-rules", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{{RuleID: "goal-required", DefinitionID: "presence", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: "blocking",
			TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "research-office"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	v2Draft := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &v1}).RuleSetSnapshot
	v2Draft.Version = "v2"
	v2Draft.State, v2Draft.RulesetHash = lingdoctemplate.RuleSetDraft, ""
	v2Draft.Bindings[0].Message = "A new required-field check"
	v2, err := lingdoctemplate.PublishRuleSet(*v2Draft)
	if err != nil {
		t.Fatal(err)
	}
	if v1.RulesetHash == v2.RulesetHash {
		t.Fatal("test RuleSet versions must have distinct source hashes")
	}
	reader := &ruleSetSnapshotReaderStub{set: v1}
	svc.WithRuleSetSnapshotReader(reader)
	raw, _, _, err := svc.CreateProject(context.Background(), owner, "ruleset-migration-create", CreateProjectInput{Name: "migration", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, "ruleset-migration-bind-v1", BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef: &lingdoctemplate.RuleSetReference{ID: v1.ID, Version: v1.Version, Hash: v1.RulesetHash},
	})
	if err != nil {
		t.Fatalf("bind original RuleSet version: %v", err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(context.Background(), owner, project.ID, "ruleset-migration-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision,
		Fields:               map[string]string{"research_subject": "迁移主题", "research_goal": "迁移目标"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.ActivateProject(context.Background(), owner, project.ID, "ruleset-migration-activate", ActivateProjectInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	oldCopy := project.TemplateCopy
	if oldCopy == nil || oldCopy.TemplateVersionRuleSetRef == nil || oldCopy.TemplateVersionRuleSetRef.Version != "v1" {
		t.Fatalf("active project was not bound to the original exact RuleSet version: %+v", oldCopy)
	}
	oldIssue := ValidationIssueBinding{IssueID: "historic-goal-required", RuleID: "goal-required", RulesetHash: oldCopy.RulesetHash,
		Severity: "blocking", TargetID: "research_goal", ProjectVersion: project.ProjectVersion}
	if _, _, _, err := svc.RecordValidationIssueDisposition(context.Background(), owner, project.ID, "ruleset-migration-dismiss-v1", ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion, ValidationIssueBinding: oldIssue, Action: "dismiss", Reason: "已记录旧规则版本的人工处置",
	}); err != nil {
		t.Fatalf("record v1 issue disposition: %v", err)
	}
	reader.set = v2
	definition := project.TemplateCopy.Definition
	overrideEnabled := true
	upgrade := &TemplateUpgradeInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef:           &lingdoctemplate.RuleSetReference{ID: v2.ID, Version: v2.Version, Hash: v2.RulesetHash},
		RuleBindingOverrides: []lingdoctemplate.RuleBindingOverride{{RuleID: "goal-required", Enabled: &overrideEnabled}},
		Fields:               append([]lingdoctemplate.Field{}, definition.Fields...), Sections: append([]lingdoctemplate.Section{}, definition.Sections...),
		Terms: append([]lingdoctemplate.Term{}, definition.Terms...), RequiredFields: append([]string{}, definition.RequiredFields...),
		Rules: append([]lingdoctemplate.Rule{}, definition.Rules...),
	}
	createdRaw, status, replayed, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "ruleset-migration-assess-v2", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "迁移到新的不可变 RuleSet 版本", TemplateUpgrade: upgrade,
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("assess RuleSet migration: status=%d replayed=%v err=%v", status, replayed, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(createdRaw, &change); err != nil || change.TemplateUpgrade == nil {
		t.Fatalf("assessed RuleSet migration = %+v err=%v", change, err)
	}
	assessedRef := lingdoctemplate.RuleSetReferenceFor(change.TemplateUpgrade.Definition)
	if assessedRef == nil || *assessedRef != (lingdoctemplate.RuleSetReference{ID: v2.ID, Version: v2.Version, Hash: v2.RulesetHash}) {
		t.Fatalf("ChangeSet did not pin the exact target RuleSet before assessment: %+v", assessedRef)
	}
	// The ChangeSet payload is the durable evidence boundary. If its stored
	// preview hash is damaged after assessment, Apply must reject it before
	// changing the active copy or recording a migration operation/audit.
	var tamperedFields struct {
		Fields          []ChangeFieldDelta `json:"fields"`
		TemplateUpgrade *TemplateUpgrade   `json:"template_upgrade,omitempty"`
	}
	if err := json.Unmarshal([]byte(func() string {
		var row changeSetRow
		if err := svc.testDB().Where("id = ? AND project_id = ?", change.ID, project.ID).First(&row).Error; err != nil {
			t.Fatalf("read assessed ChangeSet payload: %v", err)
		}
		return row.FieldsJSON
	}()), &tamperedFields); err != nil {
		t.Fatal(err)
	}
	if tamperedFields.TemplateUpgrade == nil {
		t.Fatal("assessed ChangeSet did not persist template upgrade evidence")
	}
	originalFieldsJSON, err := json.Marshal(tamperedFields)
	if err != nil {
		t.Fatal(err)
	}
	tamperedFields.TemplateUpgrade.Preview.TargetRulesetHash = strings.Repeat("f", 64)
	tamperedPayload, err := json.Marshal(tamperedFields)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&changeSetRow{}).Where("id = ? AND project_id = ?", change.ID, project.ID).Update("fields_json", string(tamperedPayload)).Error; err != nil {
		t.Fatal(err)
	}
	var copiesBeforeEvidenceTamper, operationsBeforeEvidenceTamper, auditsBeforeEvidenceTamper int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesBeforeEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", "ruleset-migration-tampered-evidence").Count(&operationsBeforeEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", project.ID, "project.change_set.apply", change.ID).Count(&auditsBeforeEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, replayed, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "ruleset-migration-tampered-evidence"); !errors.Is(err, ErrInvalidState) || replayed {
		t.Fatalf("tampered migration evidence: replayed=%v err=%v, want fail-closed non-replayed rejection", replayed, err)
	}
	unchangedProject, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchangedChange, err := svc.GetChangeSet(context.Background(), owner, project.ID, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	var copiesAfterEvidenceTamper, operationsAfterEvidenceTamper, auditsAfterEvidenceTamper int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesAfterEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", "ruleset-migration-tampered-evidence").Count(&operationsAfterEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", project.ID, "project.change_set.apply", change.ID).Count(&auditsAfterEvidenceTamper).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedProject.ProjectVersion != project.ProjectVersion || unchangedProject.TemplateCopyVersion != project.TemplateCopyVersion ||
		unchangedChange.Status != "assessed" || copiesAfterEvidenceTamper != copiesBeforeEvidenceTamper ||
		operationsAfterEvidenceTamper != operationsBeforeEvidenceTamper || auditsAfterEvidenceTamper != auditsBeforeEvidenceTamper {
		t.Fatalf("tampered migration evidence left partial writes: project=%+v change=%+v copies %d->%d operations %d->%d audits %d->%d",
			unchangedProject, unchangedChange, copiesBeforeEvidenceTamper, copiesAfterEvidenceTamper,
			operationsBeforeEvidenceTamper, operationsAfterEvidenceTamper, auditsBeforeEvidenceTamper, auditsAfterEvidenceTamper)
	}
	if err := svc.testDB().Model(&changeSetRow{}).Where("id = ? AND project_id = ?", change.ID, project.ID).Update("fields_json", string(originalFieldsJSON)).Error; err != nil {
		t.Fatal(err)
	}
	noAuditKey := "ruleset-migration-apply-without-audit"
	var copiesBeforeNoAudit, operationsBeforeNoAudit, auditsBeforeNoAudit int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesBeforeNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", noAuditKey).Count(&operationsBeforeNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", project.ID, "project.change_set.apply", change.ID).Count(&auditsBeforeNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	originalRepository := svc.repository
	svc.repository = auditlessRepository{inner: originalRepository}
	_, _, noAuditReplay, noAuditErr := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, noAuditKey)
	svc.repository = originalRepository
	if !errors.Is(noAuditErr, ErrInvalidState) || noAuditReplay {
		t.Fatalf("apply without transactional audit: replay=%v err=%v, want a non-replayed fail-closed rejection", noAuditReplay, noAuditErr)
	}
	projectAfterNoAudit, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	changeAfterNoAudit, err := svc.GetChangeSet(context.Background(), owner, project.ID, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	var copiesAfterNoAudit, operationsAfterNoAudit, auditsAfterNoAudit int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesAfterNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", noAuditKey).Count(&operationsAfterNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", project.ID, "project.change_set.apply", change.ID).Count(&auditsAfterNoAudit).Error; err != nil {
		t.Fatal(err)
	}
	if projectAfterNoAudit.ProjectVersion != project.ProjectVersion || projectAfterNoAudit.TemplateCopyVersion != project.TemplateCopyVersion ||
		changeAfterNoAudit.Status != "assessed" || copiesAfterNoAudit != copiesBeforeNoAudit ||
		operationsAfterNoAudit != operationsBeforeNoAudit || auditsAfterNoAudit != auditsBeforeNoAudit {
		t.Fatalf("missing-audit rejection left partial writes: project=%+v change=%+v copies %d->%d operations %d->%d audits %d->%d",
			projectAfterNoAudit, changeAfterNoAudit, copiesBeforeNoAudit, copiesAfterNoAudit,
			operationsBeforeNoAudit, operationsAfterNoAudit, auditsBeforeNoAudit, auditsAfterNoAudit)
	}
	applyKey := "ruleset-migration-apply-v2"
	var copiesBefore, operationsBefore, auditsBefore int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", applyKey).Count(&operationsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project.change_set.apply").Count(&auditsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Exec("CREATE TRIGGER change_set_apply_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'project.change_set.apply' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, replayed, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, applyKey); err == nil || replayed {
		t.Fatalf("audit failure apply: replayed=%v err=%v, want transactional failure", replayed, err)
	}
	if err := svc.testDB().Exec("DROP TRIGGER change_set_apply_audit_failure").Error; err != nil {
		t.Fatal(err)
	}
	failedProject, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedChange, err := svc.GetChangeSet(context.Background(), owner, project.ID, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	var copiesAfterFailure, operationsAfterFailure, auditsAfterFailure int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copiesAfterFailure).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", applyKey).Count(&operationsAfterFailure).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project.change_set.apply").Count(&auditsAfterFailure).Error; err != nil {
		t.Fatal(err)
	}
	if failedProject.ProjectVersion != project.ProjectVersion || failedProject.TemplateCopyVersion != project.TemplateCopyVersion ||
		failedProject.TemplateCopy.ContentHash != project.TemplateCopy.ContentHash || failedChange.Status != "assessed" ||
		copiesAfterFailure != copiesBefore || operationsAfterFailure != operationsBefore || auditsAfterFailure != auditsBefore {
		t.Fatalf("audit failure left partial migration: project=%+v change=%+v copies %d->%d operations %d->%d audits %d->%d", failedProject, failedChange, copiesBefore, copiesAfterFailure, operationsBefore, operationsAfterFailure, auditsBefore, auditsAfterFailure)
	}
	if _, status, replayed, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, applyKey); err != nil || status != 200 || replayed {
		t.Fatalf("apply RuleSet migration: status=%d replayed=%v err=%v", status, replayed, err)
	}
	updated, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil || updated.TemplateCopyVersion != project.TemplateCopyVersion+1 || updated.TemplateCopy == nil {
		t.Fatalf("project did not advance to new copy version: project=%+v err=%v", updated.TemplateCopy, err)
	}
	newRef := lingdoctemplate.RuleSetReferenceFor(updated.TemplateCopy.Definition)
	if newRef == nil || *newRef != *assessedRef || updated.TemplateCopy.TemplateVersionRuleSetRef == nil || *updated.TemplateCopy.TemplateVersionRuleSetRef != *assessedRef {
		t.Fatalf("applied copy did not retain exact v2 identity: ref=%+v copy=%+v", newRef, updated.TemplateCopy)
	}
	oldHistory, err := svc.GetTemplateCopy(context.Background(), owner, project.ID, oldCopy.Version)
	if err != nil || oldHistory.Status != TemplateCopySuperseded || oldHistory.TemplateVersionRuleSetRef == nil || *oldHistory.TemplateVersionRuleSetRef != (lingdoctemplate.RuleSetReference{ID: v1.ID, Version: v1.Version, Hash: v1.RulesetHash}) {
		t.Fatalf("previous copy and its RuleSet identity were not preserved: %+v err=%v", oldHistory, err)
	}
	oldChecks, err := lingdoctemplate.EvaluateScene(oldHistory.Definition, "preflight", lingdoctemplate.EvaluationInput{ProjectID: project.ID, TargetVersion: "copy-v1", Fields: project.Spec})
	if err != nil {
		t.Fatal(err)
	}
	newChecks, err := lingdoctemplate.EvaluateScene(updated.TemplateCopy.Definition, "preflight", lingdoctemplate.EvaluationInput{ProjectID: project.ID, TargetVersion: "copy-v2", Fields: updated.Spec})
	if err != nil {
		t.Fatal(err)
	}
	findBoundIssue := func(results []lingdoctemplate.Evaluation) (lingdoctemplate.Evaluation, bool) {
		for _, result := range results {
			if result.RuleID == "goal-required" {
				return result, true
			}
		}
		return lingdoctemplate.Evaluation{}, false
	}
	oldBound, oldFound := findBoundIssue(oldChecks)
	newBound, newFound := findBoundIssue(newChecks)
	if !oldFound || !newFound || oldBound.RuleSetVersion != "v1" || newBound.RuleSetVersion != "v2" || oldBound.RulesetHash == newBound.RulesetHash {
		t.Fatalf("new evaluation must use v2 while historic copy still replays v1: old=%+v new=%+v", oldChecks, newChecks)
	}
	currentIssue := oldIssue
	currentIssue.RulesetHash, currentIssue.ProjectVersion = updated.TemplateCopy.RulesetHash, updated.ProjectVersion
	newDispositions, err := svc.ValidationIssueDispositions(context.Background(), owner, project.ID, []ValidationIssueBinding{currentIssue})
	if err != nil || len(newDispositions) != 0 {
		t.Fatalf("v1 disposition must not carry over to a v2 issue: dispositions=%+v err=%v", newDispositions, err)
	}
	historicDispositions, err := svc.ValidationIssueDispositions(context.Background(), owner, project.ID, []ValidationIssueBinding{oldIssue})
	if err != nil || historicDispositions[oldIssue.IssueID].Action != "dismiss" {
		t.Fatalf("v1 disposition history was not preserved for exact historical replay: dispositions=%+v err=%v", historicDispositions, err)
	}
	events, err := svc.ListAuditEvents(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatalf("read project audit history: %v", err)
	}
	var applyAudit *AuditEvent
	for i := range events {
		if events[i].Action == "project.change_set.apply" && events[i].Target == change.ID {
			applyAudit = &events[i]
		}
	}
	if applyAudit == nil {
		t.Fatalf("successful migration did not persist project.change_set.apply audit: %+v", events)
	}
	if applyAudit.ActorID != owner.UserID || applyAudit.CreatedAt.IsZero() {
		t.Fatalf("migration audit lost actor or timestamp: %+v", applyAudit)
	}
	if applyAudit.Reason != change.Reason {
		t.Fatalf("migration audit lost reason: got %q want %q", applyAudit.Reason, change.Reason)
	}
	upgradeAudit, ok := applyAudit.Details["template_upgrade"].(map[string]any)
	if !ok {
		t.Fatalf("migration audit missing template_upgrade details: %+v", applyAudit.Details)
	}
	assertAuditString := func(key, want string) {
		t.Helper()
		if got, _ := upgradeAudit[key].(string); got != want {
			t.Fatalf("migration audit %s=%q, want %q; details=%+v", key, got, want, upgradeAudit)
		}
	}
	assertAuditNumber := func(key string, want float64) {
		t.Helper()
		if got, _ := upgradeAudit[key].(float64); got != want {
			t.Fatalf("migration audit %s=%v, want %v; details=%+v", key, got, want, upgradeAudit)
		}
	}
	assertAuditString("change_set_id", change.ID)
	assertAuditString("reason", "迁移到新的不可变 RuleSet 版本")
	assertAuditString("ruleset_id", v2.ID)
	assertAuditString("ruleset_version", v2.Version)
	assertAuditString("ruleset_source_hash", v2.RulesetHash)
	assertAuditString("source_ruleset_hash", v2.RulesetHash)
	assertAuditString("content_hash", updated.TemplateCopy.ContentHash)
	assertAuditString("effective_ruleset_hash", updated.TemplateCopy.RulesetHash)
	assertAuditNumber("base_copy_version", float64(oldCopy.Version))
	assertAuditNumber("target_copy_version", float64(updated.TemplateCopyVersion))
	assertAuditNumber("override_count", 1)
	overrides, ok := upgradeAudit["rule_binding_overrides"].([]any)
	if !ok || len(overrides) != 1 {
		t.Fatalf("migration audit overrides=%+v, want one override", upgradeAudit["rule_binding_overrides"])
	}
	override, ok := overrides[0].(map[string]any)
	if !ok || override["rule_id"] != "goal-required" || override["enabled"] != true {
		t.Fatalf("migration audit did not retain exact override: %+v", overrides[0])
	}
	var auditsAfterReplay int64
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project.change_set.apply").Count(&auditsAfterReplay).Error; err != nil {
		t.Fatal(err)
	}
	if _, status, replayed, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, applyKey); err != nil || status != 200 || !replayed {
		t.Fatalf("same-key apply retry: status=%d replayed=%v err=%v", status, replayed, err)
	}
	var auditsAfterIdempotentRetry int64
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project.change_set.apply").Count(&auditsAfterIdempotentRetry).Error; err != nil {
		t.Fatal(err)
	}
	if auditsAfterIdempotentRetry != auditsAfterReplay {
		t.Fatalf("same-key apply retry duplicated audit: before=%d after=%d", auditsAfterReplay, auditsAfterIdempotentRetry)
	}
}

func TestTemplateUpgradeApplyRechecksRuleSetLifecycleBeforeWrites(t *testing.T) {
	for _, state := range []lingdoctemplate.RuleSetState{lingdoctemplate.RuleSetWithdrawn, lingdoctemplate.RuleSetRecalled} {
		t.Run(string(state), func(t *testing.T) {
			svc := testStore(t, filepath.Join(t.TempDir(), "change-ruleset-"+string(state)+".db"))
			owner := Actor{TenantID: 319, UserID: "owner", Role: types.TenantRoleOwner}
			project := seedActiveImpactProject(t, svc, owner)
			set, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
				ID: "grant-rules", Version: "v2", State: lingdoctemplate.RuleSetDraft,
				Bindings: []lingdoctemplate.RuleBinding{{
					RuleID: "goal-required", DefinitionID: "presence", DefinitionVersion: "1",
					Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: "blocking",
					TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "research-office",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			reader := &ruleSetSnapshotReaderStub{set: set}
			svc.WithRuleSetSnapshotReader(reader)
			fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
			fields[0].Label += "（规则迁移）"
			upgrade := &TemplateUpgradeInput{
				ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
				Fields:     fields, Sections: append([]lingdoctemplate.Section{}, project.TemplateCopy.Definition.Sections...),
				Terms:          append([]lingdoctemplate.Term{}, project.TemplateCopy.Definition.Terms...),
				RequiredFields: append([]string{}, project.TemplateCopy.Definition.RequiredFields...),
				Rules:          append([]lingdoctemplate.Rule{}, project.TemplateCopy.Definition.Rules...),
			}
			raw, status, replayed, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "ruleset-lifecycle-assess-"+string(state), CreateChangeSetInput{
				ExpectedContextRevision: project.CurrentContextRevision, Reason: "迁移到指定的 RuleSet 版本", TemplateUpgrade: upgrade,
			})
			if err != nil || status != 201 || replayed {
				t.Fatalf("assess RuleSet change: status=%d replayed=%v err=%v", status, replayed, err)
			}
			var change ChangeSet
			if err := json.Unmarshal(raw, &change); err != nil || change.TemplateUpgrade == nil || change.Status != "assessed" {
				t.Fatalf("assessed RuleSet change = %+v decode=%v", change, err)
			}
			if reader.calls != 1 {
				t.Fatalf("assessment transactional RuleSet reads = %d, want 1", reader.calls)
			}

			// Model another administrator withdrawing or recalling the exact
			// target after assessment but before the Apply transaction begins.
			if state == lingdoctemplate.RuleSetWithdrawn {
				reader.set, err = lingdoctemplate.WithdrawRuleSet(reader.set)
			} else {
				reader.set, err = lingdoctemplate.RecallRuleSet(reader.set)
			}
			if err != nil {
				t.Fatalf("transition target to %s: %v", state, err)
			}
			applyKey := "ruleset-lifecycle-apply-" + string(state)
			var applyAuditBefore int64
			if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action IN ?", project.ID, []string{"project.change_set.apply", "project.change_set.stale"}).Count(&applyAuditBefore).Error; err != nil {
				t.Fatal(err)
			}
			var copyRowsBefore, operationsBefore int64
			if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copyRowsBefore).Error; err != nil {
				t.Fatal(err)
			}
			if err := svc.testDB().Model(&operationRow{}).Where("key = ?", applyKey).Count(&operationsBefore).Error; err != nil {
				t.Fatal(err)
			}
			_, _, _, err = svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, applyKey)
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("apply after target became %s: error=%v, want ErrInvalidState", state, err)
			}
			current, err := svc.GetProject(context.Background(), owner, project.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.ProjectVersion != project.ProjectVersion || current.CurrentContextRevision != project.CurrentContextRevision ||
				current.TemplateCopyVersion != project.TemplateCopyVersion || current.TemplateCopy.ContentHash != project.TemplateCopy.ContentHash ||
				current.TemplateCopy.RulesetHash != project.TemplateCopy.RulesetHash {
				t.Fatalf("failed RuleSet migration changed the active project/copy: before=%+v after=%+v", project, current)
			}
			stored, err := svc.GetChangeSet(context.Background(), owner, project.ID, change.ID)
			if err != nil || stored.Status != "assessed" {
				t.Fatalf("failed apply changed ChangeSet state: change=%+v err=%v", stored, err)
			}
			var copyRowsAfter, operationsAfter, applyAuditAfter int64
			if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copyRowsAfter).Error; err != nil {
				t.Fatal(err)
			}
			if err := svc.testDB().Model(&operationRow{}).Where("key = ?", applyKey).Count(&operationsAfter).Error; err != nil {
				t.Fatal(err)
			}
			if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action IN ?", project.ID, []string{"project.change_set.apply", "project.change_set.stale"}).Count(&applyAuditAfter).Error; err != nil {
				t.Fatal(err)
			}
			if copyRowsAfter != copyRowsBefore || operationsAfter != operationsBefore || applyAuditAfter != applyAuditBefore {
				t.Fatalf("failed apply partially wrote state: copies %d->%d operations %d->%d relevant audits %d->%d", copyRowsBefore, copyRowsAfter, operationsBefore, operationsAfter, applyAuditBefore, applyAuditAfter)
			}
		})
	}
}

func TestBindRuleSetRejectsEnabledLegacyRuleCollisionWithoutPartialWrites(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "ruleset-enabled-collision.db"))
	owner := Actor{TenantID: 320, UserID: "owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, owner)
	raw, _, _, err := svc.CreateProject(context.Background(), owner, "ruleset-collision-create", CreateProjectInput{Name: "规则冲突", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	set, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
		ID: "collision-rules", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{
			{RuleID: "additional-check", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_subject", Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "research-office"},
			{RuleID: lingdoctemplate.RuleRequiredFields, DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: false, Owner: "research-office"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.WithRuleSetSnapshotReader(&ruleSetSnapshotReaderStub{set: set})
	enabled := true
	input := BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
		Overrides:  []lingdoctemplate.RuleBindingOverride{{RuleID: lingdoctemplate.RuleRequiredFields, Enabled: &enabled}},
	}
	var copyRowsBefore, operationsBefore, auditBefore int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copyRowsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", "ruleset-collision-bind").Count(&operationsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.override_ruleset").Count(&auditBefore).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, "ruleset-collision-bind", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("enabling legacy-colliding rule error = %v, want ErrInvalidRequest", err)
	}
	current, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ProjectVersion != project.ProjectVersion || current.TemplateCopyVersion != project.TemplateCopyVersion ||
		current.TemplateCopy.ContentHash != project.TemplateCopy.ContentHash || current.TemplateCopy.RulesetHash != project.TemplateCopy.RulesetHash {
		t.Fatalf("invalid enabled override changed project/copy: before=%+v after=%+v", project, current)
	}
	var copyRowsAfter, operationsAfter, auditAfter int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copyRowsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", "ruleset-collision-bind").Count(&operationsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.override_ruleset").Count(&auditAfter).Error; err != nil {
		t.Fatal(err)
	}
	if copyRowsAfter != copyRowsBefore || operationsAfter != operationsBefore || auditAfter != auditBefore {
		t.Fatalf("invalid enabled override partially wrote state: copies %d->%d operations %d->%d audits %d->%d", copyRowsBefore, copyRowsAfter, operationsBefore, operationsAfter, auditBefore, auditAfter)
	}
}

func TestTemplateUpgradeRejectsUnmappedRequiredField(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-template-upgrade-required.db"))
	owner := Actor{TenantID: 310, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
	newField := fields[0]
	newField.ID, newField.Label, newField.Required = "new_required", "新增必填字段", true
	fields = append(fields, newField)
	required := append(append([]string{}, project.TemplateCopy.Definition.RequiredFields...), "new_required")
	_, _, _, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "change-template-upgrade-missing", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "新增必填字段",
		TemplateUpgrade: &TemplateUpgradeInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
			Fields: fields, Sections: append([]lingdoctemplate.Section{}, project.TemplateCopy.Definition.Sections...),
			Terms: append([]lingdoctemplate.Term{}, project.TemplateCopy.Definition.Terms...), RequiredFields: required,
			Rules: append([]lingdoctemplate.Rule{}, project.TemplateCopy.Definition.Rules...)},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unmapped required field error = %v, want ErrInvalidRequest", err)
	}
}

func TestTemplateUpgradeApplyRejectsStaleOrRevokedAndRollsBackMidWrite(t *testing.T) {
	newAssessedUpgrade := func(t *testing.T, name string) (*Service, Actor, Project, ChangeSet) {
		t.Helper()
		svc := testStore(t, filepath.Join(t.TempDir(), name+".db"))
		owner := Actor{TenantID: 311, UserID: "owner", Role: types.TenantRoleOwner}
		project := seedActiveImpactProject(t, svc, owner)
		fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
		fields[0].Label += "（升级）"
		raw, _, _, err := svc.CreateChangeSet(context.Background(), owner, project.ID, name+"-create", CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision, Reason: "评审后的模板副本更新",
			TemplateUpgrade: &TemplateUpgradeInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				Fields: fields, Sections: append([]lingdoctemplate.Section{}, project.TemplateCopy.Definition.Sections...),
				Terms: append([]lingdoctemplate.Term{}, project.TemplateCopy.Definition.Terms...), RequiredFields: append([]string{}, project.TemplateCopy.Definition.RequiredFields...),
				Rules: append([]lingdoctemplate.Rule{}, project.TemplateCopy.Definition.Rules...)},
		})
		if err != nil {
			t.Fatalf("create %s upgrade: %v", name, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatal(err)
		}
		return svc, owner, project, change
	}

	t.Run("stale baseline", func(t *testing.T) {
		svc, owner, project, change := newAssessedUpgrade(t, "template-upgrade-stale")
		if err := svc.testDB().Exec("UPDATE lingdoc_projects SET current_context_revision = current_context_revision + 1, project_version = project_version + 1 WHERE id = ?", project.ID).Error; err != nil {
			t.Fatal(err)
		}
		raw, status, _, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "template-upgrade-stale-apply")
		var stale ChangeSet
		if decodeErr := json.Unmarshal(raw, &stale); decodeErr != nil || err != nil || status != 409 || stale.Status != "stale" {
			t.Fatalf("stale apply: status=%d change=%+v err=%v decode=%v", status, stale, err, decodeErr)
		}
		if _, err := svc.ApplyChangeSetResult(context.Background(), owner, project.ID, change.ID, "template-upgrade-stale-apply"); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("typed stale replay = %v, want ErrVersionConflict", err)
		}
		current, err := svc.GetProject(context.Background(), owner, project.ID)
		if err != nil || current.TemplateCopyVersion != project.TemplateCopyVersion || current.TemplateCopy.ContentHash != project.TemplateCopy.ContentHash {
			t.Fatalf("stale apply mutated active copy: %+v err=%v", current.TemplateCopy, err)
		}
	})

	t.Run("permission revoked", func(t *testing.T) {
		svc, owner, project, change := newAssessedUpgrade(t, "template-upgrade-revoked")
		if err := svc.testDB().Exec("UPDATE lingdoc_member_permissions SET status = 'suspended' WHERE project_id = ? AND user_id = ?", project.ID, owner.UserID).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "template-upgrade-revoked-apply"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("apply after permission loss = %v, want ErrNotFound", err)
		}
		var copyVersion int64
		if err := svc.testDB().Raw("SELECT template_copy_version FROM lingdoc_projects WHERE id = ?", project.ID).Scan(&copyVersion).Error; err != nil || copyVersion != project.TemplateCopyVersion {
			t.Fatalf("revoked apply changed active copy version to %d (read err=%v)", copyVersion, err)
		}
	})

	t.Run("mid-write failure rolls back", func(t *testing.T) {
		svc, owner, project, change := newAssessedUpgrade(t, "template-upgrade-rollback")
		if err := svc.testDB().Exec(`CREATE TRIGGER fail_template_copy_insert BEFORE INSERT ON lingdoc_project_template_copies BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END`).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "template-upgrade-rollback-apply"); err == nil {
			t.Fatal("injected mid-write failure was not returned")
		}
		current, err := svc.GetProject(context.Background(), owner, project.ID)
		if err != nil || current.TemplateCopyVersion != project.TemplateCopyVersion || current.TemplateCopy.ContentHash != project.TemplateCopy.ContentHash {
			t.Fatalf("failed apply partially changed active copy: %+v err=%v", current.TemplateCopy, err)
		}
		stored, err := svc.GetChangeSet(context.Background(), owner, project.ID, change.ID)
		if err != nil || stored.Status != "assessed" {
			t.Fatalf("failed apply changed ChangeSet: %+v err=%v", stored, err)
		}
	})
}

func TestChangeSetStaleApplyDoesNotMutateProject(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-stale.db"))
	actor := Actor{TenantID: 302, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	makeInput := func(value string) CreateChangeSetInput {
		return CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
			Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: value}},
			AffectedChapterIDs: []string{chapters[0].ID}, Reason: "目标调整"}
	}
	firstRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-stale-first", makeInput("目标一"))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-stale-second", makeInput("目标二"))
	if err != nil {
		t.Fatal(err)
	}
	var first, second ChangeSet
	_ = json.Unmarshal(firstRaw, &first)
	_ = json.Unmarshal(secondRaw, &second)
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, first.ID, "change-stale-apply-1"); err != nil || status != 200 {
		t.Fatalf("first apply: status=%d err=%v", status, err)
	}
	raw, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, second.ID, "change-stale-apply-2")
	if err != nil || status != 409 {
		t.Fatalf("stale apply: status=%d err=%v", status, err)
	}
	var stale ChangeSet
	if err := json.Unmarshal(raw, &stale); err != nil || stale.Status != "stale" {
		t.Fatalf("stale response: %+v %v", stale, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || project.CurrentContextRevision != 2 || project.Spec["research_goal"] != "目标一" {
		t.Fatalf("stale apply mutated project: %+v %v", project, err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, second.ID, "change-stale-apply-2"); err != nil || status != 409 || !replay {
		t.Fatalf("stale replay: status=%d replay=%v err=%v", status, replay, err)
	}
}

func TestStaleChangeSetCanBeReassessedAgainstCurrentBaseline(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reassess.db"))
	actor := Actor{TenantID: 314, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	create := func(key, field, oldValue, newValue string) ChangeSet {
		t.Helper()
		raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{field: {OldValue: oldValue, NewValue: newValue}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "研究条件调整",
		})
		if err != nil || status != 201 || replay {
			t.Fatalf("create %s: status=%d replay=%v err=%v", field, status, replay, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
		return change
	}
	first := create("change-reassess-first", "research_subject", "旧主题", "新主题")
	stale := create("change-reassess-second", "research_goal", "旧目标", "新目标")
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, first.ID, "change-reassess-first-apply"); err != nil || status != 200 {
		t.Fatalf("apply first change: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, stale.ID, "change-reassess-second-apply"); err != nil || status != 409 {
		t.Fatalf("mark second change stale: status=%d err=%v", status, err)
	}
	old, err := svc.GetChangeSet(context.Background(), actor, project.ID, stale.ID)
	if err != nil || old.Status != "stale" || old.StaleReason == "" {
		t.Fatalf("stale history: %+v err=%v", old, err)
	}

	response, status, replay, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, stale.ID, "change-reassess-copy")
	if err != nil || status != 201 || replay {
		t.Fatalf("reassess: status=%d replay=%v err=%v", status, replay, err)
	}
	var reassessed ChangeSet
	if err := json.Unmarshal(response, &reassessed); err != nil {
		t.Fatalf("decode reassessed change: %v", err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reassessed.ID == stale.ID || reassessed.ReassessedFromID != stale.ID || reassessed.Status != "assessed" || reassessed.BaseContextRevision != current.CurrentContextRevision || reassessed.BaseSpecRevision != current.SpecRevision || len(reassessed.Fields) != 1 || reassessed.Fields[0].OldValue != current.Spec["research_goal"] {
		t.Fatalf("reassessed proposal did not use current baseline: %+v current=%+v", reassessed, current)
	}
	if len(reassessed.Impacts) != 1 || !sameVersion(reassessed.Impacts[0].ChapterVersionID, chapters[0].CurrentVersionID) {
		t.Fatalf("reassessed impact did not refresh chapter version: %+v", reassessed.Impacts)
	}
	replayBody, replayStatus, replayed, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, stale.ID, "change-reassess-copy")
	if err != nil || replayStatus != 201 || !replayed || string(replayBody) != string(response) {
		t.Fatalf("reassess replay: status=%d replay=%v identical=%v err=%v", replayStatus, replayed, string(replayBody) == string(response), err)
	}
	old, err = svc.GetChangeSet(context.Background(), actor, project.ID, stale.ID)
	if err != nil || old.Status != "stale" || old.StaleReason == "" {
		t.Fatalf("reassessment rewrote stale history: %+v err=%v", old, err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, reassessed.ID, "change-reassess-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply reassessed proposal: status=%d replay=%v err=%v", status, replay, err)
	}
	current, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || current.CurrentContextRevision != project.CurrentContextRevision+2 || current.Spec["research_goal"] != "新目标" {
		t.Fatalf("current project after reassessed apply: %+v err=%v", current, err)
	}
}

func TestReassessChangeSetRejectsChangedTargetField(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reassess-conflict.db"))
	actor := Actor{TenantID: 315, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	input := func(value string) CreateChangeSetInput {
		return CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
			Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: value}},
			AffectedChapterIDs: []string{chapters[0].ID}, Reason: "目标调整"}
	}
	firstRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reassess-conflict-first", input("目标一"))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reassess-conflict-second", input("目标二"))
	if err != nil {
		t.Fatal(err)
	}
	var first, second ChangeSet
	if err := json.Unmarshal(firstRaw, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondRaw, &second); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, first.ID, "change-reassess-conflict-apply-first"); err != nil || status != 200 {
		t.Fatalf("apply first: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, second.ID, "change-reassess-conflict-apply-second"); err != nil || status != 409 {
		t.Fatalf("mark second stale: status=%d err=%v", status, err)
	}
	if _, _, _, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, second.ID, "change-reassess-conflict-copy"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("reassess conflicting target error = %v, want ErrVersionConflict", err)
	}
}

func TestChangeSetApplyRollsBackProjectAndAuditOnInvalidationFailure(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-rollback.db"))
	actor := Actor{TenantID: 305, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "change-rollback-chapter", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "需要复核的章节",
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("seed chapter version: %v", err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("refresh project after chapter seed: %v", err)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-rollback-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "验证事务回滚",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	// Force the second half of confirmation invalidation to fail after the
	// project and ChangeSet updates have been attempted. The surrounding GORM
	// transaction must roll all of those writes back together.
	if err := svc.testDB().Exec("DROP TABLE lingdoc_chapter_confirmations").Error; err != nil {
		t.Fatalf("drop confirmation table: %v", err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-rollback-apply"); err == nil || status != 0 || replay {
		t.Fatalf("expected rollback failure: status=%d replay=%v err=%v", status, replay, err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("read project after rollback: %v", err)
	}
	if current.CurrentContextRevision != project.CurrentContextRevision || current.SpecRevision != project.SpecRevision || current.ProjectVersion != project.ProjectVersion || current.Spec["research_subject"] != "旧主题" {
		t.Fatalf("project mutated despite rollback: before=%+v after=%+v", project, current)
	}
	stored, err := svc.GetChangeSet(context.Background(), actor, project.ID, change.ID)
	if err != nil {
		t.Fatalf("read changeset after rollback: %v", err)
	}
	if stored.Status != "assessed" || stored.TargetContextRevision != nil || stored.TargetSpecRevision != nil {
		t.Fatalf("changeset mutated despite rollback: %+v", stored)
	}
	var operationCount int64
	if err := svc.testDB().Table("lingdoc_operations").Where("target = ? AND key = ?", project.ID+"/"+change.ID, "change-rollback-apply").Count(&operationCount).Error; err != nil {
		t.Fatalf("read failed operation: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("failed apply was persisted as an idempotent operation: %d", operationCount)
	}
}

func TestChangeSetRejectRecordsOwnerDecisionAndReplays(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reject.db"))
	actor := Actor{TenantID: 308, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reject-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "新目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "暂不采用这次目标调整",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	raw, status, replay, err := svc.RejectChangeSet(context.Background(), actor, project.ID, change.ID, "change-reject-apply")
	if err != nil || status != 200 || replay {
		t.Fatalf("reject changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var rejected ChangeSet
	if err := json.Unmarshal(raw, &rejected); err != nil || rejected.Status != "rejected" {
		t.Fatalf("rejected changeset: %+v %v", rejected, err)
	}
	if _, status, replay, err := svc.RejectChangeSet(context.Background(), actor, project.ID, change.ID, "change-reject-apply"); err != nil || status != 200 || !replay {
		t.Fatalf("reject replay: status=%d replay=%v err=%v", status, replay, err)
	}
	events, err := svc.ListAuditEvents(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("audit events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Action == "project.change_set.reject" && event.ActorID == actor.UserID && event.Target == change.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("reject audit missing: %+v", events)
	}
}

func TestChangeSetConcurrentChapterEditBecomesStale(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-chapter-stale.db"))
	actor := Actor{TenantID: 303, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-chapter-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  "章节先发生编辑",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "change-chapter-edit", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "并发编辑后的章节",
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	result, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-chapter-apply")
	if err != nil || status != 409 || replay {
		t.Fatalf("chapter-stale apply: status=%d replay=%v err=%v", status, replay, err)
	}
	var stale ChangeSet
	if err := json.Unmarshal(result, &stale); err != nil || stale.Status != "stale" {
		t.Fatalf("chapter-stale response: %+v %v", stale, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || project.CurrentContextRevision != 1 || project.Spec["research_subject"] != "旧主题" {
		t.Fatalf("chapter-stale apply mutated project: %+v %v", project, err)
	}
}

func TestChangeSetInvalidatesOnlyCurrentChapterVersion(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-history.db"))
	actor := Actor{TenantID: 304, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	chapter := chapters[0]
	firstRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapter.ID, "change-history-1", SaveChapterInput{
		ExpectedChapterVersionID: nil, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "历史版本", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("first chapter version: %v", err)
	}
	var first Chapter
	if err := json.Unmarshal(firstRaw, &first); err != nil {
		t.Fatalf("decode first chapter: %v", err)
	}
	secondRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapter.ID, "change-history-2", SaveChapterInput{
		ExpectedChapterVersionID: first.CurrentVersionID, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "当前版本", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("second chapter version: %v", err)
	}
	var second Chapter
	if err := json.Unmarshal(secondRaw, &second); err != nil {
		t.Fatalf("decode second chapter: %v", err)
	}
	db := svc.repository.(*GORMRepository).db
	// Equal timestamps are valid on coarse clocks and within rapid writes.
	// Assert history/current identity by the returned version IDs, not row order.
	if err := db.Model(&chapterVersionRow{}).Where("id IN ?", []string{*first.CurrentVersionID, *second.CurrentVersionID}).Updates(map[string]any{
		"confirmation_valid": true, "created_at": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}).Error; err != nil {
		t.Fatalf("seed confirmations: %v", err)
	}
	changeRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-history-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapter.ID}, Reason: "保留历史版本",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-history-apply"); err != nil || status != 200 {
		t.Fatalf("apply changeset: status=%d err=%v", status, err)
	}
	var versions []chapterVersionRow
	if err := db.Where("chapter_id = ?", chapter.ID).Order("created_at").Find(&versions).Error; err != nil {
		t.Fatalf("read versions: %v", err)
	}
	states := make(map[string]bool, len(versions))
	for _, version := range versions {
		states[version.ID] = version.ConfirmationValid
	}
	oldValid, oldFound := states[*first.CurrentVersionID]
	currentValid, currentFound := states[*second.CurrentVersionID]
	if len(versions) != 2 || !oldFound || !currentFound || !oldValid || currentValid {
		t.Fatalf("historical confirmation state = %#v, want old=true current=false by version ID", versions)
	}
}

func TestChangeSetReadReflectsReconfirmedImpact(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reviewed.db"))
	actor := Actor{TenantID: 305, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	initialRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "change-reviewed-initial", SaveChapterInput{
		ExpectedChapterVersionID: nil, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "变更前章节", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("save initial chapter: %v", err)
	}
	if err := json.Unmarshal(initialRaw, &chapters[0]); err != nil {
		t.Fatalf("decode initial chapter: %v", err)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reviewed-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "确认后读取状态",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-reviewed-apply"); err != nil || status != 200 {
		t.Fatalf("apply changeset: status=%d err=%v", status, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "change-reviewed-version", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "复核后的章节", SourceIDs: []string{},
	}); err != nil {
		t.Fatalf("save current chapter: %v", err)
	}
	db := svc.repository.(*GORMRepository).db
	if err := db.Model(&chapterVersionRow{}).Where("project_id = ? AND chapter_id = ?", project.ID, chapters[0].ID).Update("confirmation_valid", true).Error; err != nil {
		t.Fatalf("seed current confirmation: %v", err)
	}
	read, err := svc.GetChangeSet(context.Background(), actor, project.ID, change.ID)
	if err != nil || len(read.Impacts) != 1 || read.Impacts[0].Status != "reviewed" {
		t.Fatalf("change impact status = %+v err=%v, want reviewed", read.Impacts, err)
	}
}

func TestChangeSetRejectsMalformedAndMissingImpacts(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-invalid.db"))
	actor := Actor{TenantID: 306, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("valid chapter fixture: %+v err=%v", chapters, err)
	}
	base := func() CreateChangeSetInput {
		return CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
			AffectedChapterIDs: []string{"missing-chapter"}, Reason: "校验边界"}
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-invalid-field", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"unsupported": {OldValue: "a", NewValue: "b"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "非法字段",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsupported field error = %v, want ErrInvalidRequest", err)
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-invalid-reason", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{"chapter-1"}, Reason: "   ",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank reason error = %v, want ErrInvalidRequest", err)
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-invalid-chapter", base()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing chapter error = %v, want ErrNotFound", err)
	}
}

func TestChangeSetApplyRequiresOwnerAndActiveProject(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-auth.db"))
	owner := Actor{TenantID: 307, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	chapters, err := svc.ListChapters(context.Background(), owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "change-auth-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "新目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "权限边界",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), Actor{TenantID: owner.TenantID, UserID: "other"}, project.ID, change.ID, "change-auth-apply"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner apply error = %v, want ErrNotFound", err)
	}

	draftRaw, _, _, err := svc.CreateProject(context.Background(), owner, "change-draft-create", CreateProjectInput{Name: "draft", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("draft create: %v", err)
	}
	draft := asProject(t, draftRaw)
	if _, _, _, err := svc.CreateChangeSet(context.Background(), owner, draft.ID, "change-draft-change", CreateChangeSetInput{
		ExpectedContextRevision: 1,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{"missing"}, Reason: "非 active",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft changeset error = %v, want ErrInvalidState", err)
	}
}

func TestChangeSetApplyRollsBackWhenAuditWriteFails(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-audit-rollback.db"))
	actor := Actor{TenantID: 318, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-audit-rollback-create", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "验证审计失败回滚",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatalf("decode changeset: %v", err)
	}
	if err := svc.testDB().Exec("DROP TABLE lingdoc_project_audits").Error; err != nil {
		t.Fatalf("drop audit table: %v", err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-audit-rollback-apply"); err == nil || status != 0 || replay {
		t.Fatalf("expected audit failure: status=%d replay=%v err=%v", status, replay, err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || current.CurrentContextRevision != project.CurrentContextRevision || current.Spec["research_subject"] != "旧主题" {
		t.Fatalf("project changed despite audit failure: project=%+v err=%v", current, err)
	}
	stored, err := svc.GetChangeSet(context.Background(), actor, project.ID, change.ID)
	if err != nil || stored.Status != "assessed" || stored.DecisionBy != "" || stored.DecidedAt != nil {
		t.Fatalf("decision changed despite audit failure: changeset=%+v err=%v", stored, err)
	}
}

func TestInverseChangeSetRejectsNonAppliedSourceAndIncorrectDelta(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-inverse-reject.db"))
	actor := Actor{TenantID: 302, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-reject-source", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "新目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "原始提案",
	})
	if err != nil {
		t.Fatal(err)
	}
	var source ChangeSet
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	inverse := CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "新目标", NewValue: "旧目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "逆向提案", ReversesChangeSetID: source.ID,
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-unapplied", inverse); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("inverse of assessed source error = %v, want ErrInvalidState", err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, source.ID, "change-inverse-reject-apply"); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	wrongDelta := inverse
	wrongDelta.ExpectedContextRevision = current.CurrentContextRevision
	wrongDelta.Fields = map[string]ChangeFieldInput{"research_goal": {OldValue: "新目标", NewValue: "另一个目标"}}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-wrong-delta", wrongDelta); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-inverse delta error = %v, want ErrInvalidRequest", err)
	}
	if _, err := svc.GetChangeSet(context.Background(), actor, project.ID, source.ID); err != nil {
		t.Fatalf("rejected inverse altered source history: %v", err)
	}
}

func TestAppliedChangeSetCreatesLinkedInverseOnCurrentRevision(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-inverse.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	input := CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "新目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  "调整研究目标",
	}
	raw, status, replayed, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-source-create", input)
	if err != nil || status != 201 || replayed {
		t.Fatalf("create source ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	var source ChangeSet
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if _, status, replayed, err = svc.ApplyChangeSet(context.Background(), actor, project.ID, source.ID, "change-inverse-source-apply"); err != nil || status != 200 || replayed {
		t.Fatalf("apply source ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	inverseInput := CreateChangeSetInput{
		ExpectedContextRevision: current.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "新目标", NewValue: "旧目标"}},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  "恢复原研究目标",
		ReversesChangeSetID:     source.ID,
	}
	first, status, replayed, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-create", inverseInput)
	if err != nil || status != 201 || replayed {
		t.Fatalf("create inverse ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	var inverse ChangeSet
	if err := json.Unmarshal(first, &inverse); err != nil {
		t.Fatal(err)
	}
	if inverse.Status != "assessed" || inverse.BaseContextRevision != current.CurrentContextRevision || inverse.ReversesChangeSetID != source.ID {
		t.Fatalf("inverse proposal did not preserve current baseline and source link: %+v", inverse)
	}
	persisted, err := svc.GetChangeSet(context.Background(), actor, project.ID, inverse.ID)
	if err != nil || persisted.ReversesChangeSetID != source.ID {
		t.Fatalf("inverse link read-back: change=%+v err=%v", persisted, err)
	}
	replayBody, replayStatus, replayed, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-create", inverseInput)
	if err != nil || replayStatus != 201 || !replayed || string(replayBody) != string(first) {
		t.Fatalf("inverse create replay: status=%d replay=%v same=%v err=%v", replayStatus, replayed, string(replayBody) == string(first), err)
	}
	if _, status, replayed, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-duplicate", inverseInput); !errors.Is(err, ErrInvalidState) || replayed || status != 0 {
		t.Fatalf("second inverse proposal: status=%d replay=%v err=%v, want ErrInvalidState", status, replayed, err)
	}
	other, status, replayed, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-inverse-intervening-create", CreateChangeSetInput{
		ExpectedContextRevision: current.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "并行调整研究对象",
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create intervening ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	var intervening ChangeSet
	if err := json.Unmarshal(other, &intervening); err != nil {
		t.Fatal(err)
	}
	if _, status, replayed, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, intervening.ID, "change-inverse-intervening-apply"); err != nil || status != 200 || replayed {
		t.Fatalf("apply intervening ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, inverse.ID, "change-inverse-stale-apply"); err != nil || status != 409 {
		t.Fatalf("stale inverse apply: status=%d err=%v, want conflict", status, err)
	}
	reassessedRaw, status, replayed, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, inverse.ID, "change-inverse-reassess")
	if err != nil || status != 201 || replayed {
		t.Fatalf("reassess stale inverse: status=%d replay=%v err=%v", status, replayed, err)
	}
	var reassessed ChangeSet
	if err := json.Unmarshal(reassessedRaw, &reassessed); err != nil {
		t.Fatal(err)
	}
	if reassessed.ReversesChangeSetID != source.ID || reassessed.ReassessedFromID != inverse.ID || reassessed.BaseContextRevision != current.CurrentContextRevision+1 {
		t.Fatalf("reassessed inverse lost its source/baseline link: %+v", reassessed)
	}
	if _, status, replayed, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, reassessed.ID, "change-inverse-apply"); err != nil || status != 200 || replayed {
		t.Fatalf("apply inverse ChangeSet: status=%d replay=%v err=%v", status, replayed, err)
	}
	current, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || current.Spec["research_goal"] != "旧目标" || current.CurrentContextRevision != project.CurrentContextRevision+3 {
		t.Fatalf("inverse formal result: project=%+v err=%v", current, err)
	}
	unchangedSource, err := svc.GetChangeSet(context.Background(), actor, project.ID, source.ID)
	if err != nil || unchangedSource.Status != "applied" || unchangedSource.ReversesChangeSetID != "" {
		t.Fatalf("source history was rewritten: source=%+v err=%v", unchangedSource, err)
	}
	events, err := svc.ListAuditEvents(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundInverseApply := false
	for _, event := range events {
		if event.Action == "project.change_set.apply" && event.Target == reassessed.ID && event.Details["reverses_change_set_id"] == source.ID {
			foundInverseApply = true
			break
		}
	}
	if !foundInverseApply {
		t.Fatalf("inverse application audit does not identify source ChangeSet: %+v", events)
	}
}

func TestReassessChangeSetReturnsSatisfiedWhenAllTargetsAreCurrent(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reassess-satisfied.db"))
	actor := Actor{TenantID: 318, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	create := func(key string) ChangeSet {
		t.Helper()
		raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "新目标"}},
			AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "目标已更新",
		})
		if err != nil || status != 201 || replay {
			t.Fatalf("create %s: status=%d replay=%v err=%v", key, status, replay, err)
		}
		var change ChangeSet
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatalf("decode %s: %v", key, err)
		}
		return change
	}
	first := create("change-satisfied-first")
	stale := create("change-satisfied-stale")
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, first.ID, "change-satisfied-first-apply"); err != nil || status != 200 {
		t.Fatalf("apply first: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, stale.ID, "change-satisfied-stale-apply"); err != nil || status != 409 {
		t.Fatalf("mark stale proposal: status=%d err=%v", status, err)
	}
	before, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	raw, status, replay, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, stale.ID, "change-satisfied-reassess")
	if err != nil || status != 201 || replay {
		t.Fatalf("reassess satisfied proposal: status=%d replay=%v err=%v", status, replay, err)
	}
	var satisfied ChangeSet
	if err := json.Unmarshal(raw, &satisfied); err != nil {
		t.Fatal(err)
	}
	if satisfied.Status != "satisfied" || satisfied.ReassessedFromID != stale.ID || len(satisfied.Fields) != 0 || len(satisfied.Impacts) != 0 ||
		satisfied.TargetContextRevision == nil || *satisfied.TargetContextRevision != before.CurrentContextRevision ||
		satisfied.TargetSpecRevision == nil || *satisfied.TargetSpecRevision != before.SpecRevision {
		t.Fatalf("unexpected satisfied result: %+v", satisfied)
	}
	satisfiedAfter, err := svc.GetChangeSet(context.Background(), actor, project.ID, satisfied.ID)
	if err != nil || satisfiedAfter.Status != "satisfied" || satisfiedAfter.ReassessedFromID != stale.ID ||
		satisfiedAfter.TargetContextRevision == nil || *satisfiedAfter.TargetContextRevision != before.CurrentContextRevision ||
		satisfiedAfter.TargetSpecRevision == nil || *satisfiedAfter.TargetSpecRevision != before.SpecRevision {
		t.Fatalf("satisfied ChangeSet read-back: %+v err=%v", satisfiedAfter, err)
	}
	after, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || after.ProjectVersion != before.ProjectVersion || after.CurrentContextRevision != before.CurrentContextRevision ||
		after.SpecRevision != before.SpecRevision || after.Spec["research_goal"] != "新目标" {
		t.Fatalf("no-op reassessment mutated project: before=%+v after=%+v err=%v", before, after, err)
	}
	staleAfter, err := svc.GetChangeSet(context.Background(), actor, project.ID, stale.ID)
	if err != nil || staleAfter.Status != "stale" || staleAfter.StaleReason == "" {
		t.Fatalf("satisfied reassessment rewrote stale history: %+v err=%v", staleAfter, err)
	}
	if !projectOwner(after, actor.UserID) {
		t.Fatal("satisfied-state error must be exercised by the current Owner")
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, satisfied.ID, "change-satisfied-apply"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("applying satisfied result error = %v, want ErrInvalidState", err)
	}
	events, err := svc.ListAuditEvents(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundAudit := false
	for _, event := range events {
		if event.Target == satisfied.ID && event.Action == "project.change_set.reassess" && event.Details["outcome"] == "already_satisfied" {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatalf("missing satisfied reassessment audit for %s", satisfied.ID)
	}
	replayBody, replayStatus, replayed, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, stale.ID, "change-satisfied-reassess")
	if err != nil || replayStatus != 201 || !replayed || string(replayBody) != string(raw) {
		t.Fatalf("satisfied replay: status=%d replay=%v identical=%v err=%v", replayStatus, replayed, string(replayBody) == string(raw), err)
	}
}

func TestReassessRejectsDeltaIncompatibleWithCurrentTemplate(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-reassess-template-type.db"))
	actor := Actor{TenantID: 316, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}

	// Establish a numeric string under the original string field definition.
	baselineRaw, baselineStatus, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reassess-type-baseline", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: "123"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "设置兼容的数值文本",
	})
	if err != nil || baselineStatus != 201 {
		t.Fatalf("create baseline ChangeSet: status=%d err=%v", baselineStatus, err)
	}
	var baseline ChangeSet
	if err := json.Unmarshal(baselineRaw, &baseline); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, baseline.ID, "change-reassess-type-baseline-apply"); err != nil || status != 200 {
		t.Fatalf("apply baseline ChangeSet: status=%d err=%v", status, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	// This proposal is valid while research_goal is a string. The template is
	// then upgraded to number, making the same proposal incompatible.
	staleRaw, staleStatus, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reassess-type-stale", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: "123", NewValue: "not-a-number"}},
		AffectedChapterIDs:      []string{chapters[0].ID}, Reason: "验证当前模板类型约束",
	})
	if err != nil || staleStatus != 201 {
		t.Fatalf("create stale proposal: status=%d err=%v", staleStatus, err)
	}
	var stale ChangeSet
	if err := json.Unmarshal(staleRaw, &stale); err != nil {
		t.Fatal(err)
	}

	fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
	foundGoal := false
	for i := range fields {
		if fields[i].ID == "research_goal" {
			fields[i].Type = "number"
			foundGoal = true
		}
	}
	if !foundGoal {
		t.Fatal("research_goal field is missing from current TemplateCopy")
	}
	upgradeRaw, upgradeStatus, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-reassess-type-upgrade", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "将研究目标字段类型升级为 number",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
			Fields: fields, Sections: append([]lingdoctemplate.Section{}, project.TemplateCopy.Definition.Sections...),
			Terms:          append([]lingdoctemplate.Term{}, project.TemplateCopy.Definition.Terms...),
			RequiredFields: append([]string{}, project.TemplateCopy.Definition.RequiredFields...),
			Rules:          append([]lingdoctemplate.Rule{}, project.TemplateCopy.Definition.Rules...),
		},
	})
	if err != nil || upgradeStatus != 201 {
		t.Fatalf("create number-type template upgrade: status=%d err=%v", upgradeStatus, err)
	}
	var upgrade ChangeSet
	if err := json.Unmarshal(upgradeRaw, &upgrade); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, upgrade.ID, "change-reassess-type-upgrade-apply"); err != nil || status != 200 {
		t.Fatalf("apply number-type template upgrade: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, stale.ID, "change-reassess-type-mark-stale"); err != nil || status != 409 {
		t.Fatalf("mark proposal stale after template upgrade: status=%d err=%v", status, err)
	}
	stale, err = svc.GetChangeSet(context.Background(), actor, project.ID, stale.ID)
	if err != nil || stale.Status != "stale" || stale.Fields[0].NewValue != "not-a-number" {
		t.Fatalf("stale proposal history: %+v err=%v", stale, err)
	}
	before, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := svc.ReassessChangeSet(context.Background(), actor, project.ID, stale.ID, "change-reassess-type-reject"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reassess incompatible string→number delta error = %v, want ErrInvalidState", err)
	}
	after, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || after.ProjectVersion != before.ProjectVersion || after.CurrentContextRevision != before.CurrentContextRevision ||
		after.SpecRevision != before.SpecRevision || after.TemplateCopyVersion != before.TemplateCopyVersion ||
		after.Spec["research_goal"] != "123" || templateFieldDefinitions(after.TemplateCopy.Definition)["research_goal"].Type != "number" {
		t.Fatalf("rejected reassessment mutated the formal project: before=%+v after=%+v err=%v", before, after, err)
	}
	staleAfter, err := svc.GetChangeSet(context.Background(), actor, project.ID, stale.ID)
	if err != nil || staleAfter.Status != "stale" || staleAfter.StaleReason != stale.StaleReason || staleAfter.Fields[0].NewValue != "not-a-number" {
		t.Fatalf("rejected reassessment rewrote stale history: %+v err=%v", staleAfter, err)
	}
}
