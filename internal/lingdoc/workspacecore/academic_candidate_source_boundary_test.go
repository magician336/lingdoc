package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A Crossref result is a metadata-only lead. It must not acquire an unrelated
// project original merely because a caller supplied a readable source ID.
// A future verified association needs a separate provenance contract; this
// test intentionally does not claim a metadata record proves that relation.
func TestAcademicMetadataCandidateDoesNotAdoptUnverifiedSource(t *testing.T) {
	ctx := context.Background()
	t.Run("unrelated readable project source", func(t *testing.T) {
		f, run, _ := setupAcademicResearchRun(t)
		input := academicCandidateInput(f, run)
		input.SourceID = "source-1" // unrelated to the synthetic Crossref DOI
		raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "academic-unrelated-source", input)
		if err != nil {
			candidates, listErr := f.svc.ListImportCandidates(ctx, f.owner, f.project.ID)
			if listErr != nil || len(candidates) != 0 {
				t.Fatalf("rejected request persisted a candidate: count=%d error=%v", len(candidates), listErr)
			}
			return
		}
		var candidate ImportCandidate
		if err := json.Unmarshal(raw, &candidate); err != nil {
			t.Fatalf("decode candidate: %v", err)
		}
		if candidate.SelectionType != "academic_metadata" || candidate.SaveMode != "metadata_only" || candidate.SourceID != "" || candidate.KnowledgeID != "" || candidate.Locator != "" || candidate.QuotedTextHash != "" {
			t.Fatalf("Crossref metadata retained an unverified original association: selection=%q mode=%q source=%q knowledge=%q locator=%q hash_present=%v", candidate.SelectionType, candidate.SaveMode, candidate.SourceID, candidate.KnowledgeID, candidate.Locator, candidate.QuotedTextHash != "")
		}
	})

	for _, tc := range []struct {
		name, sourceID string
		denied         bool
	}{
		{name: "denied project source", sourceID: "source-2", denied: true},
		{name: "missing source", sourceID: "source-does-not-exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, run, _ := setupAcademicResearchRun(t)
			if tc.denied {
				f.source.denied[tc.sourceID] = true
			}
			input := academicCandidateInput(f, run)
			input.SourceID = tc.sourceID
			if _, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "academic-restricted-source", input); err == nil {
				t.Fatal("a denied or missing source must not create a metadata candidate")
			}
			candidates, err := f.svc.ListImportCandidates(ctx, f.owner, f.project.ID)
			if err != nil || len(candidates) != 0 {
				t.Fatalf("rejected request persisted a candidate: count=%d error=%v", len(candidates), err)
			}
		})
	}

	t.Run("metadata without a source remains available", func(t *testing.T) {
		f, run, _ := setupAcademicResearchRun(t)
		raw, _, _, err := f.svc.CreateImportCandidate(ctx, f.owner, f.project.ID, "academic-metadata-only", academicCandidateInput(f, run))
		candidate := decodeAgentJSON[ImportCandidate](t, raw, err)
		if candidate.SelectionType != "academic_metadata" || candidate.SaveMode != "metadata_only" || candidate.SourceID != "" || candidate.DOI != run.Results[0].DOI {
			t.Fatalf("metadata-only candidate not preserved: selection=%q mode=%q source=%q", candidate.SelectionType, candidate.SaveMode, candidate.SourceID)
		}
	})
}

// Persisted provider data is untrusted as well: a Crossref metadata record
// cannot establish an original's identity, location, or quote hash. Model
// each poisoned field separately so a later adapter change cannot silently
// turn a metadata hit into evidence by attaching a single provenance field.
func TestAcademicMetadataCandidateRejectsPoisonedPersistedResult(t *testing.T) {
	cases := []struct {
		name   string
		poison func(*ResearchResult)
	}{
		{name: "source ID", poison: func(result *ResearchResult) { result.SourceID = "source-1" }},
		{name: "locator", poison: func(result *ResearchResult) { result.Locator = "page:1" }},
		{name: "quote hash", poison: func(result *ResearchResult) { result.QuotedTextHash = "synthetic-unverified-hash" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, run, _ := setupAcademicResearchRun(t)
			tc.poison(&run.Results[0])
			payload, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			// Reproduce a persisted upstream result containing unverified
			// provenance. Never modify a real provider or production database.
			write := f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json = ? WHERE project_id = ? AND kind = ? AND id = ?", string(payload), f.project.ID, ResearchRunKind, run.ID)
			if write.Error != nil || write.RowsAffected != 1 {
				t.Fatalf("seed poisoned result: rows=%d error=%v", write.RowsAffected, write.Error)
			}
			input := academicCandidateInput(f, run)
			_, _, _, err = f.svc.CreateImportCandidate(context.Background(), f.owner, f.project.ID, "academic-poisoned-result", input)
			if !errors.Is(err, ErrEvidenceIncomplete) {
				t.Fatalf("unverified %s must be refused as incomplete evidence: %v", tc.name, err)
			}
			candidates, err := f.svc.ListImportCandidates(context.Background(), f.owner, f.project.ID)
			if err != nil || len(candidates) != 0 {
				t.Fatalf("poisoned result persisted a candidate: count=%d error=%v", len(candidates), err)
			}
		})
	}
}
