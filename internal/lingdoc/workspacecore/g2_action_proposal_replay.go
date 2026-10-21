package workspacecore

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// Only a completed creation operation may skip today's optimistic version
// checks. Reconstruct its server-canonical origin/source set, then let the
// existing G2 engine compare the original hash and reauthorize the receipt.
func (s *Service) replayAgentChangeDraftProposal(ctx context.Context, actor Actor, projectID, key string, input CreateActionProposalInput) (json.RawMessage, int, bool, error) {
	if !validActor(actor) || !operationcore.ValidKey(key) {
		return nil, 0, false, ErrInvalidRequest
	}
	request, err := DecodeAgentChangeDraftInput(projectID, input.TargetID, input.ExpectedContextRevision, input.Input)
	if err != nil {
		return nil, 0, false, err
	}
	identity := OperationIdentity{Actor: actor, Operation: "createActionProposal", Target: projectID + "/" + input.TargetID, Key: key}
	var proposal ActionProposal
	var sourceIDs []string
	found := false
	err = s.g2Read(ctx, actor, projectID, "agent", func(tx Transaction, p Project) error {
		previous, exists, err := tx.Operation(identity)
		if err != nil || !exists {
			return err
		}
		found = true
		if err := json.Unmarshal(previous.Body, &proposal); err != nil {
			return markReplayPayloadDecodeError(err)
		}
		if proposal.Action != "create_change_set" || proposal.ProjectID != projectID || proposal.TargetID != input.TargetID || proposal.CreatedBy != actor.UserID {
			return ErrIdempotencyConflict
		}
		// Typed turn/session identity, version and per-key suggestion remain
		// checked. Only equality with today's project basis is a cache-miss rule.
		current, err := OperationSourceIDs(tx, string(operationCreateChangeDraft), projectID, projectID, request.ExtendedChangeSetInput)
		if err != nil {
			return err
		}
		turnSources, err := agentChangeDraftSourcesAtBasis(tx, actor, p, request, false)
		sourceIDs = g2IDs(append(current, turnSources...))
		return err
	})
	if err != nil || !found {
		return nil, 0, false, err
	}
	input.Input, err = json.Marshal(request)
	if err != nil {
		return nil, 0, false, err
	}
	// Use exactly the normal creation canonicalization for hashing. Never
	// force the receipt's source union onto a changed incoming payload; the
	// existing G2 engine separately rechecks the receipt's historical sources.
	input.SourceIDs = g2IDs(append(input.SourceIDs, sourceIDs...))
	return s.g2Operation(ctx, actor, projectID, "agent", "createActionProposal", identity.Target, key, input, input.SourceIDs, operationcore.HumanGateNotRequired, func(Transaction, Project) (any, int, error) {
		// If the previously observed operation disappeared, this is no longer
		// a replay. Never execute an old request against a new baseline here.
		return nil, 0, ErrVersionConflict
	})
}
