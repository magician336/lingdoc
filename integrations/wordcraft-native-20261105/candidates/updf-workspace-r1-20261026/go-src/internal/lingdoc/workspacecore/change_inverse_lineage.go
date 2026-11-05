package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm/clause"
)

type inverseLineageTransaction interface {
	LockInverseSource(string, string) error
}

// The lock is owned by the same transaction as the lineage row and operation.
// SQLite has no SELECT FOR UPDATE: a no-op identity update takes its writer
// lock without changing any source ChangeSet value or lifecycle field.
func (t gormTransaction) LockInverseSource(projectID, sourceID string) error {
	if t.db.Name() == "sqlite" {
		return affected(t.db.Model(&changeSetRow{}).Where("project_id = ? AND id = ?", projectID, sourceID).UpdateColumn("id", sourceID))
	}
	var row changeSetRow
	return storageError(t.db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("project_id = ? AND id = ?", projectID, sourceID).Take(&row).Error)
}

func inverseLineageSource(tx Transaction, projectID string, change ChangeSet, seen map[string]bool) (string, error) {
	if change.ProjectID != projectID || seen[change.ID] {
		return "", ErrInvalidState
	}
	seen[change.ID] = true
	store, ok := tx.(g5Transaction)
	if !ok {
		return "", ErrInvalidState
	}
	var history g1ChangeProvenance
	_, err := store.G5Get(projectID, "g1_change_provenance", change.ID, &history)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if len(history.InheritedChangeIDs) > 1 {
		return "", ErrInvalidState
	}
	var historicalSource string
	if len(history.InheritedChangeIDs) == 1 {
		historicalSource = history.InheritedChangeIDs[0]
	}
	if change.ReversesChangeSetID != "" {
		if historicalSource != "" && historicalSource != change.ReversesChangeSetID {
			return "", ErrInvalidState
		}
		return change.ReversesChangeSetID, nil
	}
	if historicalSource != "" {
		return historicalSource, nil
	}
	if change.ReassessedFromID == "" {
		return "", nil
	}
	changes, ok := tx.(changeSetTransaction)
	if !ok {
		return "", ErrInvalidState
	}
	parent, err := changes.ChangeSet(projectID, change.ReassessedFromID)
	if err != nil {
		return "", err
	}
	return inverseLineageSource(tx, projectID, parent, seen)
}

func inverseSourceAccess(tx Transaction, p Project, actor Actor, sourceID string) (ChangeSet, error) {
	if p.Status != "active" || p.DiscardedAt != nil {
		return ChangeSet{}, ErrInvalidState
	}
	if !projectOwner(p, actor.UserID) {
		return ChangeSet{}, ErrNotFound
	}
	store, ok := tx.(changeSetTransaction)
	if !ok {
		return ChangeSet{}, ErrInvalidState
	}
	source, err := store.ChangeSet(p.ID, sourceID)
	if err != nil {
		return ChangeSet{}, err
	}
	if !slices.Contains(ChangeSetClosedStatuses, source.Status) {
		return ChangeSet{}, ErrInvalidState
	}
	return source, nil
}

// Call only on cache misses. A rejected proposal releases the source; a stale
// reassessment can supersede its own chain, but never a sibling/live inverse.
func validateInverseLineage(tx Transaction, p Project, actor Actor, sourceID, reassessedFromID string) (ChangeSet, error) {
	if _, ok := tx.(auditTransaction); !ok {
		return ChangeSet{}, ErrInvalidState
	}
	lock, ok := tx.(inverseLineageTransaction)
	if !ok {
		return ChangeSet{}, ErrInvalidState
	}
	if err := lock.LockInverseSource(p.ID, sourceID); err != nil {
		return ChangeSet{}, err
	}
	source, err := inverseSourceAccess(tx, p, actor, sourceID)
	if err != nil {
		return ChangeSet{}, err
	}
	store := tx.(changeSetTransaction)
	ancestors := map[string]bool{}
	for id := reassessedFromID; id != ""; {
		if ancestors[id] {
			return ChangeSet{}, ErrInvalidState
		}
		ancestor, err := store.ChangeSet(p.ID, id)
		if err != nil {
			return ChangeSet{}, err
		}
		ancestorSource, err := inverseLineageSource(tx, p.ID, ancestor, map[string]bool{})
		if err != nil {
			return ChangeSet{}, err
		}
		if ancestorSource != sourceID || ancestor.Status != ChangeSetStatusStale {
			return ChangeSet{}, ErrInvalidState
		}
		ancestors[id] = true
		id = ancestor.ReassessedFromID
	}
	changes, err := store.ChangeSets(p.ID)
	if err != nil {
		return ChangeSet{}, err
	}
	for _, change := range changes {
		lineage, err := inverseLineageSource(tx, p.ID, change, map[string]bool{})
		if err != nil {
			return ChangeSet{}, err
		}
		if lineage == sourceID && change.Status != ChangeSetStatusRejected && !ancestors[change.ID] {
			return ChangeSet{}, ErrInvalidState
		}
	}
	return source, nil
}

