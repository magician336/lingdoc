package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestCombineHasStableVersionAndCanonicalRules(t *testing.T) {
	effectiveAt := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	first := Rule{
		ID:                 "journal-b",
		SourceType:         " JOURNAL ",
		VenueName:          " Example Journal ",
		VenueIDs:           []string{"2222-2222", "1111-1111", "11111111"},
		AllowedLicenseURLs: []string{"https://license.example/by/4.0/", "https://license.example/by/4.0"},
		Maintainer:         " Research Office ",
		EffectiveAt:        effectiveAt,
		Version:            " 2 ",
	}
	second := Rule{
		ID:          "conference-a",
		SourceType:  "conference",
		VenueName:   "Example Conference",
		VenueIDs:    []string{"conf:Example-2026"},
		Maintainer:  "Research Office",
		EffectiveAt: effectiveAt.UTC(),
		Version:     "1",
	}

	global := Snapshot{Version: "global-v1", Rules: []Rule{first}}
	combined := Combine(global, []Rule{second})
	reordered := Combine(Snapshot{Version: "global-v1", Rules: []Rule{first}}, []Rule{second})
	if combined.Version != reordered.Version {
		t.Fatalf("same inputs must produce same version: %q != %q", combined.Version, reordered.Version)
	}
	if !strings.HasPrefix(combined.Version, "sha256:") {
		t.Fatalf("version must identify its SHA256 encoding, got %q", combined.Version)
	}
	if got, want := len(combined.Rules), 2; got != want {
		t.Fatalf("rule count = %d, want %d", got, want)
	}
	if combined.Rules[0].ID != "conference-a" || combined.Rules[1].ID != "journal-b" {
		t.Fatalf("rules are not sorted by stable ID: %#v", combined.Rules)
	}
	if got := combined.Rules[1].VenueIDs; len(got) != 2 || got[0] != "issn:11111111" || got[1] != "issn:22222222" {
		t.Fatalf("venue IDs were not normalized and deduplicated: %#v", got)
	}
	if got := combined.Rules[1].AllowedLicenseURLs; len(got) != 1 {
		t.Fatalf("equivalent license URLs were not canonicalized: %#v", got)
	}

	changed := first
	changed.Version = "3"
	versionChanged := Combine(Snapshot{Version: "global-v1", Rules: []Rule{changed}}, []Rule{second})
	if combined.Version == versionChanged.Version {
		t.Fatal("changing a rule version must change the snapshot version")
	}
	globalVersionChanged := Combine(Snapshot{Version: "global-v2", Rules: []Rule{first}}, []Rule{second})
	if combined.Version == globalVersionChanged.Version {
		t.Fatal("changing the global catalog version must change the combined version")
	}
}

func TestEvaluateNormalizesISSN(t *testing.T) {
	snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{testRule("journal-1", "Journal of Tests", []string{"0378-5955"}, nil)})
	decision := EvaluateAt(snapshot, Work{
		DOI:        "10.1000/example",
		SourceType: "journal",
		Journal:    " JOURNAL OF TESTS ",
		ISSNs:      []string{"03785955"},
	}, testNow())
	if decision.Status != StatusApproved || decision.RuleID != "journal-1" || decision.LicenseStatus != LicenseNotRequired {
		t.Fatalf("unexpected evaluation: %#v", decision)
	}

	wrongISSN := Work{SourceType: "journal", Journal: "Journal of Tests", ISSNs: []string{"1111-1111"}}
	decision = EvaluateAt(snapshot, wrongISSN, testNow())
	if decision.Status != StatusOutOfPolicy {
		t.Fatalf("conflicting ISSN must not fall back to journal name: %#v", decision)
	}
}

func TestEvaluateLicenseRequiresCompatibleReportedURL(t *testing.T) {
	snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{testRule(
		"licensed-journal",
		"Licensed Journal",
		[]string{"1234-5678"},
		[]string{"https://creativecommons.org/licenses/by/4.0/"},
	)})
	base := Work{SourceType: "journal", Journal: "Licensed Journal", ISSNs: []string{"12345678"}}

	missing := EvaluateAt(snapshot, base, testNow())
	if missing.Status != StatusOutOfPolicy || missing.LicenseStatus != LicenseMissing {
		t.Fatalf("missing license must fail closed: %#v", missing)
	}

	incompatible := base
	incompatible.LicenseURLs = []string{"https://creativecommons.org/licenses/by-nc/4.0/"}
	denied := EvaluateAt(snapshot, incompatible, testNow())
	if denied.Status != StatusOutOfPolicy || denied.LicenseStatus != LicenseNotAllowed {
		t.Fatalf("incompatible license must be rejected: %#v", denied)
	}

	compatible := base
	compatible.LicenseURLs = []string{"http://creativecommons.org/licenses/by/4.0"}
	approved := EvaluateAt(snapshot, compatible, testNow())
	if approved.Status != StatusApproved || approved.LicenseStatus != LicenseAllowed || approved.RuleID != "licensed-journal" {
		t.Fatalf("compatible license should match its rule: %#v", approved)
	}
}

