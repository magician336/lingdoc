package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T09 S4 的 HTTP 集成验收：契约里 project sources 这几个操作**在传输层**的样子。
//
// 领域判据在 internal/evidence 里已经逐条测过（授权 / 绑定 / 复核 / 分块定位），
// 这里要证的是另一件事：同一套判据挂上路由、过一遍中间件与错误映射之后，**答出来的
// 状态码与响应体**仍是那些判据说的意思。这是 S4 与 S1-S3 的分界，而它此前一条用例都
// 没有——所以 T09 报告里那句「S4 未运行」是真的，不是谦虚。
//
// 结论落成 docs/08-本轮实施方案/T09-S4-证据.json，由 verify_t09_test.go 读回去判分。
// 不带时间戳：两次绿跑产出逐字节相同的文件，工作区不会因为跑了一次测试就变脏。

const (
	// t09RouteBase 与 router.go 装配出来的前缀一致（Register 收下的是已认证的 /api/v1 组）。
	t09RouteBase = "/api/v1/lingdoc/projects/project-1"
	// t09S4EvidencePath 是这份证据的落点，相对本包目录。
	t09S4EvidencePath = "../../../docs/08-本轮实施方案/T09-S4-证据.json"
	// t09S4MinimumCases 是证据文件至少该有的条数。低于它说明有人删了用例却没删这一行——
	// 这条判据问的是「覆盖到了」，不是「跑过了」。
	t09S4MinimumCases = 18
)

// t09OriginLines 是强档 fixture 的原文，一段一行。
//
// 内容本身不重要，重要的是**每一段都能被坐标唯一指出来**：断言拿段文本当哨兵串，
// 某一段出现在不该出现的位置（跨了族、跳过了软删的块、越出了窗口）才看得出来。
var t09OriginLines = []string{
	"第一段：项目背景与建设目标。",
	"第二段：需求分析与调研结论。",
	"第三段：总体架构与技术选型。",
	"第四段：实施路径与里程碑安排。",
	"第五段：风险识别与应对措施。",
	"第六段：验收标准与交付物清单。",
	"第七段：运维保障与后续演进。",
}

// t09ParentSentinel 是父块族的哨兵串。它与 text 族的 idx 3 **同号**：窗口若按
// chunk_index 取而不按族过滤，第三段的位置上出现的就会是它。
const t09ParentSentinel = "父块：与子块的第三段同号，但不该出现在窗口里。"

// t09EditedNeighbour 是那条「坐标对不上」的邻居的正文。它的坐标仍指向第二段，
// 但内容已经不是那一段了——模拟被下游单独编辑过的块。
const t09EditedNeighbour = "这一段的正文已经被编辑过，与坐标指向的原文不再一致。"

func t09Origin() string { return strings.Join(t09OriginLines, "\n") }

// t09Span 给出第 index 段在整篇原文里的 rune 区间。段间只有一个 '\n'，
// 与 chunker.NormalizeLineEndings 归一之后的形态一致（否则强档根本立不起来）。
func t09Span(index int) (int, int) {
	start := 0
	for i := 0; i < index; i++ {
		start += len([]rune(t09OriginLines[i])) + 1
	}
	return start, start + len([]rune(t09OriginLines[index]))
}

// t09SourceID 给 text 族第 index 段一个稳定的分块 ID。
func t09SourceID(index int) string { return fmt.Sprintf("source-%d", index) }

// stubKBShareService 只作**非 nil** 用：kbReadChecker.CanReadKB 拿它挡「没装组织共享
// 这一层」的装配失误，而同租户 + Viewer 的读权限在 KBPermissions.Check 里直接放行，
// 走不到共享查询上。例外是「知识库属于别的租户」那条用例——那时它会问到
// CheckTenantKBPermission，所以覆写这一个方法答「没共享」，而不是让嵌入的 nil 接口 panic。
type stubKBShareService struct{ interfaces.KBShareService }

func (s stubKBShareService) CheckTenantKBPermission(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
	return "", false, nil
}

// stubKnowledgeBases 只回答绑定与检索真正会问的那两个问题。
// 嵌入 nil 接口：没覆写的方法被调用时直接 panic，而不是安静地答一个假值。
type stubKnowledgeBases struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s stubKnowledgeBases) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

