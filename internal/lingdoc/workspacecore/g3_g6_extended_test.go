package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func extendedG3G6Store(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	svc := testStore(t, filepath.Join(t.TempDir(), "extended.db"))
	for _, sql := range []string{
		"CREATE TABLE IF NOT EXISTS lingdoc_g3_records (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,kind TEXT NOT NULL,record_key TEXT NOT NULL,version BIGINT NOT NULL,body_json TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP NOT NULL,UNIQUE(project_id,kind,record_key))",
		"CREATE TABLE IF NOT EXISTS lingdoc_g6_records (id TEXT PRIMARY KEY,tenant_id BIGINT NOT NULL,project_id TEXT NOT NULL,kind TEXT NOT NULL,record_key TEXT NOT NULL,version BIGINT NOT NULL,body_json TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP NOT NULL,UNIQUE(tenant_id,project_id,kind,record_key))",
	} {
		if err := svc.testDB().Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	actor := Actor{TenantID: 8301, UserID: "owner"}
	p := seedActiveG3Project(t, svc, actor)
	return svc, actor, p
}
func extendedChapter(t *testing.T, svc *Service, actor Actor, p Project) Chapter {
	t.Helper()
	chapters, err := svc.ListChapters(context.Background(), actor, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v", err)
	}
	raw, _, _, err := svc.SaveChapter(context.Background(), actor, p.ID, chapters[0].ID, "extended-chapter", SaveChapterInput{ExpectedChapterVersionID: nil, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "合成测试章节", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var result Chapter
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func extendedChange(t *testing.T, svc *Service, actor Actor, p Project, chapter Chapter) ChangeSet {
	t.Helper()
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, p.ID, "extended-change", CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Fields: map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "新的合成目标"}}, AffectedChapterIDs: []string{chapter.ID}, Reason: "合成研究条件变更"})
	if err != nil {
		t.Fatal(err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error { return AssessChangeImpact(tx, p, change) }); err != nil {
		t.Fatal(err)
	}
	return change
}
func TestG3DependencyResolverSeparatesSuggestionsCyclesAndActions(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	chapter := extendedChapter(t, svc, actor, p)
	source := ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"}
	target := ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: *chapter.CurrentVersionID}
	create := func(key, action, origin string, from, to ObjectVersionRef) {
		t.Helper()
		_, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, p.ID, key, DependencyEdgeInput{ExpectedContextRevision: p.CurrentContextRevision, Source: from, Target: to, Relation: "uses", Action: action, Origin: origin, Reason: "合成依赖", Confidence: .8})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("edge-confirmed-review", "REVIEW_REQUIRED", "user", source, target)
	create("edge-confirmed-regen", "REGENERATE", "user", source, target)
	create("edge-suggested-only", "INVALIDATE_CONFIRMATION", "ai", source, target)
	create("edge-cycle-back", "RECOMPUTE", "user", target, source)
	change := extendedChange(t, svc, actor, p, chapter)
	assessment, err := svc.GetImpactAssessment(context.Background(), actor, p.ID, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPossible, foundRegen := false, false
	for _, impact := range assessment.Impacts {
		if impact.Action == "INVALIDATE_CONFIRMATION" && impact.Certainty == "possible" {
			foundPossible = true
		}
		if impact.Action == "REGENERATE" && impact.Certainty == "definite" {
			foundRegen = true
		}
	}
	if !foundPossible || !foundRegen || len(assessment.Impacts) > 8 {
		t.Fatalf("bad traversal: %+v", assessment.Impacts)
	}
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := p
		next.CurrentContextRevision++
		return CommitChangeImpactTasks(tx, next, change)
	}); err != nil {
		t.Fatal(err)
	}
	tasks, err := svc.ListImpactTasks(context.Background(), actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Action == "INVALIDATE_CONFIRMATION" {
			t.Fatal("model suggestion became a blocking task")
		}
	}
	count := len(tasks)
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := p
		next.CurrentContextRevision++
		return CommitChangeImpactTasks(tx, next, change)
	}); err != nil {
		t.Fatal(err)
	}
	tasks, err = svc.ListImpactTasks(context.Background(), actor, p.ID)
	if err != nil || len(tasks) != count {
		t.Fatalf("duplicate tasks: %d -> %d %v", count, len(tasks), err)
	}
}
func TestG3AssessmentRejectsNewGraphAndTaskPersistenceRollsBack(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	_, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, p.ID, "edge-after-assessment", DependencyEdgeInput{ExpectedContextRevision: p.CurrentContextRevision, Source: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"}, Target: ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: *chapter.CurrentVersionID}, Relation: "uses", Action: "REGENERATE", Origin: "user", Reason: "评估后新增依赖"})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error { return ValidateChangeImpactAssessment(tx, p, change) })
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale assessment accepted: %v", err)
	}
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error { return AssessChangeImpact(tx, p, change) }); err != nil {
		t.Fatal(err)
	}
	beforeFailure, err := svc.GetProject(context.Background(), actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p = beforeFailure
	if err := svc.testDB().Exec("CREATE TRIGGER extended_task_fail BEFORE INSERT ON lingdoc_g3_records WHEN NEW.kind = 'task' BEGIN SELECT RAISE(ABORT, 'task unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	err = svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := p
		next.CurrentContextRevision++
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return err
		}
		return CommitChangeImpactTasks(tx, next, change)
	})
	if err == nil || !strings.Contains(err.Error(), "task unavailable") {
		t.Fatalf("expected injected task persistence failure, got %v", err)
	}
	current, err := svc.GetProject(context.Background(), actor, p.ID)
	if err != nil || current.CurrentContextRevision != p.CurrentContextRevision || current.ProjectVersion != p.ProjectVersion {
		t.Fatalf("partial formal state: %+v %v", current, err)
	}
}

