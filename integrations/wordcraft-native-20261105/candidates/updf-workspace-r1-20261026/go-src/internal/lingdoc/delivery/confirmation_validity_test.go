package delivery

import "testing"

// A frozen confirmation must be able to say whether it still holds.
//
// The delivery side used to re-derive that from the surrounding fields, which
// is a second and weaker copy of the rule the confirmation service already
// owns. Carrying the fact lets one predicate serve every consumer.
//
// The field is a pointer so that "recorded as invalid" and "never recorded"
// stay apart: snapshots frozen before the field existed carry no value, and
// reading those as invalid would silently retire every historical snapshot.
func TestConfirmationValidDistinguishesRecordedFromUnrecorded(t *testing.T) {
	invalid := false
	valid := true
	cases := []struct {
		name  string
		value *bool
		known bool
		holds bool
	}{
		{name: "recorded as valid", value: &valid, known: true, holds: true},
		{name: "recorded as invalid", value: &invalid, known: true, holds: false},
		{name: "never recorded", value: nil, known: false},
	}
	for _, item := range cases {
		confirmation := Confirmation{ID: "c", Valid: item.value}
		if confirmation.Valid == nil {
			if item.known {
				t.Fatalf("%s: validity should be recorded", item.name)
			}
			continue
		}
		if !item.known {
			t.Fatalf("%s: validity should not be recorded", item.name)
		}
		if *confirmation.Valid != item.holds {
			t.Fatalf("%s: validity = %v, want %v", item.name, *confirmation.Valid, item.holds)
		}
	}
}

// A snapshot that never recorded validity must not claim to be valid.
func TestConfirmationWithoutValidityIsNotAssumedValid(t *testing.T) {
	confirmation := Confirmation{ID: "c"}
	if confirmation.Valid != nil {
		t.Fatalf("a confirmation built without validity recorded %v", *confirmation.Valid)
	}
}
