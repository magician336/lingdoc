package templategovernance

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// ReplayFixture is an immutable, version-pinned real input and expected rule
// outcome. The application reader owns authorized fixture persistence/lookup.
type ReplayFixture struct {
	ID             string
	RuleSet        RuleSetRef
	RuleID         string
	Scene          string
	Polarity       string // positive or negative
	TargetRef      string
	Input          template.EvaluationInput
	ExpectedStatus template.EvaluationStatus
}
type ReplayFixtureReader interface {
	GetReplayFixture(context.Context, Actor, string) (ReplayFixture, error)
}
type ExampleReplayVerifier struct{ Reader ReplayFixtureReader }

// JudgeReplayFixture is the per-fixture judgment shared by publication Verify
// and registration trial replay (Spec #768 D5/D8: one judge, no second
// criterion). It returns the replay evidence family (ErrReplayRequired) for
// every failure. Shape/semantic rejections that need no evaluation are the
// caller's 400 boundary (D6); everything this function answers required the
// real evaluator or the fixture's relation to the version, so both Verify and
// registration answer it with the 422 replay family.
func JudgeReplayFixture(version TemplateVersion, fixture ReplayFixture) error {
	bindings := bindingsByID(version)
	binding, ok := bindings[fixture.RuleID]
	if !ok || !binding.Enabled || fixture.RuleSet != version.RuleSet {
		return ErrReplayRequired
	}
	if fixture.Polarity != "positive" && fixture.Polarity != "negative" {
		return ErrReplayRequired
	}
	if !contains(binding.TriggerScenes, fixture.Scene) {
		return ErrReplayRequired
	}
	if binding.DefinitionID != "model_assisted" {
		if fixture.Polarity == "positive" && fixture.ExpectedStatus != template.EvaluationPass {
			return ErrReplayRequired
		}
		if fixture.Polarity == "negative" && fixture.ExpectedStatus != template.EvaluationIssue {
			return ErrReplayRequired
		}
	} else if fixture.ExpectedStatus != template.EvaluationUnknown {
		return ErrReplayRequired
	}
	evaluations, err := template.EvaluateRuleSet(version.Definition, version.RuleSetSnapshot, fixture.Scene, fixture.Input)
	if err != nil {
		return err
	}
	matched := false
	for _, evaluation := range evaluations {
		if evaluation.RuleID == fixture.RuleID && evaluation.TargetRef == fixture.TargetRef {
			if evaluation.Status != fixture.ExpectedStatus || evaluation.RulesetHash != version.RulesetHash {
				return fmt.Errorf("%w: fixture %s got %s", ErrReplayRequired, fixture.ID, evaluation.Status)
			}
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("%w: fixture %s did not exercise its declared target", ErrReplayRequired, fixture.ID)
	}
	return nil
}

// ReplayCoverageEntry is one (enabled binding × trigger scene) cell pair of
// the shared replay coverage matrix, plus the binding's declared example
// references and which of them the judged set did not include.
type ReplayCoverageEntry struct {
	RuleID                         string   `json:"rule_id"`
	Scene                          string   `json:"scene"`
	PositiveCovered                bool     `json:"positive_covered"`
	NegativeCovered                bool     `json:"negative_covered"`
	DeclaredPositiveExampleRefs    []string `json:"declared_positive_example_refs"`
	DeclaredNegativeExampleRefs    []string `json:"declared_negative_example_refs"`
	UnsatisfiedDeclaredExampleRefs []string `json:"unsatisfied_declared_example_refs"`
}

// ReplayCoverageReport is the (rule × scene × polarity) coverage matrix and
// the declared example reference satisfaction of one judged fixture set.
// Registration receipts, the coverage readback endpoint and publication Verify
// consume the same computation, so the three answers agree by construction.
type ReplayCoverageReport struct {
	Complete                       bool                  `json:"complete"`
	Entries                        []ReplayCoverageEntry `json:"entries"`
	UnsatisfiedDeclaredExampleRefs []string              `json:"unsatisfied_declared_example_refs"`
}

// JudgeReplayCoverage judges every fixture with JudgeReplayFixture (a fixture
// whose declared outcome contradicts the real evaluation covers nothing) and
// builds the coverage matrix over the version's enabled bindings and trigger
// scenes. Entry order follows the snapshot's binding order and each binding's
// trigger scene order; the unsatisfied declared references follow the same
// binding order, positive references before negative ones.
func JudgeReplayCoverage(version TemplateVersion, fixtures []ReplayFixture) ReplayCoverageReport {
	covered := map[string]bool{}
	submitted := map[string]bool{}
	for _, fixture := range fixtures {
		submitted[fixture.ID] = true
		if JudgeReplayFixture(version, fixture) != nil {
			continue
		}
		covered[fixture.RuleID+"/"+fixture.Scene+"/"+fixture.Polarity] = true
	}
	report := ReplayCoverageReport{Complete: true, Entries: []ReplayCoverageEntry{}}
	for _, binding := range version.RuleSetSnapshot.Bindings {
		if !binding.Enabled {
			continue
		}
		for _, scene := range binding.TriggerScenes {
			entry := ReplayCoverageEntry{
				RuleID:                         binding.RuleID,
				Scene:                          scene,
				PositiveCovered:                covered[binding.RuleID+"/"+scene+"/positive"],
				NegativeCovered:                covered[binding.RuleID+"/"+scene+"/negative"],
				DeclaredPositiveExampleRefs:    append([]string{}, binding.PositiveExampleRefs...),
				DeclaredNegativeExampleRefs:    append([]string{}, binding.NegativeExampleRefs...),
				UnsatisfiedDeclaredExampleRefs: []string{},
			}
			if !entry.PositiveCovered || !entry.NegativeCovered {
				report.Complete = false
			}
			report.Entries = append(report.Entries, entry)
		}
		for _, id := range append(append([]string{}, binding.PositiveExampleRefs...), binding.NegativeExampleRefs...) {
			if !submitted[id] {
				report.UnsatisfiedDeclaredExampleRefs = append(report.UnsatisfiedDeclaredExampleRefs, id)
				report.Complete = false
			}
		}
	}
	return report
}

// bindingsByID indexes the version's rule set snapshot bindings by rule id;
// the enabled check stays with the judge so a disabled binding never answers.
func bindingsByID(version TemplateVersion) map[string]template.RuleBinding {
	bindings := map[string]template.RuleBinding{}
	for _, binding := range version.RuleSetSnapshot.Bindings {
		bindings[binding.RuleID] = binding
	}
	return bindings
}

func (v ExampleReplayVerifier) Verify(ctx context.Context, actor Actor, version TemplateVersion, ids []string) error {
	if v.Reader == nil || len(ids) == 0 {
		return ErrReplayRequired
	}
	if err := ValidateVersion(version); err != nil {
		return err
	}
	judged := make([]ReplayFixture, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return ErrInvalidRequest
		}
		seen[id] = true
		fixture, err := v.Reader.GetReplayFixture(ctx, actor, id)
		if err != nil {
			return err
		}
		if fixture.ID != id {
			return ErrReplayRequired
		}
		if err := JudgeReplayFixture(version, fixture); err != nil {
			return err
		}
		judged = append(judged, fixture)
	}
	report := JudgeReplayCoverage(version, judged)
	for _, entry := range report.Entries {
		if !entry.PositiveCovered || !entry.NegativeCovered {
			return fmt.Errorf("%w: missing positive/negative example for %s/%s", ErrReplayRequired, entry.RuleID, entry.Scene)
		}
	}
	for _, id := range report.UnsatisfiedDeclaredExampleRefs {
		return fmt.Errorf("%w: required binding example %s missing", ErrReplayRequired, id)
	}
	return nil
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
