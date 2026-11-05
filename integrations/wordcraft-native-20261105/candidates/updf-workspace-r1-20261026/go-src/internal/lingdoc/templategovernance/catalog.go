package templategovernance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

func (s *Service) CreateProfile(ctx context.Context, actor Actor, key string, input CreateProfileInput) (Profile, error) {
	if err := s.ready(actor); err != nil {
		return Profile{}, err
	}
	if !validKey(key) || !identifier.MatchString(input.ID) || strings.TrimSpace(input.Name) == "" || len(input.Name) > 240 || !validReason(input.Reason) {
		return Profile{}, ErrInvalidRequest
	}
	var result Profile
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionCreateProfile, input.ID); err != nil {
			return err
		}
		op := operationIdentity(actor, "create_profile", input.ID, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			current, err := loadProfile(tx, actor, input.ID)
			if err != nil {
				return err
			}
			if current.Revision != result.Revision {
				return ErrVersionConflict
			}
			return nil
		}
		now := time.Now().UTC()
		row := profileRow{TenantID: actor.TenantID, TemplateID: input.ID, Name: strings.TrimSpace(input.Name), Revision: 1, CreatedBy: actor.UserID, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return ErrVersionConflict
		}
		result = Profile{TenantID: actor.TenantID, ID: input.ID, Name: row.Name, Revision: 1, CreatedBy: actor.UserID, CreatedAt: now}
		if err := recordAudit(tx, actor, ActionCreateProfile, input.ID, input.Reason, result); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, result.Revision)
	})
	return result, err
}
func (s *Service) ListProfiles(ctx context.Context, actor Actor, filter CatalogFilter) ([]Profile, error) {
	if err := s.ready(actor); err != nil {
		return nil, err
	}
	if filter.State != "" && filter.State != template.RuleSetDraft && filter.State != template.RuleSetPublished && filter.State != template.RuleSetWithdrawn && filter.State != template.RuleSetRecalled {
		return nil, ErrInvalidRequest
	}
	result := make([]Profile, 0)
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, ""); err != nil {
			return err
		}
		q := tx.Model(&profileRow{}).Where("lingdoc_template_profiles.tenant_id=?", actor.TenantID)
		if filter.Scope != "" || filter.State != "" {
			q = q.Joins("JOIN lingdoc_template_versions v ON v.tenant_id=lingdoc_template_profiles.tenant_id AND v.template_id=lingdoc_template_profiles.template_id")
			if filter.Scope != "" {
				q = q.Where("v.scope=?", filter.Scope)
			}
			if filter.State != "" {
				q = q.Where("v.state=?", string(filter.State))
			}
		}
		var rows []profileRow
		if err := q.Distinct("lingdoc_template_profiles.*").Order("lingdoc_template_profiles.template_id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, row.TemplateID); err != nil {
				return err
			}
			result = append(result, Profile{TenantID: actor.TenantID, ID: row.TemplateID, Name: row.Name, Revision: row.Revision, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt})
		}
		return nil
	})
	return result, err
}
func (s *Service) GetVersion(ctx context.Context, actor Actor, id, version string) (TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return TemplateVersion{}, err
	}
	if !identifier.MatchString(id) || !identifier.MatchString(version) {
		return TemplateVersion{}, ErrInvalidRequest
	}
	var result TemplateVersion
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, id); err != nil {
			return err
		}
		var err error
		result, err = loadVersion(tx, actor, id, version)
		return err
	})
	return result, err
}
func (s *Service) ListVersions(ctx context.Context, actor Actor, id string) ([]TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return nil, err
	}
	if !identifier.MatchString(id) {
		return nil, ErrInvalidRequest
	}
	result := make([]TemplateVersion, 0)
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, id); err != nil {
			return err
		}
		if _, err := loadProfile(tx, actor, id); err != nil {
			return err
		}
		var rows []versionRow
		if err := tx.Where("tenant_id=? AND template_id=?", actor.TenantID, id).Order("created_at DESC,version DESC").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			v, err := decodeVersion(row)
			if err != nil {
				return err
			}
			result = append(result, v)
		}
		return nil
	})
	return result, err
}

