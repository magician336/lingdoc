package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// g3SourceGate is a per-source authorization stub. It is mutated mid-test to
// simulate a source that is readable at write time and revoked before the read.
type g3SourceGate struct {
	denied map[string]bool
}

func (g *g3SourceGate) Validate(_ context.Context, _, _ string, sourceIDs []string) error {
	for _, id := range sourceIDs {
		if g.denied[id] {
			return errors.New("source_access_denied")
		}
	}
	return nil
}

func g3ChapterRef(chapter Chapter) ObjectVersionRef {
	version := ""
	if chapter.CurrentVersionID != nil {
		version = *chapter.CurrentVersionID
	}
	return ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: version}
}

func g3SpecRef(project Project) ObjectVersionRef {
	return ObjectVersionRef{Kind: "ProjectSpec", ID: project.ID, Version: strconv.FormatInt(project.SpecRevision, 10)}
}

func g3Ptr[T any](value T) *T { return &value }

// g3MaterializeChapters gives every seeded chapter a real current version,
// because a freshly seeded chapter has no CurrentVersionID and therefore no
// valid ChapterVersion ref to hang a dependency edge on.
func g3MaterializeChapters(t *testing.T, svc *Service, actor Actor, project Project, bodies map[string]string) []Chapter {
	t.Helper()
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	for i := range chapters {
		body := bodies[chapters[i].ID]
		sources := []string{}
		if body != "" {
			sources = []string{"source-secret"}
			body = "受限章节 " + body + " [[source:source-secret]]"
		}
		if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[i].ID, "g3-seed-chapter-"+string(rune('a'+i)), SaveChapterInput{
			ExpectedChapterVersionID: chapters[i].CurrentVersionID,
			ExpectedSpecRevision:     project.SpecRevision,
			BodyMarkdown:             body, SourceIDs: sources}); err != nil {
			t.Fatalf("materialize chapter %s: %v", chapters[i].ID, err)
		}
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("re-list chapters: %v", err)
	}
	for _, chapter := range chapters {
		if chapter.CurrentVersionID == nil {
			t.Fatalf("chapter %s still has no current version", chapter.ID)
		}
	}
	return chapters
}

func mustListChapters(t *testing.T, svc *Service, actor Actor, projectID string) []Chapter {
	t.Helper()
	chapters, err := svc.ListChapters(context.Background(), actor, projectID)
	if err != nil {
		t.Fatalf("list chapters: %v", err)
	}
	return chapters
}

func decodeEdge(t *testing.T, raw json.RawMessage) DependencyEdge {
	t.Helper()
	var edge DependencyEdge
	if err := json.Unmarshal(raw, &edge); err != nil {
		t.Fatalf("decode dependency edge: %v (%s)", err, raw)
	}
	return edge
}

// AC1/AC2: origin decides the starting status, and a later AI run must not
// silently overwrite a decision a human already took.
func TestDependencyEdgeOriginDecidesStatusAndAIRerunKeepsHumanDecision(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	human := DependencyEdgeInput{ExpectedContextRevision: project.CurrentContextRevision,
		Source: g3ChapterRef(chapters[0]), Target: g3ChapterRef(chapters[1]),
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "user", Reason: "人工确认依赖", Confidence: 0.9}
	raw, status, replay, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-human", human)
	if err != nil || status != 201 || replay {
		t.Fatalf("create human edge: status=%d replay=%v err=%v", status, replay, err)
	}
	edge := decodeEdge(t, raw)
	if edge.Status != "confirmed" || edge.Origin != "user" || edge.Version != 1 || edge.Confidence != 0.9 {
		t.Fatalf("human edge lifecycle: %+v", edge)
	}

	// Same (source,target,relation,action): the AI run reuses the human edge as-is.
	ai := human
	ai.Origin = "ai"
	ai.Reason = "AI 重新推测"
	ai.Confidence = 0.1
	raw, _, replay, err = svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai-same", ai)
	if err != nil || replay {
		t.Fatalf("create ai edge: replay=%v err=%v", replay, err)
	}
	again := decodeEdge(t, raw)
	if again.ID != edge.ID || again.Status != "confirmed" || again.Origin != "user" || again.Reason != "人工确认依赖" || again.Version != 1 {
		t.Fatalf("ai rerun overwrote a human-confirmed edge: %+v", again)
	}

	// A machine principal must not be able to drive a human-origin edge either.
	machine := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{KeyID: 7, FullAccess: true})
	if _, _, _, err := svc.CreateDependencyEdge(machine, actor, project.ID, "g3-edge-machine", human); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine principal bypassed the human decision gate: %v", err)
	}
}

