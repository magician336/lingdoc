package router

// The owner_unavailable freeze, judged at the HTTP boundary.
//
// The freeze is a domain fact, but the guarantee the product makes is a
// contract one: the two Owner-only ChangeSet entries answer with a reason, and
// the permission read outlet answers with the same reason. So the assertions
// live on the HTTP seam -- the production composition, the production
// authentication, the production membership -- and never on the judgement
// inside it.
//
// The fixture is the same composition the machine-read integration fixture
// builds: the real route registration, the real RBAC guards, the real member
// and owner service. A divergence between the two fixtures would be a setup
// difference, so this file reuses the helpers of that package rather than
// building a second one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// ownerFreezeFixture is the production composition, wired for one owner and
// one member.
type ownerFreezeFixture struct {
	engine   *gin.Engine
	db       *gorm.DB
	projects *workspacecore.Service
	tokens   map[string]string
}

func newOwnerFreezeFixture(t *testing.T) *ownerFreezeFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite",
		DSN: filepath.Join(t.TempDir(), "owner-freeze.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.AutoMigrate(&types.User{}, &types.AuthToken{}, &types.TenantMember{}, &types.TenantAPIKey{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"000018_lingdoc_workspace.up.sql",
		"000019_lingdoc_evidence_assets.up.sql",
		"000020_lingdoc_candidate_adoption.up.sql",
		"000023_lingdoc_chapter_confirmation_requests.up.sql",
		"000024_lingdoc_delivery_stores.up.sql",
		"000025_lingdoc_member_permissions.up.sql",
		"000026_lingdoc_project_context_revision.up.sql",
		"000027_lingdoc_draft_provenance.up.sql",
		"000028_lingdoc_owner_transfers.up.sql",
		"000029_lingdoc_project_discard.up.sql",
		"000030_lingdoc_project_baseline.up.sql",
		"000031_lingdoc_citation_usages.up.sql",
		"000032_lingdoc_change_sets.up.sql",
		"000033_lingdoc_working_copies.up.sql",
		"000034_lingdoc_selected_rewrites.up.sql",
		"000035_lingdoc_project_template_copies.up.sql",
		"000036_lingdoc_rule_sets.up.sql",
		"000037_lingdoc_full_workflow.up.sql",
		"000039_lingdoc_ruleset_export_reference.up.sql",
		"000040_lingdoc_ruleset_use_audits.up.sql",
		"000041_lingdoc_template_version_ruleset_ref.up.sql",
		// 000052 adds the two audit version columns (#327): without them the
		// audit insert of every write fails on this fixture.
		"000052_lingdoc_audit_version_fields.up.sql",
		"000053_lingdoc_project_archive.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	users := apprepo.NewUserRepository(db)
	tokens := apprepo.NewAuthTokenRepository(db)
	members := appservice.NewTenantMemberService(apprepo.NewTenantMemberRepository(db), nil, users, tokens)
	authUsers := appservice.NewUserService(cfg, users, tokens, integrationTenant{}, members, nil)
	keys := appservice.NewTenantAPIKeyService(apprepo.NewTenantAPIKeyRepository(db))
	jwtTokens := map[string]string{}
	for _, fixture := range []struct {
		id   string
		role types.TenantRole
	}{{"owner-user", types.TenantRoleOwner}, {"member-user", types.TenantRoleContributor}, {"admin-user", types.TenantRoleContributor}} {
		user := &types.User{ID: fixture.id, Username: fixture.id, Email: fixture.id + "@example.invalid", TenantID: 1, IsActive: true}
		if err := users.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		if _, err := members.AddMember(context.Background(), user.ID, 1, fixture.role, nil); err != nil {
			t.Fatal(err)
		}
		token, _, err := authUsers.GenerateTokens(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		jwtTokens[user.ID] = token
	}
	shares := &integrationShares{allowed: true}
	runtime := workspace.NewGORMSourceRuntime(db, workspace.ProjectPorts{}, shares, integrationKnowledge{}, delivery.NewFixedTemplateReader())
	projects := workspacecore.NewServiceWithSources(workspacecore.NewGORMRepository(db), workspacecore.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(workspace.ProjectPorts{Authorizer: projects, Projects: projects, Context: projects})
	runtime.EnableFullWorkflowPolicy()
	engine := gin.New()
	engine.Use(middleware.Auth(integrationTenant{}, authUsers, members, keys, cfg))
	v1 := engine.Group("/api/v1")
	guards := &rbacGuards{cfg: cfg, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	deps := workspace.HandlerDependencies{
		Projects: projects, Members: integrationMembershipService(projects, workspacecore.NewGORMRepository(db)),
		Chapters: projects, TemplateApp: projects, ChangeSets: projects,
		Sources: runtime, Integration: runtime, FullWorkflow: &workspace.FullWorkflowDependencies{
			Impact: workspace.ImpactRoutePorts{
				ChangeSets: projects, Impact: projects, Dependency: projects,
				Runtime: projects, Trials: projects, Roadmap: projects,
			},
			Agent: workspace.AgentRoutePorts{
				Research: projects, Imports: projects, EvidenceCards: projects,
				Claims: projects, Anchors: projects, AgentSessions: projects, Proposals: projects,
				Context: projects, ChangeSets: projects,
			},
			Routes: workspace.FullWorkflowRoutePorts{
				Members: integrationMembershipService(projects, workspacecore.NewGORMRepository(db)),
				Reviews: projects, Lifecycle: projects,
			},
		},
	}
	deps.WithMachineReads(projects)
	deps.WithReviewAnchors(projects, projects)
	lingdocHandler, err := workspace.NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, lingdocHandler)
	return &ownerFreezeFixture{engine: engine, db: db, projects: projects, tokens: jwtTokens}
}

// request sends one request through the production engine and returns the
// recorded answer.
func (f *ownerFreezeFixture) request(t *testing.T, method, path, body, key, identity string, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Authorization", "Bearer "+identity)
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, req)
	if response.Code != want {
		t.Fatalf("%s %s: got %d %s want %d", method, path, response.Code, response.Body.String(), want)
	}
	return response
}

// errorReason reads the reason a refused write carries.
func errorReason(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				ReasonCode string `json:"reason_code"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, response.Body.String())
	}
	return envelope.Error.Details.ReasonCode
}

// permissionReason reads the reason the permission read outlet reports.
func permissionReason(t *testing.T, response *httptest.ResponseRecorder) (bool, string) {
	t.Helper()
	var envelope struct {
		Data struct {
			Allowed    bool   `json:"allowed"`
			ReasonCode string `json:"reason_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode permission envelope: %v (%s)", err, response.Body.String())
	}
	return envelope.Data.Allowed, envelope.Data.ReasonCode
}

