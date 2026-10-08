package workspace

import (
	"context"
	"encoding/json"
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

// ValidationIssueDispositionCore persists immutable decisions through the
// workspace audit transaction. Kept optional so older test adapters and
// deployments without issue controls retain the stable T13 contract.
type ValidationIssueDispositionCore interface {
	ReplayValidationIssueDisposition(context.Context, Actor, string, string, string, string, string, int64) (json.RawMessage, int, bool, bool, error)
	RecordValidationIssueDisposition(context.Context, Actor, string, string, ValidationIssueDispositionInput) (json.RawMessage, int, bool, error)
	ValidationIssueDispositions(context.Context, Actor, string, []ValidationIssueBinding) (map[string]ValidationIssueDisposition, error)
}

type IssueDispositionRequest struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Action                 string `json:"action"`
	Reason                 string `json:"reason"`
}

type IssueDispositionApplication interface {
	SetIssueDisposition(context.Context, string, string, string, string, IssueDispositionRequest) (ValidationIssueDisposition, bool, error)
}

// TemplateCheckApplication exposes explainable G4 rule evidence without
// changing the stable T13 CheckResult schema or granting export permission.
type TemplateCheckApplication interface {
	TemplateCheck(context.Context, string, string, int64) (delivery.TemplateCheckResult, error)
}
type ExportApplication interface {
	Start(context.Context, string, string, string, string) (delivery.ExportArtifact, bool, error)
	Get(context.Context, string, string, string) (delivery.ExportArtifact, error)
	Download(context.Context, string, string, string) (delivery.ExportArtifact, []byte, error)
	List(context.Context, string, string) ([]delivery.ExportArtifact, bool, error)
}

var _ ReleaseApplication = (*DeliveryReleaseService)(nil)
var _ TemplateCheckApplication = (*DeliveryReleaseService)(nil)
var _ ExportApplication = (*DeliveryExportService)(nil)
