package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

type r34PreflightChecker struct{ service *Service }

func (c *r34PreflightChecker) Recheck(ctx context.Context, actor Actor, pid, id string) (ImpactPreflightEvidence, error) {
	r, err := c.service.ReadImpactComputationReceipt(ctx, actor, pid, id)
	if err != nil {
		return ImpactPreflightEvidence{}, err
	}
	return ImpactPreflightEvidence{ProofID: r.ID, ProofKind: "check_receipt", SnapshotDigest: r.InputHash, ProjectVersion: r.ProjectVersion, ContextRevision: r.ContextRevision, TemplateCopyVersion: r.TemplateCopyVersion, RulesetHash: r.RulesetHash, Status: r.Status, IsCurrent: true, SourceIDs: r.SourceIDs}, nil
}

func r34PersistedProof(t *testing.T, s *Service, a Actor, p Project, tag string) (string, string) {
	t.Helper()
	basis, err := s.ReadPreflightBasis(context.Background(), a, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	evaluations, err := lingdoctemplate.Evaluate(p.TemplateCopy.Definition, lingdoctemplate.EvaluationInput{ProjectID: p.ID, TargetVersion: strconv.FormatInt(p.CurrentContextRevision, 10), Fields: p.Spec})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.StoreImpactComputationReceipt(context.Background(), a, basis, "r34-evaluated-"+tag, "passed", evaluations, basis.SourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	return receipt.ID, receipt.InputHash
}

func r34Tasks(t *testing.T, s *Service, a Actor, pid string) map[string]ImpactTask {
	t.Helper()
	tasks, err := s.ListImpactTasks(context.Background(), a, pid)
	if err != nil {
		t.Fatal(err)
	}
	r := map[string]ImpactTask{}
	for _, task := range tasks {
		r[task.ID] = task
	}
	return r
}

func TestR34CurrentPreflightRefreshesDoneReleaseTaskAfterFormalEdit(t *testing.T) {
	s, actor, p := extendedG3G6Store(t)
	ctx := context.Background()
	chapter := extendedChapter(t, s, actor, p)
	p, err := s.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Real ChangeSet, persisted computation receipts and task/operation stores;
	// the test adapter maps the same receipt fields as the production HTTP seam.
	change := extendedChange(t, s, actor, p, chapter)
	if _, _, _, err := s.ApplyChangeSet(ctx, actor, p.ID, change.ID, "r34-apply-change"); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := ImpactTask{ID: "r34-release-task", ProjectID: p.ID, ChangeSetID: change.ID, Target: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "r34-current"}, TargetRevision: p.CurrentContextRevision, Action: "BLOCK_RELEASE", Status: "OPEN", Severity: "blocking", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error { return tx.(impactTransaction).InsertImpactTask(task) }); err != nil {
		t.Fatal(err)
	}
	oldProof, _ := r34PersistedProof(t, s, actor, p, "old")
	checker := &r34PreflightChecker{service: s}
	s.SetImpactPreflightChecker(checker)
	if _, _, _, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, change.ID, oldProof, "r34-first-close"); err != nil {
		t.Fatal(err)
	}
	closed := r34Tasks(t, s, actor, p.ID)[task.ID]
	if closed.Status != "DONE" || len(closed.ClosureEvidence) != 1 || closed.ClosureEvidence[0].ID != oldProof {
		t.Fatalf("initial close: %+v", closed)
	}
	chapters, err := s.ListChapters(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var current Chapter
	for _, c := range chapters {
		if c.ID == chapter.ID {
			current = c
		}
	}
	if current.ID == "" {
		t.Fatal("chapter missing")
	}
	if _, _, _, err := s.SaveChapter(ctx, actor, p.ID, current.ID, "r34-real-formal-edit", SaveChapterInput{ExpectedChapterVersionID: current.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "合成章节的实际后续正式版本", SourceIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProjectVersion <= p.ProjectVersion || after.CurrentContextRevision != p.CurrentContextRevision {
		t.Fatalf("formal edit did not reproduce same-context version drift: before=%+v after=%+v", p, after)
	}
	if _, _, _, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, change.ID, oldProof, "r34-stale-reject"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale proof accepted: %v", err)
	}
	if got := r34Tasks(t, s, actor, p.ID)[task.ID]; !reflect.DeepEqual(got, closed) {
		t.Fatal("stale proof changed completed task")
	}
	newProof, newHash := r34PersistedProof(t, s, actor, after, "new")
	beforeTasks := r34Tasks(t, s, actor, p.ID)
	raw, status, replayed, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, change.ID, newProof, "r34-current-refresh")
	if err != nil || status != 200 || replayed {
		t.Fatalf("refresh: status=%d replayed=%v err=%v", status, replayed, err)
	}
	var response ImpactPreflightAcknowledgement
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	refreshed := r34Tasks(t, s, actor, p.ID)[task.ID]
	if len(refreshed.ClosureEvidence) != 1 || refreshed.ClosureEvidence[0].ID != newProof || refreshed.ClosureEvidence[0].Version != newHash || refreshed.Version != closed.Version+1 || refreshed.Status != "DONE" {
		t.Fatalf("DONE release task kept obsolete proof: before=%+v after=%+v receipt=%+v", closed, refreshed, response)
	}
	found := false
	for _, id := range response.ClosedTaskIDs {
		if id == task.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("refreshed task absent from acknowledgement")
	}
	for id, before := range beforeTasks {
		if id != task.ID && !reflect.DeepEqual(before, r34Tasks(t, s, actor, p.ID)[id]) {
			t.Fatalf("unrelated task changed: %s", id)
		}
	}
	projectNow, err := s.GetProject(ctx, actor, p.ID)
	if err != nil || projectNow.ProjectVersion != after.ProjectVersion {
		t.Fatalf("metadata refresh changed formal project version: %+v %v", projectNow, err)
	}
	replayedBody, _, replay, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, change.ID, newProof, "r34-current-refresh")
	if err != nil || !replay || string(replayedBody) != string(raw) || !reflect.DeepEqual(r34Tasks(t, s, actor, p.ID)[task.ID], refreshed) {
		t.Fatalf("same-key replay changed task: %v", err)
	}
	if _, _, _, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, change.ID, newProof, "r34-same-proof-new-key"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r34Tasks(t, s, actor, p.ID)[task.ID], refreshed) {
		t.Fatal("same current proof unnecessarily advanced task again")
	}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		actor Actor
	}{
		{"outsider", ctx, Actor{TenantID: actor.TenantID, UserID: "r34-outsider"}},
		{"machine", types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "r34-machine"}), actor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := s.AcknowledgeImpactPreflight(tc.ctx, tc.actor, p.ID, change.ID, newProof, "r34-deny-"+tc.name); err == nil {
				t.Fatal("unauthorized refresh accepted")
			}
			if !reflect.DeepEqual(r34Tasks(t, s, actor, p.ID)[task.ID], refreshed) {
				t.Fatal("denied refresh changed task")
			}
		})
	}
}
