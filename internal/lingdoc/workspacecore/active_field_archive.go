package workspacecore

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"
)

// Restore references are derived exclusively from an applied, server-owned
// ChangeSet. The archive remains immutable history after a restoration.
func activeArchiveRestoreReferences(source ChangeSet) ([]ArchivedFieldReference, error) {
	if source.TemplateUpgrade == nil || source.Status != ChangeSetStatusApplied {
		return nil, ErrInvalidState
	}
	refs := source.TemplateUpgrade.ArchivedRemovals
	if err := validateArchivedFieldReferences(refs, source.TemplateUpgrade.FieldValues); err != nil {
		return nil, err
	}
	result := make([]ArchivedFieldReference, 0, len(refs))
	for _, ref := range refs {
		ref.ExpectedArchiveVersion++
		result = append(result, ref)
	}
	return result, nil
}

func activeArchiveRestoreRecords(tx Transaction, source ChangeSet, refs []ArchivedFieldReference) ([]fieldArchiveRecord, error) {
	expected, err := activeArchiveRestoreReferences(source)
	if err != nil || !reflect.DeepEqual(expected, refs) {
		return nil, ErrInvalidState
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	records := make([]fieldArchiveRecord, 0, len(refs))
	for _, ref := range refs {
		var record fieldArchiveRecord
		version, err := store.GetProjectRecord(source.ProjectID, fieldArchiveKind, ref.ArchiveID, &record)
		if err != nil {
			return nil, err
		}
		if version != ref.ExpectedArchiveVersion || record.ArchiveVersion != version || record.Status != "removed" ||
			record.ID != ref.ArchiveID || record.ProjectID != source.ProjectID || record.FieldID != ref.FieldID ||
			record.SpecRevision != source.BaseSpecRevision || record.ContextRevision != source.BaseContextRevision ||
			record.TemplateCopyVersion != source.TemplateUpgrade.BaseTemplateCopyVersion ||
			record.TargetDefinitionHash != activeArchivePlanHash(source.TemplateUpgrade.Definition, source.TemplateUpgrade.FieldValues) ||
			record.MetadataPresent != (record.Metadata != nil) {
			return nil, ErrInvalidState
		}
		records = append(records, record)
	}
	return records, nil
}

func validateActiveArchiveRestores(ctx context.Context, tx Transaction, actor Actor, p Project, source ChangeSet,
	refs []ArchivedFieldReference) (lockedArchiveProject, []fieldArchiveRecord, error) {
	if source.ProjectID != p.ID || len(refs) == 0 {
		return lockedArchiveProject{}, nil, ErrInvalidState
	}
	records, err := activeArchiveRestoreRecords(tx, source, refs)
	if err != nil {
		return lockedArchiveProject{}, nil, err
	}
	locker, ok := tx.(fieldArchiveProjectLocker)
	if !ok {
		return lockedArchiveProject{}, nil, ErrInvalidState
	}
	locked, err := locker.LockFieldArchiveProject(actor.TenantID, p)
	if err != nil {
		return lockedArchiveProject{}, nil, err
	}
	for _, record := range records {
		_, hasValue := locked.Spec[record.FieldID]
		_, hasMetadata := locked.Metadata[record.FieldID]
		if hasValue || hasMetadata {
			return lockedArchiveProject{}, nil, ErrVersionConflict
		}
		if record.MetadataPresent {
			if err := archiveFieldReadable(ctx, *record.Metadata, true, record.Value, record.ValuePresent); err != nil {
				return lockedArchiveProject{}, nil, err
			}
		}
	}
	return locked, records, nil
}

func archiveRestoreSpec(spec map[string]string, records []fieldArchiveRecord) map[string]string {
	result := maps.Clone(spec)
	if result == nil {
		result = map[string]string{}
	}
	for _, record := range records {
		if record.ValuePresent {
			result[record.FieldID] = record.Value
		}
	}
	return result
}

// Append archived requests before any source IO. Values are never placed in the
// public inverse ChangeSet; they are read again under the application lock.
func archiveRestoreProvenanceRequests(tx Transaction, projectID, op, target string, body any) ([]provenanceRequest, error) {
	if op != string(operationReverseChangeSet) && op != string(operationApplyChangeSet) && op != string(operationReassessChangeSet) {
		return nil, nil
	}
	store, ok := tx.(changeSetTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	var source ChangeSet
	var refs []ArchivedFieldReference
	var err error
	if input, ok := body.(reverseChangeSetInput); ok && op == string(operationReverseChangeSet) {
		source, err = store.ChangeSet(projectID, input.SourceChangeSetID)
		if err == nil && source.TemplateUpgrade != nil && len(source.TemplateUpgrade.ArchivedRemovals) > 0 {
			refs, err = activeArchiveRestoreReferences(source)
		}
	} else if op == string(operationApplyChangeSet) || op == string(operationReassessChangeSet) {
		change, e := store.ChangeSet(projectID, target[strings.LastIndex(target, "/")+1:])
		if e != nil {
			return nil, e
		}
		if change.TemplateUpgrade == nil || len(change.TemplateUpgrade.ArchiveRestores) == 0 {
			return nil, nil
		}
		refs = change.TemplateUpgrade.ArchiveRestores
		source, err = store.ChangeSet(projectID, change.ReversesChangeSetID)
	}
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	records, err := activeArchiveRestoreRecords(tx, source, refs)
	if err != nil {
		return nil, err
	}
	requests := make([]provenanceRequest, 0, len(records))
	for _, record := range records {
		if !record.MetadataPresent {
			continue
		}
		value := record.Value
		if !record.ValuePresent {
			value = record.Metadata.Value
		}
		requests = append(requests, provenanceRequest{Key: record.FieldID, Value: value, Origin: record.Metadata.Origin,
			Creator: record.Metadata.ModifiedBy, Record: record.Metadata.Provenance})
	}
	return requests, nil
}

func validateArchivedFieldReferences(refs []ArchivedFieldReference, values map[string]string) error {
	if len(refs) > 100 {
		return ErrInvalidRequest
	}
	fields, archives := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.FieldID) == "" || len(ref.FieldID) > 100 || strings.TrimSpace(ref.ArchiveID) == "" ||
			len(ref.ArchiveID) > 128 || ref.ExpectedArchiveVersion < 1 || fields[ref.FieldID] || archives[ref.ArchiveID] {
			return ErrInvalidRequest
		}
		if _, exists := values[ref.FieldID]; exists {
			return fmt.Errorf("%w: a removed field cannot also have a migration value", ErrInvalidRequest)
		}
		fields[ref.FieldID], archives[ref.ArchiveID] = true, true
	}
	return nil
}

// Validate the previously viewed archive against the current raw row and the
// exact server-resolved template/mappings. The row lock is held through the
// caller's transaction; source IO has already completed outside it.
func validateActiveArchiveRemovals(ctx context.Context, tx Transaction, actor Actor, p Project, definition Template,
	values map[string]string, refs []ArchivedFieldReference) (lockedArchiveProject, []fieldArchiveRecord, error) {
	if err := validateArchivedFieldReferences(refs, values); err != nil {
		return lockedArchiveProject{}, nil, err
	}
	if len(refs) == 0 {
		return lockedArchiveProject{}, nil, nil
	}
	locker, ok := tx.(fieldArchiveProjectLocker)
	if !ok {
		return lockedArchiveProject{}, nil, ErrInvalidState
	}
	locked, err := locker.LockFieldArchiveProject(actor.TenantID, p)
	if err != nil {
		return lockedArchiveProject{}, nil, err
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return lockedArchiveProject{}, nil, ErrInvalidState
	}
	result := make([]fieldArchiveRecord, 0, len(refs))
	for _, ref := range refs {
		var record fieldArchiveRecord
		version, err := store.GetProjectRecord(p.ID, fieldArchiveKind, ref.ArchiveID, &record)
		if err != nil {
			return lockedArchiveProject{}, nil, err
		}
		if version != ref.ExpectedArchiveVersion || record.ArchiveVersion != version || record.Status != "recorded" ||
			record.ID != ref.ArchiveID || record.ProjectID != p.ID || record.FieldID != ref.FieldID ||
			record.SpecRevision != p.SpecRevision || record.ContextRevision != p.CurrentContextRevision ||
			record.TemplateCopyVersion != p.TemplateCopyVersion || record.ProjectVersion != p.ProjectVersion ||
			record.TargetDefinitionHash != activeArchivePlanHash(definition, values) {
			return lockedArchiveProject{}, nil, ErrVersionConflict
		}
		if _, exists := templateFieldDefinitions(definition)[ref.FieldID]; exists {
			return lockedArchiveProject{}, nil, ErrInvalidRequest
		}
		value, present := locked.Spec[ref.FieldID]
		metadata, metadataPresent := locked.Metadata[ref.FieldID]
		if present != record.ValuePresent || value != record.Value || metadataPresent != record.MetadataPresent ||
			metadataPresent && (record.Metadata == nil || !reflect.DeepEqual(metadata, *record.Metadata)) {
			return lockedArchiveProject{}, nil, ErrVersionConflict
		}
		if err := archiveFieldReadable(ctx, metadata, metadataPresent, value, present); err != nil {
			return lockedArchiveProject{}, nil, err
		}
		result = append(result, record)
	}
	return locked, result, nil
}

func markActiveArchivesRemoved(tx Transaction, actor Actor, p Project, next Project, records []fieldArchiveRecord) error {
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	for _, record := range records {
		previous := record.ArchiveVersion
		now := time.Now().UTC()
		record.Status, record.ArchiveVersion, record.RemovedAt = "removed", previous+1, &now
		if err := store.PutProjectRecord(p.ID, fieldArchiveKind, record.ID, previous, record); err != nil {
			return err
		}
		if err := recordAuditEvent(tx, actor, p.ID, "project_template.remove_archived_field", record.ID, map[string]any{
			"field_id": record.FieldID, "archive_version": record.ArchiveVersion, "project_version": next.ProjectVersion,
			"spec_revision": next.SpecRevision, "template_copy_version": next.TemplateCopyVersion,
		}); err != nil {
			return err
		}
	}
	return nil
}

func archiveRestoresField(refs []ArchivedFieldReference, id string) bool {
	for _, ref := range refs {
		if ref.FieldID == id {
			return true
		}
	}
	return false
}
func redactArchiveRestorePreview(preview *TemplateMigrationPreview, refs []ArchivedFieldReference) {
	for i := range preview.Fields {
		if archiveRestoresField(refs, preview.Fields[i].FieldID) {
			preview.Fields[i].Value = ""
		}
	}
}
