package workspace

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// T14 传输层的验收：契约里的 startExport / getExport / downloadExport 三条路由。
// T13 那一批的结尾写着「导出的三个操作属于 T14……不在这里假装可用」——这一组就是
// 那句话兑现之后的样子。

const (
	// 契约给 startExport 的请求体：只有 format。
	deliveryExportBody = `{"format":"docx"}`
	// 一次导出动作的幂等键（8..128 字符）。
	deliveryExportKey = "export-action-http-1"
)

// switchableAuthorizer 先放行、后撤权，用来演 F07：**已经导出成功过**的文件，
// 在授权没了之后也不能下载。用例是单线程的，不需要加锁。
type switchableAuthorizer struct{ revoked bool }

func (a *switchableAuthorizer) AuthorizeProject(context.Context, string, string) error {
	if a.revoked {
		return candidateadoption.ErrSourceAccessDenied
	}
	return nil
}

// assembleExportRoutes 按容器与 router.go 装起来的样子挂载 T13 与 T14 两组。
//
// 两组共用同一个快照库。各自建一个的话，刚冻下的快照在导出时取不到，而那看起来
// 会像是「快照不存在」而不是「装配错了」——容器里也必须是同一份。
func assembleExportRoutes(t *testing.T, handler *Handler, db *gorm.DB, authorizer candidateadoption.DeliveryInputProjectAuthorizer) *gin.Engine {
	t.Helper()
	inputs := &candidateadoption.DeliveryInputService{
		Reader:     candidateadoption.NewSQLiteCandidateAdoptionStore(db),
		Authorizer: authorizer,
	}
	builder := handler.DeliveryInputBuilder()
	if builder == nil {
		t.Fatal("装配处拿不到交付输入构建器")
	}
	snapshots := delivery.NewMemorySnapshotStore()
	releases := NewDeliveryReleaseService(inputs, builder, snapshots)
	exports := NewDeliveryExportService(snapshots, delivery.NewMemoryExportStore(), DeliveryDocument{}, inputs)
	if releases == nil || exports == nil {
		t.Fatal("交付链装配不齐：T13 或 T14 仍是断的")
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1/lingdoc")
	RegisterDeliveryRoutes(group, NewDeliveryHandler(releases))
	RegisterDeliveryExportRoutes(group, NewDeliveryExportHandler(exports))
	return router
}

// newDeliveryExportHandler 落一个可交付的工作区并把两组路由装起来。不给章节就用
// 那条走得通的默认对（带引用的 question 章 + 陪衬 method 章）。
func newDeliveryExportHandler(t *testing.T, authorizer candidateadoption.DeliveryInputProjectAuthorizer, chapters ...deliveryChapter) (*gin.Engine, *gorm.DB) {
	t.Helper()
	handler, db, assetID := seedBoundSource(t)
	if len(chapters) == 0 {
		chapters = []deliveryChapter{
			deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
			deliveryMethodChapter(),
		}
	}
	seedDeliveryWorkspace(t, db, chapters...)
	return assembleExportRoutes(t, handler, db, authorizer), db
}

// publishedArtifact 是契约里的 ExportArtifact，按字段原名解回来。
//
// 两个可空字段用指针：用例要断言的正是「字段在不在」与「字段是不是 null」的差别，
// 而内部结构体上的 omitempty 恰恰会把后者变成前者——那正是这层 DTO 存在的理由。
type publishedArtifact struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id"`
	SnapshotID   string  `json:"snapshot_id"`
	Status       string  `json:"status"`
	FileSHA256   *string `json:"file_sha256"`
	DownloadPath *string `json:"download_path"`
	Error        *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// freezeRelease 走 F01 的第 20 步：冻结一份快照并取回它。
func freezeRelease(t *testing.T, router *gin.Engine, key string) delivery.ReleaseSnapshot {
	t.Helper()
	prepared := deliveryServe(router, deliveryRequest(http.MethodPost, deliveryRouteBase+"/releases", deliveryReadVersionBody, key))
	if prepared.Code != http.StatusCreated {
		t.Fatalf("POST releases = %d, want 201: %s", prepared.Code, prepared.Body.String())
	}
	var snapshot delivery.ReleaseSnapshot
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, prepared).Data, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snapshot
}

