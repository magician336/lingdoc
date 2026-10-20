package workspacecore

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ChapterConfirmationFact is what one confirmation record carries.
//
// The record is written by the candidate adoption confirmation service
// (internal/lingdoc/candidateadoption/confirmation.go), so these fields mirror
// `candidateadoption.Confirmation`. The workspace side used to read a narrower
// subset of them — its details struct declared only the id, the chapter version
// id, the spec revision, the template version and the valid flag — which is one
// reason the three consumers could disagree about the same chapter.
type ChapterConfirmationFact struct {
	// ID is the confirmation record's own id.
	ID string
	// ChapterID is the chapter the confirmation was made for.
	ChapterID string
	// ChapterVersionID is the chapter version the confirmation was made for.
	ChapterVersionID string
	// SpecRevision is the research-conditions revision the confirmation was made against.
	SpecRevision int64
	// TemplateVersion is the template version the confirmation was made against.
	TemplateVersion string
	// ConfirmerID is the member who confirmed.
	ConfirmerID string
	// ConfirmedAt is when the confirmation was made.
	ConfirmedAt time.Time
	// Valid reports whether the record is still valid.
	Valid bool
}

// ChapterConfirmationBasis is the current state a confirmation record is judged against.
type ChapterConfirmationBasis struct {
	// ChapterID is the chapter being asked about.
	ChapterID string
	// ChapterVersionID is the chapter's current version.
	ChapterVersionID string
	// SpecRevision is the project's current research-conditions revision.
	SpecRevision int64
	// TemplateVersion is the project's current template version.
	TemplateVersion string
}

// ChapterConfirmed reports whether the given chapter version is currently confirmed.
//
// A chapter version is confirmed only when a valid confirmation record exists and
// every fact that record carries still matches the current state: the chapter id,
// the chapter version id, the research-conditions revision, the template version,
// the confirmer and the confirmation time.
//
// DR-47-01: the chapter id must be present and must equal the target chapter. The
// rules governance runtime used to accept an empty chapter id; that lenient
// check is gone.
//
// A record whose own id does not match the id of the row it was read from is not
// a usable record, so the loader drops it before the facts reach this function.
//
// This function owns the question. Every consumer of it — the delivery preflight,
// the rules governance impact blocking decision and the stabilization impact
// resolution — therefore gives the same answer for the same chapter.
func ChapterConfirmed(basis ChapterConfirmationBasis, facts []ChapterConfirmationFact) bool {
	_, ok := ChapterConfirmationOf(basis, facts)
	return ok
}

// ChapterConfirmationOf returns the confirmation record that makes the chapter
// confirmed, if there is one.
//
// It is the same judgment as ChapterConfirmed; a consumer that needs the
// record itself (for example to name it in an audit entry) uses this one.
func ChapterConfirmationOf(basis ChapterConfirmationBasis, facts []ChapterConfirmationFact) (ChapterConfirmationFact, bool) {
	if basis.ChapterID == "" || basis.ChapterVersionID == "" {
		return ChapterConfirmationFact{}, false
	}
	for _, fact := range facts {
		if !fact.Valid {
			continue
		}
		if fact.ID == "" || fact.ChapterID == "" {
			continue
		}
		if fact.ConfirmerID == "" || fact.ConfirmedAt.IsZero() {
			continue
		}
		if fact.ChapterID != basis.ChapterID {
			continue
		}
		if fact.ChapterVersionID != basis.ChapterVersionID {
			continue
		}
		if fact.SpecRevision != basis.SpecRevision {
			continue
		}
		if fact.TemplateVersion != basis.TemplateVersion {
			continue
		}
		return fact, true
	}
	return ChapterConfirmationFact{}, false
}

// markChapterUnconfirmed clears a chapter's confirmation flag.
//
// This is the one place the flag is written. The three situations that make a
// chapter unconfirmed — the chapter body rewrite, the working copy commit and
// the formal basis change — all come through here, so no path can clear the
// flag while another leaves it standing.
//
// The durable half lives in `InvalidateChapterConfirmations`, which clears the
// stored confirmation rows. It does not have to be called from here: a chapter
// version that is no longer current cannot be matched by any confirmation
// record, because the predicate compares the record's chapter version id with
// the current one.
func markChapterUnconfirmed(chapter *Chapter) {
	chapter.ConfirmationValid = false
}

// chapterConfirmationFacts reads the valid confirmation records of one chapter
// version and turns them into the facts the predicate judges.
//
// A record is dropped when its details do not parse, when the details carry a
// different id than the row they were read from, or when the record is not
// valid. The remaining facts are judged by ChapterConfirmed.
func chapterConfirmationFacts(tx *gorm.DB, chapterID, chapterVersionID string) ([]ChapterConfirmationFact, error) {
	var rows []chapterConfirmationRow
	if err := tx.Where("chapter_id = ? AND chapter_version_id = ? AND valid = ?", chapterID, chapterVersionID, true).
		Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	facts := make([]ChapterConfirmationFact, 0, len(rows))
	for _, row := range rows {
		var details chapterConfirmationDetails
		if err := json.Unmarshal([]byte(row.DetailsJSON), &details); err != nil {
			return nil, fmt.Errorf("decode chapter confirmation %q: %w", row.ID, err)
		}
		if details.ID != row.ID {
			continue
		}
		facts = append(facts, ChapterConfirmationFact{
			ID:               row.ID,
			ChapterID:        details.ChapterID,
			ChapterVersionID: details.ChapterVersionID,
			SpecRevision:     details.SpecRevision,
			TemplateVersion:  details.TemplateVersion,
			ConfirmerID:      details.ActorUserID,
			ConfirmedAt:      details.CreatedAt,
			Valid:            details.Valid && row.Valid,
		})
	}
	return facts, nil
}
