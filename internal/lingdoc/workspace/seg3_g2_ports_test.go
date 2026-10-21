package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// g2PortsFor wires the aggregate into the nine narrow context ports of the G2
// route bundle.
//
// The helper lives in a test file on purpose: the transport layer must not name
// the aggregate concrete type, and the composition root is the only production
// place that maps the aggregate onto the bundle.
func g2PortsFor(service *core.Service) G2RoutePorts {
	return G2RoutePorts{
		Research:      service,
		Imports:       service,
		EvidenceCards: service,
		Claims:        service,
		Anchors:       service,
		AgentSessions: service,
		Proposals:     service,
		Context:       service,
		ChangeSets:    service,
	}
}