// ResolveForCopy participates in the caller's project transaction. It verifies
// current tenant permissions and both publication gates before copying; the
// caller must persist its project copy and copy audit in that same transaction.
func (s *Service) ResolveForCopy(ctx context.Context, tx *gorm.DB, actor Actor, id, version string) (TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return TemplateVersion{}, err
	}
	if tx == nil {
		return TemplateVersion{}, ErrUnavailable
	}
	if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, id); err != nil {
		return TemplateVersion{}, err
	}
	v, err := loadVersion(tx, actor, id, version)
	if err != nil {
		return v, err
	}
	if v.State != template.RuleSetPublished {
		return TemplateVersion{}, ErrInvalidState
	}
	if _, err := s.bindableRuleSet(ctx, tx, actor, v.RuleSet); err != nil {
		return TemplateVersion{}, err
	}
	return v, nil
}
func (s *Service) bindableRuleSet(ctx context.Context, tx *gorm.DB, actor Actor, ref RuleSetRef) (template.RuleSet, error) {
	if s.ports.RuleSets == nil {
		return template.RuleSet{}, ErrUnauthorized
	}
	set, err := rulesetstore.NewStore(tx, s.ports.RuleSets).GetVersion(ctx, actor, ref.ID, ref.Version)
	if err != nil {
		return set, err
	}
	if err := template.CanBindRuleSet(set); err != nil {
		return template.RuleSet{}, err
	}
	if ref.Hash != "" && ref.Hash != set.RulesetHash {
		return template.RuleSet{}, ErrVersionConflict
	}
	return set, nil
}
func validSourceRefs(refs []SourceRef) bool {
	seen := map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.Version) == "" || seen[ref.ID] {
			return false
		}
		seen[ref.ID] = true
	}
	return true
}
func (s *Service) validateDraftInput(input SaveDraftInput) error {
	if !identifier.MatchString(input.TemplateID) || !identifier.MatchString(input.Version) || len(input.Version) > 64 || input.ExpectedRevision < 0 || !validReason(input.Reason) || strings.TrimSpace(input.Scope) == "" || strings.TrimSpace(input.Curator) == "" || !validSourceRefs(input.Sources) {
		return ErrInvalidRequest
	}
	switch input.SourceType {
	case "lingdoc_builtin", "organization_input", "external_reference", "project_copy":
	default:
		return ErrInvalidRequest
	}
	if input.SourceType != "lingdoc_builtin" && len(input.Sources) == 0 {
		return ErrSourceUnavailable
	}
	if input.Definition.ContentHash != "" || input.Definition.RulesetHash != "" || len(input.Definition.Rules) != 0 {
		return ErrInvalidRequest
	}
	if input.Definition.ID != "" && input.Definition.ID != input.TemplateID {
		return ErrInvalidRequest
	}
	if input.Definition.Version != "" && input.Definition.Version != input.Version {
		return ErrInvalidRequest
	}
	return nil
}
func (s *Service) checkTemplateSources(ctx context.Context, actor Actor, refs []SourceRef) error {
	if len(refs) == 0 {
		return nil
	}
	if s.ports.Sources == nil {
		return ErrSourceUnavailable
	}
	return s.ports.Sources.VerifyTemplateSources(ctx, actor, refs)
}
func (s *Service) SaveDraft(ctx context.Context, actor Actor, key string, input SaveDraftInput) (TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return TemplateVersion{}, err
	}
	if !validKey(key) {
		return TemplateVersion{}, ErrInvalidRequest
	}
	if err := s.validateDraftInput(input); err != nil {
		return TemplateVersion{}, err
	}
	// Source IO may use the same single-connection SQLite pool; don't hold a
	// transaction while fetching it. Current grants are rechecked in the final tx.
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		return s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionSaveDraft, input.TemplateID)
	}); err != nil {
		return TemplateVersion{}, err
	}
	if err := s.checkTemplateSources(ctx, actor, input.Sources); err != nil {
		return TemplateVersion{}, err
	}
	var result TemplateVersion
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionSaveDraft, input.TemplateID); err != nil {
			return err
		}
		if err := s.checkSourcesInTransaction(ctx, tx, actor, input.Sources); err != nil {
			return err
		}
		profile, err := loadProfile(tx, actor, input.TemplateID)
		if err != nil {
			return err
		}
		op := operationIdentity(actor, "save_draft", input.TemplateID+"/"+input.Version, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			current, err := loadVersion(tx, actor, input.TemplateID, input.Version)
			if err != nil {
				return err
			}
			if current.Revision != result.Revision || current.State != result.State {
				return ErrVersionConflict
			}
			_, err = s.bindableRuleSet(ctx, tx, actor, current.RuleSet)
			return err
		}
		previous, err := loadVersion(tx, actor, input.TemplateID, input.Version)
		if errors.Is(err, ErrNotFound) {
			previous = TemplateVersion{}
		} else if err != nil {
			return err
		}
		if previous.Revision != input.ExpectedRevision {
			return ErrVersionConflict
		}
		if previous.Revision > 0 && previous.State != template.RuleSetDraft {
			return ErrInvalidState
		}
		set, err := s.bindableRuleSet(ctx, tx, actor, input.RuleSet)
		if err != nil {
			return err
		}
		def := template.Clone(input.Definition)
		def.ID = input.TemplateID
		def.Version = input.Version
		if strings.TrimSpace(def.Name) == "" {
			def.Name = profile.Name
		}
		def.Source = input.SourceType
		def.Scope = input.Scope
		def.IsDemo = input.SourceType == "lingdoc_builtin" && input.Scope == "internal-demo"
		def.Rules, err = template.CompileRuleSet(set)
		if err != nil {
			return err
		}
		def, err = template.WithHashes(def)
		if err != nil {
			return err
		}
		if err := ValidateBindingPolicies(def, set, input.BindingPolicies); err != nil {
			return err
		}
		policyHash, err := HashBindingPolicies(input.BindingPolicies)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		createdBy := actor.UserID
		createdAt := now
		if previous.Revision > 0 {
			createdBy = previous.CreatedBy
			createdAt = previous.CreatedAt
		}
		result = TemplateVersion{BindingPolicies: input.BindingPolicies, PolicyHash: policyHash, TemplateID: input.TemplateID, Version: input.Version, Revision: previous.Revision + 1, State: template.RuleSetDraft, Definition: def, RuleSet: RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}, RuleSetSnapshot: set, SourceType: input.SourceType, Scope: input.Scope, Sources: append([]SourceRef{}, input.Sources...), Curator: input.Curator, ContentHash: def.ContentHash, RulesetHash: set.RulesetHash, CreatedBy: createdBy, CreatedAt: createdAt, UpdatedAt: now}
		if err := saveVersion(tx, actor, previous, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionSaveDraft, op.Target, input.Reason, result); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, result.Revision)
	})
	return result, err
}
func (s *Service) Publish(ctx context.Context, actor Actor, key string, input PublishInput) (TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return TemplateVersion{}, err
	}
	if !validKey(key) || input.ExpectedRevision < 1 || !validReason(input.Reason) || len(input.ReplayFixtureIDs) == 0 {
		return TemplateVersion{}, ErrInvalidRequest
	}
	if s.ports.Replay == nil {
		return TemplateVersion{}, ErrReplayRequired
	}
	current, err := s.GetVersion(ctx, actor, input.TemplateID, input.Version)
	if err != nil {
		return TemplateVersion{}, err
	}
	if err := s.withTx(ctx, func(tx *gorm.DB) error {
		return s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionPublish, input.TemplateID)
	}); err != nil {
		return TemplateVersion{}, err
	}
	if err := s.checkTemplateSources(ctx, actor, current.Sources); err != nil {
		return TemplateVersion{}, err
	}
	if err := s.ports.Replay.Verify(ctx, actor, current, append([]string{}, input.ReplayFixtureIDs...)); err != nil {
		return TemplateVersion{}, err
	}
	var result TemplateVersion
	err = s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionPublish, input.TemplateID); err != nil {
			return err
		}
		v, err := loadVersion(tx, actor, input.TemplateID, input.Version)
		if err != nil {
			return err
		}
		if err := s.checkSourcesInTransaction(ctx, tx, actor, v.Sources); err != nil {
			return err
		}
		if _, err := s.bindableRuleSet(ctx, tx, actor, v.RuleSet); err != nil {
			return err
		}
		op := operationIdentity(actor, "publish", input.TemplateID+"/"+input.Version, key)
		found, err := operationGet(tx, op, input, &result)
		if err != nil {
			return err
		}
		if found {
			if v.Revision != result.Revision || v.State != template.RuleSetPublished {
				return ErrVersionConflict
			}
			return nil
		}
		if v.Revision != input.ExpectedRevision || v.Revision != current.Revision || v.ContentHash != current.ContentHash || v.RulesetHash != current.RulesetHash || v.PolicyHash != current.PolicyHash {
			return ErrVersionConflict
		}
		if v.State != template.RuleSetDraft {
			return ErrInvalidState
		}
		now := time.Now().UTC()
		result = v
		result.Revision++
		result.State = template.RuleSetPublished
		result.PublishedAt = &now
		result.UpdatedAt = now
		if err := ValidateVersion(result); err != nil {
			return err
		}
		if err := saveVersion(tx, actor, v, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, ActionPublish, op.Target, input.Reason, map[string]any{"version": result, "replay_fixture_ids": input.ReplayFixtureIDs}); err != nil {
			return err
		}
		return operationSave(tx, op, input, result, result.Revision)
	})
	return result, err
}
func (s *Service) Withdraw(ctx context.Context, actor Actor, key string, input GovernanceInput) (TemplateVersion, error) {
	return s.transition(ctx, actor, key, input, ActionWithdraw, "")
}
func (s *Service) Recall(ctx context.Context, actor Actor, key string, input RecallInput) (TemplateVersion, error) {
	if strings.TrimSpace(input.MigrationAdvice) == "" || s.ports.Usage == nil {
		return TemplateVersion{}, ErrInvalidRequest
	}
	return s.transition(ctx, actor, key, input.GovernanceInput, ActionRecall, input.MigrationAdvice)
}
func (s *Service) transition(ctx context.Context, actor Actor, key string, input GovernanceInput, action Action, advice string) (TemplateVersion, error) {
	if err := s.ready(actor); err != nil {
		return TemplateVersion{}, err
	}
	if !validKey(key) || input.ExpectedRevision < 1 || !validReason(input.Reason) {
		return TemplateVersion{}, ErrInvalidRequest
	}
	request := struct {
		GovernanceInput
		Advice string
	}{input, advice}
	var result TemplateVersion
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, action, input.TemplateID); err != nil {
			return err
		}
		current, err := loadVersion(tx, actor, input.TemplateID, input.Version)
		if err != nil {
			return err
		}
		op := operationIdentity(actor, string(action), input.TemplateID+"/"+input.Version, key)
		found, err := operationGet(tx, op, request, &result)
		if err != nil {
			return err
		}
		if found {
			if current.Revision != result.Revision || current.State != result.State {
				return ErrVersionConflict
			}
			return nil
		}
		if current.Revision != input.ExpectedRevision {
			return ErrVersionConflict
		}
		if current.State != template.RuleSetPublished {
			return ErrInvalidState
		}
		result = current
		result.Revision++
		result.UpdatedAt = time.Now().UTC()
		if action == ActionWithdraw {
			result.State = template.RuleSetWithdrawn
		} else {
			projects, err := s.ports.Usage.AffectedProjects(ctx, tx, actor, input.TemplateID, input.Version)
			if err != nil {
				return err
			}
			result.State = template.RuleSetRecalled
			result.Recall = &RecallRecord{Reason: input.Reason, MigrationAdvice: advice, AffectedProjectIDs: projects, ActorID: actor.UserID, RecordedAt: result.UpdatedAt}
		}
		if err := saveVersion(tx, actor, current, result); err != nil {
			return err
		}
		if err := recordAudit(tx, actor, action, op.Target, input.Reason, result); err != nil {
			return err
		}
		return operationSave(tx, op, request, result, result.Revision)
	})
	return result, err
}
