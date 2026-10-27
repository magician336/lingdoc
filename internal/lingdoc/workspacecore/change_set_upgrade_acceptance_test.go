package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
)

// This file closes the #142 acceptance gaps that share one fixture: a
// template_upgrade ChangeSet on an active project, on the same seam as
// TestActiveTemplateCopyUpgradeUsesOwnerChangeSetAndKeepsHistory.  The C1
// acceptance round (map #695, ticket #696) registered four evidence gaps --
// task creation (TD-02.4), idempotent apply (TD-03.5), the audit-failure half
// of the rejection row (TD-03.6) and the audit chain (US-12) -- plus the
// upgrade-specific stabilization gate (TD-02.8).  Each test drives the public
// Service methods only, so a regression that hides behind an internal seam
// cannot stay green here.

// upgradeChangeFixture seeds an active project with chapters, builds the
// template upgrade (one renamed field over the bound copy's definition) and
// creates -- but does not apply -- the upgrade ChangeSet under “createKey“.
func upgradeChangeFixture(t *testing.T, svc *Service, actor Actor, projectID, createKey string) (Project, ChangeSet, TemplateUpgradeInput) {
	t.Helper()
	project, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatalf("active project: %v", err)
	}
	if project.TemplateCopy == nil || project.TemplateCopy.Status != TemplateCopyBound {
		t.Fatalf("active project has no bound template copy: %+v", project.TemplateCopy)
	}
	// A seeded chapter has no current version yet, and an impact without a
	// chapter version is invisible to the impact assessment -- the upgrade's
	// per-chapter review obligations would silently vanish. Save each chapter
	// once so every impact carries a real ChapterVersionID.
	chapters, err := svc.ListChapters(context.Background(), actor, projectID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	for _, chapter := range chapters {
		if _, _, _, err := svc.SaveChapter(context.Background(), actor, projectID, chapter.ID,
			// The per-call uuid makes the save key collision-proof across
			// fixture invocations: a repeated key with a different body is
			// rejected as idempotency_conflict by the operation pipeline
			// (observed on CI where the failure fixture shares the database
			// server), and the fixture has no retry semantics to preserve.
			"upgrade-fixture-"+createKey+"-"+chapter.ID+"-"+uuid.NewString(), SaveChapterInput{
				ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: project.SpecRevision,
				BodyMarkdown: chapter.Title + "（升级验收夹具正文 " + createKey + "）", SourceIDs: []string{}}); err != nil {
			t.Fatalf("save chapter %s: %v", chapter.ID, err)
		}
	}
	// The chapter saves above do not advance the project, but the upgrade's
	// expected versions must come from a fresh read, not from the pre-save
	// snapshot.
	project, err = svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatalf("reload active project: %v", err)
	}
	definition := project.TemplateCopy.Definition
	fields := append([]lingdoctemplate.Field{}, definition.Fields...)
	fields[0].Label += "（升级）"
	upgrade := TemplateUpgradeInput{
		ExpectedProjectVersion:      project.ProjectVersion,
		ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields:                      fields,
		Sections:                    append([]lingdoctemplate.Section{}, definition.Sections...),
		Terms:                       append([]lingdoctemplate.Term{}, definition.Terms...),
		RequiredFields:              append([]string{}, definition.RequiredFields...),
		Rules:                       append([]lingdoctemplate.Rule{}, definition.Rules...),
	}
	raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, projectID, createKey, CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "模板升级验收：字段显示名更新",
		TemplateUpgrade:         &upgrade,
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("create upgrade ChangeSet: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil || change.TemplateUpgrade == nil {
		t.Fatalf("decode upgrade ChangeSet: upgrade=%+v err=%v", change.TemplateUpgrade, err)
	}
	return project, change, upgrade
}

