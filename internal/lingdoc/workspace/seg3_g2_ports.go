package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// G2RoutePorts 是 G2 路由这条面依赖的九个上下文窄口。
//
// 一条面可以依赖多个上下文；束只是把「这条面需要哪几门语言」写在一处，
// 它不把任何一个口变宽。
type G2RoutePorts struct {
	Research      core.ResearchTaskApplication
	Imports       core.ImportCandidateApplication
	EvidenceCards core.EvidenceCardApplication
	Claims        core.ResearchClaimApplication
	Anchors       core.SourceAnchorApplication
	AgentSessions core.AgentSessionApplication
	Proposals     core.ActionProposalApplication
	Context       core.AgentContextApplication
	ChangeSets    core.ChangeSetDraftApplication
}
