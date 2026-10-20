package delivery

import "testing"

// The source-marker syntax is owned by the sourcemarker package. Before that
// package existed, this scanner accepted any non-empty run between the prefix
// and the closing brackets, so a body such as "[[source:a.b]]" passed here and
// was rejected by the workspace domain.
//
// The narrower character set is the one the regular expressions used; it is now
// the only one, and this test pins the delivery side to it.
func TestCitationIDsRejectsIdentifiersOutsideTheSharedCharacterSet(t *testing.T) {
	if ids := citationIDs("正文 [[source:alpha]]"); len(ids) != 1 || !ids["alpha"] {
		t.Fatalf("citationIDs = %v, want {alpha:true}", ids)
	}
	for _, body := range []string{
		"正文 [[source:a.b]]",
		"正文 [[source: alpha]]",
		"正文 [[source:alpha/beta]]",
		"正文 [[source:中文]]",
		"正文 [[source:]]",
		"正文 [[source:alpha",
	} {
		ids := citationIDs(body)
		if !ids["__invalid__"] {
			t.Fatalf("citationIDs(%q) = %v, want an invalid marker verdict", body, ids)
		}
	}
}