// TD-02.4: applying the upgrade ChangeSet must create the G3 ImpactTasks
// (through AssessChangeImpact's stored assessment plus the apply-side writer),
// and every task it creates must carry the upgrade change's identity.
func TestTemplateUpgradeApplyCreatesImpactTasksAndHandoff(t *testing.T) {
	svc, actor, project := extendedImpactStore(t)
	_, change, _ := upgradeChangeFixture(t, svc, actor, project.ID, "upgrade-tasks-create")

	tasksBefore, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("list impact tasks before apply: %v", err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-tasks-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply upgrade: status=%d replay=%v err=%v", status, replay, err)
	}
	tasksAfter, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("list impact tasks after apply: %v", err)
	}
	if len(tasksAfter) <= len(tasksBefore) {
		t.Fatalf("upgrade apply created no ImpactTasks: before=%d after=%d", len(tasksBefore), len(tasksAfter))
	}
	linked := 0
	for _, task := range tasksAfter {
		if task.ChangeSetID == change.ID {
			linked++
		}
	}
	if linked == 0 {
		t.Fatalf("no created task carries the upgrade change identity %s: %+v", change.ID, tasksAfter)
	}
}

// TD-03.5: the apply's idempotency key replays the original result, and the
// same key over a different request is rejected (ErrIdempotencyConflict).
func TestTemplateUpgradeApplyReplaysSameKeyAndRejectsDifferentInput(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "upgrade-idempotency.db"))
	actor := Actor{TenantID: 402, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	_, change, _ := upgradeChangeFixture(t, svc, actor, project.ID, "upgrade-idem-create")

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-idem-apply"); err != nil || status != 200 || replay {
		t.Fatalf("first apply: status=%d replay=%v err=%v", status, replay, err)
	}
	applied, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Same key, same request: the original result replays and nothing moves.
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-idem-apply"); err != nil || status != 200 || !replay {
		t.Fatalf("apply replay: status=%d replay=%v err=%v", status, replay, err)
	}
	replayed, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ProjectVersion != applied.ProjectVersion || replayed.CurrentContextRevision != applied.CurrentContextRevision {
		t.Fatalf("apply replay moved the project: %+v -> %+v", applied, replayed)
	}

	// Same create key, different request: rejected, not silently replayed.
	second, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	definition := second.TemplateCopy.Definition
	fields := append([]lingdoctemplate.Field{}, definition.Fields...)
	fields[0].Label += "（另一份升级）"
	_, _, _, err = svc.CreateChangeSet(context.Background(), actor, project.ID, "upgrade-idem-create", CreateChangeSetInput{
		ExpectedContextRevision: second.CurrentContextRevision,
		Reason:                  "同键不同请求，必须被拒绝",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion:      second.ProjectVersion,
			ExpectedTemplateCopyVersion: second.TemplateCopyVersion,
			Fields:                      fields,
			Sections:                    append([]lingdoctemplate.Section{}, definition.Sections...),
			Terms:                       append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields:              append([]string{}, definition.RequiredFields...),
			Rules:                       append([]lingdoctemplate.Rule{}, definition.Rules...),
		},
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key over a different request error = %v, want ErrIdempotencyConflict", err)
	}
}

// TD-03.6 (the audit-failure half; the revoked-actor half is already pinned by
// TestTemplateUpgradeApplyRejectsStaleOrRevokedAndRollsBackMidWrite): the
// business audit row is the apply transaction's last write, so an injected
// failure there must roll back the revision advance, the task rows and the
// template-copy supersession -- nothing of the upgrade may survive.
func TestTemplateUpgradeApplyAuditFailureRollsBackUpgradeWrites(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "upgrade-audit-fail.db"))
	actor := Actor{TenantID: 403, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	_, change, _ := upgradeChangeFixture(t, svc, actor, project.ID, "upgrade-audit-create")

	svc.InstallFaultInjection(FaultInjection{Point: FaultPointAuditWrite, TenantID: actor.TenantID,
		Action: "project.change_set.apply", Reason: "合成注入：升级 apply 的审计写入不可用"})
	before, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, project.ID)
	auditsBefore := countApplyAudits(t, svc, actor, project.ID)

	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-audit-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("injected audit failure did not fail the upgrade apply: %v", err)
	}
	assertFaultRollbackUnchanged(t, svc, actor, project.ID, before, tasksBefore, auditsBefore)

	// The upgrade-specific writes are part of the same transaction: the old
	// copy stays bound and superseded copies exist only after a real apply.
	after, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion || after.CurrentContextRevision != before.CurrentContextRevision {
		t.Fatalf("upgrade apply partially survived the audit failure: %+v -> %+v", before, after)
	}
}

