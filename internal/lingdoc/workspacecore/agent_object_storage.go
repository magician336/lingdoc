package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"reflect"
	"strconv"
	"time"
)

type StoredObject struct {
	ProjectID string
	Kind      string
	ID        string
	Version   int64
	Payload   json.RawMessage
}

// ObjectStore extends the existing atomic transaction with project-scoped,
// append-only domain versions. It does not supply authorization decisions.
type ObjectStore interface {
	Object(string, string, string) (StoredObject, error)
	Objects(string, string) ([]StoredObject, error)
	ObjectVersion(string, string, string, int64) (StoredObject, error)
	InsertObject(StoredObject) error
	AppendObject(StoredObject, StoredObject) error
}
type objectRow struct {
	ProjectID   string `gorm:"column:project_id;primaryKey"`
	Kind        string `gorm:"column:kind;primaryKey"`
	ID          string `gorm:"column:id;primaryKey"`
	Version     int64  `gorm:"column:version"`
	PayloadJSON string `gorm:"column:payload_json"`
}

func (objectRow) TableName() string { return "lingdoc_g2_objects" }

type objectVersionRow struct {
	ProjectID   string `gorm:"column:project_id;primaryKey"`
	Kind        string `gorm:"column:kind;primaryKey"`
	ID          string `gorm:"column:id;primaryKey"`
	Version     int64  `gorm:"column:version;primaryKey"`
	PayloadJSON string `gorm:"column:payload_json"`
}

func (objectVersionRow) TableName() string { return "lingdoc_g2_object_versions" }
func objectFromRow(row objectRow) (StoredObject, error) {
	if row.ID == "" || row.ProjectID == "" || row.Version < 1 || !json.Valid([]byte(row.PayloadJSON)) {
		return StoredObject{}, ErrInvalidState
	}
	return StoredObject{ProjectID: row.ProjectID, Kind: row.Kind, ID: row.ID, Version: row.Version, Payload: json.RawMessage(row.PayloadJSON)}, nil
}
func (t gormTransaction) Object(projectID, kind, id string) (StoredObject, error) {
	var row objectRow
	err := t.db.Where("project_id = ? AND kind = ? AND id = ?", projectID, kind, id).First(&row).Error
	if err != nil {
		return StoredObject{}, storageError(err)
	}
	return objectFromRow(row)
}
func (t gormTransaction) Objects(projectID, kind string) ([]StoredObject, error) {
	var rows []objectRow
	if err := t.db.Where("project_id = ? AND kind = ?", projectID, kind).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]StoredObject, 0, len(rows))
	for _, row := range rows {
		object, err := objectFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, object)
	}
	return out, nil
}
func (t gormTransaction) ObjectVersion(projectID, kind, id string, version int64) (StoredObject, error) {
	var row objectVersionRow
	if err := t.db.Where("project_id = ? AND kind = ? AND id = ? AND version = ?", projectID, kind, id, version).First(&row).Error; err != nil {
		return StoredObject{}, storageError(err)
	}
	return objectFromRow(objectRow(row))
}
func (t gormTransaction) InsertObject(object StoredObject) error {
	if object.Version != 1 || object.ProjectID == "" || object.ID == "" || !json.Valid(object.Payload) {
		return ErrInvalidRequest
	}
	row := objectRow{ProjectID: object.ProjectID, Kind: object.Kind, ID: object.ID, Version: object.Version, PayloadJSON: string(object.Payload)}
	if err := t.db.Create(&row).Error; err != nil {
		return err
	}
	return t.db.Create(&objectVersionRow{ProjectID: object.ProjectID, Kind: object.Kind, ID: object.ID, Version: object.Version, PayloadJSON: string(object.Payload)}).Error
}
func (t gormTransaction) AppendObject(previous, next StoredObject) error {
	if next.ProjectID != previous.ProjectID || next.ID != previous.ID || next.Kind != previous.Kind || next.Version != previous.Version+1 || !json.Valid(next.Payload) {
		return ErrInvalidRequest
	}
	changed := t.db.Model(&objectRow{}).Where("project_id = ? AND kind = ? AND id = ? AND version = ?", previous.ProjectID, previous.Kind, previous.ID, previous.Version).Updates(map[string]any{"version": next.Version, "payload_json": string(next.Payload)})
	if err := affected(changed); err != nil {
		return err
	}
	return t.db.Create(&objectVersionRow{ProjectID: next.ProjectID, Kind: next.Kind, ID: next.ID, Version: next.Version, PayloadJSON: string(next.Payload)}).Error
}
func agentObjectStore(tx Transaction) (ObjectStore, error) {
	port, ok := tx.(ObjectStore)
	if !ok {
		return nil, ErrDependencyUnavailable
	}
	return port, nil
}
func loadStoredObject[T any](tx Transaction, projectID, kind, id string) (T, StoredObject, error) {
	var value T
	port, err := agentObjectStore(tx)
	if err != nil {
		return value, StoredObject{}, err
	}
	object, err := port.Object(projectID, kind, id)
	if err != nil {
		return value, object, err
	}
	err = json.Unmarshal(object.Payload, &value)
	return value, object, err
}
func storeStoredObject(tx Transaction, previous StoredObject, projectID, kind, id string, version int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	object := StoredObject{ProjectID: projectID, Kind: kind, ID: id, Version: version, Payload: raw}
	port, err := agentObjectStore(tx)
	if err != nil {
		return err
	}
	if previous.ID == "" {
		return port.InsertObject(object)
	}
	return port.AppendObject(previous, object)
}

