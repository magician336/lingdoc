package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

func seedActiveG3Project(t *testing.T, svc *Service, actor Actor) Project {
	t.Helper()
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "g3-create", CreateProjectInput{Name: "G3", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(context.Background(), actor, project.ID, "g3-spec-1", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "旧主题", "research_goal": "旧目标"}})
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.ActivateProject(context.Background(), actor, project.ID, "g3-activate", ActivateProjectInput{ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	return asProject(t, raw)
}

func TestChangeSetApplyAdvancesRevisionAndInvalidatesImpacts(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	input := CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs: []string{chapters[0].ID, chapters[1].ID}, Reason: "研究对象发生变化"}
	raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-change", input)
	if err != nil || status != 201 || replay {
		t.Fatalf("create changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil || change.Status != "assessed" || len(change.Impacts) != 2 {
		t.Fatalf("change preview: %+v %v", change, err)
	}
	raw, status, replay, err = svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-apply")
	if err != nil || status != 200 || replay {
		t.Fatalf("apply changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	firstApply := append([]byte(nil), raw...)
	replayedApply, replayStatus, replayed, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-apply")
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
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-template-upgrade.db"))
	owner := Actor{TenantID: 309, UserID: "owner"}
	project := seedActiveG3Project(t, svc, owner)
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
	if _, _, _, err := svc.SaveTemplateCopy(context.Background(), owner, project.ID, "g3-active-copy-write", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: upgrade.Fields, Sections: upgrade.Sections, Terms: upgrade.Terms,
		RequiredFields: upgrade.RequiredFields, Rules: upgrade.Rules,
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("direct active copy edit error = %v, want ErrInvalidState", err)
	}
	createdRaw, status, replay, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "g3-template-upgrade-create", CreateChangeSetInput{
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
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "g3-template-upgrade-apply"); err != nil || status != 200 || replay {
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

func TestTemplateUpgradeRejectsUnmappedRequiredField(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-template-upgrade-required.db"))
	owner := Actor{TenantID: 310, UserID: "owner"}
	project := seedActiveG3Project(t, svc, owner)
	fields := append([]lingdoctemplate.Field{}, project.TemplateCopy.Definition.Fields...)
	newField := fields[0]
	newField.ID, newField.Label, newField.Required = "new_required", "新增必填字段", true
	fields = append(fields, newField)
	required := append(append([]string{}, project.TemplateCopy.Definition.RequiredFields...), "new_required")
	_, _, _, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "g3-template-upgrade-missing", CreateChangeSetInput{
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
		project := seedActiveG3Project(t, svc, owner)
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
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-stale.db"))
	actor := Actor{TenantID: 302, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	makeInput := func(value string) CreateChangeSetInput {
		return CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
			Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: "旧目标", NewValue: value}},
			AffectedChapterIDs: []string{chapters[0].ID}, Reason: "目标调整"}
	}
	firstRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-stale-first", makeInput("目标一"))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-stale-second", makeInput("目标二"))
	if err != nil {
		t.Fatal(err)
	}
	var first, second ChangeSet
	_ = json.Unmarshal(firstRaw, &first)
	_ = json.Unmarshal(secondRaw, &second)
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, first.ID, "g3-stale-apply-1"); err != nil || status != 200 {
		t.Fatalf("first apply: status=%d err=%v", status, err)
	}
	raw, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, second.ID, "g3-stale-apply-2")
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
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, second.ID, "g3-stale-apply-2"); err != nil || status != 409 || !replay {
		t.Fatalf("stale replay: status=%d replay=%v err=%v", status, replay, err)
	}
}