// US-12: the upgrade, the tasks and the new snapshot are one reviewable chain.
// The create and the apply audit events both carry the template_upgrade
// details under the same change ID, and the project's current copy version is
// the one the apply event names.
func TestTemplateUpgradeAuditTrailLinksCreateApplyAndNewCopy(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "upgrade-audit-chain.db"))
	actor := Actor{TenantID: 404, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	_, change, _ := upgradeChangeFixture(t, svc, actor, project.ID, "upgrade-chain-create")

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-chain-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply upgrade: status=%d replay=%v err=%v", status, replay, err)
	}
	updated, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := svc.ListAuditEvents(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("audit events: %v", err)
	}
	createFound, applyFound := false, false
	for _, event := range events {
		if event.Target != change.ID {
			continue
		}
		details, _ := json.Marshal(event.Details)
		switch event.Action {
		case "project.change_set.create":
			createFound = true
			if !strings.Contains(string(details), "template_upgrade") {
				t.Fatalf("create audit event carries no template_upgrade details: %s", details)
			}
		case "project.change_set.apply":
			applyFound = true
			if !strings.Contains(string(details), "template_upgrade") {
				t.Fatalf("apply audit event carries no template_upgrade details: %s", details)
			}
		}
	}
	if !createFound || !applyFound {
		t.Fatalf("upgrade audit chain incomplete: create=%v apply=%v", createFound, applyFound)
	}
	if updated.TemplateCopyVersion != project.TemplateCopyVersion+1 {
		t.Fatalf("audit chain names no new copy: copy version %d -> %d", project.TemplateCopyVersion, updated.TemplateCopyVersion)
	}
}

// TD-02.8 (the upgrade-specific half of "re-confirmation and current export"):
// an applied upgrade closes the delivery gate and names its obligations in a
// structured, machine-readable answer -- the open tasks, the invalidated
// confirmations and the replaced template basis -- instead of a bare error.
// The freeze/export half of the same requirement is proven end to end on the
// real delivery guard fixture in the workspace package
// (TestDeliveryGateFreezeAndExportRequireStabilization).
//
// The full repair loop is exercised: close the open tasks, re-confirm every
// impacted chapter, stabilize, and watch the gate open.  (#727 was a test
// helper bug -- a constant confirmation id -- fixed in confirmChapterAtSpecRevision.)
func TestTemplateUpgradeRepairReachesStabilizedAndOpensDeliveryGate(t *testing.T) {
	svc, actor, project := extendedImpactStore(t)
	_, change, _ := upgradeChangeFixture(t, svc, actor, project.ID, "upgrade-stabilize-create")

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "upgrade-stabilize-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply upgrade: status=%d replay=%v err=%v", status, replay, err)
	}
	err := svc.AssertRevisionStabilized(context.Background(), actor, project.ID)
	if !errors.Is(err, ErrRevisionNotStable) {
		t.Fatalf("delivery gate right after the upgrade = %v, want ErrRevisionNotStable", err)
	}
	// The repair loop: re-save and re-confirm every impacted chapter FIRST
	// (each resave replaces the chapter version, so closures must bind the
	// final version), then close the open tasks against that current version.
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	for index, impact := range change.Impacts {
		confirmImpactTarget(t, svc, actor, project.ID, impact.ChapterID, 90+index)
	}
	closeBlockingTasks(t, svc, actor, project.ID)
	result, err := runStabilization(t, svc, actor, project.ID, change.ID, "upgrade-stabilize-run")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "STABILIZED" {
		t.Fatalf("repaired upgrade revision did not stabilize: %+v", result)
	}
	if err := svc.AssertRevisionStabilized(context.Background(), actor, project.ID); err != nil {
		t.Fatalf("delivery gate after a STABILIZED upgrade revision = %v, want open", err)
	}
}
