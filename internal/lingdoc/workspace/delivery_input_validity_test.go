package workspace

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

// The frozen confirmation must carry the validity fact, not just the fields
// that a consumer could re-derive it from.
//
// The delivery side used to judge "is this chapter confirmed" from the
// confirmation's own fields, which is a second copy of a rule the
// confirmation service already owns. Carrying the fact lets one predicate
// serve every consumer, and it is what makes the difference between a
// confirmation that still holds and one that was invalidated when the chapter
// moved on.
func TestFrozenConfirmationCarriesValidity(t *testing.T) {
	for _, item := range []struct {
		name  string
		valid bool
	}{
		{name: "valid", valid: true},
		{name: "invalid", valid: false},
	} {
		frozen := frozenConfirmation(&candidateadoption.Confirmation{
			ID:        "confirmation-1",
			ChapterID: "chapter-1",
			Valid:     item.valid,
		})
		if frozen == nil {
			t.Fatalf("%s: frozen confirmation is nil", item.name)
		}
		if frozen.Valid == nil {
			t.Fatalf("%s: frozen confirmation dropped the validity fact", item.name)
		}
		if *frozen.Valid != item.valid {
			t.Fatalf("%s: frozen validity = %v, want %v", item.name, *frozen.Valid, item.valid)
		}
	}
	if frozenConfirmation(nil) != nil {
		t.Fatal("a chapter without a confirmation must not gain one")
	}
}