// startExport 走第 21 步。状态码不是 202 时把信封一并交回，让调用方能打印出错原因。
func startExport(t *testing.T, router *gin.Engine, snapshotID, key string) (publishedArtifact, deliveryEnvelope, int) {
	t.Helper()
	recorder := deliveryServe(router, deliveryRequest(http.MethodPost,
		deliveryRouteBase+"/releases/"+snapshotID+"/exports", deliveryExportBody, key))
	envelope := decodeDeliveryEnvelope(t, recorder)
	if recorder.Code != http.StatusAccepted {
		return publishedArtifact{}, envelope, recorder.Code
	}
	var artifact publishedArtifact
	if err := json.Unmarshal(envelope.Data, &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	return artifact, envelope, recorder.Code
}

func startExportOf(t *testing.T, router *gin.Engine, snapshotID string) publishedArtifact {
	t.Helper()
	artifact, envelope, status := startExport(t, router, snapshotID, deliveryExportKey)
	if status != http.StatusAccepted {
		t.Fatalf("startExport = %d, want 202: %s", status, envelope.Data)
	}
	return artifact
}

// F01 的第 20–23 步走一遍：冻结 → 导出 → 读状态 → 下载。这是 T14 存在的全部意义。
func TestExportRoutesRenderValidateAndDownloadARealFile(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)

	artifact, envelope, status := startExport(t, router, snapshot.ID, deliveryExportKey)
	if status != http.StatusAccepted {
		t.Fatalf("startExport = %d, want 202: %s", status, envelope.Data)
	}
	if envelope.Meta.Replayed {
		t.Fatal("the first export of an action reports a replay")
	}
	// 渲染与校验是同步做完的，所以 202 里报的就是终态；报 queued 会让客户端去轮询
	// 一个早已结束的任务。
	if artifact.Status != string(delivery.ExportVerified) {
		t.Fatalf("export status = %q, want verified", artifact.Status)
	}
	if artifact.FileSHA256 == nil || artifact.DownloadPath == nil {
		t.Fatalf("a verified export published no file: %+v", artifact)
	}
	if artifact.Error != nil {
		t.Fatalf("a verified export carried an error: %+v", artifact.Error)
	}

	// 第 22 步：读状态。它与第 21 步说的是同一件事。
	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/exports/"+artifact.ID, "", ""))
	if got.Code != http.StatusOK {
		t.Fatalf("getExport = %d, want 200: %s", got.Code, got.Body.String())
	}
	var readBack publishedArtifact
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, got).Data, &readBack); err != nil {
		t.Fatal(err)
	}
	if readBack.Status != artifact.Status || readBack.FileSHA256 == nil || *readBack.FileSHA256 != *artifact.FileSHA256 {
		t.Fatalf("getExport = %+v, want the same verified artifact %+v", readBack, artifact)
	}

	// 第 23 步：下载。地址取自响应里的 download_path 本身，所以这条用例同时钉住了
	// 「发出去的地址就是能取到文件的那个地址」——前缀漂开的那天它先红。
	download := deliveryServe(router, deliveryRequest(http.MethodGet, *artifact.DownloadPath, "", ""))
	if download.Code != http.StatusOK {
		t.Fatalf("downloadExport = %d, want 200: %s", download.Code, download.Body.String())
	}
	if contentType := download.Header().Get("Content-Type"); contentType != docxMediaType {
		t.Fatalf("download content-type = %q, want %q", contentType, docxMediaType)
	}
	disposition := download.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") || !strings.Contains(disposition, artifact.ID) {
		t.Fatalf("download disposition = %q, want an attachment named after %s", disposition, artifact.ID)
	}

	file := download.Body.Bytes()
	// F01-23：「摘要 hash 与下载真实字节相等」。这是「下载的就是导出时校验过的那份
	// 文件」唯一可核对的说法。
	sum := sha256.Sum256(file)
	if hex.EncodeToString(sum[:]) != *artifact.FileSHA256 {
		t.Fatalf("downloaded bytes hash to %s, but the artifact published %s", hex.EncodeToString(sum[:]), *artifact.FileSHA256)
	}
	// 落到磁盘上是一个真的 DOCX 压缩包，不是一段自称是文件的字节。
	if _, err := zip.NewReader(bytes.NewReader(file), int64(len(file))); err != nil {
		t.Fatalf("the downloaded file is not a DOCX package: %v", err)
	}
	// 而且它经得起生产那道校验：结构、正文顺序、关键数字、引用、待核附录。
	// snapshot 是从 HTTP 响应解回来的，与下载走的是同一次冻结。
	if err := (DeliveryDocument{}).ValidateFrozen(snapshot.FrozenInput, file); err != nil {
		t.Fatalf("the downloaded file does not say what the frozen release says: %v", err)
	}

	// 上面那道校验与渲染器共用同一份正文模型，所以它**看不见**渲染 XML 那一跳的
	// 毛病（两边一起错就一起对）。T14 的验收里还有一条「实际打开/编辑通过」，那只能
	// 在真的编辑器里做，所以这里留一个把下载到的字节原样落盘的出口：
	//
	//	LINGDOC_T14_DUMP=<目录> go test ./internal/lingdoc/workspace/ -run RenderValidateAndDownload
	//
	// 默认不写盘：用例不该在跑的途中往仓里丢文件。
	if dir := os.Getenv("LINGDOC_T14_DUMP"); dir != "" {
		path := filepath.Join(dir, artifact.ID+".docx")
		if err := os.WriteFile(path, file, 0o644); err != nil {
			t.Fatalf("dump the downloaded file: %v", err)
		}
		t.Logf("下载到的文件已落盘：%s", path)
	}
}

