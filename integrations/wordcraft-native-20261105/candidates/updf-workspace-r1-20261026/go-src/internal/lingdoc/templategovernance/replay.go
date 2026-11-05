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

func (v ExampleReplayVerifier) Verify(ctx context.Context, actor Actor, version TemplateVersion, ids []string) error {
	if v.Reader == nil || len(ids) == 0 {
		return ErrReplayRequired
	}
	if err := ValidateVersion(version); err != nil {
		return err
	}
	coverage := map[string]bool{}
	seen := map[string]bool{}
	bindings := map[string]template.RuleBinding{}
	for _, binding := range version.RuleSetSnapshot.Bindings {
		bindings[binding.RuleID] = binding
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return ErrInvalidRequest
		}
		seen[id] = true
		fixture, err := v.Reader.GetReplayFixture(ctx, actor, id)
		if err != nil {
			return err
		}
		binding, ok := bindings[fixture.RuleID]
		if !ok || !binding.Enabled || fixture.ID != id || fixture.RuleSet != version.RuleSet {
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
					return fmt.Errorf("%w: fixture %s got %s", ErrReplayRequired, id, evaluation.Status)
				}
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("%w: fixture %s did not exercise its declared target", ErrReplayRequired, id)
		}
		coverage[fixture.RuleID+"/"+fixture.Scene+"/"+fixture.Polarity] = true
	}
	for _, binding := range version.RuleSetSnapshot.Bindings {
		if !binding.Enabled {
			continue
		}
		for _, scene := range binding.TriggerScenes {
			if !coverage[binding.RuleID+"/"+scene+"/positive"] || !coverage[binding.RuleID+"/"+scene+"/negative"] {
				return fmt.Errorf("%w: missing positive/negative example for %s/%s", ErrReplayRequired, binding.RuleID, scene)
			}
		}
		for _, id := range append(append([]string{}, binding.PositiveExampleRefs...), binding.NegativeExampleRefs...) {
			if !seen[id] {
				return fmt.Errorf("%w: required binding example %s missing", ErrReplayRequired, id)
			}
		}
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
