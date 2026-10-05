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

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
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

type citationStatusSources struct {
	fakeSources
	statuses []CitationStatus
}

func (f *citationStatusSources) CitationStatuses(context.Context, string, string, []string) ([]CitationStatus, error) {
	return slices.Clone(f.statuses), nil
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
			"000026_lingdoc_project_context_revision.up.sql",
			"000027_lingdoc_draft_provenance.up.sql",
			"000028_lingdoc_owner_transfers.up.sql",
			"000029_lingdoc_project_discard.up.sql",
			"000030_lingdoc_project_baseline.up.sql",
			"000031_lingdoc_citation_usages.up.sql",
			"000032_lingdoc_change_sets.up.sql",
			"000033_lingdoc_project_template_copies.up.sql",
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
	raw, _, _, err = svc.ActivateProject(ctx, owner, project.ID, "activate-001", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
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
	// 引用不是「不能存」，是「存之前要核」。复核通过（这里是默认放行的替身）之后，
	// 带引用的正文与空引用正文走的是同一条路——这是 F01 的形状。
	cited := SaveChapterInput{
		ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:s-demo]] 的摘录", SourceIDs: []string{"s-demo"},
		CitationUsages: []CitationUsage{{SourceID: "s-demo", Purpose: "支撑研究问题中的背景事实", Limitation: "仅覆盖演示资料中的合成样本"}},
	}
	raw, _, replay, err = svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-003", cited)
	if err != nil || replay {
		t.Fatalf("cited chapter: %v %v", replay, err)
	}
	var citedChapter Chapter
	if err := json.Unmarshal(raw, &citedChapter); err != nil || citedChapter.CurrentVersionID == nil {
		t.Fatalf("cited chapter view: %+v %v", citedChapter, err)
	}
	if !slices.Equal(citedChapter.SourceIDs, []string{"s-demo"}) {
		t.Fatalf("citation not returned: %+v", citedChapter.SourceIDs)
	}
	if len(citedChapter.CitationUsages) != 1 || citedChapter.CitationUsages[0].Purpose != "支撑研究问题中的背景事实" {
		t.Fatalf("citation usage not returned: %+v", citedChapter.CitationUsages)
	}
	if _, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-usage-invalid", SaveChapterInput{
		ExpectedChapterVersionID: citedChapter.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: cited.BodyMarkdown, SourceIDs: cited.SourceIDs,
		CitationUsages: []CitationUsage{{SourceID: "other-source", Purpose: "越权备注"}},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("usage for an unreferenced source accepted: %v", err)
	}
	if _, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-004", SaveChapterInput{
		ExpectedChapterVersionID: citedChapter.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:s-demo/invalid]]", SourceIDs: []string{},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed source marker accepted: %v", err)
	}
	// A new service opens the same on-disk database and reads the exact version.
	reopened := testStore(t, path)
	fromDisk, err := reopened.ListChapters(ctx, owner, project.ID)
	if err != nil || fromDisk[0].CurrentVersionID == nil ||
		fromDisk[0].BodyMarkdown != cited.BodyMarkdown || !slices.Equal(fromDisk[0].SourceIDs, []string{"s-demo"}) ||
		len(fromDisk[0].CitationUsages) != 1 || fromDisk[0].CitationUsages[0].Limitation != "仅覆盖演示资料中的合成样本" {
		t.Fatalf("restart readback: %+v %v", fromDisk, err)
	}
	// 同一枚操作键在新进程里重放：拿回的还是那次写下的那一份，且标记为重放。
	// 复核跑在重放之前，所以这条路径也顺带证明了放行的复核不会把重放挡回去。
	replayed, _, replay, err := reopened.SaveChapter(ctx, owner, project.ID, chapter.ID, "chapter-003", cited)
	if err != nil || !replay || string(replayed) != string(raw) {
		t.Fatalf("chapter replay after restart: %v %v", replay, err)
	}
}

func TestTwoWritersSameSpecRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	svc := testStore(t, path)
	ctx := context.Background()
	actor := Actor{TenantID: 17, UserID: "writer"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(ctx, actor, "create-race", CreateProjectInput{Name: "并发项目", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	id := asProject(t, raw).ID
	writers := []*Service{testStore(t, path), testStore(t, path)}
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
			_, _, _, err := writers[i].SaveSpec(ctx, actor, id, "race-spec-"+string(rune('0'+i)), SaveSpecInput{
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
	if err := svc.repository.(*GORMRepository).db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (12, 'inactive', 'inactive')").Error; err != nil {
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
	if _, err := svc.GetTemplateCopy(ctx, member, id, 1); err != nil {
		t.Fatalf("member cannot read the bound template copy: %v", err)
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
	if _, err := svc.GetTemplateCopy(ctx, member, id, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked member read template copy: %v", err)
	}
	// The old operation exists; current authorization must still run first.
	if _, _, _, err := svc.SaveSpec(ctx, member, id, "member-spec-key", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "共同编辑"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay exposed content: %v", err)
	}
	if err := svc.repository.(*GORMRepository).db.Exec("UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = 12 AND user_id = 'owner'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, owner, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive tenant member read project: %v", err)
	}
	if _, _, _, err := svc.CreateProject(ctx, owner, "create-after-revoke", CreateProjectInput{Name: "不应创建", TemplateID: "template-demo"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive tenant member created project: %v", err)
	}
}

func TestManualEditPreservesReviewAndCitesSources(t *testing.T) {
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
	_, _, _, err = svc.ActivateProject(ctx, actor, id, "review-activate", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
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
	if err := svc.repository.(*GORMRepository).db.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.repository.(*GORMRepository).db.Model(&chapterRow{}).Where("id = ?", chapterID).Update("current_version_id", initial.ID).Error; err != nil {
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
	if err := svc.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("id = ?", *edited.CurrentVersionID).
		Updates(map[string]any{"source_ids_json": `["s-demo"]`, "body_markdown": "[[source:s-demo]]"}).Error; err != nil {
		t.Fatal(err)
	}
	var previous chapterVersionRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", *edited.CurrentVersionID).First(&previous).Error; err != nil {
		t.Fatal(err)
	}
	cited, _, _, err := svc.SaveChapter(ctx, actor, id, chapterID, "edit-source", SaveChapterInput{
		ExpectedChapterVersionID: edited.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:s-demo]] 的摘录", SourceIDs: []string{"s-demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var editedCited Chapter
	if err := json.Unmarshal(cited, &editedCited); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(editedCited.SourceIDs, []string{"s-demo"}) || len(editedCited.ReviewItems) != 1 {
		t.Fatalf("citation or review item lost: %+v", editedCited)
	}
	var stored chapterVersionRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", *editedCited.CurrentVersionID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	// 引用按规范序落盘（提交时就这一条，所以看不出排序；排序那条由下面那支用例钉），
	// 新版本挂在旧版本下面。
	if stored.SourceIDsJSON != `["s-demo"]` || stored.ParentVersionID == nil || *stored.ParentVersionID != previous.ID {
		t.Fatalf("stored version: %+v", stored)
	}
	// 版本不可变：上一版的行逐字未动——正文与引用都还是人手写进去的那一份。
	var untouched chapterVersionRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", previous.ID).First(&untouched).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.BodyMarkdown != previous.BodyMarkdown || untouched.SourceIDsJSON != previous.SourceIDsJSON {
		t.Fatalf("previous version mutated: %+v", untouched)
	}
	// 清空引用是用户明确放弃：空集不复核，照常落一版，待核项继续随版本保留。
	cleared, _, _, err := svc.SaveChapter(ctx, actor, id, chapterID, "edit-cleared", SaveChapterInput{
		ExpectedChapterVersionID: editedCited.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "删除引用后的新正文", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var clearedChapter Chapter
	if err := json.Unmarshal(cleared, &clearedChapter); err != nil {
		t.Fatal(err)
	}
	if len(clearedChapter.SourceIDs) != 0 || len(clearedChapter.ReviewItems) != 1 {
		t.Fatalf("clearing citations: %+v", clearedChapter)
	}
}

func TestListChaptersReportsOnlyConfirmationForCurrentBasis(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "confirmation.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 55, UserID: "owner"}
	seedTenantMember(t, svc, owner)
	raw, _, _, err := svc.CreateProject(ctx, owner, "create-confirmation", CreateProjectInput{Name: "确认测试", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(ctx, owner, project.ID, "spec-confirmation-1", SaveSpecInput{
		ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "样本", "research_goal": "验证"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	if _, _, _, err := svc.ActivateProject(ctx, owner, project.ID, "activate-confirmation", ActivateProjectInput{ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion}); err != nil {
		t.Fatal(err)
	}
	chapters, err := svc.ListChapters(ctx, owner, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("list chapters: %v, %v", chapters, err)
	}
	chapter := chapters[0]
	versionRaw, _, _, err := svc.SaveChapter(ctx, owner, project.ID, chapter.ID, "save-confirmation", SaveChapterInput{
		ExpectedSpecRevision: project.SpecRevision, BodyMarkdown: "已核正文", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved Chapter
	if err := json.Unmarshal(versionRaw, &saved); err != nil || saved.CurrentVersionID == nil {
		t.Fatalf("saved chapter: %+v, %v", saved, err)
	}

	insertConfirmation := func(id string, revision int64) {
		t.Helper()
		details, err := json.Marshal(chapterConfirmationDetails{
			ID: id, ChapterVersionID: *saved.CurrentVersionID, SpecRevision: revision,
			TemplateVersion: project.TemplateVersion, Valid: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.repository.(*GORMRepository).db.Create(&chapterConfirmationRow{ID: id, ChapterID: chapter.ID,
			ChapterVersionID: *saved.CurrentVersionID, Valid: true, DetailsJSON: string(details)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("id = ?", *saved.CurrentVersionID).Update("confirmation_valid", true).Error; err != nil {
		t.Fatal(err)
	}
	insertConfirmation("confirmation-1", project.SpecRevision)
	chapters, err = svc.ListChapters(ctx, owner, project.ID)
	if err != nil || !chapters[0].ConfirmationValid {
		t.Fatalf("current confirmation not reported: %+v, %v", chapters, err)
	}

	// Active projects reject ordinary ProjectSpec saves. Simulate the semantic
	// revision transition here so this test continues to exercise the read-side
	// confirmation currentness rule without bypassing the G1 write boundary.
	project.SpecRevision++
	project.ProjectVersion++
	project.Spec["research_goal"] = "新目标"
	rawSpec, err := json.Marshal(project.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repository.(*GORMRepository).db.Model(&projectRow{}).Where("id = ?", project.ID).Updates(map[string]any{
		"spec_json": string(rawSpec), "spec_revision": project.SpecRevision, "project_version": project.ProjectVersion,
	}).Error; err != nil {
		t.Fatal(err)
	}
	chapters, err = svc.ListChapters(ctx, owner, project.ID)
	if err != nil || chapters[0].ConfirmationValid {
		t.Fatalf("stale confirmation survived spec change: %+v, %v", chapters, err)
	}
	if err := svc.repository.(*GORMRepository).db.Model(&chapterConfirmationRow{}).Where("id = ?", "confirmation-1").Update("valid", false).Error; err != nil {
		t.Fatal(err)
	}
	insertConfirmation("confirmation-2", project.SpecRevision)
	chapters, err = svc.ListChapters(ctx, owner, project.ID)
	if err != nil || !chapters[0].ConfirmationValid {
		t.Fatalf("reconfirmation of current basis not reported: %+v, %v", chapters, err)
	}
}

// errRecheckDenied 是复核届的替身给出的拒绝。核心域不解释复核的结论，只原样转交，
// 所以这里用一个平白的 sentinel：用例断言的正是「它被原样转交」。
var errRecheckDenied = errors.New("recheck denied")

// readyChapter 建一个已激活、已有两章的项目，返回项目 ID 与第一章 ID。
// 复核那几条用例的共同前置，除「必须真的激活」之外没有别的意思。
func readyChapter(t *testing.T, svc *Service, actor Actor, prefix string) (string, string) {
	t.Helper()
	ctx := context.Background()
	raw, _, _, err := svc.CreateProject(ctx, actor, prefix+"-create", CreateProjectInput{Name: "复核演示", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID
	if _, _, _, err := svc.SaveSpec(ctx, actor, projectID, prefix+"-spec", SaveSpecInput{
		ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "样本", "research_goal": "验证"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ActivateProject(ctx, actor, projectID, prefix+"-activate", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2}); err != nil {
		t.Fatal(err)
	}
	chapters, err := svc.ListChapters(ctx, actor, projectID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %+v %v", chapters, err)
	}
	return projectID, chapters[0].ID
}

func TestNormalizeCitationUsagesRejectsInvalid(t *testing.T) {
	longText := strings.Repeat("x", 2001)
	tests := []struct {
		name  string
		value []CitationUsage
	}{
		{name: "blank source", value: []CitationUsage{{SourceID: "   "}}},
		{name: "duplicate source", value: []CitationUsage{{SourceID: "alpha"}, {SourceID: "alpha"}}},
		{name: "purpose too long", value: []CitationUsage{{SourceID: "alpha", Purpose: longText}}},
		{name: "limitation too long", value: []CitationUsage{{SourceID: "alpha", Limitation: longText}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeCitationUsages(tt.value, []string{"alpha"}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("normalizeCitationUsages() error = %v, want %v", err, ErrInvalidRequest)
			}
		})
	}
}

func TestNormalizeCitationUsagesCompletesDeclaredSources(t *testing.T) {
	got, err := normalizeCitationUsages([]CitationUsage{{SourceID: "alpha", Purpose: "支持"}}, []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []CitationUsage{{SourceID: "alpha", Purpose: "支持"}, {SourceID: "beta"}}) {
		t.Fatalf("normalizeCitationUsages() = %#v", got)
	}
}

func TestListChaptersReportsCitationStatusesAndRedactsUnavailableContent(t *testing.T) {
	policy := &citationStatusSources{statuses: []CitationStatus{{SourceID: "alpha", Status: "unavailable", Detail: "来源已撤权"}}}
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "citation-status.db"), policy)
	ctx := context.Background()
	actor := Actor{TenantID: 71, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	projectID, chapterID := readyChapter(t, svc, actor, "citation-status")
	if _, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "citation-status-save", SaveChapterInput{
		ExpectedSpecRevision: 1, BodyMarkdown: "受限正文 [[source:alpha]]", SourceIDs: []string{"alpha"},
		CitationUsages: []CitationUsage{{SourceID: "alpha", Purpose: "敏感用途"}},
	}); err != nil {
		t.Fatal(err)
	}
	chapters, err := svc.ListChapters(ctx, actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) == 0 || chapters[0].BodyMarkdown != "" || len(chapters[0].CitationUsages) != 0 || len(chapters[0].CitationStatuses) != 1 {
		t.Fatalf("redacted chapter = %+v", chapters)
	}
	if chapters[0].CitationStatuses[0].Status != "unavailable" {
		t.Fatalf("citation status = %+v", chapters[0].CitationStatuses)
	}
	policy.statuses = []CitationStatus{{SourceID: "alpha", Status: "needs_review", Detail: "来源版本已变化"}}
	chapters, err = svc.ListChapters(ctx, actor, projectID)
	if err != nil || len(chapters) == 0 || chapters[0].BodyMarkdown != "" || len(chapters[0].CitationUsages) != 0 {
		t.Fatalf("stale citation was not redacted: %+v, %v", chapters, err)
	}
}

func TestSaveChapterRechecksDeclaredSources(t *testing.T) {
	policy := &fakeSources{}
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "recheck.db"), policy)
	ctx := context.Background()
	actor := Actor{TenantID: 61, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	projectID, chapterID := readyChapter(t, svc, actor, "recheck")

	// 正文里三条标记指向两条不同的引用（其中一条重复），提交的 source_ids 是乱序的：
	// 复核收到的必须是**排序去重后**的那一份，而且只问一次（不是每条问一次）。
	raw, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "recheck-001", SaveChapterInput{
		ExpectedSpecRevision: 1,
		BodyMarkdown:         "[[source:beta]] 与 [[source:alpha]] 都说 [[source:beta]]",
		SourceIDs:            []string{"beta", "alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.calls) != 1 {
		t.Fatalf("expected exactly one recheck, got %d", len(policy.calls))
	}
	if call := policy.calls[0]; !slices.Equal(call.sourceIDs, []string{"alpha", "beta"}) ||
		call.projectID != projectID || call.actorID != actor.UserID {
		t.Fatalf("recheck got the wrong arguments: %+v", call)
	}
	var saved Chapter
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	// 落盘的引用与交出去的是同一份规范序：同一组引用无论提交顺序如何，JSON 逐字节相同。
	// 幂等重放的指纹比对与 F01 的 source_ids 断言都靠这条性质。
	var stored chapterVersionRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", *saved.CurrentVersionID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SourceIDsJSON != `["alpha","beta"]` {
		t.Fatalf("citations not stored in canonical order: %s", stored.SourceIDsJSON)
	}

	// 清空引用是用户明确放弃，不是「没有复核的东西」——那一次提问不该发生。
	if _, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "recheck-002", SaveChapterInput{
		ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "不再引用任何资料", SourceIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if len(policy.calls) != 1 {
		t.Fatalf("empty citation set must not be rechecked, got %d calls", len(policy.calls))
	}
}

func TestSaveChapterRecheckRejectionLeavesNoTrace(t *testing.T) {
	policy := &fakeSources{}
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "denied.db"), policy)
	ctx := context.Background()
	actor := Actor{TenantID: 62, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	projectID, chapterID := readyChapter(t, svc, actor, "denied")

	policy.verdict = errRecheckDenied
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "[[source:alpha]] 的摘录", SourceIDs: []string{"alpha"}}
	if _, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "denied-001", input); !errors.Is(err, errRecheckDenied) {
		t.Fatalf("rejection not surfaced verbatim: %v", err)
	}
	// 复核跑在这笔事务的第一次写入之前，并且整笔回滚：一行都不该留下。
	var versions int64
	if err := svc.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapterID).Count(&versions).Error; err != nil || versions != 0 {
		t.Fatalf("rejected save left %d versions: %v", versions, err)
	}
	var chapter chapterRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", chapterID).First(&chapter).Error; err != nil {
		t.Fatal(err)
	}
	if chapter.CurrentVersionID != nil {
		t.Fatalf("rejected save advanced the chapter: %v", *chapter.CurrentVersionID)
	}
	// 被拒的那次没占住操作键：复核恢复之后，同一枚键仍然能正常落下这一版。
	policy.verdict = nil
	if _, status, replay, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "denied-001", input); err != nil || replay || status != 201 {
		t.Fatalf("save after the rejection: %d %v %v", status, replay, err)
	}
}

// TestSaveChapterRecheckRunsBeforeReplay 是这组用例里最值钱的一条：它把
// 「复核跑在幂等重放之前」钉成回归测试。谁把复核挪进 write（就管不住重放了），
// 或者挪到 operation 之外（就绕得过成员判定），这一条立刻红。
func TestSaveChapterRecheckRunsBeforeReplay(t *testing.T) {
	policy := &fakeSources{}
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "replay.db"), policy)
	ctx := context.Background()
	actor := Actor{TenantID: 63, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	projectID, chapterID := readyChapter(t, svc, actor, "replay")

	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "[[source:alpha]] 的摘录", SourceIDs: []string{"alpha"}}
	if _, status, replay, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "replay-001", input); err != nil || replay || status != 201 {
		t.Fatalf("first save: %d %v %v", status, replay, err)
	}
	// 复核翻转成拒绝，然后**用同一枚操作键、同一份 body**再请求一次：
	// 撤销授权的人拿一枚旧的 Idempotency-Key 重试，不能从缓存里把那次写下的正文换回去。
	policy.verdict = errRecheckDenied
	body, _, replay, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "replay-001", input)
	if !errors.Is(err, errRecheckDenied) || replay || body != nil {
		t.Fatalf("replay bypassed the recheck: %v %v %v", body, replay, err)
	}
	if len(policy.calls) != 2 {
		t.Fatalf("recheck must run on replay too, got %d calls", len(policy.calls))
	}
	// 被拒的重放也不动摇已经落下的那一版。
	var chapter chapterRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", chapterID).First(&chapter).Error; err != nil {
		t.Fatal(err)
	}
	if chapter.CurrentVersionID == nil {
		t.Fatal("the accepted version disappeared")
	}
	var stored chapterVersionRow
	if err := svc.repository.(*GORMRepository).db.Where("id = ?", *chapter.CurrentVersionID).First(&stored).Error; err != nil || stored.SourceIDsJSON != `["alpha"]` {
		t.Fatalf("stored version damaged by the rejected replay: %+v %v", stored, err)
	}
}

// TestSaveChapterWithoutSourcePolicyFailsClosed 钉住「漏装端口」的后果：带引用的写入
// 一律拒绝，而不是静默放行。空引用的编辑照常——装配漏项不该让整条编辑路径瘫掉。
func TestSaveChapterWithoutSourcePolicyFailsClosed(t *testing.T) {
	svc := testStoreWithPolicy(t, filepath.Join(t.TempDir(), "no-policy.db"), nil)
	ctx := context.Background()
	actor := Actor{TenantID: 64, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	projectID, chapterID := readyChapter(t, svc, actor, "no-policy")

	raw, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "no-policy-001", SaveChapterInput{
		ExpectedSpecRevision: 1, BodyMarkdown: "没有引用", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("citation-free save without a policy: %v", err)
	}
	var saved Chapter
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveChapter(ctx, actor, projectID, chapterID, "no-policy-002", SaveChapterInput{
		ExpectedChapterVersionID: saved.CurrentVersionID, ExpectedSpecRevision: 1,
		BodyMarkdown: "[[source:alpha]] 的摘录", SourceIDs: []string{"alpha"},
	}); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("citation accepted without a source policy: %v", err)
	}
}

func TestProjectTemplateCopyIsPrivateVersionedAndDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template-copy.db")
	svc := testStore(t, path)
	ctx := context.Background()
	owner := Actor{TenantID: 88, UserID: "owner"}
	outsider := Actor{TenantID: 88, UserID: "outsider"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, outsider)

	created, _, replayed, err := svc.CreateProject(ctx, owner, "copy-create-1", CreateProjectInput{Name: "副本隔离", TemplateID: lingdoctemplate.DemoTemplateID})
	if err != nil || replayed {
		t.Fatalf("create project: replay=%v err=%v", replayed, err)
	}
	project := asProject(t, created)
	if project.TemplateCopy == nil || project.TemplateCopy.Version != 1 || project.TemplateCopy.Status != TemplateCopyDraft || project.TemplateCopy.ContentHash == "" || project.TemplateCopy.RulesetHash == "" {
		t.Fatalf("project copy was not created from a pinned template: %+v", project.TemplateCopy)
	}
	specRaw, _, _, err := svc.SaveSpec(ctx, owner, project.ID, "copy-spec-1", SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields:               map[string]string{"research_subject": "合成研究对象", "research_goal": "验证可追溯写作"},
	})
	if err != nil {
		t.Fatalf("save project fields: %v", err)
	}
	project = asProject(t, specRaw)
	original := *project.TemplateCopy

	// A second service instance proves that the immutable copy is stored, not held in process memory.
	restarted := testStore(t, path)
	persisted, err := restarted.GetTemplateCopy(ctx, owner, project.ID, 1)
	if err != nil || persisted.ContentHash != original.ContentHash || persisted.Definition.Name != original.Definition.Name {
		t.Fatalf("copy did not survive service restart: %+v %v", persisted, err)
	}

	definition := original.Definition
	definition.Fields = append([]lingdoctemplate.Field(nil), definition.Fields...)
	definition.Sections = append([]lingdoctemplate.Section(nil), definition.Sections...)
	definition.Terms = append([]lingdoctemplate.Term{}, definition.Terms...)
	definition.Rules = append([]lingdoctemplate.Rule(nil), definition.Rules...)
	definition.Fields[0].Label = "新的研究对象标签"
	definition.Sections[0].Title = "新版研究问题"
	edit := TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: original.Version,
		Fields: definition.Fields, Sections: definition.Sections, Terms: definition.Terms,
		RequiredFields: definition.RequiredFields, Rules: definition.Rules,
	}
	preview, err := svc.PreviewTemplateCopyEdit(ctx, owner, project.ID, edit)
	if err != nil || preview.TargetCopyVersion != 2 || preview.RulesetChanged || preview.TargetRulesetHash == "" || len(preview.SectionChanges) != 1 || preview.SectionChanges[0].Status != "changed" {
		t.Fatalf("copy edit preview is incomplete: %+v %v", preview, err)
	}
	preservedField := false
	for _, field := range preview.Fields {
		if field.FieldID == "research_subject" && (field.Status != "preserved" || field.Value != "合成研究对象") {
			t.Fatalf("migration preview did not show preserved user data: %+v", field)
		}
		if field.FieldID == "research_subject" {
			preservedField = true
		}
	}
	if !preservedField {
		t.Fatal("migration preview omitted the user's existing research_subject value")
	}
	updatedRaw, _, replayed, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "copy-edit-1", edit)
	if err != nil || replayed {
		t.Fatalf("save copy edit: replay=%v err=%v", replayed, err)
	}
	updated := asProject(t, updatedRaw)
	if updated.ProjectVersion != project.ProjectVersion+1 || updated.TemplateCopyVersion != 2 || updated.TemplateCopy.ContentHash == original.ContentHash {
		t.Fatalf("edited copy did not advance atomically: %+v", updated)
	}
	replayedRaw, _, replayed, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "copy-edit-1", edit)
	if err != nil || !replayed || string(replayedRaw) != string(updatedRaw) {
		t.Fatalf("same edit was not idempotent: replay=%v err=%v", replayed, err)
	}
	if _, _, _, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "copy-edit-stale", edit); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale copy edit was accepted: %v", err)
	}

	historic, err := restarted.GetTemplateCopy(ctx, owner, project.ID, 1)
	if err != nil || historic.ContentHash != original.ContentHash || historic.Definition.Fields[0].Label != original.Definition.Fields[0].Label || historic.Status != TemplateCopySuperseded {
		t.Fatalf("old copy was mutated instead of retained: %+v %v", historic, err)
	}
	if _, err := svc.GetTemplateCopy(ctx, outsider, project.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project copy access leaked: %v", err)
	}

	secondRaw, _, _, err := svc.CreateProject(ctx, owner, "copy-create-2", CreateProjectInput{Name: "另一项目", TemplateID: lingdoctemplate.DemoTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	second := asProject(t, secondRaw)
	if second.TemplateCopy.ID == original.ID || second.TemplateCopy.Definition.Fields[0].Label != original.Definition.Fields[0].Label {
		t.Fatalf("two projects share a mutable template copy: first=%+v second=%+v", updated.TemplateCopy, second.TemplateCopy)
	}

	blockingRules := append([]lingdoctemplate.Rule(nil), updated.TemplateCopy.Definition.Rules...)
	blockingRules = append(blockingRules, lingdoctemplate.Rule{ID: "project-blocking", Kind: "presence", Severity: lingdoctemplate.SeverityBlocking, Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal"}})
	unsafeEdit := TemplateCopyDefinitionInput{
		ExpectedProjectVersion: updated.ProjectVersion, ExpectedTemplateCopyVersion: updated.TemplateCopyVersion,
		Fields: updated.TemplateCopy.Definition.Fields, Sections: updated.TemplateCopy.Definition.Sections,
		Terms: updated.TemplateCopy.Definition.Terms, RequiredFields: updated.TemplateCopy.Definition.RequiredFields, Rules: blockingRules,
	}
	if _, _, _, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "copy-edit-blocking", unsafeEdit); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("project-authored blocking rule was accepted: %v", err)
	}
}

func TestConcurrentProjectCreationCreatesOneTemplateCopyForAnIdempotencyKey(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "template-copy-race.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 90, UserID: "owner"}
	seedTenantMember(t, svc, owner)

	type outcome struct {
		body     json.RawMessage
		status   int
		replayed bool
		err      error
	}
	const attempts = 6
	start := make(chan struct{})
	results := make(chan outcome, attempts)
	var workers sync.WaitGroup
	for range attempts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			body, status, replayed, err := svc.CreateProject(ctx, owner, "template-copy-race-key", CreateProjectInput{
				Name: "并发副本创建", TemplateID: lingdoctemplate.DemoTemplateID,
			})
			results <- outcome{body: body, status: status, replayed: replayed, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	projectID := ""
	created := 0
	for result := range results {
		if result.err != nil {
			if errors.Is(result.err, ErrRequestInProgress) {
				continue
			}
			t.Fatalf("concurrent project create: %v", result.err)
		}
		if result.status != 201 {
			t.Fatalf("project create status = %d, want 201", result.status)
		}
		var project Project
		if err := json.Unmarshal(result.body, &project); err != nil {
			t.Fatal(err)
		}
		if project.TemplateCopy == nil || project.TemplateCopy.ProjectID != project.ID || project.TemplateCopy.Version != 1 {
			t.Fatalf("project response lost its atomic template binding: %+v", project)
		}
		if projectID == "" {
			projectID = project.ID
		} else if project.ID != projectID {
			t.Fatalf("same idempotency key created multiple projects: %q and %q", projectID, project.ID)
		}
		if !result.replayed {
			created++
		}
	}
	if projectID == "" || created != 1 {
		t.Fatalf("project creation outcomes: id=%q non-replayed creates=%d, want exactly one", projectID, created)
	}
	var copies int64
	if err := svc.repository.(*GORMRepository).db.Model(&projectTemplateCopyRow{}).Where("project_id = ?", projectID).Count(&copies).Error; err != nil {
		t.Fatal(err)
	}
	if copies != 1 {
		t.Fatalf("persisted template copies = %d, want one atomic project binding", copies)
	}
}

func TestTemplateCopyMigrationPreviewExplainsFieldChangesWithoutDroppingValues(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "template-migration.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 89, UserID: "owner"}
	seedTenantMember(t, svc, owner)

	created, _, _, err := svc.CreateProject(ctx, owner, "migration-create", CreateProjectInput{Name: "迁移预览", TemplateID: lingdoctemplate.DemoTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	updated, _, _, err := svc.SaveSpec(ctx, owner, project.ID, "migration-spec", SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields:               map[string]string{"research_subject": "合成研究对象", "research_goal": "验证可追溯写作"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, updated)
	definition := project.TemplateCopy.Definition
	definition.Fields = []lingdoctemplate.Field{
		{ID: "research_subject", Label: "数值主题", Type: "number", Order: 1, Required: true},
		{ID: "research_year", Label: "研究年份", Type: "integer", Order: 2, Required: true},
		{ID: "optional_note", Label: "补充说明", Type: "string", Order: 3},
	}
	definition.RequiredFields = []string{"research_subject", "research_year"}
	edit := TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
		RequiredFields: definition.RequiredFields, Rules: definition.Rules,
	}
	preview, err := svc.PreviewTemplateCopyEdit(ctx, owner, project.ID, edit)
	if err != nil {
		t.Fatalf("preview template migration: %v", err)
	}
	byID := make(map[string]TemplateMigrationField, len(preview.Fields))
	for _, field := range preview.Fields {
		byID[field.FieldID] = field
	}
	if field := byID["research_subject"]; field.Status != "incompatible" || field.OldType != "string" || field.NewType != "number" || field.Value != "合成研究对象" {
		t.Fatalf("type change preview = %+v", field)
	}
	if field := byID["research_goal"]; field.Status != "orphaned" || field.Value != "验证可追溯写作" || field.Before == "" {
		t.Fatalf("deleted field was not preserved as an orphan: %+v", field)
	}
	if field := byID["research_year"]; field.Status != "missing" || field.NewType != "integer" {
		t.Fatalf("new required field preview = %+v", field)
	}
	if field := byID["optional_note"]; field.Status != "added" || field.NewType != "string" {
		t.Fatalf("new optional field was omitted from preview: %+v", field)
	}
	if !slices.Contains(preview.MissingRequired, "research_subject") || !slices.Contains(preview.MissingRequired, "research_year") || !slices.Contains(preview.Orphaned, "research_goal") || !slices.Contains(preview.Incompatible, "research_subject") {
		t.Fatalf("migration summary omitted required/type/orphan impacts: %+v", preview)
	}
}
