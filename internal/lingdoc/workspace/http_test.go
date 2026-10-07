package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type applicationServiceStub struct {
	ApplicationService
	actor             Actor
	called            bool
	project           Project
	changeSetInput    CreateChangeSetInput
	changeSetID       string
	changeSetKey      string
	applyCalled       bool
	applyStatus       int
	rejectCalled      bool
	templateCopy      ProjectTemplateCopy
	templateProjectID string
	templateVersion   int64
	copyEditInput     TemplateCopyDefinitionInput
	copyEditKey       string
	copyEditCalled    bool
	copyPreviewCalled bool
}

type sourceApplicationServiceStub struct {
	SourceApplicationService
	actor     Actor
	projectID string
	input     RetrieveSourcesInput
	err       error
	called    bool
}

func (s *sourceApplicationServiceStub) RetrieveSources(_ context.Context, actor Actor, projectID string, input RetrieveSourcesInput) ([]evidence.Source, error) {
	s.called, s.actor, s.projectID, s.input = true, actor, projectID, input
	return nil, s.err
}

func (s *applicationServiceStub) ListProjects(_ context.Context, actor Actor) ([]Project, bool, error) {
	s.called = true
	s.actor = actor
	return []Project{s.project}, false, nil
}

func (s *applicationServiceStub) GetTemplateCopy(_ context.Context, actor Actor, projectID string, version int64) (ProjectTemplateCopy, error) {
	s.actor, s.templateProjectID, s.templateVersion = actor, projectID, version
	s.called = true
	return s.templateCopy, nil
}

func (s *applicationServiceStub) PreviewTemplateCopyEdit(_ context.Context, actor Actor, projectID string, input TemplateCopyDefinitionInput) (TemplateMigrationPreview, error) {
	s.actor, s.templateProjectID, s.copyEditInput = actor, projectID, input
	s.copyPreviewCalled = true
	return TemplateMigrationPreview{ProjectID: projectID, TargetCopyVersion: input.ExpectedTemplateCopyVersion + 1}, nil
}

func (s *applicationServiceStub) SaveTemplateCopy(_ context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (json.RawMessage, int, bool, error) {
	s.actor, s.templateProjectID, s.copyEditKey, s.copyEditInput = actor, projectID, key, input
	s.copyEditCalled = true
	return json.RawMessage(`{"project_id":"` + projectID + `","template_copy_version":2}`), http.StatusOK, false, nil
}

func (s *applicationServiceStub) CreateChangeSet(_ context.Context, actor Actor, projectID, key string, input CreateChangeSetInput) (json.RawMessage, int, bool, error) {
	s.called = true
	s.actor, s.changeSetInput, s.changeSetKey = actor, input, key
	return json.RawMessage(`{"id":"change-1","project_id":"` + projectID + `","status":"assessed"}`), http.StatusCreated, false, nil
}

func (s *applicationServiceStub) ApplyChangeSet(_ context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	s.applyCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	if s.applyStatus != 0 {
		return json.RawMessage(`{"id":"` + changeSetID + `","project_id":"` + projectID + `","status":"stale"}`), s.applyStatus, false, nil
	}
	return json.RawMessage(`{"id":"` + changeSetID + `","project_id":"` + projectID + `","status":"applied"}`), http.StatusOK, false, nil
}

func (s *applicationServiceStub) RejectChangeSet(_ context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	s.rejectCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	return json.RawMessage(`{"id":"` + changeSetID + `","project_id":"` + projectID + `","status":"rejected"}`), http.StatusOK, false, nil
}

func TestHandlerDelegatesSourceUseCaseAndMapsDeniedDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &sourceApplicationServiceStub{err: &DeniedAssetsError{
		Denied: []evidence.DeniedAsset{{AssetID: "asset-private", Reason: evidence.DenyNotAuthorized}},
	}}
	handler := NewHandler(HandlerDependencies{Sources: sources})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api/lingdoc"), Write: engine.Group("/api/lingdoc")})

	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/lingdoc/projects/project-1/retrieval", strings.NewReader(`{"query":"budget","asset_ids":["asset-private"]}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	if !sources.called || sources.actor != (Actor{TenantID: 42, UserID: "user-7", Role: types.TenantRoleViewer}) || sources.projectID != "project-1" || sources.input.Query != "budget" || len(sources.input.AssetIDs) != 1 || sources.input.AssetIDs[0] != "asset-private" {
		t.Fatalf("source use case input = called:%v actor:%#v project:%q input:%#v", sources.called, sources.actor, sources.projectID, sources.input)
	}
	var body struct {
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
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != "asset_not_authorized" || len(body.Error.Details.Denied) != 1 || body.Error.Details.Denied[0].AssetID != "asset-private" || body.Error.Details.Denied[0].Reason != "not_authorized" {
		t.Fatalf("error response = %#v, want the stable item-level denial contract", body.Error)
	}
}

