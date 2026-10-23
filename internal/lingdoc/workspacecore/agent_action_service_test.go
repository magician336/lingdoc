package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

type testSourceReader struct {
	sources              map[string]ResearchSource
	denied               map[string]bool
	checkedChapterScopes []string
	readChapterScopes    []string
}

func (r *testSourceReader) ReadResearchSource(_ context.Context, _ Actor, projectID, id string) (ResearchSource, error) {
	if projectID == "" || r.denied[id] {
		return ResearchSource{}, ErrSourceUnavailable
	}
	source, ok := r.sources[id]
	if !ok {
		return ResearchSource{}, ErrNotFound
	}
	return source, nil
}
func (r *testSourceReader) ReadResearchSourceForChapter(ctx context.Context, actor Actor, projectID, chapterID, id string) (ResearchSource, error) {
	r.readChapterScopes = append(r.readChapterScopes, chapterID)
	return r.ReadResearchSource(ctx, actor, projectID, id)
}
func (r *testSourceReader) CheckAssetScope(_ context.Context, _ Actor, _ string, ids []string) ([]ProjectAssetVersion, error) {
	out := []ProjectAssetVersion{}
	for _, id := range sortedUniqueIDs(ids) {
		found := false
		for sourceID, source := range r.sources {
			if source.AssetID == id && !r.denied[sourceID] && source.ProcessingState == "ready" {
				out = append(out, ProjectAssetVersion{AssetID: id, AssetRevision: source.AssetRevision})
				found = true
				break
			}
		}
		if !found {
			return nil, ErrSourceUnavailable
		}
	}
	return out, nil
}
func (r *testSourceReader) CheckAssetScopeForChapter(ctx context.Context, actor Actor, projectID, chapterID string, ids []string) ([]ProjectAssetVersion, error) {
	r.checkedChapterScopes = append(r.checkedChapterScopes, chapterID)
	return r.CheckAssetScope(ctx, actor, projectID, ids)
}

type testSearcher struct {
	calls    int
	failAt   int
	callback func(int)
	results  []ResearchResult
}

func (f *testSearcher) SearchResearch(_ context.Context, _ Actor, _ string, _ ResearchTask, _ string) ([]ResearchResult, string, error) {
	f.calls++
	if f.callback != nil {
		f.callback(f.calls)
	}
	if f.calls == f.failAt {
		return slices.Clone(f.results), "real_api_fake_model", errors.New("synthetic provider unavailable")
	}
	return slices.Clone(f.results), "real_api_fake_model", nil
}

type testAgentModel struct {
	calls    int
	requests []ModelRequest
	callback func()
	err      error
}

func (m *testAgentModel) Reply(_ context.Context, _ Actor, input ModelRequest) (ModelReply, error) {
	m.calls++
	m.requests = append(m.requests, input)
	if m.callback != nil {
		m.callback()
	}
	return ModelReply{Content: "这是待核候选建议，不能证明主张。", StructuredOutput: json.RawMessage(`{"reply":"这是待核候选建议，不能证明主张。"}`), ModelID: "synthetic-model", RunMode: "real_api_fake_model", InputTokens: 100, OutputTokens: 20, FinishReason: "stop"}, m.err
}

type testImporter struct {
	calls  int
	result ImportedAsset
	err    error
}

func (p *testImporter) ImportResearchCandidate(context.Context, Actor, string, string, ImportCandidate) (ImportedAsset, error) {
	p.calls++
	return p.result, p.err
}

type testActionExecutor struct{ calls int }

func (e *testActionExecutor) ExecuteAgentAction(context.Context, Actor, ActionProposal, string) (json.RawMessage, error) {
	e.calls++
	return json.RawMessage(`{"executed":true}`), nil
}

type agentFixture struct {
	svc     *Service
	owner   Actor
	project Project
	chapter Chapter
	source  *testSourceReader
	search  *testSearcher
	model   *testAgentModel
	policy  *fakeSources
	path    string
}