func validateInverseCreation(ctx context.Context, tx ChangeSetMutationTransaction, actor Actor, p Project, input CreateChangeSetInput) error {
	if input.ReversesChangeSetID == "" {
		return nil
	}
	full, err := extendedTransaction(tx)
	if err != nil {
		return err
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return err
	}
	source, err := validateInverseLineage(full, p, actor, input.ReversesChangeSetID, "")
	if err != nil {
		return err
	}
	controlled, _ := ctx.Value(controlledInverseKey{}).(string)
	if controlled == source.ID {
		return nil // The controlled operation constructs its plan in this tx.
	}
	// Preserve the author's field-only CreateChangeSet compatibility contract;
	// a caller-supplied lineage ID never grants arbitrary proposal authority.
	if source.TemplateUpgrade != nil || len(source.SemanticDeltas) != 0 || input.TemplateUpgrade != nil || len(source.Fields) == 0 || len(input.Fields) != len(source.Fields) {
		return ErrInvalidRequest
	}
	for _, delta := range source.Fields {
		field, ok := input.Fields[delta.Key]
		if !ok || field.OldValue != delta.NewValue || field.NewValue != delta.OldValue {
			return ErrInvalidRequest
		}
	}
	provided := map[string]bool{}
	for _, id := range input.AffectedChapterIDs {
		provided[id] = true
	}
	for _, impact := range source.Impacts {
		if !provided[impact.ChapterID] {
			return ErrInvalidRequest
		}
	}
	return nil
}

// Invoked by the existing source decoder before both replay decisions. This
// adds current Owner/source checks only to operations carrying inverse lineage.
func inverseOperationAccess(tx Transaction, request AuthorizationRequest, name, target string, body any) error {
	var sourceID string
	if input, ok := body.(CreateChangeSetInput); ok {
		sourceID = input.ReversesChangeSetID
	} else if slices.Contains([]string{"reassessChangeSet", "assessChangeSet", "editChangeDraft"}, name) {
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		change, err := store.ChangeSet(request.ProjectID, target[strings.LastIndex(target, "/")+1:])
		if err != nil {
			return err
		}
		sourceID, err = inverseLineageSource(tx, request.ProjectID, change, map[string]bool{})
		if err != nil {
			return err
		}
	}
	if sourceID == "" {
		return nil
	}
	p, err := tx.Project(request.Actor.TenantID, request.ProjectID)
	if err != nil {
		return err
	}
	_, err = inverseSourceAccess(tx, p, request.Actor, sourceID)
	return err
}

func requireInverseCaller(ctx context.Context, body any) error {
	if input, ok := body.(CreateChangeSetInput); ok && input.ReversesChangeSetID != "" {
		return RequireHumanDecision(ctx)
	}
	return nil
}

func reverseChangeSetSources(tx ChapterWorkspaceTransaction, op operationDescriptor[reverseChangeSetInput], previous OperationResult, found bool) ([]string, error) {
	full, err := extendedTransaction(tx)
	if err != nil {
		return nil, err
	}
	p, err := full.Project(op.Request.Actor.TenantID, op.Request.ProjectID)
	if err != nil {
		return nil, err
	}
	source, err := inverseSourceAccess(full, p, op.Request.Actor, op.Input.SourceChangeSetID)
	if err != nil {
		return nil, err
	}
	changes := []ChangeSet{source}
	if found {
		var saved ChangeSet
		if err := json.Unmarshal(previous.Body, &saved); err != nil {
			return nil, markReplayPayloadDecodeError(err)
		}
		if saved.ProjectID != p.ID || saved.ReversesChangeSetID != source.ID || saved.ID == "" {
			return nil, ErrInvalidState
		}
		changes = append(changes, saved)
	}
	ids := []string{}
	for _, change := range changes {
		semanticIDs, err := semanticSourceIDs(full, p, change.SemanticDeltas)
		if err != nil {
			return nil, err
		}
		ids = append(ids, semanticIDs...)
		for _, impact := range change.Impacts {
			current, err := full.Chapter(p.ID, impact.ChapterID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, current.SourceIDs...)
			if impact.ChapterVersionID != nil {
				frozen, err := full.ChapterVersion(p.ID, impact.ChapterID, *impact.ChapterVersionID)
				if err != nil {
					return nil, err
				}
				ids = append(ids, frozen.SourceIDs...)
			}
		}
	}
	return sortedImpactSourceIDs(ids), nil
}
