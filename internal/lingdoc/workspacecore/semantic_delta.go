package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

type SemanticDelta struct {
	Kind                string                      `json:"kind"`
	TargetID            string                      `json:"target_id"`
	ExpectedVersion     int64                       `json:"expected_version"`
	BeforeHash          string                      `json:"before_hash,omitempty"`
	Before              json.RawMessage             `json:"before,omitempty"`
	ProvenanceHash      string                      `json:"provenance_hash,omitempty"`
	SourceIDs           []string                    `json:"source_ids,omitempty"`
	ProposedCard        *EvidenceCardSemanticInput  `json:"proposed_card,omitempty"`
	ProposedClaim       *SaveResearchClaimInput     `json:"proposed_claim,omitempty"`
	ProposedAssetPolicy *ProjectAssetPolicy         `json:"proposed_asset_policy,omitempty"`
	ProposedBinding     *governance.BindingOverride `json:"proposed_binding,omitempty"`
	ProposedRoute       *RouteNode                  `json:"proposed_route,omitempty"`
	// ProposedEdge carries a route edge change. An edge is a semantic change
	// (it changes the prerequisite relations of the roadmap), so it travels
	// through a ChangeSet like every other semantic delta.
	ProposedEdge *RouteEdge `json:"proposed_edge,omitempty"`
}
type RouteNode struct {
	ID        string             `json:"id"`
	ProjectID string             `json:"project_id"`
	Version   int64              `json:"version"`
	Name      string             `json:"name"`
	Inputs    []ObjectVersionRef `json:"inputs"`
	// Targets are the linked target objects of this node (associated outputs
	// and claims in the minimal field set).
	Targets         []ObjectVersionRef `json:"targets"`
	Required        bool               `json:"required"`
	Status          string             `json:"status"`
	ContextRevision int64              `json:"context_revision"`
	ActorID         string             `json:"actor_id"`
	Reason          string             `json:"reason"`
	UpdatedAt       time.Time          `json:"updated_at"`
}
type SemanticCurrentValue struct {
	Ref            ObjectVersionRef
	Body           json.RawMessage
	SourceIDs      []string
	ProvenanceHash string
}
type semanticTransaction interface {
	SemanticPlan(string, string) ([]SemanticDelta, error)
	SaveSemanticPlan(string, string, []SemanticDelta) error
	RouteNode(string, string) (RouteNode, error)
	RouteNodes(string) ([]RouteNode, error)
	AppendRouteNode(RouteNode, RouteNode) error
	ProjectBindingPolicies(Project) ([]governance.BindingPolicy, error)
}

func (t gormTransaction) SemanticPlan(project, id string) ([]SemanticDelta, error) {
	var plan []SemanticDelta
	_, err := t.readVersionedRecord(project, "semantic_plan", id, &plan)
	if err == ErrNotFound {
		return []SemanticDelta{}, nil
	}
	return plan, err
}
func (t gormTransaction) SaveSemanticPlan(project, id string, plan []SemanticDelta) error {
	var old []SemanticDelta
	version, err := t.readVersionedRecord(project, "semantic_plan", id, &old)
	if err == ErrNotFound {
		return t.insertVersionedRecord(project, "semantic_plan", id, 1, plan)
	}
	if err != nil {
		return err
	}
	return t.compareAndSwapVersionedRecord(project, "semantic_plan", id, version, plan)
}
func (t gormTransaction) RouteNode(project, id string) (RouteNode, error) {
	var value RouteNode
	_, err := t.readVersionedRecord(project, "route_node", id, &value)
	return value, err
}
func (t gormTransaction) RouteNodes(project string) ([]RouteNode, error) {
	rows, err := t.listVersionedRecords(project, "route_node")
	if err != nil {
		return nil, err
	}
	out := []RouteNode{}
	for _, row := range rows {
		var node RouteNode
		if err := json.Unmarshal([]byte(row.BodyJSON), &node); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}
func (t gormTransaction) AppendRouteNode(old, next RouteNode) error {
	if next.ID == "" || next.ProjectID == "" || next.Version != old.Version+1 {
		return ErrInvalidRequest
	}
	if err := t.insertVersionedRecord(next.ProjectID, "route_node_version", next.ID+"/"+strconv.FormatInt(next.Version, 10), 1, next); err != nil {
		return err
	}
	if old.Version == 0 {
		return t.insertVersionedRecord(next.ProjectID, "route_node", next.ID, 1, next)
	}
	return t.compareAndSwapVersionedRecord(old.ProjectID, "route_node", old.ID, old.Version, next)
}
func (t gormTransaction) ProjectBindingPolicies(p Project) ([]governance.BindingPolicy, error) {
	if p.TemplateCopy == nil {
		return nil, ErrInvalidState
	}
	var value struct {
		BindingPolicies  []governance.BindingPolicy `json:"binding_policies"`
		SourcePolicyHash string                     `json:"source_policy_hash"`
	}
	if _, err := t.GetProjectRecord(p.ID, "template_binding", p.TemplateCopy.ID, &value); err != nil {
		return nil, err
	}
	hash, err := governance.HashBindingPolicies(value.BindingPolicies)
	if err != nil {
		return nil, err
	}
	if value.SourcePolicyHash == "" || hash != value.SourcePolicyHash {
		return nil, ErrVersionConflict
	}
	return value.BindingPolicies, nil
}
func HydrateSemanticChange(tx Transaction, change ChangeSet) (ChangeSet, error) {
	store, ok := tx.(semanticTransaction)
	if !ok {
		if len(change.SemanticDeltas) > 0 {
			return change, ErrInvalidState
		}
		return change, nil
	}
	plan, err := store.SemanticPlan(change.ProjectID, change.ID)
	if err != nil {
		return change, err
	}
	change.SemanticDeltas = plan
	return change, nil
}
func semanticRef(delta SemanticDelta) ObjectVersionRef {
	kind := delta.Kind
	if kind == "ProjectAssetScope" {
		kind = "ProjectAsset"
	}
	return ObjectVersionRef{Kind: kind, ID: delta.TargetID, Version: strconv.FormatInt(delta.ExpectedVersion, 10)}
}
func validSemanticDelta(delta SemanticDelta) bool {
	if delta.TargetID == "" || len(delta.TargetID) > 128 || delta.ExpectedVersion < 0 {
		return false
	}
	count := 0
	for _, found := range []bool{delta.ProposedClaim != nil, delta.ProposedAssetPolicy != nil, delta.ProposedBinding != nil, delta.ProposedRoute != nil, delta.ProposedCard != nil} {
		if found {
			count++
		}
	}
	if count != 1 {
		return false
	}
	switch delta.Kind {
	case "EvidenceCard":
		return delta.ProposedCard != nil && delta.ExpectedVersion > 0
	case "ResearchClaim":
		return delta.ProposedClaim != nil && delta.ExpectedVersion > 0
	case "ProjectAssetScope":
		return delta.ProposedAssetPolicy != nil && delta.ExpectedVersion > 0
	case "RuleBinding":
		return delta.ProposedBinding != nil && delta.ProposedBinding.RuleID == delta.TargetID && delta.ExpectedVersion > 0
	case "RouteNode":
		return delta.ProposedRoute != nil
	case "RouteEdge":
		return delta.ProposedEdge != nil
	}
	return false
}
func currentSemanticValue(tx Transaction, p Project, delta SemanticDelta) (SemanticCurrentValue, error) {
	out := SemanticCurrentValue{Ref: semanticRef(delta), SourceIDs: []string{}}
	var value any
	switch delta.Kind {
	case "EvidenceCard":
		card, _, err := loadStoredObject[EvidenceCard](tx, p.ID, EvidenceCardKind, delta.TargetID)
		if err != nil {
			return out, err
		}
		out.Ref.Version = strconv.FormatInt(card.Version, 10)
		out.SourceIDs = slices.Clone(card.SourceIDs)
		out.ProvenanceHash = hashImpactValue(struct {
			Basis        VersionBasis
			Confirmation *EvidenceConfirmation
		}{card.Basis, card.Confirmation})
		value = card
	case "ResearchClaim":
		claim, _, err := loadStoredObject[ResearchClaim](tx, p.ID, ResearchClaimKind, delta.TargetID)
		if err != nil {
			return out, err
		}
		out.Ref.Version = strconv.FormatInt(claim.Version, 10)
		value = claim
		port, err := agentObjectStore(tx)
		if err != nil {
			return out, err
		}
		cards, err := port.Objects(p.ID, EvidenceCardKind)
		if err != nil {
			return out, err
		}
		for _, row := range cards {
			var card EvidenceCard
			if err := json.Unmarshal(row.Payload, &card); err != nil {
				return out, err
			}
			if card.ClaimRef != nil && card.ClaimRef.ID == claim.ID {
				out.SourceIDs = append(out.SourceIDs, card.SourceIDs...)
			}
		}
		out.ProvenanceHash = hashImpactValue(claim.Basis)
	case "ProjectAssetScope":
		policy, ids, provenance, err := ReadProjectAssetPolicy(tx, p, delta.TargetID)
		if err != nil {
			return out, err
		}
		out.Ref.Version = strconv.FormatInt(policy.Version, 10)
		out.SourceIDs = ids
		out.ProvenanceHash = provenance
		value = policy
	case "RuleBinding":
		if p.TemplateCopy == nil || p.TemplateCopy.RuleSetSnapshot == nil {
			return out, ErrInvalidState
		}
		if err := template.ValidateRuleSet(*p.TemplateCopy.RuleSetSnapshot); err != nil {
			return out, err
		}
		found := false
		for _, binding := range p.TemplateCopy.RuleSetSnapshot.Bindings {
			if binding.RuleID == delta.TargetID {
				value = binding
				found = true
				if binding.TargetKind == "chapter" {
					chapter, err := tx.Chapter(p.ID, binding.TargetID)
					if err != nil {
						return out, err
					}
					out.SourceIDs = chapter.SourceIDs
				}
			}
		}
		if !found {
			return out, ErrNotFound
		}
		store, ok := tx.(semanticTransaction)
		if !ok {
			return out, ErrInvalidState
		}
		policies, err := store.ProjectBindingPolicies(p)
		if err != nil {
			return out, err
		}
		out.ProvenanceHash = hashImpactValue(struct {
			Hash     string
			Policies []governance.BindingPolicy
		}{p.TemplateCopy.RulesetHash, policies})
		out.Ref.Version = strconv.FormatInt(p.TemplateCopyVersion, 10)
	case "RouteNode":
		store, ok := tx.(semanticTransaction)
		if !ok {
			return out, ErrInvalidState
		}
		node, err := store.RouteNode(p.ID, delta.TargetID)
		if err == ErrNotFound && delta.ExpectedVersion == 0 {
			node = RouteNode{ID: delta.TargetID, ProjectID: p.ID, Inputs: []ObjectVersionRef{}}
		} else if err != nil {
			return out, err
		}
		out.Ref.Version = strconv.FormatInt(node.Version, 10)
		value = node
		for _, ref := range node.Inputs {
			state, err := currentImpactObject(tx, p, ref)
			if err != nil && !errors.Is(err, ErrVersionConflict) && !errors.Is(err, ErrInvalidState) {
				return out, err
			}
			out.SourceIDs = append(out.SourceIDs, state.SourceIDs...)
		}
		out.ProvenanceHash = hashImpactValue(node.Inputs)
	case "RouteEdge":
		store, ok := tx.(roadmapStore)
		if !ok {
			return out, ErrInvalidState
		}
		edge, err := store.RouteEdge(p.ID, delta.TargetID)
		if err == ErrNotFound && delta.ExpectedVersion == 0 {
			edge = RouteEdge{ID: delta.TargetID, ProjectID: p.ID}
		} else if err != nil {
			return out, err
		}
		out.Ref.Version = strconv.FormatInt(edge.Version, 10)
		value = edge
	default:
		return out, ErrInvalidRequest
	}
	body, err := json.Marshal(value)
	out.Body = body
	out.SourceIDs = sortedImpactSourceIDs(out.SourceIDs)
	return out, err
}
func validateSemanticProposal(tx Transaction, p Project, delta SemanticDelta) error {
	if !validSemanticDelta(delta) {
		return ErrInvalidRequest
	}
	switch delta.Kind {
	case "EvidenceCard":
		_, err := evidenceSemanticSources(tx, p, *delta.ProposedCard)
		return err
	case "ResearchClaim":
		input := delta.ProposedClaim
		if !input.Confirmed {
			return ErrConsentRequired
		}
		if strings.TrimSpace(input.Text) == "" || strings.TrimSpace(input.Scope) == "" || len(input.Text) > 10000 || !slices.Contains([]string{"active", "disputed", "superseded"}, input.Status) {
			return ErrInvalidRequest
		}
	case "ProjectAssetScope":
		if !slices.Contains([]string{"personal", "project", "team"}, delta.ProposedAssetPolicy.Scope) || delta.ProposedAssetPolicy.ModelUseAllowed && strings.TrimSpace(delta.ProposedAssetPolicy.Purpose) == "" {
			return ErrInvalidRequest
		}
		if delta.ProposedAssetPolicy.AssetID != "" && delta.ProposedAssetPolicy.AssetID != delta.TargetID {
			return ErrInvalidRequest
		}
	case "RuleBinding":
		store := tx.(semanticTransaction)
		policies, err := store.ProjectBindingPolicies(p)
		if err != nil {
			return err
		}
		_, err = governance.ApplyBindingOverrides(*p.TemplateCopy.RuleSetSnapshot, "project-"+p.ID, strconv.FormatInt(p.TemplateCopyVersion+1, 10), policies, []governance.BindingOverride{*delta.ProposedBinding})
		return semanticBindingError(err)
	case "RouteNode":
		node := delta.ProposedRoute
		if strings.TrimSpace(node.Name) == "" || len(node.Name) > 240 || len(node.Inputs) > 100 || !slices.Contains(RoadmapNodeStatuses, node.Status) {
			return ErrInvalidRequest
		}
		if routeRequiredByRules(p, delta.TargetID) && (node.Status == "retired" || !node.Required) {
			return ErrInvalidState
		}
		for _, ref := range node.Inputs {
			if ref.Kind == "RouteNode" && ref.ID == delta.TargetID {
				return ErrInvalidRequest
			}
			if _, err := currentImpactObject(tx, p, ref); err != nil {
				return err
			}
		}
	case "RouteEdge":
		edge := delta.ProposedEdge
		if strings.TrimSpace(edge.SourceID) == "" || strings.TrimSpace(edge.TargetID) == "" || edge.SourceID == edge.TargetID ||
			strings.TrimSpace(edge.Relation) == "" || len(edge.Relation) > 64 {
			return ErrInvalidRequest
		}
		// Both endpoints must be route nodes of this project: an edge between
		// objects that do not exist is not a relation the roadmap can hold.
		for _, id := range []string{edge.SourceID, edge.TargetID} {
			store, ok := tx.(semanticTransaction)
			if !ok {
				return ErrInvalidState
			}
			if _, err := store.RouteNode(p.ID, id); err != nil {
				return err
			}
		}
	}
	return nil
}
func semanticValueHash(value SemanticCurrentValue) string {
	return hashImpactValue(struct {
		Ref        ObjectVersionRef
		Body       json.RawMessage
		Provenance string
	}{value.Ref, value.Body, value.ProvenanceHash})
}
func (s *Service) PinSemanticDeltas(ctx context.Context, tx Transaction, actor Actor, p Project, changeID string, deltas []SemanticDelta) ([]SemanticDelta, error) {
	if err := validateCurrentCardClaimBatch(tx, p, deltas); err != nil {
		return nil, err
	}
	store, ok := tx.(semanticTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	seen := map[string]bool{}
	pinned := make([]SemanticDelta, 0, len(deltas))
	for _, delta := range deltas {
		key := delta.Kind + "/" + delta.TargetID
		if seen[key] || !validSemanticDelta(delta) {
			return nil, ErrInvalidRequest
		}
		seen[key] = true
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, p.ID, AuthorizationWrite, "")); err != nil {
			return nil, err
		}
		if delta.Kind == "RuleBinding" {
			if _, err := s.resolveTemplate(ctx, tx, actor, p.TemplateCopy.SourceTemplateID, p.TemplateCopy.SourceTemplateVersion); err != nil {
				return nil, err
			}
		}
		value, err := currentSemanticValue(tx, p, delta)
		if err != nil {
			return nil, err
		}
		if value.Ref.Version != strconv.FormatInt(delta.ExpectedVersion, 10) {
			return nil, ErrVersionConflict
		}
		if err := validateSemanticProposal(tx, p, delta); err != nil {
			return nil, err
		}
		delta.Before = value.Body
		delta.BeforeHash = semanticValueHash(value)
		delta.ProvenanceHash = value.ProvenanceHash
		delta.SourceIDs = value.SourceIDs
		if delta.ProposedCard != nil {
			ids, e := evidenceSemanticSources(tx, p, *delta.ProposedCard)
			if e != nil {
				return nil, e
			}
			delta.SourceIDs = append(delta.SourceIDs, ids...)
		}
		if delta.ProposedBinding != nil && delta.ProposedBinding.TargetKind == "chapter" && delta.ProposedBinding.TargetID != "" {
			chapter, e := tx.Chapter(p.ID, delta.ProposedBinding.TargetID)
			if e != nil {
				return nil, e
			}
			delta.SourceIDs = append(delta.SourceIDs, chapter.SourceIDs...)
		}
		if delta.ProposedRoute != nil {
			for _, ref := range delta.ProposedRoute.Inputs {
				state, e := currentImpactObject(tx, p, ref)
				if e != nil {
					return nil, e
				}
				delta.SourceIDs = append(delta.SourceIDs, state.SourceIDs...)
			}
		}
		delta.SourceIDs = sortedImpactSourceIDs(delta.SourceIDs)
		pinned = append(pinned, delta)
	}
	if err := store.SaveSemanticPlan(p.ID, changeID, pinned); err != nil {
		return nil, err
	}
	return pinned, nil
}
func ValidateSemanticDeltas(tx Transaction, p Project, change ChangeSet) error {
	if err := validateCurrentCardClaimBatch(tx, p, change.SemanticDeltas); err != nil {
		return err
	}
	for _, delta := range change.SemanticDeltas {
		if !validSemanticDelta(delta) || delta.BeforeHash == "" {
			return ErrInvalidRequest
		}
		current, err := currentSemanticValue(tx, p, delta)
		if err != nil {
			return err
		}
		if current.Ref.Version != strconv.FormatInt(delta.ExpectedVersion, 10) || semanticValueHash(current) != delta.BeforeHash {
			return ErrVersionConflict
		}
		if err := validateSemanticProposal(tx, p, delta); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) ApplySemanticDeltas(ctx context.Context, tx Transaction, actor Actor, before Project, next *Project, change ChangeSet) error {
	if err := RequireHumanDecision(ctx); err != nil {
		return err
	}
	if next == nil || next.CurrentContextRevision != before.CurrentContextRevision+1 || !projectOwner(before, actor.UserID) {
		return ErrInvalidState
	}
	if err := ValidateSemanticDeltas(tx, before, change); err != nil {
		return err
	}
	bindingOverrides := []governance.BindingOverride{}
	for _, delta := range change.SemanticDeltas {
		switch delta.Kind {
		case "EvidenceCard":
			if err := s.applyEvidenceCardDelta(ctx, tx, actor, before, *next, delta, change.Reason); err != nil {
				return err
			}
		case "ResearchClaim":
			claimInput := *delta.ProposedClaim
			claimInput.Reason = change.Reason
			if _, err := s.ApplyResearchClaimDelta(tx, actor, *next, delta.TargetID, delta.ExpectedVersion, claimInput); err != nil {
				return err
			}
		case "ProjectAssetScope":
			policyInput := *delta.ProposedAssetPolicy
			policyInput.Reason = change.Reason
			if _, err := s.ApplyProjectAssetPolicyDelta(tx, actor, *next, delta.TargetID, delta.ExpectedVersion, policyInput); err != nil {
				return err
			}
		case "RuleBinding":
			if _, err := s.resolveTemplate(ctx, tx, actor, before.TemplateCopy.SourceTemplateID, before.TemplateCopy.SourceTemplateVersion); err != nil {
				return err
			}
			bindingOverrides = append(bindingOverrides, *delta.ProposedBinding)
		case "RouteNode":
			store := tx.(semanticTransaction)
			old, err := store.RouteNode(before.ID, delta.TargetID)
			if err == ErrNotFound && delta.ExpectedVersion == 0 {
				old = RouteNode{ID: delta.TargetID, ProjectID: before.ID}
			} else if err != nil {
				return err
			}
			node := *delta.ProposedRoute
			node.ID = delta.TargetID
			node.ProjectID = before.ID
			node.Version = old.Version + 1
			node.ContextRevision = next.CurrentContextRevision
			node.ActorID = actor.UserID
			node.Reason = change.Reason
			node.UpdatedAt = time.Now().UTC()
			node.Required = node.Required || routeRequiredByRules(before, node.ID)
			if err := store.AppendRouteNode(old, node); err != nil {
				return err
			}
			for _, ref := range node.Inputs {
				if err := StoreConfirmedDependency(tx, DependencyEdge{ProjectID: before.ID, Source: ref, Target: ObjectVersionRef{Kind: "RouteNode", ID: node.ID, Version: strconv.FormatInt(node.Version, 10)}, Relation: "route_input", Action: "REVIEW_REQUIRED", Origin: "user", ActorID: actor.UserID, Reason: change.Reason}); err != nil {
					return err
				}
			}
		case "RouteEdge":
			edgeStore, ok := tx.(roadmapStore)
			if !ok {
				return ErrInvalidState
			}
			oldEdge, err := edgeStore.RouteEdge(before.ID, delta.TargetID)
			if err == ErrNotFound && delta.ExpectedVersion == 0 {
				oldEdge = RouteEdge{ID: delta.TargetID, ProjectID: before.ID}
			} else if err != nil {
				return err
			}
			edge := *delta.ProposedEdge
			edge.ID = delta.TargetID
			edge.ProjectID = before.ID
			edge.Version = oldEdge.Version + 1
			edge.ContextRevision = next.CurrentContextRevision
			edge.ActorID = actor.UserID
			edge.Reason = change.Reason
			edge.UpdatedAt = time.Now().UTC()
			if err := edgeStore.InsertRouteEdge(edge); err != nil {
				return err
			}
		}
		if err := recordAuditEvent(tx, actor, before.ID, "semantic.apply", delta.TargetID, map[string]any{"kind": delta.Kind, "before_hash": delta.BeforeHash, "target_context_revision": next.CurrentContextRevision}); err != nil {
			return err
		}
	}
	if len(bindingOverrides) > 0 {
		if change.TemplateUpgrade != nil {
			return ErrInvalidRequest
		}
		store := tx.(semanticTransaction)
		policies, err := store.ProjectBindingPolicies(before)
		if err != nil {
			return err
		}
		effective, err := governance.ApplyBindingOverrides(*before.TemplateCopy.RuleSetSnapshot, "project-"+before.ID, strconv.FormatInt(before.TemplateCopyVersion+1, 10), policies, bindingOverrides)
		if err != nil {
			return semanticBindingError(err)
		}
		definition := template.Clone(before.TemplateCopy.Definition)
		definition.Rules, err = template.CompileRuleSet(effective)
		if err != nil {
			return err
		}
		definition, err = template.WithHashes(definition)
		if err != nil {
			return err
		}
		copy, err := projectTemplateCopy(actor, before.ID, before.TemplateCopyVersion+1, definition, TemplateCopyBound)
		if err != nil {
			return err
		}
		copy.RuleSetSnapshot = &effective
		copy.SourceRuleSetID = before.TemplateCopy.SourceRuleSetID
		copy.SourceRuleSetVersion = before.TemplateCopy.SourceRuleSetVersion
		copy.SourceRuleSetHash = before.TemplateCopy.SourceRuleSetHash
		copy.RulesetHash = effective.RulesetHash
		copy.BindingPolicies = before.TemplateCopy.BindingPolicies
		copy.SourcePolicyHash = before.TemplateCopy.SourcePolicyHash
		next.TemplateCopy = copy
		next.TemplateCopyVersion = copy.Version
	}
	return nil
}

// blockingPresenceBindings returns the field ids a rule set declares through
// blocking presence rules.
//
// ruleID narrows the answer to one rule (the contract's route node rule);
// an empty ruleID means "any rule".
//
// This is the **one** place that knows what a blocking presence binding is,
// so the two call sites - the template's required fields and the contract's
// route node rule - cannot drift apart. The severity must be blocking (a
// warning presence binding is an advisory, not a declaration), the definition
// must be presence and the target must be a field (a binding over a chapter
// says nothing about a spec field).
func blockingPresenceBindings(set *template.RuleSet, ruleID string) []string {
	if set == nil {
		return nil
	}
	ids := []string{}
	for _, binding := range set.Bindings {
		if !binding.Enabled || binding.DefinitionID != "presence" ||
			binding.Severity != template.SeverityBlocking || binding.TargetKind != "field" {
			continue
		}
		if ruleID != "" && binding.RuleID != ruleID {
			continue
		}
		if binding.TargetID == "" {
			continue
		}
		ids = append(ids, binding.TargetID)
	}
	return ids
}

func routeRequiredByRules(p Project, id string) bool {
	if p.TemplateCopy == nil {
		return false
	}
	if slices.Contains(p.TemplateCopy.Definition.RequiredFields, id) {
		return true
	}
	return slices.Contains(blockingPresenceBindings(p.TemplateCopy.RuleSetSnapshot, ""), id)
}

// routeNodeRequiredIDs returns the ids of the route nodes the project template
// copy declares as required.
//
// The template contract declares a required route node by binding the blocking
// presence rule to it: template.RuleRouteNodeRequired is the "blocking presence
// rule bound to the route node the demo template declares as required ... the
// rule that turns 'the required route node is missing / not reviewed / behind'
// into a blocking validation issue". The id such a rule binds is a route node
// id, and the roadmap - not the spec - owns the value of that field.
//
// The rule binding is what tells a route node id apart from an ordinary spec
// field: the template's required fields carry both, and only the presence
// binding names the route node. The id must also be required by the template
// rules (routeRequiredByRules), because an id the rules do not require is a
// free field the spec owns.
func routeNodeRequiredIDs(p Project) []string {
	if p.TemplateCopy == nil {
		return nil
	}
	ids := []string{}
	seen := map[string]bool{}
	declare := func(id string) {
		if id == "" || seen[id] || !routeRequiredByRules(p, id) {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, rule := range p.TemplateCopy.Definition.Rules {
		if rule.ID != template.RuleRouteNodeRequired || rule.Kind != "presence" || rule.Severity != template.SeverityBlocking {
			continue
		}
		if kind, _ := rule.Parameters["target_kind"].(string); kind != "field" {
			continue
		}
		id, _ := rule.Parameters["target_id"].(string)
		declare(id)
	}
	for _, set := range []*template.RuleSet{p.TemplateCopy.RuleSetSnapshot, p.TemplateCopy.Definition.RuleSetSnapshot} {
		for _, id := range blockingPresenceBindings(set, template.RuleRouteNodeRequired) {
			declare(id)
		}
	}
	slices.Sort(ids)
	return ids
}
func RouteEvaluationFields(tx Transaction, p Project) (map[string]string, error) {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	nodes, err := store.RouteNodes(p.ID)
	if err != nil {
		return nil, err
	}
	fields := maps.Clone(p.Spec)
	if fields == nil {
		fields = map[string]string{}
	}
	// The roadmap owns the value of every route node id the template declares
	// as required. A declared node the roadmap does not hold yet has no value:
	// the placeholder the spec may carry under that id must not stand in for
	// the missing node, or the template would read the project as complete
	// while the required route is not there.
	for _, id := range routeNodeRequiredIDs(p) {
		fields[id] = ""
	}
	for _, node := range nodes {
		fields[node.ID] = ""
		if node.Status == "ready" && node.ContextRevision <= p.CurrentContextRevision {
			valid := true
			for _, ref := range node.Inputs {
				state, err := currentImpactObject(tx, p, ref)
				if err != nil || !routeInputReviewed(state, ref, p) {
					valid = false
				}
			}
			if valid {
				fields[node.ID] = node.Name
			}
		}
	}
	return fields, nil
}
func (s *Service) ListRouteNodes(ctx context.Context, actor Actor, projectID string) ([]RouteNode, error) {
	var nodes []RouteNode
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		store, ok := tx.(semanticTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		nodes, err = store.RouteNodes(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	visible := []RouteNode{}
	for _, node := range nodes {
		var ids []string
		err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
			if e != nil {
				return e
			}
			ids, e = impactSourceIDsForRefs(tx, p, node.Inputs)
			return e
		})
		if err == nil && s.checkSources(ctx, actor, projectID, ids) == nil {
			visible = append(visible, node)
		}
	}
	return visible, nil
}

func currentRouteImpactObject(tx Transaction, p Project, ref ObjectVersionRef, seen map[string]bool) (ImpactObjectState, error) {
	state := ImpactObjectState{Ref: ref, SourceIDs: []string{}}
	if seen[ref.ID] {
		return state, ErrInvalidState
	}
	seen[ref.ID] = true
	defer delete(seen, ref.ID)
	store, ok := tx.(semanticTransaction)
	if !ok {
		return state, ErrImpactObjectUnavailable
	}
	node, err := store.RouteNode(p.ID, ref.ID)
	if err != nil {
		return state, err
	}
	state.Ref.Version = strconv.FormatInt(node.Version, 10)
	state.State = "CURRENT"
	if node.Status == "retired" {
		state.State = "SUPERSEDED"
	}
	state.ContextRevision = node.ContextRevision
	state.Confirmed = node.Status == "ready" && node.ActorID != "" && node.ContextRevision > 0 && node.ContextRevision <= p.CurrentContextRevision
	for _, input := range node.Inputs {
		var source ImpactObjectState
		if input.Kind == "RouteNode" {
			source, err = currentRouteImpactObject(tx, p, input, seen)
		} else {
			source, err = currentImpactObject(tx, p, input)
		}
		if err != nil {
			return state, err
		}
		state.SourceIDs = append(state.SourceIDs, source.SourceIDs...)
		state.Confirmed = state.Confirmed && routeInputReviewed(source, input, p)
	}
	return state, nil
}
func semanticSourceIDs(tx Transaction, p Project, deltas []SemanticDelta) ([]string, error) {
	if err := validateCurrentCardClaimBatch(tx, p, deltas); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, delta := range deltas {
		current, err := currentSemanticValue(tx, p, delta)
		if err != nil {
			return nil, err
		}
		ids = append(ids, delta.SourceIDs...)
		ids = append(ids, current.SourceIDs...)
		if delta.ProposedCard != nil {
			proposedIDs, e := evidenceSemanticSources(tx, p, *delta.ProposedCard)
			if e != nil {
				return nil, e
			}
			ids = append(ids, proposedIDs...)
		}
		if delta.ProposedBinding != nil && delta.ProposedBinding.TargetKind == "chapter" && delta.ProposedBinding.TargetID != "" {
			chapter, e := tx.Chapter(p.ID, delta.ProposedBinding.TargetID)
			if e != nil {
				return nil, e
			}
			ids = append(ids, chapter.SourceIDs...)
		}
		if delta.ProposedRoute != nil {
			for _, ref := range delta.ProposedRoute.Inputs {
				state, err := currentImpactObject(tx, p, ref)
				if err != nil {
					return nil, err
				}
				ids = append(ids, state.SourceIDs...)
			}
		}
	}
	return sortedImpactSourceIDs(ids), nil
}
func (s *Service) CheckSemanticChangeAccess(ctx context.Context, actor Actor, projectID string, change ChangeSet) error {
	ids := []string{}
	for _, delta := range change.SemanticDeltas {
		ids = append(ids, delta.SourceIDs...)
	}
	return s.checkSources(ctx, actor, projectID, sortedImpactSourceIDs(ids))
}
func RouteStabilizationBlockers(tx Transaction, p Project) ([]ImpactBlocker, error) {
	store, ok := tx.(semanticTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	nodes, err := store.RouteNodes(p.ID)
	if err != nil {
		return nil, err
	}
	out := []ImpactBlocker{}
	for _, node := range nodes {
		if !node.Required && !routeRequiredByRules(p, node.ID) {
			continue
		}
		state, err := currentRouteImpactObject(tx, p, ObjectVersionRef{Kind: "RouteNode", ID: node.ID, Version: strconv.FormatInt(node.Version, 10)}, map[string]bool{})
		if err != nil || !state.Confirmed {
			out = append(out, ImpactBlocker{Code: "required_route_not_current", TargetID: node.ID, Reason: "必经技术路线未就绪、未复核或依赖版本已变化"})
		}
	}
	return out, nil
}
func validateExtendedChangeInput(input ExtendedChangeSetInput) error {
	if err := validateCardClaimBatch(input.SemanticDeltas); err != nil {
		return err
	}
	if input.ExpectedContextRevision < 1 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 || len(input.SemanticDeltas) > 100 {
		return ErrInvalidRequest
	}
	if len(input.SemanticDeltas) == 0 {
		return validateChangeSetInput(CreateChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Fields: input.Fields, AffectedChapterIDs: input.AffectedChapterIDs, Reason: input.Reason})
	}
	if len(input.Fields) > 0 {
		if err := validateChangeSetInput(CreateChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Fields: input.Fields, AffectedChapterIDs: input.AffectedChapterIDs, Reason: input.Reason}); err != nil {
			return err
		}
	}
	for _, delta := range input.SemanticDeltas {
		if delta.ProposedCard != nil && !delta.ProposedCard.Confirmed {
			return ErrConsentRequired
		}
		if delta.ProposedClaim != nil && !delta.ProposedClaim.Confirmed {
			return ErrConsentRequired
		}
		if !validSemanticDelta(delta) {
			return ErrInvalidRequest
		}
	}
	return nil
}

// Reviewing route feasibility does not assert scientific confirmation of a claim.
func routeInputReviewed(state ImpactObjectState, ref ObjectVersionRef, p Project) bool {
	if state.Ref != ref {
		return false
	}
	if ref.Kind == "ResearchClaim" {
		return slices.Contains([]string{"active", "ACTIVE"}, state.State)
	}
	return state.Confirmed
}

func semanticBindingError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, governance.ErrInvalidRequest) || errors.Is(err, template.ErrInvalidRuleSet) || errors.Is(err, template.ErrInvalidDefinition) {
		return fmt.Errorf("%w: reviewed binding policy rejected proposal: %v", ErrInvalidRequest, err)
	}
	return err
}
