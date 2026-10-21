package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// impactPortsFor wires the aggregate into the six narrow ports of the impact
// centre bundle.
//
// The helper lives in a test file on purpose: the transport layer must not name
// the aggregate concrete type, and the composition root is the only production
// place that maps the aggregate onto the bundle.
func impactPortsFor(service *core.Service) ImpactRoutePorts {
	return ImpactRoutePorts{
		ChangeSets: service,
		Impact:     service,
		Dependency: service,
		Runtime:    service,
		Trials:     service,
		Roadmap:    service,
	}
}