// CurrentEvidenceObject lets the shared impact coordinator read current
// domain facts. SourceIDs still require the caller's current SourcePolicy check.
func (t gormTransaction) CurrentEvidenceObject(projectID, kind, id string) (ImpactObjectState, error) {
	result := ImpactObjectState{Ref: ObjectVersionRef{Kind: kind, ID: id}}
	switch kind {
	case "ProjectAsset":
		var row struct {
			AssetRevision   int
			ProcessingState string
		}
		err := t.db.Table("lingdoc_project_assets").Select("asset_revision", "processing_state").Where("project_id = ? AND id = ? AND deleted_at IS NULL", projectID, id).Take(&row).Error
		if err != nil {
			return result, storageError(err)
		}
		result.Ref.Version = strconv.Itoa(row.AssetRevision)
		result.State = row.ProcessingState
		policy, sources, _, err := ReadProjectAssetPolicy(t, Project{ID: projectID}, id)
		if err != nil {
			return result, err
		}
		result.SourceIDs = sources
		result.ContextRevision = policy.ContextRevision
		// No confirmation is inferred from parsing success or model consent.
		return result, nil
	case "SourceAnchor":
		value, _, err := loadStoredObject[SourceAnchor](t, projectID, SourceAnchorKind, id)
		if err != nil {
			return result, err
		}
		result.Ref.Version = strconv.FormatInt(value.Version, 10)
		result.State = value.Status
		result.SourceIDs = []string{value.SourceID}
		result.ContextRevision = value.Basis.ContextRevision
		current, currentErr := t.IsAnchorCurrent(value)
		result.Confirmed = currentErr == nil && current
		if !result.Confirmed {
			result.State = "NEEDS_REVIEW"
		}
	case "ResearchClaim":
		value, _, err := loadStoredObject[ResearchClaim](t, projectID, ResearchClaimKind, id)
		if err != nil {
			return result, err
		}
		result.Ref.Version = strconv.FormatInt(value.Version, 10)
		result.State = value.Status
		result.ContextRevision = value.Basis.ContextRevision
	case "EvidenceCard":
		value, _, err := loadStoredObject[EvidenceCard](t, projectID, EvidenceCardKind, id)
		if err != nil {
			return result, err
		}
		result.Ref.Version = strconv.FormatInt(value.Version, 10)
		result.State = value.Status
		result.SourceIDs = value.SourceIDs
		result.ContextRevision = value.Basis.ContextRevision
		result.Confirmed, err = t.cardConfirmationCurrent(value)
		if err != nil {
			result.Confirmed = false
			result.State = "NEEDS_REVIEW"
		}
	default:
		return result, ErrNotFound
	}
	return result, nil
}

