package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	template "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

const operationApplyLegacyRuleOverrides operationName = "applyLegacyRuleOverrides"

// LegacyRuleOverridesInput can change only a project-owned effective snapshot;
// the source identity and reviewed policy are read from the current copy.
type LegacyRuleOverridesInput struct {
	ExpectedProjectVersion      int64                        `json:"expected_project_version"`
	ExpectedTemplateCopyVersion int64                        `json:"expected_template_copy_version"`
	Reason                      string                       `json:"reason"`
	Overrides                   []governance.BindingOverride `json:"overrides"`
}

// legacyProjectionMatches applies the same exact projection comparison used by
// frozen delivery. It prevents editing a copy whose rules and snapshot already
// disagree and must not be replaced by a client-provided hash.
func legacyProjectionMatches(copy *ProjectTemplateCopy) bool {
	if copy == nil || copy.RuleSetSnapshot == nil {
		return false
	}
	rules, err := template.CompileRuleSet(*copy.RuleSetSnapshot)
	if err != nil {
		return false
	}
	a, err := json.Marshal(rules)
	if err != nil {
		return false
	}
	b, err := json.Marshal(copy.Definition.Rules)
	return err == nil && string(a) == string(b)
}

// legacyRuleSetSource finds immutable source evidence within this project's
// existing copy history, in the same transaction as the proposed write. No
// catalog read is needed, so withdrawing a source cannot rewrite old copies.
func legacyRuleSetSource(tx Transaction, actor Actor, p Project) (*template.RuleSet, error) {
	current := p.TemplateCopy
	if current == nil || current.RuleSetSnapshot == nil || current.SourcePolicyHash == "" {
		return nil, ErrInvalidState
	}
	if current.legacySourceRuleSetSnapshot != nil {
		if err := validateLegacySourceSnapshot(current); err != nil {
			return nil, err
		}
		return template.Clone(template.Template{RuleSetSnapshot: current.legacySourceRuleSetSnapshot}).RuleSetSnapshot, nil
	}
	match := func(candidate *ProjectTemplateCopy) *template.RuleSet {
		if candidate == nil || candidate.RuleSetSnapshot == nil || candidate.SourcePolicyHash != current.SourcePolicyHash ||
			candidate.SourceRuleSetID != current.SourceRuleSetID || candidate.SourceRuleSetVersion != current.SourceRuleSetVersion ||
			candidate.SourceRuleSetHash != current.SourceRuleSetHash {
			return nil
		}
		set := candidate.RuleSetSnapshot
		if set.ID != current.SourceRuleSetID || set.Version != current.SourceRuleSetVersion ||
			set.RulesetHash != current.SourceRuleSetHash || template.ValidateRuleSet(*set) != nil {
			return nil
		}
		return template.Clone(template.Template{RuleSetSnapshot: set}).RuleSetSnapshot
	}
	if source := match(current); source != nil {
		return source, nil
	}
	reader, ok := tx.(interface {
		ProjectTemplateCopy(uint64, string, int64) (ProjectTemplateCopy, error)
	})
	if !ok {
		return nil, ErrInvalidState
	}
	for version := p.TemplateCopyVersion - 1; version > 0; version-- {
		candidate, err := reader.ProjectTemplateCopy(actor.TenantID, p.ID, version)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if source := match(&candidate); source != nil {
			return source, nil
		}
	}
	return nil, fmt.Errorf("%w: original legacy RuleSet snapshot is unavailable", ErrInvalidState)
}

