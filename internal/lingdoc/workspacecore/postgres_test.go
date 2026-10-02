package workspacecore

import (
	"context"
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
	for _, file := range []string{
		"000097_lingdoc_workspace.up.sql", "000098_lingdoc_evidence_assets.up.sql",
		"000099_lingdoc_candidate_adoption.up.sql", "000100_lingdoc_generation_runs.up.sql",
		"000101_lingdoc_generation_claim_token.up.sql", "000102_lingdoc_chapter_confirmation_requests.up.sql",
		"000103_lingdoc_delivery_stores.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if err := db.Exec("CREATE TABLE tenant_members (tenant_id BIGINT, user_id TEXT, status TEXT, deleted_at TIMESTAMPTZ)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id,user_id,status) VALUES (1,'owner','active')").Error; err != nil {
		t.Fatal(err)
	}
	s := NewService(NewGORMRepository(db))
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}
	input := CreateProjectInput{Name: "Postgres synthetic", TemplateID: "template-demo"}
	created, code, _, err := s.CreateProject(ctx, actor, "pg-create-key", input)
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	id := asProject(t, created).ID
	second, _, replay, err := s.CreateProject(ctx, actor, "pg-create-key", input)
	if err != nil || !replay || string(second) != string(created) {
		t.Fatalf("replay: %v %v", replay, err)
	}
	spec := SaveSpecInput{Fields: map[string]string{"research_subject": "synthetic", "research_goal": "persistence"}}
	if _, _, _, err := s.SaveSpec(ctx, actor, id, "pg-spec-key", spec); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveSpec(ctx, actor, id, "pg-stale-key", spec); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("CAS: %v", err)
	}
	if _, _, _, err := s.ActivateProject(ctx, actor, id, "pg-activate-key", ActivateProjectInput{ExpectedSpecRevision: 1}); err != nil {
		t.Fatal(err)
	}
	chapters, err := s.ListChapters(ctx, actor, id)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %v", chapters, err)
	}
	chapterInput := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "durable text", SourceIDs: []string{}}
	if _, _, _, err := s.SaveChapter(ctx, actor, id, chapters[0].ID, "pg-chapter-key", chapterInput); err != nil {
		t.Fatal(err)
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
	read, err := NewService(NewGORMRepository(reopened)).GenerationContext(ctx, actor, id, chapters[0].ID)
	if err != nil || read.ChapterBody != "durable text" || read.ChapterVersionID == nil {
		t.Fatalf("reopen: %+v %v", read, err)
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