// 契约把 Idempotency-Key 标成 startExport 的必填头。同键重试换回原来那一份，
// 而不是再渲一份——一次用户动作在交付历史里只能有一条。
func TestExportRoutesReplayTheSameAction(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	first := startExportOf(t, router, snapshot.ID)

	second, envelope, _ := startExport(t, router, snapshot.ID, deliveryExportKey)
	if !envelope.Meta.Replayed {
		t.Fatal("the same action did not report a replay")
	}
	if second.ID != first.ID {
		t.Fatalf("the same action produced %s and then %s", first.ID, second.ID)
	}
}

// 同一个键换了请求是冲突，不是重试。契约 §6：同键不同请求返回冲突。
func TestExportRoutesRefuseAKeyReusedForAnotherSnapshot(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	first := freezeRelease(t, router, deliveryFreezeKey)
	if _, _, status := startExport(t, router, first.ID, deliveryExportKey); status != http.StatusAccepted {
		t.Fatalf("the first export = %d", status)
	}

	// 第二份快照要用另一个键冻，否则 T13 自己就会把它判成重放。
	other := freezeRelease(t, router, "freeze-action-2")
	if other.ID == first.ID {
		t.Fatal("the second freeze replayed the first snapshot instead of making its own")
	}
	recorder := deliveryServe(router, deliveryRequest(http.MethodPost,
		deliveryRouteBase+"/releases/"+other.ID+"/exports", deliveryExportBody, deliveryExportKey))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("reusing a key on another snapshot = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, recorder).Error; code == nil || code.Code != "idempotency_conflict" {
		t.Fatalf("error = %+v, want idempotency_conflict", code)
	}
}