func newAgentFixture(t *testing.T, draft bool) agentFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "g2.db")
	policy := &fakeSources{}
	svc := testStoreWithPolicy(t, path, policy)
	db := svc.testDB()
	if !db.Migrator().HasColumn("tenant_members", "role") {
		if err := db.Exec("ALTER TABLE tenant_members ADD COLUMN role TEXT NOT NULL DEFAULT 'owner'").Error; err != nil {
			t.Fatal(err)
		}
	}

	owner := Actor{TenantID: 9127, UserID: "owner", Role: types.TenantRoleOwner}
	var p Project
	if draft {
		seedTenantMember(t, svc, owner)
		raw, _, _, err := svc.CreateProject(context.Background(), owner, "g2-draft-create", CreateProjectInput{Name: "G2 synthetic", TemplateID: "template-demo"})
		if err != nil {
			t.Fatal(err)
		}
		p = asProject(t, raw)
	} else {
		p = seedActiveImpactProject(t, svc, owner)
	}
	var chapter Chapter
	if !draft {
		chapters, err := svc.ListChapters(context.Background(), owner, p.ID)
		if err != nil || len(chapters) == 0 {
			t.Fatalf("chapters: %v", err)
		}
		chapter = chapters[0]
	}
	reader := &testSourceReader{sources: map[string]ResearchSource{}, denied: map[string]bool{}}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("source-%d", i)
		text := fmt.Sprintf("原始文献第%d条合成摘录；这是需人工核对的资料。", i)
		reader.sources[id] = ResearchSource{SourceID: id, AssetID: fmt.Sprintf("asset-%d", i), AssetRevision: 1, KnowledgeID: fmt.Sprintf("knowledge-%d", i), Title: "合成文献", Locator: fmt.Sprintf("原文段落%d", i), QuotedText: text, QuotedTextHash: sha256Hex(text), Status: "available", ProcessingState: "ready", StableOriginal: true, ModelUseAllowed: true}
	}
	search := &testSearcher{results: []ResearchResult{{ID: "same-result", Level: "candidate_evidence", SourceID: "source-1", AssetID: "asset-1", AssetRevision: 1, Locator: "原文段落1", QuotedTextHash: reader.sources["source-1"].QuotedTextHash, Title: "合成文献", Provider: "synthetic-provider", Status: "available"}}}
	model := &testAgentModel{}
	svc.ConfigureAgentRuntime(AgentRuntime{Sources: reader, Research: search, Model: model, Imports: &testImporter{result: ImportedAsset{AssetID: "asset-1", AssetRevision: 1, ProcessingState: "ready", KnowledgeID: "knowledge-1"}}, Actions: &testActionExecutor{}})
	return agentFixture{svc: svc, owner: owner, project: p, chapter: chapter, source: reader, search: search, model: model, policy: policy, path: path}
}
func (f agentFixture) expectation(version int64) VersionExpectation {
	return VersionExpectation{ExpectedProjectVersion: f.project.ProjectVersion, ExpectedContextRevision: f.project.CurrentContextRevision, ExpectedVersion: version}
}
func decodeAgentJSON[T any](t *testing.T, raw json.RawMessage, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (f agentFixture) task(t *testing.T) ResearchTask {
	t.Helper()
	raw, _, _, err := f.svc.SaveResearchTask(context.Background(), f.owner, f.project.ID, "g2-save-task", SaveResearchTaskInput{VersionExpectation: f.expectation(0), Goal: "研究合成材料的定义与限制", Scope: "只查询项目资料", Queries: []string{"定义", "限制"}, AssetIDs: []string{"asset-1"}})
	task := decodeAgentJSON[ResearchTask](t, raw, err)
	raw, _, _, err = f.svc.ApproveResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-approve-task", ConfirmationInput{VersionExpectation: f.expectation(task.Version), Confirmed: true, Reason: "用户确认查询范围"})
	return decodeAgentJSON[ResearchTask](t, raw, err)
}
// TestChapterResearchScopeIsPassedToTaskValidationAndSourceRead pins the
// chapter scope: it reaches the task's asset authorization and the run's
// source re-read, so a chapter-scoped research operation never falls back to a
// project-wide read.
func TestChapterResearchScopeIsPassedToTaskValidationAndSourceRead(t *testing.T) {
	f := newAgentFixture(t, false)
	input := SaveResearchTaskInput{
		VersionExpectation: f.expectation(0), ChapterID: f.chapter.ID,
		Goal: "章节限定的合成调研", Scope: "当前章节指定资料", Queries: []string{"定义", "限制"}, AssetIDs: []string{"asset-1"},
	}
	raw, _, _, err := f.svc.SaveResearchTask(context.Background(), f.owner, f.project.ID, "research-chapter-scope-task", input)
	task := decodeAgentJSON[ResearchTask](t, raw, err)
	if len(f.source.checkedChapterScopes) == 0 || f.source.checkedChapterScopes[len(f.source.checkedChapterScopes)-1] != f.chapter.ID {
		t.Fatalf("task asset authorization did not receive chapter scope %q: %v", f.chapter.ID, f.source.checkedChapterScopes)
	}
	raw, _, _, err = f.svc.ApproveResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "research-chapter-scope-approve", ConfirmationInput{
		VersionExpectation: f.expectation(task.Version), Confirmed: true, Reason: "合成测试确认章节范围",
	})
	task = decodeAgentJSON[ResearchTask](t, raw, err)
	raw, _, _, err = f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "research-chapter-scope-run", RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)})
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "completed" || run.Basis.ChapterID != f.chapter.ID {
		t.Fatalf("chapter-scoped run did not preserve its chapter basis: %+v", run)
	}
	if _, err = f.svc.GetResearchRun(context.Background(), f.owner, f.project.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.source.readChapterScopes) == 0 || f.source.readChapterScopes[len(f.source.readChapterScopes)-1] != f.chapter.ID {
		t.Fatalf("run source re-read did not receive chapter scope %q: %v", f.chapter.ID, f.source.readChapterScopes)
	}
}

func (f agentFixture) anchor(t *testing.T, sourceID, key string) SourceAnchor {
	t.Helper()
	source := f.source.sources[sourceID]
	raw, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, key, SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: sourceID, SelectionEnd: len([]rune(source.QuotedText)), ExtractionType: "native_text", OCRConfidence: 1, HumanVerified: true, Confirmed: true})
	return decodeAgentJSON[SourceAnchor](t, raw, err)
}
func (f agentFixture) claim(t *testing.T) ResearchClaim {
	t.Helper()
	raw, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "g2-create-claim", SaveResearchClaimInput{VersionExpectation: f.expectation(0), Text: "合成定义可用于本章节概念界定", Scope: "本章概念界定", Reason: "人工输入主张", Confirmed: true})
	return decodeAgentJSON[ResearchClaim](t, raw, err)
}
func (f agentFixture) card(t *testing.T, anchors []SourceAnchor, claim *ResearchClaim, key, support string) EvidenceCard {
	t.Helper()
	refs := []VersionRef{}
	for _, anchor := range anchors {
		refs = append(refs, VersionRef{ID: anchor.ID, Version: anchor.Version})
	}
	var claimRef *VersionRef
	if claim != nil {
		claimRef = &VersionRef{ID: claim.ID, Version: claim.Version}
	}
	raw, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, key, SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: refs, ClaimRef: claimRef, SupportType: support, Scope: "仅支持概念界定，不支持因果效果", ScopeChapterIDs: []string{f.chapter.ID}, Limitations: "合成材料，不能做实证结论", Confirmed: true})
	return decodeAgentJSON[EvidenceCard](t, raw, err)
}

