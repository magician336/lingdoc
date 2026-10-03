package workspace

import (
	"context"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
)

type DeliveryInputAssembler interface {
	Build(context.Context, string, candidateadoption.WorkspaceDeliveryInput) (delivery.DeliveryInput, error)
}

// DeliveryCurrentness rechecks the frozen source records at read/export time;
// chapter/project versions alone cannot detect revocation or re-parsing.
type DeliveryCurrentness interface {
	Current(context.Context, string, delivery.DeliveryInput) (bool, error)
}

type ReleaseApplication interface {
	Check(context.Context, string, string, int64) (delivery.CheckResult, error)
	Prepare(context.Context, string, string, string, int64) (delivery.ReleaseSnapshot, bool, error)
	Get(context.Context, string, string, string) (delivery.ReleaseSnapshot, error)
	List(context.Context, string, string) ([]delivery.ReleaseSnapshot, bool, error)
}
type ExportApplication interface {
	Start(context.Context, string, string, string, string) (delivery.ExportArtifact, bool, error)
	Get(context.Context, string, string, string) (delivery.ExportArtifact, error)
	Download(context.Context, string, string, string) (delivery.ExportArtifact, []byte, error)
	List(context.Context, string, string) ([]delivery.ExportArtifact, bool, error)
}

var _ ReleaseApplication = (*DeliveryReleaseService)(nil)
var _ ExportApplication = (*DeliveryExportService)(nil)
