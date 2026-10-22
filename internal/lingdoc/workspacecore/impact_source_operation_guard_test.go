package workspacecore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Guard for the source-authorization projection dispatch table (#211).
//
// Before this file nothing in the suite pinned what OperationSourceIDs resolves
// for an individual operation: the only direct references were the
// source-revocation acceptance cases, which observe a failure, not a set. A
// behaviour-preserving rewrite of the projection would therefore have been
// unfalsifiable. Every case here drives a REAL store-backed transaction -- the
// seam every caller actually uses -- and asserts the LITERAL resolved
// identifier set for exactly one operation.
//
// Every case was written against the pre-refactor implementation and was then
// re-verified with the ticket's suppression experiment: make that resolver
// return an empty reference set and the case must turn red.

const guardProjectionSource = "source-guard"

// projectionFixture seeds an active project whose first chapter carries a real
// citation, then drives one change set to applied so an impact task exists
// against that same chapter. Every source-sensitive operation therefore has a
// real object to resolve, and every resolved set must name guardProjectionSource.
func projectionFixture(t *testing.T, key string) (*Service, Actor, Project, Chapter, ChangeSet, ImpactTask) {
	t.Helper()
	svc, actor, p := extendedImpactStore(t)
	chapters := materializeChapterVersions(t, svc, actor, p, nil)
	chapter := chapters[0]
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, p.ID, chapter.ID, key+"-cite", SaveChapterInput{
		ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision,
		BodyMarkdown: "守护 来源章节 [[source:" + guardProjectionSource + "]]", SourceIDs: []string{guardProjectionSource},
	}); err != nil {
		t.Fatalf("write cited chapter: %v", err)
	}
	p = reloadProject(t, svc, actor, p.ID)
	chapter = mustListChapters(t, svc, actor, p.ID)[0]

	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, p.ID, key+"-create", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "守护 目标"}},
		AffectedChapterIDs:      []string{chapter.ID}, Reason: "守护 投影",
	})
	if err != nil {
		t.Fatalf("create change set: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, key+"-apply"); err != nil {
		t.Fatalf("apply change set: %v", err)
	}
	p = reloadProject(t, svc, actor, p.ID)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	return svc, actor, p, mustListChapters(t, svc, actor, p.ID)[0], change, task
}

// deactivateProject takes the project out of the active state through the raw
// store, so the projection's inactive-project short-circuit is reachable.
// "draft" is the other status the schema admits.
func deactivateProject(t *testing.T, svc *Service, projectID string) {
	t.Helper()
	if err := svc.testDB().Model(&projectRow{}).Where("id = ?", projectID).Update("status", "draft").Error; err != nil {
		t.Fatalf("deactivate project: %v", err)
	}
}

// resolveProjection calls the projection on a real transaction. The projection
// performs no write, so the enclosing transaction is always committed and the
// projection's own error is reported unwrapped.
func resolveProjection(t *testing.T, svc *Service, op, target, projectID string, body any) ([]string, error) {
	t.Helper()
	var ids []string
	var opErr error
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		ids, opErr = OperationSourceIDs(tx, op, target, projectID, body)
		return nil
	}), "open the transaction the projection runs in")
	return ids, opErr
}

// requireGuardSet is the shared assertion: the operation resolved exactly the
// one real citation and nothing else.
func requireGuardSet(t *testing.T, ids []string, err error) {
	t.Helper()
	require.NoError(t, err)
	require.Equal(t, []string{guardProjectionSource}, ids)
}

// The three operations that build their ChangeSet from the request payload.
func TestProjectionResolvesCitedChapterForChangePayloadOperations(t *testing.T) {
	svc, _, p, chapter, _, _ := projectionFixture(t, "payload")
	for _, tc := range []struct {
		op   string
		body any
	}{
		{"createChangeSet", CreateChangeSetInput{
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "守护 目标2"}},
			AffectedChapterIDs: []string{chapter.ID}, Reason: "守护 载荷"}},
		{"createChangeDraft", ExtendedChangeSetInput{
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "守护 目标2"}},
			AffectedChapterIDs: []string{chapter.ID}, Reason: "守护 载荷"}},
		{"editChangeDraft", ExtendedChangeSetInput{
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "守护 目标2"}},
			AffectedChapterIDs: []string{chapter.ID}, Reason: "守护 载荷"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			ids, err := resolveProjection(t, svc, tc.op, p.ID, p.ID, tc.body)
			requireGuardSet(t, ids, err)
		})
	}
}

