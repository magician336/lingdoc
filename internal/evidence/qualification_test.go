package evidence

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The material qualification gate.
//
// A material may only enter an acceptance run when every one of its chunks
// is the verbatim slice of the origin at its own coordinates. The check has
// to name what is missing: which chunk, how much longer the content is than
// the coordinate span, whether the coordinates are out of range. A gate that
// only says "not qualified" sends the next person back to the same
// diagnosis (#485 / D1).
// ---------------------------------------------------------------------------

const qualificationOrigin = `# 合成材料

| 名称 | 值 |
| --- | --- |
| A | 1 |
| B | 2 |

正文第一段。
`

func TestQualifyMaterialAcceptsVerbatimChunks(t *testing.T) {
	origin := qualificationOrigin
	runes := []rune(origin)
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 0, EndAt: 5, Content: string(runes[0:5])},
		{ID: "c2", StartAt: 5, EndAt: 20, Content: string(runes[5:20])},
	}

	report := QualifyMaterial(origin, chunks)

	if !report.Qualified {
		t.Fatalf("material must be qualified, issues: %+v", report.Issues)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("expected no issues, got %+v", report.Issues)
	}
	if len(report.Chunks) != 2 {
		t.Fatalf("expected one verdict per chunk, got %d", len(report.Chunks))
	}
	for _, verdict := range report.Chunks {
		if !verdict.Verbatim {
			t.Fatalf("chunk %s must be verbatim: %+v", verdict.ChunkID, verdict)
		}
		if verdict.ContentChars != verdict.CoordinateSpan {
			t.Fatalf("chunk %s: content %d != span %d", verdict.ChunkID, verdict.ContentChars, verdict.CoordinateSpan)
		}
	}
}

// The case that actually happened: the chunk carries a synthesized table
// header, so its content is longer than the coordinate span and the origin
// slice at those coordinates is something else entirely.
func TestQualifyMaterialRejectsAChunkThatCarriesASynthesizedHeader(t *testing.T) {
	origin := qualificationOrigin
	runes := []rune(origin)
	body := string(runes[14:29]) // the first table row
	header := "| 名称 | 值 |\n| --- | --- |\n"
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 14, EndAt: 29, Content: header + body},
	}

	report := QualifyMaterial(origin, chunks)

	if report.Qualified {
		t.Fatal("a chunk that is not the origin slice must not be qualified")
	}
	if len(report.Issues) != 1 {
		t.Fatalf("expected exactly one issue, got %+v", report.Issues)
	}
	issue := report.Issues[0]
	if issue.Code != MaterialIssueNotVerbatim {
		t.Fatalf("issue code = %q, want %q", issue.Code, MaterialIssueNotVerbatim)
	}
	if issue.ChunkID != "c1" {
		t.Fatalf("issue must name the chunk, got %q", issue.ChunkID)
	}
	// The detail has to say how much longer the content is than the span.
	if !strings.Contains(issue.Detail, "相差") {
		t.Fatalf("the detail must state the length difference: %q", issue.Detail)
	}
	if !strings.Contains(issue.Detail, "首个不一致位置") {
		t.Fatalf("the detail must state where the two diverge: %q", issue.Detail)
	}
}

func TestQualifyMaterialRejectsCoordinatesOutOfRange(t *testing.T) {
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 0, EndAt: 9999, Content: "x"},
	}

	report := QualifyMaterial(qualificationOrigin, chunks)

	if report.Qualified {
		t.Fatal("out-of-range coordinates must not be qualified")
	}
	if len(report.Issues) != 1 || report.Issues[0].Code != MaterialIssueCoordinatesOutOfRange {
		t.Fatalf("expected a coordinates_out_of_range issue, got %+v", report.Issues)
	}
	if !strings.Contains(report.Issues[0].Detail, "坐标越界") {
		t.Fatalf("the detail must say the coordinates are out of range: %q", report.Issues[0].Detail)
	}
}

// The chunks of one material overlap. The two chunks must carry the SAME
// text over the region they share: if they do not, the material says two
// different things about the same characters, and no reader can tell which
// one is the original.
func TestQualifyMaterialReportsTheOverlapVerdict(t *testing.T) {
	origin := qualificationOrigin
	runes := []rune(origin)
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 0, EndAt: 20, Content: string(runes[0:20])},
		{ID: "c2", StartAt: 10, EndAt: 30, Content: string(runes[10:30])},
	}

	report := QualifyMaterial(origin, chunks)

	if len(report.Overlaps) != 1 {
		t.Fatalf("expected one overlap verdict, got %+v", report.Overlaps)
	}
	overlap := report.Overlaps[0]
	if overlap.FirstChunkID != "c1" || overlap.SecondChunkID != "c2" {
		t.Fatalf("the verdict must name both chunks: %+v", overlap)
	}
	if overlap.StartAt != 10 || overlap.EndAt != 20 || overlap.Span != 10 {
		t.Fatalf("the verdict must state the shared region: %+v", overlap)
	}
	if !overlap.Identical {
		t.Fatalf("two verbatim chunks must agree over their overlap: %+v", overlap)
	}
	if overlap.FirstDifference != -1 {
		t.Fatalf("an identical overlap has no differing position: %+v", overlap)
	}
}

// The negative case: the two chunks disagree over the region they share.
// That is the "逐位冲突" the acceptance run has to catch, and it has to be
// reported, not averaged away.
func TestQualifyMaterialReportsAConflictingOverlap(t *testing.T) {
	origin := qualificationOrigin
	runes := []rune(origin)
	first := string(runes[0:20])
	second := string(runes[10:30])
	// Change one character inside the shared region [10,20).
	second = "X" + second[1:]
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 0, EndAt: 20, Content: first},
		{ID: "c2", StartAt: 10, EndAt: 30, Content: second},
	}

	report := QualifyMaterial(origin, chunks)

	if len(report.Overlaps) != 1 {
		t.Fatalf("expected one overlap verdict, got %+v", report.Overlaps)
	}
	overlap := report.Overlaps[0]
	if overlap.Identical {
		t.Fatalf("a conflicting overlap must not be reported as identical: %+v", overlap)
	}
	if overlap.FirstDifference != 0 {
		t.Fatalf("the differing position must be reported (0 = the first shared rune): %+v", overlap)
	}
}

func TestQualifyMaterialRejectsEmptyContent(t *testing.T) {
	chunks := []MaterialChunk{
		{ID: "c1", StartAt: 0, EndAt: 5, Content: ""},
	}

	report := QualifyMaterial(qualificationOrigin, chunks)

	if report.Qualified {
		t.Fatal("an empty chunk must not be qualified")
	}
	if len(report.Issues) != 1 || report.Issues[0].Code != MaterialIssueEmptyContent {
		t.Fatalf("expected an empty_content issue, got %+v", report.Issues)
	}
}

func TestQualifyMaterialRejectsAMaterialWithoutChunks(t *testing.T) {
	report := QualifyMaterial(qualificationOrigin, nil)

	if report.Qualified {
		t.Fatal("a material without chunks must not be qualified")
	}
}

func TestQualificationSummaryNamesTheFailingChunk(t *testing.T) {
	chunks := []MaterialChunk{
		{ID: "chunk-7", StartAt: 0, EndAt: 5, Content: "not the slice"},
	}

	summary := QualifyMaterial(qualificationOrigin, chunks).QualificationSummary()

	if !strings.Contains(summary, "chunk-7") {
		t.Fatalf("the summary must name the failing chunk: %q", summary)
	}
	if !strings.Contains(summary, "不合格") {
		t.Fatalf("the summary must state that the material is not qualified: %q", summary)
	}
}
