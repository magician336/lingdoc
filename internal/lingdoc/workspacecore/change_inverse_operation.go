package workspacecore

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// Only server-owned reverse planning may bypass the field-only compatibility
// input check. The provenance context key grants no such authority.
type controlledInverseKey struct{}
type inverseTemplatePlanKey struct{}
type inverseTemplatePlan struct {
	SourceID   string
	Definition lingdoctemplate.Template
}

type reverseChangeSetInput struct {
	SourceChangeSetID string `json:"source_change_set_id"`
}

// The idempotency input deliberately excludes the current revision and current
// values. Owner/source checks still run before replay; inverse planning runs
// only after runOperation has proved there is no saved result.
func (s *Service) reverseChangeSetOperation(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	input := reverseChangeSetInput{SourceChangeSetID: changeID}
	return runOperation(s, ctx, actor, sourceOperationInput(operationReverseChangeSet, projectID+"/"+changeID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, ""), reverseChangeSetSources), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		source, err := inverseSourceAccess(tx, p, actor, changeID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		semantic, err := BuildInverseSemanticDeltas(tx, p, source)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		fields := map[string]ChangeFieldInput{}
		for _, field := range source.Fields {
			if p.Spec[field.Key] != field.NewValue {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			fields[field.Key] = ChangeFieldInput{OldValue: field.NewValue, NewValue: field.OldValue}
		}
		create := CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision,
			Reason: "逆向变更 " + source.ID, Fields: fields, AffectedChapterIDs: []string{},
			ReversesChangeSetID: source.ID}
		for _, impact := range source.Impacts {
			create.AffectedChapterIDs = append(create.AffectedChapterIDs, impact.ChapterID)
		}
		mutationCtx := context.WithValue(ctx, controlledInverseKey{}, source.ID)
		mutationCtx = context.WithValue(mutationCtx, g1ReverseSourceKey{}, source.ID)
		if source.TemplateUpgrade != nil {
			baseline, oldCopy, err := inverseTemplateBaseline(tx, actor, p.ID, source)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			definition := lingdoctemplate.Clone(oldCopy.Definition)
			upgrade := &TemplateUpgradeInput{
				ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion,
				Fields:         append([]lingdoctemplate.Field{}, definition.Fields...),
				Sections:       append([]lingdoctemplate.Section{}, definition.Sections...),
				Terms:          append([]lingdoctemplate.Term{}, definition.Terms...),
				RequiredFields: slices.Clone(definition.RequiredFields),
				Rules:          append([]lingdoctemplate.Rule{}, definition.Rules...),
				FieldValues:    maps.Clone(baseline.Spec),
			}
			if ref := lingdoctemplate.RuleSetReferenceFor(definition); ref != nil {
				value := *ref
				upgrade.RuleSetRef = &value
				// An explicit empty slice clears current same-ref overrides.
				// nil would mean "inherit current overrides" to the binder.
				upgrade.RuleBindingOverrides = append([]lingdoctemplate.RuleBindingOverride{}, lingdoctemplate.CloneRuleBindingOverrides(definition.RuleBindingOverrides)...)
			}
			create.TemplateUpgrade = upgrade
			mutationCtx = context.WithValue(mutationCtx, inverseTemplatePlanKey{}, inverseTemplatePlan{SourceID: source.ID, Definition: definition})
		}
		if len(semantic) > 0 {
			if create.TemplateUpgrade != nil {
				return operationResult[ChangeSet]{}, ErrInvalidRequest
			}
			draft := ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: create.Reason,
				Fields: fields, AffectedChapterIDs: create.AffectedChapterIDs, SemanticDeltas: semantic}
			if err := validateExtendedChangeInput(draft); err != nil {
				return operationResult[ChangeSet]{}, err
			}
			return s.createChangeDraftMutation(mutationCtx, actor, projectID, draft)(tx, p)
		}
		if err := validateChangeSetInput(create); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		changeTx, err := mutationPort[ChangeSetMutationTransaction](tx)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return s.createAssessedChangeMutation(mutationCtx, actor, projectID, create)(changeTx, p)
	})
}

