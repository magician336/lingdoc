package rulesetstore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/gin-gonic/gin"
)

type testIdentityKey struct{}

type testIdentity struct {
	actor     Actor
	requestID string
	valid     bool
}

func testRequestInfo(c *gin.Context) (Actor, string, bool) {
	identity, ok := c.Request.Context().Value(testIdentityKey{}).(testIdentity)
	return identity.actor, identity.requestID, ok && identity.valid
}

func testIdentityContext(user, role string) context.Context {
	return context.WithValue(context.Background(), testIdentityKey{}, testIdentity{
		actor: Actor{TenantID: 1, UserID: user, Role: role}, requestID: "test-request", valid: user != "",
	})
}

func TestHTTPRuleSetGovernanceLifecycleAndAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openStoreTestDB(t, true)
	if err := db.Exec(`CREATE TABLE tenant_members (
		user_id TEXT NOT NULL, tenant_id INTEGER NOT NULL, role TEXT NOT NULL,
		status TEXT NOT NULL, deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_members(user_id,tenant_id,role,status) VALUES
		('owner',1,'owner','active'), ('admin',1,'admin','active'), ('viewer',1,'viewer','active')`).Error; err != nil {
		t.Fatal(err)
	}
	service := NewStore(db, TenantMembershipAuthorizer{})
	router := gin.New()
	group := router.Group("/api/v1/lingdoc")
	NewHTTPHandler(service, testRequestInfo).Register(group, group)

	request := func(method, path, body, user, role string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(body)).WithContext(testIdentityContext(user, role))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s for %s: %d %s; want %d", method, path, user, res.Code, res.Body.String(), want)
		}
		return res
	}

	draft := testDraft("v1")
	encoded, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	created := request(http.MethodPost, "/rule-sets", string(encoded), "owner", "owner", http.StatusCreated)
	var createdEnvelope struct {
		Data template.RuleSet `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatal(err)
	}
	if createdEnvelope.Data.State != template.RuleSetDraft || createdEnvelope.Data.RulesetHash != "" {
		t.Fatalf("created draft = %+v, want server-owned draft state and empty hash", createdEnvelope.Data)
	}

	request(http.MethodGet, "/rule-sets/definitions", "", "viewer", "viewer", http.StatusOK)
	request(http.MethodGet, "/rule-sets/proposal-rules/versions", "", "viewer", "viewer", http.StatusOK)
	request(http.MethodPost, "/rule-sets/proposal-rules/versions/v1/publish", "", "admin", "admin", http.StatusOK)
	published, err := service.GetVersion(context.Background(), Actor{TenantID: 1, UserID: "admin", Role: "admin"}, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-audit-http").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-audit-http", "viewer", "member").Error; err != nil {
		t.Fatal(err)
	}
	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	useAudit := template.RuleSetUseAuditEvent{
		TenantID: 1, ActorID: "viewer", ActorRole: "viewer", ProjectID: "project-audit-http",
		Action: "preflight", RuleSetID: draft.ID, RuleSetVersion: draft.Version, RuleSetHash: published.RulesetHash,
		EffectiveRulesetHash: verifiedBinding.EffectiveHash, TemplateID: verifiedTemplate.ID, TemplateVersion: verifiedTemplate.Version,
		TemplateCopyID: "copy-http", TemplateCopyVersion: 2, ProjectVersion: 7, ResourceID: "snapshot-http", Outcome: "blocked",
		VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
	}
	if err := service.RecordRuleSetUse(context.Background(), useAudit); err != nil {
		t.Fatal(err)
	}
	var auditEnvelope struct {
		Data struct {
			Items []template.RuleSetUseAuditEvent `json:"items"`
		} `json:"data"`
	}
	auditResponse := request(http.MethodGet, "/projects/project-audit-http/rule-set-use-audits", "", "viewer", "viewer", http.StatusOK)
	if err := json.Unmarshal(auditResponse.Body.Bytes(), &auditEnvelope); err != nil || len(auditEnvelope.Data.Items) != 1 ||
		auditEnvelope.Data.Items[0].ProjectID != "project-audit-http" || auditEnvelope.Data.Items[0].RuleSetHash != useAudit.RuleSetHash {
		t.Fatalf("tenant member could not read exact use audit: items=%+v err=%v body=%s", auditEnvelope.Data.Items, err, auditResponse.Body.String())
	}
	pageResponse := request(http.MethodGet, "/projects/project-audit-http/rule-set-use-audits?limit=1&action=preflight", "", "viewer", "viewer", http.StatusOK)
	var pageEnvelope struct {
		Data struct {
			Items      []template.RuleSetUseAuditEvent `json:"items"`
			NextCursor string                          `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &pageEnvelope); err != nil || len(pageEnvelope.Data.Items) != 1 || pageEnvelope.Data.Items[0].Action != "preflight" {
		t.Fatalf("filtered audit page = %+v err=%v body=%s", pageEnvelope.Data, err, pageResponse.Body.String())
	}
	for _, path := range []string{
		"/projects/project-audit-http/rule-set-use-audits?limit=0",
		"/projects/project-audit-http/rule-set-use-audits?from=not-a-time",
	} {
		request(http.MethodGet, path, "", "viewer", "viewer", http.StatusBadRequest)
	}
	request(http.MethodGet, "/projects/project-audit-http/rule-set-use-audits", "", "removed", "viewer", http.StatusForbidden)
	request(http.MethodGet, "/projects/empty-project/rule-set-use-audits", "", "removed", "viewer", http.StatusNotFound)
	request(http.MethodPost, "/rule-sets/proposal-rules/versions/v1/publish", "", "viewer", "viewer", http.StatusForbidden)
	request(http.MethodPost, "/rule-sets/proposal-rules/versions/v1/recall", "", "owner", "owner", http.StatusOK)
	request(http.MethodPost, "/rule-sets/proposal-rules/versions/v1/withdraw", "", "owner", "owner", http.StatusConflict)

	badState := draft
	badState.Version = "v2"
	badState.State = template.RuleSetPublished
	badJSON, _ := json.Marshal(badState)
	request(http.MethodPost, "/rule-sets", string(badJSON), "owner", "owner", http.StatusBadRequest)
	forgedHash := draft
	forgedHash.Version = "v2"
	forgedHash.RulesetHash = strings.Repeat("a", 64)
	forgedHashJSON, _ := json.Marshal(forgedHash)
	request(http.MethodPost, "/rule-sets", string(forgedHashJSON), "owner", "owner", http.StatusBadRequest)
	request(http.MethodPost, "/rule-sets", `{"ruleset_id":"proposal-rules","version":"v2","bindings":[],"unsafe":true}`, "owner", "owner", http.StatusBadRequest)
	request(http.MethodPost, "/rule-sets", string(encoded), "owner", "owner", http.StatusConflict)
	request(http.MethodGet, "/rule-sets/proposal-rules/versions/v1", "", "removed", "viewer", http.StatusForbidden)
	request(http.MethodGet, "/rule-sets/definitions", "", "", "viewer", http.StatusUnauthorized)

	var audits int64
	if err := db.Table("audit_logs").Where("scope_type = ? AND scope_id = ?", "lingdoc_ruleset", draft.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("audit rows = %d, want create, publish and recall to be audited", audits)
	}
}

func TestHTTPRuleSetCreateRejectsOversizedOrTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1/lingdoc")
	NewHTTPHandler(&httpApplicationStub{}, testRequestInfo).Register(group, group)
	ctx := testIdentityContext("owner", "owner")
	for _, body := range []string{
		`{"ruleset_id":"proposal-rules","version":"v1","bindings":{}} {}`,
		`{"ruleset_id":"proposal-rules","version":"v1","bindings":[],` + strings.Repeat(" ", 1<<20),
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/rule-sets", bytes.NewBufferString(body)).WithContext(ctx)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body length %d response = %d %s, want 400", len(body), res.Code, res.Body.String())
		}
	}
}

func TestHTTPHandlerWithoutIdentityAdapterFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1/lingdoc")
	NewHTTPHandler(&httpApplicationStub{}, nil).Register(group, group)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/rule-sets/definitions", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("request without identity adapter = %d %s, want 401", res.Code, res.Body.String())
	}
}

type httpApplicationStub struct{}

func (*httpApplicationStub) CreateDraft(context.Context, Actor, template.RuleSet) error { return nil }
func (*httpApplicationStub) GetVersion(context.Context, Actor, string, string) (template.RuleSet, error) {
	return template.RuleSet{}, nil
}
func (*httpApplicationStub) ListVersions(context.Context, Actor, string) ([]template.RuleSet, error) {
	return []template.RuleSet{}, nil
}
func (*httpApplicationStub) ListRuleSetUseAudits(context.Context, Actor, string) ([]template.RuleSetUseAuditEvent, error) {
	return []template.RuleSetUseAuditEvent{}, nil
}
func (*httpApplicationStub) Publish(context.Context, Actor, string, string) (template.RuleSet, error) {
	return template.RuleSet{}, nil
}
func (*httpApplicationStub) Withdraw(context.Context, Actor, string, string) (template.RuleSet, error) {
	return template.RuleSet{}, nil
}
func (*httpApplicationStub) Recall(context.Context, Actor, string, string) (template.RuleSet, error) {
	return template.RuleSet{}, nil
}
