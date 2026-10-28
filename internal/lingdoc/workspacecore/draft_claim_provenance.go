package workspacecore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DraftClaimProvenance records where a previously verified candidate was
// inserted. It deliberately contains no source text and does not assert that
// access to the underlying evidence is still available.
type DraftClaimProvenance struct {
	ID                  string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID           string    `json:"-" gorm:"not null;size:36;uniqueIndex:idx_claim_provenance_candidate,priority:1"`
	ChapterID           string    `json:"-" gorm:"not null;size:36;uniqueIndex:idx_claim_provenance_candidate,priority:2"`
	ChapterVersionID    string    `json:"-" gorm:"not null;size:36;uniqueIndex:idx_claim_provenance_candidate,priority:3"`
	OriginKind          string    `json:"origin_kind" gorm:"not null;size:24;uniqueIndex:idx_claim_provenance_candidate,priority:4"`
	CandidateID         string    `json:"candidate_id" gorm:"not null;size:36;uniqueIndex:idx_claim_provenance_candidate,priority:5"`
	RunID               string    `json:"run_id,omitempty" gorm:"not null;size:128"`
	WorkingCopyRevision int64     `json:"working_copy_revision" gorm:"not null;uniqueIndex:idx_claim_provenance_candidate,priority:6"`
	StartUTF16          int       `json:"start_utf16" gorm:"not null"`
	EndUTF16            int       `json:"end_utf16" gorm:"not null"`
	BodyHash            string    `json:"-" gorm:"not null;size:64"`
	Status              string    `json:"status" gorm:"not null;size:24"`
	CreatedAt           time.Time `json:"created_at"`
}

func (DraftClaimProvenance) TableName() string { return "lingdoc_claim_provenance" }
func draftClaimBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// RecordDraftClaimProvenance must be called in the same transaction as the
// working-copy write, or by a recoverable pending-apply flow after CAS. The
// caller must have already verified the candidate's evidence under authority.
func RecordDraftClaimProvenance(tx *gorm.DB, projectID, chapterID, kind, candidateID, runID string, revision int64, start int, body, inserted string, currentBody string) error {
	if tx == nil || projectID == "" || chapterID == "" || candidateID == "" || revision < 1 || (kind != "generation" && kind != "rewrite") || strings.TrimSpace(inserted) == "" {
		return ErrInvalidRequest
	}
	end := start + len(utf16.Encode([]rune(inserted)))
	if !reviewanchor.Valid(body, reviewanchor.Selection{StartUTF16: start, EndUTF16: end, QuoteMarkdown: inserted}) {
		return ErrInvalidRequest
	}
	status := "needs_revalidation"
	if currentBody != body {
		status = "stale"
	}
	row := DraftClaimProvenance{ID: uuid.NewString(), ProjectID: projectID, ChapterID: chapterID, OriginKind: kind, CandidateID: candidateID, RunID: runID, WorkingCopyRevision: revision, StartUTF16: start, EndUTF16: end, BodyHash: draftClaimBodyHash(body), Status: status, CreatedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "chapter_id"}, {Name: "chapter_version_id"}, {Name: "origin_kind"}, {Name: "candidate_id"}, {Name: "working_copy_revision"}}, DoNothing: true}).Create(&row).Error
}

// RecordChapterClaimProvenance is called after the adoption transaction has
// revalidated the referenced candidate and pinned its whole body to a version.
func RecordChapterClaimProvenance(tx *gorm.DB, projectID, chapterID, versionID, candidateID, runID, body string, revision int64) error {
	if tx == nil || projectID == "" || chapterID == "" || versionID == "" || candidateID == "" || strings.TrimSpace(body) == "" || revision < 0 {
		return ErrInvalidRequest
	}
	row := DraftClaimProvenance{ID: uuid.NewString(), ProjectID: projectID, ChapterID: chapterID, ChapterVersionID: versionID, OriginKind: "generation", CandidateID: candidateID, RunID: runID, WorkingCopyRevision: revision, StartUTF16: 0, EndUTF16: len(utf16.Encode([]rune(body))), BodyHash: draftClaimBodyHash(body), Status: "needs_revalidation", CreatedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "chapter_id"}, {Name: "chapter_version_id"}, {Name: "origin_kind"}, {Name: "candidate_id"}, {Name: "working_copy_revision"}}, DoNothing: true}).Create(&row).Error
}

