package workspacecore

import (
	"slices"
	"strings"
)

// applyAcademicCandidateMetadata copies only the metadata already checked by
// the caller. It does not authorize import or establish source equivalence.
func applyAcademicCandidateMetadata(candidate *ImportCandidate, result ResearchResult) {
	candidate.SelectionType = "academic_metadata"
	candidate.URL = ""
	candidate.DOI = result.DOI
	candidate.Author = result.Author
	candidate.PublishedAt = result.PublishedAt
	candidate.Journal = result.Journal
	candidate.ISSNs = slices.Clone(result.ISSNs)
	candidate.LicenseURLs = slices.Clone(result.LicenseURLs)
	candidate.CatalogVersion = result.CatalogVersion
	candidate.PolicySnapshotVersion = result.PolicySnapshotVersion
	candidate.PolicyStatus = result.PolicyStatus
	candidate.RetractionStatus = result.RetractionStatus
	candidate.DuplicateStatus = result.DuplicateStatus
	candidate.AcquisitionStatus = result.AcquisitionStatus
	if len(candidate.LicenseURLs) == 0 {
		candidate.License = "Crossref 未提供论文许可信息；仅保存元数据"
	} else {
		candidate.License = strings.Join(candidate.LicenseURLs, ", ")
	}
}
