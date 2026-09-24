package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite" // Pure Go test driver; production retains the existing SQLite driver.
)

func testStore(t *testing.T, path string) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if !db.Migrator().HasTable(&projectRow{}) {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000018_lingdoc_workspace.up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(migration), ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatalf("production SQLite migration: %v", err)
			}
		}
	}
	if err := db.Exec("CREATE TABLE IF NOT EXISTS tenant_members (tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, status TEXT NOT NULL, deleted_at DATETIME, PRIMARY KEY (tenant_id, user_id))").Error; err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, ContractDemoTemplate{})
	return svc
}

func seedTenantMember(t *testing.T, svc *Service, actor Actor) {
	t.Helper()
	if err := svc.db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, 'active')", actor.TenantID, actor.UserID).Error; err != nil {
		t.Fatal(err)
	}
}

func asProject(t *testing.T, raw json.RawMessage) Project {
	t.Helper()
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProjectChapterDurabilityAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	svc := testStore(t, path)
	ctx := context.Background()
	owner := Actor{TenantID: 42, UserID: "owner"}
	seedTenantMember(t, svc, owner)
	input := CreateProjectInput{Name: "演示项目", TemplateID: "template-demo"}
	raw, status, replay, err := svc.CreateProject(ctx, owner, "create-001", input)
	if err != nil || status != 201 || replay {
		t.Fatalf("create: %d %v %v", status, replay, err)
	}
	project := asProject(t, raw)
	if project.SpecRevision != 0 || project.ProjectVersion != 1 || project.Status != "draft" {
		t.Fatalf("bad initial project: %+v", project)
	}
	raw2, _, replay, err := svc.CreateProject(ctx, owner, "create-001", input)
	if err != nil || !replay || string(raw2) != string(raw) {
		t.Fatalf("create replay: %v %v", replay, err)
	}
	if _, _, _, err := svc.CreateProject(ctx, owner, "create-001", CreateProjectInput{Name: "other", TemplateID: "template-demo"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key different body: %v", err)
	}
	spec := SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "公开合成样本", "research_goal": "验证流程"}}
	raw, _, _, err = svc.SaveSpec(ctx, owner, project.ID, "spec-save-001", spec)
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	if project.SpecRevision != 1 || project.ProjectVersion != 2 {
		t.Fatalf("spec revision drift: %+v", project)
	}
	if _, _, _, err := svc.SaveSpec(ctx, owner, project.ID, "spec-save-002", spec); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale spec accepted: %v", err)
	}
	raw, _, replay, err = svc.SaveSpec(ctx, owner, project.ID, "spec-save-001", spec)
	if err != nil || !replay || asProject(t, raw).SpecRevision != 1 {
		t.Fatalf("response-lost replay: %v %v", replay, err)
	}
	raw, _, _, err = svc.ActivateProject(ctx, owner, project.ID, "activate-001", ActivateProjectInput{ExpectedSpecRevision: 1})
	if err != nil || asProject(t, raw).Status != "active" {
		t.Fatalf("activate: %v", err)
	}
	chapters, err := svc.ListChapters(ctx, owner, project.ID)
	if err != nil || len(chapters) != 2 || chapters[0].CurrentVersionID != nil {
		t.Fatalf("chapters: %+v %v", chapters, err)
	}
	chapter := chapters[0]
	text := SaveChapterInput{ExpectedChapterVersionID: nil, ExpectedSpecRevision: 1, BodyMarkdown: "第一稿", SourceIDs: []string{}}
	version, _, replay, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-001", text)
	if err != nil || replay {
		t.Fatalf("save chapter: %v %v", replay, err)
	}
	var saved Chapter
	if err := json.Unmarshal(version, &saved); err != nil || saved.CurrentVersionID == nil {
		t.Fatalf("saved chapter: %+v %v", saved, err)
	}
	if _, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-002", text); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale chapter accepted: %v", err)
	}
	_, _, replay, err = svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-001", text)
	if err != nil || !replay {
		t.Fatalf("chapter response-lost replay: %v %v", replay, err)
	}
	var count int64
	if err := svc.db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapter.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate version: %d %v", count, err)
	}
	contextView, err := svc.GenerationContext(ctx, owner, project.ID, chapter.ID)
	if err != nil || contextView.SpecRevision != 1 || contextView.ChapterVersionID == nil || contextView.ChapterBody != "第一稿" {
		t.Fatalf("generation handoff: %+v %v", contextView, err)
	}
	if _, err := svc.GetProject(ctx, Actor{TenantID: 42, UserID: "outsider"}, project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project existence exposed: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-003", SaveChapterInput{
		ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:s-demo]]", SourceIDs: []string{"s-demo"},
	}); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("unchecked source accepted: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-004", SaveChapterInput{
		ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:s-demo/invalid]]", SourceIDs: []string{},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed source marker accepted: %v", err)
	}
	// A new service opens the same on-disk database and reads the exact version.
	reopened := testStore(t, path)
	fromDisk, err := reopened.ListChapters(ctx, owner, project.ID)
	if err != nil || fromDisk[0].CurrentVersionID == nil || fromDisk[0].BodyMarkdown != "第一稿" {
		t.Fatalf("restart readback: %+v %v", fromDisk, err)
	}
}

