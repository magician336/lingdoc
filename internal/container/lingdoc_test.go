package container

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
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
