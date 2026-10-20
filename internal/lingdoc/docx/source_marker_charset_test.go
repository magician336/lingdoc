package docx

import (
	"strings"
	"testing"
)

// The source-marker syntax is owned by the sourcemarker package. Before that
// package existed, the renderer recognised markers with its own regular
// expression while three other packages accepted a wider identifier set.
//
// The narrower character set is the one the regular expressions used; it is now
// the only one, and this test pins the renderer to it.
func TestValidateMarkdownRejectsIdentifiersOutsideTheSharedCharacterSet(t *testing.T) {
	if err := ValidateMarkdown("正文 [[source:alpha]] 的引用"); err != nil {
		t.Fatalf("ValidateMarkdown rejected a well-formed marker: %v", err)
	}
	for _, body := range []string{
		"正文 [[source:a.b]]",
		"正文 [[source: alpha]]",
		"正文 [[source:alpha/beta]]",
		"正文 [[source:中文]]",
		"正文 [[source:]]",
		"正文 [[source:alpha",
	} {
		if err := ValidateMarkdown(body); err == nil {
			t.Fatalf("ValidateMarkdown(%q) accepted an out-of-charset identifier", body)
		} else if !strings.Contains(err.Error(), "marker") && !strings.Contains(err.Error(), "source") {
			t.Fatalf("ValidateMarkdown(%q) = %v, want a marker-shaped rejection", body, err)
		}
	}
}
