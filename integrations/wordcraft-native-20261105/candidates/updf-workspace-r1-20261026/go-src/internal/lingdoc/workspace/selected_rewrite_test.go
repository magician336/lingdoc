package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSelectionByteRangeUsesUTF16OffsetsAndWholeRuneBoundaries(t *testing.T) {
	body := "A🧪研究B"
	selection := RewriteSelection{StartUTF16: 1, EndUTF16: 5, SelectedText: "🧪研究"}
	start, end, ok := selectionByteRange(body, selection)
	if !ok || body[start:end] != selection.SelectedText {
		t.Fatalf("selectionByteRange() = (%d, %d, %t), selected %q", start, end, ok, body[start:end])
	}
	if got := body[:start] + "改写" + body[end:]; got != "A改写B" {
		t.Fatalf("replacement = %q, want %q", got, "A改写B")
	}

	partialSurrogate := RewriteSelection{StartUTF16: 1, EndUTF16: 2, SelectedText: "�"}
	if _, _, ok := selectionByteRange(body, partialSurrogate); ok {
		t.Fatal("selectionByteRange() accepted a range that splits a UTF-16 surrogate pair")
	}
}

func TestValidateRewriteOutputRequiresCitationsAndSourceIDsToMatch(t *testing.T) {
	valid := SelectedRewriteOutput{ReplacementMarkdown: "结论 [[source:src-a]]", SourceIDs: []string{"src-a"}}
	if err := validateRewriteOutput(valid, []string{"src-a", "src-b"}); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}

	valid.SourceIDs = []string{"src-b"}
	if err := validateRewriteOutput(valid, []string{"src-a", "src-b"}); err == nil {
		t.Fatal("output with a source ID not present in the replacement was accepted")
	}

	valid.SourceIDs = []string{"src-a"}
	valid.ReplacementMarkdown = "结论 [[source:src-c]]"
	if err := validateRewriteOutput(valid, []string{"src-a", "src-b"}); err == nil {
		t.Fatal("output citing an unauthorized source was accepted")
	}
}

func TestWriteRewriteResultPersistsOnlySourcesActuallyUsed(t *testing.T) {
	now := time.Date(2026, time.October, 3, 0, 0, 0, 0, time.UTC)
	snapshots, err := json.Marshal([]RewriteSourceSnapshot{
		{SourceID: "src-a", AssetID: "asset-a", AssetRevision: 1, Locator: "page/1", QuotedTextHash: "hash-a", AuthorizedAt: now},
		{SourceID: "src-b", AssetID: "asset-b", AssetRevision: 2, Locator: "page/2", QuotedTextHash: "hash-b", AuthorizedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceIDs, _ := json.Marshal([]string{"src-a", "src-b"})
	row := selectedRewriteRow{ID: "candidate-1", SourceIDsJSON: string(sourceIDs), AuthorizedSourcesJSON: string(snapshots), ReviewItemsJSON: "[]"}
	output := SelectedRewriteOutput{ReplacementMarkdown: "有据可依 [[source:src-b]]", SourceIDs: []string{"src-b"}}
	if err := writeRewriteResult(&row, output); err != nil {
		t.Fatalf("writeRewriteResult() error = %v", err)
	}

	var persistedIDs []string
	var persistedSnapshots []RewriteSourceSnapshot
	if err := json.Unmarshal([]byte(row.SourceIDsJSON), &persistedIDs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(row.AuthorizedSourcesJSON), &persistedSnapshots); err != nil {
		t.Fatal(err)
	}
	if len(persistedIDs) != 1 || persistedIDs[0] != "src-b" || len(persistedSnapshots) != 1 || persistedSnapshots[0].SourceID != "src-b" {
		t.Fatalf("persisted source set = %v / %v, want only src-b", persistedIDs, persistedSnapshots)
	}
	if row.Status != "ready" || row.RunMode != "" {
		t.Fatalf("candidate status/run mode = %q/%q, want ready/empty (filled by caller)", row.Status, row.RunMode)
	}
}
