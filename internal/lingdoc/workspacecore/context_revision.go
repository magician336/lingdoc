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
	rows, err := t.listVersionedRecords(projectID, "revision")
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
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	// Prepare outside runOperation as well so the mutation closure captures
	// the exact prepared context needed by its native sidecar update.
	id := OperationIdentity{Actor: actor, Operation: string(operationEditChangeDraft), Target: projectID + "/" + changeID, Key: key}
	prepared, cached, err := s.g1PrepareCachedChangeOperation(ctx, actor, authorizationRequest(actor, projectID, AuthorizationWrite, ""), id, input)
	if err != nil {
		return nil, 0, isReplayPayloadDecodeError(err), err
	}
	if !cached {
		prepared, err = s.g1PrepareOperation(ctx, actor, string(operationEditChangeDraft), projectID+"/"+changeID, projectID, input)
		if err != nil {
			return nil, 0, false, err
		}
	}
	ctx = prepared
	return runOperation(s, ctx, actor, sourceOperationInput(operationEditChangeDraft, projectID+"/"+changeID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		if p.Status != "active" || p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		old, err := store.ChangeSet(projectID, changeID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if !slices.Contains(ChangeSetProposalStatuses, old.Status) || old.TemplateUpgrade != nil {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if old.CreatedBy != actor.UserID && !projectOwner(p, actor.UserID) {
			return operationResult[ChangeSet]{}, ErrNotFound
		}
		if old.BaseContextRevision != p.CurrentContextRevision || old.BaseSpecRevision != p.SpecRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		if err := validateChangeTemplateInput(p, input.Fields); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		next := old
		next.Status = ChangeSetStatusDraft
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
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			next.Fields = append(next.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
		}
		for _, id := range input.AffectedChapterIDs {
			chapter, err := tx.Chapter(projectID, id)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			next.Impacts = append(next.Impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: input.Reason, Status: "open"})
		}
		pinned, err := s.PinSemanticDeltas(ctx, tx, actor, p, next.ID, input.SemanticDeltas)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		next.SemanticDeltas = pinned
		if err := store.UpdateChangeSet(old, next); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := storeAgentChangeDraftEditFields(ctx, tx, p, old, next, input); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "change_set.edit", changeID, map[string]any{"status": ChangeSetStatusDraft}); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return operationResult[ChangeSet]{Value: next, Status: operationStatusOK}, nil
	})
}
func (s *Service) ReverseChangeSet(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error) {
	return s.reverseChangeSetOperation(ctx, actor, projectID, changeID, key)
}
func recordContextRevisionSnapshot(t gormTransaction, p Project, change ChangeSet) error {
	var old ContextRevisionRecord
	_, err := t.readVersionedRecord(p.ID, "revision", strconv.FormatInt(p.CurrentContextRevision, 10), &old)
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
	return t.insertVersionedRecord(p.ID, "revision", strconv.FormatInt(p.CurrentContextRevision, 10), 1, record)
}
