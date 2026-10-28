package workspacecore

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Issue #774 tier A: literal occurrence locations for chapter impact tasks.
//
// An applied change leaves the owner a task that says which chapter needs a
// human pass; this file adds WHERE in that chapter the registered values and
// the change's old values literally occur — paragraph number, UTF-16 offset
// and a context excerpt. The detection is the same literal substring match the
// wording-commit gate (#753) uses, run in the opposite direction: #753 notices
// a value disappearing, this reports where surviving values sit. Paraphrased
// text is the same blind spot as #753 and is intentionally left to a later
// tier.
//
// A hint is an ATTACHMENT, never a verdict: it does not take part in task
// status, severity, stabilization or closure. Consumers must treat the offsets
// as the server-side reading of the chapter body, not as an editor coordinate
// — the client re-finds the value near the suggested offset before jumping.
const (
	// impactLocationMaxMatchesPerValue caps the positions reported for one
	// value; a value repeated through a whole chapter does not need every
	// occurrence listed to be findable.
	impactLocationMaxMatchesPerValue = 5
	// impactLocationMaxHintsPerTask caps the whole attachment so a noisy spec
	// cannot turn one task into an unbounded payload.
	impactLocationMaxHintsPerTask = 50
	// impactLocationContextRunes is how much surrounding text each excerpt
	// carries on either side of the hit.
	impactLocationContextRunes = 40
)

// Where a located value came from. A change's own old value is the more
// specific provenance, so it wins when both sources carry the same string.
const (
	ImpactLocationSourceChangeOldValue  = "change_old_value"
	ImpactLocationSourceRegisteredValue = "registered_spec_value"
)

// ImpactLocationHint is one literal occurrence of a watched value in a
// chapter body. Offsets are UTF-16 code units from the start of the chapter
// body markdown, so a client can compare them with its own string indexing.
type ImpactLocationHint struct {
	Source         string `json:"source"`
	FieldKey       string `json:"field_key"`
	Value          string `json:"value"`
	ParagraphIndex int    `json:"paragraph_index"`
	StartOffset    int    `json:"start_offset"`
	EndOffset      int    `json:"end_offset"`
	Excerpt        string `json:"excerpt"`
}

// impactLocationValue is one watched string and where the watch came from.
type impactLocationValue struct {
	Source   string
	FieldKey string
	Value    string
}

// impactLocationValues collects the strings worth locating for one applied
// change: the old value of every changed field, then every registered spec
// value. Both go through the #753 noise floor (values shorter than
// conditionValueMinRunes match too much prose). The result is deterministic:
// change fields in draft order, spec fields sorted, duplicate strings keep
// only their first (more specific) source.
func impactLocationValues(change ChangeSet, spec map[string]string) []impactLocationValue {
	values := []impactLocationValue{}
	seen := map[string]bool{}
	add := func(source, fieldKey, raw string) {
		value := strings.TrimSpace(raw)
		if utf8.RuneCountInString(value) < conditionValueMinRunes || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, impactLocationValue{Source: source, FieldKey: fieldKey, Value: value})
	}
	for _, field := range change.Fields {
		add(ImpactLocationSourceChangeOldValue, field.Key, field.OldValue)
	}
	fields := make([]string, 0, len(spec))
	for field := range spec {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		add(ImpactLocationSourceRegisteredValue, field, spec[field])
	}
	return values
}

// impactParagraph is one blank-line-separated block of the body, as byte
// offsets. Single line breaks inside a block stay in the same paragraph —
// they are markdown soft wraps, not paragraph boundaries.
type impactParagraph struct {
	start, end int
}

// splitImpactParagraphs cuts the body into paragraphs on blank lines (a
// newline followed by an optional-whitespace line and another newline).
// Trailing whitespace between paragraphs belongs to no paragraph.
func splitImpactParagraphs(body string) []impactParagraph {
	paragraphs := []impactParagraph{}
	start := 0
	isBoundarySpace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }
	for i := 0; i < len(body); {
		if body[i] != '\n' {
			i++
			continue
		}
		j := i + 1
		for j < len(body) && isBoundarySpace(body[j]) {
			j++
		}
		if j >= len(body) || body[j] != '\n' {
			i++
			continue
		}
		if i > start {
			paragraphs = append(paragraphs, impactParagraph{start: start, end: i})
		}
		k := j
		for k < len(body) {
			for k < len(body) && isBoundarySpace(body[k]) {
				k++
			}
			if k < len(body) && body[k] == '\n' {
				k++
				continue
			}
			break
		}
		start = k
		i = k
	}
	if len(body) > start {
		paragraphs = append(paragraphs, impactParagraph{start: start, end: len(body)})
	}
	return paragraphs
}

func paragraphIndexOf(paragraphs []impactParagraph, at int) int {
	for index, paragraph := range paragraphs {
		if paragraph.start <= at && at < paragraph.end {
			return index + 1
		}
	}
	return 1
}

// utf16OffsetOf converts a byte offset (which must sit on a rune boundary)
// into a UTF-16 code unit offset, counting runes above the BMP as two units
// the way JavaScript string indexing does.
func utf16OffsetOf(body string, byteIndex int) int {
	offset := 0
	for _, r := range body[:byteIndex] {
		if r > 0xFFFF {
			offset += 2
			continue
		}
		offset++
	}
	return offset
}

// impactExcerpt slices up to impactLocationContextRunes runes of surrounding
// text on each side of [start,end), always on rune boundaries.
func impactExcerpt(body string, start, end int) string {
	before := start
	for taken := 0; taken < impactLocationContextRunes && before > 0; taken++ {
		_, size := utf8.DecodeLastRuneInString(body[:before])
		before -= size
	}
	after := end
	for taken := 0; taken < impactLocationContextRunes && after < len(body); taken++ {
		_, size := utf8.DecodeRuneInString(body[after:])
		after += size
	}
	return body[before:after]
}

// locateImpactPositions reports where each watched value literally occurs in
// the body, at most impactLocationMaxMatchesPerValue places per value and at
// most impactLocationMaxHintsPerTask hints overall, sorted by offset then
// value then field so two runs over the same inputs agree exactly.
func locateImpactPositions(body string, values []impactLocationValue) []ImpactLocationHint {
	hints := []ImpactLocationHint{}
	if body == "" || len(values) == 0 {
		return hints
	}
	paragraphs := splitImpactParagraphs(body)
	for _, item := range values {
		for found, at := 0, 0; found < impactLocationMaxMatchesPerValue && at < len(body); {
			idx := strings.Index(body[at:], item.Value)
			if idx < 0 {
				break
			}
			start := at + idx
			end := start + len(item.Value)
			hints = append(hints, ImpactLocationHint{
				Source:         item.Source,
				FieldKey:       item.FieldKey,
				Value:          item.Value,
				ParagraphIndex: paragraphIndexOf(paragraphs, start),
				StartOffset:    utf16OffsetOf(body, start),
				EndOffset:      utf16OffsetOf(body, end),
				Excerpt:        impactExcerpt(body, start, end),
			})
			found++
			at = end
		}
		if len(hints) >= impactLocationMaxHintsPerTask {
			break
		}
	}
	if len(hints) > impactLocationMaxHintsPerTask {
		hints = hints[:impactLocationMaxHintsPerTask]
	}
	sort.SliceStable(hints, func(i, j int) bool {
		if hints[i].StartOffset != hints[j].StartOffset {
			return hints[i].StartOffset < hints[j].StartOffset
		}
		if hints[i].Value != hints[j].Value {
			return hints[i].Value < hints[j].Value
		}
		return hints[i].FieldKey < hints[j].FieldKey
	})
	return hints
}
