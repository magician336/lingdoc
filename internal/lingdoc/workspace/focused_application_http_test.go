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
			handler, err := NewHandler(HandlerDependencies{
				Projects:    &focusedProjectPortStub{},
				Members:     &focusedMembershipPortStub{},
				Chapters:    &focusedChapterPortStub{},
				TemplateApp: &focusedTemplatePortStub{},
				ChangeSets:  app,
				Sources:     emptySourcesStub{},
			})
			if err != nil {
				t.Fatalf("assemble the workspace handler: %v", err)
			}
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
	deps := HandlerDependencies{
		Projects:    projects,
		Members:     &focusedMembershipPortStub{},
		Chapters:    &focusedChapterPortStub{},
		TemplateApp: &focusedTemplatePortStub{},
		ChangeSets:  &focusedChangeSetPortStub{},
		Sources:     emptySourcesStub{},
	}
	// The machine read capability is named by the caller, not inferred from
	// the broad service.
	deps.WithMachineReads(legacy)
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	if handler.machineReads != legacy || handler.projects != projects {
		t.Fatalf("G5/G7 bridge replaced an explicitly supplied focused port: machine=%T projects=%T", handler.machineReads, handler.projects)
	}
}

// The service double below satisfies the five context ports. The legacy wide
// compatibility interface is gone: the ports are named per context now.
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

// Each context port serves the routes of its own context, and a write of one
// context never reaches the port of another.
//
// The legacy read/write split is retired. The ports are per context now, so
// the guarantee is the same one level up: an operation reaches the port of
// the context that owns it, and no other.
func TestEachContextPortServesOnlyItsOwnFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projects := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "project-1"}}}
	members := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "member-project"}}}
	chapters := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "chapter-project"}}}
	templates := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "template-project"}}}
	changeSets := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "changeset-project"}}}
	deps := HandlerDependencies{
		Projects:    projects,
		Members:     members,
		Chapters:    chapters,
		TemplateApp: templates,
		ChangeSets:  changeSets,
		Sources:     emptySourcesStub{},
	}
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	ports := []*splitRoutingApplication{projects, members, chapters, templates, changeSets}
	for _, test := range []struct {
		name, method, path, body string
		status                   int
		port                     *splitRoutingApplication
		marker                   string
	}{
		{"read", http.MethodGet, "/api/projects", "", http.StatusOK, projects, "project-1"},
		{"project", http.MethodPost, "/api/projects", `{"name":"split project","template_id":"template-demo"}`, http.StatusCreated, projects, "project-1"},
		{"members", http.MethodPut, "/api/projects/project-1/members", `{"expected_project_version":1,"members":[]}`, http.StatusOK, members, "member-project"},
		{"chapter", http.MethodPost, "/api/projects/project-1/chapters/chapter-1/versions", `{"expected_spec_revision":1,"body_markdown":"draft","source_ids":[]}`, http.StatusCreated, chapters, "chapter-project"},
		{"template", http.MethodPut, "/api/projects/project-1/template-copy", `{"expected_project_version":1,"expected_template_copy_version":1,"fields":[],"sections":[],"terms":[],"required_fields":[],"rules":[]}`, http.StatusOK, templates, "template-project"},
		{"change-set", http.MethodPost, "/api/projects/project-1/change-sets/changes-1/reassess", "", http.StatusCreated, changeSets, "changeset-project"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := map[*splitRoutingApplication]int{}
			for _, port := range ports {
				before[port] = len(port.writes)
			}
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
			request.Header.Set("Idempotency-Key", "split-"+test.name)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			for _, port := range ports {
				grew := len(port.writes) - before[port]
				want := 0
				if port == test.port && test.name != "read" {
					want = 1
				}
				if grew != want {
					t.Fatalf("the %s route reached the wrong context port: writes=%v", test.name, port.writes)
				}
			}
			if test.name == "read" {
				if !projects.called || !strings.Contains(response.Body.String(), test.marker) {
					t.Fatalf("read did not use the project port: %s", response.Body.String())
				}
				return
			}
			if !strings.Contains(response.Body.String(), test.marker) {
				t.Fatalf("the %s route did not answer from its own context port: %s", test.name, response.Body.String())
			}
		})
	}
}

// The explicitly supplied project port is the one that serves the project
// routes; the other context ports are not consulted for them.
func TestExplicitProjectPortServesTheProjectRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	explicit := &createProjectApplicationStub{result: CreateProjectResult{Project: Project{ID: "explicit-project"}}}
	others := &splitRoutingApplication{}
	deps := HandlerDependencies{
		Projects:    explicit,
		Members:     others,
		Chapters:    others,
		TemplateApp: others,
		ChangeSets:  others,
		Sources:     emptySourcesStub{},
	}
	// The optional capabilities are named by the caller.
	deps.WithMachineReads(others)
	deps.WithReviewAnchors(others, others)
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"name":"explicit project","template_id":"template-demo"}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "explicit-project")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !explicit.called || len(others.writes) != 0 || !strings.Contains(response.Body.String(), "explicit-project") {
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

// The review anchor and machine read capabilities keep their own read and
// write ports: the anchor write goes to the write port, the anchor read and
// the machine reads go to the read port.
func TestExtensionPortsKeepAnchorWritesAndMachineReadsSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	read := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "read-project"}}}
	write := &splitRoutingApplication{applicationServiceStub: applicationServiceStub{project: Project{ID: "write-project"}}}
	deps := HandlerDependencies{
		Projects:    read,
		Members:     read,
		Chapters:    read,
		TemplateApp: read,
		ChangeSets:  read,
		Sources:     emptySourcesStub{},
	}
	// The anchor and machine read capabilities are named by the caller. The
	// anchor write goes to the write port and the anchor read and the
	// machine reads go to the read port, which is what this test pins.
	deps.WithReviewAnchors(read, write)
	deps.WithMachineReads(read)
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
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
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s %s: wrong extension dependency, status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if len(read.writes) != 0 || len(write.writes) != 1 || write.writes[0] != "anchor" || read.confirmationReads != 1 || write.confirmationReads != 0 || read.machineReads != 2 || write.machineReads != 0 {
		t.Fatalf("extension reads/writes crossed injected services: read=%+v write=%+v", read, write)
	}
}
