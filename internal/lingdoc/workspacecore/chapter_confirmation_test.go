package workspacecore

import (
	"testing"
	"time"
)

// The confirmation predicate owns the question "is this chapter version
// currently confirmed". Every consumer of it must therefore answer the same
// way for the same chapter, so the conditions are pinned here, one case each.
//
// A confirmation record is only a confirmation when it is complete: it names
// the chapter it was made for, the version it was made for, the
// research-conditions revision and the template version it was made against,
// and it says who confirmed and when. A record missing any of those is not a
// confirmation — DR-47-01 is explicit that the chapter id must be present and
// must equal the target chapter.

func completeConfirmationFacts() ChapterConfirmationFact {
	return ChapterConfirmationFact{
		ID:               "confirmation-1",
		ChapterID:        "chapter-1",
		ChapterVersionID: "chapter-version-1",
		SpecRevision:     3,
		TemplateVersion:  "0.2.0",
		ConfirmerID:      "member-1",
		ConfirmedAt:      time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Valid:            true,
	}
}

func completeConfirmationBasis() ChapterConfirmationBasis {
	return ChapterConfirmationBasis{
		ChapterID:        "chapter-1",
		ChapterVersionID: "chapter-version-1",
		SpecRevision:     3,
		TemplateVersion:  "0.2.0",
	}
}

func TestChapterConfirmedAcceptsACompleteMatchingRecord(t *testing.T) {
	if !ChapterConfirmed(completeConfirmationBasis(), []ChapterConfirmationFact{completeConfirmationFacts()}) {
		t.Fatal("a complete record for the current state is not confirmed")
	}
}

// One case per condition: exactly that condition fails, so the test says which
// condition decides the answer.
func TestChapterConfirmedRejectsEachIncompleteOrStaleRecord(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(fact *ChapterConfirmationFact)
	}{
		{"the record is not valid", func(f *ChapterConfirmationFact) { f.Valid = false }},
		{"the record has no id", func(f *ChapterConfirmationFact) { f.ID = "" }},
		{"the record names no chapter", func(f *ChapterConfirmationFact) { f.ChapterID = "" }},
		{"the record names another chapter", func(f *ChapterConfirmationFact) { f.ChapterID = "chapter-2" }},
		{"the record names no chapter version", func(f *ChapterConfirmationFact) { f.ChapterVersionID = "" }},
		{"the record names another chapter version", func(f *ChapterConfirmationFact) { f.ChapterVersionID = "chapter-version-2" }},
		{"the record was made against another spec revision", func(f *ChapterConfirmationFact) { f.SpecRevision = 2 }},
		{"the record was made against another template version", func(f *ChapterConfirmationFact) { f.TemplateVersion = "0.1.0" }},
		{"the record names no confirmer", func(f *ChapterConfirmationFact) { f.ConfirmerID = "" }},
		{"the record has no confirmation time", func(f *ChapterConfirmationFact) { f.ConfirmedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fact := completeConfirmationFacts()
			tc.mutate(&fact)
			if ChapterConfirmed(completeConfirmationBasis(), []ChapterConfirmationFact{fact}) {
				t.Fatal("the chapter was reported confirmed although " + tc.name)
			}
		})
	}
}

// A basis without a chapter or a chapter version cannot be judged at all.
func TestChapterConfirmedRejectsAnEmptyBasis(t *testing.T) {
	facts := []ChapterConfirmationFact{completeConfirmationFacts()}
	basis := completeConfirmationBasis()
	basis.ChapterID = ""
	if ChapterConfirmed(basis, facts) {
		t.Fatal("a basis without a chapter was reported confirmed")
	}
	basis = completeConfirmationBasis()
	basis.ChapterVersionID = ""
	if ChapterConfirmed(basis, facts) {
		t.Fatal("a basis without a chapter version was reported confirmed")
	}
}

// "The chapter has no confirmation record" and "the chapter has a record that
// does not match" are different situations, and the delivery side has to be
// able to tell them apart.
func TestChapterConfirmedDistinguishesNoRecordFromAMismatchedOne(t *testing.T) {
	if ChapterConfirmed(completeConfirmationBasis(), nil) {
		t.Fatal("a chapter with no confirmation record was reported confirmed")
	}
	fact := completeConfirmationFacts()
	fact.SpecRevision = 1
	if ChapterConfirmed(completeConfirmationBasis(), []ChapterConfirmationFact{fact}) {
		t.Fatal("a chapter with a stale record was reported confirmed")
	}
}

// A stale record does not hide a current one: the basis is what decides, and a
// chapter may carry several records.
func TestChapterConfirmedAcceptsACurrentRecordBesideAStaleOne(t *testing.T) {
	stale := completeConfirmationFacts()
	stale.ID = "confirmation-old"
	stale.SpecRevision = 1
	current := completeConfirmationFacts()
	if !ChapterConfirmed(completeConfirmationBasis(), []ChapterConfirmationFact{stale, current}) {
		t.Fatal("a current record was hidden by a stale one")
	}
}

// The variant that returns the record itself agrees with the boolean one.
func TestChapterConfirmationOfAgreesWithChapterConfirmed(t *testing.T) {
	facts := []ChapterConfirmationFact{completeConfirmationFacts()}
	match, ok := ChapterConfirmationOf(completeConfirmationBasis(), facts)
	if !ok {
		t.Fatal("the matching record was not found")
	}
	if match.ID != "confirmation-1" {
		t.Fatalf("matched record = %q", match.ID)
	}
	if !ChapterConfirmed(completeConfirmationBasis(), facts) {
		t.Fatal("the two answers disagree")
	}
	stale := completeConfirmationFacts()
	stale.ChapterID = "chapter-2"
	if _, ok := ChapterConfirmationOf(completeConfirmationBasis(), []ChapterConfirmationFact{stale}); ok {
		t.Fatal("a record for another chapter was matched")
	}
}
