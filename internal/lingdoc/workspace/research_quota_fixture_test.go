package workspace

import (
	"context"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type httpResearchQuotaPolicy struct{ value core.ResearchQuotaPolicy }

func (p *httpResearchQuotaPolicy) ResearchQuotaPolicy(context.Context, core.Actor, string) (core.ResearchQuotaPolicy, error) {
	return p.value, nil
}
