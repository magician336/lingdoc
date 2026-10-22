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
	ProjectApplication
	MembershipApplication
	ChapterApplication
	TemplateApplication
	ChangeSetApplication
	actor             Actor
	called            bool
	project           Project
	changeSetInput    CreateChangeSetInput
	changeSetID       string
	changeSetKey      string
	applyCalled       bool
	applyStatus       int
	reassessCalled    bool
	rejectCalled      bool
	templateCopy      ProjectTemplateCopy
	templateProjectID string
	templateVersion   int64
	copyEditInput     TemplateCopyDefinitionInput
	copyEditKey       string
	copyEditCalled    bool
	copyPreviewCalled bool
	bindRuleSetInput  BindTemplateCopyRuleSetInput
	bindRuleSetKey    string
	bindRuleSetCalled bool
}

type saveSpecApplicationStub struct {
	applicationServiceStub
	called    bool
	actor     Actor
	projectID string
	key       string
	input     SaveSpecInput
	result    SaveSpecResult
}

func (s *saveSpecApplicationStub) SaveSpecResult(_ context.Context, actor Actor, projectID, key string, input SaveSpecInput) (SaveSpecResult, error) {
	s.called = true
	s.actor, s.projectID, s.key, s.input = actor, projectID, key, input
	return s.result, nil
}

type createProjectApplicationStub struct {
	applicationServiceStub
	called bool
	actor  Actor
	key    string
	input  CreateProjectInput
	result CreateProjectResult
}

func (s *createProjectApplicationStub) CreateProjectResult(_ context.Context, actor Actor, key string, input CreateProjectInput) (CreateProjectResult, error) {
	s.called, s.actor, s.key, s.input = true, actor, key, input
	return s.result, nil
}

type projectManagementApplicationStub struct {
	applicationServiceStub
	projectResult  ProjectMutationResult
	transferResult OwnerTransferResult
	changeCalled   bool
	copyCalled     bool
	membersCalled  bool
	transferCalled bool
	requestCalled  bool
	acceptCalled   bool
}

func (s *projectManagementApplicationStub) ChangeTemplateResult(context.Context, Actor, string, string, TemplateMigrationInput) (ProjectMutationResult, error) {
	s.changeCalled = true
	return s.projectResult, nil
}

func (s *projectManagementApplicationStub) SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error) {
	s.copyCalled = true
	return s.projectResult, nil
}

func (s *projectManagementApplicationStub) SaveMembersResult(context.Context, Actor, string, string, SaveMembersInput) (ProjectMutationResult, error) {
	s.membersCalled = true
	return s.projectResult, nil
}

func (s *projectManagementApplicationStub) TransferOwnerResult(context.Context, Actor, string, string, TransferOwnerInput) (ProjectMutationResult, error) {
	s.transferCalled = true
	return s.projectResult, nil
}

func (s *projectManagementApplicationStub) RequestOwnerTransferResult(context.Context, Actor, string, string, OwnerTransferInput) (OwnerTransferResult, error) {
	s.requestCalled = true
	return s.transferResult, nil
}

func (s *projectManagementApplicationStub) AcceptOwnerTransferResult(context.Context, Actor, string, string, string) (OwnerTransferResult, error) {
	s.acceptCalled = true
	return s.transferResult, nil
}

type chapterMutationApplicationStub struct {
	applicationServiceStub
	chapterCalled      bool
	workingCopyCalled  bool
	commitCalled       bool
	restoreCalled      bool
	createChangeCalled bool
	applyChangeCalled  bool
	rejectChangeCalled bool
	chapter            Chapter
	workingCopy        WorkingCopy
	committed          CommittedChapterVersion
	changeSet          ChangeSet
}

type draftCandidateApplicationStub struct {
	applicationServiceStub
	called    bool
	actor     Actor
	projectID string
	key       string
	input     DraftCandidateInput
	result    DraftCandidateMutationResult
}