// The four operations that read an already stored ChangeSet.
func TestProjectionResolvesCitedChapterForStoredChangeOperations(t *testing.T) {
	svc, _, p, _, change, _ := projectionFixture(t, "stored")
	for _, op := range []string{"reassessChangeSet", "applyChangeSet", "reverseChangeSet", "assessChangeSet"} {
		t.Run(op, func(t *testing.T) {
			ids, err := resolveProjection(t, svc, op, p.ID+"/"+change.ID, p.ID, nil)
			requireGuardSet(t, ids, err)
		})
	}
}

func TestProjectionResolvesTaskCitationsForActOnImpactTask(t *testing.T) {
	svc, _, p, _, _, task := projectionFixture(t, "task")
	ids, err := resolveProjection(t, svc, "actOnImpactTask", p.ID+"/"+task.ID, p.ID, ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: task.TargetRevision, Action: "start",
		Reason: "守护 任务", HumanDecision: "reviewed",
	})
	requireGuardSet(t, ids, err)
}

func TestProjectionResolvesStabilizationCitations(t *testing.T) {
	svc, _, p, _, change, _ := projectionFixture(t, "stabilize")
	ids, err := resolveProjection(t, svc, "stabilizeRevision", p.ID+"/"+change.ID, p.ID, nil)
	requireGuardSet(t, ids, err)
}

func TestProjectionResolvesEdgeEndpoints(t *testing.T) {
	svc, actor, p, chapter, _, _ := projectionFixture(t, "edge")
	others := mustListChapters(t, svc, actor, p.ID)
	raw, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, p.ID, "guard-edge", DependencyEdgeInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Source:                  chapterVersionRef(chapter), Target: chapterVersionRef(others[len(others)-1]),
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "user", Reason: "守护 依赖", Confidence: 0.9})
	if err != nil {
		t.Fatalf("create dependency edge: %v", err)
	}
	edge := decodeEdge(t, raw)

	ids, err := resolveProjection(t, svc, "createDependencyEdge", p.ID, p.ID, DependencyEdgeInput{
		Source: edge.Source, Target: edge.Target})
	requireGuardSet(t, ids, err)

	ids, err = resolveProjection(t, svc, "decideDependencyEdge", p.ID+"/"+edge.ID, p.ID, nil)
	requireGuardSet(t, ids, err)
}

func TestProjectionResolvesAcknowledgedPreflightProof(t *testing.T) {
	svc, actor, p, _, _, _ := projectionFixture(t, "preflight")
	receipt := ImpactComputationReceipt{ID: "guard-proof", Version: 1, ProjectID: p.ID, Status: "passed",
		ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision,
		TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash,
		InputHash: "guard-proof-input", ComputationBasisHash: "guard-proof-basis", SourceBasisHash: "guard-proof-sources",
		SourceIDs: []string{guardProjectionSource}, CheckedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(runtimeTransaction).InsertRuntimeRecord(actor.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt)
	}))

	ids, err := resolveProjection(t, svc, "acknowledgeImpactPreflight", p.ID+"/guard-proof", p.ID,
		map[string]string{"proof_id": "guard-proof"})
	requireGuardSet(t, ids, err)
}

// The shared prologue: a non-source-sensitive operation resolves to nothing
// without touching the store, an unknown project fails, and a project that is
// no longer active short-circuits to an empty set before any resolution.
func TestProjectionShortCircuitsNonSensitiveAndInactiveOperations(t *testing.T) {
	svc, _, p, chapter, change, _ := projectionFixture(t, "shortcircuit")

	ids, err := resolveProjection(t, svc, "readChapter", p.ID+"/"+chapter.ID, p.ID, nil)
	require.NoError(t, err)
	require.Empty(t, ids)

	ids, err = resolveProjection(t, svc, "createChangeSet", p.ID, "project-that-does-not-exist",
		CreateChangeSetInput{AffectedChapterIDs: []string{chapter.ID}})
	require.Error(t, err)
	require.Empty(t, ids)

	deactivateProject(t, svc, p.ID)
	ids, err = resolveProjection(t, svc, "applyChangeSet", p.ID+"/"+change.ID, p.ID, nil)
	require.NoError(t, err)
	require.Nil(t, ids)
}

