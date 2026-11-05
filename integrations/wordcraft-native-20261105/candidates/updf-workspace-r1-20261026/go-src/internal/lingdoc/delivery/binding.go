package delivery

import lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"

// verifiedRuleSetBinding returns the application-produced evidence attached to
// a delivery input. Inputs restored from a persisted snapshot reconstruct the
// same evidence from the durable template snapshot and hashes.
func verifiedRuleSetBinding(input DeliveryInput) (*lingdoctemplate.VerifiedRuleSetBinding, error) {
	if input.RuleSetBinding != nil {
		binding := input.RuleSetBinding.Clone()
		if err := lingdoctemplate.ValidateVerifiedRuleSetBinding(input.Template, binding); err != nil {
			return nil, err
		}
		return &binding, nil
	}
	if input.Template.RuleSetSnapshot == nil && input.Template.RuleSetRef == nil {
		return nil, nil
	}
	binding, err := lingdoctemplate.VerifyRuleSetBinding(input.Template)
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// VerifiedRuleSetBindingForInput exposes the already-produced evidence to
// application adapters such as usage-audit construction. It returns a clone
// so the adapter cannot mutate the evaluator's immutable input.
func VerifiedRuleSetBindingForInput(input DeliveryInput) (*lingdoctemplate.VerifiedRuleSetBinding, error) {
	return verifiedRuleSetBinding(input)
}
