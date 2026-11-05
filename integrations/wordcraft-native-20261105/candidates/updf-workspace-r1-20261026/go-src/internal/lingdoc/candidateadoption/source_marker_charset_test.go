package candidateadoption

import "testing"

// The source-marker syntax is owned by the sourcemarker package. Before that
// package existed, this validator accepted any non-empty trimmed run between
// the prefix and the closing brackets, so a body such as "[[source:a.b]]"
// passed here and was rejected by the workspace domain.
//
// The narrower character set is the one the regular expressions used; it is now
// the only one, and this test pins the candidate-adoption side to it.
func TestValidateSourceReferencesRejectsIdentifiersOutsideTheSharedCharacterSet(t *testing.T) {
	if err := validateSourceReferences("正文 [[source:alpha]]", []string{"alpha"}); err != nil {
		t.Fatalf("validateSourceReferences: %v", err)
	}
	for _, body := range []string{
		"正文 [[source:a.b]]",
		"正文 [[source: alpha]]",
		"正文 [[source:alpha/beta]]",
		"正文 [[source:中文]]",
		"正文 [[source:]]",
		"正文 [[source:alpha",
	} {
		if err := validateSourceReferences(body, []string{"alpha"}); err == nil {
			t.Fatalf("validateSourceReferences(%q) accepted an out-of-charset identifier", body)
		}
	}
}
