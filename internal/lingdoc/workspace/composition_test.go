package workspace

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// selectedRewriteStub stands in for the rewrite capability. The test only
// asks whether the port was installed, so the methods are never called.
type selectedRewriteStub struct {
	SelectedRewriteApplication
}

// The composition root fails when a required port is missing.
//
// A required port backs a family of routes that cannot answer without it. A
// handler assembled without one would answer the first request that reached
// that family with a nil dereference, long after the service had started and
// looked healthy. Failing at assembly turns that into a startup error that
// names the port.
//
// The ports are the ones the routes need: the project, membership, chapter,
// template and change set families, and the source family. They are named,
// not inferred: since the legacy wide ports were retired, every one of them
// has to be handed over by name, and the assembly fails when one is absent.
func TestNewHandlerRefusesToAssembleWithoutARequiredPort(t *testing.T) {
	service := &applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}
	sources := &sourceApplicationServiceStub{}

	// The complete set assembles.
	if _, err := NewHandler(contextPorts(service, sources)); err != nil {
		t.Fatalf("a complete dependency set = %v, want no error", err)
	}

	cases := []struct {
		name    string
		deps    HandlerDependencies
		missing string
	}{
		{
			name:    "no sources",
			deps:    contextPorts(service, nil),
			missing: "sources",
		},
		{
			// Nothing but the sources port: none of the application ports can
			// be inferred, so the first missing one is reported.
			name:    "no application ports",
			deps:    HandlerDependencies{Sources: sources},
			missing: "projects",
		},
		{
			// Every port but one is present. The missing one is named,
			// whichever port it is.
			name: "no change set port",
			deps: HandlerDependencies{
				Projects:    service,
				Members:     service,
				Chapters:    service,
				TemplateApp: service,
				Sources:     sources,
			},
			missing: "change sets",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler, err := NewHandler(testCase.deps)
			if err == nil {
				t.Fatalf("a missing %s port assembled without an error", testCase.missing)
			}
			if handler != nil {
				t.Fatal("a failed assembly must not return a handler")
			}
			if !strings.Contains(err.Error(), testCase.missing) {
				t.Fatalf("the error does not name the missing port: %v", err)
			}
		})
	}
}

// The optional capabilities are the caller's decision.
//
// A capability the caller did not ask for has no route. That is a decision the
// caller made by not asking, not an accident of which field happened to be
// filled in: the route set is the same either way, and it is the same set the
// caller declared.
func TestTheRewriteEndpointsExistOnlyWhenTheCallerAsksForThem(t *testing.T) {
	service := &applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}
	sources := &sourceApplicationServiceStub{}

	without := contextPorts(service, sources)
	handler, err := NewHandler(without)
	if err != nil {
		t.Fatalf("assemble without the rewrite capability: %v", err)
	}
	if handler.rewrites != nil {
		t.Fatal("the rewrite port was installed without being asked for")
	}

	with := contextPorts(service, sources)
	with.WithSelectedRewrites(&selectedRewriteStub{})
	handler, err = NewHandler(with)
	if err != nil {
		t.Fatalf("assemble with the rewrite capability: %v", err)
	}
	if handler.rewrites == nil {
		t.Fatal("the rewrite port was asked for but not installed")
	}
}

// The optional capabilities decide which routes exist.
//
// An endpoint that exists without its capability answers "invalid state", and
// a capability that is present while its endpoint is missing makes the caller
// believe the capability is gone. The route set must follow the declared
// capability exactly: nothing more, nothing less.
func TestTheOptionalCapabilitiesDecideWhichRoutesExist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &splitRoutingApplication{}
	sources := &sourceApplicationServiceStub{}

	routesOf := func(t *testing.T, declare func(*HandlerDependencies)) map[string]bool {
		t.Helper()
		deps := contextPorts(service, sources)
		declare(&deps)
		handler, err := NewHandler(deps)
		if err != nil {
			t.Fatalf("assemble the workspace handler: %v", err)
		}
		engine := gin.New()
		handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
		handler.RegisterMachineReadRoutes(engine.Group("/api"))
		paths := map[string]bool{}
		for _, route := range engine.Routes() {
			paths[route.Method+" "+route.Path] = true
		}
		return paths
	}

	plain := routesOf(t, func(*HandlerDependencies) {})
	full := routesOf(t, func(deps *HandlerDependencies) {
		deps.WithReviewAnchors(service, service)
		deps.WithMachineReads(service)
	})

	want := []string{
		"PUT /api/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor",
		"GET /api/projects/:projectId/chapters/:chapterId/confirmations/current",
		"GET /api/projects/:projectId/machine-read",
		"GET /api/projects/:projectId/chapters/:chapterId/machine-read",
	}
	for _, path := range want {
		if plain[path] {
			t.Errorf("%s exists although the capability was not asked for", path)
		}
		if !full[path] {
			t.Errorf("%s is missing although the capability was asked for", path)
		}
	}
	for path := range plain {
		if !full[path] {
			t.Errorf("%s disappeared although it does not belong to an optional capability", path)
		}
	}
	for path := range full {
		if !plain[path] {
			if !containsRoutePath(want, path) {
				t.Errorf("%s appeared without being named in the optional capability list", path)
			}
		}
	}
}

func containsRoutePath(paths []string, path string) bool {
	for _, candidate := range paths {
		if candidate == path {
			return true
		}
	}
	return false
}