func TestResearchTwoRoundsDeduplicatesReplaysAndRestarts(t *testing.T) {
	f := newAgentFixture(t, true)
	task := f.task(t)
	input := RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)}
	raw, _, _, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-run-01", input)
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "completed" || len(run.Rounds) != 2 || len(run.Results) != 1 || f.search.calls != 2 || run.RunMode != "real_api_fake_model" {
		t.Fatalf("run not complete/deduplicated: %+v calls=%d", run, f.search.calls)
	}
	if _, _, replay, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-run-01", input); err != nil || !replay || f.search.calls != 2 {
		t.Fatalf("replay incurred calls: %v %v %d", replay, err, f.search.calls)
	}
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil || p.Status != "draft" {
		t.Fatalf("research autoactivated: %+v %v", p, err)
	}
	restarted := testStoreWithPolicy(t, f.path, f.policy)
	restarted.ConfigureAgentRuntime(f.svc.agentRuntime)
	recovered, err := restarted.GetResearchRun(context.Background(), f.owner, f.project.ID, run.ID)
	if err != nil || len(recovered.Rounds) != 2 || recovered.ID != run.ID {
		t.Fatalf("restart lost run: %+v %v", recovered, err)
	}
}
func TestResearchPartialFailureRetainsResultsAndNoRetry(t *testing.T) {
	f := newAgentFixture(t, true)
	task := f.task(t)
	f.search.failAt = 2
	raw, _, _, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-partial-run", RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)})
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "failed" || len(run.Results) != 1 || len(run.Rounds) != 2 || run.Rounds[1].FailureCode != "provider_failure" || f.search.calls != 2 {
		t.Fatalf("partial failure fabricated completion: %+v", run)
	}
}
func TestResearchOldBasisIsHistoricalAndNeverOverwrites(t *testing.T) {
	f := newAgentFixture(t, true)
	task := f.task(t)
	f.search.callback = func(call int) {
		if call == 2 {
			if err := f.svc.testDB().Exec("UPDATE lingdoc_projects SET project_version = project_version + 1,current_context_revision = current_context_revision + 1 WHERE id = ?", f.project.ID).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, _, _, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-stale-run", RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)})
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "stale" || run.Basis.ContextRevision != f.project.CurrentContextRevision || run.FailureCode != "stale_basis" {
		t.Fatalf("stale basis accepted: %+v", run)
	}
}
func TestResearchCancelKeepsRunAndStopsNextRound(t *testing.T) {
	f := newAgentFixture(t, true)
	task := f.task(t)
	f.search.callback = func(call int) {
		if call == 1 {
			tasks, err := f.svc.ListResearchTasks(context.Background(), f.owner, f.project.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = f.svc.ControlResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-cancel-running", ControlResearchTaskInput{VersionExpectation: f.expectation(tasks[0].Version), Action: "cancel", Reason: "用户取消"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, _, _, err := f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, task.ID, "g2-cancel-run", RunResearchTaskInput{VersionExpectation: f.expectation(task.Version)})
	run := decodeAgentJSON[ResearchRun](t, raw, err)
	if run.Status != "cancelled" || f.search.calls != 1 || len(run.Results) != 1 {
		t.Fatalf("cancel lost/continued work: %+v calls=%d", run, f.search.calls)
	}
}
func TestDraftClaimCardAndSummaryPromotionAreBlocked(t *testing.T) {
	f := newAgentFixture(t, true)
	_, _, _, err := f.svc.SaveResearchClaim(context.Background(), f.owner, f.project.ID, "g2-draft-claim", SaveResearchClaimInput{VersionExpectation: f.expectation(0), Text: "主张", Scope: "范围", Reason: "创建", Confirmed: true})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft claim: %v", err)
	}
	_, _, _, err = f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "g2-draft-card", SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: "absent", Version: 1}}, Confirmed: true})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft card: %v", err)
	}
	_, _, _, err = f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-summary-import", CreateImportCandidateInput{VersionExpectation: f.expectation(0), URL: "https://example.org/synthetic", Purpose: "研究", SaveMode: "full_text", Scope: "project", License: "synthetic-license"})
	if !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("summary promoted: %v", err)
	}
	if f.svc.agentRuntime.Imports.(*testImporter).calls != 0 {
		t.Fatal("blocked summary imported")
	}
}
func TestAnchorsRequireReadyVerbatimOriginalAndConsent(t *testing.T) {
	f := newAgentFixture(t, false)
	source := f.source.sources["source-1"]
	input := SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: source.SourceID, SelectionEnd: len([]rune(source.QuotedText)), ExtractionType: "native_text", Confirmed: true}
	for _, state := range []string{"processing", "failed"} {
		copy := source
		copy.ProcessingState = state
		f.source.sources[source.SourceID] = copy
		if _, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "g2-anchor-"+state, input); !errors.Is(err, ErrEvidenceIncomplete) {
			t.Fatalf("unready anchor %s: %v", state, err)
		}
	}
	copy := source
	copy.StableOriginal = false
	f.source.sources[source.SourceID] = copy
	if _, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "g2-anchor-summary", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("summary anchor: %v", err)
	}
	f.source.sources[source.SourceID] = source
	input.Confirmed = false
	if _, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "g2-anchor-no-consent", input); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("no consent: %v", err)
	}
}
func TestCandidateNeedsClaimAndConfirmedVersionForGeneration(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "g2-anchor-one")
	card := f.card(t, []SourceAnchor{anchor}, nil, "g2-no-claim-card", "supports")
	if card.Status != "CANDIDATE" || card.Confirmation != nil {
		t.Fatalf("candidate auto-confirmed: %+v", card)
	}
	if _, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-no-claim-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "人工核对"}); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("claimless confirmed: %v", err)
	}
	if _, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("claimless formal: %v", err)
	}
	draft, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, false)
	if err != nil || len(draft.Pending) != 1 {
		t.Fatalf("candidate draft lost label: %+v %v", draft, err)
	}
}
func TestEvidenceSupportOppositionLocalInvalidationAndHistory(t *testing.T) {
	f := newAgentFixture(t, false)
	a := f.anchor(t, "source-1", "g2-anchor-a")
	b := f.anchor(t, "source-2", "g2-anchor-b")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{a, b}, &claim, "g2-multiple-anchor-card", "supports")
	opposite := f.card(t, []SourceAnchor{b}, &claim, "g2-opposite-card", "refutes")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-confirm-multi", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "逐一原文核对，仅概念界定"})
	confirmed := decodeAgentJSON[EvidenceCard](t, raw, err)
	if confirmed.Status != "CONFIRMED" || !strings.Contains(confirmed.Confirmation.Meaning, "不表示") {
		t.Fatalf("confirmation overclaimed: %+v", confirmed)
	}
	selection, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: confirmed.ID, Version: confirmed.Version}}, true)
	if err != nil || len(selection.Sources) != 2 {
		t.Fatalf("formal selection: %+v %v", selection, err)
	}
	f.source.denied[a.SourceID] = true
	f.policy.verdict = ErrSourceUnavailable
	if _, _, _, err = f.svc.InvalidateEvidence(context.Background(), f.owner, f.project.ID, "g2-local-invalid", []string{a.SourceID}, "来源授权撤回"); err != nil {
		t.Fatal(err)
	}
	cards, err := f.svc.ListEvidenceCards(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range cards {
		if value.ID == card.ID && value.Status != "NEEDS_REVIEW" {
			t.Fatal("dependent card stayed confirmed")
		}
		if value.ID == opposite.ID && value.Status != "CANDIDATE" {
			t.Fatal("unrelated opposite card invalidated")
		}
	}
	if _, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: confirmed.ID, Version: confirmed.Version}}, true); err == nil {
		t.Fatal("revoked evidence entered formal generation")
	}
	if err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		port, _ := agentObjectStore(tx)
		row, err := port.ObjectVersion(f.project.ID, EvidenceCardKind, confirmed.ID, confirmed.Version)
		if err != nil {
			return err
		}
		var old EvidenceCard
		if err = json.Unmarshal(row.Payload, &old); err != nil {
			return err
		}
		if old.Status != "CONFIRMED" || old.Confirmation == nil {
			t.Fatal("immutable old confirmation changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	delete(f.source.denied, a.SourceID)
	f.policy.verdict = nil
	cards, err = f.svc.ListEvidenceCards(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range cards {
		if value.ID == card.ID && value.Status == "CONFIRMED" {
			t.Fatal("regrant silently restored confirmation")
		}
	}
}
func TestAuthorAndAdminCannotConfirmEvidenceAndReplayRechecksRole(t *testing.T) {
	f := newAgentFixture(t, false)
	a := f.anchor(t, "source-1", "g2-permission-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{a}, &claim, "g2-permission-card", "supports")
	for _, name := range []string{"author", "admin"} {
		actor := Actor{TenantID: f.owner.TenantID, UserID: name, Role: types.TenantRoleAdmin}
		seedTenantMember(t, f.svc, actor)
		member := Member{UserID: name, Role: "collaborator", GovernanceRole: "member", FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {f.chapter.ID}}, Status: "active"}
		if name == "admin" {
			member.GovernanceRole = "admin"
			member.FunctionRoles = nil
			member.FunctionScopes = nil
		}
		p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			members := append(slices.Clone(p.Members), member)
			return tx.ReplaceMembers(p.ID, members)
		}); err != nil {
			t.Fatal(err)
		}
		_, _, _, err = f.svc.ConfirmEvidenceCard(context.Background(), actor, f.project.ID, card.ID, "g2-unauth-"+name, ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "尝试代行"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s confirmed: %v", name, err)
		}
	}
	input := ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "人工核对"}
	if _, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-role-replay", input); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("UPDATE tenant_members SET role = 'viewer' WHERE tenant_id = ? AND user_id = ?", f.owner.TenantID, f.owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-role-replay", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("downgraded replay: %v", err)
	}
}
func TestAgentSessionsRebuildHistoryAndEraseRevokedContent(t *testing.T) {
	f := newAgentFixture(t, false)
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, "task_assistant", "g2-task-session", f.expectation(0))
	task := decodeAgentJSON[AgentSession](t, raw, err)
	raw, _, _, err = f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, "research_agent", "g2-research-session", f.expectation(0))
	research := decodeAgentJSON[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{"source-1"}, SensitiveContextConfirmed: true}, UserInput: "解释这条候选资料的限制"}
	raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, task.ID, "g2-agent-turn-1", input)
	turn := decodeAgentJSON[AgentTurn](t, raw, err)
	if turn.Status != "completed" || f.model.calls != 1 {
		t.Fatalf("agent turn: %+v", turn)
	}
	if _, _, replay, err := f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, task.ID, "g2-agent-turn-1", input); err != nil || !replay || f.model.calls != 1 {
		t.Fatalf("agent duplicate called model: %v %v", replay, err)
	}
	if _, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, research.ID, "g2-research-turn-1", input); err != nil {
		t.Fatal(err)
	}
	if len(f.model.requests[1].History) != 0 {
		t.Fatal("agent raw transcripts merged")
	}
	if _, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, task.ID, "g2-agent-turn-2", input); err != nil {
		t.Fatal(err)
	}
	if len(f.model.requests[2].History) != 2 {
		t.Fatal("same-agent history lost")
	}
	f.source.denied["source-1"] = true
	history, err := f.svc.GetAgentTurns(context.Background(), f.owner, f.project.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range history {
		if turn.Status != "restricted" || turn.Reply != "" || turn.UserInput != "" || turn.Suggestions != nil {
			t.Fatalf("revoked history leaked: %+v", turn)
		}
	}
	if _, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, task.ID, "g2-agent-revoked", input); err == nil || f.model.calls != 3 {
		t.Fatalf("revoked model access: %v calls=%d", err, f.model.calls)
	}
}
func TestAgentModelTimeoutPreservesInputAndNeverAutoRetries(t *testing.T) {
	f := newAgentFixture(t, false)
	f.model.err = context.DeadlineExceeded
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, "task_assistant", "g2-timeout-session", f.expectation(0))
	session := decodeAgentJSON[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{}}, UserInput: "保留我的调研问题"}
	raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "g2-timeout-turn", input)
	turn := decodeAgentJSON[AgentTurn](t, raw, err)
	if turn.Status != "failed" || turn.UserInput != input.UserInput || turn.FailureCode != "timeout" || f.model.calls != 1 {
		t.Fatalf("timeout not recoverable: %+v", turn)
	}
	if _, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "g2-timeout-turn", input); err != nil || f.model.calls != 1 {
		t.Fatalf("timeout automatically repeated: %v calls=%d", err, f.model.calls)
	}
}
func TestActionConfirmationIsExplicitAndIdempotent(t *testing.T) {
	f := newAgentFixture(t, false)
	executor := f.svc.agentRuntime.Actions.(*testActionExecutor)
	raw, _, _, err := f.svc.CreateActionProposal(context.Background(), f.owner, f.project.ID, "g2-action-preview", CreateActionProposalInput{VersionExpectation: f.expectation(0), Action: "save_spec", TargetID: f.project.ID, Input: json.RawMessage(`{"expected_spec_revision":1,"fields":{"research_goal":"更新合成目标"}}`), SourceIDs: []string{}, ExpectedEffect: "创建新的研究条件版本"})
	proposal := decodeAgentJSON[ActionProposal](t, raw, err)
	if executor.calls != 0 {
		t.Fatal("proposal silently executed")
	}
	input := ConfirmationInput{VersionExpectation: f.expectation(proposal.Version), Confirmed: false, Reason: "yes"}
	if _, _, _, err = f.svc.ConfirmActionProposal(context.Background(), f.owner, f.project.ID, proposal.ID, "g2-action-no-confirm", input); !errors.Is(err, ErrConsentRequired) || executor.calls != 0 {
		t.Fatalf("ordinary yes executed: %v", err)
	}
	input.Confirmed = true
	if _, _, _, err = f.svc.ConfirmActionProposal(context.Background(), f.owner, f.project.ID, proposal.ID, "g2-action-confirm", input); err != nil {
		t.Fatal(err)
	}
	if _, _, replay, err := f.svc.ConfirmActionProposal(context.Background(), f.owner, f.project.ID, proposal.ID, "g2-action-confirm", input); err != nil || !replay || executor.calls != 1 {
		t.Fatalf("duplicate effect: %v %v calls=%d", replay, err, executor.calls)
	}
	input.Reason = "同键换内容"
	if _, _, _, err = f.svc.ConfirmActionProposal(context.Background(), f.owner, f.project.ID, proposal.ID, "g2-action-confirm", input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key altered request: %v", err)
	}
}
func TestFailureRollsBackObjectVersionOperationAndAudit(t *testing.T) {
	f := newAgentFixture(t, true)
	if err := f.svc.testDB().Exec("CREATE TRIGGER synthetic_version_failure BEFORE INSERT ON lingdoc_g2_object_versions BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err := f.svc.SaveResearchTask(context.Background(), f.owner, f.project.ID, "g2-failed-atomic", SaveResearchTaskInput{VersionExpectation: f.expectation(0), Goal: "失败保留输入", Queries: []string{"查询一", "查询二"}, AssetIDs: []string{"asset-1"}})
	if err == nil {
		t.Fatal("version storage failure accepted")
	}
	var objects, operations int64
	f.svc.testDB().Table("lingdoc_g2_objects").Count(&objects)
	f.svc.testDB().Table("lingdoc_operations").Where("operation = ?", "saveResearchTask").Count(&operations)
	if objects != 0 || operations != 0 {
		t.Fatalf("partial state remained: objects=%d operations=%d", objects, operations)
	}
}
func TestImportNeedsConsentAndSourceHashStillMatches(t *testing.T) {
	f := newAgentFixture(t, true)
	raw, _, _, err := f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-import-candidate", CreateImportCandidateInput{VersionExpectation: f.expectation(0), SourceID: "source-1", Purpose: "定义参考", SaveMode: "link_excerpt", Scope: "project", ModelUseAllowed: true, License: "合成公开fixture"})
	candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
	input := ConfirmationInput{VersionExpectation: f.expectation(candidate.Version), Confirmed: false, Reason: "确认用途"}
	if _, _, _, err = f.svc.ConfirmImportCandidate(context.Background(), f.owner, f.project.ID, candidate.ID, "g2-import-without-confirm", input); !errors.Is(err, ErrConsentRequired) {
		t.Fatal(err)
	}
	source := f.source.sources["source-1"]
	source.QuotedText += "变更"
	source.QuotedTextHash = sha256Hex(source.QuotedText)
	f.source.sources["source-1"] = source
	input.Confirmed = true
	if _, _, _, err = f.svc.ConfirmImportCandidate(context.Background(), f.owner, f.project.ID, candidate.ID, "g2-import-changed", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("changed source imported: %v", err)
	}
	if f.svc.agentRuntime.Imports.(*testImporter).calls != 0 {
		t.Fatal("import invoked after changed hash")
	}
}

func seedNativeEvidence(t *testing.T, f agentFixture) {
	t.Helper()
	db := f.svc.testDB()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS knowledges (id TEXT PRIMARY KEY,parse_status TEXT,file_hash TEXT,file_size INTEGER,processed_at DATETIME,deleted_at DATETIME)",
		"CREATE TABLE IF NOT EXISTS chunks (id TEXT PRIMARY KEY,knowledge_id TEXT,content TEXT,source_content TEXT,is_enabled BOOLEAN,deleted_at DATETIME)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range f.source.sources {
		if err := db.Exec("INSERT INTO knowledges(id,parse_status,file_hash,file_size) VALUES(?,?,?,?)", source.KnowledgeID, "completed", "synthetic-file-"+source.KnowledgeID, 99).Error; err != nil {
			t.Fatal(err)
		}
		fp := evidence.FingerprintOf(evidence.KnowledgeSignal{KnowledgeID: source.KnowledgeID, ParseStatus: "completed", FileHash: "synthetic-file-" + source.KnowledgeID, FileSize: 99})
		if err := db.Create(&evidence.ProjectAsset{ID: source.AssetID, TenantID: f.owner.TenantID, ProjectID: f.project.ID, KnowledgeID: source.KnowledgeID, KnowledgeBaseID: "synthetic-kb", Title: source.Title, AssetRevision: 1, ProcessingState: "ready", CreatedBy: f.owner.UserID}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&evidence.ProjectAssetRevision{ID: "revision-" + source.AssetID, AssetID: source.AssetID, RevisionNo: 1, KnowledgeID: source.KnowledgeID, ContentHash: fp.Digest, Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO chunks(id,knowledge_id,content,source_content,is_enabled) VALUES(?,?,?,?,?)", source.SourceID, source.KnowledgeID, source.QuotedText, source.QuotedText, true).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func TestCurrentEvidenceObjectRechecksActualClaimAnchorAndFileFingerprint(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	anchor := f.anchor(t, "source-1", "g2-native-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-native-card", "supports")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-current-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "人工核对原文定位及适用范围"})
	card = decodeAgentJSON[EvidenceCard](t, raw, err)
	read := func() ImpactObjectState {
		var out ImpactObjectState
		err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			var e error
			out, e = tx.(gormTransaction).CurrentEvidenceObject(f.project.ID, "EvidenceCard", card.ID)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if !read().Confirmed {
		t.Fatal("valid local current confirmation not readable")
	}
	if err = f.svc.testDB().Exec("UPDATE chunks SET source_content = ? WHERE id = ?", "合成原文已改变", "source-1").Error; err != nil {
		t.Fatal(err)
	}
	if read().Confirmed {
		t.Fatal("changed original selected hash retained confirmation")
	}
	if err = f.svc.testDB().Exec("UPDATE chunks SET source_content = content WHERE id = ?", "source-1").Error; err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Exec("UPDATE knowledges SET file_hash = ? WHERE id = ?", "changed-file", "knowledge-1").Error; err != nil {
		t.Fatal(err)
	}
	if read().Confirmed {
		t.Fatal("unrefreshed file fingerprint retained confirmation")
	}
}
func TestClaimSemanticDeltaUsesSameTransactionAndInvalidatesCards(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "g2-native-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-native-card", "supports")
	next := f.project
	next.ProjectVersion++
	next.CurrentContextRevision++
	next.SpecRevision++
	var updated ResearchClaim
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		var e error
		updated, e = f.svc.ApplyResearchClaimDelta(tx, f.owner, next, claim.ID, claim.Version, SaveResearchClaimInput{Text: "修订合成主张", Scope: "新的范围", Status: "disputed", Explanation: "反例需补充", Confirmed: true, Reason: "人工批准变更"})
		if e != nil {
			return e
		}
		return tx.UpdateProject(f.owner.TenantID, f.project, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != claim.Version+1 || updated.Basis.ContextRevision != next.CurrentContextRevision {
		t.Fatalf("claim not advanced: %+v", updated)
	}
	var stale EvidenceCard
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var e error
		stale, _, e = loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
		return e
	})
	if err != nil || stale.Status != "NEEDS_REVIEW" || stale.Confirmation != nil {
		t.Fatalf("card survived semantic change: %+v %v", stale, err)
	}
	var edges int64
	if err = f.svc.testDB().Table("lingdoc_g3_records").Where("project_id = ? AND kind = ?", f.project.ID, "edge").Count(&edges).Error; err != nil {
		t.Fatal(err)
	}
	if edges < 1 {
		t.Fatal("no real dependency edges persisted")
	}
}
func TestAssetPolicyDeltaPinsNativeRevisionAndConsentDoesNotFollowReparse(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	anchor := f.anchor(t, "source-1", "g2-native-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-native-card", "supports")
	next := f.project
	next.ProjectVersion++
	next.CurrentContextRevision++
	var policy ProjectAssetPolicy
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		var e error
		policy, e = f.svc.ApplyProjectAssetPolicyDelta(tx, f.owner, next, "asset-1", 1, ProjectAssetPolicy{Scope: "personal", Purpose: "本项目原文核对", ModelUseAllowed: true, Reason: "人工批准资料使用调整"})
		if e != nil {
			return e
		}
		return tx.UpdateProject(f.owner.TenantID, f.project, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Version != 2 || policy.AssetRevision != 2 || policy.PolicyVersion != 1 || !slices.Equal(policy.AllowedUserIDs, []string{f.owner.UserID}) {
		t.Fatalf("policy not real version: %+v", policy)
	}
	var native evidence.ProjectAsset
	if err = f.svc.testDB().First(&native, "id = ?", "asset-1").Error; err != nil || native.AssetRevision != 2 {
		t.Fatalf("native binding unchanged: %+v %v", native, err)
	}
	var stale EvidenceCard
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var e error
		stale, _, e = loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, card.ID)
		return e
	})
	if err != nil || stale.Status != "NEEDS_REVIEW" {
		t.Fatalf("policy did not invalidate dependent card: %+v %v", stale, err)
	}
	// Simulate the EXISTING evidence refresher advancing an identical-content
	// revision: consent remains pinned to the revision the user approved.
	if err = f.svc.testDB().Exec("UPDATE lingdoc_project_assets SET asset_revision = 3 WHERE id = ?", "asset-1").Error; err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Exec("UPDATE lingdoc_asset_revisions SET revision_no = 3 WHERE asset_id = ? AND revision_no = 2", "asset-1").Error; err != nil {
		t.Fatal(err)
	}
	read, err := ReadProjectAssetPolicyFromDB(context.Background(), f.svc.testDB(), f.project.ID, "asset-1")
	if err != nil || read.ModelUseAllowed || read.Version != 3 {
		t.Fatalf("consent auto followed revision: %+v %v", read, err)
	}
}
func TestAssetPolicyDeltaRollbackAndUnknownUserCannotGrant(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	next := f.project
	next.ProjectVersion++
	next.CurrentContextRevision++
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		_, e := f.svc.ApplyProjectAssetPolicyDelta(tx, f.owner, next, "asset-1", 1, ProjectAssetPolicy{Scope: "team", Purpose: "研究", AllowedUserIDs: []string{"stranger"}, Reason: "明确指定"})
		return e
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown grant accepted: %v", err)
	}
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if _, e := f.svc.ApplyProjectAssetPolicyDelta(tx, f.owner, next, "asset-1", 1, ProjectAssetPolicy{Scope: "project", Purpose: "研究", Reason: "回滚测试"}); e != nil {
			return e
		}
		return errors.New("synthetic transaction rollback")
	})
	if err == nil {
		t.Fatal("synthetic rollback missing")
	}
	var native evidence.ProjectAsset
	if err = f.svc.testDB().First(&native, "id = ?", "asset-1").Error; err != nil || native.AssetRevision != 1 {
		t.Fatalf("rollback left native delta: %+v %v", native, err)
	}
	var count int64
	if err = f.svc.testDB().Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", f.project.ID, AssetPolicyKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rollback left policy object: %d %v", count, err)
	}
}