// 契约把这三条路由的响应样例一起发布在 openapi.json 里；这里把字段形状对齐。
//
// 导出的样例比 T13 那三个富对象少得多（产物只有 6 条字段路径），所以显式给出下限，
// 否则 compareWithPublishedExample 那条「这条用例有没有在比什么」的守卫会一直误报。
func TestExportRoutesAnswerTheShapeTheContractPublished(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)

	started := deliveryServe(router, deliveryRequest(http.MethodPost,
		deliveryRouteBase+"/releases/"+snapshot.ID+"/exports", deliveryExportBody, deliveryExportKey))
	if started.Code != http.StatusAccepted {
		t.Fatalf("startExport = %d, want 202: %s", started.Code, started.Body.String())
	}
	// 契约给 startExport 只发布了 queued 那一条样例；我们答的是 verified，与它没有
	// 同状态的样例可比，于是退回并集——并集就是那 6 条路径，结果一样。
	compareWithPublishedExample(t, "startExport", "202", decodeDeliveryEnvelope(t, started).Data, 6)

	artifact := startExportOf(t, router, snapshot.ID)
	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/exports/"+artifact.ID, "", ""))
	if got.Code != http.StatusOK {
		t.Fatalf("getExport = %d, want 200: %s", got.Code, got.Body.String())
	}
	compareWithPublishedExample(t, "getExport", "200", decodeDeliveryEnvelope(t, got).Data, 6)
}

// 失败的产物对着契约那一份 failed 样例比：error 对象只在 failed 时出现，而它正是
// 在这里被比到的（verified 那份响应对不上这条样例的 9 条路径）。这一条同时守住了
// 「失败时 file_sha256 与 download_path 仍在响应里、只是 null」——契约要求这两个
// 字段 required，把它们整个删掉不只是少了信息，是违约。
func TestExportRoutesAnswerTheContractShapeForAFailedArtifact(t *testing.T) {
	router := exportRouterWithUnrenderableBody(t)
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	artifact := startExportOf(t, router, snapshot.ID)
	if artifact.Status != string(delivery.ExportFailed) {
		t.Fatalf("export status = %q, want failed", artifact.Status)
	}

	got := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/exports/"+artifact.ID, "", ""))
	if got.Code != http.StatusOK {
		t.Fatalf("getExport = %d, want 200: %s", got.Code, got.Body.String())
	}
	compareWithPublishedExample(t, "getExport", "200", decodeDeliveryEnvelope(t, got).Data, 9)
}

func TestExportRoutesRejectMalformedRequests(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	path := deliveryRouteBase + "/releases/" + snapshot.ID + "/exports"

	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "missing format", body: `{}`, key: deliveryExportKey},
		{name: "null format", body: `{"format":null}`, key: deliveryExportKey},
		{name: "empty format", body: `{"format":""}`, key: deliveryExportKey},
		{name: "unsupported format", body: `{"format":"pdf"}`, key: deliveryExportKey},
		{name: "unknown field", body: `{"format":"docx","template":"other"}`, key: deliveryExportKey},
		{name: "second json value", body: deliveryExportBody + ` {}`, key: deliveryExportKey},
		{name: "missing idempotency key", body: deliveryExportBody},
		{name: "short idempotency key", body: deliveryExportBody, key: "abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := deliveryServe(router, deliveryRequest(http.MethodPost, path, test.body, test.key))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d, want 400: %s", test.name, recorder.Code, recorder.Body.String())
			}
			if code := decodeDeliveryEnvelope(t, recorder).Error; code == nil || code.Code != "invalid_request" {
				t.Fatalf("%s error = %+v, want invalid_request", test.name, code)
			}
		})
	}
}

// 没有身份就不进这一层，与 T13 那三条同一条口径（一律 404，不借状态码确认项目存在）。
func TestExportRoutesRequireAnIdentity(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)

	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, deliveryRouteBase+"/releases/"+snapshot.ID+"/exports", strings.NewReader(deliveryExportBody)),
		httptest.NewRequest(http.MethodGet, deliveryRouteBase+"/exports/export-1", nil),
		httptest.NewRequest(http.MethodGet, deliveryRouteBase+"/exports/export-1/file", nil),
	}
	for _, request := range requests {
		response := deliveryServe(router, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s without an identity = %d, want 404", request.Method, request.URL.Path, response.Code)
		}
	}
}

