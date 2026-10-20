package delivery

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

var ruleSnapshotHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

// FrozenRuleSetHash is the effective binding identity when one is frozen. The
// embedded G4 Template.RulesetHash includes its exact source reference and overrides.
// Historical independent copies keep their original compiled-projection identity.
func FrozenRuleSetHash(input DeliveryInput) string {
	if input.Template.RuleSetSnapshot != nil || input.Template.RuleSetRef != nil {
		return input.Template.RulesetHash
	}
	if input.RuleSetSnapshot != nil {
		if input.TemplateCopyRulesetHash != "" {
			return input.TemplateCopyRulesetHash
		}
		return input.RuleSetSnapshot.RulesetHash
	}
	return input.Template.RulesetHash
}
func ValidateFrozenRuleSnapshot(input DeliveryInput) error {
	if input.Template.RuleSetSnapshot != nil || input.Template.RuleSetRef != nil {
		return validateBoundFrozenRuleSnapshot(input)
	}
	if input.RuleSetSnapshot == nil {
		if input.SourceRuleSetID != "" || input.SourceRuleSetVersion != "" || input.SourceRuleSetHash != "" || input.SourcePolicyHash != "" || len(input.BindingPolicies) > 0 {
			return fmt.Errorf("independent rule metadata has no snapshot")
		}
		if input.TemplateCopyRulesetHash != "" && input.TemplateCopyRulesetHash != input.Template.RulesetHash {
			return fmt.Errorf("legacy compiled rule digest mismatch")
		}
		return nil
	}
	set := *input.RuleSetSnapshot
	if input.TemplateCopyID == "" || input.TemplateCopyVersion < 1 || set.State == lingdoctemplate.RuleSetDraft || input.TemplateCopyRulesetHash != set.RulesetHash {
		return fmt.Errorf("invalid frozen binding identity")
	}
	if err := lingdoctemplate.ValidateRuleSet(set); err != nil {
		return err
	}
	content, compiled, err := lingdoctemplate.Hashes(input.Template)
	if err != nil {
		return err
	}
	if content != input.TemplateCopyContentHash || content != input.Template.ContentHash || compiled != input.Template.RulesetHash {
		return fmt.Errorf("frozen template compiled content mismatch")
	}
	projection, err := lingdoctemplate.CompileRuleSet(set)
	if err != nil {
		return err
	}
	a, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	b, err := json.Marshal(input.Template.Rules)
	if err != nil {
		return err
	}
	if string(a) != string(b) {
		return fmt.Errorf("frozen ruleset projection differs from template")
	}
	if input.SourceRuleSetID == "" || input.SourceRuleSetVersion == "" || !ruleSnapshotHash.MatchString(input.SourceRuleSetHash) {
		return fmt.Errorf("frozen source ruleset identity is absent")
	}
	if set.ID == input.SourceRuleSetID && set.Version == input.SourceRuleSetVersion && set.RulesetHash != input.SourceRuleSetHash {
		return fmt.Errorf("frozen source ruleset hash mismatch")
	}
	hash, err := governance.HashBindingPolicies(input.BindingPolicies)
	if err != nil || hash != input.SourcePolicyHash {
		return fmt.Errorf("frozen reviewed policy hash mismatch")
	}
	return nil
}

// legacyIndependentRuleSnapshot identifies the frozen #122 projection contract.
// Canonical G4 input always evaluates the immutable snapshot embedded in Template.
func legacyIndependentRuleSnapshot(input DeliveryInput) bool {
	return input.Template.RuleSetSnapshot == nil && input.Template.RuleSetRef == nil && input.RuleSetSnapshot != nil
}

