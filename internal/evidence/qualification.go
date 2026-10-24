package evidence

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Material qualification — the pre-flight check for a material.
//
// A material is usable as evidence only when every one of its chunks is the
// verbatim slice of the origin text at its own coordinates. The source
// re-check (policy.go) compares the text the origin yields at
// [start_at, end_at) with the quoted text of the source; a chunk that
// carries anything the document does not have at that position (a
// synthesized table header, for example) is judged "the quoted text no
// longer comes from the original" and every operation that re-checks the
// sources of that material is refused.
//
// The check therefore has to run **before** a material enters an acceptance
// run, not after the first 409: the refusal it produces names the chunk, the
// length difference and the first position where the two disagree, so the
// material can be fixed instead of re-diagnosed.
//
// The verdict itself is the one the re-check uses — quoteAt — so the two
// cannot drift apart **in their judgement**.
//
// That is a statement about the judgement, not about the inputs: the caller
// still has to hand in the **original uploaded file** as the origin text. A
// text that differs from it (rewritten line endings, a rewritten image
// reference, a truncated copy) is rejected by evidence.VerbatimOrigin before
// the comparison, because the coordinates of a chunk only mean something
// against the bytes the ingest chain actually split.
// ---------------------------------------------------------------------------

// MaterialIssueCode is the machine-readable reason a chunk is not qualified.
type MaterialIssueCode string

const (
	// MaterialIssueCoordinatesOutOfRange — the coordinates do not address a
	// region of the origin text (negative, empty, or past the end).
	MaterialIssueCoordinatesOutOfRange MaterialIssueCode = "coordinates_out_of_range"
	// MaterialIssueEmptyContent — the chunk carries no content at all.
	MaterialIssueEmptyContent MaterialIssueCode = "empty_content"
	// MaterialIssueNotVerbatim — the content is not the origin slice at the
	// chunk's coordinates.
	MaterialIssueNotVerbatim MaterialIssueCode = "content_not_verbatim"
)

// MaterialChunk is one chunk of a material as the qualification sees it.
type MaterialChunk struct {
	ID      string `json:"id"`
	StartAt int    `json:"start_at"`
	EndAt   int    `json:"end_at"`
	Content string `json:"content"`
}

// MaterialIssue says what is missing for one chunk.
type MaterialIssue struct {
	ChunkID string            `json:"chunk_id"`
	Code    MaterialIssueCode `json:"code"`
	Detail  string            `json:"detail"`
}

// MaterialChunkVerdict is the per-chunk answer.
type MaterialChunkVerdict struct {
	ChunkID        string `json:"chunk_id"`
	StartAt        int    `json:"start_at"`
	EndAt          int    `json:"end_at"`
	ContentChars   int    `json:"content_chars"`
	CoordinateSpan int    `json:"coordinate_span"`
	Verbatim       bool   `json:"verbatim"`
}

// MaterialOverlapVerdict is the answer for the region two chunks share.
//
// The chunks of one material overlap (the splitter keeps an overlap so a
// sentence that straddles a boundary is not lost). The two chunks must
// carry the SAME text over that region: if they do not, the material says
// two different things about the same characters, and no reader of it can
// tell which one is the original.
type MaterialOverlapVerdict struct {
	FirstChunkID  string `json:"first_chunk_id"`
	SecondChunkID string `json:"second_chunk_id"`
	StartAt       int    `json:"start_at"`
	EndAt         int    `json:"end_at"`
	// Span is the width of the overlap in runes.
	Span int `json:"span"`
	// Identical is true when the two chunks carry the same text over the
	// overlap.
	Identical bool `json:"identical"`
	// FirstDifference is the rune index INSIDE the overlap where the two
	// texts first differ; -1 when they do not differ.
	FirstDifference int `json:"first_difference"`
}

