package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// fullWorkflowGuardPortsFor wires the aggregate into the six narrow ports of
// the full workflow guard bundle.
func fullWorkflowGuardPortsFor(service *core.Service) FullWorkflowGuardPorts {
	return FullWorkflowGuardPorts{
		Delivery:   service,
		Provenance: service,
		Impact:     service,
		Runtime:    service,
		Actions:    service,
		Compose:    service,
	}
}