// HybridSearch 答「没搜到」而不是报错：这组用例判的是授权，不是检索质量。
// 答成错误会把一条 200 的验收变成 500，而问题其实出在测试底座上。
func (s stubKnowledgeBases) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}

// selectTestAssetAuthorizer 按资料逐条放行。
//
// 不复用 flipTestAssetAuthorizer：那一条是全局开关，只能答「全放」或「全拒」，而
// listAssets 与 retrieveSources 的判据恰恰是**部分**授权——全放与全拒两种都测不出
// 「被拒的那一条不出现」。
type selectTestAssetAuthorizer struct{ denied map[string]bool }

func (a *selectTestAssetAuthorizer) CanAccessAsset(_ context.Context, _ evidence.Actor, _ string, asset evidence.Asset) (bool, error) {
	return !a.denied[asset.ID], nil
}

type assetsHTTPFixture struct {
	handler    *Handler
	db         *gorm.DB
	router     *gin.Engine
	authorizer *selectTestAssetAuthorizer
	// txtAssetID 是强档资料（真 txt 文件，原文逐字可比对）；pdfAssetID 是弱档资料
	// （pdf + 空 FilePath，取不回原文，只能靠坐标自洽）。
	txtAssetID string
	pdfAssetID string
}

// newAssetsHTTPFixture 起一套**贴近生产**的底座，挂**全组**路由。
//
// 不走 currentness 的 newSourceCurrentnessHandler：那一套只 AutoMigrate 了资产与修订
// 两张表，而 bindAsset 的幂等账本 lingdoc_operations 在迁移 000018 里——缺席的后果是
// 每次绑定都撞「no such table」答 500，而 201/200/409 三条用例全落在这张账本上。
//
// 也不走 http_currentness_test.go 那种手工单挂一条路由的写法：那测不了另外三个操作，
// 也测不出中间件与错误映射。路由挂全了，用例断言的才是生产里真正暴露的那个形状。
func newAssetsHTTPFixture(t *testing.T) assetsHTTPFixture {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "assets-http.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// lingdoc 与 evidence 两域的表走生产迁移；应用侧那几张不归 lingdoc 迁移管，手写。
	runLingdocMigrations(t, db)
	for _, statement := range assetsApplicationSchema {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create application schema: %v", err)
		}
	}
	for _, seed := range []struct {
		statement string
		args      []any
	}{
		{"INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, ?)", []any{7, "reader", "active"}},
		{"INSERT INTO lingdoc_projects (id, tenant_id, name, status, project_version, spec_revision, spec_json, template_id, template_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			[]any{"project-1", 7, "assets http test", "active", 1, 0, "{}", "template-demo", "1"}},
		{"INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", []any{"project-1", "reader", "owner"}},
		// chunking_config 必须是合法 JSON：ChunkingConfig.Scan 对空串会 json.Unmarshal
		// 报错，那个错一路冒到 sendError 就是把请求答成 500，看着像接口坏了。
		{"INSERT INTO knowledge_bases (id, tenant_id, name, chunking_config) VALUES (?, ?, ?, ?)", []any{"kb-1", 7, "synthetic KB", "{}"}},
	} {
		if err := db.Exec(seed.statement, seed.args...).Error; err != nil {
			t.Fatalf("seed %s: %v", seed.statement, err)
		}
	}

	handler := NewHandler(db, nil, nil)
	authorizer := &selectTestAssetAuthorizer{denied: map[string]bool{}}
	// 装配**之后**再换掉 gateway：复核适配器每调用一次现取 h.gateway（WorkspaceSourcePolicy
	// 的注释写了为什么），这一行才作数。装配时抓快照的写法会让注入的授权判定永远看不到。
	handler.gateway = evidence.NewAssetGateway(handler.bindings, authorizer)
	handler.kbShares = stubKBShareService{}
	handler.knowledge = stubKnowledgeBases{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}}

	txtAssetID := seedT09TxtKnowledge(t, handler, db)
	pdfAssetID := seedT09WeakKnowledge(t, handler, db)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "reader")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
	})
	handler.Register(router.Group("/api/v1"))
	return assetsHTTPFixture{
		handler: handler, db: db, router: router, authorizer: authorizer,
		txtAssetID: txtAssetID, pdfAssetID: pdfAssetID,
	}
}

