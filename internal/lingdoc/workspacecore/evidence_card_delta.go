package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

var ErrSemanticBatchStagingRequired = clienterror.Wrap(clienterror.Form{Status: 400, Code: "invalid_request", Message: "关联主张与证据卡请分两次正式变更：先应用主张，再按新主张版本复核并提交证据卡变更。", Retryable: false}, "invalid_request: change the associated claim and evidence card in separate ChangeSets; revalidate the card against the new claim version", ErrInvalidRequest)

var ErrFormalChangeRequired = fmt.Errorf("%w: confirmed or formally used object requires a formal ChangeSet", ErrInvalidState)

// This is a mutation-only guard. Cached operations still reauthorize through
// runAgentOperation, but do not repeat a mutation against a later formal state.
// A confirmed dependency edge alone does not make a collected candidate formal.
func requireDraftEdit(tx Transaction, p Project, ref ObjectVersionRef) error {
	formal, err := formalG2Object(tx, p, ref)
	if err != nil {
		return err
	}
	if formal {
		return fmt.Errorf("%w: %s", ErrFormalChangeRequired, ref.Kind)
	}
	return nil
}

func formalG2Object(tx Transaction, p Project, ref ObjectVersionRef) (bool, error) {
	// An applied semantic object remains governed when its new version awaits
	// source confirmation. A draft/assessed plan alone never makes it formal.
	changes, ok := tx.(changeSetTransaction)
	if !ok {
		return false, ErrImpactObjectUnavailable
	}
	plans, ok := tx.(semanticTransaction)
	if !ok {
		return false, ErrImpactObjectUnavailable
	}
	rows, err := changes.ChangeSets(p.ID)
	if err != nil {
		return false, err
	}
	for _, change := range rows {
		if change.Status != ChangeSetStatusApplied && change.Status != ChangeSetStatusStabilized {
			continue
		}
		plan, err := plans.SemanticPlan(p.ID, change.ID)
		if err != nil {
			return false, err
		}
		for _, delta := range plan {
			if delta.Kind == ref.Kind && delta.TargetID == ref.ID {
				return true, nil
			}
		}
	}
	port, err := agentObjectStore(tx)
	if err != nil {
		return false, err
	}
	cards, err := port.Objects(p.ID, EvidenceCardKind)
	if err != nil {
		return false, err
	}
	roots := []ObjectVersionRef{ref}
	for _, row := range cards {
		var card EvidenceCard
		if err := json.Unmarshal(row.Payload, &card); err != nil {
			return false, err
		}
		cardRef := ObjectVersionRef{Kind: "EvidenceCard", ID: card.ID, Version: strconv.FormatInt(card.Version, 10)}
		if ref == cardRef && (card.Status == "CONFIRMED" || card.Confirmation != nil) {
			return true, nil
		}
		if ref.Kind == "ResearchClaim" && card.ClaimRef != nil && card.ClaimRef.ID == ref.ID && strconv.FormatInt(card.ClaimRef.Version, 10) == ref.Version {
			if card.Status == "CONFIRMED" || card.Confirmation != nil {
				return true, nil
			}
			// Claims point into their cards semantically; stored claim_support
			// edges run the other way, so include candidate cards as traversal roots.
			roots = append(roots, cardRef)
		}
	}
	edges, ok := tx.(interface {
		DependencyEdges(string) ([]DependencyEdge, error)
	})
	if !ok {
		return false, ErrImpactObjectUnavailable
	}
	all, err := edges.DependencyEdges(p.ID)
	if err != nil {
		return false, err
	}
	queue := roots
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[refIdentity(current)] {
			continue
		}
		seen[refIdentity(current)] = true
		for _, edge := range all {
			if edge.Status != "confirmed" || edge.Source != current {
				continue
			}
			switch edge.Target.Kind {
			case "ChapterVersion":
				chapter, err := tx.Chapter(p.ID, edge.Target.ID)
				if err != nil {
					return false, err
				}
				if chapter.ConfirmationValid && chapter.CurrentVersionID != nil && *chapter.CurrentVersionID == edge.Target.Version {
					return true, nil
				}
			case "RouteNode":
				store, ok := tx.(semanticTransaction)
				if !ok {
					return false, ErrImpactObjectUnavailable
				}
				node, err := store.RouteNode(p.ID, edge.Target.ID)
				if err != nil {
					return false, err
				}
				if strconv.FormatInt(node.Version, 10) == edge.Target.Version && node.Status != "retired" && node.ActorID != "" && node.ContextRevision > 0 && node.ContextRevision <= p.CurrentContextRevision {
					return true, nil
				}
			case "EvidenceCard":
				card, _, err := loadStoredObject[EvidenceCard](tx, p.ID, EvidenceCardKind, edge.Target.ID)
				if err != nil {
					return false, err
				}
				if strconv.FormatInt(card.Version, 10) == edge.Target.Version && (card.Status == "CONFIRMED" || card.Confirmation != nil) {
					return true, nil
				}
			}
			queue = append(queue, edge.Target)
		}
	}
	return false, nil
}

// Only user-editable semantics. No version, author, source_ids, status or old
// confirmation can be submitted as the new card's authority.
type EvidenceCardSemanticInput struct {
	ClaimRef               *VersionRef  `json:"claim_ref,omitempty"`
	AnchorRefs             []VersionRef `json:"anchor_refs"`
	SupportType            string       `json:"support_type"`
	Scope                  string       `json:"scope"`
	ScopeChapterIDs        []string     `json:"scope_chapter_ids"`
	Limitations            string       `json:"limitations"`
	Gaps                   string       `json:"gaps"`
	AlternativeExplanation string       `json:"alternative_explanation"`
	Confirmed              bool         `json:"confirmed"`
}

func cardSemanticInput(card EvidenceCard) EvidenceCardSemanticInput {
	return EvidenceCardSemanticInput{ClaimRef: card.ClaimRef, AnchorRefs: slices.Clone(card.AnchorRefs), SupportType: card.SupportType, Scope: card.Scope, ScopeChapterIDs: slices.Clone(card.ScopeChapterIDs), Limitations: card.Limitations, Gaps: card.Gaps, AlternativeExplanation: card.AlternativeExplanation, Confirmed: true}
}

func evidenceSemanticSources(tx Transaction, p Project, input EvidenceCardSemanticInput) ([]string, error) {
	if !input.Confirmed {
		return nil, ErrConsentRequired
	}
	if len(input.AnchorRefs) == 0 || len(input.AnchorRefs) > 20 || input.ClaimRef == nil || strings.TrimSpace(input.Scope) == "" || len(input.ScopeChapterIDs) == 0 {
		return nil, ErrEvidenceIncomplete
	}
	if !slices.Contains([]string{"supports", "refutes", "qualifies", "background"}, input.SupportType) {
		return nil, ErrInvalidRequest
	}
	claim, _, err := loadStoredObject[ResearchClaim](tx, p.ID, ResearchClaimKind, input.ClaimRef.ID)
	if err != nil {
		return nil, err
	}
	if claim.Version != input.ClaimRef.Version || claim.Status == "superseded" || claim.Basis.SpecRevision != p.SpecRevision {
		return nil, ErrVersionConflict
	}
	local, ok := tx.(assetPolicyTransaction)
	if !ok {
		return nil, ErrImpactObjectUnavailable
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, ref := range input.AnchorRefs {
		anchor, _, err := loadStoredObject[SourceAnchor](tx, p.ID, SourceAnchorKind, ref.ID)
		if err != nil {
			return nil, err
		}
		if seen[ref.ID] || anchor.Version != ref.Version || anchor.Status != "CURRENT" {
			return nil, ErrVersionConflict
		}
		seen[ref.ID] = true
		current, err := local.IsAnchorCurrent(anchor)
		if err != nil {
			return nil, err
		}
		if !current {
			return nil, ErrEvidenceIncomplete
		}
		ids = append(ids, anchor.SourceID)
	}
	for _, id := range input.ScopeChapterIDs {
		if _, err := tx.Chapter(p.ID, id); err != nil {
			return nil, err
		}
	}
	// Replacement may introduce a different claim. Its current provenance is
	// checked through the same source decoder as an ordinary claim delta.
	value, err := currentSemanticValue(tx, p, SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID})
	if err != nil {
		return nil, err
	}
	return sortedImpactSourceIDs(append(ids, value.SourceIDs...)), nil
}

func (s *Service) applyEvidenceCardDelta(ctx context.Context, tx Transaction, actor Actor, before Project, next Project, delta SemanticDelta, reason string) error {
	if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, before.ID, AuthorizationEvidenceWrite, "")); err != nil {
		return err
	}
	if !projectOwner(before, actor.UserID) || before.Status != "active" || next.ProjectVersion != before.ProjectVersion+1 || next.CurrentContextRevision != before.CurrentContextRevision+1 {
		return ErrInvalidState
	}
	input := *delta.ProposedCard
	if _, err := evidenceSemanticSources(tx, before, input); err != nil {
		return err
	}
	old, stored, err := loadStoredObject[EvidenceCard](tx, before.ID, EvidenceCardKind, delta.TargetID)
	if err != nil {
		return err
	}
	if old.Version != delta.ExpectedVersion {
		return ErrVersionConflict
	}
	sourceIDs := []string{}
	for _, ref := range input.AnchorRefs {
		anchor, _, err := loadStoredObject[SourceAnchor](tx, before.ID, SourceAnchorKind, ref.ID)
		if err != nil {
			return err
		}
		sourceIDs = append(sourceIDs, anchor.SourceID)
	}
	// A semantic edit cannot silently remove previously recorded academic provenance.
	// A stale or mismatched association remains attached and fails closed on use.
	card := EvidenceCard{ID: old.ID, ProjectID: before.ID, Version: old.Version + 1, ClaimRef: input.ClaimRef, AnchorRefs: slices.Clone(input.AnchorRefs), AcademicAssociationRefs: slices.Clone(old.AcademicAssociationRefs), SourceIDs: sortedImpactSourceIDs(sourceIDs), SupportType: input.SupportType, Scope: input.Scope, ScopeChapterIDs: sortedUniqueIDs(input.ScopeChapterIDs), Limitations: input.Limitations, Gaps: input.Gaps, AlternativeExplanation: input.AlternativeExplanation, Status: "CANDIDATE", Basis: versionBasisFor(next, Chapter{}), CreatedBy: actor.UserID, CreatedAt: time.Now().UTC()}
	if err := storeStoredObject(tx, stored, before.ID, EvidenceCardKind, card.ID, card.Version, card); err != nil {
		return err
	}
	if err := publishCardDependencies(tx, actor, next, card); err != nil {
		return err
	}
	return recordAgentAuditEvent(tx, actor, before.ID, "g2.evidence.semantic_delta", card.ID, reason, map[string]any{"previous_version": old.Version, "version": card.Version, "context_revision": next.CurrentContextRevision})
}

func sameCardSemanticInput(a, b EvidenceCardSemanticInput) bool {
	a.Confirmed, b.Confirmed = false, false
	a.ScopeChapterIDs, b.ScopeChapterIDs = sortedUniqueIDs(a.ScopeChapterIDs), sortedUniqueIDs(b.ScopeChapterIDs)
	return reflect.DeepEqual(a, b)
}

// The existing engine resolves each proposed ref against the current basis.
// It does not plan future versions of another semantic target in the same batch.
func validateCardClaimBatch(deltas []SemanticDelta) error {
	claims := map[string]bool{}
	for _, delta := range deltas {
		if delta.Kind == "ResearchClaim" {
			claims[delta.TargetID] = true
		}
	}
	for _, delta := range deltas {
		if delta.ProposedCard != nil && delta.ProposedCard.ClaimRef != nil && claims[delta.ProposedCard.ClaimRef.ID] {
			return ErrSemanticBatchStagingRequired
		}
	}
	return nil
}

func validateCurrentCardClaimBatch(tx Transaction, p Project, deltas []SemanticDelta) error {
	if err := validateCardClaimBatch(deltas); err != nil {
		return err
	}
	claims := map[string]bool{}
	for _, delta := range deltas {
		if delta.Kind == "ResearchClaim" {
			claims[delta.TargetID] = true
		}
	}
	if len(claims) == 0 {
		return nil
	}
	for _, delta := range deltas {
		if delta.Kind != "EvidenceCard" {
			continue
		}
		card, _, err := loadStoredObject[EvidenceCard](tx, p.ID, EvidenceCardKind, delta.TargetID)
		if err != nil {
			return err
		}
		// A card moving to a different claim would still be invalidated by the
		// claim writer before its own append. Reject that old association too.
		if card.ClaimRef != nil && claims[card.ClaimRef.ID] {
			return ErrSemanticBatchStagingRequired
		}
	}
	return nil
}
