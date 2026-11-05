// Package reviewanchor binds review metadata to exact, versioned Markdown ranges.
package reviewanchor

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type Anchor struct {
	StartUTF16          int     `json:"start_utf16"`
	EndUTF16            int     `json:"end_utf16"`
	QuoteMarkdown       string  `json:"quote_markdown"`
	WorkingCopyRevision int64   `json:"working_copy_revision"`
	ChapterVersionID    *string `json:"chapter_version_id"`
	Status              string  `json:"status"`
}

type Selection struct {
	StartUTF16    int    `json:"start_utf16"`
	EndUTF16      int    `json:"end_utf16"`
	QuoteMarkdown string `json:"quote_markdown"`
}

// ByteRange refuses offsets that bisect a surrogate pair. Browsers use UTF-16,
// Go slices use byte offsets; conflating them corrupts Chinese/emoji selections.
func ByteRange(body string, start, end int) (int, int, bool) {
	if !utf8.ValidString(body) || start < 0 || end <= start {
		return 0, 0, false
	}
	units, left, right := 0, -1, -1
	for pos, r := range body {
		if units == start {
			left = pos
		}
		if units == end {
			right = pos
		}
		units += utf16.RuneLen(r)
	}
	if units == end {
		right = len(body)
	}
	return left, right, left >= 0 && right > left
}

func Valid(body string, selection Selection) bool {
	start, end, ok := ByteRange(body, selection.StartUTF16, selection.EndUTF16)
	return ok && strings.TrimSpace(selection.QuoteMarkdown) != "" && body[start:end] == selection.QuoteMarkdown
}

func New(selection Selection, revision int64, version *string) *Anchor {
	return &Anchor{StartUTF16: selection.StartUTF16, EndUTF16: selection.EndUTF16, QuoteMarkdown: selection.QuoteMarkdown, WorkingCopyRevision: revision, ChapterVersionID: cloneVersion(version), Status: "located"}
}

func cloneVersion(version *string) *string {
	if version == nil {
		return nil
	}
	v := *version
	return &v
}

// Rebase never guesses a paragraph index. After an edit only one exact quote
// occurrence is sufficient; stale anchors stay stale until explicitly attached.
func Rebase(oldBody, body string, old *Anchor, revision int64, version *string) *Anchor {
	if old == nil {
		return nil
	}
	next := *old
	next.WorkingCopyRevision = revision
	next.ChapterVersionID = cloneVersion(version)
	if old.Status != "located" || !Valid(oldBody, Selection{old.StartUTF16, old.EndUTF16, old.QuoteMarkdown}) {
		next.Status = "stale"
		return &next
	}
	if body == oldBody {
		return &next
	}
	at := strings.Index(body, old.QuoteMarkdown)
	if at < 0 || strings.Contains(body[at+1:], old.QuoteMarkdown) {
		next.Status = "stale"
		return &next
	}
	next.StartUTF16 = len(utf16.Encode([]rune(body[:at])))
	next.EndUTF16 = next.StartUTF16 + len(utf16.Encode([]rune(old.QuoteMarkdown)))
	return &next
}