func (s *draftCandidateApplicationStub) CreateDraftCandidateResult(_ context.Context, actor Actor, projectID, key string, input DraftCandidateInput) (DraftCandidateMutationResult, error) {
	s.called, s.actor, s.projectID, s.key, s.input = true, actor, projectID, key, input
	return s.result, nil
}

func (s *chapterMutationApplicationStub) SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error) {
	s.chapterCalled = true
	return ChapterMutationResult{Chapter: s.chapter}, nil
}

func (s *chapterMutationApplicationStub) SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error) {
	s.workingCopyCalled = true
	return WorkingCopyMutationResult{WorkingCopy: s.workingCopy}, nil
}

func (s *chapterMutationApplicationStub) CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error) {
	s.commitCalled = true
	return CommittedChapterMutationResult{Version: s.committed}, nil
}

func (s *chapterMutationApplicationStub) RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error) {
	s.restoreCalled = true
	return WorkingCopyMutationResult{WorkingCopy: s.workingCopy}, nil
}

func (s *chapterMutationApplicationStub) CreateChangeSetResult(context.Context, Actor, string, string, CreateChangeSetInput) (ChangeSetMutationResult, error) {
	s.createChangeCalled = true
	return ChangeSetMutationResult{ChangeSet: s.changeSet}, nil
}

func (s *chapterMutationApplicationStub) ApplyChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error) {
	s.applyChangeCalled = true
	return ChangeSetMutationResult{ChangeSet: s.changeSet}, nil
}

func (s *chapterMutationApplicationStub) RejectChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error) {
	s.rejectChangeCalled = true
	return ChangeSetMutationResult{ChangeSet: s.changeSet}, nil
}

