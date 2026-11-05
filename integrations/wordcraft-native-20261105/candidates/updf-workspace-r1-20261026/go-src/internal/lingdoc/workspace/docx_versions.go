package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type docxSavedVersionRow struct {
	ID               string    `gorm:"primaryKey;size:36"`
	ProjectID        string    `gorm:"not null;index;size:36;uniqueIndex:docx_version_action"`
	DiskVersion      int64     `gorm:"not null;uniqueIndex:docx_project_disk_version"`
	DocSHA256        string    `gorm:"not null;size:64"`
	ManifestHash     string    `gorm:"not null;size:64"`
	AdapterVersion   string    `gorm:"not null;size:128"`
	AlgorithmVersion string    `gorm:"not null;size:64"`
	DocBytes         []byte    `gorm:"not null"`
	ProjectionJSON   string    `gorm:"not null;type:text"`
	CitationsJSON    string    `gorm:"not null;type:text;default:'[]'"`
	ActorID          string    `gorm:"not null;size:128;uniqueIndex:docx_version_action"`
	IdempotencyKey   string    `gorm:"not null;size:128;uniqueIndex:docx_version_action"`
	RequestHash      string    `gorm:"not null;size:64"`
	CreatedAt        time.Time `gorm:"not null"`
}

func (docxSavedVersionRow) TableName() string { return "lingdoc_docx_saved_versions" }

type docxSectionProjectionRow struct {
	SavedVersionID      string `gorm:"primaryKey;size:36"`
	SectionID           string `gorm:"primaryKey;size:64"`
	ChapterID           string `gorm:"not null;size:36"`
	ChapterVersionID    string `gorm:"not null;size:36"`
	Body                string `gorm:"not null;type:text"`
	SectionDeliveryHash string `gorm:"not null;size:64"`
	SourceIDsJSON       string `gorm:"not null;type:text"`
	CitationsJSON       string `gorm:"not null;type:text;default:'[]'"`
}

func (docxSectionProjectionRow) TableName() string { return "lingdoc_docx_section_projections" }

type DOCXSavedVersion struct {
	ID               string            `json:"id"`
	ProjectID        string            `json:"projectId"`
	DiskVersion      int64             `json:"diskVersion"`
	DocSHA256        string            `json:"docSha256"`
	ManifestHash     string            `json:"manifestHash"`
	AdapterVersion   string            `json:"adapterVersion"`
	AlgorithmVersion string            `json:"algorithmVersion"`
	Projection       DOCXProjection    `json:"projection"`
	Citations        []DOCXCitationRef `json:"citations"`
	CreatedAt        time.Time         `json:"createdAt"`
	Bytes            []byte            `json:"-"`
}

type DOCXWriteAuthorizer func(*gorm.DB, string) error
type DOCXVersionStore struct {
	db                *gorm.DB
	authorize         DOCXWriteAuthorizer
	validateCitations docxCitationValidator
}