// AC2: a candidate edge supports confirm, modify and reject. Modification
// rebinds relation/action/reason/confidence and keeps the edge confirmed.
func TestDependencyDecisionModifyRebindsSuggestedEdgeIntoConfirmed(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	raw, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  g3ChapterRef(chapters[0]), Target: g3ChapterRef(chapters[1]),
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "ai", Reason: "模型推测", Confidence: 0.2})
	if err != nil {
		t.Fatalf("create ai edge: %v", err)
	}
	suggested := decodeEdge(t, raw)
	if suggested.Status != "suggested" {
		t.Fatalf("ai origin status = %q, want suggested", suggested.Status)
	}

	raw, status, replay, err := svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-modify", DependencyDecisionInput{
		ExpectedVersion: suggested.Version, Decision: "modify", Reason: "人工修正为强制重算",
		Relation: g3Ptr("constrains"), Action: g3Ptr("REGENERATE"), Confidence: g3Ptr(0.95)})
	if err != nil || status != 200 || replay {
		t.Fatalf("modify decision: status=%d replay=%v err=%v", status, replay, err)
	}
	modified := decodeEdge(t, raw)
	if modified.Status != "confirmed" || modified.Relation != "constrains" || modified.Action != "REGENERATE" ||
		modified.Confidence != 0.95 || modified.Reason != "人工修正为强制重算" || modified.Version != suggested.Version+1 {
		t.Fatalf("modified edge: %+v", modified)
	}
	if modified.Source != suggested.Source || modified.Target != suggested.Target {
		t.Fatalf("modify rebound the edge endpoints: %+v", modified)
	}

	// The original (unmodified) tuple must not come back as a fresh AI suggestion.
	raw, _, _, err = svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai-rerun", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  suggested.Source, Target: suggested.Target,
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "ai", Reason: "模型再次推测", Confidence: 0.3})
	if !errors.Is(err, ErrDependencyDecided) {
		t.Fatalf("ai rerun after modify: err=%v, want ErrDependencyDecided (raw=%s)", err, raw)
	}
	edges, err := svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil || len(edges) != 1 || edges[0].ID != modified.ID || edges[0].Status != "confirmed" {
		t.Fatalf("edges after modify+rerun: %+v %v", edges, err)
	}
}

// AC2: rejection is final for later AI runs too, and decisions stay bound to
// the object version they were taken against.
func TestDependencyDecisionRejectsAndBindsToEdgeVersion(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	raw, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  g3ChapterRef(chapters[0]), Target: g3ChapterRef(chapters[1]),
		Relation: "supports", Action: "REVALIDATE_EVIDENCE", Origin: "ai", Reason: "模型推测", Confidence: 0.2})
	if err != nil {
		t.Fatalf("create ai edge: %v", err)
	}
	suggested := decodeEdge(t, raw)

	if _, _, _, err := svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-stale", DependencyDecisionInput{
		ExpectedVersion: suggested.Version + 5, Decision: "confirm", Reason: "版本不符"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale decision: err=%v, want ErrVersionConflict", err)
	}
	if _, _, _, err := svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-unknown", DependencyDecisionInput{
		ExpectedVersion: suggested.Version, Decision: "approve", Reason: "未知决定"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown decision: err=%v, want ErrInvalidRequest", err)
	}
	if _, _, _, err := svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-empty-modify", DependencyDecisionInput{
		ExpectedVersion: suggested.Version, Decision: "modify", Reason: "没有任何修改"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("modify without any field: err=%v, want ErrInvalidRequest", err)
	}

	raw, _, _, err = svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-reject", DependencyDecisionInput{
		ExpectedVersion: suggested.Version, Decision: "reject", Reason: "人工否决"})
	if err != nil {
		t.Fatalf("reject decision: %v", err)
	}
	rejected := decodeEdge(t, raw)
	if rejected.Status != "rejected" || rejected.Version != suggested.Version+1 || rejected.Reason != "人工否决" {
		t.Fatalf("rejected edge: %+v", rejected)
	}
	// A decided edge is no longer a decision candidate.
	if _, _, _, err := svc.DecideDependencyEdge(context.Background(), actor, project.ID, suggested.ID, "g3-decide-again", DependencyDecisionInput{
		ExpectedVersion: rejected.Version, Decision: "confirm", Reason: "反悔"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("re-deciding a decided edge: err=%v, want ErrInvalidState", err)
	}
	// Re-proposing the exact rejected tuple must return the rejected edge itself,
	// never a fresh suggested candidate that would re-open the human decision.
	raw, _, _, err = svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai-same", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  suggested.Source, Target: suggested.Target,
		Relation: suggested.Relation, Action: suggested.Action, Origin: "ai", Reason: "换个理由再来", Confidence: 0.4})
	if err != nil {
		t.Fatalf("ai rerun with the rejected tuple: %v", err)
	}
	if stillRejected := decodeEdge(t, raw); stillRejected.ID != rejected.ID || stillRejected.Status != "rejected" {
		t.Fatalf("ai rerun reopened a rejected edge: %+v", stillRejected)
	}
	// A different action for the same pair is a new candidate, not an overwrite,
	// and it must not disturb the rejected edge.
	if _, status, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-edge-ai-other", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  suggested.Source, Target: suggested.Target,
		Relation: suggested.Relation, Action: "REGENERATE", Origin: "ai", Reason: "另一个候选动作", Confidence: 0.4}); err != nil || status != 201 {
		t.Fatalf("ai rerun with a new action: status=%d err=%v", status, err)
	}
	edges, err := svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	statuses := map[string]string{}
	for _, edge := range edges {
		statuses[edge.Action] = edge.Status
	}
	if len(edges) != 2 || statuses["REVALIDATE_EVIDENCE"] != "rejected" || statuses["REGENERATE"] != "suggested" {
		t.Fatalf("edge statuses after ai rerun: %+v", edges)
	}
}

