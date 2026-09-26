package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// T13 传输层的验收：契约里的 checkCurrent / prepareRelease / getRelease 三条路由。
// 这一层此前一条都没有——交付包在包外零 importer，也就谈不上有入口。

const (
	deliveryRouteBase = "/api/v1/lingdoc/projects/project-1"
	// 契约给 prepareRelease 的请求体：只有 expected_project_version。
	deliveryReadVersionBody = `{"expected_project_version":8}`
)

func newDeliveryHTTPHandler(t *testing.T, chapters ...deliveryChapter) (*DeliveryHandler, *gorm.DB) {
	t.Helper()
	handler, db, assetID := seedBoundSource(t)
	if len(chapters) == 0 {
		chapters = []deliveryChapter{
			deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
			deliveryMethodChapter(),
		}
	}
	seedDeliveryWorkspace(t, db, chapters...)
	return NewDeliveryHandler(newDeliveryReleaseService(t, handler)), db
}

// deliveryRoutes 按容器与 router.go 装起来的样子挂载：已认证的 /lingdoc 组。
func deliveryRoutes(handler *DeliveryHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterDeliveryRoutes(router.Group("/api/v1/lingdoc"), handler)
	return router
}

func deliveryRequest(method, path, body, key string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	// policyContext 带上调用者身份：这三条路由都在已认证的组里，身份来自上游中间件。
	request := httptest.NewRequest(method, path, reader).WithContext(policyContext())
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func deliveryServe(router *gin.Engine, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// deliveryEnvelope 是契约的统一信封：data + request_id + meta，出错时换成 error。
type deliveryEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Meta  deliveryMeta    `json:"meta"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type deliveryMeta struct {
	Replayed        bool `json:"replayed"`
	RefreshRequired bool `json:"refresh_required"`
}

func decodeDeliveryEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) deliveryEnvelope {
	t.Helper()
	var envelope deliveryEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if !strings.Contains(recorder.Body.String(), `"request_id"`) {
		t.Fatalf("response is missing the contract envelope: %s", recorder.Body.String())
	}
	return envelope
}

func TestDeliveryRoutesFreezeAReleaseAndReadItBack(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	// 检查是只读的：契约没给这个操作标幂等键，给了也不该有副作用。
	checks := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/checks", deliveryReadVersionBody, ""))
	if checks.Code != http.StatusOK {
		t.Fatalf("POST checks = %d, want 200: %s", checks.Code, checks.Body.String())
	}
	checked := decodeDeliveryEnvelope(t, checks)
	var result delivery.CheckResult
	if err := json.Unmarshal(checked.Data, &result); err != nil {
		t.Fatalf("decode check result: %v", err)
	}
	if result.Status != delivery.CheckPassed || result.RulesetHash != delivery.DemoRulesetHash || result.ProjectVersion != deliveryProjectVersion {
		t.Fatalf("check result = %+v, want a passed result for the frozen version", result)
	}
	if checked.Meta.Replayed {
		t.Fatal("a read is reported as a replay")
	}

	prepared := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if prepared.Code != http.StatusCreated {
		t.Fatalf("POST releases = %d, want 201: %s", prepared.Code, prepared.Body.String())
	}
	first := decodeDeliveryEnvelope(t, prepared)
	var snapshot delivery.ReleaseSnapshot
	if err := json.Unmarshal(first.Data, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapshot.SnapshotDigest) != 64 || snapshot.Check.Status != delivery.CheckPassed || !snapshot.IsCurrent {
		t.Fatalf("snapshot = %+v, want a passed, current, digested freeze", snapshot)
	}
	if first.Meta.Replayed {
		t.Fatal("the first freeze of an action reports a replay")
	}

	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases/"+snapshot.ID, "", ""))
	if got.Code != http.StatusOK {
		t.Fatalf("GET release = %d, want 200: %s", got.Code, got.Body.String())
	}
	var readBack delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, got).Data, &readBack); err != nil {
		t.Fatalf("decode read-back snapshot: %v", err)
	}
	if readBack.ID != snapshot.ID || readBack.SnapshotDigest != snapshot.SnapshotDigest || !readBack.IsCurrent {
		t.Fatalf("read back %+v, want %s unchanged and current", readBack, snapshot.ID)
	}
}

// 契约把 Idempotency-Key 标成 /releases 的必填头，还专门写了「重放完成结果必须先于
// 首次写入的旧版本比较」。这条用例盯的就是那个先后：同一个键带着**已经过时**的版本号
// 重放，要换回原来那份快照，而不是被判成版本冲突——否则一次「已经冻好了、只是响应
// 丢了」的重试就再也拿不回结果了。
func TestDeliveryReleaseReplaysTheActionThatAlreadyFroze(t *testing.T) {
	handler, db := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	first := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST releases = %d, want 201: %s", first.Code, first.Body.String())
	}
	var created delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, first).Data, &created); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	// 工作区往前走了：此刻再比版本，8 已经不是当前版本。
	if err := db.Exec("UPDATE lingdoc_projects SET project_version = project_version + 1 WHERE id = ?", "project-1").Error; err != nil {
		t.Fatalf("move the project on: %v", err)
	}

	replay := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if replay.Code != http.StatusCreated {
		t.Fatalf("replayed POST releases = %d, want 201: %s", replay.Code, replay.Body.String())
	}
	envelope := decodeDeliveryEnvelope(t, replay)
	var again delivery.ReleaseSnapshot
	if err := json.Unmarshal(envelope.Data, &again); err != nil {
		t.Fatalf("decode replayed snapshot: %v", err)
	}
	if !envelope.Meta.Replayed || !envelope.Meta.RefreshRequired {
		t.Fatalf("replay meta = %+v, want replayed and refresh_required", envelope.Meta)
	}
	if again.ID != created.ID || again.SnapshotDigest != created.SnapshotDigest {
		t.Fatalf("replay = %s/%s, want the frozen %s/%s", again.ID, again.SnapshotDigest, created.ID, created.SnapshotDigest)
	}

	// 同一个键换一个请求体：那是键被复用，不是重试。
	conflict := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases",
		`{"expected_project_version":9}`, deliveryFreezeKey))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("reused key = %d, want 409: %s", conflict.Code, conflict.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, conflict).Error; code == nil || code.Code != "idempotency_conflict" {
		t.Fatalf("reused key error = %+v, want idempotency_conflict", code)
	}

	// 没有落下一份多余的快照：重放换回的一直是原来那一份。
	latest := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases/"+created.ID, "", ""))
	if latest.Code != http.StatusOK {
		t.Fatalf("GET release = %d, want 200: %s", latest.Code, latest.Body.String())
	}
	if readBack := decodeDeliveryEnvelope(t, latest); readBack.Meta.Replayed {
		t.Fatal("a read is reported as a replay")
	}
}

// 空章进 blocked 快照是契约 §7 明写的行为：不为缺失版本造假 ID，也不为了拿到一次
// 阻断结果强迫用户先写完所有章。传输层不能把 blocked 当成失败拒发。
func TestDeliveryReleaseFreezesABlockedSnapshotInsteadOfRefusing(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t, deliveryEmptyChapter(), deliveryMethodChapter())
	router := deliveryRoutes(handler)

	prepared := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if prepared.Code != http.StatusCreated {
		t.Fatalf("POST releases = %d, want a frozen blocked snapshot: %s", prepared.Code, prepared.Body.String())
	}
	var snapshot delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, prepared).Data, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Check.Status != delivery.CheckBlocked || len(snapshot.Check.Issues) == 0 {
		t.Fatalf("snapshot check = %+v, want a blocked result that says why", snapshot.Check)
	}
	// 阻断的理由必须指得着：一句「不通过」不是可操作的答案。
	for _, issue := range snapshot.Check.Issues {
		if issue.RuleID == "" || issue.TargetID == "" {
			t.Fatalf("issue %+v does not name a rule and a target", issue)
		}
	}
}

func TestDeliveryRoutesRefuseAVersionTheCallerDidNotSee(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	for _, route := range []string{"/checks", "/releases"} {
		response := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+route,
			`{"expected_project_version":7}`, deliveryFreezeKey))
		if response.Code != http.StatusConflict {
			t.Fatalf("POST %s = %d, want 409: %s", route, response.Code, response.Body.String())
		}
		if code := decodeDeliveryEnvelope(t, response).Error; code == nil || code.Code != "version_conflict" {
			t.Fatalf("POST %s error = %+v, want version_conflict", route, code)
		}
	}
}

func TestDeliveryRoutesAnswer404ForAnUnknownSnapshot(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	response := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases/snapshot-nope", "", ""))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET unknown release = %d, want 404: %s", response.Code, response.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, response).Error; code == nil || code.Code != "not_found" {
		t.Fatalf("unknown release error = %+v, want not_found", code)
	}
}

// 没有身份就不进这一层。工作区这一组既有路由一律答 not_found（不借状态码确认
// 项目存在），这三条跟着它，不另立一套。
func TestDeliveryRoutesRequireAnIdentity(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, deliveryRouteBase+"/checks", strings.NewReader(deliveryReadVersionBody)),
		httptest.NewRequest(http.MethodPost, deliveryRouteBase+"/releases", strings.NewReader(deliveryReadVersionBody)),
		httptest.NewRequest(http.MethodGet, deliveryRouteBase+"/releases/snapshot-1", nil),
	}
	for _, request := range requests {
		response := deliveryServe(router, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s without an identity = %d, want 404", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestDeliveryRoutesRejectMalformedRequests(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		key    string
	}{
		{name: "unknown field", method: http.MethodPost, path: "/checks", body: `{"expected_project_version":8,"extra":true}`},
		{name: "missing version", method: http.MethodPost, path: "/checks", body: `{}`},
		{name: "negative version", method: http.MethodPost, path: "/checks", body: `{"expected_project_version":-1}`},
		{name: "second json value", method: http.MethodPost, path: "/checks", body: deliveryReadVersionBody + ` {}`},
		{name: "missing idempotency key", method: http.MethodPost, path: "/releases", body: deliveryReadVersionBody},
		{name: "short idempotency key", method: http.MethodPost, path: "/releases", body: deliveryReadVersionBody, key: "abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := deliveryServe(router, deliveryRequest(test.method, deliveryRouteBase+test.path, test.body, test.key))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d, want 400: %s", test.name, response.Code, response.Body.String())
			}
			if code := decodeDeliveryEnvelope(t, response).Error; code == nil || code.Code != "invalid_request" {
				t.Fatalf("%s error = %+v, want invalid_request", test.name, code)
			}
		})
	}
}

// 契约把这三条路由的响应样例一起发布在 openapi.json 里。这条用例把样例的**字段形状**
// 与真实响应逐条对齐：值当然不同（样例是另一份 fixture），但对不上的字段名、少发的
// 字段、多发一个样例里没有的字段都会在这里现形。这仓里已经吃过一次「同一份输入四套
// 平行类型各自演化」的亏，线上形状值得钉在契约上，而不是钉在某个结构体上。
//
// 判据只到「字段在不在」这一层：空对象、空数组与 null 都算作同一个字段的存在，
// 免得把契约样例自身对空值的排版方式当成缺陷（值对不对由摘要那一组用例负责）。
func TestDeliveryRoutesAnswerTheShapeTheContractPublished(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	checks := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/checks", deliveryReadVersionBody, ""))
	if checks.Code != http.StatusOK {
		t.Fatalf("POST checks = %d, want 200: %s", checks.Code, checks.Body.String())
	}
	compareWithPublishedExample(t, "checkCurrent", "200", decodeDeliveryEnvelope(t, checks).Data)

	prepared := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if prepared.Code != http.StatusCreated {
		t.Fatalf("POST releases = %d, want 201: %s", prepared.Code, prepared.Body.String())
	}
	var snapshot delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, prepared).Data, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	compareWithPublishedExample(t, "prepareRelease", "201", decodeDeliveryEnvelope(t, prepared).Data)

	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases/"+snapshot.ID, "", ""))
	if got.Code != http.StatusOK {
		t.Fatalf("GET release = %d, want 200: %s", got.Code, got.Body.String())
	}
	compareWithPublishedExample(t, "getRelease", "200", decodeDeliveryEnvelope(t, got).Data)
}

// publishedDataExamples 从发布的契约里取出某个操作某个状态码的**全部**响应样例。
//
// 取全部而不是随便挑一个：examples 在契约里是个对象，Go 读进来是 map，遍历顺序
// 随机。挑第一个等于每次跑一条随机的断言——一条会随机挑到「issues 为空」那个
// not_evaluated 样例的用例，看起来绿着，其实什么都没比。
func publishedDataExamples(t *testing.T, operationID, status string) []json.RawMessage {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the published contract: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Responses   map[string]struct {
				Content map[string]struct {
					Examples map[string]struct {
						Value struct {
							Data json.RawMessage `json:"data"`
						} `json:"value"`
					} `json:"examples"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode the published contract: %v", err)
	}
	for _, operations := range spec.Paths {
		for _, operation := range operations {
			if operation.OperationID != operationID {
				continue
			}
			response, ok := operation.Responses[status]
			if !ok {
				t.Fatalf("%s publishes no %s response", operationID, status)
			}
			var examples []json.RawMessage
			for _, example := range response.Content["application/json"].Examples {
				if len(example.Value.Data) > 0 {
					examples = append(examples, example.Value.Data)
				}
			}
			if len(examples) == 0 {
				t.Fatalf("%s publishes no %s example to compare against", operationID, status)
			}
			return examples
		}
	}
	t.Fatalf("%s is not in the published contract", operationID)
	return nil
}

