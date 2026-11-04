package workspace

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/academic/crossref"
)

func TestCrossrefRetractionTargetsRequireExplicitDistinctDOIs(t *testing.T) {
	works := []crossref.Work{
		{DOI: "10.1000/Notice", UpdateTo: []crossref.UpdateTo{
			{DOI: " 10.1000/Paper ", Type: " RETRACTION "},
			{DOI: "10.1000/Notice", Type: "retraction"},
			{DOI: "10.1000/correction", Type: "correction"},
			{DOI: "", Type: "retraction"},
		}},
		{UpdateTo: []crossref.UpdateTo{{DOI: "10.1000/no-notice-id", Type: "retraction"}}},
	}
	got := crossrefRetractionTargets(works)
	if len(got) != 1 || !got["10.1000/paper"] {
		t.Fatalf("non-retraction or unidentifiable relationship became a target: %#v", got)
	}
}
