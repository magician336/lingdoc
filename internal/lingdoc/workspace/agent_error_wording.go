package workspace

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/clienterror"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// The agent surface's own wording.
//
// The workspace core declares these failures for every surface that reports
// them, and the agent surface words four of them differently. The form is decided
// by the situation, so the agent surface declares its own scenario values and
// wraps the core failures: the core failure keeps its own form for the other
// surfaces, and errors.Is still finds it.
//
// The internal text of each failure keeps its historical wording: it is what
// the domain and the audit trail read, and rewriting it would rewrite what
// those records say. Only the names above changed.
var (
	agentFormalChangeRequired = clienterror.Wrap(clienterror.Form{Status: 422, Code: "invalid_state", Message: "该对象已确认或进入正式依据，请在“正式变更”中建立草稿，评估后由 Owner 应用。", Retryable: false}, "g2: the object needs a formal change", core.ErrFormalChangeRequired)
	agentEvidenceIncomplete   = clienterror.Wrap(clienterror.Form{Status: 422, Code: "evidence_incomplete", Message: "定位、来源、主张或当前版本尚未满足证据要求。", Retryable: false}, "g2: the evidence is incomplete", core.ErrG2EvidenceIncomplete)
	agentOutcomeUnknown       = clienterror.Wrap(clienterror.Form{Status: 409, Code: "outcome_unknown", Message: "操作结果尚待核对，请读取已有回执，不要重复执行。", Retryable: false}, "g2: the outcome is not known yet", core.ErrG2OutcomeUnknown)
	agentUnavailable          = clienterror.Wrap(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "所需资料、搜索或模型能力暂不可用。", Retryable: false}, "g2: a dependency is unavailable", core.ErrG2Unavailable)
)

// agentSurfaceError presents a core failure the way the agent surface words it.
//
// The presentation belongs to the surface, so it is applied at the surface's
// own handler boundary, not in a transport table.
func agentSurfaceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrFormalChangeRequired):
		return agentFormalChangeRequired
	case errors.Is(err, core.ErrG2EvidenceIncomplete):
		return agentEvidenceIncomplete
	case errors.Is(err, core.ErrG2OutcomeUnknown):
		return agentOutcomeUnknown
	case errors.Is(err, core.ErrG2Unavailable):
		return agentUnavailable
	}
	return err
}
