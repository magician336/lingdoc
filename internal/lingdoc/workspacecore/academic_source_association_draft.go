package workspacecore

// This draft records a human assertion only. It is deliberately not consumed
// by evidence or writing until transaction-scoped source revalidation exists.
import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

const AcademicSourceAssociationKind = "academic_source_association"

type AcademicSourceAssociation struct {
	ID                    string       `json:"id"`
	ProjectID             string       `json:"project_id"`
	Version               int64        `json:"version"`
	CandidateRef          VersionRef   `json:"candidate_ref"`
	DOI                   string       `json:"doi"`
	CatalogVersion        string       `json:"catalog_version"`
	PolicySnapshotVersion string       `json:"policy_snapshot_version"`
	SourceID              string       `json:"source_id"`
	AssetID               string       `json:"asset_id"`
	AssetRevision         int          `json:"asset_revision"`
	Locator               string       `json:"locator"`
	QuotedTextHash        string       `json:"quoted_text_hash"`
	Basis                 VersionBasis `json:"basis"`
	ConfirmedBy           string       `json:"confirmed_by"`
	ConfirmedAt           time.Time    `json:"confirmed_at"`
	Reason                string       `json:"reason"`
	Status                string       `json:"status"`
	RevokedBy             string       `json:"revoked_by,omitempty"`
	RevokedAt             *time.Time   `json:"revoked_at,omitempty"`
	RevocationReason      string       `json:"revocation_reason,omitempty"`
}

type ConfirmAcademicSourceAssociationInput struct {
	VersionExpectation
	CandidateID            string `json:"candidate_id"`
	SourceID               string `json:"source_id"`
	ExpectedAssetRevision  int    `json:"expected_asset_revision"`
	ExpectedQuotedTextHash string `json:"expected_quoted_text_hash"`
	ExpectedLocator        string `json:"expected_locator"`
	Confirmed              bool   `json:"confirmed"`
	Reason                 string `json:"reason"`
}

func validAssociationSource(source ResearchSource, id string) bool {
	return source.SourceID == id && source.AssetID != "" && source.AssetRevision > 0 &&
		source.Status == string(evidence.SourceAvailable) && source.ProcessingState == "ready" &&
		source.StableOriginal && source.Locator != "" && source.QuotedText != "" &&
		source.QuotedTextHash != "" && sha256Hex(source.QuotedText) == source.QuotedTextHash
}

// lockAssociationDecisionRows serializes with changes to the existing project
// and tenant membership rows. The final authorization must be repeated after
// these locks: the initial pipeline authorization was an earlier snapshot.
func (s *Service) lockAssociationDecisionRows(ctx context.Context, tx Transaction, actor Actor, projectID string) (Project, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return Project{}, ErrDependencyUnavailable
	}
	var tenant struct{ UserID string }
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Table("tenant_members").Select("user_id").Where("tenant_id = ? AND user_id = ? AND status = ? AND deleted_at IS NULL", actor.TenantID, actor.UserID, "active").Take(&tenant).Error; err != nil {
		return Project{}, ErrNotFound
	}
	var project projectRow
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ? AND tenant_id = ?", projectID, actor.TenantID).Take(&project).Error; err != nil {
		return Project{}, ErrNotFound
	}
	var member memberRow
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("project_id", "user_id").Where("project_id = ? AND user_id = ?", projectID, actor.UserID).Take(&member).Error; err != nil {
		return Project{}, ErrNotFound
	}
	var permission []memberPermissionRow
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id = ? AND user_id = ?", projectID, actor.UserID).Find(&permission).Error; err != nil {
		return Project{}, ErrPermissionSchemaUnavailable
	}
	request, err := authorizationRequestFromCapability(actor, projectID, "evidence-confirm")
	if err != nil {
		return Project{}, err
	}
	return s.authorizeProject(ctx, tx, request)
}

func lockAssociationMetadata(tx Transaction, projectID, candidateID string) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrDependencyUnavailable
	}
	for _, item := range []struct{ kind, id string }{{AcademicSourceCatalogKind, academicSourceCatalogObjectID}, {ImportCandidateKind, candidateID}} {
		var row objectRow
		if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("project_id", "kind", "id").Where("project_id = ? AND kind = ? AND id = ?", projectID, item.kind, item.id).Take(&row).Error; err != nil {
			return ErrVersionConflict
		}
	}
	return nil
}

// validateAssociationSourceInTransaction strengthens the external source read
// with current native asset and chunk truth. This remains a prototype until
// authorization revocation locking and the HTTP seam are demonstrated.
func validateAssociationSourceInTransaction(ctx context.Context, tx Transaction, actor Actor, p Project, source ResearchSource) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrDependencyUnavailable
	}
	// SQLite obtains a writer lock; PostgreSQL locks the asset and chunk rows.
	if db.Name() == "sqlite" {
		result := db.Model(&evidence.ProjectAsset{}).Where("id = ? AND project_id = ? AND tenant_id = ? AND deleted_at IS NULL", source.AssetID, p.ID, actor.TenantID).UpdateColumn("id", source.AssetID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrSourceUnavailable
		}
	}
	var asset evidence.ProjectAsset
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND project_id = ? AND tenant_id = ? AND deleted_at IS NULL", source.AssetID, p.ID, actor.TenantID).Take(&asset).Error; err != nil {
		return ErrSourceUnavailable
	}
	if asset.AssetRevision != int64(source.AssetRevision) || asset.ProcessingState != "ready" || asset.KnowledgeID != source.KnowledgeID {
		return ErrVersionConflict
	}
	var chunk types.Chunk
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND knowledge_id = ? AND deleted_at IS NULL AND is_enabled = ?", source.SourceID, actor.TenantID, asset.KnowledgeID, true).Take(&chunk).Error; err != nil {
		return ErrSourceUnavailable
	}
	if chunk.ContentRevision != 0 || sha256Hex(chunk.Content) != source.QuotedTextHash {
		return ErrVersionConflict
	}
	var knowledge types.Knowledge
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", asset.KnowledgeID, actor.TenantID).Take(&knowledge).Error; err != nil {
		return ErrSourceUnavailable
	}
	var kb types.KnowledgeBase
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", knowledge.KnowledgeBaseID, actor.TenantID).Take(&kb).Error; err != nil {
		return ErrSourceUnavailable
	}
	if asset.KnowledgeBaseID != kb.ID || chunk.KnowledgeBaseID != kb.ID {
		return ErrSourceUnavailable
	}
	var policy []objectRow
	if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id = ? AND kind = ? AND id = ?", p.ID, AssetPolicyKind, asset.ID).Find(&policy).Error; err != nil {
		return err
	}
	if err = nativeSourceScope(ctx, tx, actor, p, []string{source.SourceID}, false); err != nil {
		return err
	}
	truth, ok := tx.(assetPolicyTransaction)
	if !ok {
		return ErrDependencyUnavailable
	}
	_, err = truth.AssetTruth(p, source.AssetID)
	return err
}

// ConfirmAcademicSourceAssociation records an authenticated person's matching
// decision; it does not import, grant rights or establish evidential validity.
func (s *Service) ConfirmAcademicSourceAssociation(ctx context.Context, actor Actor, projectID, key string, input ConfirmAcademicSourceAssociationInput) (json.RawMessage, int, bool, error) {
	if err := checkConfirmed(input.Confirmed, input.Reason); err != nil {
		return nil, 0, false, err
	}
	if strings.TrimSpace(input.CandidateID) == "" || strings.TrimSpace(input.SourceID) == "" || input.ExpectedVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	if s.agentRuntime.Sources == nil {
		return nil, 0, false, ErrDependencyUnavailable
	}
	source, err := s.agentRuntime.Sources.ReadResearchSource(ctx, actor, projectID, input.SourceID)
	if err != nil {
		return nil, 0, false, err
	}
	if !validAssociationSource(source, input.SourceID) {
		return nil, 0, false, ErrEvidenceIncomplete
	}
	if input.ExpectedAssetRevision < 1 || input.ExpectedQuotedTextHash == "" || input.ExpectedLocator == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if input.ExpectedAssetRevision != source.AssetRevision || input.ExpectedQuotedTextHash != source.QuotedTextHash || input.ExpectedLocator != source.Locator {
		return nil, 0, false, ErrVersionConflict
	}
	body, status, replay, err := s.runAgentOperation(ctx, actor, projectID, "evidence-confirm", "confirmAcademicSourceAssociation", projectID+"/"+input.CandidateID, key, input, []string{input.SourceID}, operationcore.HumanGateRequired, func(tx Transaction, p Project) (any, int, error) {
		locked, lockErr := s.lockAssociationDecisionRows(ctx, tx, actor, projectID)
		if lockErr != nil {
			return nil, 0, lockErr
		}
		p = locked
		if lockErr = validateAssociationSourceInTransaction(ctx, tx, actor, p, source); lockErr != nil {
			return nil, 0, lockErr
		}
		p, lockErr = s.lockAssociationDecisionRows(ctx, tx, actor, projectID)
		if lockErr != nil {
			return nil, 0, lockErr
		}
		if lockErr = lockAssociationMetadata(tx, projectID, input.CandidateID); lockErr != nil {
			return nil, 0, lockErr
		}
		candidate, _, err := loadStoredObject[ImportCandidate](tx, projectID, ImportCandidateKind, input.CandidateID)
		if err != nil {
			return nil, 0, err
		}
		if !expectationMatches(p, input.VersionExpectation) || candidate.Version != input.ExpectedVersion || !versionBasisCurrent(p, candidate.Basis) {
			return nil, 0, ErrVersionConflict
		}
		if candidate.Status != "imported" || candidate.SelectionType != "academic_metadata" || candidate.SaveMode != "metadata_only" || candidate.SourceID != "" || candidate.DOI == "" || candidate.PolicySnapshotVersion == "" || candidate.PolicyStatus != "approved" || candidate.RetractionStatus == "retracted" || candidate.RetractionStatus == "retraction_notice" || candidate.DuplicateStatus == "duplicate" || candidate.AcquisitionStatus != "metadata_only" {
			return nil, 0, ErrEvidenceIncomplete
		}
		catalog, err := currentAcademicSourceCatalog(tx, projectID)
		if err != nil {
			return nil, 0, err
		}
		if candidate.CatalogVersion != catalog.SourceCatalogVersion {
			return nil, 0, ErrVersionConflict
		}
		gate, ok := s.agentRuntime.Research.(ResearchExecutionGate)
		if !ok {
			return nil, 0, ErrAcademicGlobalCatalogUnavailable
		}
		if err := gate.ValidateAcademicResearchResult(ctx, actor, projectID, ResearchResult{Provider: "crossref", PolicySnapshotVersion: candidate.PolicySnapshotVersion}, catalog); err != nil {
			return nil, 0, err
		}
		association := AcademicSourceAssociation{ID: uuid.NewString(), ProjectID: projectID, Version: 1, CandidateRef: VersionRef{ID: candidate.ID, Version: candidate.Version}, DOI: candidate.DOI, CatalogVersion: candidate.CatalogVersion, PolicySnapshotVersion: candidate.PolicySnapshotVersion, SourceID: source.SourceID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash, Basis: versionBasisFor(p, Chapter{}), ConfirmedBy: actor.UserID, ConfirmedAt: time.Now().UTC(), Reason: strings.TrimSpace(input.Reason), Status: "recorded"}
		if err := storeStoredObject(tx, StoredObject{}, projectID, AcademicSourceAssociationKind, association.ID, association.Version, association); err != nil {
			return nil, 0, err
		}
		if err := recordAgentAuditEvent(tx, actor, projectID, "g2.academic_source_association.confirm", projectID+"/"+association.ID, association.Reason, map[string]any{"candidate_id": candidate.ID, "candidate_version": candidate.Version, "source_id": source.SourceID, "asset_id": source.AssetID, "asset_revision": source.AssetRevision, "catalog_version": candidate.CatalogVersion}); err != nil {
			return nil, 0, err
		}
		return association, 201, nil
	})
	if err != nil || !replay {
		return body, status, replay, err
	}
	// The shared pipeline reauthorizes source visibility on replay, but it does
	// not compare the previously asserted source revision or catalog. Recheck
	// them before returning the cached historical association.
	var prior AcademicSourceAssociation
	if err = json.Unmarshal(body, &prior); err != nil {
		return nil, 0, replay, err
	}
	if prior.SourceID != source.SourceID || prior.AssetID != source.AssetID || prior.AssetRevision != source.AssetRevision || prior.Locator != source.Locator || prior.QuotedTextHash != source.QuotedTextHash || prior.CandidateRef.ID != input.CandidateID || prior.CandidateRef.Version != input.ExpectedVersion {
		return nil, 0, replay, ErrVersionConflict
	}
	request, err := authorizationRequestFromCapability(actor, projectID, "evidence-confirm")
	if err != nil {
		return nil, 0, replay, err
	}
	err = s.repository.Transaction(ctx, TransactionOptions{RetryLocks: true}, func(tx Transaction) error {
		p, e := s.authorizeProject(ctx, tx, request)
		if e != nil {
			return e
		}
		if !expectationMatches(p, input.VersionExpectation) || !versionBasisCurrent(p, prior.Basis) {
			return ErrVersionConflict
		}
		p, e = s.lockAssociationDecisionRows(ctx, tx, actor, projectID)
		if e != nil {
			return e
		}
		if e = validateAssociationSourceInTransaction(ctx, tx, actor, p, source); e != nil {
			return e
		}
		p, e = s.lockAssociationDecisionRows(ctx, tx, actor, projectID)
		if e != nil {
			return e
		}
		if !expectationMatches(p, input.VersionExpectation) || !versionBasisCurrent(p, prior.Basis) {
			return ErrVersionConflict
		}
		current, _, e := loadStoredObject[AcademicSourceAssociation](tx, projectID, AcademicSourceAssociationKind, prior.ID)
		if e != nil {
			return e
		}
		if current.Version != prior.Version || current.Status != "recorded" {
			return ErrVersionConflict
		}
		if e = lockAssociationMetadata(tx, projectID, prior.CandidateRef.ID); e != nil {
			return e
		}
		candidate, _, e := loadStoredObject[ImportCandidate](tx, projectID, ImportCandidateKind, prior.CandidateRef.ID)
		if e != nil {
			return e
		}
		if candidate.Version != prior.CandidateRef.Version || candidate.Status != "imported" || candidate.DOI != prior.DOI || candidate.CatalogVersion != prior.CatalogVersion || candidate.PolicySnapshotVersion != prior.PolicySnapshotVersion || candidate.PolicyStatus != "approved" || candidate.RetractionStatus == "retracted" || candidate.RetractionStatus == "retraction_notice" || candidate.DuplicateStatus == "duplicate" || candidate.AcquisitionStatus != "metadata_only" {
			return ErrVersionConflict
		}
		catalog, e := currentAcademicSourceCatalog(tx, projectID)
		if e != nil {
			return e
		}
		if catalog.SourceCatalogVersion != prior.CatalogVersion {
			return ErrVersionConflict
		}
		gate, ok := s.agentRuntime.Research.(ResearchExecutionGate)
		if !ok {
			return ErrAcademicGlobalCatalogUnavailable
		}
		return gate.ValidateAcademicResearchResult(ctx, actor, projectID, ResearchResult{Provider: "crossref", PolicySnapshotVersion: prior.PolicySnapshotVersion}, catalog)
	})
	if err != nil {
		return nil, 0, replay, err
	}
	return body, status, replay, nil
}