func invalidateDraftClaimProvenance(tx *gorm.DB, projectID, chapterID, body string) error {
	if !tx.Migrator().HasTable(&DraftClaimProvenance{}) {
		return nil
	}
	return tx.Model(&DraftClaimProvenance{}).Where("project_id = ? AND chapter_id = ? AND chapter_version_id = '' AND status <> ? AND body_hash <> ?", projectID, chapterID, "stale", draftClaimBodyHash(body)).Update("status", "stale").Error
}

func InvalidateDraftClaimProvenance(tx *gorm.DB, projectID, chapterID, body string) error {
	return invalidateDraftClaimProvenance(tx, projectID, chapterID, body)
}

// SnapshotChapterClaimProvenance pins the draft's historical origin records
// to an immutable version. Even a current card is only 'needs_revalidation':
// the source must be authorized again when a user opens its candidate.
func SnapshotChapterClaimProvenance(db *gorm.DB, tx Transaction, projectID, chapterID, versionID, body string) error {
	if !db.Migrator().HasTable(&DraftClaimProvenance{}) {
		return nil
	}
	var rows []DraftClaimProvenance
	if err := db.Where("project_id = ? AND chapter_id = ? AND chapter_version_id = ''", projectID, chapterID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.BodyHash != draftClaimBodyHash(body) {
			row.Status = "stale"
		}
		var refs []VersionRef
		if row.Status != "stale" {
			switch row.OriginKind {
			case "generation":
				var stored struct{ BasisJSON string }
				if err := db.Table("lingdoc_candidates").Select("basis_json").Where("id = ? AND project_id = ? AND chapter_id = ?", row.CandidateID, projectID, chapterID).Take(&stored).Error; err != nil {
					row.Status = "stale"
					break
				}
				var basis struct {
					EvidenceCardRefs []VersionRef `json:"evidence_card_refs"`
				}
				if json.Unmarshal([]byte(stored.BasisJSON), &basis) != nil {
					row.Status = "stale"
					break
				}
				refs = basis.EvidenceCardRefs
			case "rewrite":
				var stored struct{ EvidenceCardRefsJSON string }
				if err := db.Table("lingdoc_selected_rewrites").Select("evidence_card_refs_json").Where("id = ? AND project_id = ? AND chapter_id = ?", row.CandidateID, projectID, chapterID).Take(&stored).Error; err != nil {
					row.Status = "stale"
					break
				}
				if json.Unmarshal([]byte(stored.EvidenceCardRefsJSON), &refs) != nil {
					row.Status = "stale"
				}
			default:
				row.Status = "stale"
			}
			if len(refs) == 0 || tx == nil || validateNativeCandidateCards(tx, Project{ID: projectID}, refs) != nil {
				row.Status = "stale"
			}
		}
		row.ID = uuid.NewString()
		row.ChapterVersionID = versionID
		row.CreatedAt = time.Now().UTC()
		if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "chapter_id"}, {Name: "chapter_version_id"}, {Name: "origin_kind"}, {Name: "candidate_id"}, {Name: "working_copy_revision"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (t gormTransaction) RestoreVersionClaimProvenance(projectID, chapterID, versionID string, revision int64, body string) error {
	if !t.db.Migrator().HasTable(&DraftClaimProvenance{}) {
		return nil
	}
	var rows []DraftClaimProvenance
	if err := t.db.Where("project_id = ? AND chapter_id = ? AND chapter_version_id = ?", projectID, chapterID, versionID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.BodyHash != draftClaimBodyHash(body) {
			row.Status = "stale"
		}
		row.ID = uuid.NewString()
		row.ChapterVersionID = ""
		row.WorkingCopyRevision = revision
		row.CreatedAt = time.Now().UTC()
		if err := t.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "chapter_id"}, {Name: "chapter_version_id"}, {Name: "origin_kind"}, {Name: "candidate_id"}, {Name: "working_copy_revision"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func staleDraftClaimProvenance(records []DraftClaimProvenance, body string) []DraftClaimProvenance {
	out := append([]DraftClaimProvenance(nil), records...)
	for i := range out {
		if out[i].BodyHash != draftClaimBodyHash(body) {
			out[i].Status = "stale"
		}
	}
	return out
}
