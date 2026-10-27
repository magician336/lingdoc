package workspace

import (
	"encoding/json"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// postAlertRuleSave posts one alert-rule save over the real route and decodes
// the answer. The assertions stay on the externally visible status and payload:
// the rule identity is what an operator's second save must converge on.
func postAlertRuleSave(t *testing.T, engine *gin.Engine, userID, key, body string) (*httptest.ResponseRecorder, core.RuntimeAlertRule) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/runtime/alert-rules", strings.NewReader(body)).WithContext(actorContext(userID))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var decoded struct {
		Data core.RuntimeAlertRule `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if response.Code == http.StatusOK || response.Code == http.StatusCreated {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode alert-rule response: %v (%s)", err, response.Body.String())
		}
	}
	return response, decoded.Data
}

func newAlertRuleEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))
	return engine
}

// #470：同一个 signal + scope 的规则在真实 HTTP 路线上也只落一条。
//
// 第二次保存换了一个幂等键（用户在面板上再点一次保存，不是重试），返回
// 200 与同一条规则；读回列表只有一条。
func TestRuntimeAlertRuleRouteConvergesOnRepeatedSave(t *testing.T) {
	engine := newAlertRuleEngine(t)
	body := `{"signal":"failed","severity":"P1","action":"notify","responsible_id":"reader"}`

	response, first := postAlertRuleSave(t, engine, "reader", "http-alert-rule-save-1", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("first save status = %d; body=%s", response.Code, response.Body.String())
	}
	if first.ID == "" || first.Version != 1 || first.Status != "ACTIVE" {
		t.Fatalf("first save answered %+v", first)
	}

	response, second := postAlertRuleSave(t, engine, "reader", "http-alert-rule-save-2", body)
	if response.Code != http.StatusOK {
		t.Fatalf("repeated save status = %d; body=%s", response.Code, response.Body.String())
	}
	if second.ID != first.ID || second.Version != 1 {
		t.Fatalf("repeated save answered a different rule: %+v vs %+v", second, first)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/runtime/alert-rules", nil).WithContext(actorContext("reader"))
	listed := httptest.NewRecorder()
	engine.ServeHTTP(listed, request)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", listed.Code, listed.Body.String())
	}
	var decoded struct {
		Data []core.RuntimeAlertRule `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(decoded.Data) != 1 || decoded.Data[0].ID != first.ID {
		t.Fatalf("repeated save left %d rules: %+v", len(decoded.Data), decoded.Data)
	}

	// 同一个幂等键的重试仍由 operation ledger 重放，标记 replayed。
	retried := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/runtime/alert-rules", strings.NewReader(body)).WithContext(actorContext("reader"))
	retried.Header.Set("Idempotency-Key", "http-alert-rule-save-1")
	replayed := httptest.NewRecorder()
	engine.ServeHTTP(replayed, retried)
	if replayed.Code != http.StatusCreated || !strings.Contains(replayed.Body.String(), `"replayed":true`) {
		t.Fatalf("same-key retry status = %d; body=%s", replayed.Code, replayed.Body.String())
	}
}

// 保存不是谁都行：没有 manage 的成员在真实路线上拿不到规则，也写不进一条。
func TestRuntimeAlertRuleRouteRefusesAnOutsiderSave(t *testing.T) {
	engine := newAlertRuleEngine(t)
	body := `{"signal":"failed","severity":"P1","action":"notify","responsible_id":"reader"}`

	response, _ := postAlertRuleSave(t, engine, "outsider", "http-alert-rule-outsider-1", body)
	if response.Code != http.StatusNotFound {
		t.Fatalf("outsider save status = %d; body=%s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/runtime/alert-rules", nil).WithContext(actorContext("reader"))
	listed := httptest.NewRecorder()
	engine.ServeHTTP(listed, request)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"data":[]`) {
		t.Fatalf("an outsider save left a rule behind: %d %s", listed.Code, listed.Body.String())
	}
}
