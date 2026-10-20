package sourcemarker

import (
	"errors"
	"strings"
	"testing"
)

func TestIDsKeepsOrderAndDuplicates(t *testing.T) {
	body := "结论 [[source:alpha]] 与 [[source:beta]] 再次引用 [[source:alpha]]"
	ids := IDs(body)
	want := []string{"alpha", "beta", "alpha"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func TestIDsOnBodyWithoutMarkers(t *testing.T) {
	if ids := IDs("没有任何引用"); len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
	if ids := IDs(""); len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestValidateAcceptsWellFormedBodies(t *testing.T) {
	for _, body := range []string{
		"",
		"没有引用的正文",
		"一个 [[source:alpha]]",
		"两个 [[source:alpha]] 和 [[source:beta]]",
		"重复 [[source:alpha]] 重复 [[source:alpha]]",
	} {
		if err := Validate(body); err != nil {
			t.Fatalf("Validate(%q) = %v, want nil", body, err)
		}
	}
}

func TestValidateRejectsPrefixWithoutMarker(t *testing.T) {
	// A body that opens a marker but never closes it claims a citation the
	// parser cannot resolve.
	for _, body := range []string{
		"[[source:alpha",
		"[[source:]]",
		"[[source:alpha]] 与 [[source:beta",
	} {
		if err := Validate(body); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Validate(%q) = %v, want ErrMalformed", body, err)
		}
	}
}

func TestIdentifierCharacterSet(t *testing.T) {
	accepted := []string{"a", "Z", "0", "_", "-", "a-b_c", "source-1", "A_1-b"}
	for _, id := range accepted {
		body := "[[source:" + id + "]]"
		if err := Validate(body); err != nil {
			t.Fatalf("Validate(%q) = %v, want nil", body, err)
		}
		if got := IDs(body); len(got) != 1 || got[0] != id {
			t.Fatalf("IDs(%q) = %v, want [%s]", body, got, id)
		}
	}

	// The character set is the one the original regular expressions used.
	// The hand-rolled scanners used to accept a wider set, so these bodies
	// are rejected now that one definition owns the syntax.
	rejected := []string{"a.b", "a b", "a/b", "中文", "a:b", ""}
	for _, id := range rejected {
		body := "[[source:" + id + "]]"
		if err := Validate(body); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Validate(%q) = %v, want ErrMalformed", body, err)
		}
	}
}

func TestStripRemovesEveryMarker(t *testing.T) {
	body := "前 [[source:alpha]] 中 [[source:beta]] 后"
	if got := Strip(body); got != "前  中  后" {
		t.Fatalf("Strip = %q", got)
	}
}

func TestReplaceAllRewritesTokens(t *testing.T) {
	body := "前 [[source:alpha]] 中 [[source:beta]] 后"
	var seen []string
	got := ReplaceAll(body, func(token, id string) string {
		seen = append(seen, token+"/"+id)
		return "[" + id + "]"
	})
	if got != "前 [alpha] 中 [beta] 后" {
		t.Fatalf("ReplaceAll = %q", got)
	}
	if len(seen) != 2 || seen[0] != "[[source:alpha]]/alpha" || seen[1] != "[[source:beta]]/beta" {
		t.Fatalf("callback saw %v", seen)
	}
}

func TestReplaceAllLeavesNonMarkersAlone(t *testing.T) {
	body := "没有引用 [普通链接](x) 也没有"
	got := ReplaceAll(body, func(token, id string) string { return "REPLACED" })
	if got != body {
		t.Fatalf("ReplaceAll = %q, want unchanged", got)
	}
}

func TestTokenID(t *testing.T) {
	id, ok := TokenID("[[source:alpha]]")
	if !ok || id != "alpha" {
		t.Fatalf("TokenID = %q, %v", id, ok)
	}
	if _, ok := TokenID("[[source:a.b]]"); ok {
		t.Fatal("TokenID accepted an out-of-charset id")
	}
	if _, ok := TokenID("plain text"); ok {
		t.Fatal("TokenID accepted a non-marker")
	}
}

func TestCountInvariantIsTheOnlyWayToSeePrefixes(t *testing.T) {
	// The package deliberately does not export a counter: a caller that could
	// count prefixes itself could re-implement the invariant and drift.
	body := "[[source:alpha]] 与 [[source:beta"
	if err := Validate(body); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Validate = %v, want ErrMalformed", err)
	}
	if !strings.Contains(body, prefix) {
		t.Fatal("fixture lost its prefix")
	}
}
