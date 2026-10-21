package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type G2ActionExecutor struct {
	actions core.AgentActionApplication
	sources SourceApplicationService
	exports ExportApplication
}

func NewG2ActionExecutor(actions core.AgentActionApplication, sources SourceApplicationService, exports ExportApplication) core.G2ActionExecutor {
	return &G2ActionExecutor{actions: actions, sources: sources, exports: exports}
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
	if nilPort(e.actions) {
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
		raw, _, _, err = e.actions.SaveSpec(ctx, actor, proposal.ProjectID, key, input)
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
		raw, _, _, err = e.actions.SaveEvidenceCard(ctx, actor, proposal.ProjectID, key, input)
	case "confirm_evidence_card":
		var input core.G2ConfirmedInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		input.Confirmed = true
		raw, _, _, err = e.actions.ConfirmEvidenceCard(ctx, actor, proposal.ProjectID, proposal.TargetID, key, input)
	case "create_change_set":
		input, cause := core.DecodeAgentChangeDraftInput(proposal.ProjectID, proposal.TargetID, proposal.Basis.ContextRevision, proposal.Input)
		if cause != nil {
			return nil, cause
		}
		prepared, cause := e.actions.PrepareAgentChangeDraft(ctx, actor, proposal, input)
		if cause != nil {
			return nil, cause
		}
		raw, _, _, err = e.actions.CreateChangeDraft(prepared, actor, proposal.ProjectID, key, input.ExtendedChangeSetInput)
	case "apply_change_set":
		var input struct{}
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		raw, _, _, err = e.actions.ApplyChangeSet(ctx, actor, proposal.ProjectID, proposal.TargetID, key)
	case "commit_working_copy":
		var input core.CommitWorkingCopyInput
		if err = g2ActionInput(proposal.Input, &input); err != nil {
			return nil, err
		}
		raw, _, _, err = e.actions.CommitWorkingCopy(ctx, actor, proposal.ProjectID, proposal.TargetID, key, input)
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

var _ core.G2ActionExecutor = (*G2ActionExecutor)(nil)
