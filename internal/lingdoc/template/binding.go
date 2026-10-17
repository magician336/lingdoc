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

// Clone returns an isolated copy so an evidence record can cross an
// application boundary without sharing mutable RuleSet maps or override
// parameters with its caller.
func (b VerifiedRuleSetBinding) Clone() VerifiedRuleSetBinding {
	return VerifiedRuleSetBinding{
		Snapshot:      cloneRuleSet(b.Snapshot),
		Reference:     b.Reference,
		Overrides:     CloneRuleBindingOverrides(b.Overrides),
		EffectiveHash: b.EffectiveHash,
		ContentHash:   b.ContentHash,
	}
}

// ValidateVerifiedRuleSetBinding checks that evidence belongs to the exact
// template definition being executed without re-resolving or recomputing its
// identity. The producer is responsible for constructing the evidence with
// VerifyRuleSetBinding; consumers only compare the stamped fields and compile
// the copied snapshot.
func ValidateVerifiedRuleSetBinding(t Template, b VerifiedRuleSetBinding) error {
	if t.RuleSetSnapshot == nil {
		return fmt.Errorf("%w: template has no immutable snapshot", ErrUnverifiedRuleSetBinding)
	}
	if err := Validate(t); err != nil {
		return err
	}
	wantRef := RuleSetReference{ID: t.RuleSetSnapshot.ID, Version: t.RuleSetSnapshot.Version, Hash: t.RuleSetSnapshot.RulesetHash}
	if t.RuleSetRef != nil {
		wantRef = *t.RuleSetRef
	}
	if b.Reference != wantRef || b.Snapshot.ID != wantRef.ID || b.Snapshot.Version != wantRef.Version || b.Snapshot.RulesetHash != wantRef.Hash {
		return fmt.Errorf("%w: binding evidence does not match template identity", ErrUnverifiedRuleSetBinding)
	}
	if b.ContentHash == "" || b.EffectiveHash == "" || t.ContentHash != b.ContentHash || t.RulesetHash != b.EffectiveHash {
		return fmt.Errorf("%w: binding evidence hash does not match template", ErrUnverifiedRuleSetBinding)
	}
	if err := CanBindRuleSet(b.Snapshot); err != nil {
		return fmt.Errorf("%w: %w", ErrUnverifiedRuleSetBinding, err)
	}
	if err := ValidateRuleBindingOverrides(b.Snapshot, b.Overrides); err != nil {
		return fmt.Errorf("%w: %w", ErrUnverifiedRuleSetBinding, err)
	}
	return nil
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