// compareWithPublishedExample 把响应的**字段形状**与契约样例对齐。
//
// 比哪条样例：样例集里带 status 时只比同状态的那条。契约正是用 status 分流发布这组
// 响应的（checkCurrent 的 passed/blocked/not_evaluated、getExport 的
// verified/failed），而 ExportArtifact 的 error 只在 failed 时出现——拿并集去比，
// 一份 verified 的响应就必须凭空多带一个 error 对象才算「合格」。样例不带 status
// 的（prepareRelease 与 getRelease 的快照）退回并集，行为与从前一致。
//
// minFields 是「这条用例有没有在比什么」的下限，默认 10。样例本来就少的响应
// （导出产物只有 6 个字段路径）要显式给一个小一点的数，否则守卫会一直误报。
func compareWithPublishedExample(t *testing.T, operationID, status string, actual json.RawMessage, minFields ...int) {
	t.Helper()
	examples := publishedDataExamples(t, operationID, status)
	if wanted := dataStatus(t, actual); wanted != "" {
		var matching []json.RawMessage
		for _, example := range examples {
			if dataStatus(t, example) == wanted {
				matching = append(matching, example)
			}
		}
		// 没有同状态的样例就退回并集，而不是让这条用例凭空失败：分流是优化，
		// 不是这条用例成立的前提。
		if len(matching) > 0 {
			examples = matching
		}
	}
	// 判据取所选样例的并集：契约在这条响应里**可能**出现的字段，响应都该有；
	// 响应里也不该多出任何一个样例从没提过的字段。
	published := map[string]bool{}
	for _, example := range examples {
		for _, path := range jsonFieldPaths(t, example) {
			published[path] = true
		}
	}
	// 两个空清单也「相等」：样例读成空的话，这条用例会永远绿着什么都不比。
	required := 10
	if len(minFields) > 0 {
		required = minFields[0]
	}
	if len(published) < required {
		t.Fatalf("%s %s 的契约样例只解析出 %d 个字段，这条用例没有在比什么", operationID, status, len(published))
	}
	got := jsonFieldPaths(t, actual)
	var missing, extra []string
	for path := range published {
		if !containsPath(got, path) {
			missing = append(missing, path)
		}
	}
	for _, path := range got {
		if !published[path] {
			extra = append(extra, path)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	sort.Strings(missing)
	t.Errorf("%s %s 的响应与契约样例对不上\n  契约有而响应没有: %v\n  响应有而契约没有: %v", operationID, status, missing, extra)
}

// dataStatus 取响应或样例里 data.status 的值；没有这个字段就是空串。
func dataStatus(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return envelope.Status
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

// jsonFieldPaths 列出每个字段的路径（数组元素收成 []）。字段在不在由它回答，
// 值是什么不在这里判。
func jsonFieldPaths(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	seen := map[string]bool{}
	var walk func(string, any)
	walk = func(prefix string, node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				path := key
				if prefix != "" {
					path = prefix + "." + key
				}
				seen[path] = true
				walk(path, child)
			}
		case []any:
			for _, child := range typed {
				walk(prefix+"[]", child)
			}
		}
	}
	walk("", value)
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// 幂等键按「项目 + 操作者 + 键」划范围：同一个键换个操作者是另一个动作，
// 不该把别人的快照交出去。
func TestDeliveryReleaseScopesTheIdempotencyKeyToItsActor(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	first := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, deliveryFreezeKey))
	if first.Code != http.StatusCreated {
		t.Fatalf("POST releases = %d, want 201: %s", first.Code, first.Body.String())
	}
	var created delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, first).Data, &created); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	other := policyContextWithUser("someone-else")
	request := httptest.NewRequest(http.MethodPost, deliveryRouteBase+"/releases", strings.NewReader(deliveryReadVersionBody)).WithContext(other)
	request.Header.Set("Idempotency-Key", deliveryFreezeKey)
	response := deliveryServe(router, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("another actor reusing the key = %d, want a freeze of its own: %s", response.Code, response.Body.String())
	}
	envelope := decodeDeliveryEnvelope(t, response)
	if envelope.Meta.Replayed {
		t.Fatal("another actor's action replayed a snapshot it did not ask for")
	}
	var second delivery.ReleaseSnapshot
	if err := json.Unmarshal(envelope.Data, &second); err != nil {
		t.Fatalf("decode second snapshot: %v", err)
	}
	if second.ID == created.ID {
		t.Fatal("two actors shared one snapshot through the same key")
	}
	if second.SnapshotDigest != created.SnapshotDigest {
		t.Fatalf("digests = %s / %s, want the same content to digest the same", second.SnapshotDigest, created.SnapshotDigest)
	}
}

