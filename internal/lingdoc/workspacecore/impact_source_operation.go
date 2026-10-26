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

// SourceSensitiveOperation reports whether the projection resolves anything for
// this operation. It is exactly the key set of the dispatch table.
func SourceSensitiveOperation(op string) bool {
	_, ok := impactSourceProjections[op]
	return ok
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
			if _, err := store.ReadRuntimeRecord(tenantID, p.ID, "preflight_check", ref.ID, &proof); err != nil {
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
		if ref.Kind == "ProjectAsset" {
			// 一份**资料**是这次操作的**对象**，不是它所引用的来源：资料自己的
			// 分块是它的正文。把它们算进操作级来源复核，等于让「改这份资料的权限」
			// 这一步依赖「这份资料本身」——授权与被授权互为前提（#545：F01-06b
			// 在真实后端上恒 409 stale_input）。
			//
			// 资料自身的来源一致性没有因此被放弃：坐标取不回原文的分块拿不到
			// available 结论，因而不会成为来源，也就不会进模型的上下文
			// （internal/lingdoc/workspace/generation.go）。
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

// impactSourceProjectionInput is everything a resolver needs. The shared
// prologue resolves the project and the target identifier once, so a resolver
// only decides WHICH objects its operation authorizes against.
type impactSourceProjectionInput struct {
	tx        Transaction
	project   Project
	projectID string
	targetID  string
	body      any
}

// impactSourceContribution is what one resolver hands back: the object
// references the shared epilogue turns into identifiers, plus identifiers the
// operation already resolved from the payload it is about to rebuild.
type impactSourceContribution struct {
	refs []ObjectVersionRef
	ids  []string
}

type impactSourceResolver func(impactSourceProjectionInput) (impactSourceContribution, error)

// impactSourceProjections is the dispatch table: one declaration per
// source-sensitive operation. Project resolution, the inactive-project
// short-circuit, reference resolution, sorting, de-duplication and the failure
// handling are written once in OperationSourceIDs and applied uniformly.
var impactSourceProjections = map[string]impactSourceResolver{
	"createChangeSet":            resolveChangeSetPayload,
	"createChangeDraft":          resolveChangeDraftPayload,
	"editChangeDraft":            resolveChangeDraftPayload,
	"reassessChangeSet":          resolveStoredChangeSet(true, true),
	"applyChangeSet":             resolveApplyChangeSet,
	"reverseChangeSet":           resolveStoredChangeSet(false, true),
	"assessChangeSet":            resolveStoredChangeSet(false, false),
	"stabilizeRevision":          resolveStabilizationTasks,
	"actOnImpactTask":            resolveImpactTaskAction,
	"createDependencyEdge":       resolveNewDependencyEdge,
	"decideDependencyEdge":       resolveDependencyEdgeDecision,
	"submitDependencyCandidates": resolveDependencyCandidateBatch,
	"decideDependencyCandidate":  resolveDependencyCandidateDecision,
	"acknowledgeImpactPreflight": resolveAcknowledgedPreflight,
}

// Shared port assertions. Every change-set resolver opens with the first and
// every impact-task/edge resolver with the second, so a transaction that lacks
// the port fails identically no matter which operation asked for it.
func changeSetPort(tx Transaction) (changeSetTransaction, error) {
	changes, ok := tx.(changeSetTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	return changes, nil
}

func impactPort(tx Transaction) (impactTransaction, error) {
	store, ok := tx.(impactTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	return store, nil
}

// chapterImpacts binds each affected chapter to the version it carries now.
func chapterImpacts(tx Transaction, projectID string, chapterIDs []string) ([]ChangeImpact, error) {
	impacts := []ChangeImpact{}
	for _, id := range chapterIDs {
		chapter, err := tx.Chapter(projectID, id)
		if err != nil {
			return nil, err
		}
		impacts = append(impacts, ChangeImpact{ChapterID: id, ChapterVersionID: cloneString(chapter.CurrentVersionID)})
	}
	return impacts, nil
}

// assessedSourceIDs reads the objects a rebuilt change set currently touches.
func assessedSourceIDs(tx Transaction, p Project, change ChangeSet) ([]string, error) {
	assessment, err := buildImpactAssessment(tx, p, change)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, object := range assessment.Objects {
		ids = append(ids, object.SourceIDs...)
	}
	return ids, nil
}

// projectionChangeSet copies the two slices a resolver may rebind to current
// truth, so resolving an operation never writes through the entity the store
// returned.
func projectionChangeSet(change ChangeSet) ChangeSet {
	change.SemanticDeltas = append([]SemanticDelta{}, change.SemanticDeltas...)
	change.Impacts = append([]ChangeImpact{}, change.Impacts...)
	return change
}

// resolveChangeSetPayload authorizes a change set that does not exist yet: the
// one the request is about to create.
func resolveChangeSetPayload(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	if _, err := changeSetPort(in.tx); err != nil {
		return out, err
	}
	input, ok := in.body.(CreateChangeSetInput)
	if !ok {
		return out, ErrInvalidRequest
	}
	change := ChangeSet{ProjectID: in.projectID, Fields: []ChangeFieldDelta{}, TemplateUpgrade: nil, Impacts: []ChangeImpact{}}
	for key, value := range input.Fields {
		change.Fields = append(change.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
	}
	chapterIDs := input.AffectedChapterIDs
	if input.TemplateUpgrade != nil {
		chapters, err := in.tx.Chapters(in.projectID)
		if err != nil {
			return out, err
		}
		chapterIDs = []string{}
		for _, chapter := range chapters {
			chapterIDs = append(chapterIDs, chapter.ID)
		}
		change.TemplateUpgrade = &TemplateUpgrade{}
	}
	change.Impacts, err = chapterImpacts(in.tx, in.projectID, chapterIDs)
	if err != nil {
		return out, err
	}
	return assessRebuiltChange(in, change)
}

// resolveChangeDraftPayload authorizes a draft the request is about to write.
// Draft metadata is pinned by the server only after current authorization, so
// arbitrary client Before/hash/source_ids never become assessment inputs.
func resolveChangeDraftPayload(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	if _, err := changeSetPort(in.tx); err != nil {
		return out, err
	}
	input, ok := in.body.(ExtendedChangeSetInput)
	if !ok {
		return out, ErrInvalidRequest
	}
	change := ChangeSet{ProjectID: in.projectID, Fields: []ChangeFieldDelta{}, Impacts: []ChangeImpact{},
		SemanticDeltas: append([]SemanticDelta{}, input.SemanticDeltas...)}
	for i := range change.SemanticDeltas {
		change.SemanticDeltas[i].Before = nil
		change.SemanticDeltas[i].BeforeHash = ""
		change.SemanticDeltas[i].ProvenanceHash = ""
		change.SemanticDeltas[i].SourceIDs = nil
	}
	for key, value := range input.Fields {
		change.Fields = append(change.Fields, ChangeFieldDelta{Key: key, OldValue: value.OldValue, NewValue: value.NewValue})
	}
	change.Impacts, err = chapterImpacts(in.tx, in.projectID, input.AffectedChapterIDs)
	if err != nil {
		return out, err
	}
	return assessRebuiltChange(in, change)
}

// assessRebuiltChange closes the shared tail of both payload operations: the
// delta citations first, then the objects the rebuilt change set touches.
func assessRebuiltChange(in impactSourceProjectionInput, change ChangeSet) (impactSourceContribution, error) {
	ids, err := semanticSourceIDs(in.tx, in.project, change.SemanticDeltas)
	if err != nil {
		return impactSourceContribution{}, err
	}
	assessed, err := assessedSourceIDs(in.tx, in.project, change)
	if err != nil {
		return impactSourceContribution{}, err
	}
	return impactSourceContribution{ids: append(ids, assessed...)}, nil
}

// resolveStoredChangeSet authorizes an operation that acts on a change set that
// already exists. refreshSemanticVersions pins the deltas to the values they
// carry today; refreshChapterVersions rebinds the impacts to current chapter
// versions. Both are declared per operation in the table, not branched on.
func resolveStoredChangeSet(refreshSemanticVersions, refreshChapterVersions bool) impactSourceResolver {
	return func(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
		changes, err := changeSetPort(in.tx)
		if err != nil {
			return out, err
		}
		stored, err := changes.ChangeSet(in.projectID, in.targetID)
		if err != nil {
			return out, err
		}
		change := projectionChangeSet(stored)
		out.ids, err = semanticSourceIDs(in.tx, in.project, change.SemanticDeltas)
		if err != nil {
			return out, err
		}
		if refreshSemanticVersions {
			for i, delta := range change.SemanticDeltas {
				value, e := currentSemanticValue(in.tx, in.project, delta)
				if e != nil {
					return out, e
				}
				version, e := strconv.ParseInt(value.Ref.Version, 10, 64)
				if e != nil {
					return out, ErrInvalidState
				}
				change.SemanticDeltas[i].ExpectedVersion = version
				change.SemanticDeltas[i].BeforeHash = ""
			}
		}
		if refreshChapterVersions {
			ids := make([]string, 0, len(change.Impacts))
			for _, impact := range change.Impacts {
				ids = append(ids, impact.ChapterID)
			}
			change.Impacts, err = chapterImpacts(in.tx, in.projectID, ids)
			if err != nil {
				return out, err
			}
		}
		assessed, err := assessedSourceIDs(in.tx, in.project, change)
		if err != nil {
			return out, err
		}
		out.ids = append(out.ids, assessed...)
		return out, nil
	}
}

// resolveApplyChangeSet authorizes a replay of an applied proposal. It checks
// the original cited sources plus the objects the recorded assessment actually
// named; it does not require a second revision application.
func resolveApplyChangeSet(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	changes, err := changeSetPort(in.tx)
	if err != nil {
		return out, err
	}
	change, err := changes.ChangeSet(in.projectID, in.targetID)
	if err != nil {
		return out, err
	}
	out.ids, err = semanticSourceIDs(in.tx, in.project, change.SemanticDeltas)
	if err != nil {
		return out, err
	}
	// The change-set state machine decides whether an apply is allowed at
	// all, so it runs before the assessment is read. An unassessed change set
	// has no assessment record, and reading it there answered "not found" for
	// a change set that does exist. The states an apply has already been
	// answered in are let through, so that the operation's idempotent replay
	// can still return its answer.
	if !ChangeSetApplyAnswered(change.Status) && !ChangeSetStatusCanMove("apply", change.Status) {
		return out, ErrInvalidState
	}
	store, err := impactPort(in.tx)
	if err != nil {
		return out, err
	}
	assessment, err := store.ImpactAssessment(in.projectID, change.ID)
	if err != nil {
		// The change set exists but carries no assessment: it is not
		// assessed, so it cannot be applied. That is an invalid state, not a
		// missing resource.
		if errors.Is(err, ErrNotFound) {
			return out, ErrChangeSetNotAssessed
		}
		return out, err
	}
	for _, object := range assessment.Objects {
		out.ids = append(out.ids, object.SourceIDs...)
		out.refs = append(out.refs, object.Ref)
	}
	return out, nil
}

// resolveStabilizationTasks authorizes the tasks the stabilization entry point
// is about to close. A BLOCK_RELEASE task is excluded from its target because
// its closure evidence already carries what the preflight named.
func resolveStabilizationTasks(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	store, err := impactPort(in.tx)
	if err != nil {
		return out, err
	}
	tasks, err := store.ImpactTasks(in.projectID)
	if err != nil {
		return out, err
	}
	for _, task := range tasks {
		if task.ChangeSetID != in.targetID || task.TargetRevision != in.project.CurrentContextRevision {
			continue
		}
		if task.Action != "BLOCK_RELEASE" {
			out.refs = append(out.refs, task.Target)
		}
		out.refs = append(out.refs, task.ClosureEvidence...)
	}
	return out, nil
}

// resolveImpactTaskAction authorizes the task target plus whatever closure
// evidence the request presents.
func resolveImpactTaskAction(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	input, ok := in.body.(ImpactTaskActionInput)
	if !ok {
		return out, ErrInvalidRequest
	}
	store, err := impactPort(in.tx)
	if err != nil {
		return out, err
	}
	task, err := store.ImpactTask(in.projectID, in.targetID)
	if err != nil {
		return out, err
	}
	out.refs = append(out.refs, task.Target)
	out.refs = append(out.refs, input.ClosureEvidence...)
	return out, nil
}

// resolveNewDependencyEdge authorizes the two endpoints the request names.
func resolveNewDependencyEdge(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	input, ok := in.body.(DependencyEdgeInput)
	if !ok {
		return out, ErrInvalidRequest
	}
	return impactSourceContribution{refs: []ObjectVersionRef{input.Source, input.Target}}, nil
}

// resolveDependencyEdgeDecision authorizes the endpoints of the edge the
// request decides.
func resolveDependencyEdgeDecision(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	store, err := impactPort(in.tx)
	if err != nil {
		return out, err
	}
	edge, err := store.DependencyEdge(in.projectID, in.targetID)
	if err != nil {
		return out, err
	}
	return impactSourceContribution{refs: []ObjectVersionRef{edge.Source, edge.Target}}, nil
}

// resolveDependencyCandidateBatch authorizes every object the batch proposes
// a relation between: an AI may not propose over an object the caller may
// not read.
func resolveDependencyCandidateBatch(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	input, ok := in.body.(DependencyCandidateBatchInput)
	if !ok {
		return out, ErrInvalidRequest
	}
	for _, candidate := range input.Candidates {
		out.refs = append(out.refs, candidate.Source, candidate.Target)
	}
	return out, nil
}

// resolveDependencyCandidateDecision authorizes the endpoints of the
// candidate the request decides.
func resolveDependencyCandidateDecision(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	store, ok := in.tx.(dependencyCandidateStore)
	if !ok {
		return out, ErrInvalidState
	}
	candidate, err := store.DependencyCandidate(in.projectID, in.targetID)
	if err != nil {
		return out, err
	}
	return impactSourceContribution{refs: []ObjectVersionRef{candidate.Source, candidate.Target}}, nil
}

// resolveAcknowledgedPreflight authorizes the preflight proof the request
// acknowledges, or the frozen snapshot it names instead.
func resolveAcknowledgedPreflight(in impactSourceProjectionInput) (out impactSourceContribution, err error) {
	input, ok := in.body.(map[string]string)
	if !ok || input["proof_id"] == "" && input["snapshot_id"] == "" {
		return out, ErrInvalidRequest
	}
	proofID := input["proof_id"]
	kind := "DeliveryPreflight"
	if proofID == "" {
		proofID = input["snapshot_id"]
		kind = "ReleaseSnapshot"
	}
	return impactSourceContribution{refs: []ObjectVersionRef{{Kind: kind, ID: proofID}}}, nil
}

// The caller already authorized this project in the same transaction. This
// projection performs no policy IO and no domain write. Its result is checked
// outside SQLite's connection, then re-read in the final write transaction.
//
// The body below is the shared prologue and epilogue, written once and applied
// to whichever operation the dispatch table names.
func OperationSourceIDs(tx Transaction, op, target, projectID string, body any) ([]string, error) {
	resolver, ok := impactSourceProjections[op]
	if !ok {
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
	found, err := resolver(impactSourceProjectionInput{
		tx: tx, project: p, projectID: projectID,
		targetID: target[strings.LastIndex(target, "/")+1:],
		body:     body,
	})
	if err != nil {
		return nil, err
	}
	resolved, err := impactSourceIDsForRefs(tx, p, found.refs)
	if err != nil {
		return nil, err
	}
	return sortedImpactSourceIDs(append(found.ids, resolved...)), nil
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
