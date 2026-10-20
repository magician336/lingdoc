package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The versioned route-roadmap editing session (#250).
//
// This module owns "how the roadmap is written and how its versions are
// counted". A parallel RouteNode write endpoint would put that knowledge in
// two places, and the two version semantics would drift apart, so the module
// is the only writer of the roadmap it owns - and it writes route NODES
// through the same AppendRouteNode seam the ChangeSet semantic-delta path
// uses.
//
// Two commit paths exist, and they are the acceptance criteria's split:
//
//   - a text edit of an existing node (name, status, inputs, targets) freezes
//     a NEW route node version in the same transaction that creates the
//     impact tasks for the objects that referenced the old version;
//   - anything semantic (adding or deleting a node, changing the prerequisite
//     relations of a node, changing a route edge) produces a ChangeSet
//     instead of being written to the roadmap directly.
//
// The unfrozen editing session never touches the frozen tables. That is what
// makes "the export only reads frozen versions" structural rather than a
// filter somebody can forget, and it is why cancelling a session restores the
// previous frozen version by construction.

const (
	operationBeginRoadmapEdit   operationName = "beginRoadmapEdit"
	operationProposeRoadmapNode operationName = "proposeRoadmapNode"
	operationProposeRoadmapEdge operationName = "proposeRoadmapEdge"
	operationCommitRoadmapEdit  operationName = "commitRoadmapEdit"
	operationCancelRoadmapEdit  operationName = "cancelRoadmapEdit"
)

// ImpactTaskStaleCancelledVersion marks a task whose target or closure
// evidence pointed at a route version that was never frozen because the
// editing session it belonged to was cancelled.
const ImpactTaskStaleCancelledVersion = "cancelled_temporary_version"

// BeginRoadmapEdit opens an unfrozen editing session bound to the current
// frozen roadmap.
func (s *Service) BeginRoadmapEdit(ctx context.Context, actor Actor, projectID, key string, input RoadmapEditBeginInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedContextRevision < 1 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, operationInput(operationBeginRoadmapEdit, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RoadmapEditSession], error) {
		if p.Status != "active" || p.CurrentContextRevision != input.ExpectedContextRevision {
			return operationResult[RoadmapEditSession]{}, ErrVersionConflict
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		sessions, err := store.RoadmapSessions(projectID)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		for _, session := range sessions {
			if session.Status == RoadmapEditSessionOpen {
				// One unfrozen session per project. The database enforces the
				// same rule with a partial unique index; this check is the
				// readable half of it.
				return operationResult[RoadmapEditSession]{}, ErrInvalidState
			}
		}
		baseline, err := roadmapBaselineOf(tx, p, roadmapVersionOf(sessions))
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		now := time.Now().UTC()
		session := RoadmapEditSession{
			ID:            newRoadmapSessionID(),
			ProjectID:     projectID,
			Status:        RoadmapEditSessionOpen,
			Baseline:      baseline,
			NodeProposals: []RoadmapNodeProposal{},
			EdgeProposals: []RoadmapEdgeProposal{},
			ActorID:       actor.UserID,
			Reason:        input.Reason,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := store.InsertRoadmapSession(session); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "roadmap.edit.begin", session.ID, map[string]any{"baseline_version": baseline.Version, "context_revision": baseline.ContextRevision, "reason": input.Reason}); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		return operationResult[RoadmapEditSession]{Value: session, Status: operationStatusCreated}, nil
	})
}

// ProposeRoadmapNode records a proposed route node change inside the open
// session. The frozen roadmap is not touched.
func (s *Service) ProposeRoadmapNode(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapNodeProposalInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, operationInput(operationProposeRoadmapNode, projectID+"/"+sessionID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RoadmapEditSession], error) {
		store, ok := tx.(roadmapStore)
		if !ok {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if session.Status != RoadmapEditSessionOpen {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		proposal, err := s.buildRoadmapNodeProposal(tx, p, session, input)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		next := session
		next.NodeProposals = replaceRoadmapNodeProposal(session.NodeProposals, proposal)
		next.UpdatedAt = time.Now().UTC()
		if err := store.UpdateRoadmapSession(session, next); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "roadmap.edit.propose.node", session.ID, map[string]any{"node_id": proposal.NodeID, "kind": proposal.Kind, "temporary_version": proposal.TemporaryVersion}); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		return operationResult[RoadmapEditSession]{Value: next, Status: operationStatusOK}, nil
	})
}

// ProposeRoadmapEdge records a proposed route edge change inside the open
// session. A route edge is a semantic change, so it is never written to the
// frozen roadmap directly; it becomes part of the ChangeSet the commit
// produces.
func (s *Service) ProposeRoadmapEdge(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapEdgeProposalInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, operationInput(operationProposeRoadmapEdge, projectID+"/"+sessionID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RoadmapEditSession], error) {
		store, ok := tx.(roadmapStore)
		if !ok {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if session.Status != RoadmapEditSessionOpen {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		proposal, err := s.buildRoadmapEdgeProposal(tx, p, session, input)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		next := session
		next.EdgeProposals = replaceRoadmapEdgeProposal(session.EdgeProposals, proposal)
		next.UpdatedAt = time.Now().UTC()
		if err := store.UpdateRoadmapSession(session, next); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "roadmap.edit.propose.edge", session.ID, map[string]any{"edge_id": proposal.EdgeID, "kind": proposal.Kind, "temporary_version": proposal.TemporaryVersion}); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		return operationResult[RoadmapEditSession]{Value: next, Status: operationStatusOK}, nil
	})
}

// CommitRoadmapEdit freezes the session.
//
// A commit that only edits the text of existing route nodes freezes new route
// node versions and creates the impact tasks for the objects that referenced
// the old versions, all in one transaction. A commit that contains a semantic
// change produces a ChangeSet instead; the roadmap is not written directly.
func (s *Service) CommitRoadmapEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCommitInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedBaselineVersion < 0 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	var semantic bool
	var deltas []SemanticDelta
	var contextRevision int64
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, ""))
		if err != nil {
			return err
		}
		contextRevision = p.CurrentContextRevision
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return err
		}
		if session.Status != RoadmapEditSessionOpen {
			return ErrInvalidState
		}
		if session.Baseline.Version != input.ExpectedBaselineVersion {
			return ErrVersionConflict
		}
		if err := verifyRoadmapBaseline(tx, p, session); err != nil {
			return err
		}
		semantic, deltas, err = roadmapCommitPlan(tx, p, session)
		return err
	})
	if err != nil {
		return nil, 0, false, err
	}
	if semantic {
		return s.commitRoadmapSemanticEdit(ctx, actor, projectID, sessionID, key, input, deltas, contextRevision)
	}
	return s.commitRoadmapFrozenEdit(ctx, actor, projectID, sessionID, key, input)
}

// commitRoadmapFrozenEdit freezes new route node versions and the impact
// tasks they cause, in one transaction.
func (s *Service) commitRoadmapFrozenEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCommitInput) (json.RawMessage, int, bool, error) {
	return runOperation(s, ctx, actor, operationInput(operationCommitRoadmapEdit, projectID+"/"+sessionID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RoadmapCommitResult], error) {
		store, ok := tx.(roadmapStore)
		if !ok {
			return operationResult[RoadmapCommitResult]{}, ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return operationResult[RoadmapCommitResult]{}, err
		}
		if session.Status != RoadmapEditSessionOpen {
			return operationResult[RoadmapCommitResult]{}, ErrInvalidState
		}
		if session.Baseline.Version != input.ExpectedBaselineVersion {
			return operationResult[RoadmapCommitResult]{}, ErrVersionConflict
		}
		if err := verifyRoadmapBaseline(tx, p, session); err != nil {
			return operationResult[RoadmapCommitResult]{}, err
		}
		now := time.Now().UTC()
		applied := []RoadmapNodeView{}
		for _, proposal := range session.NodeProposals {
			old, err := routeNodeOf(tx, projectID, proposal.NodeID)
			if err != nil {
				return operationResult[RoadmapCommitResult]{}, err
			}
			next := RouteNode{
				ID:              old.ID,
				ProjectID:       projectID,
				Version:         old.Version + 1,
				Name:            proposal.Name,
				Inputs:          proposal.Inputs,
				Targets:         proposal.Targets,
				Required:        routeRequiredByRules(p, old.ID),
				Status:          proposal.Status,
				ContextRevision: p.CurrentContextRevision,
				ActorID:         actor.UserID,
				Reason:          proposal.Reason,
				UpdatedAt:       now,
			}
			if err := appendFrozenRouteNode(tx, old, next); err != nil {
				return operationResult[RoadmapCommitResult]{}, err
			}
			if err := s.createRoadmapImpactTasks(tx, actor, p, session, old, next); err != nil {
				return operationResult[RoadmapCommitResult]{}, err
			}
			applied = append(applied, RoadmapNodeView{
				ID: next.ID, ProjectID: next.ProjectID, Version: next.Version, Name: next.Name,
				Inputs: next.Inputs, Targets: next.Targets, Required: next.Required, Status: next.Status,
				State: RoadmapNodeStateCurrent, ContextRevision: next.ContextRevision, ActorID: next.ActorID,
				Reason: next.Reason, UpdatedAt: next.UpdatedAt, Frozen: true,
			})
		}
		result := RoadmapCommitResult{
			SessionID:        session.ID,
			BaselineVersion:  session.Baseline.Version,
			CommittedVersion: session.Baseline.Version + 1,
			AppliedNodes:     applied,
			AppliedEdges:     []RouteEdge{},
		}
		closed := session
		closed.Status = RoadmapEditSessionCommitted
		closed.Result = &result
		closed.UpdatedAt = now
		if err := store.UpdateRoadmapSession(session, closed); err != nil {
			return operationResult[RoadmapCommitResult]{}, err
		}
		// The frozen roadmap changed, so the project moves to a new context
		// revision. The context revision snapshot is recorded in the same
		// transaction, so a failed commit leaves no half-applied revision.
		if len(applied) > 0 {
			recorder, ok := tx.(gormTransaction)
			if !ok {
				return operationResult[RoadmapCommitResult]{}, ErrInvalidState
			}
			if err := recordContextRevisionSnapshot(recorder, p, ChangeSet{ID: "roadmap-" + session.ID, ProjectID: projectID}); err != nil {
				return operationResult[RoadmapCommitResult]{}, err
			}
			next := p
			next.CurrentContextRevision = p.CurrentContextRevision + 1
			next.ProjectVersion = p.ProjectVersion + 1
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return operationResult[RoadmapCommitResult]{}, err
			}
		}
		if err := recordAuditEvent(tx, actor, projectID, "roadmap.edit.commit", session.ID, map[string]any{"baseline_version": result.BaselineVersion, "committed_version": result.CommittedVersion, "nodes": len(applied)}); err != nil {
			return operationResult[RoadmapCommitResult]{}, err
		}
		return operationResult[RoadmapCommitResult]{Value: result, Status: operationStatusOK}, nil
	})
}

// commitRoadmapSemanticEdit turns the session into a ChangeSet. Nothing is
// written to the frozen roadmap: a semantic change belongs to the project's
// change process, which assesses it and applies it.
func (s *Service) commitRoadmapSemanticEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCommitInput, deltas []SemanticDelta, contextRevision int64) (json.RawMessage, int, bool, error) {
	raw, _, _, err := s.CreateChangeDraft(ctx, actor, projectID, key+"-changeset", ExtendedChangeSetInput{
		ExpectedContextRevision: contextRevision,
		Reason:                  input.Reason,
		SemanticDeltas:          deltas,
	})
	if err != nil {
		return nil, 0, false, err
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		return nil, 0, false, err
	}
	result := RoadmapCommitResult{
		SessionID:        sessionID,
		CommittedVersion: change.BaseContextRevision,
		AppliedNodes:     []RoadmapNodeView{},
		AppliedEdges:     []RouteEdge{},
		ChangeSetID:      change.ID,
		Semantic:         true,
	}
	err = s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return err
		}
		if session.Status != RoadmapEditSessionOpen {
			return ErrInvalidState
		}
		result.BaselineVersion = session.Baseline.Version
		result.CommittedVersion = session.Baseline.Version + 1
		closed := session
		closed.Status = RoadmapEditSessionCommitted
		closed.Result = &result
		closed.UpdatedAt = time.Now().UTC()
		if err := store.UpdateRoadmapSession(session, closed); err != nil {
			return err
		}
		return recordAuditEvent(tx, actor, projectID, "roadmap.edit.commit", session.ID, map[string]any{"change_set_id": change.ID, "semantic": true, "reason": input.Reason})
	})
	if err != nil {
		return nil, 0, false, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, 0, false, err
	}
	return body, 200, false, nil
}

// CancelRoadmapEdit discards the unfrozen session.
//
// The frozen roadmap was never touched, so the roadmap is already at the
// baseline version. What the cancellation has to guarantee is that nothing
// keeps pointing at the temporary versions the session proposed: impact tasks
// and closure evidence that name them are invalidated and unbound in the same
// transaction that closes the session.
func (s *Service) CancelRoadmapEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCancelInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedBaselineVersion < 0 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, operationInput(operationCancelRoadmapEdit, projectID+"/"+sessionID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[RoadmapEditSession], error) {
		store, ok := tx.(roadmapStore)
		if !ok {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		session, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if session.Status != RoadmapEditSessionOpen {
			return operationResult[RoadmapEditSession]{}, ErrInvalidState
		}
		if session.Baseline.Version != input.ExpectedBaselineVersion {
			return operationResult[RoadmapEditSession]{}, ErrVersionConflict
		}
		temporary := roadmapTemporaryRefs(session)
		if err := s.releaseRoadmapTemporaryRefs(tx, actor, p, temporary); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		closed := session
		closed.Status = RoadmapEditSessionCancelled
		closed.UpdatedAt = time.Now().UTC()
		if err := store.UpdateRoadmapSession(session, closed); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		if err := recordAuditEvent(tx, actor, projectID, "roadmap.edit.cancel", session.ID, map[string]any{"baseline_version": session.Baseline.Version, "released_refs": len(temporary), "reason": input.Reason}); err != nil {
			return operationResult[RoadmapEditSession]{}, err
		}
		return operationResult[RoadmapEditSession]{Value: closed, Status: operationStatusOK}, nil
	})
}

// releaseRoadmapTemporaryRefs invalidates every impact task that points at a
// temporary version and unbinds closure evidence that names one.
//
// A cancelled temporary version never existed as a frozen version, so an
// obligation raised against it can never be discharged: leaving the task open
// would deadlock the project, and leaving the evidence bound would let a
// later reader believe the cancelled version was confirmed.
func (s *Service) releaseRoadmapTemporaryRefs(tx Transaction, actor Actor, p Project, temporary []ObjectVersionRef) error {
	if len(temporary) == 0 {
		return nil
	}
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	tasks, err := store.ImpactTasks(p.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, task := range tasks {
		if task.Status == "STALE" || task.Status == "DONE" || task.Status == "DISMISSED" {
			continue
		}
		pointsAtTemporary := slices.Contains(temporary, task.Target)
		evidence := []ObjectVersionRef{}
		changed := false
		for _, ref := range task.ClosureEvidence {
			if slices.Contains(temporary, ref) {
				changed = true
				continue
			}
			evidence = append(evidence, ref)
		}
		if !pointsAtTemporary && !changed {
			continue
		}
		next := task
		next.Status = "STALE"
		next.StaleReason = ImpactTaskStaleCancelledVersion
		next.StaleAt = now
		next.UpdatedAt = now
		next.Version = task.Version + 1
		next.ClosureEvidence = evidence
		if err := store.UpdateImpactTask(task, next); err != nil {
			return err
		}
		if err := recordAuditEvent(tx, actor, p.ID, "roadmap.edit.release_task", task.ID, map[string]any{"reason": ImpactTaskStaleCancelledVersion}); err != nil {
			return err
		}
	}
	return nil
}

// ReadRoadmap returns the current frozen roadmap. Unfrozen proposals are not
// part of it: they live in the editing session.
func (s *Service) ReadRoadmap(ctx context.Context, actor Actor, projectID string) (RoadmapBaseline, error) {
	var baseline RoadmapBaseline
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		sessions, err := store.RoadmapSessions(projectID)
		if err != nil {
			return err
		}
		baseline, err = roadmapBaselineOf(tx, p, roadmapVersionOf(sessions))
		return err
	})
	if err != nil {
		return RoadmapBaseline{}, err
	}
	return baseline, nil
}

// ReadRoadmapNodeVersions returns the frozen version sequence of one route
// node.
func (s *Service) ReadRoadmapNodeVersions(ctx context.Context, actor Actor, projectID, nodeID string) ([]RoadmapVersionEntry, error) {
	if strings.TrimSpace(nodeID) == "" {
		return nil, ErrInvalidRequest
	}
	entries := []RoadmapVersionEntry{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		versions, err := store.RouteNodeVersions(projectID, nodeID)
		if err != nil {
			return err
		}
		for _, node := range versions {
			state, reason := roadmapNodeState(tx, p, node)
			entries = append(entries, RoadmapVersionEntry{
				Version: node.Version, Name: node.Name, Status: node.Status,
				Required: routeRequiredByRules(p, node.ID), State: state, StateReason: reason,
				ContextRevision: node.ContextRevision, ActorID: node.ActorID, Reason: node.Reason,
				Inputs: node.Inputs, Targets: node.Targets, UpdatedAt: node.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// CompareRoadmapNodeVersions compares two frozen versions of one route node.
func (s *Service) CompareRoadmapNodeVersions(ctx context.Context, actor Actor, projectID, nodeID string, from, to int64) (RoadmapComparison, error) {
	if strings.TrimSpace(nodeID) == "" || from < 1 || to < 1 {
		return RoadmapComparison{}, ErrInvalidRequest
	}
	comparison := RoadmapComparison{Kind: "RouteNode", ID: nodeID, From: from, To: to, Changes: []RoadmapFieldChange{}}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		versions, err := store.RouteNodeVersions(projectID, nodeID)
		if err != nil {
			return err
		}
		before, after, found := twoRouteNodeVersions(versions, from, to)
		if !found {
			return ErrNotFound
		}
		comparison.Changes = roadmapNodeChanges(before, after)
		return nil
	})
	if err != nil {
		return RoadmapComparison{}, err
	}
	return comparison, nil
}

// ReadRouteEdges returns the current frozen route edges.
func (s *Service) ReadRouteEdges(ctx context.Context, actor Actor, projectID string) ([]RouteEdge, error) {
	edges := []RouteEdge{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		all, err := store.RouteEdges(projectID)
		if err != nil {
			return err
		}
		for _, edge := range all {
			if edge.Status == RouteEdgeStatusRetired {
				continue
			}
			edges = append(edges, edge)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return edges, nil
}

// ReadRouteEdgeVersions returns the version sequence of one route edge.
func (s *Service) ReadRouteEdgeVersions(ctx context.Context, actor Actor, projectID, edgeID string) ([]RouteEdge, error) {
	if strings.TrimSpace(edgeID) == "" {
		return nil, ErrInvalidRequest
	}
	edges := []RouteEdge{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		versions, err := store.RouteEdgeVersions(projectID, edgeID)
		if err != nil {
			return err
		}
		edges = append(edges, versions...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return edges, nil
}

// ListRoadmapSessions returns the editing sessions of a project.
func (s *Service) ListRoadmapSessions(ctx context.Context, actor Actor, projectID string) ([]RoadmapEditSession, error) {
	sessions := []RoadmapEditSession{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		all, err := store.RoadmapSessions(projectID)
		if err != nil {
			return err
		}
		sessions = append(sessions, all...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetRoadmapSession returns one editing session.
func (s *Service) GetRoadmapSession(ctx context.Context, actor Actor, projectID, sessionID string) (RoadmapEditSession, error) {
	var session RoadmapEditSession
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(roadmapStore)
		if !ok {
			return ErrInvalidState
		}
		found, err := store.RoadmapSession(projectID, sessionID)
		if err != nil {
			return err
		}
		session = found
		return nil
	})
	if err != nil {
		return RoadmapEditSession{}, err
	}
	return session, nil
}

// buildRoadmapNodeProposal validates a proposed node change against the
// frozen roadmap and the template rules.
func (s *Service) buildRoadmapNodeProposal(tx Transaction, p Project, session RoadmapEditSession, input RoadmapNodeProposalInput) (RoadmapNodeProposal, error) {
	if strings.TrimSpace(input.NodeID) == "" || len(input.NodeID) > 128 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 {
		return RoadmapNodeProposal{}, ErrInvalidRequest
	}
	if !slices.Contains([]string{RoadmapNodeProposalAdd, RoadmapNodeProposalUpdate, RoadmapNodeProposalDelete}, input.Kind) {
		return RoadmapNodeProposal{}, ErrInvalidRequest
	}
	required := routeRequiredByRules(p, input.NodeID)
	if input.Required != nil && *input.Required != required {
		// The required flag is declared by the template rules. A client that
		// states it is either right by accident or wrong on purpose; both are
		// refused, because the flag has exactly one owner.
		return RoadmapNodeProposal{}, ErrInvalidRequest
	}
	current, err := routeNodeOf(tx, p.ID, input.NodeID)
	exists := err == nil
	if err != nil && err != ErrNotFound {
		return RoadmapNodeProposal{}, err
	}
	proposal := RoadmapNodeProposal{NodeID: input.NodeID, Kind: input.Kind, Reason: input.Reason}
	switch input.Kind {
	case RoadmapNodeProposalAdd:
		if exists {
			return RoadmapNodeProposal{}, ErrInvalidState
		}
		if strings.TrimSpace(input.Name) == "" || len(input.Name) > 240 || !slices.Contains(RoadmapNodeStatuses, input.Status) {
			return RoadmapNodeProposal{}, ErrInvalidRequest
		}
		proposal.Name = input.Name
		proposal.Status = input.Status
		proposal.Inputs = input.Inputs
		proposal.Targets = input.Targets
		proposal.TemporaryVersion = 1
	case RoadmapNodeProposalUpdate:
		if !exists {
			return RoadmapNodeProposal{}, ErrNotFound
		}
		if strings.TrimSpace(input.Name) == "" || len(input.Name) > 240 || !slices.Contains(RoadmapNodeStatuses, input.Status) {
			return RoadmapNodeProposal{}, ErrInvalidRequest
		}
		if len(input.Inputs) > 100 || len(input.Targets) > 100 {
			return RoadmapNodeProposal{}, ErrInvalidRequest
		}
		for _, ref := range append(append([]ObjectVersionRef{}, input.Inputs...), input.Targets...) {
			if !validObjectRef(ref) {
				return RoadmapNodeProposal{}, ErrInvalidRequest
			}
			if ref.Kind == "RouteNode" && ref.ID == input.NodeID {
				return RoadmapNodeProposal{}, ErrInvalidRequest
			}
		}
		proposal.Name = input.Name
		proposal.Status = input.Status
		proposal.Inputs = input.Inputs
		proposal.Targets = input.Targets
		proposal.TemporaryVersion = current.Version + 1
	case RoadmapNodeProposalDelete:
		if !exists {
			return RoadmapNodeProposal{}, ErrNotFound
		}
		if required {
			// A node the template rules declare as required cannot leave the
			// roadmap: the required route would be missing afterwards.
			return RoadmapNodeProposal{}, ErrInvalidState
		}
		proposal.Name = current.Name
		proposal.Status = "retired"
		proposal.TemporaryVersion = current.Version + 1
	}
	return proposal, nil
}

// buildRoadmapEdgeProposal validates a proposed edge change.
func (s *Service) buildRoadmapEdgeProposal(tx Transaction, p Project, session RoadmapEditSession, input RoadmapEdgeProposalInput) (RoadmapEdgeProposal, error) {
	if strings.TrimSpace(input.EdgeID) == "" || len(input.EdgeID) > 128 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 {
		return RoadmapEdgeProposal{}, ErrInvalidRequest
	}
	if !slices.Contains([]string{RoadmapEdgeProposalAdd, RoadmapEdgeProposalUpdate, RoadmapEdgeProposalDelete}, input.Kind) {
		return RoadmapEdgeProposal{}, ErrInvalidRequest
	}
	store, ok := tx.(roadmapStore)
	if !ok {
		return RoadmapEdgeProposal{}, ErrInvalidState
	}
	current, err := store.RouteEdge(p.ID, input.EdgeID)
	exists := err == nil
	if err != nil && err != ErrNotFound {
		return RoadmapEdgeProposal{}, err
	}
	proposal := RoadmapEdgeProposal{EdgeID: input.EdgeID, Kind: input.Kind, Reason: input.Reason}
	switch input.Kind {
	case RoadmapEdgeProposalAdd:
		if exists {
			return RoadmapEdgeProposal{}, ErrInvalidState
		}
		if strings.TrimSpace(input.SourceID) == "" || strings.TrimSpace(input.TargetID) == "" || input.SourceID == input.TargetID || strings.TrimSpace(input.Relation) == "" || len(input.Relation) > 64 {
			return RoadmapEdgeProposal{}, ErrInvalidRequest
		}
		proposal.SourceID = input.SourceID
		proposal.TargetID = input.TargetID
		proposal.Relation = input.Relation
		proposal.TemporaryVersion = 1
	case RoadmapEdgeProposalUpdate, RoadmapEdgeProposalDelete:
		if !exists {
			return RoadmapEdgeProposal{}, ErrNotFound
		}
		proposal.SourceID = current.SourceID
		proposal.TargetID = current.TargetID
		proposal.Relation = current.Relation
		proposal.TemporaryVersion = current.Version + 1
	}
	for _, id := range []string{proposal.SourceID, proposal.TargetID} {
		if id == "" {
			continue
		}
		if _, err := routeNodeOf(tx, p.ID, id); err != nil {
			return RoadmapEdgeProposal{}, err
		}
	}
	return proposal, nil
}

// roadmapCommitPlan decides which commit path the session takes and, for the
// semantic path, builds the semantic deltas of the ChangeSet.
func roadmapCommitPlan(tx Transaction, p Project, session RoadmapEditSession) (bool, []SemanticDelta, error) {
	deltas := []SemanticDelta{}
	semantic := len(session.EdgeProposals) > 0
	for _, proposal := range session.NodeProposals {
		current, err := routeNodeOf(tx, p.ID, proposal.NodeID)
		if err != nil {
			if proposal.Kind == RoadmapNodeProposalAdd && err == ErrNotFound {
				current = RouteNode{ID: proposal.NodeID, ProjectID: p.ID}
			} else {
				return false, nil, err
			}
		}
		switch proposal.Kind {
		case RoadmapNodeProposalAdd:
			semantic = true
			deltas = append(deltas, SemanticDelta{
				Kind: "RouteNode", TargetID: proposal.NodeID, ExpectedVersion: 0,
				ProposedRoute: &RouteNode{ID: proposal.NodeID, ProjectID: p.ID, Name: proposal.Name, Status: proposal.Status, Inputs: proposal.Inputs, Targets: proposal.Targets, Required: routeRequiredByRules(p, proposal.NodeID)},
			})
		case RoadmapNodeProposalDelete:
			semantic = true
			deltas = append(deltas, SemanticDelta{
				Kind: "RouteNode", TargetID: proposal.NodeID, ExpectedVersion: current.Version,
				ProposedRoute: &RouteNode{ID: proposal.NodeID, ProjectID: p.ID, Name: current.Name, Status: "retired", Inputs: []ObjectVersionRef{}, Targets: []ObjectVersionRef{}, Required: false},
			})
		case RoadmapNodeProposalUpdate:
			if !sameRouteNodePrerequisites(current.Inputs, proposal.Inputs) {
				// Changing the prerequisite relations of a node is a
				// semantic change, not a text edit.
				semantic = true
			}
			deltas = append(deltas, SemanticDelta{
				Kind: "RouteNode", TargetID: proposal.NodeID, ExpectedVersion: current.Version,
				ProposedRoute: &RouteNode{ID: proposal.NodeID, ProjectID: p.ID, Name: proposal.Name, Status: proposal.Status, Inputs: proposal.Inputs, Targets: proposal.Targets, Required: routeRequiredByRules(p, proposal.NodeID)},
			})
		}
	}
	for _, proposal := range session.EdgeProposals {
		edge := &RouteEdge{ID: proposal.EdgeID, ProjectID: p.ID, SourceID: proposal.SourceID, TargetID: proposal.TargetID, Relation: proposal.Relation, Status: RouteEdgeStatusActive}
		expected := int64(0)
		if proposal.Kind == RoadmapEdgeProposalDelete {
			edge.Status = RouteEdgeStatusRetired
		}
		if proposal.Kind != RoadmapEdgeProposalAdd {
			store, ok := tx.(roadmapStore)
			if !ok {
				return false, nil, ErrInvalidState
			}
			current, err := store.RouteEdge(p.ID, proposal.EdgeID)
			if err != nil {
				return false, nil, err
			}
			expected = current.Version
		}
		deltas = append(deltas, SemanticDelta{Kind: "RouteEdge", TargetID: proposal.EdgeID, ExpectedVersion: expected, ProposedEdge: edge})
	}
	return semantic, deltas, nil
}

// createRoadmapImpactTasks creates the review obligations of the objects
// that referenced the previous version of a route node.
//
// The version advance and these tasks are written in the same transaction:
// a frozen roadmap that points at objects nobody has to re-check would be a
// silent gap.
func (s *Service) createRoadmapImpactTasks(tx Transaction, actor Actor, p Project, session RoadmapEditSession, old, next RouteNode) error {
	store, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	edges, err := store.DependencyEdges(p.ID)
	if err != nil {
		return err
	}
	oldRef := ObjectVersionRef{Kind: "RouteNode", ID: old.ID, Version: strconv.FormatInt(old.Version, 10)}
	now := time.Now().UTC()
	for _, edge := range edges {
		if edge.Source != oldRef {
			continue
		}
		task := ImpactTask{
			ID:                   ImpactTaskID(p.ID, "roadmap-"+session.ID, edge.Target, p.CurrentContextRevision, "REVIEW_REQUIRED"),
			ProjectID:            p.ID,
			ChangeSetID:          "roadmap-" + session.ID,
			Target:               edge.Target,
			TargetRevision:       p.CurrentContextRevision,
			Action:               "REVIEW_REQUIRED",
			Severity:             DeriveImpactSeverity("REVIEW_REQUIRED"),
			ResponsibilityDomain: "B",
			Status:               "OPEN",
			Causes:               []ImpactCause{{EdgeID: edge.ID, Source: edge.Source, Reason: "路线图节点版本推进到 v" + strconv.FormatInt(next.Version, 10)}},
			Version:              1,
			ClosureEvidence:      []ObjectVersionRef{},
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if err := store.InsertImpactTask(task); err != nil {
			return err
		}
		if err := store.InsertImpactHandoff(task); err != nil {
			return err
		}
	}
	return nil
}

// roadmapBaselineOf reads the frozen roadmap a session binds itself to.
func roadmapBaselineOf(tx Transaction, p Project, version int64) (RoadmapBaseline, error) {
	store, ok := tx.(roadmapStore)
	if !ok {
		return RoadmapBaseline{}, ErrInvalidState
	}
	nodes, err := routeNodesOf(tx, p.ID)
	if err != nil {
		return RoadmapBaseline{}, err
	}
	edges, err := store.RouteEdges(p.ID)
	if err != nil {
		return RoadmapBaseline{}, err
	}
	views, err := roadmapNodeViews(tx, p, nodes)
	if err != nil {
		return RoadmapBaseline{}, err
	}
	return RoadmapBaseline{Version: version, ContextRevision: p.CurrentContextRevision, Nodes: views, Edges: edges}, nil
}

// roadmapVersionOf counts the committed editing sessions of a project. It is
// the roadmap version a new session binds itself to.
func roadmapVersionOf(sessions []RoadmapEditSession) int64 {
	version := int64(0)
	for _, session := range sessions {
		if session.Status == RoadmapEditSessionCommitted {
			version++
		}
	}
	return version
}

// verifyRoadmapBaseline refuses a commit whose frozen baseline moved under
// the session. Without this check a commit would silently apply proposals
// that were made against a different roadmap.
func verifyRoadmapBaseline(tx Transaction, p Project, session RoadmapEditSession) error {
	current, err := roadmapBaselineOf(tx, p, session.Baseline.Version)
	if err != nil {
		return err
	}
	if current.ContextRevision != session.Baseline.ContextRevision {
		return ErrVersionConflict
	}
	base := map[string]int64{}
	for _, node := range session.Baseline.Nodes {
		base[node.ID] = node.Version
	}
	for _, node := range current.Nodes {
		version, ok := base[node.ID]
		if !ok || version != node.Version {
			return ErrVersionConflict
		}
	}
	baseEdges := map[string]int64{}
	for _, edge := range session.Baseline.Edges {
		baseEdges[edge.ID] = edge.Version
	}
	for _, edge := range current.Edges {
		version, ok := baseEdges[edge.ID]
		if !ok || version != edge.Version {
			return ErrVersionConflict
		}
	}
	return nil
}

// roadmapNodeViews turns the stored route nodes into the read model.
func roadmapNodeViews(tx Transaction, p Project, nodes []RouteNode) ([]RoadmapNodeView, error) {
	views := []RoadmapNodeView{}
	for _, node := range nodes {
		state, reason := roadmapNodeState(tx, p, node)
		views = append(views, RoadmapNodeView{
			ID: node.ID, ProjectID: node.ProjectID, Version: node.Version, Name: node.Name,
			Inputs: node.Inputs, Targets: node.Targets, Required: routeRequiredByRules(p, node.ID),
			Status: node.Status, State: state, StateReason: reason, ContextRevision: node.ContextRevision,
			ActorID: node.ActorID, Reason: node.Reason, UpdatedAt: node.UpdatedAt, Frozen: true,
		})
	}
	return views, nil
}

// roadmapNodeState derives the state of a route node from the frozen
// roadmap: 待复核 (pending review) and 版本落后 (stale) are states, not
// guesses about the user's intent.
func roadmapNodeState(tx Transaction, p Project, node RouteNode) (string, string) {
	switch node.Status {
	case "retired":
		return RoadmapNodeStateRetired, "节点已退出路线"
	case "ready":
	default:
		return RoadmapNodeStatePendingReview, "节点尚未复核"
	}
	for _, ref := range node.Inputs {
		state, err := currentImpactObject(tx, p, ref)
		if err != nil {
			return RoadmapNodeStateStale, "引用的 " + ref.Kind + "/" + ref.ID + " 当前不可读"
		}
		if state.Ref.Version != ref.Version {
			return RoadmapNodeStateStale, "引用的 " + ref.Kind + "/" + ref.ID + " 已从 v" + ref.Version + " 变为 v" + state.Ref.Version
		}
	}
	return RoadmapNodeStateCurrent, ""
}

// roadmapTemporaryRefs lists the versions the session proposed but never
// froze.
func roadmapTemporaryRefs(session RoadmapEditSession) []ObjectVersionRef {
	refs := []ObjectVersionRef{}
	for _, proposal := range session.NodeProposals {
		refs = append(refs, ObjectVersionRef{Kind: "RouteNode", ID: proposal.NodeID, Version: strconv.FormatInt(proposal.TemporaryVersion, 10)})
	}
	for _, proposal := range session.EdgeProposals {
		refs = append(refs, ObjectVersionRef{Kind: "RouteEdge", ID: proposal.EdgeID, Version: strconv.FormatInt(proposal.TemporaryVersion, 10)})
	}
	return refs
}

// routeNodesOf reads the frozen route nodes of a project.
func routeNodesOf(tx Transaction, projectID string) ([]RouteNode, error) {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	return store.RouteNodes(projectID)
}

// routeNodeOf reads one frozen route node.
func routeNodeOf(tx Transaction, projectID, nodeID string) (RouteNode, error) {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return RouteNode{}, ErrInvalidState
	}
	return store.RouteNode(projectID, nodeID)
}

// appendFrozenRouteNode writes the next frozen version of a route node through
// the shared seam the ChangeSet semantic-delta path uses.
func appendFrozenRouteNode(tx Transaction, old, next RouteNode) error {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return ErrInvalidState
	}
	return store.AppendRouteNode(old, next)
}

// sameRouteNodePrerequisites compares the prerequisite relations of two node
// versions. The prerequisite relations are the route-node inputs: they say
// which other route nodes this node depends on.
func sameRouteNodePrerequisites(before, after []ObjectVersionRef) bool {
	beforeIDs := routeNodeInputIDs(before)
	afterIDs := routeNodeInputIDs(after)
	if len(beforeIDs) != len(afterIDs) {
		return false
	}
	sorted := append([]string{}, beforeIDs...)
	sort.Strings(sorted)
	other := append([]string{}, afterIDs...)
	sort.Strings(other)
	return strings.Join(sorted, ",") == strings.Join(other, ",")
}

func routeNodeInputIDs(refs []ObjectVersionRef) []string {
	ids := []string{}
	for _, ref := range refs {
		if ref.Kind == "RouteNode" {
			ids = append(ids, ref.ID)
		}
	}
	return ids
}

// twoRouteNodeVersions picks two versions out of a version sequence.
func twoRouteNodeVersions(versions []RouteNode, from, to int64) (RouteNode, RouteNode, bool) {
	var before, after RouteNode
	foundBefore, foundAfter := false, false
	for _, node := range versions {
		if node.Version == from {
			before, foundBefore = node, true
		}
		if node.Version == to {
			after, foundAfter = node, true
		}
	}
	return before, after, foundBefore && foundAfter
}

// roadmapNodeChanges lists the field differences between two node versions.
func roadmapNodeChanges(before, after RouteNode) []RoadmapFieldChange {
	changes := []RoadmapFieldChange{}
	add := func(field, from, to string) {
		if from != to {
			changes = append(changes, RoadmapFieldChange{Field: field, From: from, To: to})
		}
	}
	add("name", before.Name, after.Name)
	add("status", before.Status, after.Status)
	add("required", strconv.FormatBool(before.Required), strconv.FormatBool(after.Required))
	add("inputs", refListText(before.Inputs), refListText(after.Inputs))
	add("targets", refListText(before.Targets), refListText(after.Targets))
	add("context_revision", strconv.FormatInt(before.ContextRevision, 10), strconv.FormatInt(after.ContextRevision, 10))
	return changes
}

func refListText(refs []ObjectVersionRef) string {
	parts := []string{}
	for _, ref := range refs {
		parts = append(parts, fmt.Sprintf("%s/%s@%s", ref.Kind, ref.ID, ref.Version))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// replaceRoadmapNodeProposal stores one proposal per node identity.
func replaceRoadmapNodeProposal(proposals []RoadmapNodeProposal, proposal RoadmapNodeProposal) []RoadmapNodeProposal {
	next := []RoadmapNodeProposal{}
	for _, existing := range proposals {
		if existing.NodeID != proposal.NodeID {
			next = append(next, existing)
		}
	}
	return append(next, proposal)
}

// replaceRoadmapEdgeProposal stores one proposal per edge identity.
func replaceRoadmapEdgeProposal(proposals []RoadmapEdgeProposal, proposal RoadmapEdgeProposal) []RoadmapEdgeProposal {
	next := []RoadmapEdgeProposal{}
	for _, existing := range proposals {
		if existing.EdgeID != proposal.EdgeID {
			next = append(next, existing)
		}
	}
	return append(next, proposal)
}