// activeProject creates and activates a project the Owner can decide on.
func (f *ownerFreezeFixture) activeProject(t *testing.T) workspacecore.Project {
	t.Helper()
	created := f.request(t, http.MethodPost, "/projects",
		`{"name":"owner freeze synthetic project","template_id":"template-demo"}`,
		"owner-freeze-create", f.tokens["owner-user"], 201)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	path := "/projects/" + envelope.Data.ID
	f.request(t, http.MethodPut, path+"/spec",
		`{"expected_spec_revision":0,"fields":{"research_subject":"合成研究对象","research_goal":"合成研究目标"}}`,
		"owner-freeze-spec", f.tokens["owner-user"], 200)
	f.request(t, http.MethodPost, path+"/activate", `{"expected_spec_revision":1}`,
		"owner-freeze-activate", f.tokens["owner-user"], 200)
	read := f.request(t, http.MethodGet, path, "", "", f.tokens["owner-user"], 200)
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// addMember records the member as a collaborator with an author scope on the
// chapter, so ordinary collaboration is reachable for them.
func (f *ownerFreezeFixture) addMember(t *testing.T, projectID, chapterID string) {
	t.Helper()
	if err := f.db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)",
		projectID, "member-user", "collaborator").Error; err != nil {
		t.Fatal(err)
	}
	scopes := `{"author":["` + chapterID + `"],"reviewer":["` + chapterID + `"]}`
	if err := f.db.Exec(`INSERT INTO lingdoc_member_permissions
		(project_id, user_id, governance_role, function_roles_json, function_scopes_json, status)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, "member-user", "member", `["author","reviewer"]`, scopes, "active").Error; err != nil {
		t.Fatal(err)
	}
}

// addAdmin records the admin as a project admin: not the Owner, but allowed
// to manage the project.
func (f *ownerFreezeFixture) addAdmin(t *testing.T, projectID string) {
	t.Helper()
	if err := f.db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)",
		projectID, "admin-user", "collaborator").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`INSERT INTO lingdoc_member_permissions
		(project_id, user_id, governance_role, function_roles_json, function_scopes_json, status)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, "admin-user", "admin", "[]", "{}", "active").Error; err != nil {
		t.Fatal(err)
	}
}

// lapseOwner makes the Owner unable to act in this project while the tenant
// identity stays untouched.
func (f *ownerFreezeFixture) lapseOwner(t *testing.T, projectID string) {
	t.Helper()
	if err := f.db.Exec("UPDATE lingdoc_member_permissions SET status = 'suspended' WHERE project_id = ? AND user_id = ?",
		projectID, "owner-user").Error; err != nil {
		t.Fatal(err)
	}
}

// restoreOwner gives the Owner its project membership back.
func (f *ownerFreezeFixture) restoreOwner(t *testing.T, projectID string) {
	t.Helper()
	if err := f.db.Exec("UPDATE lingdoc_member_permissions SET status = 'active' WHERE project_id = ? AND user_id = ?",
		projectID, "owner-user").Error; err != nil {
		t.Fatal(err)
	}
}

// assessedChange proposes a change the Owner still has to decide on.
func (f *ownerFreezeFixture) assessedChange(t *testing.T, project workspacecore.Project, chapterID string) workspacecore.ChangeSet {
	t.Helper()
	body := `{"expected_context_revision":` + strconv.FormatInt(project.CurrentContextRevision, 10) +
		`,"fields":{"research_subject":{"old_value":"合成研究对象","new_value":"新的合成研究对象"}}` +
		`,"affected_chapter_ids":["` + chapterID + `"],"reason":"研究对象发生变化"}`
	created := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets", body,
		"owner-freeze-propose", f.tokens["owner-user"], 201)
	var envelope struct {
		Data workspacecore.ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Status != "assessed" {
		t.Fatalf("proposed change = %+v, want assessed", envelope.Data)
	}
	return envelope.Data
}

// chapters reads the project's chapters.
func (f *ownerFreezeFixture) chapters(t *testing.T, projectID string) []workspacecore.Chapter {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID+"/chapters", "", "", f.tokens["owner-user"], 200)
	var envelope struct {
		Data []workspacecore.Chapter `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// readProject reads the project as the Owner.
func (f *ownerFreezeFixture) readProject(t *testing.T, projectID string) workspacecore.Project {
	t.Helper()
	read := f.request(t, http.MethodGet, "/projects/"+projectID, "", "", f.tokens["owner-user"], 200)
	var envelope struct {
		Data workspacecore.Project `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// commitChapter gives the chapter a formal version, so a comment and a
// review have something to point at.
func (f *ownerFreezeFixture) commitChapter(t *testing.T, project workspacecore.Project, chapterID string) string {
	t.Helper()
	path := "/projects/" + project.ID + "/chapters/" + chapterID
	f.request(t, http.MethodPut, path+"/working-copy",
		`{"base_chapter_version_id":null,"expected_spec_revision":`+strconv.FormatInt(project.SpecRevision, 10)+
			`,"expected_working_copy_revision":1,"body_markdown":"合成章节正文","source_ids":[]}`,
		"owner-freeze-copy", f.tokens["owner-user"], 200)
	f.request(t, http.MethodPost, path+"/working-copy/commit",
		`{"expected_spec_revision":`+strconv.FormatInt(project.SpecRevision, 10)+
			`,"expected_working_copy_revision":2,"expected_chapter_version_id":null}`,
		"owner-freeze-commit", f.tokens["owner-user"], 201)
	read := f.request(t, http.MethodGet, "/projects/"+project.ID+"/chapters", "", "", f.tokens["owner-user"], 200)
	var envelope struct {
		Data []workspacecore.Chapter `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, chapter := range envelope.Data {
		if chapter.ID == chapterID {
			if chapter.CurrentVersionID == nil {
				t.Fatalf("committed chapter has no current version: %+v", chapter)
			}
			return *chapter.CurrentVersionID
		}
	}
	t.Fatalf("chapter %s is gone after the commit", chapterID)
	return ""
}

// TestOwnerUnavailableFreezesChangeSetDecision is the freeze itself: the two
// Owner-only entries answer with the reason, and the permission read outlet
// answers with the same reason.
func TestOwnerUnavailableFreezesChangeSetDecision(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addMember(t, project.ID, chapter.ID)
	change := f.assessedChange(t, project, chapter.ID)

	// The Owner's project membership lapses: the Owner may no longer act as a
	// tenant member of this project, while the tenant identity is untouched.
	if err := f.db.Exec("UPDATE lingdoc_member_permissions SET status = 'suspended' WHERE project_id = ? AND user_id = ?",
		project.ID, "owner-user").Error; err != nil {
		t.Fatal(err)
	}

	apply := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-freeze-apply", f.tokens["owner-user"], 404)
	if reason := errorReason(t, apply); reason != "owner_unavailable" {
		t.Fatalf("apply refusal reason = %q, want owner_unavailable (%s)", reason, apply.Body.String())
	}

	reject := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/reject",
		"{}", "owner-freeze-reject", f.tokens["owner-user"], 404)
	if reason := errorReason(t, reject); reason != "owner_unavailable" {
		t.Fatalf("reject refusal reason = %q, want owner_unavailable (%s)", reason, reject.Body.String())
	}

	// The member is told the same reason, so the interface can say "the Owner
	// is unavailable" instead of "you lack permission".
	permission := f.request(t, http.MethodGet, "/projects/"+project.ID+"/permissions?capability=manage",
		"", "", f.tokens["member-user"], 200)
	allowed, reason := permissionReason(t, permission)
	if allowed || reason != "owner_unavailable" {
		t.Fatalf("permission read outlet = allowed %v reason %q, want false / owner_unavailable (%s)",
			allowed, reason, permission.Body.String())
	}
}

// TestOwnerUnavailableRefusalIsNotTheOwnersOwnRefusal is the distinction the
// refusal has to carry: the freeze and the caller's own lack of authority are
// two different answers.
func TestOwnerUnavailableRefusalIsNotTheOwnersOwnRefusal(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addAdmin(t, project.ID)
	change := f.assessedChange(t, project, chapter.ID)
	f.lapseOwner(t, project.ID)

	// The Owner is refused because the Owner's identity lapsed.
	frozen := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-freeze-owner-apply", f.tokens["owner-user"], 404)
	if reason := errorReason(t, frozen); reason != "owner_unavailable" {
		t.Fatalf("frozen Owner refusal reason = %q, want owner_unavailable (%s)", reason, frozen.Body.String())
	}

	// The admin is refused because the admin is not the Owner, and that
	// refusal names no freeze.
	notOwner := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-freeze-admin-apply", f.tokens["admin-user"], 404)
	if reason := errorReason(t, notOwner); reason != "" {
		t.Fatalf("non-Owner refusal reason = %q, want none (%s)", reason, notOwner.Body.String())
	}
	if !strings.Contains(notOwner.Body.String(), `"code":"not_found"`) {
		t.Fatalf("non-Owner refusal lost its error code: %s", notOwner.Body.String())
	}
}

// TestOwnerUnavailableUnfreezesWhenTheIdentityComesBack is the derived part:
// the freeze is not a state anybody repairs, it is a fact that changes by
// itself.
func TestOwnerUnavailableUnfreezesWhenTheIdentityComesBack(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addMember(t, project.ID, chapter.ID)
	change := f.assessedChange(t, project, chapter.ID)

	f.lapseOwner(t, project.ID)
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-unfreeze-blocked-apply", f.tokens["owner-user"], 404)

	// The Owner's project membership comes back. There is no repair action to
	// run: the next decision simply runs.
	f.restoreOwner(t, project.ID)
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-unfreeze-restored-apply", f.tokens["owner-user"], 200)
}

// TestOwnerUnavailableDoesNotFreezeOrdinaryCollaboration is the positive
// evidence: the freeze stops the Owner-only decision and nothing else.
func TestOwnerUnavailableDoesNotFreezeOrdinaryCollaboration(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addMember(t, project.ID, chapter.ID)

	versionID := f.commitChapter(t, project, chapter.ID)
	current := f.readProject(t, project.ID)

	f.lapseOwner(t, project.ID)

	// The member still comments.
	comment := `{"expected_project_version":` + strconv.FormatInt(current.ProjectVersion, 10) +
		`,"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) +
		`,"chapter_version_id":"` + versionID + `","body":"冻结期间的成员评论"}`
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/comments",
		comment, "owner-freeze-comment", f.tokens["member-user"], 201)

	// The member still reviews.
	review := `{"expected_project_version":` + strconv.FormatInt(current.ProjectVersion, 10) +
		`,"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) +
		`,"chapter_version_id":"` + versionID + `","action":"submit","reason":"冻结期间的成员复核"}`
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/review",
		review, "owner-freeze-review", f.tokens["member-user"], 200)

	// The member's own change-draft path is not taken over by the freeze: the
	// refusal it answers with is the ordinary one, and it names no freeze.
	proposal := `{"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) +
		`,"fields":{"research_subject":{"old_value":"合成研究对象","new_value":"成员的拟议值"}}` +
		`,"affected_chapter_ids":["` + chapter.ID + `"],"reason":"冻结期间的成员拟议"}`
	draft := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets", proposal,
		"owner-freeze-member-draft", f.tokens["member-user"], 404)
	if reason := errorReason(t, draft); reason != "" {
		t.Fatalf("member draft refusal reason = %q, want none (%s)", reason, draft.Body.String())
	}
}

// TestCompletedOwnerTransferSurvivesThePredecessorRestoration is the
// ownership part: a transfer that has been completed is not undone by the
// predecessor's identity coming back.
func TestCompletedOwnerTransferSurvivesThePredecessorRestoration(t *testing.T) {
	f := newOwnerFreezeFixture(t)
	project := f.activeProject(t)
	chapters := f.chapters(t, project.ID)
	if len(chapters) == 0 {
		t.Fatalf("activated project has no chapter: %+v", project)
	}
	chapter := chapters[0]
	f.addMember(t, project.ID, chapter.ID)
	change := f.assessedChange(t, project, chapter.ID)

	// The Owner hands the ownership over and the successor accepts it.
	requested := f.request(t, http.MethodPost, "/projects/"+project.ID+"/owner-transfer",
		`{"expected_project_version":`+strconv.FormatInt(project.ProjectVersion, 10)+`,"to_user_id":"member-user"}`,
		"owner-transfer-request", f.tokens["owner-user"], 201)
	var envelope struct {
		Data workspacecore.OwnerTransfer `json:"data"`
	}
	if err := json.Unmarshal(requested.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID == "" || envelope.Data.Status != "pending" {
		t.Fatalf("requested transfer = %+v", envelope.Data)
	}
	f.request(t, http.MethodPost, "/projects/"+project.ID+"/owner-transfer/"+envelope.Data.ID+"/accept",
		"{}", "owner-transfer-accept", f.tokens["member-user"], 200)

	// The predecessor's project membership lapses and comes back.
	f.lapseOwner(t, project.ID)
	f.restoreOwner(t, project.ID)

	// The predecessor is still not the Owner: the refusal carries no freeze.
	predecessor := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-transfer-predecessor-apply", f.tokens["owner-user"], 404)
	if reason := errorReason(t, predecessor); reason != "" {
		t.Fatalf("predecessor refusal reason = %q, want none (%s)", reason, predecessor.Body.String())
	}

	// The successor is the Owner now, and the decision runs.
	successor := f.request(t, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+change.ID+"/apply",
		"{}", "owner-transfer-successor-apply", f.tokens["member-user"], 200)
	if !strings.Contains(successor.Body.String(), `"status":"applied"`) {
		t.Fatalf("successor apply = %s", successor.Body.String())
	}
}