// ApplyLegacyRuleOverrides appends an immutable draft copy after evaluating
// the server-persisted policy and source proof in the project transaction.
func (s *Service) ApplyLegacyRuleOverrides(ctx context.Context, actor Actor, projectID, key string, input LegacyRuleOverridesInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || len(input.Overrides) == 0 ||
		len(input.Overrides) > 100 || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationApplyLegacyRuleOverrides, projectID, key, input,
		authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[Transaction],
		func(tx Transaction, p Project) (operationResult[Project], error) {
			if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyDraft ||
				p.TemplateCopy.Definition.RuleSetSnapshot != nil || p.TemplateCopy.Definition.RuleSetRef != nil || p.TemplateCopy.RuleSetSnapshot == nil {
				return operationResult[Project]{}, ErrInvalidState
			}
			if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion {
				return operationResult[Project]{}, ErrVersionConflict
			}
			if validateProjectTemplateCopy(p.TemplateCopy) != nil || !legacyProjectionMatches(p.TemplateCopy) {
				return operationResult[Project]{}, ErrInvalidState
			}
			policyReader, ok := tx.(interface {
				ProjectBindingPolicies(Project) ([]governance.BindingPolicy, error)
			})
			if !ok {
				return operationResult[Project]{}, ErrInvalidState
			}
			policies, err := policyReader.ProjectBindingPolicies(p)
			if err != nil {
				return operationResult[Project]{}, err
			}
			policyHash, err := governance.HashBindingPolicies(policies)
			if err != nil || policyHash != p.TemplateCopy.SourcePolicyHash || len(policies) == 0 {
				return operationResult[Project]{}, ErrInvalidState
			}
			source, err := legacyRuleSetSource(tx, actor, p)
			if err != nil {
				return operationResult[Project]{}, err
			}
			if err := governance.ValidateBindingPolicies(p.TemplateCopy.Definition, *source, policies); err != nil {
				return operationResult[Project]{}, fmt.Errorf("%w: invalid reviewed source policy", ErrInvalidState)
			}
			effective, err := governance.ApplyBindingOverrides(*p.TemplateCopy.RuleSetSnapshot, "project-"+p.ID,
				strconv.FormatInt(p.TemplateCopyVersion+1, 10), policies, input.Overrides)
			if err != nil {
				return operationResult[Project]{}, semanticBindingError(err)
			}
			if err := governance.ValidateChangedBindingTargets(p.TemplateCopy.Definition, *p.TemplateCopy.RuleSetSnapshot, effective); err != nil {
				return operationResult[Project]{}, semanticBindingError(err)
			}
			if reflect.DeepEqual(effective.Bindings, p.TemplateCopy.RuleSetSnapshot.Bindings) {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			definition := template.Clone(p.TemplateCopy.Definition)
			definition.Rules, err = template.CompileRuleSet(effective)
			if err != nil {
				return operationResult[Project]{}, semanticBindingError(err)
			}
			definition, err = template.WithHashes(definition)
			if err != nil {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			if err := requireResolvedTemplateMigration(buildTemplateMigrationPreview(p, definition, p.ProjectVersion)); err != nil {
				return operationResult[Project]{}, err
			}
			copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, definition, TemplateCopyDraft)
			if err != nil {
				return operationResult[Project]{}, err
			}
			copy.SourceKind = p.TemplateCopy.SourceKind
			copy.RuleSetSnapshot = &effective
			copy.RulesetHash = effective.RulesetHash
			copy.SourceRuleSetID, copy.SourceRuleSetVersion, copy.SourceRuleSetHash = p.TemplateCopy.SourceRuleSetID, p.TemplateCopy.SourceRuleSetVersion, p.TemplateCopy.SourceRuleSetHash
			copy.SourcePolicyHash, copy.BindingPolicies = policyHash, cloneRuleSetSourcePolicies(policies)
			copy.legacySourceRuleSetSnapshot = source
			if err := validateLegacySourceSnapshot(copy); err != nil || validateProjectTemplateCopy(copy) != nil || !legacyProjectionMatches(copy) {
				return operationResult[Project]{}, ErrInvalidState
			}
			next := p
			next.ProjectVersion++
			next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return operationResult[Project]{}, err
			}
			ids := make([]string, 0, len(input.Overrides))
			for _, override := range input.Overrides {
				ids = append(ids, override.RuleID)
			}
			if err := recordAuditEvent(tx, actor, projectID, "project_template.override_ruleset", copy.ID, map[string]any{
				"reason": strings.TrimSpace(input.Reason), "copy_version": copy.Version, "source_ruleset_id": copy.SourceRuleSetID,
				"source_ruleset_version": copy.SourceRuleSetVersion, "source_ruleset_hash": copy.SourceRuleSetHash,
				"source_policy_hash": policyHash, "effective_ruleset_hash": copy.RulesetHash, "rule_ids": ids,
			}); err != nil {
				return operationResult[Project]{}, err
			}
			return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
		})
}

func (s *Service) ApplyLegacyRuleOverridesResult(ctx context.Context, actor Actor, projectID, key string, input LegacyRuleOverridesInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.ApplyLegacyRuleOverrides(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}
