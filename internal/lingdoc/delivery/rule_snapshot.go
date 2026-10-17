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
// legacy Template.RulesetHash always remains the compiled projection digest.
func FrozenRuleSetHash(input DeliveryInput) string {
	if input.RuleSetSnapshot != nil {
		if input.TemplateCopyRulesetHash != "" {
			return input.TemplateCopyRulesetHash
		}
		return input.RuleSetSnapshot.RulesetHash
	}
	return input.Template.RulesetHash
}
func ValidateFrozenRuleSnapshot(input DeliveryInput) error {
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
	if snapshot.FrozenInput.RuleSetSnapshot == nil {
		return nil
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
	// Human disposition is workflow metadata; it cannot manufacture new findings
	// or alter an evaluator's severity, identity, evidence, or version.
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
