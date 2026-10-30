package workspacecore

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

const academicRetractionKind = "academic_retraction"

type academicRetractionRecord struct {
	DOI        string    `json:"doi"`
	ObservedBy string    `json:"observed_by"`
	ObservedAt time.Time `json:"observed_at"`
}

func academicRetractionID(doi string) string {
	return sha256Hex(strings.ToLower(strings.TrimSpace(doi)))
}

// knownAcademicRetraction is a durable, fail-closed project-level observation.
// A later search lacking the notice is not evidence that retraction was undone.
func knownAcademicRetraction(tx Transaction, projectID, doi string) (bool, error) {
	if strings.TrimSpace(doi) == "" {
		return false, ErrInvalidRequest
	}
	store, err := agentObjectStore(tx)
	if err != nil {
		return false, err
	}
	_, err = store.Object(projectID, academicRetractionKind, academicRetractionID(doi))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// All writers that create or invalidate catalog-bound evidence serialize on
// the same project row. SQLite needs an actual write to acquire its lock.
func lockAcademicEvidenceProject(tx Transaction, actor Actor, projectID string) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrDependencyUnavailable
	}
	if db.Name() == "sqlite" {
		result := db.Model(&projectRow{}).Where("id = ? AND tenant_id = ?", projectID, actor.TenantID).UpdateColumn("id", projectID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		return nil
	}
	var row projectRow
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ? AND tenant_id = ?", projectID, actor.TenantID).Take(&row).Error; err != nil {
		return storageError(err)
	}
	return nil
}

// invalidateAcademicEvidenceCards appends a new version only for cards whose
// explicitly recorded academic association matches the trigger. It runs in
// the caller's authorized transaction with the catalog or research update.
// The older confirmed payload and its confirmation remain in version history.
func invalidateAcademicEvidenceCards(tx Transaction, actor Actor, projectID, reason, trigger string, affected func(AcademicSourceAssociation) bool) error {
	store, err := agentObjectStore(tx)
	if err != nil {
		return err
	}
	rows, err := store.Objects(projectID, EvidenceCardKind)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var card EvidenceCard
		if err := json.Unmarshal(row.Payload, &card); err != nil {
			return err
		}
		if len(card.AcademicAssociationRefs) == 0 || card.Status == "NEEDS_REVIEW" || card.Status == "SUPERSEDED" || card.Status == "REVOKED" || card.Status == "RETRACTED" || card.Status == "OUT_OF_POLICY" {
			continue
		}
		matched := false
		for _, ref := range card.AcademicAssociationRefs {
			association, _, err := loadStoredObject[AcademicSourceAssociation](tx, projectID, AcademicSourceAssociationKind, ref.ID)
			if err != nil {
				return err
			}
			if affected(association) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		previousVersion := card.Version
		card.Version++
		card.Status = "NEEDS_REVIEW"
		card.InvalidReason = reason
		card.Confirmation = nil
		if err := storeStoredObject(tx, row, projectID, EvidenceCardKind, card.ID, card.Version, card); err != nil {
			return err
		}
		if err := recordAgentAuditEvent(tx, actor, projectID, "g2.evidence.invalidate", card.ID, reason, map[string]any{"trigger_kind": trigger, "previous_version": previousVersion, "version": card.Version}); err != nil {
			return err
		}
	}
	return nil
}

func invalidateAcademicEvidenceForCatalog(tx Transaction, actor Actor, projectID, catalogVersion string) error {
	return invalidateAcademicEvidenceCards(tx, actor, projectID, "academic_catalog_changed", "AcademicSourceCatalog", func(a AcademicSourceAssociation) bool {
		return a.CatalogVersion != catalogVersion
	})
}

// A retraction is acted upon only when the result identifies the affected DOI
// as retracted. A notice's own DOI is not the withdrawn article's DOI.
func academicRetractedDOIs(results []ResearchResult) map[string]bool {
	retracted := make(map[string]bool)
	for _, result := range results {
		if result.Provider == AcademicCrossrefResultProvider && result.RetractionStatus == "retracted" {
			if doi := strings.ToLower(strings.TrimSpace(result.DOI)); doi != "" {
				retracted[doi] = true
			}
		}
	}
	return retracted
}

func invalidateAcademicEvidenceForRetractions(tx Transaction, actor Actor, projectID string, results []ResearchResult) error {
	retracted := academicRetractedDOIs(results)
	if len(retracted) == 0 {
		return nil
	}
	store, err := agentObjectStore(tx)
	if err != nil {
		return err
	}
	dois := make([]string, 0, len(retracted))
	for doi := range retracted {
		dois = append(dois, doi)
	}
	for _, doi := range sortedUniqueIDs(dois) {
		known, err := knownAcademicRetraction(tx, projectID, doi)
		if err != nil {
			return err
		}
		if known {
			continue
		}
		observation := academicRetractionRecord{DOI: doi, ObservedBy: actor.UserID, ObservedAt: time.Now().UTC()}
		if err := storeStoredObject(tx, StoredObject{}, projectID, academicRetractionKind, academicRetractionID(doi), 1, observation); err != nil {
			return err
		}
		if err := recordAgentAuditEvent(tx, actor, projectID, "g2.academic_source.retraction_observed", academicRetractionID(doi), "academic_source_retracted", nil); err != nil {
			return err
		}
	}
	candidates, err := store.Objects(projectID, ImportCandidateKind)
	if err != nil {
		return err
	}
	for _, row := range candidates {
		var candidate ImportCandidate
		if err := json.Unmarshal(row.Payload, &candidate); err != nil {
			return err
		}
		if candidate.SelectionType != "academic_metadata" || candidate.RetractionStatus == "retracted" || !retracted[strings.ToLower(strings.TrimSpace(candidate.DOI))] {
			continue
		}
		previousVersion := candidate.Version
		candidate.Version++
		candidate.RetractionStatus = "retracted"
		if err := storeStoredObject(tx, row, projectID, ImportCandidateKind, candidate.ID, candidate.Version, candidate); err != nil {
			return err
		}
		if err := recordAgentAuditEvent(tx, actor, projectID, "g2.academic_source.retracted", candidate.ID, "academic_source_retracted", map[string]any{"previous_version": previousVersion, "version": candidate.Version}); err != nil {
			return err
		}
	}
	return invalidateAcademicEvidenceCards(tx, actor, projectID, "academic_source_retracted", "ResearchResult", func(a AcademicSourceAssociation) bool {
		return retracted[strings.ToLower(strings.TrimSpace(a.DOI))]
	})
}