// assetsApplicationSchema 是应用侧那几张表的手写 DDL。
//
// chunks 的 chunk_type / is_enabled 与生产 DDL 同款默认值：两列各钉一条判据——
// 语境窗口按族取邻居（text 与 parent_text 各有独立的 chunk_index 空间），而停用是
// 「不参与检索」、不是「不在原文里」。列缺了，那两条就无从测起，会安静地退化成「没测」。
var assetsApplicationSchema = []string{
	`CREATE TABLE tenant_members (
		tenant_id INTEGER NOT NULL,
		user_id TEXT NOT NULL,
		status TEXT NOT NULL,
		deleted_at DATETIME,
		PRIMARY KEY (tenant_id, user_id)
	)`,
	`CREATE TABLE knowledge_bases (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		chunking_config TEXT NOT NULL,
		deleted_at DATETIME
	)`,
	`CREATE TABLE knowledges (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		knowledge_base_id TEXT NOT NULL,
		type TEXT NOT NULL,
		title TEXT NOT NULL,
		file_type TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		file_hash TEXT NOT NULL,
		file_path TEXT NOT NULL,
		parse_status TEXT NOT NULL,
		processed_at DATETIME,
		deleted_at DATETIME
	)`,
	`CREATE TABLE chunks (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		knowledge_id TEXT NOT NULL,
		knowledge_base_id TEXT NOT NULL,
		content TEXT NOT NULL,
		content_revision INTEGER NOT NULL DEFAULT 0,
		chunk_index INTEGER NOT NULL,
		chunk_type TEXT NOT NULL DEFAULT 'text',
		is_enabled INTEGER NOT NULL DEFAULT 1,
		start_at INTEGER NOT NULL,
		end_at INTEGER NOT NULL,
		deleted_at DATETIME
	)`,
}

// seedT09TxtKnowledge 种一份**强档**资料：真 txt 文件 + 逐段落到了坐标上的分块行。
//
// text 族的布局：
//
//	idx 0 正常   idx 1 正常   idx 2 坐标对不上（被编辑过）
//	idx 3 正常   idx 4 正常（主命中块）
//	idx 5 **软删**   idx 6 正常（窗口 after 的落点，因为 5 被删了）
//
// 另有两族的干扰项：parent_text 的 idx 3、summary 的 idx 4。两条都与 text 族同号，
// 所以任何一条「不按族过滤」的实现都会在这里露馅。
func seedT09TxtKnowledge(t *testing.T, handler *Handler, db *gorm.DB) string {
	t.Helper()
	originPath := filepath.Join(t.TempDir(), "origin.txt")
	if err := os.WriteFile(originPath, []byte(t09Origin()), 0o600); err != nil {
		t.Fatalf("write origin file: %v", err)
	}
	assetID := bindT09Knowledge(t, handler, db, "knowledge-txt", "synthetic txt", "txt",
		originPath, "hash-txt", len([]rune(t09Origin())))

	for index := range t09OriginLines {
		content := t09OriginLines[index]
		if index == 2 {
			// 坐标不动、正文换掉：quoteAt 取回的原文片段与它不等，判成 stale。
			content = t09EditedNeighbour
		}
		start, end := t09Span(index)
		seedT09Chunk(t, db, t09SourceID(index), "knowledge-txt", content,
			string(types.ChunkTypeText), index, start, end)
	}
	if err := db.Exec("UPDATE chunks SET deleted_at = ? WHERE id = ?", time.Now().UTC(), t09SourceID(5)).Error; err != nil {
		t.Fatalf("soft delete the fifth chunk: %v", err)
	}

	parentStart, parentEnd := t09Span(3)
	seedT09Chunk(t, db, "parent-3", "knowledge-txt", t09ParentSentinel,
		string(types.ChunkTypeParentText), 3, parentStart, parentEnd)
	// summary 块与命中块同号、且坐标是对的：它的状态是 available，
	// 于是「不可展开」与「引用失效」两件事才分得开——用例判的正是这个区别。
	summaryStart, summaryEnd := t09Span(4)
	seedT09Chunk(t, db, "summary-4", "knowledge-txt", t09OriginLines[4],
		string(types.ChunkTypeSummary), 4, summaryStart, summaryEnd)
	return assetID
}

