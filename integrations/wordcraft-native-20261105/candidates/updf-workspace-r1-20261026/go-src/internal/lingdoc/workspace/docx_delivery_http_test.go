package workspace

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type docxHTTPExportResponse struct {
	Export   delivery.ExportArtifact `json:"export"`
	Replayed bool                    `json:"replayed"`
}

func newDOCXDeliveryHTTPRouter(service *DOCXDeliveryService, f candidateOneConnFixture) *gin.Engine {
	router := gin.New()
	(&DOCXHTTPHandler{Runtime: f.runtime, Delivery: service}).Register(router)
	return router
}

func TestDOCXHTTPDeliveryStatusFreezeExportAndDownload(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	router := newDOCXDeliveryHTTPRouter(service, f)
	data := docxFixtureWithAllBodies(t, "http")
	saved := saveDOCXOverHTTP(t, f, router, "http-delivery-save", 0, data).Version
	for _, section := range saved.Projection.Sections {
		request := docxHTTPConfirmationRequest(saved, section.SectionID, f.input.ExpectedSpecRevision)
		response := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/confirmations", "http-delivery-confirm-"+section.SectionID, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	statusPath := "/api/docx/projects/project-1/versions/" + saved.ID + "/delivery"
	beforeFreeze := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, statusPath, "", nil)
	require.Equal(t, http.StatusOK, beforeFreeze.Code, beforeFreeze.Body.String())
	before := decodeDOCXHTTPResponse[DOCXDeliveryStatus](t, beforeFreeze)
	require.Equal(t, saved.ID, before.Version.ID)
	require.Len(t, before.Chapters, 6)
	for _, chapter := range before.Chapters {
		require.True(t, chapter.Confirmed, chapter.SectionID)
		require.NotNil(t, chapter.Confirmation, chapter.SectionID)
	}
	require.Nil(t, before.Snapshot)
	require.False(t, before.DeliveryEligible)

	freezeBody := map[string]string{"savedVersionId": saved.ID}
	frozenResponse := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/freezes", "http-delivery-freeze", freezeBody)
	require.Equal(t, http.StatusOK, frozenResponse.Code, frozenResponse.Body.String())
	frozen := decodeDOCXHTTPResponse[DOCXFreezeResult](t, frozenResponse)
	require.True(t, frozen.DeliveryEligible)
	require.Equal(t, delivery.CheckPassed, frozen.Snapshot.Check.Status)

	afterFreeze := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, statusPath, "", nil)
	require.Equal(t, http.StatusOK, afterFreeze.Code, afterFreeze.Body.String())
	after := decodeDOCXHTTPResponse[DOCXDeliveryStatus](t, afterFreeze)
	require.NotNil(t, after.Snapshot)
	require.Equal(t, frozen.Snapshot.ID, after.Snapshot.ID)
	require.True(t, after.Snapshot.IsCurrent)
	require.True(t, after.DeliveryEligible)

	replayedFreezeResponse := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/freezes", "http-delivery-freeze", freezeBody)
	require.Equal(t, http.StatusOK, replayedFreezeResponse.Code, replayedFreezeResponse.Body.String())
	replayedFreeze := decodeDOCXHTTPResponse[DOCXFreezeResult](t, replayedFreezeResponse)
	require.True(t, replayedFreeze.Replayed)
	require.Equal(t, frozen.Snapshot.ID, replayedFreeze.Snapshot.ID)

	conflict := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/freezes", "http-delivery-freeze", map[string]string{"savedVersionId": "another-version"})
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Equal(t, "IDEMPOTENCY_CONFLICT", decodeDOCXHTTPResponse[map[string]string](t, conflict)["error"])

	exportResponse := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/exports", "http-delivery-export", map[string]string{"snapshotId": frozen.Snapshot.ID})
	require.Equal(t, http.StatusOK, exportResponse.Code, exportResponse.Body.String())
	exported := decodeDOCXHTTPResponse[docxHTTPExportResponse](t, exportResponse)
	require.Equal(t, delivery.ExportVerified, exported.Export.Status)
	require.Equal(t, saved.DocSHA256, exported.Export.FileSHA256)

	downloadPath := "/api/docx/projects/project-1/exports/" + exported.Export.ID
	download := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, downloadPath, "", nil)
	require.Equal(t, http.StatusOK, download.Code, download.Body.String())
	require.Equal(t, data, download.Body.Bytes())
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", download.Header().Get("Content-Type"))
	require.Contains(t, download.Header().Get("Content-Disposition"), ".docx")

	missing := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/exports", "http-delivery-missing", map[string]string{"snapshotId": "missing-snapshot"})
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
	require.Equal(t, "NOT_FOUND", decodeDOCXHTTPResponse[map[string]string](t, missing)["error"])
	unauthenticated := performDOCXHTTPRequest(t, router, context.Background(), http.MethodGet, statusPath, "", nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code)

	require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id='source-1'").Error)
	stale := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, downloadPath, "", nil)
	require.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())
	require.Equal(t, "STALE_INPUT", decodeDOCXHTTPResponse[map[string]string](t, stale)["error"])

	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
	for _, request := range []struct {
		method string
		path   string
		key    string
		body   any
	}{
		{http.MethodGet, statusPath, "", nil},
		{http.MethodPost, "/api/docx/projects/project-1/freezes", "http-delivery-freeze", freezeBody},
		{http.MethodGet, downloadPath, "", nil},
	} {
		response := performDOCXHTTPRequest(t, router, f.ctx, request.method, request.path, request.key, request.body)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
}

func TestDOCXHTTPBlockedSnapshotCannotBeExported(t *testing.T) {
	f, service := newDOCXDeliveryFixture(t)
	router := newDOCXDeliveryHTTPRouter(service, f)
	saved := saveDOCXOverHTTP(t, f, router, "http-blocked-save", 0, readDOCXFixture(t)).Version
	frozenResponse := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/freezes", "http-blocked-freeze", map[string]string{"savedVersionId": saved.ID})
	require.Equal(t, http.StatusOK, frozenResponse.Code, frozenResponse.Body.String())
	frozen := decodeDOCXHTTPResponse[DOCXFreezeResult](t, frozenResponse)
	require.False(t, frozen.DeliveryEligible)
	require.Equal(t, delivery.CheckBlocked, frozen.Snapshot.Check.Status)
	require.NotEmpty(t, frozen.Snapshot.Check.Issues)

	exportResponse := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/exports", "http-blocked-export", map[string]string{"snapshotId": frozen.Snapshot.ID})
	require.Equal(t, http.StatusUnprocessableEntity, exportResponse.Code, exportResponse.Body.String())
	require.Equal(t, "PREFLIGHT_BLOCKED", decodeDOCXHTTPResponse[map[string]string](t, exportResponse)["error"])
}