func TestEvaluateRetractionAlwaysWins(t *testing.T) {
	decision := EvaluateAt(Snapshot{}, Work{
		Journal:   "Not Listed",
		Retracted: true,
	}, testNow())
	if decision.Status != StatusRetracted {
		t.Fatalf("explicit retraction must always be rejected: %#v", decision)
	}

	snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{testRule("listed", "Listed Journal", []string{"1234-5678"}, nil)})
	decision = EvaluateAt(snapshot, Work{
		SourceType: "journal",
		Journal:    "Listed Journal",
		ISSNs:      []string{"12345678"},
		Retracted:  true,
	}, testNow())
	if decision.Status != StatusRetracted || decision.RuleID != "listed" {
		t.Fatalf("a retracted matching work must keep its matched rule for audit: %#v", decision)
	}
}

func TestEvaluateNoMatchingRuleFailsClosed(t *testing.T) {
	snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{testRule("allowed", "Allowed Journal", []string{"2222-2222"}, nil)})
	decision := EvaluateAt(snapshot, Work{SourceType: "journal", Journal: "Unlisted Journal", ISSNs: []string{"1111-1111"}}, testNow())
	if decision.Status != StatusOutOfPolicy || decision.RuleID != "" || decision.LicenseStatus != LicenseNotChecked {
		t.Fatalf("unmatched work must be out of policy: %#v", decision)
	}

	emptyCatalog := Combine(Snapshot{}, nil)
	decision = EvaluateAt(emptyCatalog, Work{Journal: "Any Journal"}, testNow())
	if decision.Status != StatusOutOfPolicy {
		t.Fatalf("Crossref metadata without a venue rule must not auto-approve: %#v", decision)
	}
}

func TestEvaluateFutureRuleAndSourceTypeMismatchFailClosed(t *testing.T) {
	future := testRule("future", "Future Journal", []string{"1234-5678"}, nil)
	future.EffectiveAt = testNow().Add(time.Hour)
	snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{future})
	work := Work{SourceType: "journal", Journal: "Future Journal", ISSNs: []string{"12345678"}}
	if got := EvaluateAt(snapshot, work, testNow()); got.Status != StatusOutOfPolicy {
		t.Fatalf("future rule must not be active: %#v", got)
	}

	active := future
	active.EffectiveAt = testNow().Add(-time.Hour)
	snapshot = Combine(Snapshot{Version: "global-v1"}, []Rule{active})
	work.SourceType = "conference"
	if got := EvaluateAt(snapshot, work, testNow()); got.Status != StatusOutOfPolicy {
		t.Fatalf("mismatched source type must not match: %#v", got)
	}
}

func TestConferenceAndProceedingsRulesAreCompatible(t *testing.T) {
	for _, test := range []struct {
		name     string
		ruleType string
		workType string
	}{
		{name: "conference rule accepts Crossref proceedings article", ruleType: "conference", workType: "proceedings"},
		{name: "proceedings rule accepts conference work", ruleType: "proceedings", workType: "conference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule := testRule("venue", "Example Conference", []string{"conference:example"}, nil)
			rule.SourceType = test.ruleType
			snapshot := Combine(Snapshot{Version: "global-v1"}, []Rule{rule})
			decision := EvaluateAt(snapshot, Work{
				SourceType: test.workType,
				Journal:    "Example Conference",
				VenueIDs:   []string{"conference:example"},
			}, testNow())
			if decision.Status != StatusApproved || decision.RuleID != "venue" {
				t.Fatalf("compatible conference/proceedings types should match: %#v", decision)
			}
		})
	}
}

func testRule(id, venueName string, venueIDs, licenses []string) Rule {
	return Rule{
		ID:                 id,
		SourceType:         "journal",
		VenueName:          venueName,
		VenueIDs:           venueIDs,
		AllowedLicenseURLs: licenses,
		Maintainer:         "test-maintainer",
		EffectiveAt:        testNow().Add(-time.Hour),
		Version:            "1",
	}
}

func testNow() time.Time {
	return time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
}