// seedT09WeakKnowledge 种一份**弱档**资料：pdf + 空 FilePath，原文取不回来。
// 坐标只要自洽（runeLen(Content) == EndAt-StartAt）就是 available——与产出侧同一条判据。
func seedT09WeakKnowledge(t *testing.T, handler *Handler, db *gorm.DB) string {
	t.Helper()
	assetID := bindT09Knowledge(t, handler, db, "knowledge-pdf", "synthetic pdf", "pdf", "", "hash-pdf", 32)

	const first = "弱档资料的第一段正文。"
	const second = "弱档资料的第二段正文，这一句是被引用的。"
	seedT09Chunk(t, db, "weak-0", "knowledge-pdf", first, string(types.ChunkTypeText), 0, 0, len([]rune(first)))
	seedT09Chunk(t, db, "weak-1", "knowledge-pdf", second, string(types.ChunkTypeText), 1,
		len([]rune(first))+1, len([]rune(first))+1+len([]rune(second)))
	return assetID
}

// bindT09Knowledge 写入知识行并把它绑到 project-1 上。知识行必须先于绑定：
// AssetGateway 会刷新资料版本，缺行会被读成「正在删除」。
func bindT09Knowledge(t *testing.T, handler *Handler, db *gorm.DB, knowledgeID, title, fileType, filePath, fileHash string, size int) string {
	t.Helper()
	processedAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)
	if err := db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_type, file_size, file_hash, file_path, parse_status, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		knowledgeID, 7, "kb-1", "file", title, fileType, size, fileHash, filePath, types.ParseStatusCompleted, processedAt,
	).Error; err != nil {
		t.Fatalf("seed knowledge %s: %v", knowledgeID, err)
	}
	asset, err := handler.bindings.Bind(context.Background(), evidence.BindInput{
		TenantID: 7, ProjectID: "project-1", KnowledgeID: knowledgeID, KnowledgeBaseID: "kb-1",
		Title: title, CreatedBy: "reader",
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: knowledgeID, ParseStatus: types.ParseStatusCompleted,
			FileHash: fileHash, FileSize: int64(size), ProcessedAt: processedAt,
		},
	})
	if err != nil {
		t.Fatalf("bind asset for %s: %v", knowledgeID, err)
	}
	return asset.ID
}

// seedT09Chunk 写一条分块行，索引与族都显式给。
//
// 不复用 source_policy_test.go 的 seedChunk：那条把 chunk_index 写死成 0、落进默认的
// text 族，而窗口的判据全在这两列上（按索引位取邻居、按族过滤）——写死的索引让
// 「跨族」与「软删空洞」两件事**都测不出来**。
func seedT09Chunk(t *testing.T, db *gorm.DB, id, knowledgeID, content, chunkType string, chunkIndex, startAt, endAt int) {
	t.Helper()
	if err := db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_id, knowledge_base_id, content, content_revision, chunk_index, chunk_type, start_at, end_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)",
		id, 7, knowledgeID, "kb-1", content, chunkIndex, chunkType, startAt, endAt,
	).Error; err != nil {
		t.Fatalf("seed chunk %s: %v", id, err)
	}
}

// ---------- 请求与响应 ----------

// t09Request / t09Serve 与 delivery_http_test.go 的同名样板是同一件事：注入调用者身份
// （这几条路由都在已认证的组里）、按需带幂等键。另起一对是因为那份常量与注释都绑在
// 交付链上，改交付测试不该动到 T09 的用例。
func t09Request(method, path, body, key string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader).WithContext(policyContext())
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func t09Serve(router *gin.Engine, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// t09Call 是一整次请求。
func t09Call(f assetsHTTPFixture, method, path, body, key string) *httptest.ResponseRecorder {
	return t09Serve(f.router, t09Request(method, path, body, key))
}

// t09Envelope 与交付链共用同一个信封（data + request_id + meta，出错时换成 error）。
type t09Envelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		Replayed bool `json:"replayed"`
	} `json:"meta"`
	Error *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

func t09Decode(t *testing.T, recorder *httptest.ResponseRecorder) t09Envelope {
	t.Helper()
	var envelope t09Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if !strings.Contains(recorder.Body.String(), `"request_id"`) {
		t.Fatalf("响应缺契约信封: %s", recorder.Body.String())
	}
	return envelope
}

// t09Expect 断言状态码并把信封解出来；失败信息里带上响应体，一条 500 才不会只报
// 「want 200, got 500」而让人回头翻代码猜原因。
func t09Expect(t *testing.T, recorder *httptest.ResponseRecorder, want int) t09Envelope {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("状态码 = %d, want %d；响应体 %s", recorder.Code, want, recorder.Body.String())
	}
	return t09Decode(t, recorder)
}

