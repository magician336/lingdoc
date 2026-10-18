package workspacecore

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
)

type impactSourceReadTransaction interface {
	ProjectForImpact(string) (Project, error)
	SnapshotImpactSourceIDs(string, string) ([]string, error)
}

func (t gormTransaction) ProjectForImpact(projectID string) (Project, error) {
	var row projectRow
	if err := t.db.Where("id = ?", projectID).First(&row).Error; err != nil {
		return Project{}, storageError(err)
	}
	return projectView(t.db, row)
}
func (t gormTransaction) SnapshotImpactSourceIDs(projectID, snapshotID string) ([]string, error) {
	var row struct{ FrozenInputJSON string }
	if err := t.db.Table("lingdoc_release_snapshots").Select("frozen_input_json").Where("project_id = ? AND id = ?", projectID, snapshotID).Take(&row).Error; err != nil {
		return nil, storageError(err)
	}
	var input struct {
		Sources  []struct{ ID string }
		Chapters []struct {
			SourceIDs []string `json:"source_ids"`
		}
	}
	if err := json.Unmarshal([]byte(row.FrozenInputJSON), &input); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, source := range input.Sources {
		ids = append(ids, source.ID)
	}
	for _, chapter := range input.Chapters {
		ids = append(ids, chapter.SourceIDs...)
	}
	return sortedImpactSourceIDs(ids), nil
}
func G3SourceSensitiveOperation(op string) bool {
	return slices.Contains([]string{"createChangeSet", "createChangeDraft", "editChangeDraft", "reassessChangeSet", "applyChangeSet", "assessChangeSet", "stabilizeRevision", "reverseChangeSet", "acknowledgeImpactPreflight", "createDependencyEdge", "decideDependencyEdge", "actOnImpactTask"}, op)
}
func sortedImpactSourceIDs(ids []string) []string {
	result := []string{}
	for _, id := range ids {
		if id != "" {
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}
func impactSourceIDsForRefs(tx Transaction, p Project, refs []ObjectVersionRef) ([]string, error) {
	ids := []string{}
	for _, ref := range refs {
		if ref.Kind == "DeliveryPreflight" {
			store, ok := tx.(runtimeTransaction)
			if !ok {
				return nil, ErrInvalidState
			}
			var proof struct {
				SourceIDs []string `json:"source_ids"`
			}
			tenantID, err := impactProjectTenantID(tx, p)
			if err != nil {
				return nil, err
			}
			if _, err := store.G6Read(tenantID, p.ID, "preflight_check", ref.ID, &proof); err != nil {
				if err == ErrNotFound {
					reader, ok := tx.(impactSourceReadTransaction)
					if !ok {
						return nil, ErrInvalidState
					}
					fallback, err := reader.SnapshotImpactSourceIDs(p.ID, ref.ID)
					if err != nil {
						return nil, err
					}
					ids = append(ids, fallback...)
					continue
				}
				return nil, err
			}
			ids = append(ids, proof.SourceIDs...)
			continue
		}
		if ref.Kind == "ReleaseSnapshot" {
			reader, ok := tx.(impactSourceReadTransaction)
			if !ok {
				return nil, ErrInvalidState
			}
			snapshotIDs, err := reader.SnapshotImpactSourceIDs(p.ID, ref.ID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, snapshotIDs...)
			continue
		}
		if ref.Kind == "ResearchRun" || ref.Kind == "ComputationReceipt" {
			found, err := impactResultSourceIDs(tx, p, ref)
			if err != nil {
				return nil, err
			}
			ids = append(ids, found...)
			continue
		}
		if ref.Kind == "RouteNode" && ref.Version == "0" {
			continue
		}
		state, err := currentImpactObject(tx, p, ref)
		if err != nil && !errors.Is(err, ErrVersionConflict) && !errors.Is(err, ErrInvalidState) {
			return nil, err
		}
		ids = append(ids, state.SourceIDs...)
	}
	return ids, nil
}

// The caller already authorized this project in the same transaction. This
// projection performs no policy IO and no domain write. Its result is checked
// outside SQLite's connection, then re-read in the final write transaction.
func G3OperationSourceIDs(tx Transaction, op, target, projectID string, body any) ([]string, error) {
	if !G3SourceSensitiveOperation(op) {
		return nil, nil
	}
	reader, ok := tx.(impactSourceReadTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	p, err := reader.ProjectForImpact(projectID)
	if err != nil {
		return nil, err
	}
	if p.Status != "active" {
		return nil, nil
	}
	ids := []string{}
	addRefs := func(refs []ObjectVersionRef) error {
		found, err := impactSourceIDsForRefs(tx, p, refs)
		if err != nil {
			return err
		}
		ids = append(ids, found...)
		return nil
	}
	targetID := target[strings.LastIndex(target, "/")+1:]
	if op == "createDependencyEdge" {
		input, ok := body.(DependencyEdgeInput)
		if !ok {
			return nil, ErrInvalidRequest
		}
		if err := addRefs([]ObjectVersionRef{input.Source, input.Target}); err != nil {
			return nil, err
		}
		return sortedImpactSourceIDs(ids), nil
	}
	if op == "decideDependencyEdge" {
		store, ok := tx.(impactTransaction)
		if !ok {
			return nil, ErrInvalidState
		}
		edge, err := store.DependencyEdge(projectID, targetID)
		if err != nil {
			return nil, err
		}
		if err := addRefs([]ObjectVersionRef{edge.Source, edge.Target}); err != nil {
			return nil, err
		}
		return sortedImpactSourceIDs(ids), nil
	}
	if op == "actOnImpactTask" {
		input, ok := body.(ImpactTaskActionInput)
		if !ok {
			return nil, ErrInvalidRequest
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return nil, ErrInvalidState
		}
		task, err := store.ImpactTask(projectID, targetID)
		if err != nil {
			return nil, err
		}
		refs := append([]ObjectVersionRef{task.Target}, input.ClosureEvidence...)
		if err := addRefs(refs); err != nil {
			return nil, err
		}
		return sortedImpactSourceIDs(ids), nil
	}
	if op == "acknowledgeImpactPreflight" {
		input, ok := body.(map[string]string)
		if !ok || input["proof_id"] == "" && input["snapshot_id"] == "" {
			return nil, ErrInvalidRequest
		}
		proofID := input["proof_id"]
		kind := "DeliveryPreflight"
		if proofID == "" {
			proofID = input["snapshot_id"]
			kind = "ReleaseSnapshot"
		}
		if err := addRefs([]ObjectVersionRef{{Kind: kind, ID: proofID}}); err != nil {
			return nil, err
		}
		return sortedImpactSourceIDs(ids), nil
	}
	if op == "stabilizeRevision" {
		store, ok := tx.(impactTransaction)
		if !ok {
			return nil, ErrInvalidState
		}
		tasks, err := store.ImpactTasks(projectID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.ChangeSetID != targetID || task.TargetRevision != p.CurrentContextRevision {
				continue
			}
			if task.Action != "BLOCK_RELEASE" {
				if err := addRefs([]ObjectVersionRef{task.Target}); err != nil {
					return nil, err
				}
			}
			if err := addRefs(task.ClosureEvidence); err != nil {
				return nil, err
			}
		}
		return sortedImpactSourceIDs(ids), nil
	}
	changes, ok := tx.(changeSetTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	var change ChangeSet
	switch op {
	case "createChangeSet":
		input, ok := body.(CreateChangeSetInput)
		if !ok {
			return nil, ErrInvalidRequest
		}
		change = ChangeSet{ProjectID: projectID, Fields: []ChangeFieldDelta{}, TemplateUpgrade: nil, Impacts: []ChangeImpact{}}
		for key, value := range input.Fields {
			change.Fields = append(change.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
		}
		chapterIDs := input.AffectedChapterIDs
		if input.TemplateUpgrade != nil {
			chapters, err := tx.Chapters(projectID)
			if err != nil {
				return nil, err
			}
			chapterIDs = []string{}
			for _, chapter := range chapters {
				chapterIDs = append(chapterIDs, chapter.ID)
			}
			change.TemplateUpgrade = &TemplateUpgrade{}
		}
		for _, id := range chapterIDs {
			chapter, err := tx.Chapter(projectID, id)
			if err != nil {
				return nil, err
			}
			change.Impacts = append(change.Impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID)})
		}
	case "createChangeDraft", "editChangeDraft":
		input, ok := body.(ExtendedChangeSetInput)
		if !ok {
			return nil, ErrInvalidRequest
		}
		change = ChangeSet{ProjectID: projectID, Fields: []ChangeFieldDelta{}, Impacts: []ChangeImpact{}, SemanticDeltas: append([]SemanticDelta{}, input.SemanticDeltas...)}
		// Draft metadata is pinned by the server only after current authorization.
		// Arbitrary client Before/hash/source_ids cannot become assessment inputs.
		for i := range change.SemanticDeltas {
			change.SemanticDeltas[i].Before = nil
			change.SemanticDeltas[i].BeforeHash = ""
			change.SemanticDeltas[i].ProvenanceHash = ""
			change.SemanticDeltas[i].SourceIDs = nil
		}
		for key, value := range input.Fields {
			change.Fields = append(change.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
		}
		for _, id := range input.AffectedChapterIDs {
			chapter, err := tx.Chapter(projectID, id)
			if err != nil {
				return nil, err
			}
			change.Impacts = append(change.Impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID)})
		}
	default:
		change, err = changes.ChangeSet(projectID, targetID)
		if err != nil {
			return nil, err
		}
	}
	semanticIDs, err := semanticSourceIDs(tx, p, change.SemanticDeltas)
	if err != nil {
		return nil, err
	}
	ids = append(ids, semanticIDs...)
	// Replays of applied proposals check original cited sources plus the actual
	// current objects. They do not require a second revision application.
	if op == "applyChangeSet" {
		// SATISFIED is intentionally assessment-free and terminal. Current
		// project/Owner authorization already ran before this decoder; reject
		// the invalid business action instead of reading a nonexistent plan.
		if change.Status == "satisfied" {
			return nil, ErrInvalidState
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return nil, ErrInvalidState
		}
		assessment, err := store.ImpactAssessment(projectID, change.ID)
		if err != nil {
			return nil, err
		}
		refs := []ObjectVersionRef{}
		for _, object := range assessment.Objects {
			ids = append(ids, object.SourceIDs...)
			refs = append(refs, object.Ref)
		}
		if err := addRefs(refs); err != nil {
			return nil, err
		}
		return sortedImpactSourceIDs(ids), nil
	}
	if op == "reassessChangeSet" {
		for i, delta := range change.SemanticDeltas {
			value, e := currentSemanticValue(tx, p, delta)
			if e != nil {
				return nil, e
			}
			version, e := strconv.ParseInt(value.Ref.Version, 10, 64)
			if e != nil {
				return nil, ErrInvalidState
			}
			change.SemanticDeltas[i].ExpectedVersion = version
			change.SemanticDeltas[i].BeforeHash = ""
		}
	}
	if op == "reassessChangeSet" || op == "reverseChangeSet" {
		for i := range change.Impacts {
			chapter, err := tx.Chapter(projectID, change.Impacts[i].ChapterID)
			if err != nil {
				return nil, err
			}
			change.Impacts[i].ChapterVersionID = cloneString(chapter.CurrentVersionID)
		}
	}
	assessment, err := buildImpactAssessment(tx, p, change)
	if err != nil {
		return nil, err
	}
	for _, object := range assessment.Objects {
		ids = append(ids, object.SourceIDs...)
	}
	return sortedImpactSourceIDs(ids), nil
}

func impactProjectTenantID(tx Transaction, p Project) (uint64, error) {
	reader, ok := tx.(interface{ ImpactProjectTenantID(string) (uint64, error) })
	if !ok {
		return 0, ErrInvalidState
	}
	return reader.ImpactProjectTenantID(p.ID)
}
func (t gormTransaction) ImpactProjectTenantID(projectID string) (uint64, error) {
	var row projectRow
	if err := t.db.Select("tenant_id").Where("id = ?", projectID).Take(&row).Error; err != nil {
		return 0, storageError(err)
	}
	return row.TenantID, nil
}