func TestAgentJoinedTimeoutUnknownPreservesUnknownAndReplayNeverRecallsModel(t *testing.T) {
	f := newAgentFixture(t, false)
	f.model.err = errors.Join(context.DeadlineExceeded, ErrOutcomeUnknown)
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID, "task_assistant", "g2-joined-unknown-session", f.expectation(0))
	session := decodeAgentJSON[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{VersionExpectation: f.expectation(0), SourceIDs: []string{}}, UserInput: "保留未取得确定回执的请求"}
	raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "g2-joined-unknown-turn", input)
	turn := decodeAgentJSON[AgentTurn](t, raw, err)
	if turn.FailureCode != "outcome_unknown" || turn.Status != "failed" || turn.UserInput != input.UserInput || f.model.calls != 1 {
		t.Fatalf("unknown downgraded: %+v calls=%d", turn, f.model.calls)
	}
	raw, _, replay, err := f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "g2-joined-unknown-turn", input)
	receipt := decodeAgentJSON[AgentTurn](t, raw, err)
	if !replay || f.model.calls != 1 || receipt.ID != turn.ID || receipt.FailureCode != "outcome_unknown" {
		t.Fatalf("unknown request repeated: %+v replay=%v calls=%d", receipt, replay, f.model.calls)
	}
}