// F10：快照本来就是一份 blocked 的检查结论。要答 422 preflight_blocked，让界面去
// 显示那份 issue 清单，而不是给一次假下载。
func TestExportRoutesBlockADeliberatelyBlockedSnapshot(t *testing.T) {
	// 空正文的章节冻出来就是一份 blocked 快照——契约 §7 说它要被冻下来，而不是被
	// 挡在门外（T13 那条用例盯的就是这个）。导出这一头于是必须把它挡回去。
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{}, deliveryEmptyChapter(), deliveryMethodChapter())
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	if snapshot.Check.Status != delivery.CheckBlocked {
		t.Fatalf("fixture status = %s, want blocked", snapshot.Check.Status)
	}

	recorder := deliveryServe(router, deliveryRequest(http.MethodPost,
		deliveryRouteBase+"/releases/"+snapshot.ID+"/exports", deliveryExportBody, deliveryExportKey))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("exporting a blocked snapshot = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, recorder).Error; code == nil || code.Code != "preflight_blocked" {
		t.Fatalf("error = %+v, want preflight_blocked", code)
	}
}

// F11：冻结之后工作区又动了，这份快照不再代表此刻的输入。要答 409 stale_input，
// 让调用方重新准备，而不是按一份过期的交付发文件。
func TestExportRoutesRefuseASnapshotTheWorkspaceMovedPast(t *testing.T) {
	router, db := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)

	if err := db.Exec("UPDATE lingdoc_projects SET project_version = project_version + 1 WHERE id = ?", "project-1").Error; err != nil {
		t.Fatalf("move the workspace on: %v", err)
	}
	recorder := deliveryServe(router, deliveryRequest(http.MethodPost,
		deliveryRouteBase+"/releases/"+snapshot.ID+"/exports", deliveryExportBody, deliveryExportKey))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("exporting a stale snapshot = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, recorder).Error; code == nil || code.Code != "stale_input" {
		t.Fatalf("error = %+v, want stale_input", code)
	}
}

