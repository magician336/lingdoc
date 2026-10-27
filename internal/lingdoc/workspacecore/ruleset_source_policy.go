package workspacecore

import (
	"fmt"
	"slices"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

// A sidecar source proof must name the exact immutable catalog RuleSet. A
// derived project snapshot is never accepted as its own source evidence.
func validateLegacySourceSnapshot(copy *ProjectTemplateCopy) error {
	if copy == nil || copy.legacySourceRuleSetSnapshot == nil {
		return nil // Historical sidecars did not include this proof.
	}
	set := copy.legacySourceRuleSetSnapshot
	if copy.Definition.RuleSetSnapshot != nil || copy.Definition.RuleSetRef != nil ||
		copy.RuleSetSnapshot == nil || set.ID != copy.SourceRuleSetID ||
		set.Version != copy.SourceRuleSetVersion || set.RulesetHash != copy.SourceRuleSetHash ||
		lingdoctemplate.ValidateRuleSet(*set) != nil {
		return fmt.Errorf("%w: invalid legacy source RuleSet evidence", ErrInvalidState)
	}
	return nil
}

// inheritRuleSetSourcePolicy carries reviewed catalog metadata into a newly
// constructed project copy. The source policy identity is unchanged; a new
// canonical RuleSet replaces the legacy source pin, not the policy itself.
// Call after projectTemplateCopy and before persisting either copy or project.
func inheritRuleSetSourcePolicy(previous, next *ProjectTemplateCopy) error {
	if previous == nil || next == nil || previous == next {
		return fmt.Errorf("%w: missing independent template copies", ErrInvalidState)
	}
	policies := cloneRuleSetSourcePolicies(previous.BindingPolicies)
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil || previous.SourcePolicyHash != "" && previous.SourcePolicyHash != policyHash ||
		previous.SourcePolicyHash == "" && len(policies) != 0 {
		return fmt.Errorf("%w: invalid source binding policy identity", ErrInvalidState)
	}

	var source *lingdoctemplate.RuleSet
	legacy := false
	ref := lingdoctemplate.RuleSetReferenceFor(next.Definition)
	if next.Definition.RuleSetSnapshot != nil {
		source = next.Definition.RuleSetSnapshot
		if ref == nil || ref.ID != source.ID || ref.Version != source.Version || ref.Hash != source.RulesetHash {
			return fmt.Errorf("%w: canonical RuleSet source mismatch", ErrInvalidState)
		}
	} else if ref != nil {
		return fmt.Errorf("%w: missing canonical RuleSet snapshot", ErrInvalidState)
	} else if previous.RuleSetSnapshot != nil {
		// Existing catalog copies predate the canonical Definition envelope.
		// Ordinary structural edits retain that exact legacy source identity.
		source, legacy = previous.RuleSetSnapshot, true
		if previous.SourceRuleSetID == "" || previous.SourceRuleSetVersion == "" || previous.SourceRuleSetHash == "" {
			return fmt.Errorf("%w: legacy RuleSet source mismatch", ErrInvalidState)
		}
	} else if previous.SourceRuleSetID != "" || previous.SourceRuleSetVersion != "" || previous.SourceRuleSetHash != "" {
		return fmt.Errorf("%w: missing legacy RuleSet snapshot", ErrInvalidState)
	}
	if source != nil {
		if err := lingdoctemplate.ValidateRuleSet(*source); err != nil {
			return fmt.Errorf("%w: invalid source RuleSet snapshot", ErrInvalidState)
		}
		policySource := source
		if legacy && previous.legacySourceRuleSetSnapshot != nil {
			if err := validateLegacySourceSnapshot(previous); err != nil {
				return err
			}
			policySource = previous.legacySourceRuleSetSnapshot
		}
		// This checks both rule identity and allowed field/section/term targets
		// against the resulting definition, including structural upgrades.
		if err := governance.ValidateBindingPolicies(next.Definition, *policySource, policies); err != nil {
			return fmt.Errorf("%w: reviewed binding policy does not apply to the target template", ErrInvalidRequest)
		}
		source = lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: source}).RuleSetSnapshot
	} else if len(policies) != 0 {
		return fmt.Errorf("%w: binding policies require a RuleSet source", ErrInvalidState)
	}

	// Mutate only after all validation succeeds. Neither the old copy nor the
	// canonical Definition shares mutable policy/snapshot data with the sidecar.
	next.BindingPolicies, next.SourcePolicyHash = policies, previous.SourcePolicyHash
	next.RuleSetSnapshot = source
	next.legacySourceRuleSetSnapshot = nil
	next.SourceRuleSetID, next.SourceRuleSetVersion, next.SourceRuleSetHash = "", "", ""
	if source != nil {
		next.SourceRuleSetID, next.SourceRuleSetVersion, next.SourceRuleSetHash = source.ID, source.Version, source.RulesetHash
		if legacy {
			if previous.legacySourceRuleSetSnapshot != nil {
				next.legacySourceRuleSetSnapshot = lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: previous.legacySourceRuleSetSnapshot}).RuleSetSnapshot
			}
			// Legacy catalog copies used the source hash at the top level.
			// A semantic edit can derive a project-owned effective snapshot;
			// keep its original catalog pin rather than relabeling the source.
			next.SourceRuleSetID, next.SourceRuleSetVersion, next.SourceRuleSetHash = previous.SourceRuleSetID, previous.SourceRuleSetVersion, previous.SourceRuleSetHash
			// Canonical copies keep their compiled template+override hash.
			next.RulesetHash = source.RulesetHash
		}
	}
	return nil
}

func cloneRuleSetSourcePolicies(source []governance.BindingPolicy) []governance.BindingPolicy {
	if source == nil {
		return nil
	}
	cloned := make([]governance.BindingPolicy, len(source))
	for i, policy := range source {
		cloned[i] = policy
		cloned[i].AllowedTargetKinds = slices.Clone(policy.AllowedTargetKinds)
		cloned[i].AllowedTargetIDs = slices.Clone(policy.AllowedTargetIDs)
		cloned[i].AllowedParameterKeys = slices.Clone(policy.AllowedParameterKeys)
		cloned[i].AllowedSeverities = slices.Clone(policy.AllowedSeverities)
		if policy.ParameterBounds != nil {
			cloned[i].ParameterBounds = make(map[string]governance.ParameterBounds, len(policy.ParameterBounds))
			for key, bounds := range policy.ParameterBounds {
				if bounds.Minimum != nil {
					value := *bounds.Minimum
					bounds.Minimum = &value
				}
				if bounds.Maximum != nil {
					value := *bounds.Maximum
					bounds.Maximum = &value
				}
				cloned[i].ParameterBounds[key] = bounds
			}
		}
	}
	return cloned
}
