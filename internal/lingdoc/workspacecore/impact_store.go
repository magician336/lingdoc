package workspacecore

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
	"time"
)

// Each record has an explicit identity and CAS version. History records and
// durable handoffs are append-only; mutable edge/task records retain audit.
type versionedRecordRow struct {
	ID, ProjectID, Kind, RecordKey string
	Version                        int64
	BodyJSON                       string
	CreatedAt, UpdatedAt           time.Time
}

func (versionedRecordRow) TableName() string { return "lingdoc_g3_records" }
func (t gormTransaction) readVersionedRecord(project, kind, key string, value any) (int64, error) {
	var row versionedRecordRow
	if err := t.db.Where("project_id = ? AND kind = ? AND record_key = ?", project, kind, key).First(&row).Error; err != nil {
		return 0, storageError(err)
	}
	return row.Version, json.Unmarshal([]byte(row.BodyJSON), value)
}
func (t gormTransaction) insertVersionedRecord(project, kind, key string, version int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return t.db.Create(&versionedRecordRow{ID: uuid.NewString(), ProjectID: project, Kind: kind, RecordKey: key, Version: version, BodyJSON: string(raw), CreatedAt: now, UpdatedAt: now}).Error
}
func (t gormTransaction) compareAndSwapVersionedRecord(project, kind, key string, version int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return affected(t.db.Model(&versionedRecordRow{}).Where("project_id = ? AND kind = ? AND record_key = ? AND version = ?", project, kind, key, version).Updates(map[string]any{"body_json": string(raw), "version": version + 1, "updated_at": time.Now().UTC()}))
}
func (t gormTransaction) listVersionedRecords(project, kind string) ([]versionedRecordRow, error) {
	var rows []versionedRecordRow
	err := t.db.Where("project_id = ? AND kind = ?", project, kind).Order("created_at, id").Find(&rows).Error
	return rows, err
}
func (t gormTransaction) PutDependencyEdge(edge DependencyEdge) error {
	return t.insertVersionedRecord(edge.ProjectID, "edge", edge.ID, edge.Version, edge)
}
func (t gormTransaction) DependencyEdge(project, id string) (DependencyEdge, error) {
	var edge DependencyEdge
	_, err := t.readVersionedRecord(project, "edge", id, &edge)
	return edge, err
}
func (t gormTransaction) DependencyEdges(project string) ([]DependencyEdge, error) {
	rows, err := t.listVersionedRecords(project, "edge")
	if err != nil {
		return nil, err
	}
	result := make([]DependencyEdge, 0, len(rows))
	for _, row := range rows {
		var edge DependencyEdge
		if err := json.Unmarshal([]byte(row.BodyJSON), &edge); err != nil {
			return nil, err
		}
		result = append(result, edge)
	}
	return result, nil
}
func (t gormTransaction) UpdateDependencyEdge(old, next DependencyEdge) error {
	return t.compareAndSwapVersionedRecord(old.ProjectID, "edge", old.ID, old.Version, next)
}
func (t gormTransaction) SaveImpactAssessment(value ImpactAssessment) error {
	var old ImpactAssessment
	version, err := t.readVersionedRecord(value.ProjectID, "assessment", value.ChangeSetID, &old)
	if err == ErrNotFound {
		return t.insertVersionedRecord(value.ProjectID, "assessment", value.ChangeSetID, 1, value)
	}
	if err != nil {
		return err
	}
	return t.compareAndSwapVersionedRecord(value.ProjectID, "assessment", value.ChangeSetID, version, value)
}
func (t gormTransaction) ImpactAssessment(project, id string) (ImpactAssessment, error) {
	var value ImpactAssessment
	_, err := t.readVersionedRecord(project, "assessment", id, &value)
	return value, err
}

// ImpactTaskIdentity is the logical identity of an impact task:
// change_set_id + target_ref + target_revision + action_type. Two impacts with
// the same identity are the same obligation, so a replayed, duplicated or
// out-of-order event must merge into the existing task instead of creating a
// second one. Different action types stay independent because the action is
// part of the identity.
func ImpactTaskIdentity(task ImpactTask) string {
	return fmt.Sprintf("%s/%s/%s/%d/%s", task.ChangeSetID, task.Target.Kind, task.Target.ID, task.TargetRevision, task.Action)
}

func impactTaskIdentity(task ImpactTask) string { return ImpactTaskIdentity(task) }

// impactTaskID derives a stable task ID from the logical identity. Deriving the
// ID (rather than minting a random UUID) is what makes the identity derivable
// across a service restart: a redelivered event recomputes the same ID and
// therefore addresses the same task row. No schema change is required because
// the persisted record is already keyed by the identity string.
func impactTaskID(projectID, identity string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(projectID+"|"+identity)).String()
}

// ImpactTaskID exposes the derived ID so the service can build a task that
// addresses an existing row before attempting an insert.
func ImpactTaskID(projectID string, changeSetID string, target ObjectVersionRef, targetRevision int64, action string) string {
	return impactTaskID(projectID, ImpactTaskIdentity(ImpactTask{ChangeSetID: changeSetID, Target: target, TargetRevision: targetRevision, Action: action}))
}

func (t gormTransaction) ImpactTaskByIdentity(project, identity string) (ImpactTask, error) {
	var task ImpactTask
	_, err := t.readVersionedRecord(project, "task", identity, &task)
	return task, err
}
func (t gormTransaction) InsertImpactTask(task ImpactTask) error {
	// Unique logical identity makes transaction replay/out-of-order redelivery safe.
	key := impactTaskIdentity(task)
	var old ImpactTask
	version, err := t.readVersionedRecord(task.ProjectID, "task", key, &old)
	if err == nil {
		// Idempotent merge: a redelivered event contributes only the causes it
		// adds. Nothing is duplicated and nothing is lost, and a redelivery
		// that adds no new cause performs no write at all, so it also produces
		// no version bump and no second audit trail entry.
		merged := false
		for _, cause := range task.Causes {
			found := false
			for _, prior := range old.Causes {
				if prior.EdgeID == cause.EdgeID && prior.Source == cause.Source && prior.Reason == cause.Reason {
					found = true
				}
			}
			if !found {
				old.Causes = append(old.Causes, cause)
				merged = true
			}
		}
		if !merged {
			return nil
		}
		old.Version = version + 1
		old.UpdatedAt = time.Now().UTC()
		return t.compareAndSwapVersionedRecord(task.ProjectID, "task", key, version, old)
	}
	if err != ErrNotFound {
		return err
	}
	return t.insertVersionedRecord(task.ProjectID, "task", key, task.Version, task)
}
func (t gormTransaction) ImpactTasks(project string) ([]ImpactTask, error) {
	rows, err := t.listVersionedRecords(project, "task")
	if err != nil {
		return nil, err
	}
	result := make([]ImpactTask, 0, len(rows))
	for _, row := range rows {
		var task ImpactTask
		if err := json.Unmarshal([]byte(row.BodyJSON), &task); err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, nil
}
func (t gormTransaction) ImpactTask(project, id string) (ImpactTask, error) {
	tasks, err := t.ImpactTasks(project)
	if err != nil {
		return ImpactTask{}, err
	}
	for _, task := range tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return ImpactTask{}, ErrNotFound
}
func (t gormTransaction) UpdateImpactTask(old, next ImpactTask) error {
	return t.compareAndSwapVersionedRecord(old.ProjectID, "task", impactTaskIdentity(old), old.Version, next)
}
func (t gormTransaction) InsertImpactHandoff(task ImpactTask) error {
	raw, err := json.Marshal(map[string]any{"task_id": task.ID, "action": task.Action, "target_revision": task.TargetRevision, "status": "pending"})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&versionedRecordRow{ID: uuid.NewString(), ProjectID: task.ProjectID, Kind: "handoff", RecordKey: impactTaskIdentity(task), Version: 1, BodyJSON: string(raw), CreatedAt: now, UpdatedAt: now}).Error
}
func (t gormTransaction) RecordContextRevision(p Project, change ChangeSet) error {
	return recordContextRevisionSnapshot(t, p, change)
}

// Persistent confirmed facts originate in actual domain relationships, never
// model inference. An equal version-bound fact is idempotent.
func StoreConfirmedDependency(tx Transaction, edge DependencyEdge) error {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	if !validObjectRef(edge.Source) || !validObjectRef(edge.Target) || !validImpactAction(edge.Action) || edge.ActorID == "" || edge.Reason == "" || edge.Origin == "ai" {
		return ErrInvalidRequest
	}
	edges, err := store.DependencyEdges(edge.ProjectID)
	if err != nil {
		return err
	}
	for _, old := range edges {
		if old.Source == edge.Source && old.Target == edge.Target && old.Relation == edge.Relation && old.Action == edge.Action {
			return nil
		}
	}
	if edge.ID == "" {
		edge.ID = uuid.NewString()
	}
	edge.Status = "confirmed"
	edge.Version = 1
	edge.CreatedAt = time.Now().UTC()
	edge.UpdatedAt = edge.CreatedAt
	return store.PutDependencyEdge(edge)
}
