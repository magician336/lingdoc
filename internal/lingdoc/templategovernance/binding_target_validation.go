package templategovernance

import "github.com/Tencent/WeKnora/internal/lingdoc/template"

// ValidateChangedBindingTargets checks the final static target of a binding
// whose target changed. Source and review-item targets are runtime objects,
// so their existence cannot be decided from a template definition.
func ValidateChangedBindingTargets(definition template.Template, previous, effective template.RuleSet) error {
	prior := make(map[string]template.RuleBinding, len(previous.Bindings))
	for _, binding := range previous.Bindings {
		prior[binding.RuleID] = binding
	}
	for _, binding := range effective.Bindings {
		old, ok := prior[binding.RuleID]
		if !ok {
			return ErrInvalidRequest
		}
		if binding.TargetKind == old.TargetKind && binding.TargetID == old.TargetID {
			continue
		}
		switch binding.TargetKind {
		case "field", "chapter":
			if !targetExists(definition, binding.TargetKind, binding.TargetID) {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}
