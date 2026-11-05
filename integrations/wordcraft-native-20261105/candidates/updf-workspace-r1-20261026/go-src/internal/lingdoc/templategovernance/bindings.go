package templategovernance

import (
	"encoding/json"
	"reflect"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// BindingPolicy comes from the server's reviewed template policy, never the
// request body. Absence of a policy means no override is permitted.
type BindingPolicy struct {
	RuleID               string                     `json:"rule_id"`
	MayDisable           bool                       `json:"may_disable"`
	AllowedTargetKinds   []string                   `json:"allowed_target_kinds,omitempty"`
	AllowedTargetIDs     []string                   `json:"allowed_target_ids,omitempty"`
	AllowedParameterKeys []string                   `json:"allowed_parameter_keys,omitempty"`
	ParameterBounds      map[string]ParameterBounds `json:"parameter_bounds,omitempty"`
	AllowedSeverities    []string                   `json:"allowed_severities,omitempty"`
}
type ParameterBounds struct {
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}
type BindingOverride struct {
	RemoveParameterKeys []string       `json:"remove_parameter_keys,omitempty"`
	RuleID              string         `json:"rule_id"`
	Enabled             *bool          `json:"enabled,omitempty"`
	TargetKind          string         `json:"target_kind,omitempty"`
	TargetID            string         `json:"target_id,omitempty"`
	Parameters          map[string]any `json:"parameters,omitempty"`
	Severity            string         `json:"severity,omitempty"`
}

// ApplyBindingOverrides derives a project-owned immutable binding snapshot.
// The caller assigns its project snapshot identity/version, preserves the
// original source RuleSetRef, and persists snapshot+audit in its project tx.
// This helper neither publishes a catalog RuleSet nor writes another store.
func ApplyBindingOverrides(base template.RuleSet, effectiveID, effectiveVersion string, policies []BindingPolicy, overrides []BindingOverride) (template.RuleSet, error) {
	if err := template.ValidateRuleSet(base); err != nil {
		return template.RuleSet{}, err
	}
	if base.State == template.RuleSetDraft || !identifier.MatchString(effectiveID) || !identifier.MatchString(effectiveVersion) || effectiveID == base.ID && effectiveVersion == base.Version {
		return template.RuleSet{}, ErrInvalidRequest
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return template.RuleSet{}, err
	}
	var effective template.RuleSet
	if err := json.Unmarshal(raw, &effective); err != nil {
		return effective, err
	}
	index := map[string]int{}
	for i, binding := range effective.Bindings {
		index[binding.RuleID] = i
	}
	policyByID := map[string]BindingPolicy{}
	for _, policy := range policies {
		if _, exists := policyByID[policy.RuleID]; exists {
			return effective, ErrInvalidRequest
		}
		policyByID[policy.RuleID] = policy
	}
	seen := map[string]bool{}
	for _, override := range overrides {
		at, found := index[override.RuleID]
		policy, allowed := policyByID[override.RuleID]
		if !found || !allowed || seen[override.RuleID] {
			return template.RuleSet{}, ErrInvalidRequest
		}
		seen[override.RuleID] = true
		original := effective.Bindings[at]
		next := original
		if original.Parameters != nil {
			next.Parameters = map[string]any{}
			for k, v := range original.Parameters {
				next.Parameters[k] = v
			}
		}
		if override.Enabled != nil && *override.Enabled != original.Enabled {
			if !*override.Enabled && (!policy.MayDisable || mandatoryBinding(original)) {
				return template.RuleSet{}, ErrInvalidRequest
			}
			next.Enabled = *override.Enabled
		}
		if override.TargetKind != "" && override.TargetKind != original.TargetKind {
			if !contains(policy.AllowedTargetKinds, override.TargetKind) {
				return template.RuleSet{}, ErrInvalidRequest
			}
			next.TargetKind = override.TargetKind
		}
		if override.TargetID != "" && override.TargetID != original.TargetID {
			if !contains(policy.AllowedTargetIDs, override.TargetID) {
				return template.RuleSet{}, ErrInvalidRequest
			}
			next.TargetID = override.TargetID
		}
		if override.Severity != "" && override.Severity != original.Severity {
			if mandatoryBinding(original) || !contains(policy.AllowedSeverities, override.Severity) || severityRank(override.Severity) == 0 {
				return template.RuleSet{}, ErrInvalidRequest
			}
			next.Severity = override.Severity
		}
		removed := map[string]bool{}
		for _, key := range override.RemoveParameterKeys {
			if removed[key] || !contains(policy.AllowedParameterKeys, key) {
				return template.RuleSet{}, ErrInvalidRequest
			}
			if _, exists := original.Parameters[key]; !exists {
				return template.RuleSet{}, ErrInvalidRequest
			}
			if _, edited := override.Parameters[key]; edited {
				return template.RuleSet{}, ErrInvalidRequest
			}
			removed[key] = true
			delete(next.Parameters, key)
		}
		for key, value := range override.Parameters {
			if reflect.DeepEqual(original.Parameters[key], value) {
				continue
			}
			if !contains(policy.AllowedParameterKeys, key) {
				return template.RuleSet{}, ErrInvalidRequest
			}
			if bounds, exists := policy.ParameterBounds[key]; exists {
				number, ok := parameterNumber(value)
				if !ok || bounds.Minimum != nil && number < *bounds.Minimum || bounds.Maximum != nil && number > *bounds.Maximum {
					return template.RuleSet{}, ErrInvalidRequest
				}
			}
			if next.Parameters == nil {
				next.Parameters = map[string]any{}
			}
			next.Parameters[key] = value
		}
		if mandatoryBinding(original) && !reflect.DeepEqual(original, next) {
			return template.RuleSet{}, ErrInvalidRequest
		}
		effective.Bindings[at] = next
	}
	effective.ID = effectiveID
	effective.Version = effectiveVersion
	effective.State = template.RuleSetDraft
	effective.RulesetHash = ""
	if err := template.ValidateRuleSet(effective); err != nil {
		return template.RuleSet{}, err
	}
	hash, err := template.HashRuleSet(effective)
	if err != nil {
		return template.RuleSet{}, err
	}
	effective.State = template.RuleSetPublished
	effective.RulesetHash = hash
	return effective, nil
}
func mandatoryBinding(binding template.RuleBinding) bool {
	if binding.Severity != template.SeverityBlocking {
		return false
	}
	switch binding.DefinitionID {
	case "computed/required_fields", "computed/nonempty_chapter", "computed/confirmed_chapter", "computed/review_items_decided", "computed/source_available":
		return true
	}
	return binding.Origin == "builtin"
}
func severityRank(severity string) int {
	switch severity {
	case template.SeverityBlocking:
		return 3
	case template.SeverityWarning:
		return 2
	case template.SeverityInfo:
		return 1
	}
	return 0
}
func parameterNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	}
	return 0, false
}