// The shared failure handling: a malformed payload or an unknown target still
// fails with the same typed error it failed with before the rewrite.
func TestProjectionRejectsMalformedPayloads(t *testing.T) {
	svc, _, p, _, _, _ := projectionFixture(t, "malformed")
	for _, tc := range []struct {
		name   string
		op     string
		target string
		body   any
		want   error
	}{
		{"createChangeSet payload", "createChangeSet", p.ID, "not-an-input", ErrInvalidRequest},
		{"createChangeDraft payload", "createChangeDraft", p.ID, 42, ErrInvalidRequest},
		{"editChangeDraft payload", "editChangeDraft", p.ID, nil, ErrInvalidRequest},
		{"createDependencyEdge payload", "createDependencyEdge", p.ID, nil, ErrInvalidRequest},
		{"actOnImpactTask payload", "actOnImpactTask", p.ID, nil, ErrInvalidRequest},
		{"acknowledgeImpactPreflight payload", "acknowledgeImpactPreflight", p.ID, map[string]string{}, ErrInvalidRequest},
		{"unknown change set", "reassessChangeSet", p.ID + "/change-that-does-not-exist", nil, ErrNotFound},
		{"unknown task", "actOnImpactTask", p.ID + "/task-that-does-not-exist",
			ImpactTaskActionInput{Action: "start"}, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveProjection(t, svc, tc.op, tc.target, p.ID, tc.body)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// Both endpoints of a dependency edge must contribute to the authorized set.
//
// The fixture above gives every operation the same single cited chapter, so a
// resolver that silently dropped one of two references still resolved to the
// same one-element set and stayed green. Authorizing only one endpoint would
// let a target's own sources through unchecked, so this case cites the two
// endpoints with *different* sources and requires both in the resolved set.
func TestProjectionAuthorizesBothDependencyEdgeEndpoints(t *testing.T) {
	svc, actor, p, _, _, _ := projectionFixture(t, "both-ends")
	chapters := mustListChapters(t, svc, actor, p.ID)
	require.GreaterOrEqual(t, len(chapters), 2)

	// Re-cite each endpoint against a distinct source.
	for i, id := range []string{"guard-source-a", "guard-source-b"} {
		chapter := chapters[i]
		if _, _, _, err := svc.SaveChapter(context.Background(), actor, p.ID, chapter.ID, "both-ends-cite-"+id,
			SaveChapterInput{
				ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision,
				BodyMarkdown: "两端守护 [[source:" + id + "]]", SourceIDs: []string{id},
			}); err != nil {
			t.Fatalf("cite chapter %s with %s: %v", chapter.ID, id, err)
		}
	}
	chapters = mustListChapters(t, svc, actor, p.ID)
	p = reloadProject(t, svc, actor, p.ID)

	raw, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, p.ID, "guard-both-ends", DependencyEdgeInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Source:                  chapterVersionRef(chapters[0]), Target: chapterVersionRef(chapters[1]),
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "user", Reason: "守护 两端", Confidence: 0.9})
	if err != nil {
		t.Fatalf("create dependency edge: %v", err)
	}
	edge := decodeEdge(t, raw)

	for _, op := range []struct {
		name   string
		target string
		body   any
	}{
		{"createDependencyEdge", p.ID, DependencyEdgeInput{Source: edge.Source, Target: edge.Target}},
		{"decideDependencyEdge", p.ID + "/" + edge.ID, nil},
	} {
		ids, err := resolveProjection(t, svc, op.name, op.target, p.ID, op.body)
		require.NoError(t, err, "%s", op.name)
		require.Equal(t, []string{"guard-source-a", "guard-source-b"}, ids,
			"%s must authorize BOTH edge endpoints, not just the source", op.name)
	}
}
