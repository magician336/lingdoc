package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The AI-proposed dependency candidates on PostgreSQL (G3 gap closure #252).
//
// The SQLite round proves the behaviour; this round proves that the same
// behaviour survives the other dialect: the production migration chain runs
// unchanged, the batch is written in one transaction, the human decision
// produces the dependency edge, and a NEW connection reads it back - so the
// proposal is not process-local state.
//
// Requires an isolated TEST server, never the application's live database.
func TestPostgresDependencyCandidateBatchAndDecision(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer rootSQL.Close()
	schema := "issue252_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	for _, file := range productionLingDocMigrationNames(t, "versioned") {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if err := db.Exec("CREATE TABLE tenant_members ( role TEXT NOT NULL DEFAULT 'owner',tenant_id BIGINT, user_id TEXT, status TEXT, deleted_at TIMESTAMPTZ)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (1,'owner','active')").Error; err != nil {
		t.Fatal(err)
	}
	reader := &ruleSetSnapshotReaderStub{set: publishedWorkspaceRuleSet(t)}
	s := NewService(NewGORMRepository(db)).WithRuleSetSnapshotReader(reader)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}
	created, code, _, err := s.CreateProject(ctx, actor, "pg-candidate-create-key", CreateProjectInput{Name: "Postgres candidate", TemplateID: "template-demo"})
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	project := asProject(t, created)
	if _, _, _, err := s.SaveSpec(ctx, actor, project.ID, "pg-candidate-spec-key", SaveSpecInput{Fields: map[string]string{"research_subject": "synthetic", "research_goal": "persistence"}}); err != nil {
		t.Fatal(err)
	}
	project, err = s.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ActivateProject(ctx, actor, project.ID, "pg-candidate-activate-key", ActivateProjectInput{ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion}); err != nil {
		t.Fatal(err)
	}
	chapters, err := s.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	for i := range chapters {
		if _, _, _, err := s.SaveChapter(ctx, actor, project.ID, chapters[i].ID, "pg-candidate-chapter-key-"+string(rune('a'+i)), SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "durable text", SourceIDs: []string{}}); err != nil {
			t.Fatalf("save chapter %d: %v", i, err)
		}
	}
	chapters, err = s.ListChapters(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err = s.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := chapterVersionRef(chapters[0])
	target := chapterVersionRef(chapters[1])
	raw, status, replay, err := s.SubmitDependencyCandidateBatch(ctx, actor, project.ID, "pg-candidate-batch-key", DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "研究条件、证据卡和第 2-4 章",
		Scope:                   DependencyCandidateScope{Objects: []ObjectVersionRef{source, target}},
		Candidates: []DependencyCandidateInput{
			{Source: source, Target: target, Relation: "supports", Action: "REVALIDATE_EVIDENCE", Evidence: "第 2 章结论引用了证据卡 E12", Reason: "章节结论依赖该证据", Confidence: 0.6},
			{Source: target, Target: source, Relation: "constrains", Action: "REGENERATE", Evidence: "证据卡 E12 的版本已经落后", Reason: "证据变化需要重算章节", Confidence: 0.4},
		},
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("submit batch: status=%d replay=%v err=%v", status, replay, err)
	}
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(raw, &batch); err != nil || len(batch.Candidates) != 2 {
		t.Fatalf("batch: %+v %v", batch, err)
	}

	// The same batch key never produces a second batch.
	_, _, replay, err = s.SubmitDependencyCandidateBatch(ctx, actor, project.ID, "pg-candidate-batch-key", DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "研究条件、证据卡和第 2-4 章",
		Scope:                   DependencyCandidateScope{Objects: []ObjectVersionRef{source, target}},
		Candidates: []DependencyCandidateInput{
			{Source: source, Target: target, Relation: "supports", Action: "REVALIDATE_EVIDENCE", Evidence: "第 2 章结论引用了证据卡 E12", Reason: "章节结论依赖该证据", Confidence: 0.6},
			{Source: target, Target: source, Relation: "constrains", Action: "REGENERATE", Evidence: "证据卡 E12 的版本已经落后", Reason: "证据变化需要重算章节", Confidence: 0.4},
		},
	})
	if err != nil || !replay {
		t.Fatalf("batch replay: replay=%v err=%v", replay, err)
	}

	// Three states: confirm one, modify one, reject one.
	decisionRaw, _, _, err := s.DecideDependencyCandidate(ctx, actor, project.ID, batch.Candidates[0].ID, "pg-candidate-confirm-key", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "confirm", Reason: "人工确认这条依赖"})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var decision DependencyCandidateDecisionResult
	if err := json.Unmarshal(decisionRaw, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Candidate.Status != DependencyCandidateStatusConfirmed || decision.Edge == nil || decision.Edge.Status != "confirmed" {
		t.Fatalf("confirmed: %+v", decision)
	}
	if _, _, _, err := s.DecideDependencyCandidate(ctx, actor, project.ID, batch.Candidates[1].ID, "pg-candidate-modify-key", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[1].Version, Decision: "modify", Reason: "人工修正", Relation: pointerTo("governs"), Action: pointerTo("REVIEW_REQUIRED")}); err != nil {
		t.Fatalf("modify: %v", err)
	}

	// A NEW connection proves the proposal and the edge are not process-local.
	reopened, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	reopenedSQL, err := reopened.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedSQL.Close()
	reopenedService := NewService(NewGORMRepository(reopened))
	edges, err := reopenedService.ListDependencyEdges(ctx, actor, project.ID)
	if err != nil || len(edges) != 2 {
		t.Fatalf("edges after reopen: %+v %v", edges, err)
	}
	for _, edge := range edges {
		if edge.Status != "confirmed" {
			t.Fatalf("an undecided candidate reached the graph: %+v", edge)
		}
	}
	batches, err := reopenedService.ListDependencyCandidates(ctx, actor, project.ID, "")
	if err != nil || len(batches) != 1 || len(batches[0].Candidates) != 2 {
		t.Fatalf("candidate batches after reopen: %+v %v", batches, err)
	}
	decisions := map[string]string{}
	for _, candidate := range batches[0].Candidates {
		decisions[candidate.ID] = candidate.Status
	}
	if decisions[batch.Candidates[0].ID] != DependencyCandidateStatusConfirmed || decisions[batch.Candidates[1].ID] != DependencyCandidateStatusModified {
		t.Fatalf("candidate conclusions after reopen: %+v", decisions)
	}
	// The refused tuple of a corrected candidate may not come back.
	if _, _, _, err := s.DecideDependencyCandidate(ctx, actor, project.ID, batch.Candidates[1].ID, "pg-candidate-modify-again-key", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[1].Version + 1, Decision: "confirm", Reason: "反悔"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("re-deciding a decided candidate: err=%v, want ErrInvalidState", err)
	}
}
