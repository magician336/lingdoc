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

// ErrDependencyDecided reports that a human already decided this dependency
// pair, so an automated run may not silently re-propose or overwrite it.
var ErrDependencyDecided = errors.New("dependency edge already decided")

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
func recordAuditEvent(tx Transaction, actor Actor, project, action, target string, details map[string]any) error {
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
		if input.Origin == "ai" {
			// An automated run may add a candidate, but it may not resurrect the
			// exact tuple a human replaced with decision="modify": that is the one
			// case where re-proposing would undo a recorded human decision.
			for _, edge := range edges {
				if edge.Source != input.Source || edge.Target != input.Target {
					continue
				}
				if edge.SupersededRelation == input.Relation && edge.SupersededAction == input.Action {
					return operationResult[DependencyEdge]{}, ErrDependencyDecided
				}
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
		if err := recordAuditEvent(tx, actor, projectID, "dependency.create", edge.ID, map[string]any{"status": status, "source": edge.Source, "target": edge.Target}); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		return operationResult[DependencyEdge]{Value: edge, Status: operationStatusCreated}, nil
	})
}
func (s *Service) DecideDependencyEdge(ctx context.Context, actor Actor, projectID, edgeID, key string, input DependencyDecisionInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if input.ExpectedVersion < 1 || !slices.Contains([]string{"confirm", "reject", "modify"}, input.Decision) || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if input.Decision == "modify" {
		if (input.Relation == nil && input.Action == nil && input.Confidence == nil) ||
			(input.Relation != nil && (strings.TrimSpace(*input.Relation) == "" || len(*input.Relation) > 128)) ||
			(input.Action != nil && !validImpactAction(*input.Action)) ||
			(input.Confidence != nil && (math.IsNaN(*input.Confidence) || *input.Confidence < 0 || *input.Confidence > 1)) {
			return nil, 0, false, ErrInvalidRequest
		}
	} else if input.Relation != nil || input.Action != nil || input.Confidence != nil {
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
		if input.Decision == "modify" {
			// A modification is a confirmation of the corrected candidate, not a
			// new candidate: it stays confirmed and becomes binding.
			next.Status = "confirmed"
			next.SupersededRelation, next.SupersededAction = old.Relation, old.Action
			if input.Relation != nil {
				next.Relation = *input.Relation
			}
			if input.Action != nil {
				next.Action = *input.Action
			}
			if input.Confidence != nil {
				next.Confidence = *input.Confidence
			}
		}
		next.Version++
		next.ActorID = actor.UserID
		next.Reason = input.Reason
		next.UpdatedAt = time.Now().UTC()
		if err := store.UpdateDependencyEdge(old, next); err != nil {
			return operationResult[DependencyEdge]{}, err
		}
		details := map[string]any{"version": next.Version}
		if input.Decision == "modify" {
			details["relation"], details["action"], details["confidence"] = next.Relation, next.Action, next.Confidence
		}
		if err := recordAuditEvent(tx, actor, projectID, "dependency."+input.Decision, edgeID, details); err != nil {
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
		// Identity is change_set_id + target_ref + target_revision + action_type.
		// Deriving the ID from that identity makes a duplicated, replayed or
		// out-of-order event address the same task instead of minting a second
		// one, and it survives a service restart because the ID is recomputed
		// rather than remembered.
		task := ImpactTask{ID: ImpactTaskID(p.ID, change.ID, impact.Target, p.CurrentContextRevision, impact.Action), ProjectID: p.ID, ChangeSetID: change.ID, Target: impact.Target, TargetRevision: p.CurrentContextRevision, Action: impact.Action, Severity: "blocking", ResponsibilityDomain: domain, Status: "OPEN", Causes: impact.Causes, Version: 1, ClosureEvidence: []ObjectVersionRef{}, CreatedAt: now, UpdatedAt: now}
		// A delayed or out-of-order redelivery must be judged against the
		// current domain truth, not against the stored event payload. If the
		// target version was already replaced, the task is born STALE with an
		// explicit reason instead of an actionable-looking OPEN task.
		if reason := staleTargetReason(tx, p, impact.Target); reason != "" {
			task.Status = "STALE"
			task.StaleReason = reason
			task.StaleAt = now
		}
		if err := store.InsertImpactTask(task); err != nil {
			return err
		}
		// The handoff record is keyed by the same identity, so a redelivery
		// re-issues no second handoff.
		if err := store.InsertImpactHandoff(task); err != nil {
			return err
		}
	}
	return store.RecordContextRevision(p, change)
}

// markImpactTaskStale performs the STALE transition, its version bump and its
// audit entry together, so a caller never leaves a half-applied invalidation.
//
// Idempotency: a task that is already STALE is returned untouched, so repeated
// triggers produce no second write, no second version bump and no second audit
// entry.
func (s *Service) markImpactTaskStale(tx Transaction, actor Actor, p Project, store impactTransaction, task ImpactTask, reason string) (ImpactTask, error) {
	if task.Status == "STALE" {
		return task, nil
	}
	stale := task
	now := time.Now().UTC()
	stale.Status = "STALE"
	stale.StaleReason = reason
	stale.StaleAt = now
	stale.UpdatedAt = now
	stale.Version++
	if err := store.UpdateImpactTask(task, stale); err != nil {
		return ImpactTask{}, err
	}
	if err := recordAuditEvent(tx, actor, p.ID, "impact_task.stale", task.ID, map[string]any{
		"status": stale.Status, "stale_reason": reason, "target_revision": stale.TargetRevision, "target": stale.Target}); err != nil {
		return ImpactTask{}, err
	}
	return stale, nil
}

// staleTargetReason reports why an impact target no longer supports work on
// the version a task was raised against. It re-reads the current domain truth
// instead of trusting a stored payload, so a redelivered or delayed event is
// classified the same way a fresh one would be.
func staleTargetReason(tx Transaction, p Project, target ObjectVersionRef) string {
	if !validObjectRef(target) {
		return ImpactTaskStaleTargetReplaced
	}
	if _, err := currentImpactObject(tx, p, target); err != nil {
		// The object is gone or no longer valid at the exact version the task
		// names: the obligation was raised against a version that has been
		// replaced. An adapter or infrastructure failure is deliberately not
		// treated as staleness, because it is not evidence about the version.
		if errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidState) {
			return ImpactTaskStaleTargetReplaced
		}
	}
	return ""
}
func (s *Service) GetImpactAssessment(ctx context.Context, actor Actor, projectID, changeID string) (ImpactAssessment, error) {
	var result ImpactAssessment
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
		result, err = store.ImpactAssessment(projectID, changeID)
		return err
	})
	if err != nil {
		return result, err
	}
	// Edges and impacts name objects that may sit outside Objects (a suggested
	// edge target is deliberately not a required object), so authorization has to
	// cover every ref the response exposes, not just the required set.
	refs := map[string]ObjectVersionRef{}
	for _, state := range result.Objects {
		refs[refIdentity(state.Ref)] = state.Ref
	}
	for _, edge := range result.Edges {
		refs[refIdentity(edge.Source)] = edge.Source
		refs[refIdentity(edge.Target)] = edge.Target
	}
	for _, impact := range result.Impacts {
		refs[refIdentity(impact.Target)] = impact.Target
		for _, cause := range impact.Causes {
			refs[refIdentity(cause.Source)] = cause.Source
		}
	}
	var sources []string
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		for _, ref := range refs {
			state, err := currentImpactObject(tx, project, ref)
			if err != nil {
				return err
			}
			sources = append(sources, state.SourceIDs...)
		}
		return nil
	})
	if err != nil {
		return ImpactAssessment{}, err
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
		// Re-check the task's basis before doing anything with it. When the
		// basis is gone the task becomes STALE with an explicit reason rather
		// than silently failing the action, so the obligation stays visible and
		// auditable instead of vanishing or being mistaken for success.
		if task.Status != "STALE" {
			if reason := s.impactTaskStaleReason(ctx, tx, actor, p, task, input.Action); reason != "" {
				stale, err := s.markImpactTaskStale(tx, actor, p, store, task, reason)
				if err != nil {
					return operationResult[ImpactTask]{}, err
				}
				return operationResult[ImpactTask]{Value: stale, Status: operationStatusOK}, nil
			}
		}
		if task.TargetRevision != p.CurrentContextRevision || input.TargetRevision != p.CurrentContextRevision {
			return operationResult[ImpactTask]{}, ErrVersionConflict
		}
		if slices.Contains(ImpactTaskClosedStatuses, task.Status) || task.Status == "STALE" {
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
		if err := recordAuditEvent(tx, actor, projectID, "impact_task."+input.Action, taskID, map[string]any{"status": task.Status, "target_revision": task.TargetRevision, "closure_evidence": task.ClosureEvidence}); err != nil {
			return operationResult[ImpactTask]{}, err
		}
		return operationResult[ImpactTask]{Value: task, Status: operationStatusOK}, nil
	})
}

// impactTaskVersionNeutralActions are the actions that do not work a task
// against the version it was raised against.
//
// "complete" is the re-targeting action: it validates and records caller-supplied
// closure evidence for the current version, so a replaced version is expected
// there. "assign" only names a responsible person and "wait" only parks the task
// while a human decision is outstanding; neither engages the recorded version, so
// staling them would let a non-committal action destroy work that is still
// perfectly dischargeable. "start" is deliberately NOT here: starting work
// against a version that no longer exists is meaningless, so it must stale.
var impactTaskVersionNeutralActions = []string{"complete", "assign", "wait"}

// impactTaskStaleReason decides, against current domain truth, whether the
// basis an impact task was raised against still holds. It returns "" when the
// task can still be worked, otherwise a stable reason code.
//
// The triggers are deliberately distinct so a consumer can tell them apart
// without re-deriving them:
//   - the task belongs to a superseded context revision,
//   - a source backing the target is no longer readable by the responsible actor,
//   - the assignee lost the capability or scope needed to act,
//   - the version the task names was replaced.
//
// Target-version replacement is deliberately not a trigger for "complete":
// completion is exactly the action that re-targets a task, because it validates
// and records caller-supplied closure evidence for the current version. Applying
// the rule there would destroy valid work.
//
// "assign" is excluded for the same reason from the other side: naming a
// responsible person does not work the task against its recorded version at all,
// so a replaced version says nothing about whether the delegation is still
// meaningful. Without this carve-out the ordinary owner flow (assign, then
// complete against the current version) marked the task STALE on the assign and
// could never close it, which left a change that invalidates chapter
// confirmations unable to reach STABILIZED at all.
func (s *Service) impactTaskStaleReason(ctx context.Context, tx Transaction, actor Actor, p Project, task ImpactTask, action string) string {
	if task.TargetRevision != p.CurrentContextRevision {
		// The task belongs to a superseded context revision; every fact it was
		// assessed against has moved on.
		return ImpactTaskStaleTargetReplaced
	}
	// A responsible actor whose access to the target's sources was revoked can
	// no longer produce a trustworthy result for this task.
	ids, err := impactSourceIDsForRefs(tx, p, []ObjectVersionRef{task.Target})
	if err == nil && s.checkSources(ctx, actor, p.ID, ids) != nil {
		return ImpactTaskStaleSourceRevoked
	}
	// An assignee that lost project write scope cannot discharge the task.
	if task.AssigneeID != "" && task.AssigneeID != actor.UserID {
		recipient := actor
		recipient.UserID = task.AssigneeID
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(recipient, p.ID, AuthorizationWrite, "")); err != nil {
			return ImpactTaskStaleAssigneeRevoked
		}
	}
	// "assign" and "wait" are exempt for the same reason from the other side:
	// neither names a responsible person nor parks a task "working" against its
	// recorded version, so a replaced version says nothing about whether they are
	// still meaningful. Without these carve-outs the ordinary owner flows
	// (assign then complete, suspend then resume then complete) marked the task
	// STALE on a non-committal action and could never close it, which left a
	// change that invalidates chapter confirmations unable to reach STABILIZED at
	// all.
	if reason := staleTargetReason(tx, p, task.Target); reason != "" && !slices.Contains(impactTaskVersionNeutralActions, action) {
		// A version replacement means the task can no longer be worked as
		// recorded, so it is invalidated with an explicit reason.
		return reason
	}
	return ""
}

// impactConfirmationBlockers re-reads the CURRENT confirmation of every object
// the change set's persisted impact assessment recorded as impacted, and reports
// each one that is no longer confirmed at its current version.
//
// Why this exists: the task loop above only re-reads closure evidence attached to
// DONE tasks. A task that went STALE is deliberately neither a blocker nor
// closure (#200) - its basis is gone, so no actor can ever discharge it - which
// left its impacted object with no obligation at all. Nothing blocked on it and
// nothing checked it, so an impacted chapter could be left unconfirmed while
// stabilization still reported STABILIZED. Reading the assessment closes that
// gap without making STALE blocking again, which would be an unresolvable
// deadlock.
//
// The re-read is deliberately scoped to the assessment's own objects and skipped
// for any object a DONE task re-targeted: completing a task legitimately moves
// it onto a newer version, so the assessed version is expected to be stale there
// and the task's own closure check already covers the current one.
func impactConfirmationBlockers(tx Transaction, p Project, store impactTransaction, changeID string) []ImpactBlocker {
	assessment, err := store.ImpactAssessment(p.ID, changeID)
	if err != nil {
		// A change set with no stored assessment has no impacted object set to
		// re-read; the task, route, template and rule checks still apply.
		return nil
	}
	tasks, err := store.ImpactTasks(p.ID)
	if err != nil {
		return nil
	}
	// An object is accounted for when a DONE task of this change set carries it
	// as closure evidence: that task already verified the current confirmation.
	accountedFor := map[string]bool{}
	for _, task := range tasks {
		if task.ChangeSetID != changeID || task.Status != "DONE" {
			continue
		}
		for _, ref := range task.ClosureEvidence {
			accountedFor[ref.Kind+"/"+ref.ID] = true
		}
	}
	blockers := []ImpactBlocker{}
	for _, object := range assessment.Objects {
		identity := object.Ref.Kind + "/" + object.Ref.ID
		if accountedFor[identity] {
			continue
		}
		// The spec revision recorded by the assessment is the revision the change
		// set was assessed against, and applying that very change set advances the
		// spec. Treating that expected step as drift would block every stabilization,
		// so the spec root is judged on its CURRENT confirmation instead of its
		// assessed version.
		if object.Ref.Kind == "ProjectSpec" {
			if _, err := currentImpactObject(tx, p, ObjectVersionRef{Kind: "ProjectSpec", ID: object.Ref.ID,
				Version: strconv.FormatInt(p.SpecRevision, 10)}); err != nil {
				blockers = append(blockers, ImpactBlocker{Code: "impact_object_stale", TargetID: object.Ref.ID,
					Reason: "受影响对象的版本已被替换，需重新评估影响"})
			}
			continue
		}
		// Re-read against current domain truth rather than the stored snapshot:
		// only the CURRENT version of the object decides, so a re-targeted object
		// is judged on the version that is live now.
		state, err := currentImpactObject(tx, p, assessmentObjectsCurrent(tx, p, object.Ref))
		if err != nil {
			// The object is unreadable at any current version. A task that
			// legitimately moved the object on would have accounted for it above,
			// so this is real drift worth reporting rather than a silent pass.
			blockers = append(blockers, ImpactBlocker{Code: "impact_object_stale", TargetID: object.Ref.ID,
				Reason: "受影响对象的版本已被替换，需重新评估影响"})
			continue
		}
		if !state.Confirmed {
			blockers = append(blockers, ImpactBlocker{Code: "impact_object_unconfirmed", TargetID: object.Ref.ID,
				Reason: "受影响对象的当前确认已失效，需重新确认"})
		}
	}
	return blockers
}

// assessmentObjectsCurrent resolves the version an impacted object should be
// judged at. The assessed version is used when it is still current. When the
// object has since been re-targeted, the chapter's live current version is
// resolved from domain truth, because an impacted object that was repaired and
// re-confirmed must be judged on the version that exists now rather than on the
// replaced one it was originally assessed against.
func assessmentObjectsCurrent(tx Transaction, p Project, ref ObjectVersionRef) ObjectVersionRef {
	if ref.Kind != "ChapterVersion" {
		// For every other kind the recorded version is the identity; a semantic
		// object is replaced by writing a new version, never by silently
		// re-pointing the old one.
		return ref
	}
	chapter, err := tx.Chapter(p.ID, ref.ID)
	if err != nil || chapter.CurrentVersionID == nil {
		return ref
	}
	return ObjectVersionRef{Kind: "ChapterVersion", ID: ref.ID, Version: *chapter.CurrentVersionID}
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
			// Only closed tasks contribute closure evidence. A STALE task
			// contributed nothing, so feeding its evidence into the write guard
			// would only reintroduce the invalidation the task already records.
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
		result := StabilizationResult{ChangeSetID: changeID, ContextRevision: p.CurrentContextRevision, Status: "BLOCKED", Blockers: []ImpactBlocker{}, StaleTaskIDs: []string{}, CheckedAt: time.Now().UTC()}
		store := tx.(impactTransaction)
		tasks, err := store.ImpactTasks(projectID)
		if err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		for _, task := range tasks {
			if task.ChangeSetID != changeID || task.TargetRevision != p.CurrentContextRevision {
				continue
			}
			// A STALE task records an invalidated obligation. It must not block
			// (the basis it was raised against no longer exists, so no actor can
			// ever discharge it) and it must not count as closure. It is
			// reported explicitly instead of being dropped.
			if task.Status == "STALE" {
				result.StaleTaskIDs = append(result.StaleTaskIDs, task.ID)
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
						// Re-checked before closure: the version this result was
						// produced against has been replaced, so the result no
						// longer holds. Record the invalidation as an explicit
						// STALE transition instead of only reporting a blocker, so
						// the task stops claiming to be DONE and stays explainable.
						if err != nil {
							if _, markErr := s.markImpactTaskStale(tx, actor, p, store, task, ImpactTaskStaleTargetReplaced); markErr != nil {
								return operationResult[StabilizationResult]{}, markErr
							}
							result.StaleTaskIDs = append(result.StaleTaskIDs, task.ID)
							continue
						}
						result.Blockers = append(result.Blockers, ImpactBlocker{Code: "closure_stale", TargetID: task.ID, Reason: "闭环证据已变化，需重新处理"})
					}
				}
			}
		}
		result.Blockers = append(result.Blockers, impactConfirmationBlockers(tx, p, store, changeID)...)
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
		if err := recordAuditEvent(tx, actor, projectID, "revision.stabilize", changeID, map[string]any{"status": result.Status, "blockers": result.Blockers}); err != nil {
			return operationResult[StabilizationResult]{}, err
		}
		return operationResult[StabilizationResult]{Value: result, Status: operationStatusOK}, nil
	})
}

func (s *Service) CreateChangeDraft(ctx context.Context, actor Actor, projectID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
	if err := validateExtendedChangeInput(input); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, sourceOperationInput(operationCreateChangeDraft, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[Transaction], s.createChangeDraftMutation(ctx, actor, projectID, input))
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
		if err := validateChangeTemplateFields(p, change.Fields); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := AssessChangeImpact(tx, p, change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		next := change
		next.Status = "assessed"
		if err := store.UpdateChangeSet(change, next); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "change_set.assess", changeID, nil); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		return operationResult[ChangeSet]{Value: next, Status: operationStatusOK}, nil
	})
}
