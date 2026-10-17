package template

import (
	"errors"
	"fmt"
)

// ErrUnverifiedRuleSetBinding is returned when a governed path receives a
// RuleSet identity without the immutable, hash-verified snapshot that proves
// which rules are executable.
var ErrUnverifiedRuleSetBinding = errors.New("unverified RuleSet binding")

// VerifiedRuleSetBinding is the single value produced by the template
// governance boundary for a RuleSet-backed template. Every field is copied
// from validated server-owned data; callers must treat the value as an
// immutable evidence record. The constructor defensively clones nested data.
type VerifiedRuleSetBinding struct {
	Snapshot      RuleSet
	Reference     RuleSetReference
	Overrides     []RuleBindingOverride
	EffectiveHash string
	ContentHash   string
}

// VerifyRuleSetBinding validates and materializes RuleSet binding evidence.
// A template with no RuleSet at all is a valid legacy template and should not
// call this function. A bare RuleSetRef is unresolved metadata and fails
// closed. Snapshot-only definitions remain supported for historical callers;
// their exact reference is derived from the verified snapshot.
func VerifyRuleSetBinding(t Template) (VerifiedRuleSetBinding, error) {
	if t.RuleSetSnapshot == nil {
		if t.RuleSetRef != nil {
			return VerifiedRuleSetBinding{}, fmt.Errorf("%w: RuleSetRef has no immutable snapshot", ErrUnverifiedRuleSetBinding)
		}
		return VerifiedRuleSetBinding{}, fmt.Errorf("%w: template has no RuleSet snapshot", ErrUnverifiedRuleSetBinding)
	}
	if err := Validate(t); err != nil {
		return VerifiedRuleSetBinding{}, err
	}
	if err := CanBindRuleSet(*t.RuleSetSnapshot); err != nil {
		return VerifiedRuleSetBinding{}, fmt.Errorf("%w: %w", ErrUnverifiedRuleSetBinding, err)
	}
	ref := RuleSetReference{ID: t.RuleSetSnapshot.ID, Version: t.RuleSetSnapshot.Version, Hash: t.RuleSetSnapshot.RulesetHash}
	if t.RuleSetRef != nil {
		ref = *t.RuleSetRef
	}
	contentHash, effectiveHash, err := Hashes(t)
	if err != nil {
		return VerifiedRuleSetBinding{}, err
	}
	return VerifiedRuleSetBinding{
		Snapshot:      cloneRuleSet(*t.RuleSetSnapshot),
		Reference:     ref,
		Overrides:     CloneRuleBindingOverrides(t.RuleBindingOverrides),
		EffectiveHash: effectiveHash,
		ContentHash:   contentHash,
	}, nil
}