// AC4: confirmed traversal reaches multi-hop targets exactly once, survives
// cycles, keeps several causes, and never touches unrelated objects.
func TestConfirmedDependencyTraversalIsMultiHopCycleSafeAndScoped(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	spec := g3SpecRef(project)
	declared, graph := g3ChapterRef(chapters[0]), g3ChapterRef(chapters[1])
	// Two confirmed edges share source, target and action but differ in relation,
	// so their causes must collapse onto one impact without losing either.
	edges := []DependencyEdgeInput{
		{Source: spec, Target: graph, Relation: "governs", Action: "REVALIDATE_EVIDENCE", Origin: "rule", Reason: "规范驱动"},
		{Source: spec, Target: graph, Relation: "anchors", Action: "REVALIDATE_EVIDENCE", Origin: "user", Reason: "第二条直接因果"},
		{Source: graph, Target: declared, Relation: "supports", Action: "REGENERATE", Origin: "user", Reason: "多跳传递"},
		{Source: declared, Target: graph, Relation: "constrains", Action: "REVIEW_REQUIRED", Origin: "user", Reason: "回边成环"},
	}
	for i, edge := range edges {
		edge.ExpectedContextRevision = project.CurrentContextRevision
		if _, status, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-chain-"+string(rune('a'+i)), edge); err != nil || status != 201 {
			t.Fatalf("create edge %d: status=%d err=%v", i, status, err)
		}
	}

	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-chain-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{declared.ID},
		Reason:                  "研究对象变化触发依赖图"})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	assessment, err := svc.GetImpactAssessment(context.Background(), actor, project.ID, change.ID)
	if err != nil {
		t.Fatalf("read assessment: %v", err)
	}

	causes := map[string][]ImpactCause{}
	for _, impact := range assessment.Impacts {
		causes[refIdentity(impact.Target)+"/"+impact.Action+"/"+impact.Certainty] = impact.Causes
	}
	multiCause := refIdentity(graph) + "/REVALIDATE_EVIDENCE/definite"
	if len(causes[multiCause]) != 2 {
		t.Fatalf("multi-cause retention on %s = %+v, want 2 causes", multiCause, causes[multiCause])
	}
	hop := refIdentity(declared) + "/REGENERATE/definite"
	if len(causes[hop]) != 1 {
		t.Fatalf("multi-hop impact %s = %+v, want 1 cause", hop, causes[hop])
	}
	cycle := refIdentity(graph) + "/REVIEW_REQUIRED/definite"
	if len(causes[cycle]) != 1 {
		t.Fatalf("cycle impact %s = %+v, want 1 cause", cycle, causes[cycle])
	}
	for key := range causes {
		if !strings.HasSuffix(key, "/definite") {
			t.Fatalf("unexpected non-definite impact %s", key)
		}
	}
	required := map[string]bool{}
	for _, object := range assessment.Objects {
		required[refIdentity(object.Ref)] = true
	}
	for _, ref := range []ObjectVersionRef{spec, declared, graph} {
		if !required[refIdentity(ref)] {
			t.Fatalf("reached object %s missing from the required set %v", refIdentity(ref), assessment.Objects)
		}
	}
	if len(assessment.Objects) != 3 {
		t.Fatalf("unrelated objects leaked into the assessment: %+v", assessment.Objects)
	}
}