func TestHandlerUsesInjectedApplicationService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}
	handler := NewHandler(HandlerDependencies{Service: service})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !service.called || service.actor != (Actor{TenantID: 42, UserID: "user-7", Role: types.TenantRoleViewer}) {
		t.Fatalf("injected service did not receive caller identity: called=%v actor=%#v", service.called, service.actor)
	}
}

func TestHandlerDelegatesProjectTemplateCopyReadPreviewAndSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{templateCopy: ProjectTemplateCopy{ProjectID: "project-1", Version: 1, Status: "draft"}}
	handler := NewHandler(HandlerDependencies{Service: service})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/template-copies/1", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.called || service.templateProjectID != "project-1" || service.templateVersion != 1 || service.actor != (Actor{TenantID: 42, UserID: "owner-7", Role: types.TenantRoleViewer}) {
		t.Fatalf("template copy read: status=%d called=%v project=%q version=%d actor=%#v body=%s", response.Code, service.called, service.templateProjectID, service.templateVersion, service.actor, response.Body.String())
	}

	body := `{"expected_project_version":3,"expected_template_copy_version":1,"fields":[],"sections":[],"terms":[],"required_fields":[],"rules":[]}`
	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/template-copy/preview", strings.NewReader(body)).WithContext(ctx)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.copyPreviewCalled || service.copyEditInput.ExpectedProjectVersion != 3 || service.copyEditInput.ExpectedTemplateCopyVersion != 1 || service.actor.UserID != "owner-7" {
		t.Fatalf("template copy preview: status=%d called=%v input=%#v actor=%#v body=%s", response.Code, service.copyPreviewCalled, service.copyEditInput, service.actor, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/projects/project-1/template-copy", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "template-copy-edit-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.copyEditCalled || service.copyEditKey != "template-copy-edit-1" || service.templateProjectID != "project-1" {
		t.Fatalf("template copy save: status=%d called=%v project=%q key=%q body=%s", response.Code, service.copyEditCalled, service.templateProjectID, service.copyEditKey, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/projects/project-1/template-copy", strings.NewReader(body)).WithContext(ctx)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("template copy save without idempotency key = %d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestHandlerServesVersionedDemoTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(HandlerDependencies{Templates: delivery.NewFixedTemplateReader()})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api/lingdoc"), Write: engine.Group("/api/lingdoc")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodGet, "/api/lingdoc/templates/template-demo", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		Data delivery.Template `json:"data"`
		Meta struct {
			Replayed        bool `json:"replayed"`
			RefreshRequired bool `json:"refresh_required"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode template response: %v", err)
	}
	if body.Data.ID != delivery.DemoTemplateID || body.Data.Version != delivery.DemoTemplateVersion || !body.Data.IsDemo {
		t.Fatalf("template = %#v, want fixed demo template", body.Data)
	}
	if len(body.Data.Sections) != 2 || len(body.Data.Rules) != 5 || len(body.Data.RequiredFields) != 2 {
		t.Fatalf("template shape = %#v, want complete contract", body.Data)
	}
	if body.Meta.Replayed || body.Meta.RefreshRequired {
		t.Fatalf("template meta = %#v, want non-replayed read", body.Meta)
	}
}

func TestHandlerMapsUnknownTemplateToNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(HandlerDependencies{Templates: delivery.NewFixedTemplateReader()})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api/lingdoc"), Write: engine.Group("/api/lingdoc")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodGet, "/api/lingdoc/templates/unknown", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode template error: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("error code = %q, want not_found", body.Error.Code)
	}
}

func TestHandlerDelegatesChangeSetPreviewAndApply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{}
	handler := NewHandler(HandlerDependencies{Service: service})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(`{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"旧主题","new_value":"新主题"}},"affected_chapter_ids":["chapter-1"],"reason":"研究对象发生变化"}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "change-preview-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !service.called || service.changeSetInput.ExpectedContextRevision != 1 || service.changeSetInput.Reason != "研究对象发生变化" || service.changeSetKey != "change-preview-1" {
		t.Fatalf("preview delegation: status=%d called=%v input=%#v key=%q body=%s", response.Code, service.called, service.changeSetInput, service.changeSetKey, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/apply", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "change-apply-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.applyCalled || service.changeSetID != "change-1" || service.changeSetKey != "change-apply-1" {
		t.Fatalf("apply delegation: status=%d called=%v id=%q key=%q body=%s", response.Code, service.applyCalled, service.changeSetID, service.changeSetKey, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/reject", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "change-reject-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.rejectCalled || service.changeSetID != "change-1" || service.changeSetKey != "change-reject-1" {
		t.Fatalf("reject delegation: status=%d called=%v id=%q key=%q body=%s", response.Code, service.rejectCalled, service.changeSetID, service.changeSetKey, response.Body.String())
	}
}

func TestHandlerMapsStaleChangeSetApplyToVersionConflictEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{applyStatus: http.StatusConflict}
	handler := NewHandler(HandlerDependencies{Service: service})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/apply", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "change-stale-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode stale response: %v", err)
	}
	if body.Error.Code != "version_conflict" {
		t.Fatalf("error code = %q, want version_conflict; body=%s", body.Error.Code, response.Body.String())
	}
}
