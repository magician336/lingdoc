package templategovernance

import (
	"context"
	"encoding/json"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

// HashBindingPolicies uses canonical reviewed policy content. Empty policy
// hashes are still real hashes; missing metadata never grants an override.
func HashBindingPolicies(policies []BindingPolicy) (string, error) {
	raw, err := json.Marshal(policies)
	if err != nil {
		return "", ErrInvalidRequest
	}
	canonical := []BindingPolicy{}
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return "", err
	}
	if canonical == nil {
		canonical = []BindingPolicy{}
	}
	seen := map[string]bool{}
	for i := range canonical {
		p := &canonical[i]
		if !identifier.MatchString(p.RuleID) || seen[p.RuleID] {
			return "", ErrInvalidRequest
		}
		seen[p.RuleID] = true
		for _, items := range [][]string{p.AllowedTargetKinds, p.AllowedTargetIDs, p.AllowedParameterKeys, p.AllowedSeverities} {
			unique := map[string]bool{}
			for _, item := range items {
				if item == "" || unique[item] {
					return "", ErrInvalidRequest
				}
				unique[item] = true
			}
			sort.Strings(items)
		}
		for _, b := range p.ParameterBounds {
			if b.Minimum != nil && (math.IsNaN(*b.Minimum) || math.IsInf(*b.Minimum, 0)) || b.Maximum != nil && (math.IsNaN(*b.Maximum) || math.IsInf(*b.Maximum, 0)) || b.Minimum != nil && b.Maximum != nil && *b.Minimum > *b.Maximum {
				return "", ErrInvalidRequest
			}
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].RuleID < canonical[j].RuleID })
	return hashJSON(canonical)
}
func ValidateBindingPolicies(definition template.Template, set template.RuleSet, policies []BindingPolicy) error {
	if _, err := HashBindingPolicies(policies); err != nil {
		return err
	}
	byID := map[string]template.RuleBinding{}
	for _, b := range set.Bindings {
		byID[b.RuleID] = b
	}
	definitions := map[string]template.RuleDefinition{}
	for _, d := range template.RuleDefinitions() {
		definitions[d.ID] = d
	}
	for _, p := range policies {
		b, ok := byID[p.RuleID]
		if !ok {
			return ErrInvalidRequest
		}
		if mandatoryBinding(b) && (p.MayDisable || len(p.AllowedTargetKinds) > 0 || len(p.AllowedTargetIDs) > 0 || len(p.AllowedParameterKeys) > 0 || len(p.ParameterBounds) > 0 || len(p.AllowedSeverities) > 0) {
			return ErrInvalidRequest
		}
		d := definitions[b.DefinitionID]
		kinds := p.AllowedTargetKinds
		if len(kinds) == 0 {
			kinds = []string{b.TargetKind}
		}
		for _, kind := range p.AllowedTargetKinds {
			if !contains(d.AllowedTargetKinds, kind) {
				return ErrInvalidRequest
			}
		}
		for _, id := range p.AllowedTargetIDs {
			if !identifier.MatchString(id) {
				return ErrInvalidRequest
			}
			for _, kind := range kinds {
				if !targetExists(definition, kind, id) {
					return ErrInvalidRequest
				}
			}
		}
		for _, severity := range p.AllowedSeverities {
			if severityRank(severity) == 0 || severityRank(severity) > severityRank(b.Severity) {
				return ErrInvalidRequest
			}
		}
		// Parameter controls can change existing declarative knobs only. RuleSet
		// validation remains the authority for the resulting parameter values.
		for _, key := range p.AllowedParameterKeys {
			if _, ok := b.Parameters[key]; !ok {
				return ErrInvalidRequest
			}
			switch key {
			case "target_kind", "target_id", "definition_id", "definition_version", "evaluator", "script", "expression", "code", "owner", "origin":
				return ErrInvalidRequest
			}
		}
		for key := range p.ParameterBounds {
			if !contains(p.AllowedParameterKeys, key) {
				return ErrInvalidRequest
			}
			if _, ok := parameterNumber(b.Parameters[key]); !ok {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}
func (s *Service) BindingPoliciesForCopy(ctx context.Context, tx *gorm.DB, actor Actor, id, version string) ([]BindingPolicy, string, error) {
	v, err := s.ResolveForCopy(ctx, tx, actor, id, version)
	if err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(v.BindingPolicies)
	if err != nil {
		return nil, "", err
	}
	policies := []BindingPolicy{}
	if err := json.Unmarshal(raw, &policies); err != nil {
		return nil, "", err
	}
	return policies, v.PolicyHash, nil
}
