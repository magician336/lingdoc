package container

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"go.uber.org/dig"
	"testing"
)

func TestLingDocDeliveryCompositionRequiresProtectionPorts(t *testing.T) {
	if _, err := NewLingDocDelivery(DeliveryDependencies{}); err == nil {
		t.Fatal("missing protection ports accepted")
	}
	deps := DeliveryDependencies{Snapshots: delivery.NewMemorySnapshotStore(), Exports: delivery.NewMemoryExportStore(), Currentness: delivery.CurrentnessFunc(func(delivery.DeliveryInput) (bool, error) { return true, nil }), Access: delivery.ExportAccessFunc(func(string, string) error { return nil })}
	app, err := NewLingDocDelivery(deps)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.Releases.Prepare(delivery.DeliveryInput{ProjectID: "composition-test", DeliveryKind: "internal_demo", Template: delivery.DemoTemplate()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exports.Start("fixture", snapshot.ProjectID, snapshot.ID); err != delivery.ErrExportPreflightBlocked {
		t.Fatalf("factory bypasses checks: %v", err)
	}
}

func TestLingDocDeliveryInterfacesResolveInContainer(t *testing.T) {
	c := dig.New()
	for _, constructor := range []any{
		func() delivery.SnapshotStore { return delivery.NewMemorySnapshotStore() },
		func() delivery.ExportStore { return delivery.NewMemoryExportStore() },
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
