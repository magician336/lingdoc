package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Requires an isolated TEST server, never the application's live database.
// One generated schema per run; production migration is executed unchanged.
func TestPostgresMigrationAndRepositoryContract(t *testing.T) {
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
	schema := "issue40_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	if err := db.Exec("INSERT INTO tenant_members (tenant_id,user_id,status) VALUES (1,'owner','active')").Error; err != nil {
		t.Fatal(err)
	}
	reader := &ruleSetSnapshotReaderStub{set: publishedWorkspaceRuleSet(t)}
	s := NewService(NewGORMRepository(db)).WithRuleSetSnapshotReader(reader)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}
	input := CreateProjectInput{Name: "Postgres synthetic", TemplateID: "template-demo"}
	created, code, _, err := s.CreateProject(ctx, actor, "pg-create-key", input)
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	createdProject := asProject(t, created)
	id := createdProject.ID
	second, _, replay, err := s.CreateProject(ctx, actor, "pg-create-key", input)
	if err != nil || !replay || string(second) != string(created) {
		t.Fatalf("replay: %v %v", replay, err)
	}
	// Binding appends a template copy and requires all mandatory fields to be
	// populated. Keep the stale-spec CAS check before binding and use the
	// project version returned by the successful save for the binding request.
	spec := SaveSpecInput{ExpectedSpecRevision: createdProject.SpecRevision, Fields: map[string]string{"research_subject": "synthetic", "research_goal": "persistence"}}
	savedRaw, code, replay, err := s.SaveSpec(ctx, actor, id, "pg-spec-key", spec)
	if err != nil || code != 200 || replay {
		t.Fatalf("save spec: code=%d replay=%v err=%v", code, replay, err)
	}
	savedProject := asProject(t, savedRaw)
	if _, _, _, err := s.SaveSpec(ctx, actor, id, "pg-stale-key", spec); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("CAS: %v", err)
	}
	boundRaw, code, replay, err := s.BindTemplateCopyRuleSet(ctx, actor, id, "pg-bind-ruleset-key", BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: savedProject.ProjectVersion, ExpectedTemplateCopyVersion: savedProject.TemplateCopyVersion,
		RuleSetID: reader.set.ID, RuleSetVersion: reader.set.Version,
	})
	if err != nil || code != 200 || replay {
		t.Fatalf("bind RuleSet: code=%d replay=%v err=%v", code, replay, err)
	}
	boundProject := asProject(t, boundRaw)
	if boundProject.TemplateCopy == nil || boundProject.TemplateCopy.Definition.RuleSetSnapshot == nil || boundProject.TemplateCopy.Definition.RuleSetSnapshot.RulesetHash != reader.set.RulesetHash {
		t.Fatalf("PostgreSQL binding response lost the published snapshot: %+v", boundProject.TemplateCopy)
	}
	projectAfterSpec, err := s.GetProject(ctx, actor, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ActivateProject(ctx, actor, id, "pg-activate-key", ActivateProjectInput{ExpectedSpecRevision: projectAfterSpec.SpecRevision, ExpectedProjectVersion: projectAfterSpec.ProjectVersion, ReviewedProjectVersion: projectAfterSpec.ProjectVersion}); err != nil {
		t.Fatal(err)
	}
	chapters, err := s.ListChapters(ctx, actor, id)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %v", chapters, err)
	}
	chapterInput := SaveChapterInput{ExpectedSpecRevision: projectAfterSpec.SpecRevision, BodyMarkdown: "durable text", SourceIDs: []string{}}
	if _, _, _, err := s.SaveChapter(ctx, actor, id, chapters[0].ID, "pg-chapter-key", chapterInput); err != nil {
		t.Fatal(err)
	}
	project, err := s.GetProject(ctx, actor, id)
	if err != nil {
		t.Fatal(err)
	}
	changeRaw, code, replay, err := s.CreateChangeSet(ctx, actor, id, "pg-change-key", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "synthetic", NewValue: "synthetic-next"}},
		AffectedChapterIDs:      []string{chapters[0].ID},
		Reason:                  "PostgreSQL persistence",
	})
	if err != nil || code != 201 || replay {
		t.Fatalf("create: code=%d replay=%v err=%v", code, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(changeRaw, &change); err != nil || change.Status != "assessed" {
		t.Fatalf("preview: %+v %v", change, err)
	}
	if _, code, replay, err := s.ApplyChangeSet(ctx, actor, id, change.ID, "pg-apply-key"); err != nil || code != 200 || replay {
		t.Fatalf("apply: code=%d replay=%v err=%v", code, replay, err)
	}
	if _, code, replay, err := s.ApplyChangeSet(ctx, actor, id, change.ID, "pg-apply-key"); err != nil || code != 200 || !replay {
		t.Fatalf("apply replay: code=%d replay=%v err=%v", code, replay, err)
	}
	// New pool/service proves read-back is not process-local state.
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
	reopenedProject, err := reopenedService.GetProject(ctx, actor, id)
	if err != nil || reopenedProject.TemplateCopy == nil || reopenedProject.TemplateCopy.Definition.RuleSetSnapshot == nil || reopenedProject.TemplateCopy.Definition.RuleSetSnapshot.RulesetHash != reader.set.RulesetHash {
		t.Fatalf("reopen lost the project-copy RuleSet snapshot: %+v err=%v", reopenedProject.TemplateCopy, err)
	}
	read, err := reopenedService.GenerationContext(ctx, actor, id, chapters[0].ID)
	if err != nil || read.ChapterBody != "durable text" || read.ChapterVersionID == nil {
		t.Fatalf("reopen: %+v %v", read, err)
	}
	readChange, err := reopenedService.GetChangeSet(ctx, actor, id, change.ID)
	if err != nil || readChange.Status != "applied" || readChange.TargetContextRevision == nil || *readChange.TargetContextRevision != 2 {
		t.Fatalf("reopen formal change set: %+v %v", readChange, err)
	}
	if err := db.Exec("UPDATE tenant_members SET status='inactive' WHERE user_id='owner'").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveChapter(ctx, actor, id, chapters[0].ID, "pg-chapter-key", chapterInput); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay: %v", err)
	}
	var count int64
	if err := db.Model(&chapterVersionRow{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate versions: %d %v", count, err)
	}
}