func TestTwoWritersSameSpecRevision(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "race.db"))
	ctx := context.Background()
	actor := Actor{TenantID: 17, UserID: "writer"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(ctx, actor, "create-race", CreateProjectInput{Name: "并发项目", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	id := asProject(t, raw).ID
	start := make(chan struct{})
	type writeResult struct {
		value string
		err   error
	}
	results := make(chan writeResult, 2)
	var wg sync.WaitGroup
	for i, value := range []string{"甲", "乙"} {
		wg.Add(1)
		go func(i int, value string) {
			defer wg.Done()
			<-start
			_, _, _, err := svc.SaveSpec(ctx, actor, id, "race-spec-"+string(rune('0'+i)), SaveSpecInput{
				ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": value},
			})
			results <- writeResult{value: value, err: err}
		}(i, value)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	conflicts := 0
	winner := ""
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.value
		} else if errors.Is(result.err, ErrVersionConflict) {
			conflicts++
		} else {
			t.Errorf("concurrent save returned neither success nor version conflict: %v", result.err)
		}
	}
	project, err := svc.GetProject(ctx, actor, id)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 || project.SpecRevision != 1 || project.ProjectVersion != 2 {
		t.Fatalf("lost CAS: successes=%d conflicts=%d project=%+v", successes, conflicts, project)
	}
	if got := project.Spec["research_subject"]; got != winner {
		t.Fatalf("persisted spec = %q, want winning writer value %q", got, winner)
	}
}
func TestFixedMembersAndRevokedReplay(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "members.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 12, UserID: "owner"}
	member := Actor{TenantID: 12, UserID: "member"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, member)
	if err := svc.db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (12, 'inactive', 'inactive')").Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := svc.CreateProject(ctx, owner, "create-members", CreateProjectInput{Name: "协作", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	id := asProject(t, raw).ID
	if _, _, _, err := svc.SaveMembers(ctx, owner, id, "members-bad", SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{"inactive"}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("inactive member admitted: %v", err)
	}
	input := SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{"member"}}
	raw, _, _, err = svc.SaveMembers(ctx, owner, id, "members-add", input)
	if err != nil || len(asProject(t, raw).Members) != 2 {
		t.Fatalf("add collaborator: %v", err)
	}
	if _, err := svc.GetProject(ctx, member, id); err != nil {
		t.Fatalf("member cannot read: %v", err)
	}
	if _, _, _, err := svc.SaveMembers(ctx, member, id, "members-own", SaveMembersInput{ExpectedProjectVersion: 2, CollaboratorUserIDs: []string{}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collaborator managed members: %v", err)
	}
	_, _, _, err = svc.SaveSpec(ctx, member, id, "member-spec-key", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "共同编辑"}})
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.GetProject(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.SaveMembers(ctx, owner, id, "members-remove", SaveMembersInput{ExpectedProjectVersion: project.ProjectVersion, CollaboratorUserIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, member, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked member read: %v", err)
	}
	// The old operation exists; current authorization must still run first.
	if _, _, _, err := svc.SaveSpec(ctx, member, id, "member-spec-key", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "共同编辑"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay exposed content: %v", err)
	}
	if err := svc.db.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = 12 AND user_id = 'owner'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, owner, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive tenant member read project: %v", err)
	}
	if _, _, _, err := svc.CreateProject(ctx, owner, "create-after-revoke", CreateProjectInput{Name: "不应创建", TemplateID: "template-demo"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive tenant member created project: %v", err)
	}
}

func TestManualEditPreservesReviewAndFailsClosedOnExistingSource(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "review.db"))
	ctx := context.Background()
	actor := Actor{TenantID: 51, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(ctx, actor, "create-review", CreateProjectInput{Name: "待核项", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	id := asProject(t, raw).ID
	_, _, _, err = svc.SaveSpec(ctx, actor, id, "review-spec", SaveSpecInput{ExpectedSpecRevision: 0,
		Fields: map[string]string{"research_subject": "演示", "research_goal": "验证"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.ActivateProject(ctx, actor, id, "review-activate", ActivateProjectInput{ExpectedSpecRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := svc.ListChapters(ctx, actor, id)
	if err != nil || len(chapters) != 2 {
		t.Fatalf("chapters: %v %v", chapters, err)
	}
	chapterID := chapters[0].ID
	initial := chapterVersionRow{ID: "version-with-review", ProjectID: id, ChapterID: chapterID,
		BodyMarkdown: "待人工核查", SourceIDsJSON: "[]",
		ReviewItemsJSON: `[{"id":"review-1","statement":"样本量待核实","origin_candidate_id":"candidate-1"}]`}
	if err := svc.db.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Model(&chapterRow{}).Where("id = ?", chapterID).Update("current_version_id", initial.ID).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = svc.SaveChapter(ctx, actor, id, chapterID, "edit-review", SaveChapterInput{
		ExpectedChapterVersionID: &initial.ID, ExpectedSpecRevision: 1,
		BodyMarkdown: "人工改写", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var edited Chapter
	if err := json.Unmarshal(raw, &edited); err != nil || len(edited.ReviewItems) != 1 || edited.ReviewItems[0].ID != "review-1" {
		t.Fatalf("review item lost: %+v %v", edited, err)
	}
	if err := svc.db.Model(&chapterVersionRow{}).Where("id = ?", *edited.CurrentVersionID).
		Updates(map[string]any{"source_ids_json": `["s-demo"]`, "body_markdown": "[[source:s-demo]]"}).Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.SaveChapter(ctx, actor, id, chapterID, "edit-source", SaveChapterInput{
		ExpectedChapterVersionID: edited.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "删除引用后的新正文", SourceIDs: []string{},
	})
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("existing source was dropped without T09 recheck: %v", err)
	}
}
