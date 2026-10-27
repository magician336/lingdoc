package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// The cold-start recommender for dependency edges (#751).
//
// The dependency graph only grows through two gates: the manual registration
// form and a confirmed candidate. A project with no edges degrades every
// impact assessment to manual chapter picking, which is what this recommender
// addresses: a human asks for proposals, the server collects the signals the
// project already carries (chapter source references, citation usages,
// evidence-card scopes, the template structure, route-node inputs and the
// edges that already exist) and asks the model for candidate edges.
//
// The invariants that keep the human gate intact:
//
//   - The model proposes KIND and ID only. The object VERSION is filled in by
//     the server from readImpactObjectState, the one place that knows the
//     current encoding, and every proposal is re-checked against the signal
//     list: a fabricated object never reaches the queue.
//   - A replayed idempotency key never triggers a second model call. The batch
//     is looked up before the model is invoked; only a miss runs the model.
//   - A failed, timed-out or unusable model call leaves no business write at
//     all. The ledger row (written for every call, succeeded or failed) is the
//     only trace, because it records a fact about the provider, not about the
//     project.
//   - Valid proposals enter the EXISTING candidate queue through
//     SubmitDependencyCandidateBatch: same suppression memory, same human
//     decision endpoints, same audit events. The recommender adds a producer,
//     not a second queue.

const (
	// dependencyRecommendationCallTimeout bounds one recommendation call. The
	// route is synchronous: a recommendation that hangs must surface as a
	// timeout instead of holding the panel forever. A real qwen3.8-flash call
	// measured 63s on the live round-trip (4010 completion tokens including
	// reasoning), so the ceiling has to sit above a real answer, not just
	// above a stub.
	dependencyRecommendationCallTimeout = 120 * time.Second
)

var (
	// ErrDependencyRecommendationUnavailable reports that no recommendation
	// model is wired or the model registry has no usable default generation
	// model. The manual registration form stays fully usable.
	ErrDependencyRecommendationUnavailable = clienterror.New(clienterror.Form{
		Status: 503, Code: "dependency_recommendation_unavailable",
		Message: "依赖推荐服务当前不可用，人工登记不受影响。", Retryable: true,
	}, "dependency_recommendation_unavailable")

	// ErrDependencyRecommendationInvalidOutput reports that the model answered
	// but the answer is not the agreed shape. Nothing is submitted.
	ErrDependencyRecommendationInvalidOutput = clienterror.New(clienterror.Form{
		Status: 502, Code: "dependency_recommendation_invalid_output",
		Message: "依赖推荐模型返回的内容无法解析，未产生任何候选。", Retryable: true,
	}, "dependency_recommendation_invalid_output")

	// ErrDependencyRecommendationNoValidCandidates reports that the model
	// answered the agreed shape but every proposal failed review. Nothing is
	// submitted: an empty batch is not a recommendation.
	ErrDependencyRecommendationNoValidCandidates = clienterror.New(clienterror.Form{
		Status: 422, Code: "dependency_recommendation_no_valid_candidates",
		Message: "模型建议的依赖边全部未通过复核，未产生任何候选。", Retryable: false,
	}, "dependency_recommendation_no_valid_candidates")
)

// Reasons a reviewed proposal was dropped instead of queued.
const (
	DependencyRecommendationDropFabricatedObject = "fabricated_object"
	DependencyRecommendationDropInvalidField     = "invalid_field"
	DependencyRecommendationDropInvalidAction    = "invalid_action"
	DependencyRecommendationDropScopeViolation   = "scope_violation"
	DependencyRecommendationDropDuplicate        = "duplicate"
	DependencyRecommendationDropEdgePresent      = "edge_already_present"
	DependencyRecommendationDropOverflow         = "overflow"
)

// DependencyRecommendationModel is the narrow port the recommender needs from
// a model adapter: read the signal pack, return proposals. One call, no
// automatic retry, one ledger row per call — the discipline the other host
// model adapters already follow.
type DependencyRecommendationModel interface {
	Recommend(ctx context.Context, actor Actor, request DependencyRecommendationRequest) (DependencyRecommendationResult, error)
}

// DependencyRecommendationInput is the request body of the recommendation
// trigger.
type DependencyRecommendationInput struct {
	// IdempotencyKey identifies the recommendation run. It defaults to the
	// transport idempotency key. A replay of the same key returns the original
	// batch without a second model call.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// Reason is the operator's own statement of why recommendations are wanted
	// (why this round was initiated). It is carried into the batch.
	Reason string `json:"reason"`
	// Scope optionally narrows what may be proposed. An empty scope lets the
	// model read the whole project, which is the cold-start case.
	Scope DependencyCandidateScope `json:"scope"`
}

// DependencySignalObject is one object the model may propose an edge between.
//
// The Ref carries the server-owned current version, so a proposal only needs
// kind and id and the server can still re-check the basis at decision time.
type DependencySignalObject struct {
	Ref             ObjectVersionRef `json:"ref"`
	Title           string           `json:"title,omitempty"`
	SourceIDs       []string         `json:"source_ids,omitempty"`
	ScopeChapterIDs []string         `json:"scope_chapter_ids,omitempty"`
	Detail          string           `json:"detail,omitempty"`
}

// DependencyEdgeSummary tells the model which tuples already exist so it does
// not spend its proposal budget on them.
type DependencyEdgeSummary struct {
	Source   ObjectVersionRef `json:"source"`
	Target   ObjectVersionRef `json:"target"`
	Relation string           `json:"relation"`
	Action   string           `json:"action"`
	Status   string           `json:"status"`
}

// DependencyRecommendationSignals is the read-only picture of the project the
// recommendation is derived from.
type DependencyRecommendationSignals struct {
	Objects []DependencySignalObject `json:"objects"`
	Edges   []DependencyEdgeSummary  `json:"edges"`
}

// DependencyRecommendationRequest is what the adapter sends to the model.
type DependencyRecommendationRequest struct {
	ProjectID string
	Project   Project
	Signals   DependencyRecommendationSignals
	Scope     DependencyCandidateScope
}

// ModelProposedDependency is one model proposal. It deliberately carries no
// version: the server owns the version and fills it from the signal list.
type ModelProposedDependency struct {
	SourceKind string  `json:"source_kind"`
	SourceID   string  `json:"source_id"`
	TargetKind string  `json:"target_kind"`
	TargetID   string  `json:"target_id"`
	Relation   string  `json:"relation"`
	Action     string  `json:"action"`
	Evidence   string  `json:"evidence"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

// DependencyRecommendationResult is the adapter's answer.
type DependencyRecommendationResult struct {
	Proposals  []ModelProposedDependency
	ModelID    string
	RunMode    string
	ModelRunID string
}

// DependencyRecommendationDrop is one proposal the review refused, with the
// reason. Drops are reported back so the caller sees what the model wasted.
type DependencyRecommendationDrop struct {
	Proposal ModelProposedDependency `json:"proposal"`
	Reason   string                  `json:"reason"`
}

// DependencyRecommendationModelInfo reports which model produced the batch.
type DependencyRecommendationModelInfo struct {
	ModelID    string `json:"model_id,omitempty"`
	RunMode    string `json:"run_mode,omitempty"`
	ModelRunID string `json:"model_run_id,omitempty"`
}

// DependencyRecommendationOutcome is the response of the recommendation
// trigger: the queue batch, the refused proposals, and the model that ran.
type DependencyRecommendationOutcome struct {
	Batch   json.RawMessage                   `json:"batch"`
	Dropped []DependencyRecommendationDrop    `json:"dropped,omitempty"`
	Model   DependencyRecommendationModelInfo `json:"model"`
}

// SetDependencyRecommendationModel wires the recommendation model. Without it
// the recommender fails closed; the manual registration form is unaffected.
func (s *Service) SetDependencyRecommendationModel(model DependencyRecommendationModel) {
	s.recommendationModel = model
}

// RecommendDependencyCandidates runs one recommendation: collect signals,
// ask the model, review every proposal, and queue the survivors through the
// existing candidate batch path.
//
// The write itself is NOT done here: the reviewed proposals are submitted
// through SubmitDependencyCandidateBatch, which owns authorization, the
// transaction, the suppression memory and the audit event. This method owns
// the model call and the review.
func (s *Service) RecommendDependencyCandidates(ctx context.Context, actor Actor, projectID, key string, input DependencyRecommendationInput) (json.RawMessage, int, bool, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	batchKey := strings.TrimSpace(input.IdempotencyKey)
	if batchKey == "" {
		batchKey = strings.TrimSpace(key)
	}
	if batchKey == "" || len(batchKey) > 128 {
		return nil, 0, false, ErrInvalidRequest
	}
	if strings.TrimSpace(input.Reason) == "" || len(input.Reason) > dependencyCandidateTextLimit {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := validateDependencyRecommendationScope(input.Scope); err != nil {
		return nil, 0, false, err
	}
	if s.recommendationModel == nil {
		return nil, 0, false, ErrDependencyRecommendationUnavailable
	}
	p, signals, replayed, err := s.dependencyRecommendationBasis(ctx, actor, projectID, batchKey)
	if err != nil {
		return nil, 0, false, err
	}
	if replayed != nil {
		raw, err := json.Marshal(replayed)
		if err != nil {
			return nil, 0, false, err
		}
		return raw, 200, true, nil
	}
	modelCtx, cancel := context.WithTimeout(ctx, dependencyRecommendationCallTimeout)
	defer cancel()
	result, err := s.recommendationModel.Recommend(modelCtx, actor, DependencyRecommendationRequest{
		ProjectID: projectID,
		Project:   p,
		Signals:   signals,
		Scope:     input.Scope,
	})
	if err != nil {
		// Whatever the adapter said, a model that could not answer is an
		// unavailable recommender to the caller: the cause stays first in the
		// chain, an adapter that already declared its own client form keeps
		// it, and a bare provider error cannot leak as an internal error.
		return nil, 0, false, errors.Join(err, ErrDependencyRecommendationUnavailable)
	}
	if result.ModelRunID == "" {
		return nil, 0, false, ErrDependencyRecommendationInvalidOutput
	}
	candidates, dropped := reviewModelProposedDependencies(result.Proposals, signals, input.Scope, result.ModelRunID)
	if len(candidates) == 0 {
		return nil, 0, false, ErrDependencyRecommendationNoValidCandidates
	}
	scope := input.Scope
	if strings.TrimSpace(scope.Reason) == "" {
		scope.Reason = "AI 依赖候选推荐：" + input.Reason
	}
	raw, status, replay, err := s.SubmitDependencyCandidateBatch(ctx, actor, projectID, key, DependencyCandidateBatchInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		IdempotencyKey:          batchKey,
		Scope:                   scope,
		Reason:                  input.Reason,
		Candidates:              candidates,
	})
	if err != nil {
		return nil, 0, false, err
	}
	outcome := DependencyRecommendationOutcome{
		Batch:   raw,
		Dropped: dropped,
		Model: DependencyRecommendationModelInfo{
			ModelID:    result.ModelID,
			RunMode:    result.RunMode,
			ModelRunID: result.ModelRunID,
		},
	}
	out, err := json.Marshal(outcome)
	if err != nil {
		return nil, 0, false, err
	}
	return out, status, replay, nil
}

// dependencyRecommendationBasis loads the project and its signals in one
// read-only transaction, and reports an idempotency replay before the model
// is ever invoked: a replayed key must not cost a second model call.
func (s *Service) dependencyRecommendationBasis(ctx context.Context, actor Actor, projectID, batchKey string) (Project, DependencyRecommendationSignals, *DependencyCandidateBatch, error) {
	var p Project
	var signals DependencyRecommendationSignals
	var replayed *DependencyCandidateBatch
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		p, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		store, ok := tx.(dependencyCandidateStore)
		if !ok {
			return ErrInvalidState
		}
		existing, err := store.DependencyCandidateBatchByKey(projectID, actor.UserID, batchKey)
		if err == nil {
			replayed = &existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		signals, err = collectDependencyRecommendationSignals(tx, p)
		return err
	})
	if err != nil {
		return Project{}, DependencyRecommendationSignals{}, nil, err
	}
	return p, signals, replayed, nil
}

func validateDependencyRecommendationScope(scope DependencyCandidateScope) error {
	for _, object := range scope.Objects {
		if !validObjectRef(object) {
			return ErrInvalidRequest
		}
	}
	for _, kind := range scope.TargetKinds {
		if !validObjectKind(kind) {
			return ErrInvalidRequest
		}
	}
	if len(scope.Reason) > dependencyCandidateTextLimit {
		return ErrInvalidRequest
	}
	return nil
}

// validObjectKind reports whether kind is one of the dependency-graph object
// kinds. It is the same vocabulary validObjectRef enforces, stated once more
// for callers that check a kind without an id.
func validObjectKind(kind string) bool {
	for _, candidate := range []string{"ProjectSpec", "ProjectAsset", "SourceAnchor", "EvidenceCard", "ResearchClaim", "ChapterVersion", "TemplateCopy", "RuleSet", "RuleBinding", "RouteNode", "RouteEdge", "ResearchRun", "ComputationReceipt"} {
		if kind == candidate {
			return true
		}
	}
	return false
}

// dependencySignalObjectRef resolves the CURRENT server-owned ref of one
// signal object. readImpactObjectState is the single place that knows the
// version encoding per kind, so the ref a candidate later carries is encoded
// exactly the way the write-time basis check reads it.
func dependencySignalObjectRef(tx Transaction, p Project, kind, id string) (ObjectVersionRef, error) {
	state, err := readImpactObjectState(tx, p, ObjectVersionRef{Kind: kind, ID: id, Version: "0"})
	if err != nil {
		return ObjectVersionRef{}, err
	}
	return state.Ref, nil
}

// skipSignalObject reports whether an object that cannot be resolved should
// be skipped from the signal list (it has no current version to anchor an
// edge on) instead of failing the whole recommendation.
func skipSignalObject(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidState)
}

// collectDependencyRecommendationSignals reads the project's own signals:
// the spec and template structure, the chapters with their source references
// and citation usages, the evidence cards with their chapter scopes, the
// route nodes with their declared inputs, and the dependency edges that
// already exist.
func collectDependencyRecommendationSignals(tx Transaction, p Project) (DependencyRecommendationSignals, error) {
	signals := DependencyRecommendationSignals{Objects: []DependencySignalObject{}, Edges: []DependencyEdgeSummary{}}
	if err := appendSpecSignals(tx, p, &signals); err != nil {
		return signals, err
	}
	if err := appendChapterSignals(tx, p, &signals); err != nil {
		return signals, err
	}
	if err := appendEvidenceCardSignals(tx, p, &signals); err != nil {
		return signals, err
	}
	if err := appendRouteNodeSignals(tx, p, &signals); err != nil {
		return signals, err
	}
	return signals, appendEdgeSummaries(tx, p, &signals)
}

func appendSpecSignals(tx Transaction, p Project, signals *DependencyRecommendationSignals) error {
	specRef, err := dependencySignalObjectRef(tx, p, "ProjectSpec", p.ID)
	if err != nil {
		return err
	}
	object := DependencySignalObject{Ref: specRef, Title: "项目研究条件"}
	if p.TemplateCopy != nil {
		fields := make([]string, 0, len(p.TemplateCopy.Definition.Fields))
		for _, field := range p.TemplateCopy.Definition.Fields {
			fields = append(fields, field.ID+"（"+field.Label+"）")
		}
		if len(fields) > 0 {
			object.Detail = "模板字段：" + strings.Join(fields, "、")
		}
	}
	signals.Objects = append(signals.Objects, object)
	if p.TemplateCopy == nil {
		return nil
	}
	copyRef, err := dependencySignalObjectRef(tx, p, "TemplateCopy", p.TemplateID)
	if err != nil {
		if skipSignalObject(err) {
			return nil
		}
		return err
	}
	sections := make([]string, 0, len(p.TemplateCopy.Definition.Sections))
	for _, section := range p.TemplateCopy.Definition.Sections {
		sections = append(sections, section.ID+"（"+section.Title+"）")
	}
	signals.Objects = append(signals.Objects, DependencySignalObject{
		Ref:    copyRef,
		Title:  "项目模板",
		Detail: "章节骨架：" + strings.Join(sections, "、"),
	})
	return nil
}

func appendChapterSignals(tx Transaction, p Project, signals *DependencyRecommendationSignals) error {
	chapters, err := tx.Chapters(p.ID)
	if err != nil {
		return err
	}
	for _, chapter := range chapters {
		if chapter.CurrentVersionID == nil || *chapter.CurrentVersionID == "" {
			// A chapter without a current version has no versioned identity
			// to anchor an edge on.
			continue
		}
		ref, err := dependencySignalObjectRef(tx, p, "ChapterVersion", chapter.ID)
		if err != nil {
			if skipSignalObject(err) {
				continue
			}
			return err
		}
		object := DependencySignalObject{Ref: ref, Title: chapter.Title, SourceIDs: chapter.SourceIDs}
		usages := make([]string, 0, len(chapter.CitationUsages))
		for _, usage := range chapter.CitationUsages {
			if usage.Purpose != "" {
				usages = append(usages, usage.SourceID+"："+usage.Purpose)
			}
		}
		if len(usages) > 0 {
			object.Detail = "引用用途：" + strings.Join(usages, "；")
		}
		signals.Objects = append(signals.Objects, object)
	}
	return nil
}

func appendEvidenceCardSignals(tx Transaction, p Project, signals *DependencyRecommendationSignals) error {
	port, err := agentObjectStore(tx)
	if err != nil {
		return err
	}
	rows, err := port.Objects(p.ID, EvidenceCardKind)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var card EvidenceCard
		if err := json.Unmarshal(row.Payload, &card); err != nil {
			return err
		}
		ref, err := dependencySignalObjectRef(tx, p, "EvidenceCard", card.ID)
		if err != nil {
			if skipSignalObject(err) {
				continue
			}
			return err
		}
		detail := "support_type=" + card.SupportType
		if card.Scope != "" {
			detail += "；scope=" + card.Scope
		}
		signals.Objects = append(signals.Objects, DependencySignalObject{
			Ref:             ref,
			Title:           "证据卡 " + card.ID,
			SourceIDs:       card.SourceIDs,
			ScopeChapterIDs: card.ScopeChapterIDs,
			Detail:          detail,
		})
	}
	return nil
}

func appendRouteNodeSignals(tx Transaction, p Project, signals *DependencyRecommendationSignals) error {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return ErrInvalidState
	}
	nodes, err := store.RouteNodes(p.ID)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		ref, err := dependencySignalObjectRef(tx, p, "RouteNode", node.ID)
		if err != nil {
			if skipSignalObject(err) {
				continue
			}
			return err
		}
		inputs := make([]string, 0, len(node.Inputs))
		for _, input := range node.Inputs {
			inputs = append(inputs, input.Kind+"/"+input.ID)
		}
		detail := "required=" + strconv.FormatBool(node.Required)
		if len(inputs) > 0 {
			detail += "；inputs：" + strings.Join(inputs, "、")
		}
		signals.Objects = append(signals.Objects, DependencySignalObject{
			Ref:    ref,
			Title:  node.Name,
			Detail: detail,
		})
	}
	return nil
}

func appendEdgeSummaries(tx Transaction, p Project, signals *DependencyRecommendationSignals) error {
	edges, ok := tx.(impactTransaction)
	if !ok {
		return ErrInvalidState
	}
	existing, err := edges.DependencyEdges(p.ID)
	if err != nil {
		return err
	}
	for _, edge := range existing {
		// A rejected edge may legitimately be re-proposed: the human refusal
		// is remembered on the candidate side, and resurrecting it takes the
		// normal candidate decision path, not silence from the recommender.
		if edge.Status == "rejected" {
			continue
		}
		signals.Edges = append(signals.Edges, DependencyEdgeSummary{
			Source:   edge.Source,
			Target:   edge.Target,
			Relation: edge.Relation,
			Action:   edge.Action,
			Status:   edge.Status,
		})
	}
	return nil
}

// reviewModelProposedDependencies turns model proposals into candidate
// inputs. Every rule that fails drops ONE proposal with a reason; the batch is
// built from what survives. The caller decides what an empty result means.
func reviewModelProposedDependencies(proposals []ModelProposedDependency, signals DependencyRecommendationSignals, scope DependencyCandidateScope, modelRunID string) ([]DependencyCandidateInput, []DependencyRecommendationDrop) {
	refs := make(map[string]ObjectVersionRef, len(signals.Objects))
	for _, object := range signals.Objects {
		refs[object.Ref.Kind+"/"+object.Ref.ID] = object.Ref
	}
	edges := make(map[string]bool, len(signals.Edges))
	for _, edge := range signals.Edges {
		edges[edge.Source.Kind+"/"+edge.Source.ID+"→"+edge.Target.Kind+"/"+edge.Target.ID+"|"+edge.Relation+"|"+edge.Action] = true
	}
	seen := make(map[string]bool, len(proposals))
	valid := []DependencyCandidateInput{}
	dropped := []DependencyRecommendationDrop{}
	for _, proposal := range proposals {
		drop, ok := reviewOneModelProposal(proposal, refs, edges, seen, scope)
		if !ok {
			dropped = append(dropped, drop)
			continue
		}
		valid = append(valid, DependencyCandidateInput{
			Source:     refs[proposal.SourceKind+"/"+proposal.SourceID],
			Target:     refs[proposal.TargetKind+"/"+proposal.TargetID],
			Relation:   strings.TrimSpace(proposal.Relation),
			Action:     proposal.Action,
			Evidence:   strings.TrimSpace(proposal.Evidence),
			Reason:     strings.TrimSpace(proposal.Reason),
			Confidence: proposal.Confidence,
			ModelRunID: modelRunID,
		})
	}
	if len(valid) > dependencyCandidateBatchLimit {
		// Keep the most confident proposals; the queue has a hard batch limit.
		sort.SliceStable(valid, func(i, j int) bool { return valid[i].Confidence > valid[j].Confidence })
		for range valid[dependencyCandidateBatchLimit:] {
			dropped = append(dropped, DependencyRecommendationDrop{Reason: DependencyRecommendationDropOverflow})
		}
		valid = valid[:dependencyCandidateBatchLimit]
	}
	return valid, dropped
}

func reviewOneModelProposal(proposal ModelProposedDependency, refs map[string]ObjectVersionRef, edges, seen map[string]bool, scope DependencyCandidateScope) (DependencyRecommendationDrop, bool) {
	source, sourceOK := refs[proposal.SourceKind+"/"+proposal.SourceID]
	target, targetOK := refs[proposal.TargetKind+"/"+proposal.TargetID]
	if !sourceOK || !targetOK {
		return drop(proposal, DependencyRecommendationDropFabricatedObject), false
	}
	trim := func(value string) string { return strings.TrimSpace(value) }
	if trim(proposal.Relation) == "" || len(proposal.Relation) > dependencyCandidateRelationLimit ||
		trim(proposal.Evidence) == "" || len(proposal.Evidence) > dependencyCandidateTextLimit ||
		trim(proposal.Reason) == "" || len(proposal.Reason) > dependencyCandidateTextLimit ||
		source == target {
		return drop(proposal, DependencyRecommendationDropInvalidField), false
	}
	if !validImpactAction(proposal.Action) {
		return drop(proposal, DependencyRecommendationDropInvalidAction), false
	}
	candidate := DependencyCandidateInput{Source: source, Target: target, Relation: trim(proposal.Relation), Action: proposal.Action}
	if !dependencyCandidateScopeAllows(scope, candidate) {
		return drop(proposal, DependencyRecommendationDropScopeViolation), false
	}
	tuple := source.Kind + "/" + source.ID + "→" + target.Kind + "/" + target.ID + "|" + trim(proposal.Relation) + "|" + proposal.Action
	if seen[tuple] {
		return drop(proposal, DependencyRecommendationDropDuplicate), false
	}
	if edges[tuple] {
		return drop(proposal, DependencyRecommendationDropEdgePresent), false
	}
	seen[tuple] = true
	return DependencyRecommendationDrop{}, true
}

func drop(proposal ModelProposedDependency, reason string) DependencyRecommendationDrop {
	return DependencyRecommendationDrop{Proposal: proposal, Reason: reason}
}