// listSnapshots 读一次交付历史。四种断言（形状、当前性、截断、隔离）都要先取到它，
// 取法只有一种，写成一处。
func listSnapshots(t *testing.T, router *gin.Engine, projectID string) []delivery.ReleaseSnapshot {
	t.Helper()
	listed := deliveryServe(router, deliveryRequest(http.MethodGet, "/api/v1/lingdoc/projects/"+projectID+"/releases", "", ""))
	if listed.Code != http.StatusOK {
		t.Fatalf("GET releases = %d, want 200: %s", listed.Code, listed.Body.String())
	}
	var data struct {
		Items []delivery.ReleaseSnapshot `json:"items"`
	}
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, listed).Data, &data); err != nil {
		t.Fatalf("decode listed snapshots: %v", err)
	}
	return data.Items
}

// compareListItemsWithPublishedExample 把列表响应里的每一条与**单条**样例对齐。
//
// 整包比不了：列表的 data 上没有 status，compareWithPublishedExample 于是退回样例并集，
// 而并集里有 blocked 样例才有的 issues[].rule_id——一条 passed 的快照没有那个字段，
// 比出来必红。拆开就对了：信封判信封（items 与 truncated 两个键都在），元素判元素。
// 元素正是 getRelease / getExport 已经发布过的那张单条形状，连按 status 分流都能照用。
//
// 返回解析出的元素，好让调用方自己断言条数：**空列表会跳过整轮比较**，那时这条用例
// 什么都没比，而「该有几条」只有调用方知道。
func compareListItemsWithPublishedExample(t *testing.T, listOperationID, itemOperationID, status string, data json.RawMessage, minFields ...int) []json.RawMessage {
	t.Helper()
	var envelope struct {
		Items     []json.RawMessage `json:"items"`
		Truncated *bool             `json:"truncated"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode %s data: %v", listOperationID, err)
	}
	// truncated 用指针接：契约把它标成 required，而「false」与「字段不在」必须分得开，
	// bool 的零值恰好也是 false。
	if envelope.Truncated == nil {
		t.Fatalf("%s 的 data 里没有 truncated：%s", listOperationID, data)
	}
	if envelope.Items == nil {
		t.Fatalf("%s 的 data 里没有 items 数组：%s", listOperationID, data)
	}
	for _, item := range envelope.Items {
		compareWithPublishedExample(t, itemOperationID, status, item, minFields...)
	}
	return envelope.Items
}

// 交付历史的读侧：响应要对得上契约发布的形状，且 items 里的每一条就是 getRelease
// 发布过的那张单条形状——列表不是第二种快照表示。
func TestDeliveryListAnswersTheContractShape(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)
	created := freezeRelease(t, router, deliveryFreezeKey)

	listed := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases", "", ""))
	if listed.Code != http.StatusOK {
		t.Fatalf("GET releases = %d, want 200: %s", listed.Code, listed.Body.String())
	}
	envelope := decodeDeliveryEnvelope(t, listed)
	if envelope.Meta.Replayed {
		t.Fatal("a read is reported as a replay")
	}
	items := compareListItemsWithPublishedExample(t, "listReleases", "getRelease", "200", envelope.Data)
	if len(items) != 1 {
		t.Fatalf("listed %d snapshots, want the one that was frozen", len(items))
	}
	var readBack delivery.ReleaseSnapshot
	if err := json.Unmarshal(items[0], &readBack); err != nil {
		t.Fatalf("decode listed snapshot: %v", err)
	}
	if readBack.ID != created.ID || readBack.SnapshotDigest != created.SnapshotDigest {
		t.Fatalf("listed %s/%s, want the frozen %s/%s", readBack.ID, readBack.SnapshotDigest, created.ID, created.SnapshotDigest)
	}
}

// 契约 §7 要求界面在每次刷新时拿到**重算过**的当前性。列表是最容易违反这条的地方：
// 交回冻结时记下的那个值，历史里每一条都会永远自称「当前内容」。
//
// 这条用例同时盯住另一半：工作区变了，动的是**结论**，不是快照——frozen_input 与
// digest 必须停在冻结那一刻。那正是「冻结后内容变化不改变快照」看得见的样子。
func TestDeliveryListRecomputesCurrentnessAgainstTheLiveWorkspace(t *testing.T) {
	handler, db := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)
	created := freezeRelease(t, router, deliveryFreezeKey)

	before := listSnapshots(t, router, "project-1")
	if len(before) != 1 || !before[0].IsCurrent {
		t.Fatalf("listed %+v, want the freshly frozen snapshot to read as current", before)
	}

	// 工作区往前走了。冻结本身不改项目版本，所以这一步就是「冻结之后又编辑过」。
	if err := db.Exec("UPDATE lingdoc_projects SET project_version = project_version + 1 WHERE id = ?", "project-1").Error; err != nil {
		t.Fatalf("move the project on: %v", err)
	}

	after := listSnapshots(t, router, "project-1")
	if len(after) != 1 {
		t.Fatalf("listed %d snapshots, want the same one", len(after))
	}
	if after[0].IsCurrent {
		t.Fatal("一份已经不再代表工作区的快照被列表报成了当前内容")
	}
	if after[0].ID != created.ID || after[0].SnapshotDigest != created.SnapshotDigest {
		t.Fatalf("快照本身动了：%s/%s，want %s/%s", after[0].ID, after[0].SnapshotDigest, created.ID, created.SnapshotDigest)
	}
	if after[0].FrozenInput.ProjectVersion != deliveryProjectVersion {
		t.Fatalf("冻结输入记的项目版本变成了 %d，它该停在冻结那一刻的 %d",
			after[0].FrozenInput.ProjectVersion, deliveryProjectVersion)
	}

	// 单条读取与列表必须给同一个答案。两处各判一次的话，迟早一个说当前、一个说历史，
	// 而那时没人知道该信哪一个。
	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases/"+created.ID, "", ""))
	var readBack delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, got).Data, &readBack); err != nil {
		t.Fatalf("decode read-back snapshot: %v", err)
	}
	if readBack.IsCurrent {
		t.Fatal("getRelease 与 listReleases 对同一份快照给了两个结论")
	}
}

// 契约 §3：列表设了上限就必须显式提示截断，界面不能把截断后的列表当成全部历史。
func TestDeliveryListReportsTruncation(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)

	// 每个键是一次独立的用户动作，所以每一条都真的落进历史。
	for index := 0; index <= deliveryHistoryLimit; index++ {
		freezeRelease(t, router, fmt.Sprintf("freeze-action-%d", index))
	}

	listed := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/releases", "", ""))
	var data struct {
		Items     []json.RawMessage `json:"items"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, listed).Data, &data); err != nil {
		t.Fatalf("decode listed snapshots: %v", err)
	}
	if len(data.Items) != deliveryHistoryLimit {
		t.Fatalf("listed %d snapshots, want the %d之限", len(data.Items), deliveryHistoryLimit)
	}
	if !data.Truncated {
		t.Fatal("列表被截断了却没有报 truncated——界面会把这一屏当成全部历史")
	}
}

// 历史按项目隔离：另一个项目读不到这个项目的交付记录，而它读到的也不是一个错误
// ——「我没有交付历史」是个正常答案。
func TestDeliveryListScopesHistoryToTheProject(t *testing.T) {
	handler, _ := newDeliveryHTTPHandler(t)
	router := deliveryRoutes(handler)
	freezeRelease(t, router, deliveryFreezeKey)

	other := deliveryServe(router, deliveryRequest(http.MethodGet, "/api/v1/lingdoc/projects/project-other/releases", "", ""))
	if other.Code != http.StatusOK {
		t.Fatalf("GET another project's releases = %d, want 200: %s", other.Code, other.Body.String())
	}
	items := compareListItemsWithPublishedExample(t, "listReleases", "getRelease", "200", decodeDeliveryEnvelope(t, other).Data)
	if len(items) != 0 {
		t.Fatalf("另一个项目看到了 %d 条不属于它的交付历史", len(items))
	}
}
