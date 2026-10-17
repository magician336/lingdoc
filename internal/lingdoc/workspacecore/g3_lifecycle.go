package workspacecore

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"
)

type ContextRevisionRecord struct {
	ProjectID           string                    `json:"project_id"`
	Revision            int64                     `json:"revision"`
	ChangeSetID         string                    `json:"change_set_id"`
	SpecRevision        int64                     `json:"spec_revision"`
	TemplateCopyVersion int64                     `json:"template_copy_version"`
	Spec                map[string]string         `json:"spec"`
	SpecProvenance      ProjectProvenanceSnapshot `json:"spec_provenance"`
	CreatedAt           time.Time                 `json:"created_at"`
}

func (t gormTransaction) ContextRevisions(projectID string) ([]ContextRevisionRecord, error) {
	rows, err := t.g3List(projectID, "revision")
	if err != nil {
		return nil, err
	}
	result := []ContextRevisionRecord{}
	for _, row := range rows {
		var value ContextRevisionRecord
		if err := json.Unmarshal([]byte(row.BodyJSON), &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b ContextRevisionRecord) int {
		if a.Revision < b.Revision {
			return -1
		}
		if a.Revision > b.Revision {
			return 1
		}
		return 0
	})
	return result, nil
}
func (s *Service) ListContextRevisions(ctx context.Context, actor Actor, projectID string) ([]ContextRevisionRecord, error) {
	var result []ContextRevisionRecord
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		store, ok := tx.(interface {
			ContextRevisions(string) ([]ContextRevisionRecord, error)
		})
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = store.ContextRevisions(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	for i, revision := range result {
		if err := s.AssertFrozenProjectProvenance(ctx, actor, projectID, revision.Spec, revision.SpecProvenance, false); err != nil {
			if err != ErrSourceUnavailable {
				return nil, err
			}
			result[i].Spec = map[string]string{}
		}
	}
	return result, nil
}
func (s *Service) EditChangeDraft(ctx context.Context, actor Actor, projectID, changeID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
	if err := validateExtendedChangeInput(input); err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "editChangeDraft", projectID+"/"+changeID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" || p.CurrentContextRevision != input.ExpectedContextRevision {
			return nil, 0, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		old, err := store.ChangeSet(projectID, changeID)
		if err != nil {
			return nil, 0, err
		}
		if !slices.Contains([]string{"draft", "assessed"}, old.Status) || old.TemplateUpgrade != nil {
			return nil, 0, ErrInvalidState
		}
		if old.CreatedBy != actor.UserID && !projectOwner(p, actor.UserID) {
			return nil, 0, ErrNotFound
		}
		if old.BaseContextRevision != p.CurrentContextRevision || old.BaseSpecRevision != p.SpecRevision {
			return nil, 0, ErrVersionConflict
		}
		next := old
		next.Status = "draft"
		next.Reason = input.Reason
		next.Fields = []ChangeFieldDelta{}
		next.Impacts = []ChangeImpact{}
		keys := make([]string, 0, len(input.Fields))
		for key := range input.Fields {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			value := input.Fields[key]
			if p.Spec[key] != value.OldValue {
				return nil, 0, ErrVersionConflict
			}
			next.Fields = append(next.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
		}
		for _, id := range input.AffectedChapterIDs {
			chapter, err := tx.Chapter(projectID, id)
			if err != nil {
				return nil, 0, err
			}
			next.Impacts = append(next.Impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: input.Reason, Status: "open"})
		}
		pinned, err := s.PinSemanticDeltas(ctx, tx, actor, p, next.ID, input.SemanticDeltas)
		if err != nil {
			return nil, 0, err
		}
		next.SemanticDeltas = pinned
		if err := store.UpdateChangeSet(old, next); err != nil {
			return nil, 0, err
		}
		if err := g3Audit(tx, actor, projectID, "change_set.edit", changeID, map[string]any{"status": "draft"}); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}
func (s *Service) ReverseChangeSet(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	var p Project
	var change ChangeSet
	var baseline ContextRevisionRecord
	var templateCopy ProjectTemplateCopy
	var inverse []SemanticDelta
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		p, err = s.authorizeProject(ctx, tx, actor, projectID, "manage")
		if err != nil {
			return err
		}
		if !projectOwner(p, actor.UserID) {
			return ErrNotFound
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		change, err = store.ChangeSet(projectID, changeID)
		if err != nil {
			return err
		}
		if !slices.Contains([]string{"applied", "stabilized"}, change.Status) {
			return ErrInvalidState
		}
		inverse, err = BuildInverseSemanticDeltas(tx, p, change)
		if err != nil {
			return err
		}
		if change.TemplateUpgrade != nil {
			records, ok := tx.(interface {
				ContextRevisions(string) ([]ContextRevisionRecord, error)
			})
			if !ok {
				return ErrInvalidState
			}
			history, err := records.ContextRevisions(projectID)
			if err != nil {
				return err
			}
			found := false
			for _, revision := range history {
				if revision.Revision == change.BaseContextRevision {
					baseline = revision
					found = true
				}
			}
			if !found {
				return ErrInvalidState
			}
			reader, ok := tx.(interface {
				ProjectTemplateCopy(uint64, string, int64) (ProjectTemplateCopy, error)
			})
			if !ok {
				return ErrInvalidState
			}
			templateCopy, err = reader.ProjectTemplateCopy(actor.TenantID, projectID, change.TemplateUpgrade.BaseTemplateCopyVersion)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	prepared, err := s.g1PrepareChangeProvenance(ctx, actor, change, map[string]bool{})
	if err != nil {
		return nil, 0, false, err
	}
	ctx = context.WithValue(prepared, g1ReverseSourceKey{}, change.ID)
	if change.TemplateUpgrade != nil {
		if err := s.AssertFrozenProjectProvenance(ctx, actor, projectID, baseline.Spec, baseline.SpecProvenance, false); err != nil {
			return nil, 0, false, err
		}
	}
	fields := map[string]ChangeFieldInput{}
	for _, delta := range change.Fields {
		if p.Spec[delta.Key] != delta.NewValue {
			return nil, 0, false, ErrVersionConflict
		}
		fields[delta.Key] = ChangeFieldInput{OldValue: delta.NewValue, NewValue: delta.OldValue}
	}
	input := CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "逆向变更 " + changeID, Fields: fields, AffectedChapterIDs: []string{}}
	for _, impact := range change.Impacts {
		input.AffectedChapterIDs = append(input.AffectedChapterIDs, impact.ChapterID)
	}
	if change.TemplateUpgrade != nil {
		definition := templateCopy.Definition
		input.TemplateUpgrade = &TemplateUpgradeInput{ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion, Fields: definition.Fields, Sections: definition.Sections, Terms: definition.Terms, RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: maps.Clone(baseline.Spec)}
	}
	if len(inverse) > 0 {
		if input.TemplateUpgrade != nil {
			return nil, 0, false, ErrInvalidRequest
		}
		return s.CreateChangeDraft(ctx, actor, projectID, key, ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: input.Reason, Fields: fields, AffectedChapterIDs: input.AffectedChapterIDs, SemanticDeltas: inverse})
	}
	// Reverse is a new assessed proposal. Only the existing Owner apply action
	// can advance the revision, after all current-source/impact checks.
	return s.CreateChangeSet(ctx, actor, projectID, key, input)
}
func recordContextRevisionSnapshot(t gormTransaction, p Project, change ChangeSet) error {
	var old ContextRevisionRecord
	_, err := t.g3Read(p.ID, "revision", strconv.FormatInt(p.CurrentContextRevision, 10), &old)
	if err == nil {
		if old.SpecRevision != p.SpecRevision || old.TemplateCopyVersion != p.TemplateCopyVersion || !maps.Equal(old.Spec, p.Spec) {
			return ErrVersionConflict
		}
		return nil
	}
	if err != ErrNotFound {
		return err
	}
	snapshot, err := g1PersistSnapshot(t, p)
	if err != nil {
		return err
	}
	record := ContextRevisionRecord{ProjectID: p.ID, Revision: p.CurrentContextRevision, ChangeSetID: change.ID, SpecRevision: p.SpecRevision, TemplateCopyVersion: p.TemplateCopyVersion, Spec: maps.Clone(p.Spec), SpecProvenance: snapshot, CreatedAt: time.Now().UTC()}
	return t.g3Insert(p.ID, "revision", strconv.FormatInt(p.CurrentContextRevision, 10), 1, record)
}