func TestChangeSetApplyRollsBackProjectAndAuditOnInvalidationFailure(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-rollback.db"))
	actor := Actor{TenantID: 305, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "g3-rollback-chapter", SaveChapterInput{
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
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-rollback-create", CreateChangeSetInput{
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
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-rollback-apply"); err == nil || status != 0 || replay {
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
	if err := svc.testDB().Table("lingdoc_operations").Where("target = ? AND key = ?", project.ID+"/"+change.ID, "g3-rollback-apply").Count(&operationCount).Error; err != nil {
		t.Fatalf("read failed operation: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("failed apply was persisted as an idempotent operation: %d", operationCount)
	}
}

func TestChangeSetRejectRecordsOwnerDecisionAndReplays(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-reject.db"))
	actor := Actor{TenantID: 308, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-reject-create", CreateChangeSetInput{
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
	raw, status, replay, err := svc.RejectChangeSet(context.Background(), actor, project.ID, change.ID, "g3-reject-apply")
	if err != nil || status != 200 || replay {
		t.Fatalf("reject changeset: status=%d replay=%v err=%v", status, replay, err)
	}
	var rejected ChangeSet
	if err := json.Unmarshal(raw, &rejected); err != nil || rejected.Status != "rejected" {
		t.Fatalf("rejected changeset: %+v %v", rejected, err)
	}
	if _, status, replay, err := svc.RejectChangeSet(context.Background(), actor, project.ID, change.ID, "g3-reject-apply"); err != nil || status != 200 || !replay {
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
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-chapter-stale.db"))
	actor := Actor{TenantID: 303, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-chapter-change", CreateChangeSetInput{
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
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "g3-chapter-edit", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "并发编辑后的章节",
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	result, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-chapter-apply")
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
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-history.db"))
	actor := Actor{TenantID: 304, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	chapter := chapters[0]
	firstRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapter.ID, "g3-history-1", SaveChapterInput{
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
	secondRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapter.ID, "g3-history-2", SaveChapterInput{
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
	if err := db.Model(&chapterVersionRow{}).Where("id IN ?", []string{*first.CurrentVersionID, *second.CurrentVersionID}).Update("confirmation_valid", true).Error; err != nil {
		t.Fatalf("seed confirmations: %v", err)
	}
	changeRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-history-change", CreateChangeSetInput{
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
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-history-apply"); err != nil || status != 200 {
		t.Fatalf("apply changeset: status=%d err=%v", status, err)
	}
	var versions []chapterVersionRow
	if err := db.Where("chapter_id = ?", chapter.ID).Order("created_at").Find(&versions).Error; err != nil {
		t.Fatalf("read versions: %v", err)
	}
	if len(versions) != 2 || !versions[0].ConfirmationValid || versions[1].ConfirmationValid {
		t.Fatalf("historical confirmation state = %#v, want old=true current=false", versions)
	}
}

func TestChangeSetReadReflectsReconfirmedImpact(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-reviewed.db"))
	actor := Actor{TenantID: 305, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	initialRaw, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "g3-reviewed-initial", SaveChapterInput{
		ExpectedChapterVersionID: nil, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "变更前章节", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("save initial chapter: %v", err)
	}
	if err := json.Unmarshal(initialRaw, &chapters[0]); err != nil {
		t.Fatalf("decode initial chapter: %v", err)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-reviewed-create", CreateChangeSetInput{
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
	if _, status, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "g3-reviewed-apply"); err != nil || status != 200 {
		t.Fatalf("apply changeset: status=%d err=%v", status, err)
	}
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "g3-reviewed-version", SaveChapterInput{
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
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-invalid.db"))
	actor := Actor{TenantID: 306, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	base := func() CreateChangeSetInput {
		return CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
			AffectedChapterIDs: []string{"missing-chapter"}, Reason: "校验边界"}
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-invalid-field", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"unsupported": {OldValue: "a", NewValue: "b"}},
		AffectedChapterIDs:      []string{"missing-chapter"}, Reason: "非法字段",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsupported field error = %v, want ErrInvalidRequest", err)
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-invalid-reason", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{"chapter-1"}, Reason: "   ",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank reason error = %v, want ErrInvalidRequest", err)
	}
	if _, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-invalid-chapter", base()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing chapter error = %v, want ErrNotFound", err)
	}
}

func TestChangeSetApplyRequiresOwnerAndActiveProject(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3-auth.db"))
	owner := Actor{TenantID: 307, UserID: "owner"}
	project := seedActiveG3Project(t, svc, owner)
	chapters, err := svc.ListChapters(context.Background(), owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "g3-auth-create", CreateChangeSetInput{
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
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), Actor{TenantID: owner.TenantID, UserID: "other"}, project.ID, change.ID, "g3-auth-apply"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner apply error = %v, want ErrNotFound", err)
	}

	draftRaw, _, _, err := svc.CreateProject(context.Background(), owner, "g3-draft-create", CreateProjectInput{Name: "draft", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("draft create: %v", err)
	}
	draft := asProject(t, draftRaw)
	if _, _, _, err := svc.CreateChangeSet(context.Background(), owner, draft.ID, "g3-draft-change", CreateChangeSetInput{
		ExpectedContextRevision: 1,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{"missing"}, Reason: "非 active",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft changeset error = %v, want ErrInvalidState", err)
	}
}