func inverseTemplateBaseline(tx Transaction, actor Actor, projectID string, source ChangeSet) (ContextRevisionRecord, ProjectTemplateCopy, error) {
	var baseline ContextRevisionRecord
	if source.ProjectID != projectID || source.TemplateUpgrade == nil {
		return baseline, ProjectTemplateCopy{}, ErrInvalidState
	}
	records, ok := tx.(interface {
		ContextRevisions(string) ([]ContextRevisionRecord, error)
	})
	if !ok {
		return baseline, ProjectTemplateCopy{}, ErrInvalidState
	}
	history, err := records.ContextRevisions(projectID)
	if err != nil {
		return baseline, ProjectTemplateCopy{}, err
	}
	found := false
	for _, revision := range history {
		if revision.Revision == source.BaseContextRevision {
			if found {
				return baseline, ProjectTemplateCopy{}, ErrInvalidState
			}
			baseline, found = revision, true
		}
	}
	if !found || baseline.ProjectID != projectID || baseline.SpecRevision != source.BaseSpecRevision ||
		baseline.TemplateCopyVersion != source.TemplateUpgrade.BaseTemplateCopyVersion {
		return baseline, ProjectTemplateCopy{}, ErrInvalidState
	}
	reader, ok := tx.(interface {
		ProjectTemplateCopy(uint64, string, int64) (ProjectTemplateCopy, error)
	})
	if !ok {
		return baseline, ProjectTemplateCopy{}, ErrInvalidState
	}
	copy, err := reader.ProjectTemplateCopy(actor.TenantID, projectID, source.TemplateUpgrade.BaseTemplateCopyVersion)
	return baseline, copy, err
}

// Called from g1PrepareOperation after its short current-project read, and
// before the existing preparation for reassess/apply/etc. All source-policy IO
// remains outside the final write transaction. This key only records ancestry.
func (s *Service) prepareInverseOperationProvenance(ctx context.Context, actor Actor, op, target, projectID string, body any) (context.Context, error) {
	var sourceID string
	switch input := body.(type) {
	case reverseChangeSetInput:
		if op == string(operationReverseChangeSet) {
			sourceID = input.SourceChangeSetID
		}
	case CreateChangeSetInput:
		sourceID = input.ReversesChangeSetID
	}
	if sourceID == "" && op != string(operationReassessChangeSet) {
		return ctx, nil
	}
	var source ChangeSet
	var baseline ContextRevisionRecord
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if sourceID == "" {
			store, ok := tx.(changeSetTransaction)
			if !ok {
				return ErrInvalidState
			}
			stale, err := store.ChangeSet(projectID, target[strings.LastIndex(target, "/")+1:])
			if err != nil {
				return err
			}
			sourceID, err = inverseLineageSource(tx, projectID, stale, map[string]bool{})
			if err != nil {
				return err
			}
			if sourceID == "" {
				return nil
			}
		}
		source, err = inverseSourceAccess(tx, p, actor, sourceID)
		if err != nil {
			return err
		}
		if source.TemplateUpgrade != nil {
			baseline, _, err = inverseTemplateBaseline(tx, actor, projectID, source)
		}
		return err
	})
	if err != nil || sourceID == "" {
		return ctx, err
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return ctx, err
	}
	prepared, err := s.g1PrepareChangeProvenance(ctx, actor, source, map[string]bool{})
	if err != nil {
		return ctx, err
	}
	if source.TemplateUpgrade != nil {
		if err := s.AssertFrozenProjectProvenance(prepared, actor, projectID, baseline.Spec, baseline.SpecProvenance, false); err != nil {
			return ctx, err
		}
	}
	return context.WithValue(prepared, g1ReverseSourceKey{}, source.ID), nil
}

// Applying a template inverse restores the policy basis of the historical
// copy, rather than inheriting policies from the template being undone. The
// normal source-policy validator still checks targets against the new copy.
func inverseTemplatePolicyBasis(tx Transaction, actor Actor, p Project, change ChangeSet) (*ProjectTemplateCopy, error) {
	if change.ReversesChangeSetID == "" || change.TemplateUpgrade == nil {
		return p.TemplateCopy, nil
	}
	store, ok := tx.(changeSetTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	source, err := store.ChangeSet(p.ID, change.ReversesChangeSetID)
	if err != nil {
		return nil, err
	}
	_, historical, err := inverseTemplateBaseline(tx, actor, p.ID, source)
	if err != nil {
		return nil, err
	}
	definition, err := lingdoctemplate.WithHashes(lingdoctemplate.Clone(historical.Definition))
	if err != nil {
		return nil, ErrInvalidState
	}
	target := change.TemplateUpgrade.Definition
	if definition.ContentHash != target.ContentHash || definition.RulesetHash != target.RulesetHash ||
		hashImpactValue(lingdoctemplate.RuleSetReferenceFor(definition)) != hashImpactValue(lingdoctemplate.RuleSetReferenceFor(target)) ||
		hashImpactValue(append([]lingdoctemplate.RuleBindingOverride{}, definition.RuleBindingOverrides...)) != hashImpactValue(append([]lingdoctemplate.RuleBindingOverride{}, target.RuleBindingOverrides...)) {
		return nil, ErrVersionConflict
	}
	return &historical, nil
}
