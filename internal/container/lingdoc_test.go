package container

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
	"gorm.io/gorm"
	"testing"
)

func TestLingDocWorkspaceCompositionSharesApplicationAndSourceRuntime(t *testing.T) {
	c := dig.New()
	for _, constructor := range []any{
		func() *gorm.DB { return &gorm.DB{} },
		func() interfaces.KBShareService { return nil },
		func() interfaces.KnowledgeBaseService { return nil },
		func() interfaces.AuditLogService { return nil },
		func() interfaces.ModelService { return nil },
		func() interfaces.KnowledgeService { return nil },
		func() interfaces.WebSearchService { return nil },
		NewLingDocTemplateCatalog,
		NewLingDocWorkspaceHandler,
	} {
		if err := c.Provide(constructor); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Invoke(func(h *workspace.Handler, ruleSets *rulesetstore.HTTPHandler, authorizer workspacecore.CapabilityAuthorizer, projects workspacecore.ProjectApplication, integration workspace.WorkspaceIntegration, runtime *workspace.SourceRuntime) {
		if h == nil || ruleSets == nil || authorizer == nil || projects == nil || runtime == nil || integration != runtime {
			t.Fatal("workspace consumers did not receive the shared application/runtime ports")
		}
		if integration.CandidateAdoptionSourcePolicy() == nil || integration.WorkspaceSourcePolicy() == nil || integration.DeliveryInputBuilder() == nil {
			t.Fatal("shared runtime is missing a source or delivery integration")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLingDocWorkspaceCompositionRequiresTemplateCatalog(t *testing.T) {
	if _, err := NewLingDocWorkspaceHandler(&gorm.DB{}, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing template catalog accepted")
	}
}

// The workspace handler is assembled at startup, so a port it needs but does
// not have must fail here rather than on the first request that reaches the
// route family behind it.
func TestLingDocWorkspaceCompositionRequiresEveryPortTheRoutesNeed(t *testing.T) {
	templates := NewLingDocTemplateCatalog()

	// The complete composition starts.
	workspace, err := NewLingDocWorkspaceHandler(&gorm.DB{}, nil, nil, nil, nil, nil, nil, templates)
	if err != nil {
		t.Fatalf("the complete composition = %v, want no error", err)
	}
	if workspace.Handler == nil {
		t.Fatal("the complete composition produced no handler")
	}
}

// The production composition is the one that ships, so its route surface is
// the surface that must not change.
//
// The base tree measured the same 48 routes for the same production
// composition, so making the optional capabilities explicit did not add or
// remove an endpoint. The optional routes are named here so that a future
// change that quietly drops one of them is reported by name.
//
// The project archive entry adds one route of its own (PF-4, Spec 49). It is
// named here for the same reason the others are: an endpoint that appears
// without being declared is reported by name, and an endpoint that is dropped
// is reported by name too. The routes that were already there are unchanged.
// The G6 runtime endpoints (the alert rule read-back and the alert rule
// action entry, PF-8 / #382) are **not** in this surface: the impact center
// route family that carries them is registered by RegisterFullRoutes, and
// this composition registers the core families and the machine read only.
// So this snapshot's count does not move when that family gains endpoints.
func TestLingDocWorkspaceCompositionKeepsItsRouteSurface(t *testing.T) {
	templates := NewLingDocTemplateCatalog()
	composed, err := NewLingDocWorkspaceHandler(&gorm.DB{}, nil, nil, nil, nil, nil, nil, templates)
	if err != nil {
		t.Fatalf("the complete composition = %v, want no error", err)
	}
	engine := gin.New()
	composed.Handler.Register(workspace.RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	composed.Handler.RegisterMachineReadRoutes(engine.Group("/api"))

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	// The optional capabilities the production composition declares by name.
	for _, path := range []string{
		"PUT /api/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor",
		"GET /api/projects/:projectId/chapters/:chapterId/confirmations/current",
		"GET /api/projects/:projectId/machine-read",
		"GET /api/projects/:projectId/chapters/:chapterId/machine-read",
		"GET /api/projects/:projectId/rewrite-candidates/:candidateId",
		"POST /api/projects/:projectId/chapters/:chapterId/rewrite-candidates",
		"POST /api/projects/:projectId/chapters/:chapterId/rewrite-candidates/:candidateId/apply",
		"POST /api/projects/:projectId/chapters/:chapterId/review-reports",
		"GET /api/projects/:projectId/review-reports/:reportId",
		"POST /api/projects/:projectId/review-reports/:reportId/reject",
		"POST /api/projects/:projectId/chapters/:chapterId/review-reports/:reportId/apply",
		"POST /api/projects/:projectId/archive",
		"PUT /api/projects/:projectId/template-copy/legacy-rule-overrides",
		"POST /api/projects/:projectId/template-copy/field-archives",
		"GET /api/projects/:projectId/template-copy/field-archives",
		"GET /api/projects/:projectId/template-copy/field-archives/:archiveId",
		"POST /api/projects/:projectId/template-copy/field-archives/:archiveId/remove",
	} {
		if !routes[path] {
			t.Errorf("the production composition does not serve %s although the capability is declared", path)
		}
	}
	if got := len(engine.Routes()); got != 59 {
		t.Fatalf("the production composition registered %d routes, want 59", got)
	}
}

func TestLingDocDeliveryCompositionRequiresProtectionPorts(t *testing.T) {
	if _, err := NewLingDocDelivery(DeliveryDependencies{}); err == nil {
		t.Fatal("missing protection ports accepted")
	}
	deps := DeliveryDependencies{Snapshots: delivery.NewMemorySnapshotStore(), Exports: delivery.NewMemoryExportStore(), Currentness: delivery.CurrentnessFunc(func(delivery.DeliveryInput) (bool, error) { return true, nil }), Access: delivery.ExportAccessFunc(func(string, string) error { return nil })}
	deps.Validator = delivery.FrozenValidatorFunc(func(delivery.DeliveryInput, []byte) error { return nil })
	withoutValidator := deps
	withoutValidator.Validator = nil
	if _, err := NewLingDocDelivery(withoutValidator); err == nil {
		t.Fatal("missing frozen-file validator accepted")
	}
	app, err := NewLingDocDelivery(deps)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.Releases.Prepare(delivery.DeliveryInput{ProjectID: "composition-test", DeliveryKind: "internal_demo", Template: delivery.DemoTemplate()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.Exports.Start("fixture", snapshot.ProjectID, snapshot.ID, "fixture-start"); err != delivery.ErrExportPreflightBlocked {
		t.Fatalf("factory bypasses checks: %v", err)
	}
}

func TestLingDocDeliveryInterfacesResolveInContainer(t *testing.T) {
	c := dig.New()
	for _, constructor := range []any{
		func() delivery.SnapshotStore { return delivery.NewMemorySnapshotStore() },
		func() delivery.ExportStore { return delivery.NewMemoryExportStore() },
		func() delivery.FrozenValidator {
			return delivery.FrozenValidatorFunc(func(delivery.DeliveryInput, []byte) error { return nil })
		},
		func() delivery.CurrentnessChecker {
			return delivery.CurrentnessFunc(func(delivery.DeliveryInput) (bool, error) { return true, nil })
		},
		func() delivery.ExportAccessChecker {
			return delivery.ExportAccessFunc(func(string, string) error { return nil })
		},
		NewLingDocDelivery,
	} {
		if err := c.Provide(constructor); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Invoke(func(releases delivery.ReleaseApplication, exports delivery.ExportApplication) {
		if releases == nil || exports == nil {
			t.Fatal("missing application ports")
		}
	}); err != nil {
		t.Fatal(err)
	}
}