func validateBoundFrozenRuleSnapshot(input DeliveryInput) error {
	set := input.Template.RuleSetSnapshot
	if set == nil {
		return fmt.Errorf("bound RuleSet reference has no immutable snapshot")
	}
	content, effective, err := lingdoctemplate.Hashes(input.Template)
	if err != nil {
		return err
	}
	if content != input.Template.ContentHash || effective != input.Template.RulesetHash {
		return fmt.Errorf("bound template content or effective ruleset hash mismatch")
	}
	// Standalone evaluator callers have no project-copy metadata. If any copy
	// identity is provided, require the immutable copy and content hash. The
	// #122 effective-hash alias was absent on earlier G4 frozen values; when
	// present it must agree with the canonical effective hash above.
	if input.TemplateCopyID != "" || input.TemplateCopyVersion != 0 || input.TemplateCopyContentHash != "" || input.TemplateCopyRulesetHash != "" {
		if input.TemplateCopyID == "" || input.TemplateCopyVersion < 1 || input.TemplateCopyContentHash != content || (input.TemplateCopyRulesetHash != "" && input.TemplateCopyRulesetHash != effective) {
			return fmt.Errorf("bound frozen project-copy identity mismatch")
		}
	}
	ref := lingdoctemplate.RuleSetReferenceFor(input.Template)
	if ref == nil || ref.ID != set.ID || ref.Version != set.Version || ref.Hash != set.RulesetHash {
		return fmt.Errorf("bound frozen RuleSet reference mismatch")
	}
	// Old top-level fields may be absent on G4 input. When dual-written, they
	// are consistency pins and never replace the embedded evaluator identity.
	if input.RuleSetSnapshot != nil {
		mirror := *input.RuleSetSnapshot
		if err := lingdoctemplate.ValidateRuleSet(mirror); err != nil {
			return err
		}
		if mirror.ID != ref.ID || mirror.Version != ref.Version || mirror.RulesetHash != ref.Hash {
			return fmt.Errorf("top-level RuleSet snapshot differs from bound reference")
		}
	}
	if input.SourceRuleSetID != "" || input.SourceRuleSetVersion != "" || input.SourceRuleSetHash != "" {
		if input.SourceRuleSetID != ref.ID || input.SourceRuleSetVersion != ref.Version || input.SourceRuleSetHash != ref.Hash {
			return fmt.Errorf("frozen source RuleSet identity differs from bound reference")
		}
	}
	if input.SourcePolicyHash != "" || len(input.BindingPolicies) > 0 {
		hash, err := governance.HashBindingPolicies(input.BindingPolicies)
		if err != nil || hash != input.SourcePolicyHash {
			return fmt.Errorf("frozen reviewed policy hash mismatch")
		}
	}
	return nil
}

func cloneRuleSnapshot(set *lingdoctemplate.RuleSet) *lingdoctemplate.RuleSet {
	if set == nil {
		return nil
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return &lingdoctemplate.RuleSet{}
	}
	var copy lingdoctemplate.RuleSet
	if json.Unmarshal(raw, &copy) != nil {
		return &lingdoctemplate.RuleSet{}
	}
	return &copy
}
func cloneFrozenPolicies(policies []governance.BindingPolicy) []governance.BindingPolicy {
	if policies == nil {
		return nil
	}
	raw, err := json.Marshal(policies)
	if err != nil {
		return []governance.BindingPolicy{{RuleID: "invalid"}}
	}
	var copy []governance.BindingPolicy
	if json.Unmarshal(raw, &copy) != nil {
		return []governance.BindingPolicy{{RuleID: "invalid"}}
	}
	return copy
}

// ValidateRuleSnapshotReceipt replays the same immutable server snapshot. A
// persisted passed flag cannot substitute for its digest or current check.
func ValidateRuleSnapshotReceipt(snapshot ReleaseSnapshot) error {
	if err := ValidateFrozenRuleSnapshot(snapshot.FrozenInput); err != nil {
		return err
	}
	if snapshot.FrozenInput.RuleSetSnapshot == nil && snapshot.FrozenInput.Template.RuleSetSnapshot == nil {
		return nil
	}
	if snapshot.FrozenInput.Template.RuleSetSnapshot != nil && !reflect.DeepEqual(snapshot.RuleSetRef, lingdoctemplate.RuleSetReferenceFor(snapshot.FrozenInput.Template)) {
		return fmt.Errorf("snapshot RuleSet reference differs from frozen input")
	}
	digest, err := digestFrozenInput(snapshot.FrozenInput)
	if err != nil {
		return err
	}
	if digest != snapshot.SnapshotDigest {
		return fmt.Errorf("frozen snapshot digest mismatch")
	}
	fresh := Evaluate(snapshot.FrozenInput)
	if fresh.Status != CheckPassed || fresh.RulesetHash != snapshot.Check.RulesetHash || snapshot.Check.Status != CheckPassed {
		return ErrExportPreflightBlocked
	}
	// A human disposition is workflow metadata: it cannot manufacture new
	// findings or alter an evaluator's severity, identity, evidence, or
	// version. It does change one thing — whether the finding still blocks
	// (see BlockingIssues) — and that question is answered by the published
	// evaluation, not by this receipt.
	//
	// The receipt therefore replays the **disposition-free** evaluation on
	// purpose: its subject is "the frozen rule snapshot still produces these
	// findings". A dismissed finding still blocks the export receipt, because
	// the receipt is not the delivery gate: the delivery gate is the
	// stabilization of the revision, which a dismissal does not unblock.
	old := snapshot.Check
	old.ReceiptID = ""
	old.Issues = cloneIssues(old.Issues)
	for i := range old.Issues {
		old.Issues[i].Disposition = nil
	}
	if !reflect.DeepEqual(old, fresh) {
		return fmt.Errorf("stored rule receipt differs from immutable replay")
	}
	return nil
}