var _ ObjectStore = gormTransaction{}

type assetTruth struct {
	Asset       evidence.ProjectAsset
	Revision    evidence.ProjectAssetRevision
	Fingerprint string
	SourceIDs   []string
}
type assetPolicyTransaction interface {
	AssetTruth(Project, string) (assetTruth, error)
	AdvanceAssetPolicy(assetTruth) error
	IsAnchorCurrent(SourceAnchor) (bool, error)
}

func (t gormTransaction) AssetTruth(p Project, id string) (assetTruth, error) {
	var out assetTruth
	if err := t.db.Where("project_id = ? AND id = ? AND deleted_at IS NULL", p.ID, id).First(&out.Asset).Error; err != nil {
		return out, storageError(err)
	}
	if err := t.db.Where("asset_id = ? AND revision_no = ?", id, out.Asset.AssetRevision).First(&out.Revision).Error; err != nil {
		return out, storageError(err)
	}
	if out.Revision.Status != evidence.RevisionActive || out.Revision.KnowledgeID != out.Asset.KnowledgeID {
		return out, ErrSourceUnavailable
	}
	var knowledge types.Knowledge
	if err := t.db.Where("id = ? AND deleted_at IS NULL", out.Asset.KnowledgeID).First(&knowledge).Error; err != nil {
		return out, storageError(err)
	}
	processed := time.Time{}
	if knowledge.ProcessedAt != nil {
		processed = *knowledge.ProcessedAt
	}
	// The revision may include the storage-backed digest captured by the
	// authorized source gateway. Recompute with that persisted value: this
	// transaction has no storage port and must not silently fall back to v1.
	var metadata struct {
		OriginalContentSHA256 string `json:"original_content_sha256"`
	}
	if out.Revision.Metadata != "" {
		if err := json.Unmarshal([]byte(out.Revision.Metadata), &metadata); err != nil {
			return out, ErrVersionConflict
		}
	}
	out.Fingerprint = evidence.FingerprintOf(evidence.KnowledgeSignal{KnowledgeID: knowledge.ID, ParseStatus: knowledge.ParseStatus, FileHash: knowledge.FileHash, FileSize: knowledge.FileSize, ProcessedAt: processed, OriginalContentSHA256: metadata.OriginalContentSHA256}).Digest
	if out.Fingerprint != out.Revision.ContentHash {
		return out, ErrVersionConflict
	}
	if err := t.db.Model(&types.Chunk{}).Where("knowledge_id = ? AND deleted_at IS NULL AND is_enabled = ?", knowledge.ID, true).Order("id").Pluck("id", &out.SourceIDs).Error; err != nil {
		return out, err
	}
	return out, nil
}

// assetPolicyVersionAdvance is how far ONE applied asset-policy change moves
// the material's own revision forward.
//
// It lives here, next to the writer that performs the move, and the writer
// uses it. That is deliberate: the version a change set is expected to produce
// and the version it really produces are then the same number by construction,
// so the stabilization re-read can compare against exactly this step when it
// has to tell "this change set's own step" from "somebody else moved the
// material on".
const assetPolicyVersionAdvance = 1