// MaterialQualification is the answer for one material.
type MaterialQualification struct {
	// Qualified is true when the material has chunks and every one of them
	// is the verbatim slice of the origin at its own coordinates.
	Qualified bool `json:"qualified"`
	// OriginRunes is the length of the origin text in runes.
	OriginRunes int `json:"origin_runes"`
	// Chunks holds one verdict per chunk, in the order they were given.
	Chunks []MaterialChunkVerdict `json:"chunks"`
	// Overlaps holds one verdict per pair of chunks that share a region.
	Overlaps []MaterialOverlapVerdict `json:"overlaps"`
	// Issues holds one entry per unqualified chunk.
	Issues []MaterialIssue `json:"issues"`
}

// QualifyMaterial decides whether a material may be used as evidence.
//
// The origin text is the text the material was parsed from; the chunks carry
// the coordinates the source anchors use. A material with no chunks is not
// qualified: there is nothing to quote.
func QualifyMaterial(origin string, chunks []MaterialChunk) MaterialQualification {
	runes := []rune(origin)
	report := MaterialQualification{
		Qualified:   len(chunks) > 0,
		OriginRunes: len(runes),
		Chunks:      make([]MaterialChunkVerdict, 0, len(chunks)),
		Overlaps:    make([]MaterialOverlapVerdict, 0),
		Issues:      make([]MaterialIssue, 0),
	}
	for _, chunk := range chunks {
		verdict := MaterialChunkVerdict{
			ChunkID:        chunk.ID,
			StartAt:        chunk.StartAt,
			EndAt:          chunk.EndAt,
			ContentChars:   utf8.RuneCountInString(chunk.Content),
			CoordinateSpan: chunk.EndAt - chunk.StartAt,
		}
		issue := qualifyOne(runes, chunk)
		verdict.Verbatim = issue == nil
		report.Chunks = append(report.Chunks, verdict)
		if issue != nil {
			report.Qualified = false
			report.Issues = append(report.Issues, *issue)
		}
	}
	report.Overlaps = qualifyOverlaps(chunks)
	return report
}

// qualifyOverlaps answers for every region two chunks share.
//
// The chunks are sorted by their start, and every pair that overlaps is
// compared over the shared region only. A pair that does not overlap is not
// reported: there is nothing to compare.
func qualifyOverlaps(chunks []MaterialChunk) []MaterialOverlapVerdict {
	verdicts := make([]MaterialOverlapVerdict, 0)
	ordered := make([]MaterialChunk, len(chunks))
	copy(ordered, chunks)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartAt != ordered[j].StartAt {
			return ordered[i].StartAt < ordered[j].StartAt
		}
		return ordered[i].EndAt < ordered[j].EndAt
	})
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			start := max(ordered[i].StartAt, ordered[j].StartAt)
			end := min(ordered[i].EndAt, ordered[j].EndAt)
			if end <= start {
				continue
			}
			verdict := MaterialOverlapVerdict{
				FirstChunkID:    ordered[i].ID,
				SecondChunkID:   ordered[j].ID,
				StartAt:         start,
				EndAt:           end,
				Span:            end - start,
				FirstDifference: -1,
			}
			first := overlapText(ordered[i], start, end)
			second := overlapText(ordered[j], start, end)
			verdict.Identical = first == second
			if !verdict.Identical {
				verdict.FirstDifference = firstDifference(first, second)
			}
			verdicts = append(verdicts, verdict)
		}
	}
	return verdicts
}

// overlapText is the text a chunk carries over the region [start,end).
//
// A chunk whose own coordinates do not cover that region has no text to
// compare there; the answer is the empty string, which differs from any
// text — the pair is then reported as a conflict rather than as an
// agreement.
func overlapText(chunk MaterialChunk, start, end int) string {
	if start < chunk.StartAt || end > chunk.EndAt {
		return ""
	}
	runes := []rune(chunk.Content)
	from := start - chunk.StartAt
	to := end - chunk.StartAt
	if from < 0 || to > len(runes) || to < from {
		return ""
	}
	return string(runes[from:to])
}

