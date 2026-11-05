package generation

import "testing"

// The source-marker syntax is owned by the sourcemarker package. Before that
// package existed, this scanner accepted any non-empty trimmed run between the
// prefix and the closing brackets, so a body such as "[[source:a.b]]" passed
// here and was rejected by the workspace domain.
//
// The narrower character set is the one the regular expressions used; it is now
// the only one, and this test pins the generation side to it.
func TestSourceReferencesRejectsIdentifiersOutsideTheSharedCharacterSet(t *testing.T) {
	ids, err := sourceReferences("正文 [[source:alpha]] 与 [[source:beta]]")
	if err != nil {
		t.Fatalf("sourceReferences: %v", err)
	}
	if len(ids) != 2 || ids[0] != "alpha" || ids[1] != "beta" {
		t.Fatalf("sourceReferences = %v, want [alpha beta]", ids)
	}
	for _, body := range []string{
		"正文 [[source:a.b]]",
		"正文 [[source: alpha]]",
		"正文 [[source:alpha/beta]]",
		"正文 [[source:中文]]",
		"正文 [[source:]]",
		"正文 [[source:alpha",
	} {
		if _, err := sourceReferences(body); err == nil {
			t.Fatalf("sourceReferences(%q) accepted an out-of-charset identifier", body)
		}
	}
}
