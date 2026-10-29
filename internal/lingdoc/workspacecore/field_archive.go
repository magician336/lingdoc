package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	operationCreateFieldArchive  operationName       = "createTemplateFieldArchive"
	operationRemoveArchivedField operationName       = "removeArchivedTemplateField"
	fieldArchiveKind                                 = "template_field_archive"
	operationOutputFieldArchive  operationOutputKind = "field_archive"
)

type CreateFieldArchiveInput struct {
	FieldID                 string                      `json:"field_id"`
	ExpectedSpecRevision    int64                       `json:"expected_spec_revision"`
	TemplateCopy            TemplateCopyDefinitionInput `json:"template_copy"`
	ExpectedContextRevision int64                       `json:"expected_context_revision,omitempty"`
	TemplateUpgrade         *TemplateUpgradeInput       `json:"template_upgrade,omitempty"`
}

type RemoveArchivedFieldInput struct {
	Confirmed              bool                        `json:"confirmed"`
	ExpectedArchiveVersion int64                       `json:"expected_archive_version"`
	ExpectedSpecRevision   int64                       `json:"expected_spec_revision"`
	TemplateCopy           TemplateCopyDefinitionInput `json:"template_copy"`
}

type FieldArchiveReceipt struct {
	ID                  string `json:"id"`
	Status              string `json:"status"`
	ArchiveVersion      int64  `json:"archive_version"`
	ProjectVersion      int64  `json:"project_version"`
	SpecRevision        int64  `json:"spec_revision"`
	TemplateCopyVersion int64  `json:"template_copy_version"`
}

type FieldArchiveResult struct {
	Archive  FieldArchiveReceipt
	Replayed bool
}

type FieldArchiveView struct {
	ID                  string     `json:"id"`
	ProjectID           string     `json:"project_id"`
	FieldID             string     `json:"field_id"`
	Status              string     `json:"status"`
	ArchiveVersion      int64      `json:"archive_version"`
	ValuePresent        bool       `json:"value_present"`
	Value               string     `json:"value"`
	MetadataPresent     bool       `json:"metadata_present"`
	Metadata            *SpecField `json:"metadata,omitempty"`
	ValueRedacted       bool       `json:"value_redacted"`
	CreatedBy           string     `json:"created_by"`
	CreatedAt           time.Time  `json:"created_at"`
	ProjectVersion      int64      `json:"project_version"`
	SpecRevision        int64      `json:"spec_revision"`
	TemplateCopyVersion int64      `json:"template_copy_version"`
	ContextRevision     int64      `json:"context_revision,omitempty"`
	TargetContentHash   string     `json:"target_content_hash"`
	RemovedAt           *time.Time `json:"removed_at,omitempty"`
}

// The record is server-owned. In particular, neither value nor provenance can
// be supplied by the client, and targetDefinitionHash is recomputed from the
// validated candidate at both steps.
type fieldArchiveRecord struct {
	FieldArchiveView
	TargetDefinitionHash string `json:"target_definition_hash"`
}

func fieldArchiveCandidate(p Project, fieldID string, specRevision int64, input TemplateCopyDefinitionInput) (string, string, error) {
	if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyDraft {
		return "", "", ErrInvalidState
	}
	if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion || p.SpecRevision != specRevision {
		return "", "", ErrVersionConflict
	}
	if fieldID == "" || input.Fields == nil || input.Sections == nil || input.Terms == nil || input.Rules == nil {
		return "", "", ErrInvalidRequest
	}
	if err := requireUnmodifiedLegacyRuleProjection(p.TemplateCopy, input.Rules); err != nil {
		return "", "", err
	}
	definition, err := editableTemplateDefinition(p.TemplateCopy.Definition, input)
	if err != nil {
		return "", "", err
	}
	if _, exists := templateFieldDefinitions(definition)[fieldID]; exists {
		return "", "", fmt.Errorf("%w: target template still contains the field", ErrInvalidRequest)
	}
	return hashImpactValue(definition), definition.ContentHash, nil
}

func activeArchivePlanHash(definition Template, fieldValues map[string]string) string {
	fieldValues = maps.Clone(fieldValues)
	if fieldValues == nil {
		fieldValues = map[string]string{}
	}
	return hashImpactValue(struct {
		Definition  Template
		FieldValues map[string]string
	}{definition, fieldValues})
}

func (s *Service) activeFieldArchiveCandidate(ctx context.Context, tx Transaction, actor Actor, p Project, input CreateFieldArchiveInput) (string, string, error) {
	upgrade := input.TemplateUpgrade
	if p.Status != "active" || p.DiscardedAt != nil || p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound || upgrade == nil {
		return "", "", ErrInvalidState
	}
	if p.ProjectVersion != upgrade.ExpectedProjectVersion || p.TemplateCopyVersion != upgrade.ExpectedTemplateCopyVersion ||
		p.SpecRevision != input.ExpectedSpecRevision || p.CurrentContextRevision != input.ExpectedContextRevision {
		return "", "", ErrVersionConflict
	}
	if len(upgrade.ArchivedRemovals) != 0 || input.TemplateCopy.ExpectedProjectVersion != 0 || input.TemplateCopy.Fields != nil {
		return "", "", ErrInvalidRequest
	}
	if err := validateChangeSetInput(CreateChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Reason: "archive candidate", TemplateUpgrade: upgrade}); err != nil {
		return "", "", err
	}
	if err := requireUnmodifiedLegacyRuleProjection(p.TemplateCopy, upgrade.Rules); err != nil {
		return "", "", err
	}
	definition, err := editableTemplateDefinitionFromUpgrade(p.TemplateCopy.Definition, *upgrade)
	if err != nil {
		return "", "", err
	}
	definition, err = s.bindUpgradeRuleSet(ctx, tx, actor, p.TemplateCopy.Definition, definition, *upgrade)
	if err != nil {
		return "", "", err
	}
	if _, exists := templateFieldDefinitions(definition)[input.FieldID]; exists {
		return "", "", fmt.Errorf("%w: target template still contains the field", ErrInvalidRequest)
	}
	if _, exists := upgrade.FieldValues[input.FieldID]; exists {
		return "", "", ErrInvalidRequest
	}
	return activeArchivePlanHash(definition, upgrade.FieldValues), definition.ContentHash, nil
}

// Archive/remove need a currently readable field, not merely a previously
// stored provenance record. The prepared proof is rechecked by runOperation
// in the write transaction before this function examines the target pin.
func archiveFieldReadable(ctx context.Context, field SpecField, exists bool, value string, present bool) error {
	if !exists || field.Provenance == nil && (field.Origin == "" || field.Origin == "human") {
		return nil
	}
	if !present {
		value = field.Value
	} else if value != field.Value {
		return ErrSourceUnavailable
	}
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok {
		return ErrSourceUnavailable
	}
	pin, found := proof.Requests[provenanceRequestKey(field.Provenance, value)]
	if !found || !proof.States[pin.ID] {
		return ErrSourceUnavailable
	}
	return nil
}

func (s *Service) CreateFieldArchive(ctx context.Context, actor Actor, projectID, key string, input CreateFieldArchiveInput) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(input.FieldID) == "" || input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	prepared, err := s.prepareProvenanceOperation(ctx, actor, string(operationCreateFieldArchive), projectID, projectID, input)
	if err != nil {
		return nil, 0, false, err
	}
	operation := operationInput(operationCreateFieldArchive, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, ""))
	operation.Output = operationOutputFieldArchive
	return runOperation(s, prepared, actor, operation, mutationPort[Transaction],
		func(tx Transaction, p Project) (operationResult[FieldArchiveReceipt], error) {
			var targetHash, contentHash string
			var err error
			if input.TemplateUpgrade != nil {
				targetHash, contentHash, err = s.activeFieldArchiveCandidate(prepared, tx, actor, p, input)
			} else {
				targetHash, contentHash, err = fieldArchiveCandidate(p, input.FieldID, input.ExpectedSpecRevision, input.TemplateCopy)
			}
			if err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			locker, ok := tx.(fieldArchiveProjectLocker)
			if !ok {
				return operationResult[FieldArchiveReceipt]{}, ErrInvalidState
			}
			locked, err := locker.LockFieldArchiveProject(actor.TenantID, p)
			if err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			value, valuePresent := locked.Spec[input.FieldID]
			metadata, metadataPresent := locked.Metadata[input.FieldID]
			if !valuePresent && !metadataPresent {
				return operationResult[FieldArchiveReceipt]{}, ErrNotFound
			}
			if err := archiveFieldReadable(prepared, metadata, metadataPresent, value, valuePresent); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			store, ok := tx.(projectRecordTransaction)
			if !ok {
				return operationResult[FieldArchiveReceipt]{}, ErrInvalidState
			}
			id, now := uuid.NewString(), time.Now().UTC()
			record := fieldArchiveRecord{FieldArchiveView: FieldArchiveView{ID: id, ProjectID: p.ID, FieldID: input.FieldID,
				Status: "recorded", ArchiveVersion: 1, ValuePresent: valuePresent, Value: value,
				MetadataPresent: metadataPresent, CreatedBy: actor.UserID, CreatedAt: now,
				ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, TemplateCopyVersion: p.TemplateCopyVersion,
				ContextRevision: p.CurrentContextRevision, TargetContentHash: contentHash}, TargetDefinitionHash: targetHash}
			if metadataPresent {
				record.Metadata = &metadata
			}
			if err := store.PutProjectRecord(p.ID, fieldArchiveKind, id, 0, record); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			if err := recordAuditEvent(tx, actor, p.ID, "project_template.archive_field", id, map[string]any{
				"field_id": input.FieldID, "project_version": p.ProjectVersion, "spec_revision": p.SpecRevision,
				"template_copy_version": p.TemplateCopyVersion, "target_content_hash": contentHash,
			}); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			return operationResult[FieldArchiveReceipt]{Value: fieldArchiveReceipt(record, p), Output: operationOutputFieldArchive, Status: operationStatusCreated}, nil
		})
}

func fieldArchiveReceipt(record fieldArchiveRecord, p Project) FieldArchiveReceipt {
	return FieldArchiveReceipt{ID: record.ID, Status: record.Status, ArchiveVersion: record.ArchiveVersion,
		ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, TemplateCopyVersion: p.TemplateCopyVersion}
}

func (s *Service) CreateFieldArchiveResult(ctx context.Context, actor Actor, projectID, key string, input CreateFieldArchiveInput) (FieldArchiveResult, error) {
	archive, replayed, err := adaptMutationResult[FieldArchiveReceipt](func() (json.RawMessage, int, bool, error) {
		return s.CreateFieldArchive(ctx, actor, projectID, key, input)
	}, "created field archive")
	return FieldArchiveResult{Archive: archive, Replayed: replayed}, err
}

func (s *Service) RemoveArchivedField(ctx context.Context, actor Actor, projectID, archiveID, key string, input RemoveArchivedFieldInput) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(archiveID) == "" || !input.Confirmed || input.ExpectedArchiveVersion < 1 || input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	target := projectID + "/" + archiveID
	prepared, err := s.prepareProvenanceOperation(ctx, actor, string(operationRemoveArchivedField), target, projectID, input)
	if err != nil {
		return nil, 0, false, err
	}
	operation := operationInput(operationRemoveArchivedField, target, key, input, authorizationRequest(actor, projectID, AuthorizationManage, ""))
	operation.Output = operationOutputFieldArchive
	return runOperation(s, prepared, actor, operation, mutationPort[Transaction],
		func(tx Transaction, p Project) (operationResult[FieldArchiveReceipt], error) {
			store, ok := tx.(projectRecordTransaction)
			if !ok {
				return operationResult[FieldArchiveReceipt]{}, ErrInvalidState
			}
			var record fieldArchiveRecord
			version, err := store.GetProjectRecord(p.ID, fieldArchiveKind, archiveID, &record)
			if err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			if version != input.ExpectedArchiveVersion || record.ArchiveVersion != version || record.Status != "recorded" ||
				record.ID != archiveID || record.ProjectID != p.ID || record.ProjectVersion != p.ProjectVersion ||
				record.SpecRevision != p.SpecRevision || record.TemplateCopyVersion != p.TemplateCopyVersion {
				return operationResult[FieldArchiveReceipt]{}, ErrVersionConflict
			}
			targetHash, _, err := fieldArchiveCandidate(p, record.FieldID, input.ExpectedSpecRevision, input.TemplateCopy)
			if err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			if targetHash != record.TargetDefinitionHash {
				return operationResult[FieldArchiveReceipt]{}, ErrVersionConflict
			}
			locker, ok := tx.(fieldArchiveProjectLocker)
			if !ok {
				return operationResult[FieldArchiveReceipt]{}, ErrInvalidState
			}
			locked, err := locker.LockFieldArchiveProject(actor.TenantID, p)
			if err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			value, present := locked.Spec[record.FieldID]
			metadata, metadataPresent := locked.Metadata[record.FieldID]
			if present != record.ValuePresent || value != record.Value || metadataPresent != record.MetadataPresent ||
				metadataPresent && (record.Metadata == nil || !reflect.DeepEqual(metadata, *record.Metadata)) {
				return operationResult[FieldArchiveReceipt]{}, ErrVersionConflict
			}
			if err := archiveFieldReadable(prepared, metadata, metadataPresent, value, present); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			next := p
			next.Spec, next.SpecFields = maps.Clone(locked.Spec), maps.Clone(locked.Metadata)
			delete(next.Spec, record.FieldID)
			delete(next.SpecFields, record.FieldID)
			next.ProjectVersion++
			next.SpecRevision++
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			now := time.Now().UTC()
			record.Status, record.ArchiveVersion, record.RemovedAt = "removed", version+1, &now
			if err := store.PutProjectRecord(p.ID, fieldArchiveKind, archiveID, version, record); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			if err := recordAuditEvent(tx, actor, p.ID, "project_template.remove_archived_field", archiveID, map[string]any{
				"field_id": record.FieldID, "archive_version": record.ArchiveVersion, "project_version": next.ProjectVersion,
				"spec_revision": next.SpecRevision, "template_copy_version": next.TemplateCopyVersion,
			}); err != nil {
				return operationResult[FieldArchiveReceipt]{}, err
			}
			return operationResult[FieldArchiveReceipt]{Value: fieldArchiveReceipt(record, next), Output: operationOutputFieldArchive, Status: operationStatusOK}, nil
		})
}

func (s *Service) RemoveArchivedFieldResult(ctx context.Context, actor Actor, projectID, archiveID, key string, input RemoveArchivedFieldInput) (FieldArchiveResult, error) {
	archive, replayed, err := adaptMutationResult[FieldArchiveReceipt](func() (json.RawMessage, int, bool, error) {
		return s.RemoveArchivedField(ctx, actor, projectID, archiveID, key, input)
	}, "removed archived field")
	return FieldArchiveResult{Archive: archive, Replayed: replayed}, err
}

func (s *Service) readFieldArchives(ctx context.Context, actor Actor, projectID, archiveID string) ([]fieldArchiveRecord, Project, error) {
	var project Project
	var records []fieldArchiveRecord
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		project, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		if archiveID != "" {
			var record fieldArchiveRecord
			version, err := store.GetProjectRecord(projectID, fieldArchiveKind, archiveID, &record)
			if err != nil {
				return err
			}
			if record.ID != archiveID || record.ProjectID != projectID || record.ArchiveVersion != version {
				return ErrInvalidState
			}
			records = append(records, record)
			return nil
		}
		rows, err := store.ListProjectRecords(projectID, fieldArchiveKind)
		if err != nil {
			return err
		}
		for _, row := range rows {
			var record fieldArchiveRecord
			if err := json.Unmarshal([]byte(row.PayloadJSON), &record); err != nil || record.ID != row.ID || record.ProjectID != projectID || record.ArchiveVersion != row.Version {
				return ErrInvalidState
			}
			records = append(records, record)
		}
		return nil
	})
	return records, project, err
}

func (s *Service) visibleFieldArchive(ctx context.Context, actor Actor, p Project, record fieldArchiveRecord) (FieldArchiveView, error) {
	view := record.FieldArchiveView
	synthetic := p
	synthetic.Spec = map[string]string{}
	synthetic.SpecFields = map[string]SpecField{}
	if view.ValuePresent {
		synthetic.Spec[view.FieldID] = view.Value
	}
	if view.MetadataPresent && view.Metadata != nil {
		synthetic.SpecFields[view.FieldID] = *view.Metadata
	}
	visible, err := s.projectProvenanceProjection(ctx, actor, synthetic)
	if err != nil {
		return FieldArchiveView{}, err
	}
	if view.MetadataPresent {
		field, exists := visible.SpecFields[view.FieldID]
		if !exists {
			view.Value, view.Metadata, view.ValueRedacted = "", nil, true
			return view, nil
		}
		view.Metadata = &field
		if field.Provenance != nil && !field.Provenance.Accessible {
			view.ValueRedacted = true
		}
	}
	if view.ValuePresent {
		view.Value = visible.Spec[view.FieldID]
		if _, exists := visible.Spec[view.FieldID]; !exists {
			view.ValueRedacted = true
		}
	}
	return view, nil
}

func (s *Service) ListFieldArchives(ctx context.Context, actor Actor, projectID string) ([]FieldArchiveView, error) {
	records, project, err := s.readFieldArchives(ctx, actor, projectID, "")
	if err != nil {
		return nil, err
	}
	views := make([]FieldArchiveView, 0, len(records))
	for _, record := range records {
		view, err := s.visibleFieldArchive(ctx, actor, project, record)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) GetFieldArchive(ctx context.Context, actor Actor, projectID, archiveID string) (FieldArchiveView, error) {
	if strings.TrimSpace(archiveID) == "" {
		return FieldArchiveView{}, ErrInvalidRequest
	}
	records, project, err := s.readFieldArchives(ctx, actor, projectID, archiveID)
	if err != nil {
		return FieldArchiveView{}, err
	}
	return s.visibleFieldArchive(ctx, actor, project, records[0])
}