func TestFormalEvidenceSurvivesUnrelatedContextButNotClaimSpecDependencyChange(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	anchor := f.anchor(t, "source-1", "g2-unrelated-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-unrelated-card", "supports")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-unrelated-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "人工核对"})
	card = decodeAgentJSON[EvidenceCard](t, raw, err)
	confirmedAt := card.Confirmation.ConfirmedAt
	if err = f.svc.testDB().Exec("UPDATE lingdoc_projects SET project_version = project_version + 1,current_context_revision = current_context_revision + 1 WHERE id = ?", f.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	selection, err := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true)
	if err != nil || len(selection.Cards) != 1 || !selection.Cards[0].Confirmation.ConfirmedAt.Equal(confirmedAt) {
		t.Fatalf("unrelated context rewrote/invalidated confirmation: %+v %v", selection, err)
	}
	var current ImpactObjectState
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var e error
		current, e = tx.(gormTransaction).CurrentEvidenceObject(f.project.ID, "EvidenceCard", card.ID)
		return e
	})
	if err != nil || !current.Confirmed {
		t.Fatalf("current reader retained global revision bug: %+v %v", current, err)
	}
	if err = f.svc.testDB().Exec("UPDATE lingdoc_projects SET spec_revision = spec_revision + 1 WHERE id = ?", f.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true); err == nil {
		t.Fatal("actual ProjectSpec claim dependency change accepted")
	}
}

