package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAcademicSourceCatalogIsProjectScopedVersionedAndFailClosed(t *testing.T) {
	f := newAgentFixture(t, true)
	ctx := context.Background()

	initial, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil || initial.Version != 0 || initial.GlobalVersion != AcademicGlobalCatalogVersion || len(initial.Rules) != 0 {
		t.Fatalf("initial catalog: %+v %v", initial, err)
	}
	globalVersion := initial.SourceCatalogVersion

	input := UpdateAcademicSourceCatalogInput{
		VersionExpectation: f.expectation(0),
		Rules: []AcademicSourceRule{{
			ID: "journal-example", SourceType: "journal", VenueName: "Journal of Examples",
			Maintainer: "spoofed-client-maintainer",
			VenueIDs:   []string{"1234-5678"}, AllowedLicenseURLs: []string{"https://creativecommons.org/licenses/by/4.0/"},
		}},
	}
	raw, status, replay, err := f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "g2-academic-catalog-v1", input)
	if err != nil || status != http.StatusOK || replay {
		t.Fatalf("update catalog: status=%d replay=%v err=%v", status, replay, err)
	}
	var saved AcademicSourceCatalog
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.GlobalVersion != AcademicGlobalCatalogVersion || len(saved.Rules) != 1 || saved.Rules[0].Maintainer != f.owner.UserID || saved.Rules[0].Version != 1 {
		t.Fatalf("stored catalog metadata: %+v", saved)
	}
	if saved.SourceCatalogVersion == globalVersion {
		t.Fatal("project extension did not advance source catalog version")
	}

	readBack, err := f.svc.GetAcademicSourceCatalog(ctx, f.owner, f.project.ID)
	if err != nil || readBack.Version != saved.Version || readBack.SourceCatalogVersion != saved.SourceCatalogVersion || len(readBack.Rules) != 1 {
		t.Fatalf("catalog readback: %+v %v", readBack, err)
	}
	if _, _, _, err = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "g2-academic-catalog-stale", UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: input.Rules}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale catalog update accepted: %v", err)
	}
	if _, _, _, err = f.svc.UpdateAcademicSourceCatalog(ctx, f.owner, f.project.ID, "g2-academic-catalog-bad-license", UpdateAcademicSourceCatalogInput{
		VersionExpectation: f.expectation(saved.Version),
		Rules:              []AcademicSourceRule{{ID: "bad", SourceType: "journal", VenueName: "Journal", VenueIDs: []string{"1234-5678"}, AllowedLicenseURLs: []string{"http://license.example/"}}},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-HTTPS license URL accepted: %v", err)
	}
}

func TestAcademicSourceCatalogVersionIsOrderIndependentAndRuleSensitive(t *testing.T) {
	first := []AcademicSourceRule{
		{ID: "b", SourceType: "journal", VenueName: "B", VenueIDs: []string{"8765-4321", "1234-5678"}, AllowedLicenseURLs: []string{"https://example.org/b", "https://example.org/a"}},
		{ID: "a", SourceType: "conference", VenueName: "A", VenueIDs: []string{"conference:example"}},
	}
	second := []AcademicSourceRule{
		{ID: "a", SourceType: "conference", VenueName: "A", VenueIDs: []string{"conference:example"}},
		{ID: "b", SourceType: "journal", VenueName: "B", VenueIDs: []string{"1234-5678", "8765-4321"}, AllowedLicenseURLs: []string{"https://example.org/a", "https://example.org/b"}},
	}
	if AcademicSourceCatalogVersion(first) != AcademicSourceCatalogVersion(second) {
		t.Fatal("catalog version changed only because of input order")
	}
	second[1].VenueName = "Changed"
	if AcademicSourceCatalogVersion(first) == AcademicSourceCatalogVersion(second) {
		t.Fatal("venue change did not advance catalog version")
	}
}