func TestG3StabilizationFailsClosedOnMissingRuleAdapter(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	raw, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "extended-apply")
	if err != nil {
		t.Fatal(err)
	}
	var applied ChangeSet
	if err := json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = svc.StabilizeRevision(context.Background(), actor, p.ID, change.ID, "extended-stabilize")
	if err != nil {
		t.Fatal(err)
	}
	var result StabilizationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "BLOCKED" {
		t.Fatalf("missing rules checker stabilized: %+v", result)
	}
	got, err := svc.GetChangeSet(context.Background(), actor, p.ID, change.ID)
	if err != nil || got.Status != "applied" {
		t.Fatalf("blocked attempt changed lifecycle: %+v %v", got, err)
	}
}
func TestG6RuntimePauseBlocksServerWriteAndPreservesRead(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	input := RuntimeControlInput{Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "pause", Reason: "故障隔离"}
	raw, _, replay, err := svc.ControlRuntime(context.Background(), actor, p.ID, "extended-pause", input)
	if err != nil || replay {
		t.Fatalf("pause: %v %v", replay, err)
	}
	same, _, replay, err := svc.ControlRuntime(context.Background(), actor, p.ID, "extended-pause", input)
	if err != nil || !replay || string(raw) != string(same) {
		t.Fatalf("pause replay: %v %v", replay, err)
	}
	if err := svc.CheckRuntimeOperation(context.Background(), actor, p.ID, "generation", ""); !errors.Is(err, ErrRuntimePaused) {
		t.Fatalf("server gate bypassed: %v", err)
	}
	if _, err := svc.GetProject(context.Background(), actor, p.ID); err != nil {
		t.Fatalf("paused history unreadable: %v", err)
	}
	input.Action = "resume"
	input.ExpectedVersion = 1
	if _, _, _, err := svc.ControlRuntime(context.Background(), actor, p.ID, "extended-resume", input); !errors.Is(err, ErrRuntimeRecoveryRequired) {
		t.Fatalf("resume without rechecks: %v", err)
	}
	input.Action = "pause"
	input.Scope.Value = "other-project"
	if _, _, _, err := svc.ControlRuntime(context.Background(), actor, p.ID, "extended-other", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project pause: %v", err)
	}
}
func TestG6ReportedEventsCannotTriggerAutomaticRuntimePause(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	_, _, _, err := svc.SaveRuntimeAlertRule(context.Background(), actor, p.ID, "extended-alert-rule", RuntimeAlertRule{Signal: "failed", Severity: "P1", Action: "pause", ResponsibleID: actor.UserID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.RecordRuntimeEvent(context.Background(), actor, p.ID, "extended-reported", RuntimeEvent{CorrelationID: "synthetic.report", RuntimeMode: "real", Category: "dependency", Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckRuntimeOperation(context.Background(), actor, p.ID, "generation", ""); err != nil {
		t.Fatalf("untrusted report paused runtime: %v", err)
	}
	trace := RuntimeTrace{CorrelationID: "synthetic.runtime", Category: "dependency"}
	if err := svc.ObserveRuntime(WithRuntimeTrace(context.Background(), trace), actor, p.ID, "model", "failed", 12); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckRuntimeOperation(context.Background(), actor, p.ID, "generation", ""); !errors.Is(err, ErrRuntimePaused) {
		t.Fatalf("server failure did not act: %v", err)
	}
}
func extendedTrialWindow(t *testing.T, svc *Service, actor Actor, p Project) TrialWindow {
	t.Helper()
	input := TrialWindow{StartsAt: time.Now().UTC().Add(-time.Hour), EndsAt: time.Now().UTC().Add(time.Hour), IncludedProjectIDs: []string{p.ID}, IncludedUserIDs: []string{actor.UserID}, DecisionOwners: map[string]string{"product": actor.UserID, "qa": actor.UserID, "safety": actor.UserID}, TargetSamples: 2, MinimumSamples: 2, TemplateVersion: p.TemplateVersion, RulesetHash: p.TemplateCopy.RulesetHash, RuntimeModes: []string{"real"}, HumanSupport: "support.owner", Redaction: "redaction.hash", ExitConditions: []string{"p0.pause"}}
	raw, _, _, err := svc.CreateTrialWindow(context.Background(), actor, p.ID, "extended-window", input)
	if err != nil {
		t.Fatal(err)
	}
	var window TrialWindow
	if err := json.Unmarshal(raw, &window); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = svc.ActOnTrialWindow(context.Background(), actor, p.ID, window.ID, "extended-window-lock", TrialWindowActionInput{ExpectedVersion: window.Version, Action: "lock", Reason: "锁定合成测试窗口"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &window); err != nil {
		t.Fatal(err)
	}
	return window
}
func TestG6TrialNeverPromotesUnreviewedOrNonRealSamples(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	window := extendedTrialWindow(t, svc, actor, p)
	sample := TrialSample{SampleVersion: "sample.v1", FixtureDigest: "fixture.digest", ProjectID: p.ID, TemplateVersion: p.TemplateVersion, RulesetHash: p.TemplateCopy.RulesetHash, ContextRevision: p.CurrentContextRevision, RuntimeMode: "real", PermissionSnapshot: "permission.snapshot", InputHash: "input.hash", OutputHash: "output.hash", ExecutedAt: time.Now().UTC(), CorrelationID: "synthetic.trial", QualityResult: "PASS"}
	if _, _, _, err := svc.RecordTrialSample(context.Background(), actor, p.ID, window.ID, "extended-auto-pass", sample); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("automatic PASS accepted: %v", err)
	}
	sample.QualityResult = ""
	raw, _, _, err := svc.RecordTrialSample(context.Background(), actor, p.ID, window.ID, "extended-sample", sample)
	if err != nil {
		t.Fatal(err)
	}
	var saved TrialSample
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.QualityResult != "NOT RUN" || saved.ReviewerID != "" {
		t.Fatalf("invented review: %+v", saved)
	}
	report, err := svc.GetTrialReport(context.Background(), actor, p.ID, window.ID)
	if err != nil || report.Eligible != 0 || report.Readiness != "NOT READY" {
		t.Fatalf("bad unreviewed report: %+v %v", report, err)
	}
	for _, metric := range report.Metrics {
		if metric.Value != nil {
			t.Fatalf("manufactured rate with no denominator: %+v", metric)
		}
	}
	if _, _, _, err := svc.CreateTrialDecision(context.Background(), actor, p.ID, window.ID, "extended-expand", TrialDecisionInput{Decision: "expand", Reason: "不能扩大", ReportHash: report.InputHash}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expanded empty window: %v", err)
	}
	if _, _, _, err := svc.ReviewTrialSample(context.Background(), actor, p.ID, window.ID, saved.ID, "extended-manual-review", TrialReviewInput{ExpectedVersion: 1, QualityResult: "PASS", HumanDecision: "accept", Reason: "仅合成fixture审核", WordRoundtripEvidence: "word.fixture"}); err != nil {
		t.Fatal(err)
	}
	report, err = svc.GetTrialReport(context.Background(), actor, p.ID, window.ID)
	if err != nil || report.Eligible != 0 {
		t.Fatalf("incomplete real evidence entered denominator: %+v %v", report, err)
	}
}
func TestG6TrialPauseRequiresAllHumanSignaturesAndPreservesHistory(t *testing.T) {
	svc, actor, p := extendedG3G6Store(t)
	window := extendedTrialWindow(t, svc, actor, p)
	report, err := svc.GetTrialReport(context.Background(), actor, p.ID, window.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := svc.CreateTrialDecision(context.Background(), actor, p.ID, window.ID, "extended-pause-proposal", TrialDecisionInput{Decision: "pause", Reason: "合成样本不足", ReportHash: report.InputHash})
	if err != nil {
		t.Fatal(err)
	}
	var decision TrialDecisionRecord
	if err := json.Unmarshal(raw, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Status != "WAITING_HUMAN" || len(decision.Signatures) != 0 {
		t.Fatalf("invented signatures: %+v", decision)
	}
	for _, role := range []string{"product", "qa", "safety"} {
		raw, _, _, err = svc.SignTrialDecision(context.Background(), actor, p.ID, decision.ID, "extended-sign-"+role, TrialDecisionSignInput{ExpectedVersion: decision.Version, Responsibility: role, Reason: "合成fixture签名"})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &decision); err != nil {
			t.Fatal(err)
		}
	}
	if decision.Status != "DECIDED" || decision.DecidedAt == nil {
		t.Fatalf("not decided: %+v", decision)
	}
	windows, err := svc.ListTrialWindows(context.Background(), actor, p.ID)
	if err != nil || len(windows) != 1 || windows[0].Status != "PAUSED" {
		t.Fatalf("history lost: %+v %v", windows, err)
	}
	records, err := svc.ListTrialDecisions(context.Background(), actor, p.ID, window.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("decision history lost: %+v %v", records, err)
	}
}
