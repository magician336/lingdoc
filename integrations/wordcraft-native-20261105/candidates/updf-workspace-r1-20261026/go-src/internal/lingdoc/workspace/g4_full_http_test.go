package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func g4HTTPContext(ctx context.Context) context.Context {
	caller := types.CallerFromContext(ctx)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, caller.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, caller.UserID)
	return context.WithValue(ctx, types.TenantRoleContextKey, caller.Role)
}
func g4HTTPRequest(router http.Handler, ctx context.Context, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(g4HTTPContext(ctx))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
func TestG4FullHTTPCurrentAuthorityIdentityStrictBodyAndReplay(t *testing.T) {
	runtime, db, _, _, ctx := g4AdapterFixture(t)
	router := gin.New()
	RegisterG4FullRoutes(router.Group("/api/v1/lingdoc"), runtime)
	path := "/api/v1/lingdoc/template-catalog/profiles"
	body := `{"template_id":"http-profile","name":"真实目录HTTP","reason":"current authenticated curator"}`
	for n := 0; n < 2; n++ {
		response := g4HTTPRequest(router, ctx, http.MethodPost, path, body, "http-profile-operation")
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
	response := g4HTTPRequest(router, ctx, http.MethodPost, path, `{"template_id":"forged","name":"forged","reason":"synthetic","user_id":"someone-else"}`, "spoof-auth-operation")
	if response.Code != 400 {
		t.Fatal("caller identity body accepted", response.Code, response.Body.String())
	}
	if err := db.Exec("UPDATE tenant_members SET status='suspended' WHERE user_id='curator'").Error; err != nil {
		t.Fatal(err)
	}
	response = g4HTTPRequest(router, ctx, http.MethodPost, path, body, "http-profile-operation")
	if response.Code != 404 {
		t.Fatal("revoked actor replay leaked success", response.Code, response.Body.String())
	}
	response = g4HTTPRequest(router, ctx, http.MethodGet, "/api/v1/lingdoc/template-catalog/rulesets/empty/versions", "", "")
	if response.Code != 404 {
		t.Fatal("empty history skipped current read authority", response.Code, response.Body.String())
	}
}
func TestG4FullHTTPMachineCannotMakeHumanPublicationOrSuggestionDecisions(t *testing.T) {
	runtime, _, _, p, ctx := g4AdapterFixture(t)
	router := gin.New()
	RegisterG4FullRoutes(router.Group("/api/v1/lingdoc"), runtime)
	machine := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})
	for _, path := range []string{"/api/v1/lingdoc/template-catalog/profiles/profile/versions/1/publish", "/api/v1/lingdoc/template-catalog/profiles/profile/versions/1/withdraw", "/api/v1/lingdoc/template-catalog/rulesets/rules/versions/1/publish", "/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions/run/accept", "/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions/run/reject"} {
		response := g4HTTPRequest(router, machine, http.MethodPost, path, "{}", "human-only-operation")
		if response.Code != 403 || !strings.Contains(response.Body.String(), "human_decision_required") {
			t.Fatalf("machine decision %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
func TestG4FullHTTPRejectsPathScopeMismatchBeforeExecuting(t *testing.T) {
	runtime, _, _, p, ctx := g4AdapterFixture(t)
	router := gin.New()
	RegisterG4FullRoutes(router.Group("/api/v1/lingdoc"), runtime)
	for _, input := range []struct{ Path, Body string }{{"/api/v1/lingdoc/template-catalog/profiles/a/versions", `{"template_id":"b"}`}, {"/api/v1/lingdoc/template-catalog/profiles/a/versions/1/publish", `{"template_id":"a","version":"2"}`}, {"/api/v1/lingdoc/template-catalog/rulesets/a/versions", `{"draft":{"ruleset_id":"b"},"reason":"synthetic mismatch"}`}, {"/api/v1/lingdoc/projects/" + p.ID + "/rule-suggestions", `{"project_id":"different-project"}`}} {
		response := g4HTTPRequest(router, ctx, http.MethodPost, input.Path, input.Body, "path-scope-operation")
		if response.Code != 400 {
			t.Fatalf("path scope mismatch %s: %d %s", input.Path, response.Code, response.Body.String())
		}
	}
}
func TestG4FullHTTPListsRegisteredReplayFixtureCatalog(t *testing.T) {
	runtime, _, _, _, ctx := g4AdapterFixture(t)
	router := gin.New()
	RegisterG4FullRoutes(router.Group("/api/v1/lingdoc"), runtime)
	path := "/api/v1/lingdoc/template-catalog/replay-fixtures"
	for _, requestCtx := range []struct {
		name string
		ctx  context.Context
	}{{"human", ctx}, {"machine", types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})}} {
		response := g4HTTPRequest(router, requestCtx.ctx, http.MethodGet, path, "", "")
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

func TestG4ErrorStatusKeepsReplayAndAvailabilityFailuresDistinct(t *testing.T) {
	for _, input := range []struct {
		Err    error
		Status int
		Code   string
	}{{governance.ErrReplayRequired, 422, "template_replay_required"}, {governance.ErrSourceUnavailable, 403, "source_access_denied"}, {governance.ErrVersionConflict, 409, "version_conflict"}, {ErrG4CrossTenantSourceUnsupported, 422, "cross_tenant_source_unsupported"}, {governance.ErrModelUnavailable, 503, "template_governance_unavailable"}} {
		status, code, _, ok := G4ErrorStatus(input.Err)
		if !ok || status != input.Status || code != input.Code {
			t.Fatalf("error mapping %v: %d %s", input.Err, status, code)
		}
	}
}