// F13：导出失败不提供假下载。产物落成 failed（202 里的终态），下载答 422 invalid_state。
func TestExportRoutesPersistAFailedExportAndRefuseToDownloadIt(t *testing.T) {
	router := exportRouterWithUnrenderableBody(t)
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	if snapshot.Check.Status != delivery.CheckPassed {
		t.Fatalf("fixture status = %s, want passed", snapshot.Check.Status)
	}

	artifact := startExportOf(t, router, snapshot.ID)
	if artifact.Status != string(delivery.ExportFailed) {
		t.Fatalf("export status = %q, want failed", artifact.Status)
	}
	if artifact.Error == nil || artifact.Error.Code != delivery.FailureRenderFailed {
		t.Fatalf("failed export carries %+v, want render_failed", artifact.Error)
	}
	if artifact.FileSHA256 != nil || artifact.DownloadPath != nil {
		t.Fatalf("a failed export published a file: %+v", artifact)
	}

	// 即便照着 ID 直接去点下载，也不能拿到东西。
	download := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/exports/"+artifact.ID+"/file", "", ""))
	if download.Code != http.StatusUnprocessableEntity {
		t.Fatalf("downloading a failed export = %d, want 422: %s", download.Code, download.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, download).Error; code == nil || code.Code != "invalid_state" {
		t.Fatalf("error = %+v, want invalid_state", code)
	}
}

// F04：别的项目的导出，在这个项目下就是不存在。
func TestExportRoutesHideAnotherProjectsExport(t *testing.T) {
	router, _ := newDeliveryExportHandler(t, deliveryTestAuthorizer{})
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	artifact := startExportOf(t, router, snapshot.ID)

	for _, path := range []string{
		"/api/v1/lingdoc/projects/other-project/exports/" + artifact.ID,
		"/api/v1/lingdoc/projects/other-project/exports/" + artifact.ID + "/file",
	} {
		recorder := deliveryServe(router, deliveryRequest(http.MethodGet, path, "", ""))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("cross-project read = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
		if code := decodeDeliveryEnvelope(t, recorder).Error; code == nil || code.Code != "not_found" {
			t.Fatalf("cross-project read error = %+v, want not_found", code)
		}
	}
}

// F07：授权撤了之后，**已经导出成功过**的文件也不能下载。下载这条路每次重查授权，
// 而不是复用创建时的那次判定——否则一枚旧的 export_id 就是一扇后门。
func TestExportRoutesDenyADownloadAfterAccessIsGone(t *testing.T) {
	authorizer := &switchableAuthorizer{}
	router, _ := newDeliveryExportHandler(t, authorizer)
	snapshot := freezeRelease(t, router, deliveryFreezeKey)
	artifact := startExportOf(t, router, snapshot.ID)
	if artifact.DownloadPath == nil {
		t.Fatal("the export did not produce a file to revoke access to")
	}

	authorizer.revoked = true
	download := deliveryServe(router, deliveryRequest(http.MethodGet, *artifact.DownloadPath, "", ""))
	if download.Code != http.StatusForbidden {
		t.Fatalf("download after revocation = %d, want 403: %s", download.Code, download.Body.String())
	}
	if code := decodeDeliveryEnvelope(t, download).Error; code == nil || code.Code != "source_access_denied" {
		t.Fatalf("error = %+v, want source_access_denied", code)
	}
	// 读状态也要一起关门：否则撤权之后还能从状态里问出这个项目导出过什么。
	status := deliveryServe(router, deliveryRequest(http.MethodGet, deliveryRouteBase+"/exports/"+artifact.ID, "", ""))
	if status.Code != http.StatusForbidden {
		t.Fatalf("getExport after revocation = %d, want 403", status.Code)
	}
}

// 失败码带着各自的恢复动作，不只是换一句文案：渲染这一侧的岔子值得另起一个新动作
// 重试，校验失败不值得——文件是照着同一份冻结输入确定地渲出来的。
func TestExportFailureCodesCarryTheirOwnRecoveryAdvice(t *testing.T) {
	if !exportFailureRetryable(delivery.FailureRenderFailed) {
		t.Fatal("a renderer outage is not worth retrying")
	}
	if !exportFailureRetryable(delivery.FailureEmptyFile) {
		t.Fatal("an empty render is not worth retrying")
	}
	if exportFailureRetryable(delivery.FailureValidationFailed) {
		t.Fatal("a content mismatch would retry into the same mismatch")
	}
	codes := []string{delivery.FailureRenderFailed, delivery.FailureEmptyFile, delivery.FailureValidationFailed}
	for _, code := range codes {
		if exportFailureMessage(code) == "" {
			t.Fatalf("%s has no message to show", code)
		}
	}
	if exportFailureMessage(delivery.FailureValidationFailed) == exportFailureMessage(delivery.FailureRenderFailed) {
		t.Fatal("a content mismatch reads exactly like a renderer crash")
	}
}

// exportUnrenderableBody 是一段过得了 T13、却过不了 DOCX 渲染器的正文。
//
// demo 模板的五条规则（required_fields / chapter_nonempty / chapter_confirmed /
// review_items_decided / source_available）没有一条管正文的 Markdown 形态，所以
// 「加粗」这样的富标记能一路走到渲染器面前；而 T05 的渲染器只支持纯段落，它会拒。
// F13「导出器产生损坏文件」在这条链路里的真实形状就是它：不是磁盘坏了，是做不出
// 这份文件。这也是唯一一条能拿**真实**适配器走到的失败路径——另外两个失败码
// （empty_file、validation_failed）分别要一个空渲染器与一份被改过的文件。
const exportUnrenderableBody = "研究问题：**加粗**演示资料包含虚构记录 [[source:source-1]]，不能当作真实结论。"

// exportRouterWithUnrenderableBody 装一组正文带富标记的工作区。
func exportRouterWithUnrenderableBody(t *testing.T) *gin.Engine {
	t.Helper()
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), exportUnrenderableBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	return assembleExportRoutes(t, handler, db, deliveryTestAuthorizer{})
}
