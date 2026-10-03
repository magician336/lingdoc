package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite" // Pure Go test driver; production retains the existing SQLite driver.
)

// fakeSources 是来源复核在核心域用例里的替身：默认放行、可随时翻转成拒绝，
// 并记下每次收到的坐标与调用次数。
//
// 默认放行而不是默认拒绝，是因为这些用例测的是 T08 自己的行为（幂等、版本推进、
// 待核项留存），不是 T09 的判定；传 nil 会让所有带引用的路径集体 403，看着像
// 大面积回归。「装配漏了端口」另有一条专门的用例，用的是 nil。
//
// 它记下的 sourceIDs 是**原样**的（调用方给什么记什么），因为核心域该在自己这一侧
// 把它们排好序、去好重再交出去——那一条正是 TestSaveChapterRechecksDeclaredSources 要钉的。
type fakeSources struct {
	verdict error
	calls   []fakeSourceCall
}

type fakeSourceCall struct {
	projectID string
	actorID   string
	sourceIDs []string
}

func (f *fakeSources) Validate(_ context.Context, projectID, actorID string, sourceIDs []string) error {
	f.calls = append(f.calls, fakeSourceCall{projectID: projectID, actorID: actorID, sourceIDs: slices.Clone(sourceIDs)})
	return f.verdict
}

func testStore(t *testing.T, path string) *Service {
	t.Helper()
	return testStoreWithPolicy(t, path, &fakeSources{})
}

// testStoreWithPolicy 与 testStore 走同一条建库路径，只是换一台来源复核。
// policy 传 nil 也是合法输入（装配漏项），后果由用例自己断言。
func testStoreWithPolicy(t *testing.T, path string, policy SourcePolicy) *Service {
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
		for _, name := range []string{
			"000018_lingdoc_workspace.up.sql",
			"000019_lingdoc_evidence_assets.up.sql",
			"000020_lingdoc_candidate_adoption.up.sql",
			"000023_lingdoc_chapter_confirmation_requests.up.sql",
			"000025_lingdoc_member_permissions.up.sql",
			"000026_lingdoc_working_copies.up.sql",
			"000027_lingdoc_selected_rewrites.up.sql",
		} {
			migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range strings.Split(stripSQLLineComments(string(migration)), ";") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if err := db.Exec(stmt).Error; err != nil {
					t.Fatalf("production SQLite migration %s: %v", name, err)
				}
			}
		}
	}
	if err := db.Exec("CREATE TABLE IF NOT EXISTS tenant_members (tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, status TEXT NOT NULL, deleted_at DATETIME, PRIMARY KEY (tenant_id, user_id))").Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, policy)
	return svc
}

func (s *Service) testDB() *gorm.DB { return s.repository.(*GORMRepository).db }

func stripSQLLineComments(script string) string {
	var uncommented strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		uncommented.WriteString(line)
		uncommented.WriteByte('\n')
	}
	return uncommented.String()
}

func seedTenantMember(t *testing.T, svc *Service, actor Actor) {
	t.Helper()
	if err := svc.repository.(*GORMRepository).db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, 'active')", actor.TenantID, actor.UserID).Error; err != nil {
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
	if err := svc.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapter.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate version: %d %v", count, err)
	}
	contextView, err := svc.GenerationContext(ctx, owner, project.ID, chapter.ID)
	if err != nil || contextView.SpecRevision != 1 || contextView.ChapterVersionID == nil || contextView.ChapterBody != "第一稿" {
		t.Fatalf("generation handoff: %+v %v", contextView, err)
	}
	if _, err := svc.GetProject(ctx, Actor{TenantID: 42, UserID: "outsider"}, project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project existence exposed: %v", err)
	}
	// 引用不是「不能存」，是「存之前要核」。复核