// emptySourcesStub stands in for the sources port when a test does not
// exercise the source routes. The port is required, so a handler assembled
// without it would fail at startup; this keeps the other routes usable.
type emptySourcesStub struct {
	SourceApplicationService
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

func (s *applicationServiceStub) SaveTemplateCopyResult(_ context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (ProjectMutationResult, error) {
	s.actor, s.templateProjectID, s.copyEditKey, s.copyEditInput = actor, projectID, key, input
	s.copyEditCalled = true
	return ProjectMutationResult{Project: Project{ID: projectID, TemplateCopyVersion: 2}}, nil
}

func (s *applicationServiceStub) BindTemplateCopyRuleSetResult(_ context.Context, actor Actor, projectID, key string, input BindTemplateCopyRuleSetInput) (ProjectMutationResult, error) {
	s.actor, s.templateProjectID, s.bindRuleSetKey, s.bindRuleSetInput = actor, projectID, key, input
	s.bindRuleSetCalled = true
	return ProjectMutationResult{Project: Project{ID: projectID, TemplateCopyVersion: 3}}, nil
}

func (s *applicationServiceStub) CreateChangeSetResult(_ context.Context, actor Actor, projectID, key string, input CreateChangeSetInput) (ChangeSetMutationResult, error) {
	s.called = true
	s.actor, s.changeSetInput, s.changeSetKey = actor, input, key
	return ChangeSetMutationResult{ChangeSet: ChangeSet{ID: "change-1", ProjectID: projectID, Status: "assessed"}}, nil
}

func (s *applicationServiceStub) ApplyChangeSetResult(_ context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	s.applyCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	if s.applyStatus == http.StatusConflict {
		return ChangeSetMutationResult{}, ErrVersionConflict
	}
	return ChangeSetMutationResult{ChangeSet: ChangeSet{ID: changeSetID, ProjectID: projectID, Status: "applied"}}, nil
}

func (s *applicationServiceStub) ReassessChangeSet(_ context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	s.reassessCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	return json.RawMessage(`{"id":"reassessed-1","project_id":"` + projectID + `","reassessed_from_id":"` + changeSetID + `","status":"assessed"}`), http.StatusCreated, false, nil
}

func (s *applicationServiceStub) ReassessChangeSetResult(_ context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	s.reassessCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	return ChangeSetMutationResult{ChangeSet: ChangeSet{ID: "reassessed-1", ProjectID: projectID, ReassessedFromID: changeSetID, Status: "assessed"}}, nil
}

func (s *applicationServiceStub) RejectChangeSet(_ context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	s.rejectCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	return json.RawMessage(`{"id":"` + changeSetID + `","project_id":"` + projectID + `","status":"rejected"}`), http.StatusOK, false, nil
}

func (s *applicationServiceStub) RejectChangeSetResult(_ context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	s.rejectCalled = true
	s.actor, s.changeSetID, s.changeSetKey = actor, changeSetID, key
	return ChangeSetMutationResult{ChangeSet: ChangeSet{ID: changeSetID, ProjectID: projectID, Status: "rejected"}}, nil
}

func TestHandlerDelegatesSourceUseCaseAndMapsDeniedDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &sourceApplicationServiceStub{err: &DeniedAssetsError{
		Denied: []evidence.DeniedAsset{{AssetID: "asset-private", Reason: evidence.DenyNotAuthorized}},
	}}
	handler, err := NewHandler(contextPorts(&applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}, sources))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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

func TestHandlerMapsTransportNeutralSaveSpecResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &saveSpecApplicationStub{result: SaveSpecResult{
		Project:  Project{ID: "project-1", SpecRevision: 1, ProjectVersion: 2},
		Replayed: true,
	}}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/spec", strings.NewReader(`{"expected_spec_revision":0,"fields":{"research_subject":"subject"}}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "typed-spec-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !service.called || service.actor != (Actor{TenantID: 42, UserID: "owner-7", Role: types.TenantRoleViewer}) || service.projectID != "project-1" || service.key != "typed-spec-key" || service.input.ExpectedSpecRevision != 0 {
		t.Fatalf("typed save input = called:%v actor:%#v project=%q key=%q input=%#v", service.called, service.actor, service.projectID, service.key, service.input)
	}
	var body struct {
		Data Project `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode typed save response: %v", err)
	}
	if body.Data.ID != "project-1" || body.Data.SpecRevision != 1 || !body.Meta.Replayed {
		t.Fatalf("typed save response = %#v, want project and replay metadata", body)
	}
}

func TestHandlerMapsTransportNeutralCreateProjectResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &createProjectApplicationStub{result: CreateProjectResult{
		Project: Project{ID: "project-1", Name: "typed project", Status: "draft"},
	}}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"name":"typed project","template_id":"template-demo"}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "typed-project-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if !service.called || service.actor != (Actor{TenantID: 42, UserID: "owner-7", Role: types.TenantRoleViewer}) || service.key != "typed-project-key" || service.input.Name != "typed project" || service.input.TemplateID != "template-demo" {
		t.Fatalf("typed create input = called:%v actor:%#v key=%q input=%#v", service.called, service.actor, service.key, service.input)
	}
	var body struct {
		Data Project `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode typed create response: %v", err)
	}
	if body.Data.ID != "project-1" || body.Data.Name != "typed project" || body.Data.Status != "draft" || body.Meta.Replayed {
		t.Fatalf("typed create response = %#v, want created project without replay", body)
	}
}

