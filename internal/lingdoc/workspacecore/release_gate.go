package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
	"slices"
	"time"
)

// ErrRevisionNotStable preserves the existing invalid-state domain guard while
// letting clients explain the pending Owner stabilization step.
var ErrRevisionNotStable = clienterror.Wrap(clienterror.Form{Status: 422, Code: "context_unstable", Message: "正式变更尚未完成稳定化。请用当前交付检查回执闭环影响任务，再由 Owner 检查稳定化后冻结。", Retryable: false}, "invalid_state: current formal changes require Owner stabilization", ErrInvalidState)

type PreflightBasis struct {
	Project              Project
	InputBasisHash       string
	ComputationBasisHash string
	SourceBasisHash      string
	SourceIDs            []string
}

func currentPreflightSourceIDs(tx Transaction, p Project) ([]string, error) {
	chapters, err := tx.Chapters(p.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	specSources, err := ProvenanceSourceIDs(tx, p)
	if err != nil {
		return nil, err
	}
	ids = append(ids, specSources...)
	for _, chapter := range chapters {
		ids = append(ids, chapter.SourceIDs...)
	}
	store, ok := tx.(semanticTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	routes, err := store.RouteNodes(p.ID)
	if err != nil {
		return nil, err
	}
	for _, node := range routes {
		if node.Status == "retired" {
			continue
		}
		found, e := impactSourceIDsForRefs(tx, p, node.Inputs)
		if e != nil {
			return nil, e
		}
		ids = append(ids, found...)
	}
	if p.TemplateCopy != nil && p.TemplateCopy.RuleSetSnapshot != nil {
		for _, binding := range p.TemplateCopy.RuleSetSnapshot.Bindings {
			if binding.Enabled && binding.TargetKind == "source" {
				ids = append(ids, binding.TargetID)
			}
		}
	}
	return sortedImpactSourceIDs(ids), nil
}
func preflightBasisInTransaction(tx Transaction, p Project, expectedSourceIDs []string) (PreflightBasis, error) {
	if p.Status != "active" || p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound {
		return PreflightBasis{}, ErrInvalidState
	}
	sourceIDs, err := currentPreflightSourceIDs(tx, p)
	if err != nil {
		return PreflightBasis{}, err
	}
	if expectedSourceIDs != nil && !slices.Equal(sourceIDs, sortedImpactSourceIDs(expectedSourceIDs)) {
		return PreflightBasis{}, fmt.Errorf("%w: evaluated source IDs differ from all current chapter, route and rule references", ErrVersionConflict)
	}
	computation, err := ImpactComputationBasisHash(tx, p)
	if err != nil {
		return PreflightBasis{}, err
	}
	sources, ok := tx.(impactComputationSourceTransaction)
	if !ok {
		return PreflightBasis{}, ErrInvalidState
	}
	sourceHash, err := sources.ImpactSourceDigest(p.ID, sourceIDs)
	if err != nil {
		return PreflightBasis{}, err
	}
	basis := PreflightBasis{Project: p, ComputationBasisHash: computation, SourceBasisHash: sourceHash, SourceIDs: sourceIDs}
	basis.InputBasisHash = hashImpactValue(struct {
		Computation, SourceHash string
		SourceIDs               []string
	}{computation, sourceHash, sourceIDs})
	return basis, nil
}
func (s *Service) ReadPreflightBasis(ctx context.Context, actor Actor, projectID string, sourceIDs []string) (PreflightBasis, error) {
	var basis PreflightBasis
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if e != nil {
			return e
		}
		basis, e = preflightBasisInTransaction(tx, p, sourceIDs)
		return e
	})
	if err != nil {
		return PreflightBasis{}, err
	}
	if err := s.checkSources(ctx, actor, projectID, basis.SourceIDs); err != nil {
		return PreflightBasis{}, err
	}
	return basis, nil
}

// Trusted server composition supplies actual evaluator outputs. No HTTP route
// accepts this argument. The expected basis is captured before any model/IO.
func (s *Service) StoreImpactComputationReceipt(ctx context.Context, actor Actor, basis PreflightBasis, inputHash, status string, evaluations []template.Evaluation, sourceIDs []string) (ImpactComputationReceipt, error) {
	if basis.Project.ID == "" || basis.Project.TemplateCopy == nil || basis.InputBasisHash == "" || basis.ComputationBasisHash == "" || basis.SourceBasisHash == "" || inputHash == "" || !slices.Contains([]string{"passed", "blocked"}, status) {
		return ImpactComputationReceipt{}, ErrInvalidRequest
	}
	if !slices.Equal(sortedImpactSourceIDs(sourceIDs), basis.SourceIDs) {
		return ImpactComputationReceipt{}, fmt.Errorf("%w: result source IDs do not match the captured current evaluation basis", ErrVersionConflict)
	}
	for _, evaluation := range evaluations {
		if evaluation.RulesetHash != basis.Project.TemplateCopy.RulesetHash || evaluation.EvaluatorVersion != template.EvaluatorVersion || evaluation.RuleID == "" || evaluation.TargetVersion == "" || evaluation.TargetRef == "" || !slices.Contains([]template.EvaluationStatus{template.EvaluationPass, template.EvaluationIssue, template.EvaluationUnknown, template.EvaluationNotApplicable}, evaluation.Status) {
			return ImpactComputationReceipt{}, ErrInvalidRequest
		}
		if status == "passed" && evaluation.Status == template.EvaluationUnknown {
			return ImpactComputationReceipt{}, ErrInvalidState
		}
	}
	if err := s.checkSources(ctx, actor, basis.Project.ID, basis.SourceIDs); err != nil {
		return ImpactComputationReceipt{}, err
	}
	var receipt ImpactComputationReceipt
	err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, basis.Project.ID, AuthorizationRead, ""))
		if e != nil {
			return e
		}
		actual, e := preflightBasisInTransaction(tx, p, sourceIDs)
		if e != nil {
			return e
		}
		if actual.InputBasisHash != basis.InputBasisHash || p.ProjectVersion != basis.Project.ProjectVersion || p.CurrentContextRevision != basis.Project.CurrentContextRevision || p.TemplateCopyVersion != basis.Project.TemplateCopyVersion {
			return ErrVersionConflict
		}
		for _, evaluation := range evaluations {
			if evaluation.RulesetHash != p.TemplateCopy.RulesetHash {
				return ErrVersionConflict
			}
		}
		if len(evaluations) == 0 && (p.TemplateCopy.RuleSetSnapshot == nil && len(p.TemplateCopy.Definition.Rules) > 0) {
			return ErrInvalidState
		}
		receipt = ImpactComputationReceipt{ID: uuid.NewString(), Version: 1, ProjectID: p.ID, Status: status, ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash, InputHash: inputHash, ComputationBasisHash: actual.ComputationBasisHash, SourceBasisHash: actual.SourceBasisHash, SourceIDs: actual.SourceIDs, Evaluations: append([]template.Evaluation{}, evaluations...), CheckedAt: time.Now().UTC()}
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return ErrInvalidState
		}
		if err := store.G6Insert(actor.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt); err != nil {
			return err
		}
		if err := recordAuditEvent(tx, actor, p.ID, "preflight.compute", receipt.ID, map[string]any{"status": status, "input_hash": inputHash, "basis_hash": actual.InputBasisHash, "ruleset_hash": receipt.RulesetHash}); err != nil {
			return err
		}
		after, e := preflightBasisInTransaction(tx, p, sourceIDs)
		if e != nil {
			return e
		}
		if after.InputBasisHash != actual.InputBasisHash {
			return ErrVersionConflict
		}
		return nil
	})
	return receipt, err
}
func (s *Service) ReadImpactComputationReceipt(ctx context.Context, actor Actor, projectID, id string) (ImpactComputationReceipt, error) {
	var receipt ImpactComputationReceipt
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if e != nil {
			return e
		}
		receipt, e = readImpactComputation(tx, p, id)
		if e != nil {
			return e
		}
		actual, e := preflightBasisInTransaction(tx, p, receipt.SourceIDs)
		if e != nil {
			return e
		}
		if receipt.ProjectVersion != p.ProjectVersion || receipt.ContextRevision != p.CurrentContextRevision || receipt.TemplateCopyVersion != p.TemplateCopyVersion || receipt.RulesetHash != p.TemplateCopy.RulesetHash || receipt.ComputationBasisHash != actual.ComputationBasisHash || receipt.SourceBasisHash != actual.SourceBasisHash {
			return ErrVersionConflict
		}
		return nil
	})
	if err != nil {
		return receipt, err
	}
	return receipt, s.checkSources(ctx, actor, projectID, receipt.SourceIDs)
}

// Read-only actual release gate. It does not perform an Owner's stabilization
// decision or advance metadata versions while inspecting a completed receipt.
func (s *Service) AssertRevisionStabilized(ctx context.Context, actor Actor, projectID string) error {
	var basis PreflightBasis
	preflightIDs := []string{}
	sourceIDs := []string{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if e != nil {
			return e
		}
		basis, e = preflightBasisInTransaction(tx, p, nil)
		if e != nil {
			return e
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		history, e := changes.ChangeSets(p.ID)
		if e != nil {
			return e
		}
		found := false
		for _, change := range history {
			if change.TargetContextRevision != nil && *change.TargetContextRevision == p.CurrentContextRevision && slices.Contains(ChangeSetClosedStatuses, change.Status) {
				if change.Status != ChangeSetStatusStabilized {
					// Name the exact ChangeSet so the client can drive the
					// Owner stabilization step instead of inferring it.
					return fmt.Errorf("%w: current applied ChangeSet %s has not been stabilized by Owner", ErrRevisionNotStable, change.ID)
				}
				found = true
			}
		}
		if !found && p.CurrentContextRevision > 1 {
			return fmt.Errorf("%w: no current stabilized ChangeSet", ErrRevisionNotStable)
		}
		store, ok := tx.(impactTransaction)
		if !ok {
			return ErrInvalidState
		}
		// Every "what counts as not stabilized" condition is decided by the one
		// evaluation in stabilizationConditions, the same one the Owner
		// stabilization path consumes. This entry adds only what is deliberately
		// gate-shaped: it writes nothing, it fails fast on the first unsatisfied
		// condition, and it re-authorizes sources afterwards.
		conditions, e := s.stabilizationConditions(tx, p, store, stabilizationScope{GateFailFast: true, ClosureSourcesOfResultTasks: true})
		if e != nil {
			return e
		}
		preflightIDs = conditions.PreflightIDs
		sourceIDs = conditions.ClosureSourceIDs
		if e := conditions.GateError(); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return err
	}
	sourceIDs = append(sourceIDs, basis.SourceIDs...)
	// ADJUDICATION #4 (deliberate difference, not merged). The gate re-authorizes
	// the full current basis source set (chapters, routes, rule bindings) in
	// addition to the closure evidence sources; the Owner stabilization path
	// only re-authorizes its closure evidence refs. Making the Owner path equally
	// strict would make stabilization stricter. The asymmetry is safe now that
	// #214 has been decided: a revoked source never reaches either path as a
	// STALE transition, because the operation-level recheck refuses the whole
	// operation first and the task stays unhandled. Relaxing the Owner path
	// would therefore not resurrect any impact task's ability to go stale - it
	// would only let stabilization report on a task whose sources it can no
	// longer vouch for.
	if err := s.checkSources(ctx, actor, projectID, sortedImpactSourceIDs(sourceIDs)); err != nil {
		return err
	}
	for _, id := range sortedImpactSourceIDs(preflightIDs) {
		proof, e := s.recheckImpactPreflight(ctx, actor, projectID, id)
		if e != nil {
			return e
		}
		if !validImpactPreflight(basis.Project, id, proof) {
			return ErrVersionConflict
		}
	}
	latest, err := s.ReadPreflightBasis(ctx, actor, projectID, basis.SourceIDs)
	if err != nil {
		return err
	}
	if latest.InputBasisHash != basis.InputBasisHash {
		return ErrVersionConflict
	}
	return nil
}

// Strict semantic comparison permits lifecycle metadata updates, but a
// changed text, scope, consent, binding or route requires a fresh human draft.
func semanticMeaning(kind string, body json.RawMessage) (any, error) {
	switch kind {
	case "EvidenceCard":
		var value EvidenceCard
		if e := json.Unmarshal(body, &value); e != nil {
			return nil, e
		}
		return cardSemanticInput(value), nil
	case "ResearchClaim":
		var value ResearchClaim
		if e := json.Unmarshal(body, &value); e != nil {
			return nil, e
		}
		return struct{ Text, Scope, Status, Explanation string }{value.Text, value.Scope, value.Status, value.Explanation}, nil
	case "ProjectAssetScope":
		var value ProjectAssetPolicy
		if e := json.Unmarshal(body, &value); e != nil {
			return nil, e
		}
		return struct {
			Scope, Purpose  string
			AllowedUsers    []string
			ModelUseAllowed bool
		}{value.Scope, value.Purpose, sortedImpactSourceIDs(value.AllowedUserIDs), value.ModelUseAllowed}, nil
	case "RuleBinding":
		var value template.RuleBinding
		if e := json.Unmarshal(body, &value); e != nil {
			return nil, e
		}
		return value, nil
	case "RouteNode":
		var value RouteNode
		if e := json.Unmarshal(body, &value); e != nil {
			return nil, e
		}
		return struct {
			Name, Status string
			Required     bool
			Inputs       []ObjectVersionRef
		}{value.Name, value.Status, value.Required, value.Inputs}, nil
	}
	return nil, ErrInvalidRequest
}
func (s *Service) ReassessSemanticPlan(ctx context.Context, tx Transaction, actor Actor, p Project, newID string, stale ChangeSet) ([]SemanticDelta, error) {
	if len(stale.SemanticDeltas) == 0 {
		return []SemanticDelta{}, nil
	}
	deltas := append([]SemanticDelta{}, stale.SemanticDeltas...)
	for i, delta := range deltas {
		current, e := currentSemanticValue(tx, p, delta)
		if e != nil {
			return nil, e
		}
		before, e := semanticMeaning(delta.Kind, delta.Before)
		if e != nil {
			return nil, e
		}
		now, e := semanticMeaning(delta.Kind, current.Body)
		if e != nil {
			return nil, e
		}
		if hashImpactValue(before) != hashImpactValue(now) {
			return nil, ErrVersionConflict
		}
		var version int64
		if _, e := fmt.Sscan(current.Ref.Version, &version); e != nil {
			return nil, ErrInvalidState
		}
		deltas[i].ExpectedVersion = version
	}
	return s.PinSemanticDeltas(ctx, tx, actor, p, newID, deltas)
}

func researchClaimReviewCurrent(tx Transaction, p Project, ref ObjectVersionRef) bool {
	claim, _, err := g2Load[ResearchClaim](tx, p.ID, G2ClaimKind, ref.ID)
	return err == nil && ref.Version == fmt.Sprint(claim.Version) && (claim.Status == "active" || claim.Status == "disputed" || claim.Status == "superseded") && claim.Basis.SpecRevision == p.SpecRevision
}
