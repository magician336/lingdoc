package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// connectAggregateCapabilities wires the four optional capabilities the way
// the production composition root does.
//
// The transport used to discover them with a run-time type assertion on the
// project service. The production value of the project service is the
// aggregate, and the aggregate answers all four capabilities, so a fixture
// that wires the aggregate has to hand the four over explicitly. Otherwise
// the fixture would be asserting something the production assembly never
// does: a workspace with no provenance and no evidence selection.
func connectAggregateCapabilities(runtime *SourceRuntime, aggregate *core.Service) {
	runtime.ConnectOptionalCapabilities(OptionalCapabilities{
		Provenance:                     aggregate,
		EvidenceSelection:              aggregate,
		EvidenceSelectionInTransaction: aggregate,
		EvidenceProofPreparation:       aggregate,
	})
}
