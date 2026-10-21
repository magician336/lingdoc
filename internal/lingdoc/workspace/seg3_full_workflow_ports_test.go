package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// fullWorkflowRoutePortsFor wires the aggregate into the three narrow ports of
// the full workflow route bundle.
//
// The helper lives in a test file on purpose: the transport layer must not name
// the aggregate concrete type, and the composition root is the only production
// place that maps the aggregate onto the bundle.
func fullWorkflowRoutePortsFor(service *core.Service) FullWorkflowRoutePorts {
	return FullWorkflowRoutePorts{
		Members:   service,
		Reviews:   service,
		Lifecycle: service,
	}
}

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
