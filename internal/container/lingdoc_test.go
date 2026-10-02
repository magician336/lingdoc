package container

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
		NewLingDocWorkspaceHandler,
	} {
		if err := c.Provide(constructor); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Invoke(func(h *workspace.Handler, projects workspace.ApplicationService, integration workspace.WorkspaceIntegration, runtime *workspace.SourceRuntime) {
		if h == nil || projects == nil || runtime == nil || integration != runtime || h.Service() != projects {
			t.Fatal("workspace consumers did not receive the shared application/runtime ports")
		}
		if integration.CandidateAdoptionSourcePolicy() == nil || integration.WorkspaceSourcePolicy() == nil || integration.DeliveryInputBuilder() == nil {
			t.Fatal("shared runtime is missing a source or delivery integration")
		}
	}); err != nil {
		t.Fatal(err)
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