// t09ErrorCode 断言错误码。三条用例共用，省得每处都写一遍 nil 检查。
func t09ErrorCode(t *testing.T, envelope t09Envelope, want string) {
	t.Helper()
	if envelope.Error == nil {
		t.Fatalf("响应里没有 error 段，want code=%s", want)
	}
	if envelope.Error.Code != want {
		t.Fatalf("error.code = %q, want %q；响应体 %s", envelope.Error.Code, want, envelope.Error.Message)
	}
}

// t09Data 把 data 解进 dst。
func t09Data(t *testing.T, envelope t09Envelope, dst any) {
	t.Helper()
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		t.Fatalf("decode data %s: %v", string(envelope.Data), err)
	}
}

type contextSegmentView struct {
	SourceID   string `json:"source_id"`
	ChunkIndex int    `json:"chunk_index"`
	Relation   string `json:"relation"`
	Verbatim   bool   `json:"verbatim"`
	Text       string `json:"text"`
}

type sourceContextView struct {
	Source struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"source"`
	ContextAvailable bool `json:"context_available"`
	Window           struct {
		Before int `json:"before"`
		After  int `json:"after"`
	} `json:"window"`
	Segments []contextSegmentView `json:"segments"`
}

type assetView struct {
	ID              string `json:"id"`
	KnowledgeID     string `json:"knowledge_id"`
	ProcessingState string `json:"processing_state"`
}

// t09Context 请求语境端点并解出 data。
func t09Context(t *testing.T, f assetsHTTPFixture, sourceID string) sourceContextView {
	t.Helper()
	envelope := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/sources/"+sourceID+"/context", "", ""), http.StatusOK)
	var view sourceContextView
	t09Data(t, envelope, &view)
	return view
}

// ---------- 顶层用例：把子用例的结果收成一份证据 ----------

func TestT09SourceRoutesHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := make([]string, 0, 24)
	passed := 0
	record := func(name string, run func(t *testing.T)) {
		cases = append(cases, name)
		if t.Run(name, run) {
			passed++
		}
	}

	record("listAssets 只回允许集合", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		f.authorizer.denied[f.pdfAssetID] = true

		envelope := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/assets", "", ""), http.StatusOK)
		var assets []assetView
		t09Data(t, envelope, &assets)
		if len(assets) != 1 || assets[0].ID != f.txtAssetID {
			t.Fatalf("assets = %+v, want 只剩 %s——被拒的资料不得出现在列表里", assets, f.txtAssetID)
		}
	})

	record("bindAsset 首次 201 与重放 200", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		const body = `{"knowledge_id":"knowledge-txt"}`
		const key = "t09-bind-replay-key"

		first := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets", body, key), http.StatusCreated)
		if first.Meta.Replayed {
			t.Fatal("首次绑定被标成重放")
		}
		var created assetView
		t09Data(t, first, &created)
		if created.ID != f.txtAssetID {
			t.Fatalf("绑定回来的是 %s, want %s（同一份知识只该有一份资料）", created.ID, f.txtAssetID)
		}

		second := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets", body, key), http.StatusOK)
		if !second.Meta.Replayed {
			t.Fatal("重放没有被标出来——调用方无从知道这次没有新建")
		}
		var replayed assetView
		t09Data(t, second, &replayed)
		if replayed.ID != created.ID {
			t.Fatalf("重放回了另一份资料 %s, want %s", replayed.ID, created.ID)
		}
	})

	record("bindAsset 同键不同请求 409", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		const key = "t09-bind-conflict-key"

		t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets",
			`{"knowledge_id":"knowledge-txt"}`, key), http.StatusCreated)
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets",
			`{"knowledge_id":"knowledge-pdf"}`, key), http.StatusConflict)
		t09ErrorCode(t, envelope, "idempotency_conflict")
	})

	record("bindAsset 缺幂等键 400", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets",
			`{"knowledge_id":"knowledge-txt"}`, ""), http.StatusBadRequest)
		t09ErrorCode(t, envelope, "invalid_request")
	})

	record("bindAsset 未知知识 404", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets",
			`{"knowledge_id":"knowledge-absent"}`, "t09-bind-missing-key"), http.StatusNotFound)
		t09ErrorCode(t, envelope, "not_found")
	})

	record("bindAsset 无知识库读权限也是 404", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		// 知识库属于别的租户：kbReadChecker 放行不了，而**答 404 不答 403**——
		// 403 等于承认「这个知识库存在」，那是存在性泄露。
		f.handler.knowledge = stubKnowledgeBases{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 9}}

		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/assets",
			`{"knowledge_id":"knowledge-txt"}`, "t09-bind-denied-key"), http.StatusNotFound)
		t09ErrorCode(t, envelope, "not_found")
	})

	record("retrieveSources 空查询 400", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/retrieval",
			fmt.Sprintf(`{"query":"  ","asset_ids":[%q]}`, f.txtAssetID), ""), http.StatusBadRequest)
		t09ErrorCode(t, envelope, "invalid_request")
	})

	record("retrieveSources 空资料范围 400", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		// 空范围**不是**「查全库」：那会把一次没有明确范围的调用变成一次全量检索。
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/retrieval",
			`{"query":"建设目标","asset_ids":[]}`, ""), http.StatusBadRequest)
		t09ErrorCode(t, envelope, "invalid_request")
	})

	record("retrieveSources 部分授权整批 422 且逐项给出原因", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		f.authorizer.denied[f.pdfAssetID] = true

		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/retrieval",
			fmt.Sprintf(`{"query":"建设目标","asset_ids":[%q,%q]}`, f.txtAssetID, f.pdfAssetID), ""),
			http.StatusUnprocessableEntity)
		t09ErrorCode(t, envelope, "asset_not_authorized")

		// details 在 error **里面**（sendErrorDetails），不是顶层——前端可达路径是
		// err.error.details.denied，这一条钉的就是那个形状。
		var details struct {
			Denied []map[string]any `json:"denied"`
		}
		if err := json.Unmarshal(envelope.Error.Details, &details); err != nil {
			t.Fatalf("decode error.details %s: %v", envelope.Error.Details, err)
		}
		if len(details.Denied) != 1 {
			t.Fatalf("denied = %+v, want 恰好一条——逐项作答，不折叠成一句「有资料不可用」", details.Denied)
		}
		entry := details.Denied[0]
		// 每项**恰好**是 {asset_id, reason}：多一个键就是契约外的形状，而界面正是照
		// 这两个键渲染的。
		if len(entry) != 2 {
			t.Fatalf("denied[0] = %+v, want 恰好 {asset_id, reason}", entry)
		}
		if entry["asset_id"] != f.pdfAssetID || entry["reason"] != string(evidence.DenyNotAuthorized) {
			t.Fatalf("denied[0] = %+v, want {%s, not_authorized}", entry, f.pdfAssetID)
		}
	})

	record("retrieveSources 全部允许 200", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		envelope := t09Expect(t, t09Call(f, http.MethodPost, t09RouteBase+"/retrieval",
			fmt.Sprintf(`{"query":"建设目标","asset_ids":[%q]}`, f.txtAssetID), ""), http.StatusOK)
		var sources []map[string]any
		t09Data(t, envelope, &sources)
		// 检索底座在 fixture 里答「没搜到」，所以是空数组而不是 null——空数组是
		// 「查过了、没有」，null 是「没查」。这条判据在 accessStatus 那里已经栽过一次。
		if sources == nil {
			t.Fatal("data = null, want []（空数组）")
		}
	})

	record("getSource 撤权后 403", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		allowed := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/sources/"+t09SourceID(4), "", ""), http.StatusOK)
		var before struct {
			Status string `json:"status"`
		}
		t09Data(t, allowed, &before)
		if before.Status != string(evidence.SourceAvailable) {
			t.Fatalf("撤权前的 status = %q, want available——后面那条 403 才有意义", before.Status)
		}

		f.authorizer.denied[f.txtAssetID] = true
		envelope := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/sources/"+t09SourceID(4), "", ""),
			http.StatusForbidden)
		t09ErrorCode(t, envelope, "source_access_denied")
	})

	record("getSource 未知引用 404", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		envelope := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/sources/source-absent", "", ""), http.StatusNotFound)
		t09ErrorCode(t, envelope, "not_found")
	})

	record("getSource 坐标漂移是 200 的合法结论", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		if err := f.db.Exec("UPDATE chunks SET content_revision = 1 WHERE id = ?", t09SourceID(4)).Error; err != nil {
			t.Fatalf("edit chunk: %v", err)
		}
		envelope := t09Expect(t, t09Call(f, http.MethodGet, t09RouteBase+"/sources/"+t09SourceID(4), "", ""), http.StatusOK)
		var source struct {
			Status string `json:"status"`
		}
		t09Data(t, envelope, &source)
		// 「这条引用不再指得回原文」是一个**结论**，不是一次失败：答 4xx 会让界面把它
		// 显示成错误，而它描述的是内容本身的状态。
		if source.Status != string(evidence.SourceStale) {
			t.Fatalf("status = %q, want stale", source.Status)
		}
	})

	record("getSourceContext 展开同族邻居", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		view := t09Context(t, f, t09SourceID(4))

		if !view.ContextAvailable {
			t.Fatal("context_available = false，want true——强档资料的同族邻居应当展开")
		}
		if view.Window.Before != 1 || view.Window.After != 1 {
			t.Fatalf("window = %+v, want 两侧各 1 段", view.Window)
		}
		// 命中块是 idx 4：before 是 idx 3、after 是 idx 6（idx 5 被软删，见下一条用例）。
		if len(view.Segments) != 2 {
			t.Fatalf("segments = %+v, want 2 段", view.Segments)
		}
		before, after := view.Segments[0], view.Segments[1]
		if before.ChunkIndex != 3 || before.Relation != "before" || before.Text != t09OriginLines[3] || !before.Verbatim {
			t.Fatalf("before 段 = %+v, want idx 3 的原文且 verbatim", before)
		}
		if after.ChunkIndex != 6 || after.Relation != "after" || after.Text != t09OriginLines[6] || !after.Verbatim {
			t.Fatalf("after 段 = %+v, want idx 6 的原文且 verbatim", after)
		}
		if view.Source.Status != string(evidence.SourceAvailable) {
			t.Fatalf("source.status = %q, want available", view.Source.Status)
		}
	})

	record("getSourceContext 不跨族取父块", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		view := t09Context(t, f, t09SourceID(4))

		// parent_text 的 idx 3 与 text 的 idx 3 同号：窗口若按 chunk_index 取而不按族过滤，
		// 拿到的是父块。两族各有独立且稠密的索引空间，混进来同一段文字会出现两次。
		for _, segment := range view.Segments {
			if segment.Text == t09ParentSentinel || segment.SourceID == "parent-3" {
				t.Fatalf("segments 里混进了父块: %+v", segment)
			}
		}
	})

	record("getSourceContext 软删留号码空洞", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		view := t09Context(t, f, t09SourceID(4))

		// idx 5 被软删。按索引位取（`<` 加 LIMIT）会跳过它拿到 idx 6；
		// 按 chunk_index 的算术区间 [5,5] 取则会空手而归，让人以为文档到头了。
		if len(view.Segments) == 0 {
			t.Fatal("窗口是空的——软删不该让它整个塌掉")
		}
		for _, segment := range view.Segments {
			if segment.ChunkIndex == 5 {
				t.Fatalf("软删的 idx 5 出现在窗口里: %+v", segment)
			}
		}
		last := view.Segments[len(view.Segments)-1]
		if last.ChunkIndex != 6 {
			t.Fatalf("窗口末段 = idx %d, want 6——软删留下的空洞不该让窗口提前收尾", last.ChunkIndex)
		}
	})

	record("getSourceContext 逐字对不上只给位置", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		// 命中换成 idx 1：它的 after 邻居是 idx 2，那一条的正文与坐标对不上。
		view := t09Context(t, f, t09SourceID(1))

		if !view.ContextAvailable {
			t.Fatal("context_available = false，want true（命中块本身是好的）")
		}
		if len(view.Segments) != 2 {
			t.Fatalf("segments = %+v, want 2 段", view.Segments)
		}
		edited := view.Segments[1]
		if edited.ChunkIndex != 2 {
			t.Fatalf("窗口第二段 = idx %d, want 2", edited.ChunkIndex)
		}
		if edited.Verbatim || edited.Text != "" {
			t.Fatalf("被编辑过的邻居 = %+v, want 只给位置、不给正文——那一段确实不是原文，摆出来就是错的", edited)
		}
		// 它仍留在 segments 里：丢掉它会让窗口看上去是连续的，而 chunk_index 的号码
		// 本来就把这个缺口说清了。
		if head := view.Segments[0]; head.ChunkIndex != 0 || !head.Verbatim || head.Text != t09OriginLines[0] {
			t.Fatalf("窗口第一段 = %+v, want idx 0 的原文", head)
		}
	})

	record("getSourceContext 派生块不展开", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		view := t09Context(t, f, "summary-4")

		// 派生块没有指向原文的坐标，窗口无从取起。白名单而非黑名单：
		// 将来底座加了新的派生类型，默认落在「不可展开」这一侧。
		if view.ContextAvailable {
			t.Fatal("context_available = true，want false——派生块不该被当作可回原文的中心")
		}
		if view.Segments == nil {
			t.Fatal("segments = null, want []（契约里是 array，null 会让界面 .map 直接炸）")
		}
		if len(view.Segments) != 0 {
			t.Fatalf("segments = %+v, want 空", view.Segments)
		}
		if view.Source.Status != string(evidence.SourceAvailable) {
			t.Fatalf("source.status = %q, want available（不可展开不等于引用失效）", view.Source.Status)
		}
	})

	record("getSourceContext 弱档照样给正文但标注非逐字", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		view := t09Context(t, f, "weak-1")

		if !view.ContextAvailable {
			t.Fatal("context_available = false，want true——窗口的数据来源是分块表，它总是有内容")
		}
		if len(view.Segments) != 1 || view.Segments[0].ChunkIndex != 0 {
			t.Fatalf("segments = %+v, want 只剩 idx 0", view.Segments)
		}
		segment := view.Segments[0]
		// 取不回原文时正文照样给（它就是这个系统里落库的那份正文），但 verbatim 必须
		// 如实为 false——这一段无法被原文证明。两种 false 在界面上的措辞不同。
		if segment.Verbatim {
			t.Fatalf("弱档邻居被标成逐字: %+v", segment)
		}
		if segment.Text == "" {
			t.Fatalf("弱档邻居没有正文: %+v", segment)
		}
	})

	record("getSourceContext 撤权后 403 且不泄露正文", func(t *testing.T) {
		f := newAssetsHTTPFixture(t)
		t09Context(t, f, t09SourceID(4))

		f.authorizer.denied[f.txtAssetID] = true
		recorder := t09Call(f, http.MethodGet, t09RouteBase+"/sources/"+t09SourceID(4)+"/context", "", "")
		t09Expect(t, recorder, http.StatusForbidden)
		// 窗口是在授权与复核**之后**才读的：拒绝的响应体里不许出现邻居的正文，
		// 否则这条端点就成了绕过 ResolveAllowed 读全文的旁路。
		body := recorder.Body.String()
		for _, line := range t09OriginLines {
			if strings.Contains(body, line) {
				t.Fatalf("403 的响应体里带出了原文: %s", body)
			}
		}
	})

	t09WriteEvidence(t, cases, passed)
}

// s4Evidence 是交给 verify_t09_test.go 判分的那份结论。
//
// 没有时间戳、没有环境信息：这份文件要能被逐字节复现（跑两次绿，git status 不该动），
// 与两份验证报告同构。判分只问三件事——文件在不在、是不是合法 JSON、passed 等不等于 total。
type s4Evidence struct {
	Package string   `json:"package"`
	Source  string   `json:"source"`
	Total   int      `json:"total"`
	Passed  int      `json:"passed"`
	Cases   []string `json:"cases"`
}

func t09WriteEvidence(t *testing.T, cases []string, passed int) {
	t.Helper()
	if passed != len(cases) {
		// 有子用例红了就**不写**证据文件：留着一份旧的绿结论比没有结论更坏——
		// 它会让后来的人以为这次也验过了。
		t.Logf("S4 有 %d/%d 条未通过，不写证据文件", len(cases)-passed, len(cases))
		return
	}
	if len(cases) < t09S4MinimumCases {
		t.Fatalf("S4 只跑了 %d 条用例，低于下限 %d——要么用例被删了，要么下限该跟着改", len(cases), t09S4MinimumCases)
	}
	record := s4Evidence{
		Package: "internal/lingdoc/workspace",
		Source:  "assets_http_test.go",
		Total:   len(cases),
		Passed:  passed,
		Cases:   cases,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("marshal S4 evidence: %v", err)
	}
	if err := os.WriteFile(t09S4EvidencePath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write S4 evidence: %v", err)
	}
}