func TestHandlerMapsTransportNeutralDraftCandidateResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &draftCandidateApplicationStub{result: DraftCandidateMutationResult{
		Candidate: DraftCandidate{ID: "candidate-1", ProjectID: "project-1", Title: "typed candidate", Content: "candidate body"},
		Replayed:  true,
	}}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/draft-candidates", strings.NewReader(`{"kind":"retrieval","title":"typed candidate","content":"candidate body","level":"background"}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "typed-candidate-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if !service.called || service.actor != (Actor{TenantID: 42, UserID: "owner-7", Role: types.TenantRoleViewer}) || service.projectID != "project-1" || service.key != "typed-candidate-key" || service.input.Title != "typed candidate" {
		t.Fatalf("typed candidate input = called:%v actor:%#v project=%q key=%q input=%#v", service.called, service.actor, service.projectID, service.key, service.input)
	}
	var body struct {
		Data DraftCandidate `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode typed candidate response: %v", err)
	}
	if body.Data.ID != "candidate-1" || body.Data.Title != "typed candidate" || body.Data.Content != "candidate body" || !body.Meta.Replayed {
		t.Fatalf("typed candidate response = %#v, want candidate and replay metadata", body)
	}
}

func TestHandlerMapsTransportNeutralProjectManagementResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &projectManagementApplicationStub{
		projectResult:  ProjectMutationResult{Project: Project{ID: "project-1", ProjectVersion: 2}},
		transferResult: OwnerTransferResult{Transfer: OwnerTransfer{ID: "transfer-1", Status: "pending"}},
	}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	requests := []struct {
		method string
		path   string
		body   string
		key    string
		want   int
		check  func() bool
	}{
		{http.MethodPost, "/api/projects/project-1/template-migration", `{}`, "typed-template-change", http.StatusOK, func() bool { return service.changeCalled }},
		{http.MethodPut, "/api/projects/project-1/template-copy", `{}`, "typed-template-copy", http.StatusOK, func() bool { return service.copyCalled }},
		{http.MethodPut, "/api/projects/project-1/members", `{}`, "typed-members", http.StatusOK, func() bool { return service.membersCalled }},
		{http.MethodPost, "/api/projects/project-1/owner-transfer", `{"expected_project_version":1,"new_owner_user_id":"member"}`, "typed-owner-transfer", http.StatusOK, func() bool { return service.transferCalled }},
		{http.MethodPost, "/api/projects/project-1/owner-transfer", `{"expected_project_version":1,"to_user_id":"member"}`, "typed-owner-request", http.StatusCreated, func() bool { return service.requestCalled }},
		{http.MethodPost, "/api/projects/project-1/owner-transfer/transfer-1/accept", ``, "typed-owner-accept", http.StatusOK, func() bool { return service.acceptCalled }},
	}
	for _, test := range requests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", test.key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s status = %d, want %d; body=%s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
		if !test.check() {
			t.Fatalf("%s %s did not use its transport-neutral application port", test.method, test.path)
		}
	}
}

func TestHandlerMapsTransportNeutralChapterAndChangeSetResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &chapterMutationApplicationStub{
		chapter:     Chapter{ID: "chapter-1", ProjectID: "project-1"},
		workingCopy: WorkingCopy{ProjectID: "project-1", ChapterID: "chapter-1", WorkingCopyRevision: 2},
		committed:   CommittedChapterVersion{ChapterVersionID: "version-1"},
		changeSet:   ChangeSet{ID: "change-1", ProjectID: "project-1", Status: "assessed"},
	}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	requests := []struct {
		method string
		path   string
		body   string
		key    string
		want   int
		check  func() bool
	}{
		{http.MethodPost, "/api/projects/project-1/chapters/chapter-1/versions", `{"expected_spec_revision":1,"body_markdown":"draft","source_ids":[]}`, "typed-chapter", http.StatusCreated, func() bool { return service.chapterCalled }},
		{http.MethodPut, "/api/projects/project-1/chapters/chapter-1/working-copy", `{}`, "typed-working-copy", http.StatusOK, func() bool { return service.workingCopyCalled }},
		{http.MethodPost, "/api/projects/project-1/chapters/chapter-1/working-copy/commit", `{}`, "typed-commit", http.StatusCreated, func() bool { return service.commitCalled }},
		{http.MethodPost, "/api/projects/project-1/chapters/chapter-1/working-copy/restore", `{}`, "typed-restore", http.StatusOK, func() bool { return service.restoreCalled }},
		{http.MethodPost, "/api/projects/project-1/change-sets", `{}`, "typed-change-create", http.StatusCreated, func() bool { return service.createChangeCalled }},
		{http.MethodPost, "/api/projects/project-1/change-sets/change-1/apply", ``, "typed-change-apply", http.StatusOK, func() bool { return service.applyChangeCalled }},
		{http.MethodPost, "/api/projects/project-1/change-sets/change-1/reject", ``, "typed-change-reject", http.StatusOK, func() bool { return service.rejectChangeCalled }},
	}
	for _, test := range requests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", test.key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s status = %d, want %d; body=%s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
		if !test.check() {
			t.Fatalf("%s %s did not use its transport-neutral application port", test.method, test.path)
		}
	}
}

func TestHandlerDelegatesProjectTemplateCopyReadPreviewAndSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{templateCopy: ProjectTemplateCopy{ProjectID: "project-1", Version: 1, Status: "draft"}}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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

	bindBody := `{"expected_project_version":4,"expected_template_copy_version":2,"ruleset_id":"grant-rules","ruleset_version":"v1"}`
	request = httptest.NewRequest(http.MethodPut, "/api/projects/project-1/template-copy/rule-set", strings.NewReader(bindBody)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "template-ruleset-bind-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.bindRuleSetCalled || service.bindRuleSetKey != "template-ruleset-bind-1" || service.templateProjectID != "project-1" || service.bindRuleSetInput.RuleSetID != "grant-rules" || service.bindRuleSetInput.ExpectedTemplateCopyVersion != 2 {
		t.Fatalf("template RuleSet binding: status=%d called=%v project=%q key=%q input=%#v body=%s", response.Code, service.bindRuleSetCalled, service.templateProjectID, service.bindRuleSetKey, service.bindRuleSetInput, response.Body.String())
	}
}

// The template RuleSet binding belongs to the template context, so it must
// reach the template port and no other.
//
// The legacy read/write split is retired: the binding used to be dispatched
// through the write half of a read/write pair. What the port model now
// guarantees is the same thing one level up — an operation reaches the port
// of the context that owns it.
func TestBindTemplateCopyRuleSetUsesTheTemplatePort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	template := &applicationServiceStub{}
	projects := &applicationServiceStub{}
	handler, err := NewHandler(HandlerDependencies{
		Projects:    projects,
		Members:     projects,
		Chapters:    projects,
		TemplateApp: template,
		ChangeSets:  projects,
		Sources:     emptySourcesStub{},
	})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	body := `{"expected_project_version":4,"expected_template_copy_version":2,"ruleset_id":"grant-rules","ruleset_version":"v1"}`
	request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/template-copy/rule-set", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "template-ruleset-bind-port")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !template.bindRuleSetCalled || projects.bindRuleSetCalled {
		t.Fatalf("binding must use the template port: status=%d projects=%v template=%v body=%s", response.Code, projects.bindRuleSetCalled, template.bindRuleSetCalled, response.Body.String())
	}
}

func TestHandlerServesVersionedDemoTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deps := contextPorts(&applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}, emptySourcesStub{})
	deps.Templates = delivery.NewFixedTemplateReader()
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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
	deps := contextPorts(&applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}, emptySourcesStub{})
	deps.Templates = delivery.NewFixedTemplateReader()
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/reassess", nil).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "change-reassess-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !service.reassessCalled || service.changeSetID != "change-1" || service.changeSetKey != "change-reassess-1" {
		t.Fatalf("reassess delegation: status=%d called=%v id=%q key=%q body=%s", response.Code, service.reassessCalled, service.changeSetID, service.changeSetKey, response.Body.String())
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
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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

func TestReassessChangeSetRejectsAnyRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{}
	handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	for _, body := range []string{`{}`, `{`, ` `} {
		request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/reassess", strings.NewReader(body)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", "change-reassess-body")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || service.reassessCalled {
			t.Fatalf("body %q: status=%d delegated=%v body=%s", body, response.Code, service.reassessCalled, response.Body.String())
		}
	}
}
