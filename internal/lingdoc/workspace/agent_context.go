package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// G2Application is an additive transport port. Existing workspace mocks retain
// their stable ApplicationService contract; the new HTTP adapter consumes this.
type G2Application interface {
	SaveResearchTask(context.Context, Actor, string, string, core.SaveResearchTaskInput) (json.RawMessage, int, bool, error)
	ListResearchTasks(context.Context, Actor, string) ([]core.ResearchTask, error)
	ApproveResearchTask(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	ControlResearchTask(context.Context, Actor, string, string, string, core.ControlResearchTaskInput) (json.RawMessage, int, bool, error)
	RunResearchTask(context.Context, Actor, string, string, string, core.RunResearchTaskInput) (json.RawMessage, int, bool, error)
	GetResearchRun(context.Context, Actor, string, string) (core.ResearchRun, error)
	InspectResearchResult(context.Context, Actor, string, string, string) (core.ResearchResult, error)
	ListResearchRuns(context.Context, Actor, string) ([]core.ResearchRun, error)
	CreateImportCandidate(context.Context, Actor, string, string, core.CreateImportCandidateInput) (json.RawMessage, int, bool, error)
	ConfirmImportCandidate(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	RejectImportCandidate(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	RefreshImportCandidate(context.Context, Actor, string, string) (core.ImportCandidate, error)
	ListImportCandidates(context.Context, Actor, string) ([]core.ImportCandidate, error)
	SaveSourceAnchor(context.Context, Actor, string, string, core.SaveSourceAnchorInput) (json.RawMessage, int, bool, error)
	ListSourceAnchors(context.Context, Actor, string) ([]core.SourceAnchor, error)
	SaveResearchClaim(context.Context, Actor, string, string, core.SaveResearchClaimInput) (json.RawMessage, int, bool, error)
	ListResearchClaims(context.Context, Actor, string) ([]core.ResearchClaim, error)
	SaveEvidenceCard(context.Context, Actor, string, string, core.SaveEvidenceCardInput) (json.RawMessage, int, bool, error)
	ConfirmEvidenceCard(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	ReviewEvidenceCard(context.Context, Actor, string, string, string, core.ReviewEvidenceCardInput) (json.RawMessage, int, bool, error)
	ListEvidenceCards(context.Context, Actor, string) ([]core.EvidenceCard, error)
	CreateAgentSession(context.Context, Actor, string, string, string, core.G2Expectation) (json.RawMessage, int, bool, error)
	ListAgentSessions(context.Context, Actor, string, string) ([]core.AgentSession, error)
	GetAgentTurns(context.Context, Actor, string, string) ([]core.AgentTurn, error)
	SendAgentTurn(context.Context, Actor, string, string, string, core.AgentTurnInput) (json.RawMessage, int, bool, error)
	BuildAgentContext(context.Context, Actor, string, core.AgentContextInput) (core.AgentContextPackage, error)
	CreateActionProposal(context.Context, Actor, string, string, core.CreateActionProposalInput) (json.RawMessage, int, bool, error)
	ConfirmActionProposal(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	RejectActionProposal(context.Context, Actor, string, string, string, core.G2ConfirmedInput) (json.RawMessage, int, bool, error)
	ListActionProposals(context.Context, Actor, string) ([]core.ActionProposal, error)
	SelectGenerationEvidence(context.Context, Actor, string, string, []core.G2VersionRef, bool) (core.G2EvidenceSelection, error)
}

type G2ActionExecutor struct {
	projects *core.Service
	sources  SourceApplicationService
	exports  ExportApplication
}

func NewG2ActionExecutor(projects *core.Service, sources SourceApplicationService, exports ExportApplication) core.G2ActionExecutor {
	return &G2ActionExecutor{projects: projects, sources: sources, exports: exports}
}
func g2ActionInput(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return core.ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return core.ErrInvalidRequest
	}
	return nil
}
func (e *G2ActionExecutor) ExecuteAgentAction(ctx context.Context, actor core.Actor, proposal core.ActionProposal, key string) (json.RawMessage, error) {
	if e.projects == nil {
		return nil, core.ErrG2Unavailable
	}
	var raw json.RawMessage
	var err error
	switch proposal.Action {
	case "save_spec":
		var input core.SaveSpecInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		raw, _, _, err = e.projects.SaveSpec(ctx, actor, proposal.ProjectID, key, input)
	case "bind_asset":
		if e.sources == nil {
			return nil, core.ErrG2Unavailable
		}
		var input struct {
			KnowledgeID string `json:"knowledge_id"`
		}
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		asset, _, cause := e.sources.BindAsset(ctx, actor, proposal.ProjectID, key, input.KnowledgeID)
		if cause != nil {
			return nil, cause
		}
		raw, err = json.Marshal(asset)
	case "create_evidence_card":
		var input core.SaveEvidenceCardInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		input.Confirmed = true
		raw, _, _, err = e.projects.SaveEvidenceCard(ctx, actor, proposal.ProjectID, key, input)
	case "confirm_evidence_card":
		var input core.G2ConfirmedInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		input.Confirmed = true
		raw, _, _, err = e.projects.ConfirmEvidenceCard(ctx, actor, proposal.ProjectID, proposal.TargetID, key, input)
	case "apply_change_set":
		var input struct{}
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		raw, _, _, err = e.projects.ApplyChangeSet(ctx, actor, proposal.ProjectID, proposal.TargetID, key)
	case "commit_working_copy":
		var input core.CommitWorkingCopyInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		raw, _, _, err = e.projects.CommitWorkingCopy(ctx, actor, proposal.ProjectID, proposal.TargetID, key, input)
	case "export":
		if e.exports == nil {
			return nil, core.ErrG2Unavailable
		}
		var input struct{}
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		artifact, _, cause := e.exports.Start(ctx, actor.UserID, proposal.ProjectID, proposal.TargetID, key)
		if cause != nil {
			return nil, cause
		}
		raw, err = json.Marshal(artifact)
	default:
		return nil, core.ErrInvalidRequest
	}
	return raw, err
}

var _ G2Application = (*core.Service)(nil)
var _ core.G2ActionExecutor = (*G2ActionExecutor)(nil)