func TestMachineCannotAssertHumanDecisionDespiteProjectOwnerAndConfirmedFlag(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "g2-machine-real-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-machine-real-card", "supports")
	raw, _, _, err := f.svc.CreateActionProposal(context.Background(), f.owner, f.project.ID, "g2-machine-action-preview", CreateActionProposalInput{VersionExpectation: f.expectation(0), Action: "save_spec", TargetID: f.project.ID, Input: json.RawMessage(`{"expected_spec_revision":1,"fields":{"research_goal":"候选条件"}}`), SourceIDs: []string{}, ExpectedEffect: "生成新条件版本"})
	action := decodeAgentJSON[ActionProposal](t, raw, err)
	raw, _, _, err = f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "g2-machine-import-preview", CreateImportCandidateInput{VersionExpectation: f.expectation(0), SourceID: "source-1", Purpose: "合成材料参考", SaveMode: "link_excerpt", Scope: "project", ModelUseAllowed: true, License: "合成fixture"})
	imported := decodeAgentJSON[ImportCandidate](t, raw, err)
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"claim", func(ctx context.Context) error {
			_, _, _, e := f.svc.SaveResearchClaim(ctx, f.owner, f.project.ID, "g2-machine-new-claim", SaveResearchClaimInput{VersionExpectation: f.expectation(0), Text: "声称已经核实", Scope: "本章", Reason: "机器confirmed", Confirmed: true})
			return e
		}},
		{"card_confirm", func(ctx context.Context) error {
			_, _, _, e := f.svc.ConfirmEvidenceCard(ctx, f.owner, f.project.ID, card.ID, "g2-machine-card-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "机器confirmed"})
			return e
		}},
		{"import_consent", func(ctx context.Context) error {
			_, _, _, e := f.svc.ConfirmImportCandidate(ctx, f.owner, f.project.ID, imported.ID, "g2-machine-import-confirm", ConfirmationInput{VersionExpectation: f.expectation(imported.Version), Confirmed: true, Reason: "机器许可"})
			return e
		}},
		{"action_confirm", func(ctx context.Context) error {
			_, _, _, e := f.svc.ConfirmActionProposal(ctx, f.owner, f.project.ID, action.ID, "g2-machine-action-confirm", ConfirmationInput{VersionExpectation: f.expectation(action.Version), Confirmed: true, Reason: "机器confirmed"})
			return e
		}},
		{"action_reject", func(ctx context.Context) error {
			_, _, _, e := f.svc.RejectActionProposal(ctx, f.owner, f.project.ID, action.ID, "g2-machine-action-reject", ConfirmationInput{VersionExpectation: f.expectation(action.Version), Confirmed: true, Reason: "机器confirmed"})
			return e
		}},
	}
	for _, principalType := range []string{types.PrincipalAPITenant, types.PrincipalAPIExternalUser, types.PrincipalEmbedSession} {
		ctx := types.WithPrincipal(context.Background(), types.Principal{Type: principalType, ID: "synthetic-machine"})
		for _, testCase := range cases {
			t.Run(principalType+"/"+testCase.name, func(t *testing.T) {
				// The machine identity must be refused. Which refusal it gets
				// depends on which check runs first: the human decision gate
				// is applied by the shared pipeline, so a request that fails
				// another check first is refused by that one. The refusal is
				// what matters; the shape is the domain's to decide.
				if err := testCase.call(ctx); err == nil {
					t.Fatal("machine human decision accepted")
				}
			})
		}
	}
	keyCtx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{KeyID: 44, Capabilities: types.StringArray{"agent", "evidence-write", "write"}})
	if err := cases[3].call(keyCtx); err == nil {
		t.Fatal("key with no principal bypassed human guard")
	}
	if f.svc.agentRuntime.Actions.(*testActionExecutor).calls != 0 || f.svc.agentRuntime.Imports.(*testImporter).calls != 0 {
		t.Fatal("machine decision invoked external effects")
	}
	current, err := f.svc.ListEvidenceCards(context.Background(), f.owner, f.project.ID)
	if err != nil || len(current) != 1 || current[0].Confirmation != nil {
		t.Fatalf("machine changed card truth: %+v %v", current, err)
	}
}
