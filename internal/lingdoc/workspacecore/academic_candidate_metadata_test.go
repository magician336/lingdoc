package workspacecore

import (
	"reflect"
	"testing"
)

func TestAcademicCandidateMetadataCopiesFieldsAndOwnsSlices(t *testing.T) {
	result := ResearchResult{DOI: "10.1/example", Author: "Author", PublishedAt: "2024", Journal: "Journal", ISSNs: []string{"1111-1111"}, LicenseURLs: []string{"https://example.org/license"}, CatalogVersion: "catalog", PolicySnapshotVersion: "policy-version", PolicyStatus: "approved", RetractionStatus: "unknown", DuplicateStatus: "unique", AcquisitionStatus: "metadata_only"}
	candidate := ImportCandidate{ID: "candidate", Title: "unchanged title", URL: "https://publisher.example"}
	applyAcademicCandidateMetadata(&candidate, result)
	want := ImportCandidate{ID: "candidate", Title: "unchanged title", SelectionType: "academic_metadata", DOI: result.DOI, Author: result.Author, PublishedAt: result.PublishedAt, Journal: result.Journal, ISSNs: []string{"1111-1111"}, LicenseURLs: []string{"https://example.org/license"}, CatalogVersion: result.CatalogVersion, PolicySnapshotVersion: result.PolicySnapshotVersion, PolicyStatus: result.PolicyStatus, RetractionStatus: result.RetractionStatus, DuplicateStatus: result.DuplicateStatus, AcquisitionStatus: result.AcquisitionStatus, License: "https://example.org/license"}
	if !reflect.DeepEqual(candidate, want) {
		t.Fatalf("metadata candidate = %+v, want %+v", candidate, want)
	}
	result.ISSNs[0], result.LicenseURLs[0] = "changed", "changed"
	if !reflect.DeepEqual(candidate, want) {
		t.Fatal("candidate metadata changed when the source slices were mutated")
	}
	applyAcademicCandidateMetadata(&candidate, ResearchResult{})
	if candidate.License != "Crossref 未提供论文许可信息；仅保存元数据" || candidate.ISSNs != nil || candidate.LicenseURLs != nil {
		t.Fatalf("empty metadata fallback changed: %+v", candidate)
	}
}
