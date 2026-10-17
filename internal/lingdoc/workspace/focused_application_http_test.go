package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type focusedReassessApplication struct {
	ChangeSetApplication
	result                      ChangeSetMutationResult
	err                         error
	actor                       Actor
	projectID, changeSetID, key string
}

func (s *focusedReassessApplication) ReassessChangeSetResult(_ context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	s.actor, s.projectID, s.changeSetID, s.key = actor, projectID, changeSetID, key
	return s.result, s.err
}

func TestFocusedChangeSetReassessKeepsTypedHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name     string
		replayed bool
		err      error
		status   int
	}{
		{name: "first response", status: http.StatusCreated},
		{name: "replay", replayed: true, status: http.StatusCreated},
		{name: "conflict", err: ErrVersionConflict, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &focusedReassessApplication{result: ChangeSetMutationResult{ChangeSet: ChangeSet{ID: "reassessed-1"}, Replayed: test.replayed}, err: test.err}
			handler := NewHandler(HandlerDependencies{ChangeSets: app})
			engine := gin.New()
			handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
			ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
			ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
			request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/changes-1/reassess", nil).WithContext(ctx)
			request.Header.Set("Idempotency-Key", "reassess-action-1")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if app.actor.TenantID != 42 || app.actor.UserID != "owner-1" || app.projectID != "project-1" || app.changeSetID != "changes-1" || app.key != "reassess-action-1" {
				t.Fatalf("focused reassess received incorrect actor or operation identity: %+v", app)
			}
			var envelope struct {
				Data *ChangeSet `json:"data"`
				Meta struct {
					Replayed        bool `json:"replayed"`
					RefreshRequired bool `json:"refresh_required"`
				} `json:"meta"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if test.err != nil {
				if envelope.Data != nil || envelope.Error.Code != "version_conflict" {
					t.Fatalf("conflict leaked a result or changed domain error mapping: %s", response.Body.String())
				}
				return
			}
			if envelope.Data == nil || envelope.Data.ID != "reassessed-1" || envelope.Meta.Replayed != test.replayed || envelope.Meta.RefreshRequired != test.replayed {
				t.Fatalf("typed result/replay metadata changed: %s", response.Body.String())
			}
		})
	}
}

func TestFocusedPortsRetainG5G7CompatibilityBridge(t *testing.T) {
	legacy := &machineHTTPTestApplication{}
	projects := &focusedProjectPortStub{}
	handler := NewHandler(HandlerDependencies{Service: legacy, Projects: projects})
	if handler.machineReads != legacy || handler.projects != projects {
		t.Fatalf("G5/G7 bridge replaced an explicitly supplied focused port: service=%T projects=%T", handler.machineReads, handler.projects)
	}
}

// Both instances deliberately satisfy the broad compatibility interface: the
// transport must still send reads and writes to the separately injected ports.
type splitRoutingApplication struct {
	applicationServiceStub
	writes            []string
	confirmationReads int
	machineReads      int
}

func (s *splitRoutingApplication) CreateProjectResult(context.Context, Actor, string, CreateProjectInput) (CreateProjectResult, error) {
	s.writes = append(s.writes, "project")
	return CreateProjectResult{Project: s.project}, nil
}
func (s *splitRoutingApplication) SaveMembersResult(context.Context, Actor, string, string, SaveMembersInput) (ProjectMutationResult, error) {
	s.writes = append(s.writes, "members")
	return ProjectMutationResult{Project: s.project}, nil
}
func (s *splitRoutingApplication) SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error) {
	s.writes = append(s.writes, "chapter")
	return ChapterMutationResult{Chapter: Chapter{ID: "chapter-1", ProjectID: s.project.ID}}, nil
}
func (s *splitRoutingApplication) SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error) {
	s.writes = append(s.writes, "template")
	return ProjectMutationResult{Project: s.project}, nil
}
func (s *splitRoutingApplication) ReassessChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error) {
	s.writes = append(s.writes, "change-set")
	return ChangeSetMutationResult{ChangeSet: ChangeSet{ID: "changes-1", ProjectID: s.project.ID}}, nil
}

func TestSplitApplicationPortsDoNotRouteWritesThroughRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	read := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "read-project"}}}
	write := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "write-project"}}}
	handler := NewHandler(HandlerDependencies{Read: read, Write: write})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	for _, test := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"read", http.MethodGet, "/api/projects", "", http.StatusOK},
		{"project", http.MethodPost, "/api/projects", `{"name":"split project","template_id":"template-demo"}`, http.StatusCreated},
		{"members", http.MethodPut, "/api/projects/project-1/members", `{"expected_project_version":1,"members":[]}`, http.StatusOK},
		{"chapter", http.MethodPost, "/api/projects/project-1/chapters/chapter-1/versions", `{"expected_spec_revision":1,"body_markdown":"draft","source_ids":[]}`, http.StatusCreated},
		{"template", http.MethodPut, "/api/projects/project-1/template-copy", `{"expected_project_version":1,"expected_template_copy_version":1,"fields":[],"sections":[],"terms":[],"required_fields":[],"rules":[]}`, http.StatusOK},
		{"change-set", http.MethodPost, "/api/projects/project-1/change-sets/changes-1/reassess", "", http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := len(write.writes)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
			request.Header.Set("Idempotency-Key", "split-"+test.name)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if len(read.writes) != 0 {
				t.Fatalf("a write was dispatched through Read: %v", read.writes)
			}
			if test.name == "read" {
				if !read.called || write.called || !strings.Contains(response.Body.String(), "read-project") {
					t.Fatalf("read did not use the Read instance: %s", response.Body.String())
				}
			} else if len(write.writes) != before+1 || write.writes[before] != test.name || !strings.Contains(response.Body.String(), "write-project") {
				t.Fatalf("write did not reach the intended Write instance: calls=%v body=%s", write.writes, response.Body.String())
			}
		})
	}
}

func TestExplicitProjectPortPrecedesLegacyAndSplitDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	explicit := &createProjectApplicationStub{result: CreateProjectResult{Project: Project{ID: "explicit-project"}}}
	legacy, read, write := &splitRoutingApplication{}, &splitRoutingApplication{}, &splitRoutingApplication{}
	handler := NewHandler(HandlerDependencies{Projects: explicit, Service: legacy, Read: read, Write: write})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"name":"explicit project","template_id":"template-demo"}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "explicit-project")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !explicit.called || len(legacy.writes)+len(read.writes)+len(write.writes) != 0 || !strings.Contains(response.Body.String(), "explicit-project") {
		t.Fatalf("explicit project port was replaced: status=%d body=%s", response.Code, response.Body.String())
	}
}

func (s *splitRoutingApplication) SetReviewAnchor(context.Context, Actor, string, string, string, string, core.SetReviewAnchorInput) (json.RawMessage, int, bool, error) {
	s.writes = append(s.writes, "anchor")
	data, err := json.Marshal(map[string]string{"project_id": s.project.ID})
	return data, http.StatusOK, false, err
}
func (s *splitRoutingApplication) GetCurrentConfirmation(context.Context, Actor, string, string) (json.RawMessage, error) {
	s.confirmationReads++
	return json.Marshal(map[string]string{"project_id": s.project.ID})
}
func (s *splitRoutingApplication) ReadMachineProjectSummary(context.Context, string) (core.MachineProjectSummary, error) {
	s.machineReads++
	return core.MachineProjectSummary{ID: s.project.ID}, nil
}
func (s *splitRoutingApplication) ReadMachineChapter(context.Context, string, string) (core.MachineChapterView, error) {
	s.machineReads++
	return core.MachineChapterView{ProjectID: s.project.ID}, nil
}

func TestSplitExtensionPortsKeepAnchorWritesAndMachineReadsSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, explicitLegacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "split ports", true: "explicit compatibility service"}[explicitLegacy], func(t *testing.T) {
			read := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "read-project"}}}
			write := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "write-project"}}}
			legacy := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "legacy-project"}}}
			deps := HandlerDependencies{Read: read, Write: write}
			if explicitLegacy {
				deps.Service = legacy
			}
			handler := NewHandler(deps)
			engine := gin.New()
			handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
			handler.RegisterMachineReadRoutes(engine.Group("/machine"))
			ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
			ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
			for _, test := range []struct {
				method, path, body string
				write              bool
			}{
				{http.MethodPut, "/api/projects/p/chapters/c/working-copy/review-items/item/anchor", `{"anchor":null}`, true},
				{http.MethodGet, "/api/projects/p/chapters/c/confirmations/current", "", false},
				{http.MethodGet, "/machine/projects/p/machine-read", "", false},
				{http.MethodGet, "/machine/projects/p/chapters/c/machine-read", "", false},
			} {
				request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
				request.Header.Set("Idempotency-Key", "anchor-split")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				want := "read-project"
				if test.write {
					want = "write-project"
				}
				if explicitLegacy {
					want = "legacy-project"
				}
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
					t.Fatalf("%s %s: wrong extension dependency, status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
				}
			}
			if explicitLegacy {
				if len(legacy.writes) != 1 || legacy.confirmationReads != 1 || legacy.machineReads != 2 || len(read.writes)+len(write.writes)+read.confirmationReads+write.confirmationReads+read.machineReads+write.machineReads != 0 {
					t.Fatalf("explicit legacy extension calls were not preserved: read=%+v write=%+v legacy=%+v", read, write, legacy)
				}
			} else if len(read.writes) != 0 || len(write.writes) != 1 || write.writes[0] != "anchor" || read.confirmationReads != 1 || write.confirmationReads != 0 || read.machineReads != 2 || write.machineReads != 0 {
				t.Fatalf("extension reads/writes crossed injected services: read=%+v write=%+v", read, write)
			}
		})
	}
}