// AC3/AC4: a suggested edge is reported as possible certainty but never
// becomes a required object, so it cannot block a delivery on its own.
func TestSuggestedDependencyNeverGatesDeliveryOnItsOwn(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g3.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	declared, suggested := g3ChapterRef(chapters[0]), g3ChapterRef(chapters[1])
	if _, status, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-suggested", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  g3SpecRef(project), Target: suggested,
		Relation: "maybe", Action: "BLOCK_RELEASE", Origin: "ai", Reason: "模型不确定", Confidence: 0.3}); err != nil || status != 201 {
		t.Fatalf("create suggested edge: status=%d err=%v", status, err)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-suggested-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{declared.ID},
		Reason:                  "只有一条建议依赖"})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	assessment, err := svc.GetImpactAssessment(context.Background(), actor, project.ID, change.ID)
	if err != nil {
		t.Fatalf("read assessment: %v", err)
	}
	possible := 0
	for _, impact := range assessment.Impacts {
		if impact.Target == suggested && impact.Action == "BLOCK_RELEASE" {
			possible++
			if impact.Certainty != "possible" {
				t.Fatalf("suggested edge certainty = %q, want possible", impact.Certainty)
			}
		}
	}
	if possible != 1 {
		t.Fatalf("suggested edge impact count = %d, want 1", possible)
	}
	for _, object := range assessment.Objects {
		if object.Ref == suggested {
			t.Fatalf("suggested edge made a required object out of %s: %+v", refIdentity(suggested), assessment.Objects)
		}
	}
	// BLOCK_RELEASE on a merely suggested edge must not produce a definite gate.
	for _, task := range assessment.Impacts {
		if task.Certainty == "definite" && task.Target == suggested {
			t.Fatalf("suggested edge produced a definite impact: %+v", task)
		}
	}
}

// AC5: the impact assessment response must not name objects whose sources the
// caller may not read, even when those objects are only reachable through edges.
func TestImpactAssessmentHidesUnauthorizedEdgeEndpoints(t *testing.T) {
	gate := &g3SourceGate{denied: map[string]bool{}}
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "g3.db"), gate)
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	listed := mustListChapters(t, svc, actor, project.ID)
	// Only the first chapter cites a revocable source, and it is deliberately
	// kept out of AffectedChapterIDs: a suggested edge makes it graph-only, so
	// the authorization gap is exactly "exposed via edges/impacts but absent
	// from Objects".
	bodies := map[string]string{listed[0].ID: "受限内容"}
	chapters := g3MaterializeChapters(t, svc, actor, project, bodies)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	secret, declared := g3ChapterRef(chapters[0]), g3ChapterRef(chapters[1])
	if _, status, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "g3-secret-edge", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  g3SpecRef(project), Target: secret,
		Relation: "governs", Action: "BLOCK_RELEASE", Origin: "ai", Reason: "指向受限章节", Confidence: 0.4}); err != nil || status != 201 {
		t.Fatalf("create edge: status=%d err=%v", status, err)
	}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "g3-secret-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{declared.ID},
		Reason:                  "评估里只有图可达的受限章节"})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	// Sanity: the object is readable while the source grant still holds, and it is
	// genuinely outside Objects — that is the shape the fix has to close.
	readable, err := svc.GetImpactAssessment(context.Background(), actor, project.ID, change.ID)
	if err != nil {
		t.Fatalf("readable assessment: %v", err)
	}
	if !slices.ContainsFunc(readable.Edges, func(edge DependencyEdge) bool { return edge.Target == secret }) {
		t.Fatalf("fixture edge missing before revocation: %+v", readable.Edges)
	}
	if slices.ContainsFunc(readable.Objects, func(object ImpactObjectState) bool { return object.Ref == secret }) {
		t.Fatalf("fixture precondition broken: secret object is in Objects: %+v", readable.Objects)
	}

	gate.denied["source-secret"] = true
	assessment, err := svc.GetImpactAssessment(context.Background(), actor, project.ID, change.ID)
	if err == nil {
		t.Fatalf("revoked source still exposed through the impact graph: %+v", assessment)
	}
	for _, edge := range assessment.Edges {
		if edge.Source == secret || edge.Target == secret {
			t.Fatalf("revoked object leaked through edges: %+v", edge)
		}
	}
	for _, impact := range assessment.Impacts {
		if impact.Target == secret {
			t.Fatalf("revoked object leaked through impacts: %+v", impact)
		}
	}
}
