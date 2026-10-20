package workspacecore

import (
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"slices"
	"strconv"
)

// The historical before value is only proposal input. Current version and
// policy checks run again in PinSemanticDeltas and the normal Owner apply.
func BuildInverseSemanticDeltas(tx Transaction, p Project, change ChangeSet) ([]SemanticDelta, error) {
	out := []SemanticDelta{}
	for _, delta := range change.SemanticDeltas {
		current, err := currentSemanticValue(tx, p, delta)
		if err != nil {
			return nil, err
		}
		version, err := strconv.ParseInt(current.Ref.Version, 10, 64)
		if err != nil {
			return nil, err
		}
		// Evidence source confirmation appends a lifecycle version without
		// changing the relation. Its exact meaning is checked below, then the
		// inverse pins this current version and rechecks all current sources.
		if len(delta.Before) == 0 || version < delta.ExpectedVersion+1 || (delta.Kind != "EvidenceCard" && version != delta.ExpectedVersion+1) {
			return nil, ErrVersionConflict
		}
		inverse := SemanticDelta{Kind: delta.Kind, TargetID: delta.TargetID, ExpectedVersion: version}
		switch delta.Kind {
		case "EvidenceCard":
			var old, now EvidenceCard
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(current.Body, &now); err != nil {
				return nil, err
			}
			if delta.ProposedCard == nil || !sameCardSemanticInput(cardSemanticInput(now), *delta.ProposedCard) {
				return nil, ErrVersionConflict
			}
			input := cardSemanticInput(old)
			inverse.ProposedCard = &input
		case "ResearchClaim":
			var old ResearchClaim
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			var now ResearchClaim
			if err := json.Unmarshal(current.Body, &now); err != nil {
				return nil, err
			}
			proposed := delta.ProposedClaim
			if proposed == nil || now.Text != proposed.Text || now.Scope != proposed.Scope || now.Status != proposed.Status || now.Explanation != proposed.Explanation {
				return nil, ErrVersionConflict
			}
			inverse.ProposedClaim = &SaveResearchClaimInput{Text: old.Text, Scope: old.Scope, Status: old.Status, Explanation: old.Explanation, Confirmed: delta.ProposedClaim.Confirmed, Reason: "逆向已审核语义变更"}
		case "ProjectAssetScope":
			var old ProjectAssetPolicy
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			var now ProjectAssetPolicy
			if err := json.Unmarshal(current.Body, &now); err != nil {
				return nil, err
			}
			proposed := delta.ProposedAssetPolicy
			if proposed == nil || now.Scope != proposed.Scope || now.Purpose != proposed.Purpose || now.ModelUseAllowed != proposed.ModelUseAllowed {
				return nil, ErrVersionConflict
			}
			// Metadata and native asset revision will be rebuilt by the actual G2 writer.
			inverse.ProposedAssetPolicy = &ProjectAssetPolicy{AssetID: delta.TargetID, Scope: old.Scope, Purpose: old.Purpose, AllowedUserIDs: old.AllowedUserIDs, ModelUseAllowed: old.ModelUseAllowed}
		case "RuleBinding":
			var old, now template.RuleBinding
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(current.Body, &now); err != nil {
				return nil, err
			}
			enabled := old.Enabled
			override := governance.BindingOverride{RuleID: old.RuleID, Enabled: &enabled, TargetKind: old.TargetKind, TargetID: old.TargetID, Parameters: old.Parameters, Severity: old.Severity}
			for key := range now.Parameters {
				if _, found := old.Parameters[key]; !found {
					override.RemoveParameterKeys = append(override.RemoveParameterKeys, key)
				}
			}
			slices.Sort(override.RemoveParameterKeys)
			inverse.ProposedBinding = &override
		case "RouteNode":
			var old RouteNode
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			if old.Version == 0 {
				old = *delta.ProposedRoute
				old.Status = "retired"
				old.Required = false
				old.Inputs = []ObjectVersionRef{}
			}
			inverse.ProposedRoute = &old
		case "RouteEdge":
			var old RouteEdge
			if err := json.Unmarshal(delta.Before, &old); err != nil {
				return nil, err
			}
			if old.Version == 0 {
				// A newly created edge has no previous version to restore, so
				// its inverse is the same relation with an empty relation
				// type: applying it retires the edge from the roadmap.
				old = *delta.ProposedEdge
				old.Relation = ""
			}
			inverse.ProposedEdge = &old
		default:
			return nil, ErrInvalidRequest
		}
		out = append(out, inverse)
	}
	return out, nil
}
