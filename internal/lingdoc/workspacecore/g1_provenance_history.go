package workspacecore

import (
	"context"
	"errors"
	"maps"
	"slices"
)

type g1ChangeProvenance struct {
	Snapshot           ProjectProvenanceSnapshot    `json:"snapshot"`
	Spec               map[string]string            `json:"spec"`
	InheritedChangeIDs []string                     `json:"inherited_change_ids,omitempty"`
	InheritedFields    map[string]SpecProvenanceRef `json:"inherited_fields,omitempty"`
	ProposedFields     map[string]SpecProvenanceRef `json:"proposed_fields,omitempty"`
	ProposedOrigins    map[string]string            `json:"proposed_origins,omitempty"`
}
type g1ReverseSourceKey struct{}

func g1PersistSnapshot(tx Transaction, p Project) (ProjectProvenanceSnapshot, error) {
	snapshot, err := g1StorageSnapshot(tx, p)
	if err != nil {
		return snapshot, err
	}
	store, ok := tx.(g5Transaction)
	if !ok {
		return snapshot, ErrInvalidState
	}
	var previous ProjectProvenanceSnapshot
	_, err = store.G5Get(p.ID, "g1_snapshot", snapshot.Digest, &previous)
	if err == nil {
		if hashImpactValue(previous) != hashImpactValue(snapshot) {
			return snapshot, ErrVersionConflict
		}
		return snapshot, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return snapshot, err
	}
	return snapshot, store.G5Put(p.ID, "g1_snapshot", snapshot.Digest, 0, snapshot)
}

func g1RecordChangeProvenance(t gormTransaction, change ChangeSet) error {
	// This sidecar binds every old-value/preview reference to the baseline which
	// actually existed when the proposal was written. It never changes on apply.
	var old g1ChangeProvenance
	if _, err := t.G5Get(change.ProjectID, "g1_change_provenance", change.ID, &old); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	var row projectRow
	if err := t.db.Where("id = ?", change.ProjectID).First(&row).Error; err != nil {
		return storageError(err)
	}
	p, err := projectView(t.db, row)
	if err != nil {
		return err
	}
	if change.BaseContextRevision != p.CurrentContextRevision || change.BaseSpecRevision != p.SpecRevision {
		return ErrVersionConflict
	}
	snapshot, err := g1PersistSnapshot(t, p)
	if err != nil {
		return err
	}
	history := g1ChangeProvenance{Snapshot: snapshot, Spec: maps.Clone(p.Spec)}
	// Reassessment creates a new ChangeSet. Preserve only unchanged proposed
	// new-value identities; a new baseline never converts AI text into human text.
	if change.ReassessedFromID != "" {
		var prior g1ChangeProvenance
		if _, err := t.G5Get(p.ID, "g1_change_provenance", change.ReassessedFromID, &prior); err != nil {
			return err
		}
		for _, field := range change.Fields {
			if ref, exists := prior.ProposedFields[field.Key]; exists && ref.ValueHash == hashImpactValue(field.NewValue) {
				if history.ProposedFields == nil {
					history.ProposedFields = map[string]SpecProvenanceRef{}
				}
				history.ProposedFields[field.Key] = ref
				if history.ProposedOrigins == nil {
					history.ProposedOrigins = map[string]string{}
				}
				history.ProposedOrigins[field.Key] = agentProposedOrigin(prior.ProposedOrigins[field.Key])
			}
		}
	}
	return t.G5Put(p.ID, "g1_change_provenance", change.ID, 0, history)
}

func (s *Service) AssertChangeProvenance(ctx context.Context, actor Actor, change ChangeSet) error {
	_, err := s.g1PrepareChangeProvenance(ctx, actor, change, map[string]bool{})
	return err
}
func (s *Service) g1PrepareChangeProvenance(ctx context.Context, actor Actor, change ChangeSet, seen map[string]bool) (context.Context, error) {
	if len(change.Fields) == 0 && change.TemplateUpgrade == nil {
		return ctx, nil
	}
	if seen[change.ID] {
		return ctx, ErrSourceUnavailable
	}
	seen[change.ID] = true
	var history g1ChangeProvenance
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, change.ProjectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrSourceUnavailable
		}
		_, err := store.G5Get(change.ProjectID, "g1_change_provenance", change.ID, &history)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ctx, ErrSourceUnavailable
		}
		return ctx, err
	}
	return s.g1PrepareChangeProvenanceHistory(ctx, actor, change, history, seen)
}

