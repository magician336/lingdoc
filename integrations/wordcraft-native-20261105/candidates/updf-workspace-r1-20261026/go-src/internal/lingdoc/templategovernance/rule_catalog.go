package templategovernance

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

type RuleDraftInput struct {
	Draft  template.RuleSet `json:"draft"`
	Reason string           `json:"reason"`
}
type RuleGovernanceInput struct {
	ExpectedState       template.RuleSetState `json:"expected_state"`
	ExpectedRuleSetHash string                `json:"expected_ruleset_hash"`
	Reason              string                `json:"reason"`
	ReplayFixtureIDs    []string              `json:"replay_fixture_ids"`
	TemplateID          string                `json:"template_id"`
	TemplateVersion     string                `json:"template_version"`
}

func (s *Service) GetRuleSetVersion(ctx context.Context, actor Actor, id, version string) (template.RuleSet, error) {
	if err := s.ready(actor); err != nil {
		return template.RuleSet{}, err
	}
	if s.ports.RuleSets == nil {
		return template.RuleSet{}, ErrUnauthorized
	}
	return rulesetstore.NewStore(s.db, s.ports.RuleSets).GetVersion(ctx, actor, id, version)
}
func (s *Service) ListRuleSetVersions(ctx context.Context, actor Actor, id string) ([]template.RuleSet, error) {
	if err := s.ready(actor); err != nil {
		return nil, err
	}
	if !identifier.MatchString(id) || s.ports.RuleSets == nil {
		return nil, ErrInvalidRequest
	}
	// The existing store's empty-list path has no row to authorize. Check it here.
	var result []template.RuleSet
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.RuleSets.AuthorizeRuleSet(ctx, tx, actor, rulesetstore.ActionRead, nil); err != nil {
			return err
		}
		var err error
		result, err = rulesetstore.NewStore(tx, s.ports.RuleSets).ListVersions(ctx, actor, id)
		return err
	})
	return result, err
}
func (s *Service) CreateRuleSetDraft(ctx context.Context, actor Actor, key string, input RuleDraftInput) (template.RuleSet, error) {
	if err := s.ready(actor); err != nil {
		return template.RuleSet{}, err
	}
	if !validKey(key) || !validReason(input.Reason) || input.Draft.State != template.RuleSetDraft || input.Draft.RulesetHash != "" || s.ports.RuleSets == nil {
		return template.RuleSet{}, ErrInvalidRequest
	}
	if err := template.ValidateRuleSet(input.Draft); err != nil {
		return template.RuleSet{}, err
	}
	var result template.RuleSet
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.RuleSets.AuthorizeRuleSet(ctx, tx, actor, rulesetstore.ActionCreateDraft, &input.Draft); err != nil {
			return err
		}
		op := operationIdentity(actor, "ruleset_create_draft", input.Draft.ID+"/"+input.Draft.Version, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		store := rulesetstore.NewStore(tx, s.ports.RuleSets)
		if found {
			current, err := store.GetVersion(ctx, actor, result.ID, result.Version)
			if err != nil {
				return err
			}
			a, err := hashJSON(current)
			if err != nil {
				return err
			}
			b, _ := hashJSON(result)
			if a != b {
				return ErrVersionConflict
			}
			return nil
		}
		if err := store.CreateDraft(ctx, actor, input.Draft); err != nil {
			return err
		}
		result = input.Draft
		if err := recordAudit(tx, actor, Action("ruleset_create_draft"), digestText(op.Target), input.Reason, map[string]any{"ruleset": result}); err != nil {
			return err
		}
		basis, err := template.HashRuleSet(result)
		if err != nil {
			return err
		}
		return operationSave(tx, op, input, result, basis)
	})
	return result, err
}
func (s *Service) PublishRuleSet(ctx context.Context, actor Actor, id, version, key string, input RuleGovernanceInput) (template.RuleSet, error) {
	return s.governRuleSet(ctx, actor, id, version, key, input, rulesetstore.ActionPublish)
}
func (s *Service) WithdrawRuleSet(ctx context.Context, actor Actor, id, version, key string, input RuleGovernanceInput) (template.RuleSet, error) {
	return s.governRuleSet(ctx, actor, id, version, key, input, rulesetstore.ActionWithdraw)
}
func (s *Service) RecallRuleSet(ctx context.Context, actor Actor, id, version, key string, input RuleGovernanceInput) (template.RuleSet, error) {
	return s.governRuleSet(ctx, actor, id, version, key, input, rulesetstore.ActionRecall)
}
func (s *Service) ruleReplayVersion(ctx context.Context, tx *gorm.DB, actor Actor, input RuleGovernanceInput, set template.RuleSet) (TemplateVersion, error) {
	var v TemplateVersion
	var err error
	if input.TemplateID == template.DemoTemplateID && input.TemplateVersion == template.DemoTemplateVersion {
		def := template.DemoTemplate()
		v = TemplateVersion{TemplateID: def.ID, Version: def.Version, Revision: 1, State: template.RuleSetDraft, Definition: def, BindingPolicies: []BindingPolicy{}}
	} else {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, input.TemplateID); err != nil {
			return v, err
		}
		v, err = loadVersion(tx, actor, input.TemplateID, input.TemplateVersion)
		if err != nil {
			return v, err
		}
		if v.State != template.RuleSetDraft && v.State != template.RuleSetPublished {
			return v, ErrInvalidState
		}
	}
	v.RuleSetSnapshot = set
	v.RuleSet = RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	v.RulesetHash = set.RulesetHash
	v.Definition.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		return v, err
	}
	v.Definition, err = template.WithHashes(v.Definition)
	if err != nil {
		return v, err
	}
	v.ContentHash = v.Definition.ContentHash
	v.PolicyHash, err = HashBindingPolicies(v.BindingPolicies)
	if err != nil {
		return v, err
	}
	return v, ValidateVersion(v)
}
func (s *Service) governRuleSet(ctx context.Context, actor Actor, id, version, key string, input RuleGovernanceInput, action rulesetstore.Action) (template.RuleSet, error) {
	if err := s.ready(actor); err != nil {
		return template.RuleSet{}, err
	}
	if !identifier.MatchString(id) || !identifier.MatchString(version) || !validKey(key) || !validReason(input.Reason) || s.ports.RuleSets == nil {
		return template.RuleSet{}, ErrInvalidRequest
	}
	var result template.RuleSet
	var current template.RuleSet
	var replay TemplateVersion
	replayed := false
	op := operationIdentity(actor, "ruleset_"+string(action), id+"/"+version, key)
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		store := rulesetstore.NewStore(tx, s.ports.RuleSets)
		var err error
		current, err = store.GetVersion(ctx, actor, id, version)
		if err != nil {
			return err
		}
		if err = s.ports.RuleSets.AuthorizeRuleSet(ctx, tx, actor, action, &current); err != nil {
			return err
		}
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			a, _ := hashJSON(current)
			b, _ := hashJSON(result)
			if a != b {
				return ErrVersionConflict
			}
			replayed = true
			return nil
		}
		if current.State != input.ExpectedState || current.RulesetHash != input.ExpectedRuleSetHash {
			return ErrVersionConflict
		}
		if action == rulesetstore.ActionPublish {
			if s.ports.Replay == nil || len(input.ReplayFixtureIDs) == 0 || strings.TrimSpace(input.TemplateID) == "" || strings.TrimSpace(input.TemplateVersion) == "" {
				return ErrReplayRequired
			}
			published, err := template.PublishRuleSet(current)
			if err != nil {
				return err
			}
			replay, err = s.ruleReplayVersion(ctx, tx, actor, input, published)
			return err
		}
		return nil
	})
	if err != nil || replayed {
		return result, err
	}
	if action == rulesetstore.ActionPublish {
		if err := s.checkTemplateSources(ctx, actor, replay.Sources); err != nil {
			return result, err
		}
		if err := s.ports.Replay.Verify(ctx, actor, replay, input.ReplayFixtureIDs); err != nil {
			return result, err
		}
	}
	err = s.withTx(ctx, func(tx *gorm.DB) error {
		store := rulesetstore.NewStore(tx, s.ports.RuleSets)
		latest, err := store.GetVersion(ctx, actor, id, version)
		if err != nil {
			return err
		}
		if err := s.ports.RuleSets.AuthorizeRuleSet(ctx, tx, actor, action, &latest); err != nil {
			return err
		}
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			a, _ := hashJSON(latest)
			b, _ := hashJSON(result)
			if a != b {
				return ErrVersionConflict
			}
			return nil
		}
		a, _ := hashJSON(latest)
		b, _ := hashJSON(current)
		if a != b || latest.State != input.ExpectedState || latest.RulesetHash != input.ExpectedRuleSetHash {
			return ErrVersionConflict
		}
		switch action {
		case rulesetstore.ActionPublish:
			published, err := template.PublishRuleSet(latest)
			if err != nil {
				return err
			}
			now, err := s.ruleReplayVersion(ctx, tx, actor, input, published)
			if err != nil {
				return err
			}
			a, _ := hashJSON(now)
			b, _ := hashJSON(replay)
			if a != b {
				return ErrVersionConflict
			}
			if err := s.checkSourcesInTransaction(ctx, tx, actor, now.Sources); err != nil {
				return err
			}
			result, err = store.Publish(ctx, actor, id, version)
			if err != nil {
				return err
			}
		case rulesetstore.ActionWithdraw:
			result, err = store.Withdraw(ctx, actor, id, version)
			if err != nil {
				return err
			}
		case rulesetstore.ActionRecall:
			result, err = store.Recall(ctx, actor, id, version)
			if err != nil {
				return err
			}
		default:
			return ErrInvalidRequest
		}
		if err := recordAudit(tx, actor, Action("ruleset_"+string(action)), digestText(op.Target), input.Reason, map[string]any{"ruleset": result, "replay_fixture_ids": input.ReplayFixtureIDs, "policy_hash": replay.PolicyHash}); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, map[string]any{"state": result.State, "hash": result.RulesetHash})
	})
	return result, err
}
