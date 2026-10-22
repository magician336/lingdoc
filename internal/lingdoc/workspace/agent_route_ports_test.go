package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// agentRoutePortsFor wires the aggregate into the nine narrow context ports of the
// agent route bundle.
//
// The helper lives in a test file on purpose: the transport layer must not name
// the aggregate concrete type, and the composition root is the only production
// place that maps the aggregate onto the bundle.
func agentRoutePortsFor(service *core.Service) AgentRoutePorts {
	return AgentRoutePorts{
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