func NewDOCXVersionStore(db *gorm.DB, authorizers ...DOCXWriteAuthorizer) *DOCXVersionStore {
	var authorize DOCXWriteAuthorizer
	if len(authorizers) > 0 {
		authorize = authorizers[0]
	}
	return &DOCXVersionStore{db: db, authorize: authorize}
}
func (s *DOCXVersionStore) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&docxSavedVersionRow{}, &docxSectionProjectionRow{})
}
func docxBytesHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func docxSaveRequestHash(projectID, actorID string, expected int64, docSHA string) string {
	return docxBytesHash([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", projectID, actorID, expected, docSHA)))
}

// Save parses and persists DOCX bytes and derived chapter versions atomically.
// Sources follow actual saved markers and require persisted provenance or a
// currently valid card reference; the browser never supplies chapter text.
func (s *DOCXVersionStore) Save(ctx context.Context, projectID, actorID, key string, expectedDiskVersion int64, data []byte, citationInputs ...[]DOCXCitationRef) (DOCXSavedVersion, bool, error) {
	var result DOCXSavedVersion
	var replayed bool
	if s == nil || s.db == nil || s.authorize == nil || projectID == "" || actorID == "" || len(key) < 8 || len(key) > 128 || len(data) == 0 || len(data) > 8<<20 {
		return result, false, errors.New("DOCX_SAVE_INVALID_REQUEST")
	}
	if len(citationInputs) > 1 {
		return result, false, errors.New("DOCX_CITATION_INVALID_REQUEST")
	}
	var requested []DOCXCitationRef
	if len(citationInputs) == 1 {
		requested = citationInputs[0]
	}
	requested, err := normalizeDOCXCitations(requested)
	if err != nil {
		return result, false, err
	}
	docSHA := docxBytesHash(data)
	requestHash := docxSaveRequestHash(projectID, actorID, expectedDiskVersion, docSHA)
	// Preserve the old hash for callers without citation metadata, including
	// retries of transactions recorded before this additive API extension.
	if len(requested) > 0 {
		requestHash = docxBytesHash([]byte(requestHash + "\x00" + docxCitationJSON(requested)))
	}
	projection, err := parseDOCXProjection(data)
	if err != nil {
		return result, false, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chapters []struct{ ID, SectionID, CurrentVersionID string }
		if err := tx.Table("lingdoc_chapters").Select("id,section_id,current_version_id").Where("project_id=?", projectID).Find(&chapters).Error; err != nil {
			return err
		}
		bySection := map[string]struct{ ID, SectionID, CurrentVersionID string }{}
		for _, chapter := range chapters {
			bySection[chapter.SectionID] = chapter
			if err := s.authorize(tx, chapter.ID); err != nil {
				return err
			}
		}
		if len(bySection) != len(docxSectionSpecs) {
			return errors.New("DOCX_SECTION_MAPPING_INCOMPLETE")
		}
		var previous docxSavedVersionRow
		err := tx.Where("project_id=? AND actor_id=? AND idempotency_key=?", projectID, actorID, key).First(&previous).Error
		if err == nil {
			if previous.RequestHash != requestHash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			result, err = docxVersionFromRow(previous)
			replayed = err == nil
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var current int64
		if err := tx.Model(&docxSavedVersionRow{}).Where("project_id=?", projectID).Select("COALESCE(MAX(disk_version),0)").Scan(&current).Error; err != nil {
			return err
		}
		if current != expectedDiskVersion {
			return errors.New("VERSION_CHANGED")
		}
		versionID := uuid.NewString()
		createdAt := time.Now().UTC()
		projectionRows := make([]docxSectionProjectionRow, 0, len(projection.Sections))
		persistedCitations := []DOCXCitationRef{}
		for _, section := range projection.Sections {
			chapter, ok := bySection[section.SectionID]
			if !ok || chapter.CurrentVersionID == "" {
				return errors.New("DOCX_SECTION_MAPPING_INCOMPLETE")
			}
			var base struct {
				BodyMarkdown, SourceIDsJSON, CitationUsagesJSON, ReviewItemsJSON, CandidateID string
				SpecRevision                                                                  int64
			}
			if err := tx.Table("lingdoc_chapter_versions").Select("body_markdown,source_ids_json,citation_usages_json,review_items_json,candidate_id,spec_revision").Where("id=? AND project_id=? AND chapter_id=?", chapter.CurrentVersionID, projectID, chapter.ID).Scan(&base).Error; err != nil {
				return err
			}
			var previousCitations string
			if err := tx.Table("lingdoc_docx_section_projections AS p").Select("p.citations_json").Joins("JOIN lingdoc_docx_saved_versions AS v ON v.id=p.saved_version_id").Where("v.project_id=? AND p.chapter_id=? AND p.chapter_version_id=?", projectID, chapter.ID, chapter.CurrentVersionID).Order("v.disk_version DESC").Limit(1).Scan(&previousCitations).Error; err != nil {
				return err
			}
			inherited, err := decodeDOCXCitations(previousCitations)
			if err != nil {
				return err
			}
			ids, usages, refs, err := resolveDOCXCitations(section, base.SourceIDsJSON, base.CitationUsagesJSON, inherited, requested)
			if err != nil {
				return err
			}
			if len(ids) > 0 {
				if s.validateCitations == nil {
					return errors.New("DOCX_CITATION_DEPENDENCY_MISSING")
				}
				if err := s.validateCitations(tx, chapter.ID, ids, refs); err != nil {
					return err
				}
			}
			sourceJSON, usageJSON, refsJSON := docxCitationJSON(ids), docxCitationJSON(usages), docxCitationJSON(refs)
			chapterVersionID := chapter.CurrentVersionID
			if base.BodyMarkdown != section.Body || base.SourceIDsJSON != sourceJSON || base.CitationUsagesJSON != usageJSON || docxCitationJSON(inherited) != refsJSON {
				chapterVersionID = uuid.NewString()
				insert := tx.Exec(`INSERT INTO lingdoc_chapter_versions (id,project_id,chapter_id,parent_version_id,candidate_id,body_markdown,source_ids_json,citation_usages_json,review_items_json,spec_revision,confirmation_valid,created_by,created_at) SELECT ?,project_id,chapter_id,id,candidate_id,?,?,?,review_items_json,spec_revision,0,?,? FROM lingdoc_chapter_versions WHERE id=? AND project_id=? AND chapter_id=?`, chapterVersionID, section.Body, sourceJSON, usageJSON, actorID, createdAt, chapter.CurrentVersionID, projectID, chapter.ID)
				if insert.Error != nil {
					return insert.Error
				}
				if insert.RowsAffected != 1 {
					return errors.New("DOCX_PROJECTION_VERSION_MISMATCH")
				}
				update := tx.Exec("UPDATE lingdoc_chapters SET current_version_id=? WHERE id=? AND project_id=? AND current_version_id=?", chapterVersionID, chapter.ID, projectID, chapter.CurrentVersionID)
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected != 1 {
					return errors.New("VERSION_CHANGED")
				}
				if err := tx.Exec("UPDATE lingdoc_chapter_confirmations SET valid=0 WHERE chapter_id=? AND chapter_version_id=? AND valid=1", chapter.ID, chapter.CurrentVersionID).Error; err != nil {
					return err
				}
			}
			projectionRows = append(projectionRows, docxSectionProjectionRow{SavedVersionID: versionID, SectionID: section.SectionID, ChapterID: chapter.ID, ChapterVersionID: chapterVersionID, Body: section.Body, SectionDeliveryHash: section.SectionDeliveryHash, SourceIDsJSON: sourceJSON, CitationsJSON: refsJSON})
			persistedCitations = append(persistedCitations, refs...)
		}
		projectionJSON, err := json.Marshal(projection)
		if err != nil {
			return err
		}
		persistedCitations, err = normalizeDOCXCitations(persistedCitations)
		if err != nil {
			return err
		}
		row := docxSavedVersionRow{ID: versionID, ProjectID: projectID, DiskVersion: current + 1, DocSHA256: docSHA, ManifestHash: projection.ManifestHash, AdapterVersion: projection.AdapterVersion, AlgorithmVersion: projection.AlgorithmVersion, DocBytes: append([]byte(nil), data...), ProjectionJSON: string(projectionJSON), CitationsJSON: docxCitationJSON(persistedCitations), ActorID: actorID, IdempotencyKey: key, RequestHash: requestHash, CreatedAt: createdAt}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Create(&projectionRows).Error; err != nil {
			return err
		}
		result = DOCXSavedVersion{ID: row.ID, ProjectID: projectID, DiskVersion: row.DiskVersion, DocSHA256: docSHA, ManifestHash: row.ManifestHash, AdapterVersion: row.AdapterVersion, AlgorithmVersion: row.AlgorithmVersion, Projection: projection, Citations: persistedCitations, CreatedAt: createdAt, Bytes: append([]byte(nil), data...)}
		return nil
	})
	return result, replayed, err
}

func docxVersionFromRow(row docxSavedVersionRow) (DOCXSavedVersion, error) {
	var projection DOCXProjection
	if err := json.Unmarshal([]byte(row.ProjectionJSON), &projection); err != nil {
		return DOCXSavedVersion{}, err
	}
	citations, err := decodeDOCXCitations(row.CitationsJSON)
	if err != nil {
		return DOCXSavedVersion{}, err
	}
	version := DOCXSavedVersion{ID: row.ID, ProjectID: row.ProjectID, DiskVersion: row.DiskVersion, DocSHA256: row.DocSHA256, ManifestHash: row.ManifestHash, AdapterVersion: row.AdapterVersion, AlgorithmVersion: row.AlgorithmVersion, Projection: projection, Citations: citations, CreatedAt: row.CreatedAt.UTC(), Bytes: append([]byte(nil), row.DocBytes...)}
	if docxBytesHash(row.DocBytes) != row.DocSHA256 {
		return DOCXSavedVersion{}, errors.New("DOCX_FILE_INTEGRITY_FAILED")
	}
	actual, err := parseDOCXProjection(row.DocBytes)
	if err != nil || !reflect.DeepEqual(actual, projection) || version.ManifestHash != actual.ManifestHash || version.AdapterVersion != actual.AdapterVersion || version.AlgorithmVersion != actual.AlgorithmVersion {
		return DOCXSavedVersion{}, errors.New("DOCX_FILE_INTEGRITY_FAILED")
	}
	return version, nil
}
func (s *DOCXVersionStore) Get(ctx context.Context, projectID, savedVersionID string) (DOCXSavedVersion, error) {
	var row docxSavedVersionRow
	if err := s.db.WithContext(ctx).Where("project_id=? AND id=?", projectID, savedVersionID).First(&row).Error; err != nil {
		return DOCXSavedVersion{}, err
	}
	return docxVersionFromRow(row)
}
func (s *DOCXVersionStore) Latest(ctx context.Context, projectID string) (DOCXSavedVersion, error) {
	var row docxSavedVersionRow
	if err := s.db.WithContext(ctx).Where("project_id=?", projectID).Order("disk_version DESC").First(&row).Error; err != nil {
		return DOCXSavedVersion{}, err
	}
	return docxVersionFromRow(row)
}
func (s *DOCXVersionStore) GuardChapters(ctx context.Context, savedVersionID string, sectionIDs []string) ([]DOCXGuardChapter, error) {
	var rows []docxSectionProjectionRow
	query := s.db.WithContext(ctx).Where("saved_version_id=?", savedVersionID)
	if len(sectionIDs) > 0 {
		query = query.Where("section_id IN ?", sectionIDs)
	}
	if err := query.Order("section_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(sectionIDs) > 0 && len(rows) != len(sectionIDs) {
		return nil, errors.New("DOCX_SECTION_MAPPING_INVALID")
	}
	out := make([]DOCXGuardChapter, 0, len(rows))
	for _, row := range rows {
		out = append(out, DOCXGuardChapter{SectionID: row.SectionID, ChapterID: row.ChapterID, ChapterVersionID: row.ChapterVersionID, Body: row.Body})
	}
	return out, nil
}

// DOCXFrozenRenderer reads only the immutable object named by FrozenInput.
// It never resolves the project's latest version during export.
type DOCXFrozenRenderer struct{ Store *DOCXVersionStore }

func (r DOCXFrozenRenderer) RenderFrozen(input delivery.DeliveryInput) ([]byte, error) {
	if r.Store == nil || input.DOCXSavedVersionID == "" {
		return nil, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	version, err := r.Store.Get(context.Background(), input.ProjectID, input.DOCXSavedVersionID)
	if err != nil {
		return nil, err
	}
	if version.DocSHA256 != input.DOCXSHA256 {
		return nil, errors.New("DOCX_FILE_INTEGRITY_FAILED")
	}
	return version.Bytes, nil
}

type DOCXFrozenValidator struct{}

func (DOCXFrozenValidator) ValidateFrozen(input delivery.DeliveryInput, data []byte) error {
	if len(data) == 0 || docxBytesHash(data) != input.DOCXSHA256 {
		return errors.New("DOCX_FILE_INTEGRITY_FAILED")
	}
	_, err := parseDOCXProjection(data)
	return err
}