// qualifyOne answers for a single chunk. It returns nil when the chunk is
// qualified, and otherwise the issue that says what is missing.
//
// The verdict is the one the re-check uses — quoteAt — so "qualified here" and
// "usable there" cannot drift apart. This function only *explains* a negative
// verdict; it never decides one on its own.
func qualifyOne(origin []rune, chunk MaterialChunk) *MaterialIssue {
	span := chunk.EndAt - chunk.StartAt
	if chunk.StartAt < 0 || chunk.EndAt <= chunk.StartAt || chunk.EndAt > len(origin) {
		return &MaterialIssue{
			ChunkID: chunk.ID,
			Code:    MaterialIssueCoordinatesOutOfRange,
			Detail: fmt.Sprintf(
				"坐标越界：start_at=%d end_at=%d 跨度=%d，原文长度=%d 字符（合法区间是 [0,%d]）",
				chunk.StartAt, chunk.EndAt, span, len(origin), len(origin)),
		}
	}
	if chunk.Content == "" {
		return &MaterialIssue{
			ChunkID: chunk.ID,
			Code:    MaterialIssueEmptyContent,
			Detail: fmt.Sprintf(
				"分块内容为空：坐标 [%d,%d) 跨度=%d，但引文没有内容",
				chunk.StartAt, chunk.EndAt, span),
		}
	}

	// The canonical verdict: the same function the source re-check uses.
	status, _ := quoteAt(string(origin), chunk.StartAt, chunk.EndAt, chunk.Content)
	if status == SourceAvailable {
		return nil
	}

	// The content is not the origin slice. Say where it diverges and by how
	// much: "the content is longer than the coordinate span" is the symptom
	// of a chunk that carries something the document does not have there.
	expected := string(origin[chunk.StartAt:chunk.EndAt])
	offset := firstDifference(expected, chunk.Content)
	lengthDelta := utf8.RuneCountInString(chunk.Content) - span
	return &MaterialIssue{
		ChunkID: chunk.ID,
		Code:    MaterialIssueNotVerbatim,
		Detail: fmt.Sprintf(
			"引文与原文不一致：坐标 [%d,%d) 跨度=%d，内容长度=%d（相差 %+d），"+
				"首个不一致位置=%d，原文=%q，引文=%q",
			chunk.StartAt, chunk.EndAt, span, utf8.RuneCountInString(chunk.Content),
			lengthDelta, offset, snippet(expected, offset), snippet(chunk.Content, offset)),
	}
}

// firstDifference returns the rune index at which the two texts first differ,
// or the length of the shorter one when one is a prefix of the other.
func firstDifference(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	limit := len(ar)
	if len(br) < limit {
		limit = len(br)
	}
	for i := 0; i < limit; i++ {
		if ar[i] != br[i] {
			return i
		}
	}
	return limit
}

// snippet renders the text around an offset so a reader can see the
// divergence without opening the whole document.
func snippet(text string, offset int) string {
	runes := []rune(text)
	const window = 24
	start := offset - window/2
	if start < 0 {
		start = 0
	}
	end := offset + window/2
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

// QualificationSummary renders the report as a short human-readable text:
// one line per chunk and one line per issue. It is what the gate prints when
// it refuses a material.
func (q MaterialQualification) QualificationSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "材料合格性：%s（原文 %d 字符，分块 %d 个，问题 %d 条）\n",
		map[bool]string{true: "合格", false: "不合格"}[q.Qualified], q.OriginRunes, len(q.Chunks), len(q.Issues))
	for _, chunk := range q.Chunks {
		fmt.Fprintf(&b, "  chunk %s [%d,%d) 跨度=%d 内容长度=%d 逐字一致=%t\n",
			chunk.ChunkID, chunk.StartAt, chunk.EndAt, chunk.CoordinateSpan, chunk.ContentChars, chunk.Verbatim)
	}
	for _, issue := range q.Issues {
		fmt.Fprintf(&b, "  ✗ chunk %s [%s] %s\n", issue.ChunkID, issue.Code, issue.Detail)
	}
	for _, overlap := range q.Overlaps {
		fmt.Fprintf(&b, "  重叠 chunk %s / %s [%d,%d) 跨度=%d 逐位一致=%t\n",
			overlap.FirstChunkID, overlap.SecondChunkID, overlap.StartAt, overlap.EndAt,
			overlap.Span, overlap.Identical)
	}
	return b.String()
}
