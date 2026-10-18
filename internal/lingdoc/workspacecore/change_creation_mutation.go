package workspacecore

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
)

// These callbacks are shared by the existing creation services and the
// controlled inverse operation. They never open a Service transaction.
func (s *Service) createAssessedChangeMutation(ctx context.Context, actor Actor, projectID string, input CreateChangeSetInput) func(ChangeSetMutationTransaction, Project) (operationResult[ChangeSet], error) {
	return func(tx ChangeSetMutationTransaction, p Project) (operationResult[ChangeSet], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[ChangeSet]{}, featureErr
		}

		if p.Status != "active" || p.DiscardedAt != nil {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if err := validateInverseCreation(ctx, tx, actor, p, input); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := validateChangeTemplateInput(p, input.Fields); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		var templateUpgrade *TemplateUpgrade
		impactChapterIDs := append([]string{}, input.AffectedChapterIDs...)
		var upgradeChapters []Chapter
		if requested := input.TemplateUpgrade; requested != nil {
			if p.ProjectVersion != requested.ExpectedProjectVersion || p.TemplateCopy == nil || p.TemplateCopyVersion != requested.ExpectedTemplateCopyVersion || p.TemplateCopy.Status != TemplateCopyBound {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			definition, err := editableTemplateDefinitionFromUpgrade(p.TemplateCopy.Definition, *requested)
			if plan, ok := ctx.Value(inverseTemplatePlanKey{}).(inverseTemplatePlan); ok {
				controlled, _ := ctx.Value(controlledInverseKey{}).(string)
				if controlled == "" || controlled != input.ReversesChangeSetID || plan.SourceID != controlled {
					return operationResult[ChangeSet]{}, ErrInvalidState
				}
				// Restore the complete server-owned historical definition,
				// including an unbound template. Never inherit a current pin.
				definition, err = lingdoctemplate.WithHashes(lingdoctemplate.Clone(plan.Definition))
			}
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			definition, err = s.bindUpgradeRuleSet(ctx, tx, actor, p.TemplateCopy.Definition, definition, *requested)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			if _, err := templateSpecAfterUpgrade(p.Spec, definition, requested.FieldValues); err != nil {
				return operationResult[ChangeSet]{}, err
			}
			chapters, err := tx.Chapters(projectID)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			upgradeChapters = chapters
			impactChapterIDs = make([]string, 0, len(chapters))
			for _, chapter := range chapters {
				impactChapterIDs = append(impactChapterIDs, chapter.ID)
			}
			slices.Sort(impactChapterIDs)
			if len(impactChapterIDs) == 0 {
				return operationResult[ChangeSet]{}, ErrInvalidState
			}
			preview := buildTemplateMigrationPreview(p, definition, p.ProjectVersion)
			impact := buildTemplateUpgradeImpact(p.TemplateCopy, definition, preview, impactChapterIDs, upgradeChapters)
			templateUpgrade = &TemplateUpgrade{BaseTemplateCopyVersion: p.TemplateCopyVersion, Definition: definition, FieldValues: maps.Clone(requested.FieldValues), Preview: preview, Impact: impact}
		}
		keys := make([]string, 0, len(input.Fields))
		for key := range input.Fields {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		fields := make([]ChangeFieldDelta, 0, len(keys))
		for _, key := range keys {
			delta := input.Fields[key]
			if p.Spec[key] != delta.OldValue {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			fields = append(fields, ChangeFieldDelta{Key: key, OldValue: delta.OldValue, NewValue: delta.NewValue})
		}
		impacts := make([]ChangeImpact, 0, len(impactChapterIDs))
		for _, chapterID := range impactChapterIDs {
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			reason := "研究条件变化，需要重新复核"
			if templateUpgrade != nil {
				reason = "模板或规则版本变化，需要重新复核"
			}
			impacts = append(impacts, ChangeImpact{ChapterID: chapter.ID, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: reason, Status: "open"})
		}
		change := ChangeSet{ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID, ReversesChangeSetID: input.ReversesChangeSetID, Reason: strings.TrimSpace(input.Reason), Status: "assessed", BaseContextRevision: p.CurrentContextRevision, BaseSpecRevision: p.SpecRevision, Fields: fields, Impacts: impacts, TemplateUpgrade: templateUpgrade, CreatedAt: time.Now().UTC()}
		if _, ok := tx.(impactTransaction); ok {
			if err := AssessChangeImpact(featureTx, p, change); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		if err := store.InsertChangeSet(change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			details := map[string]any{"base_context_revision": change.BaseContextRevision, "fields": sanitizedChangeFields(change.Fields), "impacts": change.Impacts, "reason": change.Reason}
			if change.ReversesChangeSetID != "" {
				details["reverses_change_set_id"] = change.ReversesChangeSetID
			}
			if change.TemplateUpgrade != nil {
				details["template_upgrade"] = templateUpgradeAuditDetails(change, nil)
			}
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.create", Target: change.ID, Details: details, Reason: change.Reason, CreatedAt: change.CreatedAt}); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusCreated}, nil
	}
}

func (s *Service) createChangeDraftMutation(ctx context.Context, actor Actor, projectID string, input ExtendedChangeSetInput) func(Transaction, Project) (operationResult[ChangeSet], error) {
	return func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		if p.Status != "active" || p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		sourceID, _ := ctx.Value(g1ReverseSourceKey{}).(string)
		if sourceID != "" {
			if _, err := validateInverseLineage(tx, p, actor, sourceID, ""); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		if err := validateChangeTemplateInput(p, input.Fields); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		change := ChangeSet{ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID, ReversesChangeSetID: sourceID, Reason: input.Reason, Status: "draft", BaseContextRevision: p.CurrentContextRevision, BaseSpecRevision: p.SpecRevision, Fields: []ChangeFieldDelta{}, Impacts: []ChangeImpact{}, CreatedAt: time.Now().UTC()}
		keys := make([]string, 0, len(input.Fields))
		for key := range input.Fields {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			field := input.Fields[key]
			if p.Spec[key] != field.OldValue {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			change.Fields = append(change.Fields, ChangeFieldDelta{Key: key, OldValue: field.OldValue, NewValue: field.NewValue})
		}
		for _, id := range input.AffectedChapterIDs {
			chapter, err := tx.Chapter(projectID, id)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			change.Impacts = append(change.Impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: input.Reason, Status: "open"})
		}
		pinned, err := s.PinSemanticDeltas(ctx, tx, actor, p, change.ID, input.SemanticDeltas)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		change.SemanticDeltas = pinned
		if err := store.InsertChangeSet(change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := storeAgentChangeDraftFields(ctx, tx, p, change, input); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := g3Audit(tx, actor, projectID, "change_set.draft", change.ID, map[string]any{"reverses_change_set_id": sourceID}); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusCreated}, nil
	}
}
