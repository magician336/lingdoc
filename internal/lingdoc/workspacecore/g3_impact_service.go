package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

var ErrImpactObjectUnavailable = errors.New("current impact object adapter is unavailable")

func (s *Service) SetImpactRuleChecker(checker ImpactRuleChecker) { s.impactRules = checker }
func validObjectRef(ref ObjectVersionRef) bool {
	return ref.ID != "" && ref.Version != "" && len(ref.ID) <= 128 && len(ref.Version) <= 128 && slices.Contains([]string{"ProjectSpec", "ProjectAsset", "SourceAnchor", "EvidenceCard", "ResearchClaim", "ChapterVersion", "TemplateCopy", "RuleSet", "RuleBinding", "RouteNode", "ResearchRun", "ComputationReceipt"}, ref.Kind)
}
func validImpactAction(action string) bool {
	return slices.Contains([]string{"REVALIDATE_EVIDENCE", "RETRIEVE_CANDIDATES", "REVIEW_REQUIRED", "REGENERATE", "RECOMPUTE", "INVALIDATE_CONFIRMATION", "BLOCK_RELEASE"}, action)
}
func refIdentity(ref ObjectVersionRef) string { return ref.Kind + "/" + ref.ID + "/" + ref.Version }
func currentImpactObject(tx Transaction, p Project, ref ObjectVersionRef) (ImpactObjectState, error) {
	if !validObjectRef(ref) {
		return ImpactObjectState{}, ErrInvalidRequest
	}
	state := ImpactObjectState{Ref: ref, State: "CURRENT", SourceIDs: []string{}, ContextRevision: p.CurrentContextRevision}
	switch ref.Kind {
	case "ProjectSpec":
		if ref.ID != p.ID {
			return state, ErrNotFound
		}
		state.Ref.Version = strconv.FormatInt(p.SpecRevision, 10)
		state.Confirmed = p.Status == "active"
	case "ChapterVersion":
		chapter, err := tx.Chapter(p.ID, ref.ID)
		if err != nil {
			return state, err
		}
		if chapter.CurrentVersionID == nil {
			return state, ErrInvalidState
		}
		state.Ref.Version = *chapter.CurrentVersionID
		version, err := tx.ChapterVersion(p.ID, ref.ID, *chapter.CurrentVersionID)
		if err != nil {
			return state, err
		}
		state.Confirmed = chapter.ConfirmationValid && version.SpecRevision == p.SpecRevision
		state.SourceIDs = append(state.SourceIDs, chapter.SourceIDs...)
		state.ContextRevision = p.CurrentContextRevision
	case "ResearchRun", "ComputationReceipt":
		var err error
		state, err = currentImpactResultObject(tx, p, ref)
		if err != nil {
			return state, err
		}
	case "RouteNode":
		var err error
		state, err = currentRouteImpactObject(tx, p, ref, map[string]bool{})
		if err != nil {
			return state, err
		}
	case "RuleBinding":
		value, err := currentSemanticValue(tx, p, SemanticDelta{Kind: "RuleBinding", TargetID: ref.ID})
		if err != nil {
			return state, err
		}
		state.Ref = value.Ref
		state.State = "CURRENT"
		state.Confirmed = true
		state.SourceIDs = value.SourceIDs
	case "TemplateCopy":
		if p.TemplateCopy == nil || ref.ID != p.TemplateID {
			return state, ErrNotFound
		}
		state.Ref.Version = strconv.FormatInt(p.TemplateCopyVersion, 10)
		state.Confirmed = p.TemplateCopy.Status == TemplateCopyBound
	case "RuleSet":
		if p.TemplateCopy == nil || ref.ID != p.TemplateID {
			return state, ErrNotFound
		}
		state.Ref.Version = p.TemplateCopy.RulesetHash
		state.Confirmed = state.Ref.Version != ""
	default:
		reader, ok := tx.(currentEvidenceObjectTransaction)
		if !ok {
			return state, ErrImpactObjectUnavailable
		}
		var err error
		state, err = reader.CurrentEvidenceObject(p.ID, ref.Kind, ref.ID)
		if err != nil {
			return state, err
		}
	}
	if state.Ref.Version != ref.Version {
		return state, ErrVersionConflict
	}
	if state.State == "STALE" || state.State == "SUPERSEDED" || state.State == "replaced" || state.State == "failed" {
		return state, ErrInvalidState
	}
	return state, nil
}
func hashImpactValue(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func checkedImpactWrite[I any, R any](s *Service, ctx context.Context, actor Actor, projectID string, op operationName, target, key string, input I, refs []ObjectVersionRef, request AuthorizationRequest, write func(Transaction, Project) (operationResult[R], error)) (json.RawMessage, int, bool, error) {
	var basis Project
	var states []ImpactObjectState
	var sourceIDs []string
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		basis, err = s.authorizeProject(ctx, tx, request)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			state, err := currentImpactObject(tx, basis, ref)
			if err != nil {
				return err
			}
			states = append(states, state)
			sourceIDs = append(sourceIDs, state.SourceIDs...)
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	if err := s.checkSources(ctx, actor, projectID, sourceIDs); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, sourceOperationInput(op, target, key, input, request, impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[R], error) {
		if p.ProjectVersion != basis.ProjectVersion {
			return operationResult[R]{}, ErrVersionConflict
		}
		for i, ref := range refs {
			current, err := currentImpactObject(tx, p, ref)
			if err != nil {
				return operationResult[R]{}, err
			}
			if hashImpactValue(current) != hashImpactValue(states[i]) {
				return operationResult[R]{}, ErrVersionConflict
			}
		}
		return write(tx, p)
	})
}
func g3Audit(tx Transaction, actor Actor, project, action, target string, details map[string]any) error {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return ErrInvalidState
	}
	return audit.RecordAudit(AuditEvent{ID: uuid.NewString(), TenantID: actor.TenantID, ProjectID: project, ActorID: actor.UserID, Action: action, Target: target, Details: details, CreatedAt: time.Now().UTC()})
}

func (s *Service) CreateDependencyEdge(ctx context.Context, actor Actor, projectID, key string, input DependencyEdgeInput) (json.RawMessage, int, bool, error) {
	if input.Origin != "ai" {
		if err := RequireHumanDecision(ctx); err != nil {
			return nil, 0, false, err
		}
	}
	if !validObjectRef(input.Source) || !validObjectRef(input.Target) || !validImpactAction(input.Action) || strings.TrimSpace(input.Reason) == "" || input.Relation == "" || math.IsNaN(input.Confidence) || input.Confidence < 0 || input.Confidence > 1 || !slices.Contains([]string{"user", "rule", "ai"}, input.Origin) {
		return nil, 0, false, ErrInvalidRequest
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationCreateDependencyEdge, projectID, key, input, []ObjectVersionRef{input.Source, input.Target}, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[DependencyEdge], error) {
		if p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[DependencyEdge]{}, ErrVersionConflict
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return operationResult[DependencyEdge]{}, ErrInvalidState
		}
		edges, err := store.DependencyEdges(projectID)
		if err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		for _, edge := range edges {
			if edge.Source == input.Source && edge.Target == input.Target && edge.Relation == input.Relation && edge.Action == input.Action {
				return operationResult[DependencyEdge]{Value: edge, Status: operationStatusOK}, nil
			}
		}
		now := time.Now().UTC()
		status := "confirmed"
		if input.Origin == "ai" {
			status = "suggested"
		}
		edge := DependencyEdge{ID: uuid.NewString(), ProjectID: projectID, Source: input.Source, Target: input.Target, Relation: input.Relation, Action: input.Action, Origin: input.Origin, Reason: input.Reason, Confidence: input.Confidence, Status: status, ActorID: actor.UserID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.PutDependencyEdge(edge); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		if err := g3Audit(tx, actor, projectID, "dependency.create", edge.ID, map[string]any{"status": status, "source": edge.Source, "target": edge.Target}); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		return operationResult[DependencyEdge]{Value: edge, Status: operationStatusCreated}, nil
	})
}
func (s *Service) DecideDependencyEdge(ctx context.Context, actor Actor, projectID, edgeID, key string, input DependencyDecisionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"confirm", "reject"}, input.Decision) || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	var edge DependencyEdge
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, "")); err != nil {
			return err
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		edge, err = store.DependencyEdge(projectID, edgeID)
		return err
	})
	if err != nil {
		return nil, 0, false, err
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationDecideDependencyEdge, projectID+"/"+edgeID, key, input, []ObjectVersionRef{edge.Source, edge.Target}, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[DependencyEdge], error) {
		store := tx.(impactTransaction)
		old, err := store.DependencyEdge(projectID, edgeID)
		if err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		if old.Version != input.ExpectedVersion {
			return operationResult[DependencyEdge]{}, ErrVersionConflict
		}
		if old.Status != "suggested" {
			return operationResult[DependencyEdge]{}, ErrInvalidState
		}
		next := old
		next.Status = "rejected"
		if input.Decision == "confirm" {
			next.Status = "confirmed"
		}
		next.Version++
		next.ActorID = actor.UserID
		next.Reason = input.Reason
		next.UpdatedAt = time.Now().UTC()
		if err := store.UpdateDependencyEdge(old, next); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		if err := g3Audit(tx, actor, projectID, "dependency."+input.Decision, edgeID, map[string]any{"version": next.Version}); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		return operationResult[DependencyEdge]{Value: next, Status: operationStatusOK}, nil
	})
}
func (s *Service) ListDependencyEdges(ctx context.Context, actor Actor, projectID string) ([]DependencyEdge, error) {
	var p Project
	var result []DependencyEdge
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		p, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = store.DependencyEdges(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	visible := make([]DependencyEdge, 0, len(result))
	for _, edge := range result {
		var sources []string
		err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			for _, ref := range []ObjectVersionRef{edge.Source, edge.Target} {
				state, err := currentImpactObject(tx, p, ref)
				if err != nil {
					return err
				}
				sources = append(sources, state.SourceIDs...)
			}
			return nil
		})
		if err == nil && s.checkSources(ctx, actor, projectID, sources) == nil {
			visible = append(visible, edge)
		}
	}
	return visible, nil
}

func buildImpactAssessment(tx Transaction, p Project, change ChangeSet) (ImpactAssessment, error) {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ImpactAssessment{}, ErrInvalidState
	}
	edges, err := store.DependencyEdges(p.ID)
	if err != nil {
		return ImpactAssessment{}, err
	}
	if len(edges) > 10000 {
		return ImpactAssessment{}, ErrInvalidState
	}
	result := ImpactAssessment{ChangeSetID: change.ID, ProjectID: p.ID, ContextRevision: p.CurrentContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, Objects: []ImpactObjectState{}, Edges: []DependencyEdge{}, Impacts: []ResolvedImpact{}, AssessedAt: time.Now().UTC()}
	if p.TemplateCopy != nil {
		result.RulesetHash = p.TemplateCopy.RulesetHash
	}
	specRef := ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)}
	roots := []ObjectVersionRef{}
	if len(change.Fields) > 0 || change.TemplateUpgrade != nil || len(change.SemanticDeltas) == 0 {
		roots = append(roots, specRef)
	}
	semanticStates := map[string]ImpactObjectState{}
	if len(change.SemanticDeltas) > 0 {
		result.SemanticHash = hashImpactValue(change.SemanticDeltas)
	}
	for _, delta := range change.SemanticDeltas {
		value, e := currentSemanticValue(tx, p, delta)
		if e != nil {
			return result, e
		}
		if value.Ref.Version != strconv.FormatInt(delta.ExpectedVersion, 10) {
			return result, ErrVersionConflict
		}
		if delta.BeforeHash != "" && semanticValueHash(value) != delta.BeforeHash {
			return result, ErrVersionConflict
		}
		state := ImpactObjectState{Ref: value.Ref, State: "CURRENT", SourceIDs: append(value.SourceIDs, delta.SourceIDs...), ContextRevision: p.CurrentContextRevision}
		if delta.ProposedRoute != nil {
			for _, input := range delta.ProposedRoute.Inputs {
				current, e := currentImpactObject(tx, p, input)
				if e != nil {
					return result, e
				}
				state.SourceIDs = append(state.SourceIDs, current.SourceIDs...)
			}
		}
		state.SourceIDs = sortedImpactSourceIDs(state.SourceIDs)
		roots = append(roots, value.Ref)
		semanticStates[refIdentity(value.Ref)] = state
	}

	if change.TemplateUpgrade != nil && p.TemplateCopy != nil {
		roots = append(roots, ObjectVersionRef{Kind: "TemplateCopy", ID: p.TemplateID, Version: strconv.FormatInt(p.TemplateCopyVersion, 10)}, ObjectVersionRef{Kind: "RuleSet", ID: p.TemplateID, Version: p.TemplateCopy.RulesetHash})
	}
	states := map[string]ImpactObjectState{}
	requiredObjects := map[string]bool{}
	for _, root := range roots {
		state, found := semanticStates[refIdentity(root)]
		if !found {
			var err error
			state, err = currentImpactObject(tx, p, root)
			if err != nil {
				return result, err
			}
		}
		states[refIdentity(root)] = state
		requiredObjects[refIdentity(root)] = true
	}
	valid := make([]DependencyEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.Status != "confirmed" && edge.Status != "suggested" {
			continue
		}
		source, e1 := currentImpactObject(tx, p, edge.Source)
		target, e2 := currentImpactObject(tx, p, edge.Target)
		if e1 != nil || e2 != nil {
			if e1 != nil && !errors.Is(e1, ErrVersionConflict) && !errors.Is(e1, ErrInvalidState) && !errors.Is(e1, ErrNotFound) {
				return result, e1
			}
			if e2 != nil && !errors.Is(e2, ErrVersionConflict) && !errors.Is(e2, ErrInvalidState) && !errors.Is(e2, ErrNotFound) {
				return result, e2
			}
			continue
		}
		states[refIdentity(source.Ref)] = source
		states[refIdentity(target.Ref)] = target
		valid = append(valid, edge)
	}
	visited := map[string]bool{}
	queue := append([]ObjectVersionRef{}, roots...)
	impacts := map[string]*ResolvedImpact{}
	add := func(ref ObjectVersionRef, action, certainty string, cause ImpactCause) {
		key := refIdentity(ref) + "/" + action + "/" + certainty
		if impacts[key] == nil {
			impacts[key] = &ResolvedImpact{Target: ref, Action: action, Certainty: certainty, Causes: []ImpactCause{}}
		}
		found := false
		for _, old := range impacts[key].Causes {
			if old == cause {
				found = true
			}
		}
		if !found {
			impacts[key].Causes = append(impacts[key].Causes, cause)
		}
	}
	for _, delta := range change.SemanticDeltas {
		ref := semanticRef(delta)
		action := "REVIEW_REQUIRED"
		if delta.Kind == "RuleBinding" {
			action = "RECOMPUTE"
		}
		if delta.Kind == "ProjectAssetScope" {
			action = "BLOCK_RELEASE"
		}
		add(ref, action, "definite", ImpactCause{EdgeID: "semantic:" + change.ID + "/" + delta.Kind + "/" + delta.TargetID, Source: ref, Reason: change.Reason})
	}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		id := refIdentity(ref)
		if visited[id] {
			continue
		}
		visited[id] = true
		for _, edge := range valid {
			if edge.Source != ref {
				continue
			}
			certainty := "definite"
			if edge.Status == "suggested" {
				certainty = "possible"
			}
			add(edge.Target, edge.Action, certainty, ImpactCause{EdgeID: edge.ID, Source: edge.Source, Reason: edge.Reason})
			if certainty == "definite" {
				requiredObjects[refIdentity(edge.Target)] = true
				requiredObjects[refIdentity(edge.Source)] = true
				queue = append(queue, edge.Target)
			}
		}
	}
	for _, impact := range change.Impacts {
		if impact.ChapterVersionID == nil {
			continue
		}
		ref := ObjectVersionRef{Kind: "ChapterVersion", ID: impact.ChapterID, Version: *impact.ChapterVersionID}
		state, err := currentImpactObject(tx, p, ref)
		if err != nil {
			return result, err
		}
		states[refIdentity(ref)] = state
		requiredObjects[refIdentity(ref)] = true
		add(ref, "REVIEW_REQUIRED", "definite", ImpactCause{EdgeID: "explicit:" + change.ID, Source: specRef, Reason: impact.Reason})
	}
	keys := make([]string, 0, len(impacts))
	for key := range impacts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		result.Impacts = append(result.Impacts, *impacts[key])
	}
	// Fingerprint eligible edges and reached definite objects so a
	// graph update never silently inherits a previous Owner impact preview.
	result.Edges = valid
	slices.SortFunc(result.Edges, func(a, b DependencyEdge) int { return strings.Compare(a.ID, b.ID) })
	keys = keys[:0]
	for key := range states {
		if !requiredObjects[key] {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		result.Objects = append(result.Objects, states[key])
	}
	fingerprint := result
	fingerprint.AssessedAt = time.Time{}
	fingerprint.InputHash = ""
	result.InputHash = hashImpactValue(fingerprint)
	return result, nil
}
func AssessChangeImpact(tx Transaction, p Project, change ChangeSet) error {
	assessment, err := buildImpactAssessment(tx, p, change)
	if err != nil {
		return err
	}
	if err := tx.(impactTransaction).RecordContextRevision(p, change); err != nil {
		return err
	}
	return tx.(impactTransaction).SaveImpactAssessment(assessment)
}
func ValidateChangeImpactAssessment(tx Transaction, p Project, change ChangeSet) error {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	old, err := store.ImpactAssessment(p.ID, change.ID)
	if err != nil {
		return err
	}
	current, err := buildImpactAssessment(tx, p, change)
	if err != nil {
		return err
	}
	if current.InputHash != old.InputHash {
		return ErrVersionConflict
	}
	return nil
}
func CommitChangeImpactTasks(tx Transaction, p Project, change ChangeSet) error {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	assessment, err := store.ImpactAssessment(p.ID, change.ID)
	if err != nil {
		return err
	}
	// The impact graph drives actual confirmation invalidation as well as tasks.
	// Only the exact current version assessed here is invalidated; newer body
	// versions are never silently selected by an old proposal.
	chapterIDs := []string{}
	for _, impact := range assessment.Impacts {
		if impact.Certainty != "definite" || impact.Target.Kind != "ChapterVersion" {
			continue
		}
		chapter, e := tx.Chapter(p.ID, impact.Target.ID)
		if e != nil {
			return e
		}
		if chapter.CurrentVersionID != nil && *chapter.CurrentVersionID == impact.Target.Version {
			chapterIDs = append(chapterIDs, chapter.ID)
		}
	}
	if len(chapterIDs) > 0 {
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		if err := changes.InvalidateChapterConfirmations(p.ID, sortedImpactSourceIDs(chapterIDs)); err != nil {
			return err
		}
	}
	for _, impact := range assessment.Impacts {
		if impact.Certainty != "definite" {
			continue
		}
		domain := "B"
		if slices.Contains([]string{"REVALIDATE_EVIDENCE", "RETRIEVE_CANDIDATES"}, impact.Action) {
			domain = "A"
		}
		if impact.Action == "BLOCK_RELEASE" || impact.Action == "RECOMPUTE" {
			domain = "C"
		}
		for _, delta := range change.SemanticDeltas {
			if impact.Target == semanticRef(delta) {
				current, e := currentSemanticValue(tx, p, delta)
				if e != nil {
					return e
				}
				impact.Target = current.Ref
			}
		}
		now := time.Now().UTC()
		task := ImpactTask{ID: uuid.NewString(), ProjectID: p.ID, ChangeSetID: change.ID, Target: impact.Target, TargetRevision: p.CurrentContextRevision, Action: impact.Action, Severity: "blocking", ResponsibilityDomain: domain, Status: "OPEN", Causes: impact.Causes, Version: 1, ClosureEvidence: []ObjectVersionRef{}, CreatedAt: now, UpdatedAt: now}
		if err := store.InsertImpactTask(task); err != nil {
			return err
		}
		if err := store.InsertImpactHandoff(task); err != nil {
			return err
		}
	}
	return store.RecordContextRevision(p, change)
}
func (s *Service) GetImpactAssessment(ctx context.Context, actor Actor, projectID, changeID string) (ImpactAssessment, error) {
	var result ImpactAssessment
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = store.ImpactAssessment(projectID, changeID)
		return err
	})
	if err != nil {
		return result, err
	}
	var sources []string
	for _, state := range result.Objects {
		sources = append(sources, state.SourceIDs...)
	}
	if err := s.checkSources(ctx, actor, projectID, sources); err != nil {
		return ImpactAssessment{}, err
	}
	return result, nil
}
func (s *Service) ListImpactTasks(ctx context.Context, actor Actor, projectID string) ([]ImpactTask, error) {
	var result []ImpactTask
	var project Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		project, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = store.ImpactTasks(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.visibleImpactTasks(ctx, actor, project, result)
}
func (s *Service) ActOnImpactTask(ctx context.Context, actor Actor, projectID, taskID, key string, input ImpactTaskActionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || input.TargetRevision < 1 || strings.TrimSpace(input.Reason) == "" || !slices.Contains([]string{"assign", "start", "wait", "complete", "dismiss", "cancel"}, input.Action) {
		return nil, 0, false, ErrInvalidRequest
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationActOnImpactTask, projectID+"/"+taskID, key, input, input.ClosureEvidence, authorizationRequest(actor, projectID, AuthorizationWrite, ""), func(tx Transaction, p Project) (operationResult[ImpactTask], error) {
		store, ok := tx.(impactTransaction)
		if !ok {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		task, err := store.ImpactTask(projectID, taskID)
		if err != nil {
			return operationResult[ImpactTask]{}, err
		}
		if task.Version != input.ExpectedVersion {
			return operationResult[ImpactTask]{}, ErrVersionConflict
		}
		if task.TargetRevision != p.CurrentContextRevision || input.TargetRevision != p.CurrentContextRevision {
			return operationResult[ImpactTask]{}, ErrVersionConflict
		}
		if slices.Contains([]string{"DONE", "DISMISSED", "CANCELLED", "STALE"}, task.Status) {
			return operationResult[ImpactTask]{}, ErrInvalidState
		}
		old := task
		if input.Action == "assign" || input.Action == "dismiss" || input.Action == "cancel" {
			if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, "")); err != nil {
				return operationResult[ImpactTask]{}, err
			}
		} else if task.AssigneeID != actor.UserID {
			return operationResult[ImpactTask]{}, ErrNotFound
		}
		switch input.Action {
		case "assign":
			if input.AssigneeID == "" {
				return operationResult[ImpactTask]{}, ErrInvalidRequest
			}
			recipient := actor
			recipient.UserID = input.AssigneeID
			if _, err := s.authorizeProject(ctx, tx, authorizationRequest(recipient, projectID, AuthorizationWrite, "")); err != nil {
				return operationResult[ImpactTask]{}, err
			}
			task.AssigneeID = input.AssigneeID
		case "start":
			task.Status = "IN_PROGRESS"
		case "wait":
			task.Status = "WAITING_USER"
		case "cancel", "dismiss":
			if task.Severity == "blocking" {
				return operationResult[ImpactTask]{}, ErrInvalidState
			}
			task.Status = "CANCELLED"
			if input.Action == "dismiss" {
				task.Status = "DISMISSED"
			}
		case "complete":
			if task.Action == "BLOCK_RELEASE" || len(input.ClosureEvidence) == 0 {
				return operationResult[ImpactTask]{}, ErrInvalidState
			}
			if task.Action == "RECOMPUTE" || task.Action == "RETRIEVE_CANDIDATES" {
				if err := verifyImpactActionResult(tx, p, task, input.ClosureEvidence); err != nil {
					return operationResult[ImpactTask]{}, err
				}
				task.Status = "DONE"
				task.ClosureEvidence = append([]ObjectVersionRef{}, input.ClosureEvidence...)
				break
			}
			matched := false
			for _, ref := range input.ClosureEvidence {
				state, err := currentImpactObject(tx, p, ref)
				if err != nil {
					return operationResult[ImpactTask]{}, err
				}
				if ref.Kind == task.Target.Kind && ref.ID == task.Target.ID {
					matched = true
					if task.Action == "REVIEW_REQUIRED" && task.Target.Kind == "ResearchClaim" {
						if input.HumanDecision != "reviewed" || !researchClaimReviewCurrent(tx, p, ref) {
							return operationResult[ImpactTask]{}, ErrInvalidState
						}
						task.HumanDecision = input.HumanDecision
					} else if slices.Contains([]string{"REVIEW_REQUIRED", "INVALIDATE_CONFIRMATION", "REVALIDATE_EVIDENCE"}, task.Action) && !state.Confirmed {
						return operationResult[ImpactTask]{}, ErrInvalidState
					}
					if task.Action == "REGENERATE" && ref.Version == task.Target.Version {
						return operationResult[ImpactTask]{}, ErrInvalidState
					}
					task.Target = ref
				}
			}
			if !matched {
				return operationResult[ImpactTask]{}, ErrInvalidState
			}
			task.Status = "DONE"
			task.ClosureEvidence = append([]ObjectVersionRef{}, input.ClosureEvidence...)
		}
		task.Version++
		task.Disposition = input.Reason
		task.UpdatedAt = time.Now().UTC()
		if err := store.UpdateImpactTask(old, task); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		if err := g3Audit(tx, actor, projectID, "impact_task."+input.Action, taskID, map[string]any{"status": task.Status, "target_revision": task.TargetRevision, "closure_evidence": task.ClosureEvidence}); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		return operationResult[ImpactTask]{Value: task, Status: operationStatusOK}, nil
	})
}
func (s *Service) StabilizeRevision(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	var refs []ObjectVersionRef
	var preflightIDs []string
	var preflightProofs []ImpactPreflightEvidence
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		tasks, err := store.ImpactTasks(projectID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.ChangeSetID == changeID && task.TargetRevision == p.CurrentContextRevision && task.Status == "DONE" {
				if task.Action == "BLOCK_RELEASE" {
					for _, ref := range task.ClosureEvidence {
						preflightIDs = append(preflightIDs, ref.ID)
					}
				} else {
					refs = append(refs, task.ClosureEvidence...)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	for _, snapshotID := range preflightIDs {
		proof, err := s.recheckImpactPreflight(ctx, actor, projectID, snapshotID)
		if err != nil {
			return nil, 0, false, err
		}
		preflightProofs = append(preflightProofs, proof)
	}
	return checkedImpactWrite(s, ctx, actor, projectID, operationStabilizeRevision, projectID+"/"+changeID, key, map[string]string{"change_set_id": changeID}, refs, authorizationRequest(actor, projectID, AuthorizationManage, ""), func(tx Transaction, p Project) (operationResult[StabilizationResult], error) {
		for _, proof := range preflightProofs {
			if !validImpactPreflight(p, impactPreflightProofID(proof), proof) {
				return operationResult[StabilizationResult]{}, ErrVersionConflict
			}
		}
		if !projectOwner(p, actor.UserID) {
			return operationResult[StabilizationResult]{}, ErrNotFound
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[StabilizationResult]{}, ErrInvalidState
		}
		change, err := changes.ChangeSet(projectID, changeID)
		if err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		if change.TargetContextRevision == nil || *change.TargetContextRevision != p.CurrentContextRevision || !slices.Contains([]string{"applied", "stabilized"}, change.Status) {
			return operationResult[StabilizationResult]{}, ErrVersionConflict
		}
		result := StabilizationResult{ChangeSetID: changeID, ContextRevision: p.CurrentContextRevision, Status: "BLOCKED", Blockers: []ImpactBlocker{}, CheckedAt: time.Now().UTC()}
		store := tx.(impactTransaction)
		tasks, err := store.ImpactTasks(projectID)
		if err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		for _, task := range tasks {
			if task.ChangeSetID != changeID || task.TargetRevision != p.CurrentContextRevision {
				continue
			}
			if task.Severity == "blocking" && task.Status != "DONE" {
				result.Blockers = append(result.Blockers, ImpactBlocker{Code: "impact_task_open", TargetID: task.ID, Reason: task.Action})
				continue
			}
			if task.Status == "DONE" && (task.Action == "RECOMPUTE" || task.Action == "RETRIEVE_CANDIDATES") {
				if err := verifyImpactActionResult(tx, p, task, task.ClosureEvidence); err != nil {
					result.Blockers = append(result.Blockers, ImpactBlocker{Code: "result_receipt_stale", TargetID: task.ID, Reason: "服务端结果回执未完成或已经过期"})
				}
				continue
			}
			if task.Status == "DONE" && task.Action != "BLOCK_RELEASE" {
				for _, ref := range task.ClosureEvidence {
					state, err := currentImpactObject(tx, p, ref)
					if err != nil || ((task.Action == "REVIEW_REQUIRED" || task.Action == "INVALIDATE_CONFIRMATION" || task.Action == "REVALIDATE_EVIDENCE") && (!state.Confirmed && (task.Action != "REVIEW_REQUIRED" || task.Target.Kind != "ResearchClaim" || task.HumanDecision != "reviewed" || !researchClaimReviewCurrent(tx, p, ref)))) {
						result.Blockers = append(result.Blockers, ImpactBlocker{Code: "closure_stale", TargetID: task.ID, Reason: "闭环证据已变化，需重新处理"})
					}
				}
			}
		}
		routeBlockers, err := RouteStabilizationBlockers(tx, p)
		if err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		result.Blockers = append(result.Blockers, routeBlockers...)
		if p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound {
			result.Blockers = append(result.Blockers, ImpactBlocker{Code: "template_unbound", Reason: "当前模板未绑定"})
		}
		if s.impactRules == nil {
			result.Blockers = append(result.Blockers, ImpactBlocker{Code: "rules_checker_unavailable", Reason: "缺少当前规则检查器"})
		} else {
			blockers, err := s.impactRules.Check(tx, p)
			if err != nil {
				return operationResult[StabilizationResult]{}, err
			}
			result.Blockers = append(result.Blockers, blockers...)
		}
		if len(result.Blockers) == 0 {
			result.Status = "STABILIZED"
			if change.Status != "stabilized" {
				next := change
				next.Status = "stabilized"
				if err := changes.UpdateChangeSet(change, next); err != nil {
					return operationResult[StabilizationResult]{}, err
				}
			}
		}
		if err := g3Audit(tx, actor, projectID, "revision.stabilize", changeID, map[string]any{"status": result.Status, "blockers": result.Blockers}); err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		return operationResult[StabilizationResult]{Value: result, Status: operationStatusOK}, nil
	})
}

func (s *Service) CreateChangeDraft(ctx context.Context, actor Actor, projectID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
	if err := validateExtendedChangeInput(input); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, sourceOperationInput(operationCreateChangeDraft, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		if p.Status != "active" || p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		change := ChangeSet{ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID, Reason: input.Reason, Status: "draft", BaseContextRevision: p.CurrentContextRevision, BaseSpecRevision: p.SpecRevision, Fields: []ChangeFieldDelta{}, Impacts: []ChangeImpact{}, CreatedAt: time.Now().UTC()}
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
		if err := g3Audit(tx, actor, projectID, "change_set.draft", change.ID, nil); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusCreated}, nil
	})
}
func (s *Service) AssessChangeSet(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error) {
	return runOperation(s, ctx, actor, sourceOperationInput(operationAssessChangeSet, projectID+"/"+changeID, key, map[string]string{"change_set_id": changeID}, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		change, err := store.ChangeSet(projectID, changeID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if !slices.Contains([]string{"draft", "assessed"}, change.Status) {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if change.BaseContextRevision != p.CurrentContextRevision || change.BaseSpecRevision != p.SpecRevision {
			return operationResult[ChangeSet]{}, ErrVersionConflict
		}
		if err := AssessChangeImpact(tx, p, change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		next := change
		next.Status = "assessed"
		if err := store.UpdateChangeSet(change, next); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := g3Audit(tx, actor, projectID, "change_set.assess", changeID, nil); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return operationResult[ChangeSet]{Value: next, Status: operationStatusOK}, nil
	})
}