func (t gormTransaction) AdvanceAssetPolicy(truth assetTruth) error {
	now := time.Now().UTC()
	changed := t.db.Model(&evidence.ProjectAsset{}).Where("id = ? AND project_id = ? AND asset_revision = ? AND deleted_at IS NULL", truth.Asset.ID, truth.Asset.ProjectID, truth.Asset.AssetRevision).Updates(map[string]any{"asset_revision": truth.Asset.AssetRevision + assetPolicyVersionAdvance, "updated_at": now})
	if err := affected(changed); err != nil {
		return err
	}
	old := t.db.Model(&evidence.ProjectAssetRevision{}).Where("asset_id = ? AND revision_no = ? AND status = ?", truth.Asset.ID, truth.Asset.AssetRevision, evidence.RevisionActive).Update("status", evidence.RevisionSuperseded)
	if err := affected(old); err != nil {
		return err
	}
	return t.db.Create(&evidence.ProjectAssetRevision{ID: uuid.NewString(), AssetID: truth.Asset.ID, RevisionNo: truth.Asset.AssetRevision + assetPolicyVersionAdvance, KnowledgeID: truth.Asset.KnowledgeID, ContentHash: truth.Fingerprint, Status: evidence.RevisionActive, Metadata: truth.Revision.Metadata, CreatedAt: now}).Error
}
func ReadProjectAssetPolicy(tx Transaction, p Project, assetID string) (ProjectAssetPolicy, []string, string, error) {
	port, ok := tx.(assetPolicyTransaction)
	if !ok {
		return ProjectAssetPolicy{}, nil, "", ErrDependencyUnavailable
	}
	truth, err := port.AssetTruth(p, assetID)
	if err != nil {
		return ProjectAssetPolicy{}, nil, "", err
	}
	value, _, err := loadStoredObject[ProjectAssetPolicy](tx, p.ID, AssetPolicyKind, assetID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return value, nil, "", err
	}
	if errors.Is(err, ErrNotFound) {
		value = ProjectAssetPolicy{ID: assetID, ProjectID: p.ID, AssetID: assetID, Scope: "project", AllowedUserIDs: []string{}, CreatedBy: truth.Asset.CreatedBy}
		store, _ := agentObjectStore(tx)
		rows, e := store.Objects(p.ID, ImportCandidateKind)
		if e != nil {
			return value, nil, "", e
		}
		var latest *ImportCandidate
		for _, row := range rows {
			var receipt ImportCandidate
			if e = json.Unmarshal(row.Payload, &receipt); e != nil {
				return value, nil, "", e
			}
			if receipt.Status != "imported" || receipt.AssetID != assetID || int64(receipt.AssetRevision) != truth.Asset.AssetRevision {
				continue
			}
			if latest == nil || receipt.CreatedAt.After(latest.CreatedAt) {
				copy := receipt
				latest = &copy
			}
		}
		if latest != nil {
			value.Scope = latest.Scope
			value.Purpose = latest.Purpose
			value.ModelUseAllowed = latest.ModelUseAllowed
			value.CreatedBy = latest.CreatedBy
			value.GrantedBy = latest.CreatedBy
			value.SourceVersionHash = truth.Fingerprint
			if value.Scope == "personal" {
				value.AllowedUserIDs = []string{latest.CreatedBy}
			}
		}
	}
	// A consent never follows a changed/reparsed source automatically.
	if value.AssetRevision != 0 && value.AssetRevision != truth.Asset.AssetRevision || value.SourceVersionHash != "" && value.SourceVersionHash != truth.Fingerprint {
		value.ModelUseAllowed = false
	}
	value.Version = truth.Asset.AssetRevision
	value.AssetRevision = truth.Asset.AssetRevision
	if value.SourceVersionHash == "" {
		value.SourceVersionHash = truth.Fingerprint
	}
	provenance := sha256Hex(truth.Revision.ContentHash + "|" + strconv.FormatInt(truth.Asset.AssetRevision, 10) + "|" + truth.Asset.KnowledgeID)
	return value, truth.SourceIDs, provenance, nil
}

