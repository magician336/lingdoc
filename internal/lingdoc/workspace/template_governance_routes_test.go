package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func governanceHTTPContext(ctx context.Context) context.Context {
	caller := types.CallerFromContext(ctx)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, caller.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, caller.UserID)
	return context.WithValue(ctx, types.TenantRoleContextKey, caller.Role)
}
func governanceHTTPRequest(router http.Handler, ctx context.Context, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(governanceHTTPContext(ctx))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
func TestCurrentAuthorityIdentityStrictBodyAndReplay(t *testing.T) {
	runtime, db, _, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	path := "/api/v1/lingdoc/template-catalog/profiles"
	body := `{"template_id":"http-profile","name":"真实目录HTTP","reason":"current authenticated curator"}`
	for n := 0; n < 2; n++ {
		response := governanceHTTPRequest(router, ctx, http.MethodPost, path, body, "http-profile-operation")
		if response.Code != 201 {
			t.Fatalf("profile status %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Meta struct {
				Replayed bool `json:"replayed"`
			}
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Meta.Replayed != (n == 1) {
			t.Fatal("false replay receipt", response.Body.String())
		}
	}
	response := governanceHTTPRequest(router, ctx, http.MethodPost, path, `{"template_id":"forged","name":"forged","reason":"synthetic","user_id":"someone-else"}`, "spoof-auth-operation")
	if response.Code != 400 {
		t.Fatal("caller identity body accepted", response.Code, response.Body.String())
	}
	if err := db.Exec("UPDATE tenant_members SET status='suspended' WHERE user_id='curator'").Error; err != nil {
		t.Fatal(err)
	}
	response = governanceHTTPRequest(router, ctx, http.MethodPost, path, body, "http-profile-operation")
	if response.Code != 404 {
		t.Fatal("revoked actor replay leaked success", response.Code, response.Body.String())
	}
	response = governanceHTTPRequest(router, ctx, http.MethodGet, "/api/v1/lingdoc/template-catalog/rulesets/empty/versions", "", "")
	if response.Code != 404 {
		t.Fatal("empty history skipped current read authority", response.Code, response.Body.String())
	}
}
func TestReaderCannotSeeUnpublishedCatalogDraft(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	reader := core.Actor{TenantID: owner.TenantID, UserID: "catalog-reader", Role: types.TenantRoleViewer}
	if err := db.Exec("INSERT INTO tenant_members VALUES(7,'catalog-reader','viewer','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	set, _ := seedDemoRuleSetFixture(t, runtime, owner, ctx)
	if _, err := runtime.Service().CreateProfile(ctx, governanceActor(owner), "create-reader-private-profile", governance.CreateProfileInput{ID: "template-demo", Name: "Private working draft", Reason: "reviewed draft visibility"}); err != nil {
		t.Fatal(err)
	}
	draft := saveDemoTemplateDraftFixture(t, runtime, owner, set, ctx)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	base := "/api/v1/lingdoc/template-catalog/profiles"
	readerCtx := governanceCallerContext(context.Background(), reader)
	response := governanceHTTPRequest(router, readerCtx, http.MethodGet, base, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("reader catalog: %d %s", response.Code, response.Body.String())
	}
	var catalog struct {
		Data []governance.Profile `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil || len(catalog.Data) != 0 {
		t.Fatalf("private draft profile visible: %+v, %v", catalog, err)
	}
	versionPath := base + "/" + draft.TemplateID + "/versions/" + draft.Version
	response = governanceHTTPRequest(router, readerCtx, http.MethodGet, base+"/"+draft.TemplateID+"/versions", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("reader draft-only profile history: %d %s", response.Code, response.Body.String())
	}
	response = governanceHTTPRequest(router, readerCtx, http.MethodGet, versionPath, "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("reader draft read: %d %s", response.Code, response.Body.String())
	}
	response = governanceHTTPRequest(router, ctx, http.MethodGet, versionPath, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin draft read: %d %s", response.Code, response.Body.String())
	}
	response = governanceHTTPRequest(router, readerCtx, http.MethodPost, base, `{"template_id":"reader-write","name":"forbidden","reason":"unauthorized"}`, "reader-write-attempt")
	if response.Code != http.StatusNotFound {
		t.Fatalf("reader catalog write: %d %s", response.Code, response.Body.String())
	}
	var denied int64
	if err := db.Table("audit_logs").Where("action=? AND actor_user_id=? AND outcome=?", "lingdoc.template.create_profile", reader.UserID, "failure").Count(&denied).Error; err != nil || denied != 1 {
		t.Fatalf("reader rejection audit = %d, %v", denied, err)
	}
	var receipt struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND actor_user_id=? AND outcome=?", "lingdoc.template.create_profile", reader.UserID, "failure").Take(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	var auditDetails struct {
		Details struct {
			ErrorCode string `json:"error_code"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(receipt.Details), &auditDetails); err != nil || auditDetails.Details.ErrorCode != "permission_denied" {
		t.Fatalf("reader denial audit code=%q details=%s err=%v, want permission_denied", auditDetails.Details.ErrorCode, receipt.Details, err)
	}
}

func TestTemplateAuditHistoryIsAdminOnlyAndTenantScoped(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	published, _ := publishDemoCatalogFixture(t, runtime, owner, ctx)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	path := "/api/v1/lingdoc/template-catalog/profiles/" + published.TemplateID + "/audit-events"

	reader := core.Actor{TenantID: owner.TenantID, UserID: "audit-reader", Role: types.TenantRoleViewer}
	if err := db.Exec("INSERT INTO tenant_members VALUES(7,'audit-reader','viewer','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	response := governanceHTTPRequest(router, governanceCallerContext(context.Background(), reader), http.MethodGet, path, "", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-manager audit history = %d %s, want 403", response.Code, response.Body.String())
	}

	if err := db.Exec("INSERT INTO tenants(id) VALUES(8)").Error; err != nil {
		t.Fatal(err)
	}
	other := core.Actor{TenantID: 8, UserID: "other-tenant-admin", Role: types.TenantRoleAdmin}
	if err := db.Exec("INSERT INTO tenant_members VALUES(8,'other-tenant-admin','admin','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	response = governanceHTTPRequest(router, governanceCallerContext(context.Background(), other), http.MethodGet, path, "", "")
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "lingdoc.template.publish") {
		t.Fatalf("cross-tenant audit history leaked: %d %s", response.Code, response.Body.String())
	}
	response = governanceHTTPRequest(router, ctx, http.MethodGet, path+"?after_id=not-a-cursor", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid audit cursor = %d %s", response.Code, response.Body.String())
	}
}

func TestRevokedActorWritesMinimalCatalogReceipt(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	if err := db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", owner.TenantID, owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	response := governanceHTTPRequest(router, ctx, http.MethodPost, "/api/v1/lingdoc/template-catalog/profiles",
		`{"template_id":"revoked-write","name":"revoked","reason":"must not author a receipt"}`, "revoked-catalog-write")
	if response.Code != http.StatusNotFound {
		t.Fatalf("revoked actor response = %d %s, want anti-enumerating 404", response.Code, response.Body.String())
	}
	var receipts []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND actor_user_id=? AND outcome=?", "lingdoc.template.create_profile", owner.UserID, "failure").Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || !strings.Contains(receipts[0].Details, `"error_code":"permission_denied"`) || strings.Contains(receipts[0].Details, `"content_hash"`) {
		t.Fatalf("revoked actor must leave one minimal denial receipt without protected catalog metadata: %+v", receipts)
	}
}

func TestTemplateGovernanceFailureReceiptRejectsMismatchedCallerIdentity(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	claimed := governanceActor(owner)
	claimed.UserID = "forged-catalog-actor"
	_, err := runtime.Service().CreateProfile(ctx, claimed, "forged-receipt-attribution", governance.CreateProfileInput{
		ID: "forged-receipt", Name: "must not exist", Reason: "mismatched authenticated identity",
	})
	if !errors.Is(err, core.ErrNotFound) || !errors.Is(err, governance.ErrUnverifiedActor) {
		t.Fatalf("mismatched caller error = %v, want an unverified identity denial", err)
	}
	var receipts, profiles int64
	if err := db.Table("audit_logs").Where("actor_user_id=?", claimed.UserID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("lingdoc_template_profiles").Where("template_id=?", "forged-receipt").Count(&profiles).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || profiles != 0 {
		t.Fatalf("unverified actor authored data or an attributed receipt: profiles=%d receipts=%d", profiles, receipts)
	}
}

func TestMachineCannotMakeHumanPublicationOrSuggestionDecisions(t *testing.T) {
	runtime, _, _, p, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	machine := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})
	for _, path := range []string{"/api/v1/lingdoc/template-catalog/profiles/profile/versions/1/publish", "/api/v1/lingdoc/template-catalog/profiles/profile/versions/1/withdraw", "/api/v1/lingdoc/template-catalog/rulesets/rules/versions/1/publish", "/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions/run/accept", "/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions/run/reject"} {
		response := governanceHTTPRequest(router, machine, http.MethodPost, path, "{}", "human-only-operation")
		if response.Code != 403 || !strings.Contains(response.Body.String(), "human_decision_required") {
			t.Fatalf("machine decision %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
func TestRejectsPathScopeMismatchBeforeExecuting(t *testing.T) {
	runtime, _, _, p, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	for _, input := range []struct{ Path, Body string }{{"/api/v1/lingdoc/template-catalog/profiles/a/versions", `{"template_id":"b"}`}, {"/api/v1/lingdoc/template-catalog/profiles/a/versions/1/publish", `{"template_id":"a","version":"2"}`}, {"/api/v1/lingdoc/template-catalog/rulesets/a/versions", `{"draft":{"ruleset_id":"b"},"reason":"synthetic mismatch"}`}, {"/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions", `{"project_id":"different-project"}`}} {
		response := governanceHTTPRequest(router, ctx, http.MethodPost, input.Path, input.Body, "path-scope-operation")
		if response.Code != 400 {
			t.Fatalf("path scope mismatch %s: %d %s", input.Path, response.Code, response.Body.String())
		}
	}
}
func TestListsRegisteredReplayFixtureCatalog(t *testing.T) {
	runtime, _, _, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	path := "/api/v1/lingdoc/template-catalog/replay-fixtures"
	for _, requestCtx := range []struct {
		name string
		ctx  context.Context
	}{{"human", ctx}, {"machine", types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})}} {
		response := governanceHTTPRequest(router, requestCtx.ctx, http.MethodGet, path, "", "")
		if response.Code != 200 {
			t.Fatalf("%s fixture catalog read: %d %s", requestCtx.name, response.Code, response.Body.String())
		}
		var result struct {
			Data []ReplayFixtureSummary `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Data) != 20 {
			t.Fatalf("%s fixture count = %d", requestCtx.name, len(result.Data))
		}
		previous := ""
		seen := map[string]bool{}
		for _, summary := range result.Data {
			if summary.FixtureID <= previous {
				t.Fatalf("fixture list not in stable order: %q after %q", summary.FixtureID, previous)
			}
			previous = summary.FixtureID
			if seen[summary.FixtureID] || summary.RuleID == "" || summary.Scene == "" || (summary.Polarity != "positive" && summary.Polarity != "negative") || summary.TemplateID == "" || summary.TemplateVersion == "" || summary.RuleSet.ID == "" {
				t.Fatalf("incomplete fixture summary: %+v", summary)
			}
			seen[summary.FixtureID] = true
		}
	}
}

// TestGovernanceFailuresKeepTheirOwnForm pins the outward shape of the
// governance failures that used to live in a transport table. The status and
// the code now travel with the error itself.
func TestGovernanceFailuresKeepTheirOwnForm(t *testing.T) {
	for _, input := range []struct {
		Err    error
		Status int
		Code   string
	}{{governance.ErrReplayRequired, 422, "template_replay_required"}, {governance.ErrSourceUnavailable, 403, "source_access_denied"}, {governance.ErrVersionConflict, 409, "version_conflict"}, {ErrCrossTenantSourceUnsupported, 422, "cross_tenant_source_unsupported"}, {governance.ErrModelUnavailable, 503, "template_governance_unavailable"}} {
		form, ok := clienterror.FormOf(input.Err)
		if !ok || form.Status != input.Status || form.Code != input.Code {
			t.Fatalf("error form %v: %+v", input.Err, form)
		}
	}
}
