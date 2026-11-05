package reviewanchor

import "testing"

func TestExactUnicodeAndAmbiguousRanges(t *testing.T) {
	body := "甲😀引用\n后文"
	for _, s := range []Selection{{1, 5, "😀引用"}, {3, 5, "引用"}} {
		if !Valid(body, s) {
			t.Fatalf("valid range rejected: %+v", s)
		}
	}
	for _, s := range []Selection{{2, 5, "引用"}, {1, 2, "😀"}, {3, 6, "引用"}, {-1, 1, "甲"}} {
		if Valid(body, s) {
			t.Fatalf("unsafe range accepted: %+v", s)
		}
	}
	original := New(Selection{3, 5, "引用"}, 2, nil)
	moved := Rebase(body, "前文\n"+body, original, 3, nil)
	if moved.Status != "located" || moved.StartUTF16 != 6 || original.StartUTF16 != 3 {
		t.Fatalf("move or immutable original: %+v %+v", moved, original)
	}
	for _, next := range []string{"替换正文", "引用。引用"} {
		got := Rebase(body, next, original, 4, nil)
		if got.Status != "stale" {
			t.Fatalf("unsafe rebase: %+v", got)
		}
	}
	stale := *original
	stale.Status = "stale"
	if Rebase(body, body, &stale, 5, nil).Status != "stale" {
		t.Fatal("silent stale resurrection")
	}
	if Rebase(body, body, nil, 5, nil) != nil {
		t.Fatal("legacy item gained invented anchor")
	}
}