// Shared validation for both current ChangeSet history and an immutable operation receipt.
func (s *Service) g1PrepareChangeProvenanceHistory(ctx context.Context, actor Actor, change ChangeSet, history g1ChangeProvenance, seen map[string]bool) (context.Context, error) {
	if history.Snapshot.SpecRevision != change.BaseSpecRevision || history.Snapshot.ContextRevision != change.BaseContextRevision {
		return ctx, ErrSourceUnavailable
	}
	for _, field := range change.Fields {
		if value, found := history.Spec[field.Key]; found && value != field.OldValue {
			return ctx, ErrSourceUnavailable
		} else if !found && field.OldValue != "" {
			return ctx, ErrSourceUnavailable
		}
	}
	prepared, err := s.PrepareFrozenProjectProvenance(ctx, actor, change.ProjectID, history.Spec, history.Snapshot, false)
	if err != nil {
		return ctx, err
	}
	prepared, err = s.prepareProposedChangeFields(prepared, actor, change, history.ProposedFields)
	if err != nil {
		return ctx, err
	}
	proof := prepared.Value(g1ProvenanceProofKey{}).(*g1ProvenanceProof)
	for _, id := range history.InheritedChangeIDs {
		var previous ChangeSet
		err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			store, ok := tx.(changeSetTransaction)
			if !ok {
				return ErrInvalidState
			}
			var e error
			previous, e = store.ChangeSet(change.ProjectID, id)
			return e
		})
		if err != nil {
			return ctx, err
		}
		inherited, err := s.g1PrepareChangeProvenance(ctx, actor, previous, seen)
		if err != nil {
			return ctx, err
		}
		if other, ok := inherited.Value(g1ProvenanceProofKey{}).(*g1ProvenanceProof); ok {
			if g1ProjectHash(other.Project) != g1ProjectHash(proof.Project) {
				return ctx, ErrVersionConflict
			}
			for pinID, pin := range other.Pins {
				proof.Pins[pinID] = pin
				proof.States[pinID] = other.States[pinID]
				proof.NativeBefore[pinID] = other.NativeBefore[pinID]
			}
		}
	}
	return prepared, nil
}
func g1StoreReverseSources(ctx context.Context, tx Transaction, value any) error {
	original, ok := ctx.Value(g1ReverseSourceKey{}).(string)
	if !ok || original == "" {
		return nil
	}
	change, ok := value.(ChangeSet)
	if !ok {
		return ErrInvalidState
	}
	if original == change.ID {
		return ErrInvalidState
	}
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrInvalidState
	}
	var record g1ChangeProvenance
	version, err := store.G5Get(change.ProjectID, "g1_change_provenance", change.ID, &record)
	if err != nil {
		return err
	}
	record.InheritedChangeIDs = []string{original}
	var source g1ChangeProvenance
	if _, err := store.G5Get(change.ProjectID, "g1_change_provenance", original, &source); err != nil {
		return err
	}
	record.InheritedFields = map[string]SpecProvenanceRef{}
	values := map[string]string{}
	for _, field := range change.Fields {
		values[field.Key] = field.NewValue
	}
	if change.TemplateUpgrade != nil {
		for key, value := range change.TemplateUpgrade.FieldValues {
			values[key] = value
		}
	}
	for _, ref := range source.Snapshot.Fields {
		value, present := values[ref.FieldKey]
		if present && value == source.Spec[ref.FieldKey] && hashImpactValue(value) == ref.ValueHash && ref.PinID != "" {
			record.InheritedFields[ref.FieldKey] = ref
		}
	}
	return store.G5Put(change.ProjectID, "g1_change_provenance", change.ID, version, record)
}

func (s *Service) g1ChangeProjection(ctx context.Context, actor Actor, change ChangeSet) (ChangeSet, error) {
	err := s.AssertChangeProvenance(ctx, actor, change)
	if err == nil {
		return change, nil
	}
	if !errors.Is(err, ErrSourceUnavailable) {
		return ChangeSet{}, err
	}
	change.Fields = slices.Clone(change.Fields)
	for i := range change.Fields {
		change.Fields[i].OldValue = ""
		change.Fields[i].NewValue = ""
	}
	if change.TemplateUpgrade != nil {
		copy := *change.TemplateUpgrade
		copy.FieldValues = map[string]string{}
		copy.Preview.Fields = slices.Clone(copy.Preview.Fields)
		for i := range copy.Preview.Fields {
			copy.Preview.Fields[i].Value = ""
			copy.Preview.Fields[i].Before = ""
			copy.Preview.Fields[i].After = ""
		}
		change.TemplateUpgrade = &copy
	}
	return change, nil
}

func (s *Service) g1PreviewProjection(ctx context.Context, actor Actor, preview TemplateMigrationPreview) (TemplateMigrationPreview, error) {
	p, err := s.GetProject(ctx, actor, preview.ProjectID)
	if err != nil {
		return preview, err
	}
	if preview.ExpectedProjectVersion != p.ProjectVersion {
		return TemplateMigrationPreview{}, ErrVersionConflict
	}
	preview.Fields = slices.Clone(preview.Fields)
	for i, field := range preview.Fields {
		if provenance := p.SpecFields[field.FieldID].Provenance; provenance != nil && !provenance.Accessible {
			preview.Fields[i].Value = ""
			preview.Fields[i].Before = ""
			preview.Fields[i].After = ""
		}
	}
	return preview, nil
}
