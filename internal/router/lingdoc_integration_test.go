package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// Only external tenant/catalog/share lookups are fixtures. JWT issuance and
// validation, token/key repositories, membership, workspace, evidence storage,
// handler and main route registration/guards are the production implementations.
type integrationTenant struct{ interfaces.TenantService }

func (integrationTenant) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type integrationKnowledge struct {
	interfaces.KnowledgeBaseService
}

func (integrationKnowledge) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "shared-kb", TenantID: 2}, nil
}
func (integrationKnowledge) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return []*types.SearchResult{}, nil
}

type integrationShares struct {
	interfaces.KBShareService
	allowed bool
}

func (s *integrationShares) CheckTenantKBPermission(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, s.allowed, nil
}

type discardHTTPResponseWriter struct {
	header http.Header
}

func (w *discardHTTPResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*discardHTTPResponseWriter) WriteHeader(int) {}

func (*discardHTTPResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}

func TestLingDocRealAPIThroughMainRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: filepath.Join(t.TempDir(), "api.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Error)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Authentication asynchronously updates API-key last_used_at. This fixture
	// tests sequential API semantics, not competing SQLite writers; serialize
	// its pool so that unrelated auth telemetry cannot invalidate a read-to-write
	// evidence transaction. Writer contention has dedicated production-port tests.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&types.User{}, &types.AuthToken{}, &types.TenantMember{}, &types.TenantAPIKey{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"000018_lingdoc_workspace.up.sql",
		"000019_lingdoc_evidence_assets.up.sql",
		"000020_lingdoc_candidate_adoption.up.sql",
		"000021_lingdoc_generation_runs.up.sql",
		"000022_lingdoc_generation_claim_token.up.sql",
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
		"000046_lingdoc_audit_action_bounds.up.sql",
		"000047_lingdoc_candidate_storage_shape.up.sql",
		"000048_lingdoc_selected_rewrite_action_shape.up.sql",
		"000049_lingdoc_generation_digest_version.up.sql",
		"000050_lingdoc_selected_rewrite_validity.up.sql",
		"000051_lingdoc_review_reports.up.sql",
		"000052_lingdoc_audit_version_fields.up.sql",
		"000053_lingdoc_project_archive.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		// Let SQLite parse the unchanged script, including semicolons inside
		// comments. database/sql does not inspect a comment-only final result.
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TABLE knowledges (id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT, title TEXT, parse_status TEXT, file_hash TEXT, file_size INTEGER, processed_at DATETIME, deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, parse_status, file_hash, file_size, processed_at) VALUES ('knowledge', 2, 'shared-kb', 'synthetic shared source', 'completed', 'hash-v1', 12, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, parse_status, file_hash, file_size, processed_at) VALUES ('knowledge-unassigned', 2, 'shared-kb', 'synthetic unassigned source', 'completed', 'hash-v2', 13, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	users := apprepo.NewUserRepository(db)
	tokens := apprepo.NewAuthTokenRepository(db)
	members := appservice.NewTenantMemberService(apprepo.NewTenantMemberRepository(db), nil, users, tokens)
	authUsers := appservice.NewUserService(cfg, users, tokens, integrationTenant{}, members, nil)
	jwtTokens := map[string]string{}
	for _, fixture := range []struct {
		id       string
		role     types.TenantRole
		tenantID uint64
	}{{"a-owner", types.TenantRoleOwner, 1}, {"b-viewer", types.TenantRoleViewer, 1}, {"c-outsider", types.TenantRoleContributor, 1}, {"d-member", types.TenantRoleContributor, 1}, {"e-inactive", types.TenantRoleContributor, 1}, {"f-cross-tenant", types.TenantRoleContributor, 2}, {"g-reviewer", types.TenantRoleContributor, 1}} {
		user := &types.User{ID: fixture.id, Username: fixture.id, Email: fixture.id + "@example.invalid", TenantID: fixture.tenantID, IsActive: true}
		if err := users.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		if _, err := members.AddMember(context.Background(), user.ID, fixture.tenantID, fixture.role, nil); err != nil {
			t.Fatal(err)
		}
		if fixture.id == "e-inactive" {
			if err := db.Model(&types.TenantMember{}).Where("tenant_id = ? AND user_id = ?", fixture.tenantID, user.ID).
				Update("status", types.TenantMemberStatusSuspended).Error; err != nil {
				t.Fatal(err)
			}
		}
		token, _, err := authUsers.GenerateTokens(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		jwtTokens[user.ID] = token
	}
	keys := appservice.NewTenantAPIKeyService(apprepo.NewTenantAPIKeyRepository(db))
	full, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1, Name: "synthetic full key", FullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1, Name: "synthetic chat key", Capabilities: []string{string(types.APIKeyCapabilityChat)}})
	if err != nil {
		t.Fatal(err)
	}
	projects := workspacecore.NewService(workspacecore.NewGORMRepository(db))
	shares := &integrationShares{allowed: true}
	ports := workspace.NewGORMSourcePorts(db, shares)
	sources := workspace.NewSourceService(projects, ports.Bindings, ports.Gateway, ports.Catalog, ports.Origins, shares, integrationKnowledge{})
	engine := gin.New()
	engine.Use(middleware.Auth(integrationTenant{}, authUsers, members, keys, cfg))
	v1 := engine.Group("/api/v1")
	guards := &rbacGuards{cfg: cfg, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	deps := workspace.HandlerDependencies{
		FullWorkflow: &workspace.FullWorkflowDependencies{
			Agent: workspace.AgentRoutePorts{
				Research: projects, Imports: projects, EvidenceCards: projects, Claims: projects,
				Anchors: projects, AgentSessions: projects, Proposals: projects, Context: projects, ChangeSets: projects,
			},
			Routes: workspace.FullWorkflowRoutePorts{Members: integrationMembershipService(projects, workspacecore.NewGORMRepository(db)), Reviews: projects, Lifecycle: projects},
			Impact: workspace.ImpactRoutePorts{ChangeSets: projects, Impact: projects, Dependency: projects, Runtime: projects, Trials: projects, Roadmap: projects},
		},
		Projects:    projects,
		Members:     integrationMembershipService(projects, workspacecore.NewGORMRepository(db)),
		Chapters:    projects,
		TemplateApp: projects,
		ChangeSets:  projects,
		Sources:     sources,
	}
	// The capabilities the project service provides are named by the caller.
	deps.WithMachineReads(projects)
	deps.WithReviewAnchors(projects, projects)
	lingdocHandler, err := workspace.NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, lingdocHandler)
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
	requestWithTB := func(tb testing.TB, method, path, body, key, identity string, apiKey bool, want int) *httptest.ResponseRecorder {
		tb.Helper()
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if apiKey {
			req.Header.Set("X-API-Key", identity)
		} else {
			req.Header.Set("Authorization", "Bearer "+identity)
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != want {
			tb.Fatalf("%s %s: %d %s, want %d", method, path, res.Code, res.Body.String(), want)
		}
		return res
	}
	request := func(method, path, body, key, identity string, apiKey bool, want int) *httptest.ResponseRecorder {
		return requestWithTB(t, method, path, body, key, identity, apiKey, want)
	}
	dropResponse := func(method, path, body, key, identity string, apiKey bool) {
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if apiKey {
			req.Header.Set("X-API-Key", identity)
		} else {
			req.Header.Set("Authorization", "Bearer "+identity)
		}
		engine.ServeHTTP(&discardHTTPResponseWriter{}, req)
	}
	created := request(http.MethodPost, "/projects", `{"name":"real API synthetic project","template_id":"template-demo"}`, "create-integration", jwtTokens["a-owner"], false, 201)
	var envelope struct{ Data workspacecore.Project }
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	id := envelope.Data.ID
	if id == "" {
		t.Fatal("missing server project ID")
	}
	path := "/projects/" + id
	request(http.MethodGet, path, "", "", jwtTokens["a-owner"], false, 200)
	request(http.MethodGet, path, "", "", jwtTokens["c-outsider"], false, 404)
	request(http.MethodPost, "/projects", `{"name":"denied","template_id":"template-demo"}`, "viewer-create", jwtTokens["b-viewer"], false, 403)
	request(http.MethodGet, path, "", "", "invalid-jwt", false, 401)
	request(http.MethodGet, path, "", "", scoped.Token, true, 403)
	request(http.MethodGet, path, "", "", full.Token, true, 404) // Native envelope alone grants no project delegation.
	input := `{"expected_spec_revision":0,"fields":{"research_subject":"synthetic","research_goal":"integration"}}`
	t.Run("response_lost_after_commit", func(t *testing.T) {
		// The handler commits and writes its response to a transport that drops it;
		// the caller then retries the same action key without seeing that response.
		dropResponse(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false)
		replayed := request(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false, 200)
		if !strings.Contains(replayed.Body.String(), `"replayed":true`) {
			t.Fatal("lost-response replay not reported")
		}
	})
	request(http.MethodPut, path+"/spec", input, "save-stale-integration", jwtTokens["a-owner"], false, 409)
	request(http.MethodPost, path+"/activate", `{"expected_spec_revision":1}`, "activate-integration", jwtTokens["a-owner"], false, 200)
	chapterEnvelope := struct {
		Data []workspacecore.Chapter `json:"data"`
	}{}
	chapterResponse := request(http.MethodGet, path+"/chapters", "", "", jwtTokens["a-owner"], false, 200)
	if err := json.Unmarshal(chapterResponse.Body.Bytes(), &chapterEnvelope); err != nil || len(chapterEnvelope.Data) != 2 {
		t.Fatalf("list chapters for working-copy flow: chapters=%d err=%v", len(chapterEnvelope.Data), err)
	}
	chapterID := chapterEnvelope.Data[0].ID
	chapterPath := path + "/chapters/" + chapterID
	copyResponse := request(http.MethodGet, chapterPath+"/working-copy", "", "", jwtTokens["a-owner"], false, 200)
	var initialCopy struct {
		Data workspacecore.WorkingCopy `json:"data"`
	}
	if err := json.Unmarshal(copyResponse.Body.Bytes(), &initialCopy); err != nil || initialCopy.Data.WorkingCopyRevision != 1 {
		t.Fatalf("read initial working copy: %+v err=%v", initialCopy.Data, err)
	}
	request(http.MethodPut, chapterPath+"/working-copy", `{"base_chapter_version_id":null,"expected_spec_revision":1,"expected_working_copy_revision":1,"body_markdown":"synthetic saved draft","source_ids":[]}`, "save-copy-integration", jwtTokens["a-owner"], false, 200)
	commitResponse := request(http.MethodPost, chapterPath+"/working-copy/commit", `{"expected_spec_revision":1,"expected_working_copy_revision":2,"expected_chapter_version_id":null}`, "commit-copy-integration", jwtTokens["a-owner"], false, 201)
	if !strings.Contains(commitResponse.Body.String(), `"body_markdown":"synthetic saved draft"`) {
		t.Fatalf("explicit commit did not persist the working-copy body: %s", commitResponse.Body.String())
	}
	request(http.MethodGet, chapterPath+"/versions", "", "", jwtTokens["a-owner"], false, 200)
	bound := request(http.MethodPost, path+"/assets", `{"knowledge_id":"knowledge"}`, "bind-integration", jwtTokens["a-owner"], false, 201)
	var asset struct{ Data struct{ ID string } }
	if err := json.Unmarshal(bound.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.Data.ID == "" {
		t.Fatal("binding missing real ID")
	}
	retrieval := `{"query":"synthetic","asset_ids":["` + asset.Data.ID + `"]}`
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, 200)
	t.Run("g5_invitation_and_member_lifecycle_http", func(t *testing.T) {
		request := func(method, path, body, key, identity string, apiKey bool, want int) *httptest.ResponseRecorder {
			t.Helper()
			return requestWithTB(t, method, path, body, key, identity, apiKey, want)
		}
		directory := request(http.MethodGet, path+"/members/directory", "", "", jwtTokens["a-owner"], false, 200)
		if !strings.Contains(directory.Body.String(), `"user_id":"d-member"`) {
			t.Fatalf("active same-tenant candidate is missing from the directory: %s", directory.Body.String())
		}
		if strings.Contains(directory.Body.String(), `"user_id":"e-inactive"`) || strings.Contains(directory.Body.String(), `"user_id":"f-cross-tenant"`) {
			t.Fatalf("inactive or cross-tenant candidates leaked into the member directory: %s", directory.Body.String())
		}

		readOwnerProject := func() workspacecore.Project {
			t.Helper()
			response := request(http.MethodGet, path, "", "", jwtTokens["a-owner"], false, 200)
			var result struct {
				Data workspacecore.Project `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			return result.Data
		}
		marshalInput := func(value any) string {
			t.Helper()
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
		assignmentFor := func(userID string) workspacecore.Member {
			t.Helper()
			return workspacecore.Member{
				UserID:         userID,
				GovernanceRole: "member",
				FunctionRoles:  []string{"researcher"},
				FunctionScopes: map[string][]string{"researcher": {"asset:" + asset.Data.ID}},
			}
		}

		for _, userID := range []string{"e-inactive", "f-cross-tenant"} {
			input := workspacecore.InviteMemberInput{
				ExpectedProjectVersion: readOwnerProject().ProjectVersion,
				Assignment:             assignmentFor(userID),
				Reason:                 "synthetic G5 candidate eligibility acceptance",
			}
			request(http.MethodPost, path+"/invitations", marshalInput(input), "invite-ineligible-"+userID, jwtTokens["a-owner"], false, http.StatusNotFound)
		}

		assignment := assignmentFor("d-member")
		invite := workspacecore.InviteMemberInput{
			ExpectedProjectVersion: readOwnerProject().ProjectVersion,
			Assignment:             assignment,
			Reason:                 "synthetic G5 HTTP lifecycle acceptance",
		}
		createdInvitation := request(http.MethodPost, path+"/invitations", marshalInput(invite), "invite-member-integration", jwtTokens["a-owner"], false, http.StatusCreated)
		var invitationEnvelope struct {
			Data workspacecore.MemberInvitation `json:"data"`
		}
		if err := json.Unmarshal(createdInvitation.Body.Bytes(), &invitationEnvelope); err != nil {
			t.Fatal(err)
		}
		invitation := invitationEnvelope.Data
		if invitation.ID == "" || invitation.Version != 1 || invitation.Status != "invited" ||
			invitation.ExpiresAt.Sub(invitation.CreatedAt) < 71*time.Hour || invitation.ExpiresAt.Sub(invitation.CreatedAt) > 73*time.Hour {
			t.Fatalf("invitation did not persist its initial three-day lifecycle: %+v", invitation)
		}

		inbox := request(http.MethodGet, "/project-invitations", "", "", jwtTokens["d-member"], false, http.StatusOK)
		if !strings.Contains(inbox.Body.String(), invitation.ID) {
			t.Fatalf("invitee cannot read their pending invitation: %s", inbox.Body.String())
		}

		invite.InvitationID = invitation.ID
		invite.ExpectedInvitationVersion = invitation.Version
		invite.ExpectedProjectVersion = readOwnerProject().ProjectVersion
		resent := request(http.MethodPost, path+"/invitations", marshalInput(invite), "resend-member-integration", jwtTokens["a-owner"], false, http.StatusCreated)
		if err := json.Unmarshal(resent.Body.Bytes(), &invitationEnvelope); err != nil {
			t.Fatal(err)
		}
		invitation = invitationEnvelope.Data
		if invitation.Version != 2 || invitation.Status != "invited" {
			t.Fatalf("resend did not advance the pending invitation version: %+v", invitation)
		}

		acceptPath := path + "/invitations/" + invitation.ID + "/accept"
		request(http.MethodPost, acceptPath, `{"expected_invitation_version":1}`, "accept-stale-invitation", jwtTokens["d-member"], false, http.StatusConflict)
		pending := request(http.MethodGet, path+"/invitations", "", "", jwtTokens["a-owner"], false, http.StatusOK)
		if !strings.Contains(pending.Body.String(), `"version":2`) || !strings.Contains(pending.Body.String(), `"status":"invited"`) {
			t.Fatalf("stale acceptance changed the current invitation: %s", pending.Body.String())
		}
		request(http.MethodPost, acceptPath, `{"expected_invitation_version":2}`, "accept-current-invitation", jwtTokens["d-member"], false, http.StatusOK)
		request(http.MethodPost, acceptPath, `{"expected_invitation_version":2}`, "accept-current-invitation", jwtTokens["d-member"], false, http.StatusOK)

		memberProject := request(http.MethodGet, path, "", "", jwtTokens["d-member"], false, http.StatusOK)
		var accepted struct {
			Data workspacecore.Project `json:"data"`
		}
		if err := json.Unmarshal(memberProject.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		memberFound := false
		for _, member := range accepted.Data.Members {
			if member.UserID == "d-member" {
				memberFound = member.Status == "active" && member.GovernanceRole == "member" &&
					strings.Contains(strings.Join(member.FunctionScopes["researcher"], ","), "asset:"+asset.Data.ID)
			}
		}
		if !memberFound {
			t.Fatalf("acceptance failed to activate the assigned member/scope: %+v", accepted.Data.Members)
		}

		assignmentInput := workspacecore.MemberAssignmentInput{
			ExpectedProjectVersion: accepted.Data.ProjectVersion,
			Assignment: workspacecore.Member{
				UserID:         "d-member",
				Role:           "collaborator",
				GovernanceRole: "admin",
				FunctionRoles:  []string{"author", "researcher"},
				FunctionScopes: map[string][]string{
					"author":     {chapterID},
					"researcher": {"asset:" + asset.Data.ID},
				},
			},
			Reason: "synthetic G5 role and scope HTTP acceptance",
		}
		staleAssignment := assignmentInput
		staleAssignment.ExpectedProjectVersion--
		request(http.MethodPut, path+"/members/d-member/assignment", marshalInput(staleAssignment), "stale-member-assignment", jwtTokens["a-owner"], false, http.StatusConflict)
		unchangedProject := readOwnerProject()
		unchangedAssignment := false
		for _, member := range unchangedProject.Members {
			if member.UserID == "d-member" {
				unchangedAssignment = member.GovernanceRole == "member" && len(member.FunctionRoles) == 1 && member.FunctionRoles[0] == "researcher" && unchangedProject.ProjectVersion == accepted.Data.ProjectVersion
			}
		}
		if !unchangedAssignment {
			t.Fatalf("stale assignment changed role, scope or project version: %+v", unchangedProject)
		}

		assignmentInput.ExpectedProjectVersion = unchangedProject.ProjectVersion
		assigned := request(http.MethodPut, path+"/members/d-member/assignment", marshalInput(assignmentInput), "current-member-assignment", jwtTokens["a-owner"], false, http.StatusOK)
		var assignedEnvelope struct {
			Data workspacecore.Project `json:"data"`
		}
		if err := json.Unmarshal(assigned.Body.Bytes(), &assignedEnvelope); err != nil {
			t.Fatal(err)
		}
		updatedAssignment := false
		for _, member := range assignedEnvelope.Data.Members {
			if member.UserID == "d-member" {
				updatedAssignment = member.GovernanceRole == "admin" &&
					strings.Contains(strings.Join(member.FunctionRoles, ","), "author") &&
					strings.Contains(strings.Join(member.FunctionRoles, ","), "researcher") &&
					strings.Contains(strings.Join(member.FunctionScopes["author"], ","), chapterID) &&
					strings.Contains(strings.Join(member.FunctionScopes["researcher"], ","), "asset:"+asset.Data.ID)
			}
		}
		if !updatedAssignment || assignedEnvelope.Data.ProjectVersion != unchangedProject.ProjectVersion+1 {
			t.Fatalf("current assignment did not persist both role axes/scopes with one version advance: %+v", assignedEnvelope.Data)
		}
		checkPermission := func(capability string, allowed bool) {
			t.Helper()
			response := request(http.MethodGet, path+"/permissions?capability="+capability, "", "", jwtTokens["d-member"], false, http.StatusOK)
			var result struct {
				Data workspacecore.EffectivePermission `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.Allowed != allowed {
				t.Fatalf("capability %q allowed=%v, want %v: %s", capability, result.Data.Allowed, allowed, response.Body.String())
			}
		}
		checkPermission("manage", true)
		checkPermission("write:"+chapterID, true)
		checkPermission("research", true)
		checkPermission("confirm:"+chapterID, false)
		reviewerInvite := workspacecore.InviteMemberInput{
			ExpectedProjectVersion: readOwnerProject().ProjectVersion,
			Assignment: workspacecore.Member{
				UserID: "g-reviewer", GovernanceRole: "member", FunctionRoles: []string{"reviewer"},
				FunctionScopes: map[string][]string{"reviewer": {chapterID}},
			},
			Reason: "synthetic G5 reviewer chapter-scope acceptance",
		}
		reviewerCreated := request(http.MethodPost, path+"/invitations", marshalInput(reviewerInvite), "invite-reviewer-integration", jwtTokens["d-member"], false, http.StatusCreated)
		var reviewerInvitation struct {
			Data workspacecore.MemberInvitation `json:"data"`
		}
		if err := json.Unmarshal(reviewerCreated.Body.Bytes(), &reviewerInvitation); err != nil || reviewerInvitation.Data.ID == "" {
			t.Fatalf("parse reviewer invitation: invitation=%+v err=%v", reviewerInvitation.Data, err)
		}
		request(http.MethodPost, path+"/invitations/"+reviewerInvitation.Data.ID+"/accept", `{"expected_invitation_version":1}`, "accept-reviewer-integration", jwtTokens["g-reviewer"], false, http.StatusOK)
		checkReviewerPermission := func(capability string, allowed bool) {
			t.Helper()
			response := request(http.MethodGet, path+"/permissions?capability="+capability, "", "", jwtTokens["g-reviewer"], false, http.StatusOK)
			var result struct {
				Data workspacecore.EffectivePermission `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.Allowed != allowed {
				t.Fatalf("reviewer capability %q allowed=%v, want %v: %s", capability, result.Data.Allowed, allowed, response.Body.String())
			}
		}
		checkReviewerPermission("confirm:"+chapterID, true)
		checkReviewerPermission("write:"+chapterID, false)
		checkReviewerPermission("research", false)
		request(http.MethodPost, path+"/retrieval", `{"query":"synthetic","asset_ids":["`+asset.Data.ID+`"]}`, "", jwtTokens["g-reviewer"], false, http.StatusForbidden)
		unassignedAssetResponse := request(http.MethodPost, path+"/assets", `{"knowledge_id":"knowledge-unassigned"}`, "bind-unassigned-asset", jwtTokens["a-owner"], false, http.StatusCreated)
		var unassignedAsset struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(unassignedAssetResponse.Body.Bytes(), &unassignedAsset); err != nil || unassignedAsset.Data.ID == "" {
			t.Fatalf("bind unassigned synthetic source: asset=%+v err=%v", unassignedAsset.Data, err)
		}
		assetList := request(http.MethodGet, path+"/assets", "", "", jwtTokens["d-member"], false, http.StatusOK)
		var visibleAssets struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(assetList.Body.Bytes(), &visibleAssets); err != nil {
			t.Fatal(err)
		}
		listedIDs := map[string]bool{}
		for _, visible := range visibleAssets.Data {
			listedIDs[visible.ID] = true
		}
		if len(visibleAssets.Data) != 2 || !listedIDs[asset.Data.ID] || !listedIDs[unassignedAsset.Data.ID] {
			t.Fatalf("active project member read-only asset list = %+v, want both current project assets", visibleAssets.Data)
		}
		deniedRetrieval := request(http.MethodPost, path+"/retrieval", `{"query":"synthetic","asset_ids":["`+unassignedAsset.Data.ID+`"]}`, "", jwtTokens["d-member"], false, http.StatusForbidden)
		// An Admin + Author + Researcher cannot turn a valid chapter ID into
		// project-wide material access. This crosses real JWT, current member
		// policy, typed batch port, binding store and the HTTP denial contract.
		request(http.MethodPost, path+"/retrieval", `{"query":"synthetic","asset_ids":["`+unassignedAsset.Data.ID+`"],"chapter_id":"`+chapterID+`"}`, "", jwtTokens["d-member"], false, http.StatusForbidden)
		var deniedEnvelope struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					Denied []struct {
						AssetID string `json:"asset_id"`
						Reason  string `json:"reason"`
					} `json:"denied"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(deniedRetrieval.Body.Bytes(), &deniedEnvelope); err != nil || deniedEnvelope.Error.Code != "asset_not_authorized" || len(deniedEnvelope.Error.Details.Denied) != 1 || deniedEnvelope.Error.Details.Denied[0].AssetID != unassignedAsset.Data.ID || deniedEnvelope.Error.Details.Denied[0].Reason != "not_authorized" {
			t.Fatalf("researcher asset scope denial = %s (err=%v)", deniedRetrieval.Body.String(), err)
		}
		adminProject := readOwnerProject()
		adminInvite := workspacecore.InviteMemberInput{
			ExpectedProjectVersion: adminProject.ProjectVersion,
			Assignment: workspacecore.Member{
				UserID: "c-outsider", GovernanceRole: "member", FunctionRoles: []string{"observer"},
				FunctionScopes: map[string][]string{"observer": {}},
			},
			Reason: "synthetic G5 admin invitation acceptance",
		}
		observerCreated := request(http.MethodPost, path+"/invitations", marshalInput(adminInvite), "admin-invite-member-integration", jwtTokens["d-member"], false, http.StatusCreated)
		var observerInvitation struct {
			Data workspacecore.MemberInvitation `json:"data"`
		}
		if err := json.Unmarshal(observerCreated.Body.Bytes(), &observerInvitation); err != nil || observerInvitation.Data.ID == "" {
			t.Fatalf("parse observer invitation: invitation=%+v err=%v", observerInvitation.Data, err)
		}
		request(http.MethodPost, path+"/invitations/"+observerInvitation.Data.ID+"/accept", `{"expected_invitation_version":1}`, "accept-observer-invitation", jwtTokens["c-outsider"], false, http.StatusOK)
		request(http.MethodGet, path, "", "", jwtTokens["c-outsider"], false, http.StatusOK)
		observerAssets := request(http.MethodGet, path+"/assets", "", "", jwtTokens["c-outsider"], false, http.StatusOK)
		if !strings.Contains(observerAssets.Body.String(), asset.Data.ID) || !strings.Contains(observerAssets.Body.String(), unassignedAsset.Data.ID) {
			t.Fatalf("observer cannot read current project asset list: %s", observerAssets.Body.String())
		}
		observerPermission := request(http.MethodGet, path+"/permissions?capability=research", "", "", jwtTokens["c-outsider"], false, http.StatusOK)
		if !strings.Contains(observerPermission.Body.String(), `"allowed":false`) {
			t.Fatalf("observer unexpectedly has research permission: %s", observerPermission.Body.String())
		}
		request(http.MethodPost, path+"/retrieval", `{"query":"synthetic","asset_ids":["`+asset.Data.ID+`"]}`, "", jwtTokens["c-outsider"], false, http.StatusForbidden)

		request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["d-member"], false, http.StatusOK)
		changeState := func(action string) {
			t.Helper()
			input := workspacecore.MemberLifecycleInput{
				ExpectedProjectVersion: readOwnerProject().ProjectVersion,
				Action:                 action,
				Reason:                 "synthetic G5 HTTP lifecycle acceptance",
			}
			request(http.MethodPost, path+"/members/d-member/state", marshalInput(input), "member-state-"+action, jwtTokens["a-owner"], false, http.StatusOK)
		}
		memberState := func(userID string) workspacecore.Member {
			t.Helper()
			project := readOwnerProject()
			for _, member := range project.Members {
				if member.UserID == userID {
					return member
				}
			}
			t.Fatalf("project member %q is missing", userID)
			return workspacecore.Member{}
		}

		// A project-side suspension must remain a project-side decision across
		// tenant removal/rejoin; tenant reactivation cannot restore its scope.
		changeState("suspend")
		suspendedBeforeTenantRemoval := memberState("d-member")
		if suspendedBeforeTenantRemoval.Status != "suspended" {
			t.Fatalf("project suspension did not persist before tenant removal: %+v", suspendedBeforeTenantRemoval)
		}
		// Exercise the real tenant-membership removal adapter. It must not
		// overwrite the already-suspended project membership.
		if err := members.RemoveMember(context.Background(), "d-member", 1); err != nil {
			t.Fatalf("remove tenant membership: %v", err)
		}
		memberAfterTenantRemoval := memberState("d-member")
		if memberAfterTenantRemoval.Status != "suspended" {
			t.Fatalf("tenant removal overwrote project suspension: %+v", memberAfterTenantRemoval)
		}
		request(http.MethodGet, path, "", "", jwtTokens["d-member"], false, http.StatusUnauthorized)
		if _, err := members.AddMember(context.Background(), "d-member", 1, types.TenantRoleContributor, nil); err != nil {
			t.Fatalf("restore tenant membership: %v", err)
		}
		rejoined, err := users.GetUserByID(context.Background(), "d-member")
		if err != nil || rejoined == nil {
			t.Fatalf("load rejoined user: user=%v err=%v", rejoined, err)
		}
		rejoined.TenantID = 1
		if err := users.UpdateUser(context.Background(), rejoined); err != nil {
			t.Fatalf("update rejoined user's active tenant: %v", err)
		}
		rejoinedToken, _, err := authUsers.GenerateTokens(context.Background(), rejoined)
		if err != nil {
			t.Fatalf("issue token after tenant rejoin: %v", err)
		}
		if rejoinedToken == jwtTokens["d-member"] {
			t.Fatal("token reissued after tenant rejoin must differ from the revoked JWT")
		}
		if _, _, err := authUsers.ValidateToken(context.Background(), rejoinedToken); err != nil {
			t.Fatalf("validate token after tenant rejoin: %v", err)
		}
		jwtTokens["d-member"] = rejoinedToken
		request(http.MethodGet, path, "", "", rejoinedToken, false, http.StatusNotFound)
		if got := memberState("d-member"); got.Status != "suspended" || strings.Join(got.FunctionScopes["researcher"], ",") != strings.Join(suspendedBeforeTenantRemoval.FunctionScopes["researcher"], ",") {
			t.Fatalf("tenant rejoin overwrote project suspension or scopes: before=%+v after=%+v", suspendedBeforeTenantRemoval, got)
		}
		changeState("restore")
		request(http.MethodGet, path, "", "", rejoinedToken, false, http.StatusOK)

		// An active project member removed from the tenant is suspended by the
		// tenant adapter and requires explicit restore_tenant after rejoining.
		if err := members.RemoveMember(context.Background(), "d-member", 1); err != nil {
			t.Fatalf("remove active tenant membership: %v", err)
		}
		tenantSuspended := memberState("d-member")
		if tenantSuspended.Status != "suspended_by_tenant" {
			t.Fatalf("tenant membership removal did not suspend active project membership: %+v", tenantSuspended)
		}
		request(http.MethodGet, path, "", "", jwtTokens["d-member"], false, http.StatusUnauthorized)
		if _, err := members.AddMember(context.Background(), "d-member", 1, types.TenantRoleContributor, nil); err != nil {
			t.Fatalf("rejoin tenant before tenant-scoped restore: %v", err)
		}
		rejoined, err = users.GetUserByID(context.Background(), "d-member")
		if err != nil || rejoined == nil {
			t.Fatalf("reload user after tenant rejoin: user=%v err=%v", rejoined, err)
		}
		rejoined.TenantID = 1
		if err := users.UpdateUser(context.Background(), rejoined); err != nil {
			t.Fatalf("update active tenant after second rejoin: %v", err)
		}
		rejoinedToken, _, err = authUsers.GenerateTokens(context.Background(), rejoined)
		if err != nil {
			t.Fatalf("issue token after second tenant rejoin: %v", err)
		}
		if _, _, err := authUsers.ValidateToken(context.Background(), rejoinedToken); err != nil {
			t.Fatalf("validate token after second tenant rejoin: %v", err)
		}
		jwtTokens["d-member"] = rejoinedToken
		request(http.MethodGet, path, "", "", rejoinedToken, false, http.StatusNotFound)
		changeState("restore_tenant")
		request(http.MethodGet, path, "", "", jwtTokens["d-member"], false, http.StatusOK)
		request(http.MethodPost, path+"/retrieval", retrieval, "", rejoinedToken, false, http.StatusOK)
		changeState("remove")
		request(http.MethodGet, path, "", "", jwtTokens["d-member"], false, http.StatusNotFound)
		removedProject := readOwnerProject()
		for _, member := range removedProject.Members {
			if member.UserID == "d-member" && (member.Status != "removed" || len(member.FunctionScopes) != 0) {
				t.Fatalf("removal left access scopes active: %+v", member)
			}
		}
		// A project-side removal and its revoked scopes must survive a subsequent
		// tenant removal/rejoin cycle; tenant reactivation cannot re-add the member.
		if err := members.RemoveMember(context.Background(), "d-member", 1); err != nil {
			t.Fatalf("remove tenant membership for project-removed member: %v", err)
		}
		if _, err := members.AddMember(context.Background(), "d-member", 1, types.TenantRoleContributor, nil); err != nil {
			t.Fatalf("restore tenant membership for project-removed member: %v", err)
		}
		removedUser, err := users.GetUserByID(context.Background(), "d-member")
		if err != nil || removedUser == nil {
			t.Fatalf("reload user after tenant rejoin for removed project member: user=%v err=%v", removedUser, err)
		}
		removedUser.TenantID = 1
		if err := users.UpdateUser(context.Background(), removedUser); err != nil {
			t.Fatalf("update tenant after rejoining for removed project member: %v", err)
		}
		removedToken, _, err := authUsers.GenerateTokens(context.Background(), removedUser)
		if err != nil {
			t.Fatalf("issue token after tenant rejoin for removed project member: %v", err)
		}
		if _, _, err := authUsers.ValidateToken(context.Background(), removedToken); err != nil {
			t.Fatalf("validate token after tenant rejoin for removed project member: %v", err)
		}
		jwtTokens["d-member"] = removedToken
		request(http.MethodGet, path, "", "", removedToken, false, http.StatusNotFound)
		removedAfterTenantRejoin := memberState("d-member")
		if removedAfterTenantRejoin.Status != "removed" || len(removedAfterTenantRejoin.FunctionScopes) != 0 {
			t.Fatalf("tenant rejoin restored project-removed member or revoked scopes: %+v", removedAfterTenantRejoin)
		}

		expiredInput := workspacecore.InviteMemberInput{
			ExpectedProjectVersion: removedProject.ProjectVersion,
			Assignment:             assignment,
			Reason:                 "synthetic G5 expired invitation acceptance",
		}
		createdExpired := request(http.MethodPost, path+"/invitations", marshalInput(expiredInput), "create-expired-invitation", jwtTokens["a-owner"], false, http.StatusCreated)
		var expiredEnvelope struct {
			Data workspacecore.MemberInvitation `json:"data"`
		}
		if err := json.Unmarshal(createdExpired.Body.Bytes(), &expiredEnvelope); err != nil {
			t.Fatal(err)
		}
		expiredInvitation := expiredEnvelope.Data
		// This is a constructed expired record fixture, not a real-time clock
		// advancement or wall-clock expiry end-to-end test.
		expiredInvitation.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		expiredPayload := marshalInput(expiredInvitation)
		update := db.Table("lingdoc_g5_records").
			Where("project_id = ? AND kind = ? AND id = ?", removedProject.ID, "invitation", expiredInvitation.ID).
			Update("payload_json", expiredPayload)
		if update.Error != nil || update.RowsAffected != 1 {
			t.Fatalf("expire synthetic invitation: rows=%d err=%v", update.RowsAffected, update.Error)
		}
		projectInvitations := request(http.MethodGet, path+"/invitations", "", "", jwtTokens["a-owner"], false, http.StatusOK)
		if !strings.Contains(projectInvitations.Body.String(), expiredInvitation.ID) || !strings.Contains(projectInvitations.Body.String(), `"status":"expired"`) {
			t.Fatalf("expired invitation status was not visible to the project manager: %s", projectInvitations.Body.String())
		}
		expiredAcceptPath := path + "/invitations/" + expiredInvitation.ID + "/accept"
		request(http.MethodPost, expiredAcceptPath, `{"expected_invitation_version":1}`, "accept-expired-invitation", jwtTokens["d-member"], false, http.StatusUnprocessableEntity)
		finalProject := readOwnerProject()
		expiredMemberPreserved := false
		for _, member := range finalProject.Members {
			if member.UserID == "d-member" {
				expiredMemberPreserved = member.Status == "invited" && len(member.FunctionScopes) > 0
			}
		}
		if !expiredMemberPreserved {
			t.Fatalf("rejected expired acceptance changed the pending invitation/member state: %+v", finalProject.Members)
		}
	})
	shares.allowed = false
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, http.StatusForbidden)                  // KB-share authorization is an explicit permission denial.
	request(http.MethodPost, path+"/assets", `{"knowledge_id":"knowledge"}`, "bind-integration", jwtTokens["a-owner"], false, 404) // Reauthorization before replay.
	shares.allowed = true
	if err := db.Exec("UPDATE knowledges SET parse_status='processing' WHERE id='knowledge'").Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, 422)
	if err := db.Exec("DELETE FROM lingdoc_members WHERE project_id=? AND user_id='a-owner'", id).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false, 404)
	request(http.MethodGet, path, "", "", full.Token, true, 404) // FullAccess is not project membership.
	var count int64
	if err := db.Table("lingdoc_projects").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("denied write had side effects: %d %v", count, err)
	}
}

// Actual native key authentication and route policies are used here. Only the
// external tenant/KB-share lookups are explicit fixtures; no human Caller is
// inserted for the machine, and the production source bridge checks real rows.
func TestLingDocBoundedNativeMachineRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite",
		DSN: filepath.Join(t.TempDir(), "machine-read.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"},
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
	if err := db.AutoMigrate(&types.User{}, &types.AuthToken{}, &types.TenantMember{},
		&types.TenantAPIKey{}, &types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{}); err != nil {
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
		"000052_lingdoc_audit_version_fields.up.sql",
	} {
		script, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(context.Background(), string(script)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	users := apprepo.NewUserRepository(db)
	tokens := apprepo.NewAuthTokenRepository(db)
	members := appservice.NewTenantMemberService(apprepo.NewTenantMemberRepository(db), nil, users, tokens)
	authUsers := appservice.NewUserService(cfg, users, tokens, integrationTenant{}, members, nil)
	owner := &types.User{ID: "machine-grantor", Username: "machine-grantor", Email: "machine-grantor@example.invalid", TenantID: 1, IsActive: true}
	if err := users.CreateUser(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := members.AddMember(context.Background(), owner.ID, 1, types.TenantRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	token, _, err := authUsers.GenerateTokens(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	keys := appservice.NewTenantAPIKeyService(apprepo.NewTenantAPIKeyRepository(db))
	bounded, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1,
		Name: "synthetic bounded retrieve", Capabilities: []string{string(types.APIKeyCapabilityRetrieve)},
		KnowledgeBaseIDs: []string{"shared-kb"}})
	if err != nil {
		t.Fatal(err)
	}
	full, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1,
		Name: "synthetic full envelope", FullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	shares := &integrationShares{allowed: true}
	runtime := workspace.NewGORMSourceRuntime(db, workspace.ProjectPorts{}, shares, integrationKnowledge{}, delivery.NewFixedTemplateReader())
	projects := workspacecore.NewServiceWithSources(workspacecore.NewGORMRepository(db), workspacecore.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(workspace.ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	runtime.EnableFullWorkflowPolicy()
	engine := gin.New()
	engine.Use(middleware.Auth(integrationTenant{}, authUsers, members, keys, cfg))
	var afterNativeAuth func()
	engine.Use(func(c *gin.Context) {
		if _, machine := types.TenantAPIKeyScopeFromContext(c.Request.Context()); machine && afterNativeAuth != nil {
			hook := afterNativeAuth
			afterNativeAuth = nil
			hook()
		}
	})
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
	// The capabilities the project service provides are named by the caller.
	deps.WithMachineReads(projects)
	deps.WithReviewAnchors(projects, projects)
	lingdocHandler, err := workspace.NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, lingdocHandler)
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
	request := func(runT *testing.T, method, path string, body any, key, credential string, machine bool, want int) *httptest.ResponseRecorder {
		runT.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				runT.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if machine {
			req.Header.Set("X-API-Key", credential)
		} else {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != want {
			runT.Fatalf("%s %s: got %d %s want %d", method, path, response.Code, response.Body.String(), want)
		}
		return response
	}
	projectData := func(response *httptest.ResponseRecorder) workspacecore.Project {
		t.Helper()
		var envelope struct {
			Data workspacecore.Project `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	p := projectData(request(t, http.MethodPost, "/projects", workspacecore.CreateProjectInput{Name: "synthetic machine project",
		TemplateID: "template-demo"}, "machine-http-create", token, false, 201))
	path := "/projects/" + p.ID
	p = projectData(request(t, http.MethodPut, path+"/spec", workspacecore.SaveSpecInput{ExpectedSpecRevision: 0,
		Fields: map[string]string{"research_subject": "private synthetic Spec", "research_goal": "machine integration"}},
		"machine-http-spec", token, false, 200))
	p = projectData(request(t, http.MethodPost, path+"/activate", workspacecore.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision,
		ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion}, "machine-http-activate", token, false, 200))
	var chapterList struct {
		Data []workspacecore.Chapter `json:"data"`
	}
	if err := json.Unmarshal(request(t, http.MethodGet, path+"/chapters", nil, "", token, false, 200).Body.Bytes(), &chapterList); err != nil || len(chapterList.Data) == 0 {
		t.Fatalf("chapter fixture: %v %v", chapterList, err)
	}
	chapterID := chapterList.Data[0].ID
	chapterPath := path + "/chapters/" + chapterID
	now := time.Now().UTC()
	if err := db.Create(&types.KnowledgeBase{ID: "shared-kb", TenantID: 2, Name: "synthetic shared KB"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&types.Knowledge{ID: "machine-knowledge", TenantID: 2, KnowledgeBaseID: "shared-kb",
		Title: "synthetic shared source", ParseStatus: "completed", FileHash: "machine-source-fingerprint",
		FileSize: 17, ProcessedAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	const excerpt = "synthetic excerpt"
	if err := db.Create(&types.Chunk{ID: "machine-source", TenantID: 2, KnowledgeBaseID: "shared-kb",
		KnowledgeID: "machine-knowledge", ChunkType: types.ChunkTypeText, ChunkIndex: 0,
		Content: excerpt, StartAt: 0, EndAt: len(excerpt), IsEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	request(t, http.MethodPost, path+"/assets", map[string]string{"knowledge_id": "machine-knowledge"},
		"machine-http-bind-source", token, false, 201)
	const sentinel = "MACHINE_CHAPTER_BODY_SENTINEL_77"
	request(t, http.MethodPost, chapterPath+"/versions", workspacecore.SaveChapterInput{ExpectedSpecRevision: p.SpecRevision,
		BodyMarkdown: sentinel + " [[source:machine-source]]", SourceIDs: []string{"machine-source"}},
		"machine-http-save-chapter", token, false, 201)
	p = projectData(request(t, http.MethodGet, path, nil, "", token, false, 200))
	summaryPath := path + "/machine-read"
	machineChapterPath := chapterPath + "/machine-read"
	request(t, http.MethodGet, summaryPath, nil, "", bounded.Token, true, 404) // No project grant.
	grant := func(native *interfaces.TenantAPIKeyCreateResult, active bool, key string) {
		t.Helper()
		current := projectData(request(t, http.MethodGet, path, nil, "", token, false, 200))
		request(t, http.MethodPut, path+"/api-key-grants", workspacecore.ProjectAPIKeyGrant{KeyID: native.APIKey.ID,
			Capabilities: []string{"read"}, ExpectedProjectVersion: current.ProjectVersion, Active: active,
			Reason: "synthetic explicit read delegation", CreatedBy: "client-spoof-must-be-ignored"},
			key, token, false, 200)
	}
	grant(bounded, true, "machine-http-read-grant")
	summary := request(t, http.MethodGet, summaryPath, nil, "", bounded.Token, true, 200)
	if strings.Contains(summary.Body.String(), "private synthetic Spec") || strings.Contains(summary.Body.String(), "members") {
		t.Fatalf("summary leaked Spec/governance: %s", summary.Body.String())
	}
	body := request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 200)
	if !strings.Contains(body.Body.String(), sentinel) {
		t.Fatalf("source-checked chapter missing: %s", body.Body.String())
	}
	for _, forbidden := range []string{"function_scopes", "grantor_id", "review_items"} {
		if strings.Contains(body.Body.String(), forbidden) {
			t.Fatalf("chapter leaked extra field %s", forbidden)
		}
	}
	request(t, http.MethodGet, "/projects/unknown/machine-read", nil, "", bounded.Token, true, 404)
	request(t, http.MethodGet, "/projects", nil, "", bounded.Token, true, 403)
	request(t, http.MethodPut, path+"/spec", workspacecore.SaveSpecInput{Fields: map[string]string{"research_subject": "forbidden"}},
		"machine-http-denied-write", bounded.Token, true, 403)
	request(t, http.MethodGet, path+"/permissions", nil, "", bounded.Token, true, 403) // Undeclared additive path.
	grant(full, true, "machine-http-full-read-grant")
	request(t, http.MethodGet, summaryPath, nil, "", full.Token, true, 200)
	humanDenied := request(t, http.MethodPost, path+"/activate", workspacecore.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision},
		"machine-http-full-human-denied", full.Token, true, 403)
	if !strings.Contains(humanDenied.Body.String(), "human_decision_required") {
		t.Fatalf("full key human guard: %s", humanDenied.Body.String())
	}

	updateEnvelope := func(runT *testing.T, kbIDs, capabilities []string) {
		runT.Helper()
		if _, err := keys.UpdateAPIKey(context.Background(), interfaces.TenantAPIKeyUpdateRequest{
			TenantID: 1, APIKeyID: bounded.APIKey.ID, Name: "synthetic bounded retrieve",
			KnowledgeBaseIDs: kbIDs, Capabilities: capabilities,
		}); err != nil {
			runT.Fatal(err)
		}
		// Verify the real authentication projection, rather than assuming that
		// an SQL JSON literal is decoded identically to the native Valuer.
		mapped, err := keys.AuthenticateAPIKey(context.Background(), bounded.Token)
		if err != nil {
			runT.Fatal(err)
		}
		if len(mapped.KnowledgeBaseIDs) != len(kbIDs) || len(mapped.Capabilities) != len(capabilities) {
			runT.Fatalf("native envelope mapping: KBs=%v caps=%v", mapped.KnowledgeBaseIDs, mapped.Capabilities)
		}
		for _, id := range kbIDs {
			found := false
			for _, current := range mapped.KnowledgeBaseIDs {
				if current == id {
					found = true
				}
			}
			if !found {
				runT.Fatalf("fresh native authentication lost KB %q", id)
			}
		}
		for _, cap := range capabilities {
			if !(types.TenantAPIKeyScope{Capabilities: mapped.Capabilities, FullAccess: mapped.FullAccess}).HasCapability(types.APIKeyCapability(cap)) {
				runT.Fatalf("fresh native authentication lost capability %q", cap)
			}
		}
	}

	for _, change := range []string{"kb-envelope", "share-revoked", "grantor-role", "grantor-member", "grantor-user"} {
		t.Run(change, func(t *testing.T) {
			switch change {
			case "kb-envelope":
				updateEnvelope(t, []string{"other-kb"}, []string{string(types.APIKeyCapabilityRetrieve)})
			case "share-revoked":
				shares.allowed = false
			case "grantor-role":
				if err := db.Exec("UPDATE tenant_members SET role='viewer' WHERE user_id=?", owner.ID).Error; err != nil {
					t.Fatal(err)
				}
			case "grantor-member":
				if err := db.Exec("UPDATE lingdoc_member_permissions SET status='removed' WHERE project_id=? AND user_id=?", p.ID, owner.ID).Error; err != nil {
					t.Fatal(err)
				}
			case "grantor-user":
				if err := db.Exec("UPDATE users SET is_active=0 WHERE id=?", owner.ID).Error; err != nil {
					t.Fatal(err)
				}
			}
			denied := request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 404)
			if strings.Contains(denied.Body.String(), sentinel) {
				t.Fatal("revocation leaked cached chapter")
			}
			shares.allowed = true
			updateEnvelope(t, []string{"shared-kb"}, []string{string(types.APIKeyCapabilityRetrieve)})
			if err := db.Exec("UPDATE tenant_members SET role='owner' WHERE user_id=?", owner.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE lingdoc_member_permissions SET status='active' WHERE project_id=? AND user_id=?", p.ID, owner.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE users SET is_active=1 WHERE id=?", owner.ID).Error; err != nil {
				t.Fatal(err)
			}
			request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 200)
		})
	}

	t.Run("post-auth-native-expansion-does-not-widen-request", func(t *testing.T) {
		updateEnvelope(t, []string{"other-kb"}, []string{string(types.APIKeyCapabilityRetrieve)})
		afterNativeAuth = func() {
			updateEnvelope(t, []string{"shared-kb"}, []string{string(types.APIKeyCapabilityRetrieve)})
		}
		// Authentication captured other-kb before the hook widens the DB row.
		// The old request remains denied, and only a fresh auth sees shared-kb.
		response := request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 404)
		if strings.Contains(response.Body.String(), sentinel) {
			t.Fatal("post-auth expansion leaked source-bearing chapter")
		}
		request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 200)
	})

	grant(bounded, false, "machine-http-revoke-grant")
	request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 404)
	grant(bounded, true, "machine-http-regrant")
	updateEnvelope(t, []string{"shared-kb"}, []string{string(types.APIKeyCapabilityChat)})
	request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 403)
	updateEnvelope(t, []string{"shared-kb"}, []string{string(types.APIKeyCapabilityRetrieve)})
	if err := keys.RevokeAPIKey(context.Background(), 1, bounded.APIKey.ID); err != nil {
		t.Fatal(err)
	}
	request(t, http.MethodGet, machineChapterPath, nil, "", bounded.Token, true, 401)
	var audits []struct {
		ActorID     string
		DetailsJSON string
	}
	if err := db.Table("lingdoc_project_audits").Select("actor_id,details_json").Where("project_id=? AND action='machine.read'", p.ID).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 {
		t.Fatal("machine audit missing")
	}
	for _, audit := range audits {
		if audit.ActorID != "api_tenant:1" || strings.Contains(audit.DetailsJSON, sentinel) || strings.Contains(audit.DetailsJSON, "private synthetic Spec") {
			t.Fatalf("machine audit forged human or retained content: %+v", audit)
		}
	}
}

// integrationMembershipService builds the member and owner service for the
// integration fixtures.
//
// The member context left the aggregate: it runs its writes through the shared
// pipeline and reads through narrow ports, and it never holds the aggregate
// itself. The composition root is the only place that knows how those ports
// are built.
func integrationMembershipService(
	projects *workspacecore.Service,
	repository workspacecore.Repository,
) *membership.Service {
	storage := workspacecore.NewMemberStorage(projects)
	return membership.NewService(membership.Ports{
		Runner:     workspacecore.NewOperationRunner(projects),
		Authorizer: workspacecore.NewWorkspaceAuthorizer(projects),
		Policy:     workspacecore.NewWorkspacePolicy(projects),
		Repository: repository,
		Records:    storage,
		Directory:  storage,
		Transfers:  storage,
		APIKeys:    storage,
		Resources:  storage,
	})
}
