package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// Neither this key nor the immutable proof fields can be populated by HTTP DTOs.
type candidateEvidenceProofKey struct{}
type candidateEvidenceProof struct {
	actor                                                                                            Actor
	projectID, chapterID, identityHash, basisHash, refsHash, usedIDsHash, sourceDigest, policyDigest string
	sourceIDs                                                                                        []string
	selectionJSON                                                                                    []byte
}

func candidateIdentityHash(ctx context.Context) string {
	principal, principalPresent := types.PrincipalFromContext(ctx)
	scope, scopePresent := types.TenantAPIKeyScopeFromContext(ctx)
	tenant, tenantPresent := types.TenantIDFromContext(ctx)
	return hashImpactValue(struct {
		Caller           types.Caller
		Principal        types.Principal
		PrincipalPresent bool
		Scope            types.TenantAPIKeyScope
		ScopePresent     bool
		ExecutionTenant  uint64
		TenantPresent    bool
	}{types.CallerFromContext(ctx), principal, principalPresent, scope, scopePresent, tenant, tenantPresent})
}
func candidateFormalBasisHash(p Project) string {
	var copyID, content, rules string
	if p.TemplateCopy != nil {
		copyID = p.TemplateCopy.ID
		content = p.TemplateCopy.ContentHash
		rules = p.TemplateCopy.RulesetHash
	}
	return hashImpactValue(struct {
		ProjectID                  string
		Spec, Context, CopyVersion int64
		CopyID, Content, Rules     string
	}{p.ID, p.SpecRevision, p.CurrentContextRevision, p.TemplateCopyVersion, copyID, content, rules})
}
func candidateRefsHash(refs []VersionRef) string {
	refs = slices.Clone(refs)
	for i := range refs {
		if refs[i].Kind == "" {
			refs[i].Kind = "EvidenceCard"
		}
	}
	slices.SortFunc(refs, func(a, b VersionRef) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return hashImpactValue(refs)
}

// PrepareCandidateGenerationEvidence performs all real source/origin/KB IO
// before the adoption tx takes SQLite's single pooled connection.
func (s *Service) PrepareCandidateGenerationEvidence(ctx context.Context, actor Actor, projectID, chapterID string, refs []VersionRef, usedSourceIDs []string) (context.Context, EvidenceSelection, error) {
	selected := EvidenceSelection{Cards: []EvidenceCard{}, Sources: []AgentSourceRef{}, Pending: []VersionRef{}}
	if s == nil {
		return ctx, selected, ErrInvalidState
	}
	caller := types.CallerFromContext(ctx)
	execution, ok := types.TenantIDFromContext(ctx)
	if !ok || execution != actor.TenantID || caller.TenantID != actor.TenantID || caller.UserID != actor.UserID {
		return ctx, selected, ErrNotFound
	}
	if err := s.Authorize(ctx, actor, projectID, "write:"+chapterID); err != nil {
		return ctx, selected, err
	}
	var err error
	if len(refs) > 0 {
		for _, ref := range refs {
			if ref.ID == "" || ref.Version < 1 || (ref.Kind != "" && ref.Kind != "EvidenceCard") {
				return ctx, selected, ErrInvalidRequest
			}
		}
		selected, err = s.SelectGenerationEvidence(ctx, actor, projectID, chapterID, refs, true)
		if err != nil {
			return ctx, selected, err
		}
	} else {
		if len(usedSourceIDs) > 0 && s.agentRuntime.Sources == nil {
			return ctx, selected, ErrDependencyUnavailable
		}
		for _, id := range sortedImpactSourceIDs(usedSourceIDs) {
			source, e := s.readResearchSource(ctx, actor, projectID, chapterID, id)
			if e != nil {
				return ctx, selected, e
			}
			if source.Status != "available" || source.ProcessingState != "ready" || !source.ModelUseAllowed {
				return ctx, selected, ErrSourceUnavailable
			}
			selected.Sources = append(selected.Sources, AgentSourceRef{SourceID: source.SourceID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, QuotedTextHash: source.QuotedTextHash, Locator: source.Locator, QuotedText: source.QuotedText, ModelUseAllowed: true})
		}
	}
	if err = s.checkSources(ctx, actor, projectID, usedSourceIDs); err != nil {
		return ctx, selected, err
	}
	allIDs := []string{}
	for _, source := range selected.Sources {
		allIDs = append(allIDs, source.SourceID)
	}
	for _, id := range usedSourceIDs {
		if !slices.Contains(allIDs, id) {
			return ctx, selected, ErrEvidenceIncomplete
		}
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return ctx, selected, err
	}
	proof := candidateEvidenceProof{actor: actor, projectID: projectID, chapterID: chapterID, identityHash: candidateIdentityHash(ctx), refsHash: candidateRefsHash(refs), usedIDsHash: hashImpactValue(sortedImpactSourceIDs(usedSourceIDs)), sourceIDs: sortedImpactSourceIDs(allIDs), selectionJSON: raw}
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID))
		if e != nil {
			return e
		}
		if p.Status != "active" {
			return ErrInvalidState
		}
		if _, e = tx.Chapter(projectID, chapterID); e != nil {
			return e
		}
		proof.basisHash = candidateFormalBasisHash(p)
		selected.ContextRevision = p.CurrentContextRevision
		proof.selectionJSON, e = json.Marshal(selected)
		if e != nil {
			return e
		}
		if e = validateNativeCandidateCards(tx, p, refs); e != nil {
			return e
		}
		digest, ok := tx.(impactComputationSourceTransaction)
		if !ok {
			return ErrInvalidState
		}
		proof.sourceDigest, e = digest.ImpactSourceDigest(projectID, proof.sourceIDs)
		if e != nil {
			return e
		}
		proof.policyDigest, e = candidateNativePolicyDigest(ctx, tx, actor, p, selected.Sources)
		return e
	})
	if err != nil {
		return ctx, selected, err
	}
	return context.WithValue(ctx, candidateEvidenceProofKey{}, proof), selected, nil
}

func validateNativeCandidateCards(tx Transaction, p Project, refs []VersionRef) error {
	reader, ok := tx.(interface {
		CurrentEvidenceObject(string, string, string) (ImpactObjectState, error)
	})
	if len(refs) > 0 && !ok {
		return ErrInvalidState
	}
	for _, ref := range refs {
		current, err := reader.CurrentEvidenceObject(p.ID, "EvidenceCard", ref.ID)
		if err != nil || !current.Confirmed || current.Ref.Version != fmt.Sprint(ref.Version) {
			return ErrEvidenceIncomplete
		}
	}
	return nil
}

// This is native currentness of the already authorized proof, not a new share
// authorization engine. Cross-tenant sources stay closed; the existing access
// helper enforces the original caller's exact API KB scope.
func candidateNativePolicyDigest(ctx context.Context, tx Transaction, actor Actor, p Project, sources []AgentSourceRef) (string, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return "", err
	}
	values := []any{}
	for _, source := range sources {
		var asset evidence.ProjectAsset
		if err = db.Where("id = ? AND project_id = ? AND tenant_id = ? AND deleted_at IS NULL", source.AssetID, p.ID, actor.TenantID).Take(&asset).Error; err != nil {
			return "", storageError(err)
		}
		if asset.AssetRevision != int64(source.AssetRevision) || asset.ProcessingState != "ready" {
			return "", ErrVersionConflict
		}
		var chunk types.Chunk
		if err = db.Where("id = ? AND tenant_id = ? AND knowledge_id = ? AND deleted_at IS NULL AND is_enabled = ?", source.SourceID, actor.TenantID, asset.KnowledgeID, true).Take(&chunk).Error; err != nil {
			return "", storageError(err)
		}
		var knowledge types.Knowledge
		if err = db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", asset.KnowledgeID, actor.TenantID).Take(&knowledge).Error; err != nil {
			return "", storageError(err)
		}
		if asset.KnowledgeBaseID == "" || knowledge.KnowledgeBaseID != asset.KnowledgeBaseID || chunk.KnowledgeBaseID != asset.KnowledgeBaseID {
			return "", ErrSourceUnavailable
		}
		var kb types.KnowledgeBase
		if err = db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", asset.KnowledgeBaseID, actor.TenantID).Take(&kb).Error; err != nil {
			return "", storageError(err)
		}
		allowed, e := access.NewKBPermissions(ctx, nil).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
		if e != nil {
			return "", e
		}
		if !allowed {
			return "", ErrSourceUnavailable
		}
		policy, _, provenance, e := ReadProjectAssetPolicy(tx, p, asset.ID)
		if e != nil {
			return "", e
		}
		if !policy.ModelUseAllowed || policy.AssetRevision != int64(source.AssetRevision) {
			return "", ErrConsentRequired
		}
		values = append(values, struct {
			SourceID, KBID, Provenance string
			Policy                     ProjectAssetPolicy
		}{source.SourceID, kb.ID, provenance, policy})
	}
	return hashImpactValue(values), nil
}

// All work here uses the supplied tx. In particular this method never calls
// ReadResearchSource, SourceRuntime, a main-pool repository or an external KB service.
func (s *Service) SelectGenerationEvidenceInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, chapterID string, refs []VersionRef, formal bool, usedSourceIDs []string) (EvidenceSelection, error) {
	selected := EvidenceSelection{}
	if s == nil || tx == nil || !formal {
		return selected, ErrInvalidState
	}
	p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID))
	if err != nil {
		return selected, err
	}
	if _, err = tx.Chapter(projectID, chapterID); err != nil {
		return selected, err
	}
	proof, ok := ctx.Value(candidateEvidenceProofKey{}).(candidateEvidenceProof)
	if !ok || proof.actor != actor || proof.projectID != projectID || proof.chapterID != chapterID || proof.identityHash != candidateIdentityHash(ctx) || proof.refsHash != candidateRefsHash(refs) || proof.usedIDsHash != hashImpactValue(sortedImpactSourceIDs(usedSourceIDs)) {
		return selected, ErrEvidenceIncomplete
	}
	if p.Status != "active" || proof.basisHash != candidateFormalBasisHash(p) {
		return selected, ErrVersionConflict
	}
	if err = json.Unmarshal(proof.selectionJSON, &selected); err != nil {
		return selected, err
	}
	if err = validateNativeCandidateCards(tx, p, refs); err != nil {
		return selected, err
	}
	digest, ok := tx.(impactComputationSourceTransaction)
	if !ok {
		return selected, ErrInvalidState
	}
	currentDigest, err := digest.ImpactSourceDigest(projectID, proof.sourceIDs)
	if err != nil {
		return selected, err
	}
	if currentDigest != proof.sourceDigest {
		return selected, ErrVersionConflict
	}
	policyDigest, err := candidateNativePolicyDigest(ctx, tx, actor, p, selected.Sources)
	if err != nil {
		return selected, err
	}
	if policyDigest != proof.policyDigest {
		return selected, ErrVersionConflict
	}
	return selected, nil
}