// IsAnchorCurrent re-reads local version and original parser content only.
// Authorization stays with the caller's current SourcePolicy outside this tx.
func (t gormTransaction) IsAnchorCurrent(anchor SourceAnchor) (bool, error) {
	var asset evidence.ProjectAsset
	if err := t.db.Where("id = ? AND project_id = ? AND deleted_at IS NULL", anchor.AssetID, anchor.ProjectID).First(&asset).Error; err != nil {
		return false, storageError(err)
	}
	if asset.AssetRevision != int64(anchor.AssetRevision) || asset.ProcessingState != "ready" || anchor.Status != "CURRENT" {
		return false, nil
	}
	truth, err := t.AssetTruth(Project{ID: anchor.ProjectID}, asset.ID)
	if err != nil {
		return false, err
	}
	if truth.Fingerprint == evidence.FingerprintNoContentSignal {
		return false, nil
	}
	var chunk types.Chunk
	if err := t.db.Where("id = ? AND knowledge_id = ? AND is_enabled = ? AND deleted_at IS NULL", anchor.SourceID, asset.KnowledgeID, true).First(&chunk).Error; err != nil {
		return false, storageError(err)
	}
	original := chunk.Content
	if chunk.SourceContent != "" {
		original = chunk.SourceContent
	}
	text := []rune(original)
	if anchor.SelectionStart < 0 || anchor.SelectionEnd > len(text) || anchor.SelectionEnd <= anchor.SelectionStart {
		return false, nil
	}
	if sha256Hex(string(text[anchor.SelectionStart:anchor.SelectionEnd])) != anchor.QuotedTextHash {
		return false, nil
	}
	return anchor.ExtractionType == "native_text" || anchor.HumanVerified, nil
}
func (t gormTransaction) cardConfirmationCurrent(card EvidenceCard) (bool, error) {
	// Database-only currentness cannot establish external original bytes for
	// an academic association. This guards the transaction-time selection
	// seam as well as the public confirmation path.
	if len(card.AcademicAssociationRefs) > 0 {
		return false, nil
	}
	if !confirmationTupleCurrent(card) {
		return false, nil
	}
	var projectRecord projectRow
	if err := t.db.Where("id = ?", card.ProjectID).First(&projectRecord).Error; err != nil {
		return false, storageError(err)
	}
	p, err := t.Project(projectRecord.TenantID, card.ProjectID)
	if err != nil {
		return false, err
	}
	if p.Status != "active" {
		return false, nil
	}
	claim, _, err := loadStoredObject[ResearchClaim](t, p.ID, ResearchClaimKind, card.ClaimRef.ID)
	if err != nil {
		return false, err
	}
	if claim.Version != card.ClaimRef.Version || claim.Status == "superseded" || claim.Basis.SpecRevision != p.SpecRevision {
		return false, nil
	}
	ids := []string{}
	for _, ref := range card.AnchorRefs {
		anchor, _, err := loadStoredObject[SourceAnchor](t, p.ID, SourceAnchorKind, ref.ID)
		if err != nil {
			return false, err
		}
		if anchor.Version != ref.Version {
			return false, nil
		}
		current, err := t.IsAnchorCurrent(anchor)
		if err != nil {
			return false, err
		}
		if !current {
			return false, nil
		}
		// Formal use also pins the complete source text that was supplied to
		// the anchor, including any text outside the human-selected span.
		var chunk types.Chunk
		if err := t.db.Where("id = ? AND deleted_at IS NULL", anchor.SourceID).First(&chunk).Error; err != nil {
			return false, storageError(err)
		}
		if chunk.ContentRevision != 0 || chunk.SourceContent != "" && chunk.SourceContent != chunk.Content || !anchorMatchesWholeSource(anchor, chunk.Content) {
			return false, nil
		}
		ids = append(ids, anchor.SourceID)
	}
	return reflect.DeepEqual(sortedUniqueIDs(ids), sortedUniqueIDs(card.SourceIDs)), nil
}

// ReadProjectAssetPolicyFromDB is a read projection for already-authorized
// gateways. It supplies no tenant/project/KB authorization of its own.
func ReadProjectAssetPolicyFromDB(ctx context.Context, db *gorm.DB, projectID, assetID string) (ProjectAssetPolicy, error) {
	var out ProjectAssetPolicy
	if db == nil {
		return out, ErrDependencyUnavailable
	}
	err := db.WithContext(ctx).Transaction(func(dbtx *gorm.DB) error {
		var err error
		out, _, _, err = ReadProjectAssetPolicy(gormTransaction{db: dbtx}, Project{ID: projectID}, assetID)
		return err
	})
	return out, err
}
