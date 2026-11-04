// Package catalog provides a deterministic, versioned allowlist for academic
// source metadata. Crossref is a metadata provider only: a Crossref record is
// not a venue-quality signal, and this package ships no pre-approved venues.
package catalog

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// MetadataProviderCrossref identifies Crossref as a bibliographic metadata
// provider. It must not be interpreted as an approval or quality decision.
const MetadataProviderCrossref = "crossref"

type Status string

const (
	StatusApproved    Status = "approved"
	StatusOutOfPolicy Status = "out_of_policy"
	StatusRetracted   Status = "retracted"
)

type LicenseStatus string

const (
	LicenseNotChecked  LicenseStatus = "not_checked"
	LicenseNotRequired LicenseStatus = "not_required"
	LicenseAllowed     LicenseStatus = "allowed"
	LicenseMissing     LicenseStatus = "missing"
	LicenseNotAllowed  LicenseStatus = "not_allowed"
)

// Rule approves a venue for a source type. VenueIDs should use stable,
// normalized identifiers such as ISSNs or conference keys. A non-empty
// AllowedLicenseURLs list is restrictive: a work must report a compatible
// license URL to pass that rule.
type Rule struct {
	ID                 string    `json:"id"`
	SourceType         string    `json:"source_type"`
	VenueName          string    `json:"venue_name,omitempty"`
	VenueIDs           []string  `json:"venue_ids,omitempty"`
	AllowedLicenseURLs []string  `json:"allowed_license_urls,omitempty"`
	Maintainer         string    `json:"maintainer"`
	EffectiveAt        time.Time `json:"effective_at"`
	Version            string    `json:"version"`
}

// Snapshot is an immutable view of the global catalog plus any project-level
// extension rules. Combine computes Version from the global version and the
// canonical rule contents.
type Snapshot struct {
	Version string `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Work contains the metadata used for an allowlist decision. DOI is retained
// for traceability; it does not approve a work by itself. SourceType is
// optional because some providers do not reliably classify venue types.
type Work struct {
	DOI         string   `json:"doi,omitempty"`
	SourceType  string   `json:"source_type,omitempty"`
	Journal     string   `json:"journal,omitempty"`
	ISSNs       []string `json:"issns,omitempty"`
	VenueIDs    []string `json:"venue_ids,omitempty"`
	LicenseURLs []string `json:"license_urls,omitempty"`
	Retracted   bool     `json:"retracted"`
}

// Evaluation is a fail-closed catalog decision. RuleID is the deterministic
// rule used to explain the decision; it is empty when no active rule matched.
type Evaluation struct {
	Status        Status        `json:"status"`
	RuleID        string        `json:"rule_id,omitempty"`
	LicenseStatus LicenseStatus `json:"license_status"`
}

// Combine creates a stable snapshot from the global catalog and project
// extensions. Duplicate identical rules are collapsed; conflicting rule IDs
// are retained and resolved deterministically by Evaluate. An empty global
// catalog stays empty: Crossref metadata does not create an implicit venue
// whitelist.
func Combine(global Snapshot, projectRules []Rule) Snapshot {
	rules := make([]Rule, 0, len(global.Rules)+len(projectRules))
	for _, rule := range global.Rules {
		rules = append(rules, canonicalRule(rule))
	}
	for _, rule := range projectRules {
		rules = append(rules, canonicalRule(rule))
	}

	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ID != rules[j].ID {
			return rules[i].ID < rules[j].ID
		}
		return canonicalRuleKey(rules[i]) < canonicalRuleKey(rules[j])
	})

	unique := rules[:0]
	lastKey := ""
	for _, rule := range rules {
		key := canonicalRuleKey(rule)
		if len(unique) > 0 && key == lastKey {
			continue
		}
		unique = append(unique, rule)
		lastKey = key
	}
	rules = unique

	identity := struct {
		GlobalVersion string `json:"global_version"`
		Rules         []Rule `json:"rules"`
	}{
		GlobalVersion: strings.TrimSpace(global.Version),
		Rules:         rules,
	}
	return Snapshot{
		Version: "sha256:" + operationcore.ValueHash(identity),
		Rules:   rules,
	}
}

// Evaluate checks for an active exact venue rule and then enforces any license
// restrictions carried by matching rules. It uses the current UTC time for
// EffectiveAt; callers that need a reproducible historical decision should use
// EvaluateAt with a fixed time.
func Evaluate(snapshot Snapshot, work Work) Evaluation {
	return EvaluateAt(snapshot, work, time.Now().UTC())
}

// EvaluateAt is Evaluate with an explicit evaluation time. It is useful for
// replaying decisions and for deterministic tests.
func EvaluateAt(snapshot Snapshot, work Work, at time.Time) Evaluation {
	retracted := work.Retracted
	work.Retracted = false
	decision := evaluateAt(snapshot, work, at)
	if retracted {
		decision.Status = StatusRetracted
	}
	return decision
}

func evaluateAt(snapshot Snapshot, work Work, at time.Time) Evaluation {
	if strings.TrimSpace(snapshot.Version) == "" {
		return Evaluation{Status: StatusOutOfPolicy, LicenseStatus: LicenseNotChecked}
	}

	type candidate struct {
		rule      Rule
		idMatched bool
	}
	matches := make([]candidate, 0, 1)
	for _, rule := range snapshot.Rules {
		if !validRule(rule) || rule.EffectiveAt.After(at) || !sourceTypeMatches(rule.SourceType, work.SourceType) {
			continue
		}
		idMatched, matched := venueMatches(rule, work)
		if matched {
			matches = append(matches, candidate{rule: rule, idMatched: idMatched})
		}
	}
	if len(matches) == 0 {
		return Evaluation{Status: StatusOutOfPolicy, LicenseStatus: LicenseNotChecked}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].idMatched != matches[j].idMatched {
			return matches[i].idMatched
		}
		if !matches[i].rule.EffectiveAt.Equal(matches[j].rule.EffectiveAt) {
			return matches[i].rule.EffectiveAt.After(matches[j].rule.EffectiveAt)
		}
		if matches[i].rule.ID != matches[j].rule.ID {
			return matches[i].rule.ID < matches[j].rule.ID
		}
		return matches[i].rule.Version < matches[j].rule.Version
	})

	decisionRule := matches[0]
	constrained := false
	allowedLicenses := make(map[string]struct{})
	for _, match := range matches {
		for _, licenseURL := range match.rule.AllowedLicenseURLs {
			if strings.TrimSpace(licenseURL) == "" {
				continue
			}
			constrained = true
			if isAbsoluteHTTPURL(licenseURL) {
				allowedLicenses[NormalizeLicenseURL(licenseURL)] = struct{}{}
			}
		}
	}

	licenseStatus := LicenseNotRequired
	if constrained {
		licenseStatus = LicenseNotAllowed
		workLicenses := make([]string, 0, len(work.LicenseURLs))
		for _, licenseURL := range work.LicenseURLs {
			if isAbsoluteHTTPURL(licenseURL) {
				workLicenses = append(workLicenses, NormalizeLicenseURL(licenseURL))
			}
		}
		if len(workLicenses) == 0 {
			if len(nonEmpty(work.LicenseURLs)) == 0 {
				licenseStatus = LicenseMissing
			}
		} else {
			for _, licenseURL := range workLicenses {
				if _, ok := allowedLicenses[licenseURL]; ok {
					licenseStatus = LicenseAllowed
					break
				}
			}
		}
		if licenseStatus == LicenseAllowed {
			for _, match := range matches {
				if ruleAllowsAnyWorkLicense(match.rule, work.LicenseURLs) {
					decisionRule = match
					break
				}
			}
		} else {
			for _, match := range matches {
				if len(nonEmpty(match.rule.AllowedLicenseURLs)) > 0 {
					decisionRule = match
					break
				}
			}
		}
	}

	if constrained && licenseStatus != LicenseAllowed {
		return Evaluation{Status: StatusOutOfPolicy, RuleID: decisionRule.rule.ID, LicenseStatus: licenseStatus}
	}
	return Evaluation{Status: StatusApproved, RuleID: decisionRule.rule.ID, LicenseStatus: licenseStatus}
}

// NormalizeVenueID returns a stable key for ISSNs and conference identifiers.
// ISSNs accept either "1234-5678", "12345678", or an "issn:" prefix; all
// normalize to "issn:12345678". Other keys are NFKC-normalized, case-folded,
// trimmed, and have internal whitespace collapsed.
func NormalizeVenueID(value string) string {
	value = fold(value)
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "issn:") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "issn:"))
	}
	compactISSN := strings.NewReplacer("-", "", " ", "").Replace(value)
	if isISSNKey(compactISSN) {
		return "issn:" + compactISSN
	}
	return strings.Join(strings.Fields(value), " ")
}

// NormalizeVenueName provides exact, case-insensitive matching while folding
// Unicode compatibility forms and collapsing whitespace. It intentionally
// does not use fuzzy or substring matching.
func NormalizeVenueName(value string) string {
	return strings.Join(strings.Fields(fold(value)), " ")
}

// NormalizeLicenseURL canonicalizes common license URL differences. HTTP and
// HTTPS forms of the Creative Commons license URIs are treated as equivalent;
// other schemes and paths remain distinct. Invalid URLs are returned as
// normalized text but never qualify as compatible licenses.
func NormalizeLicenseURL(value string) string {
	value = strings.TrimSpace(norm.NFKC.String(value))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(fold(value), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Host == "www.creativecommons.org" || parsed.Host == "creativecommons.org" {
		parsed.Host = "creativecommons.org"
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			parsed.Scheme = "https"
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/")
}

func canonicalRule(rule Rule) Rule {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.SourceType = NormalizeVenueName(rule.SourceType)
	rule.VenueName = strings.TrimSpace(norm.NFKC.String(rule.VenueName))
	rule.Maintainer = strings.TrimSpace(rule.Maintainer)
	rule.Version = strings.TrimSpace(rule.Version)
	if !rule.EffectiveAt.IsZero() {
		rule.EffectiveAt = rule.EffectiveAt.UTC()
	}
	rule.VenueIDs = normalizeUnique(rule.VenueIDs, NormalizeVenueID)
	rule.AllowedLicenseURLs = normalizeUnique(rule.AllowedLicenseURLs, NormalizeLicenseURL)
	return rule
}

func canonicalRuleKey(rule Rule) string {
	encoded, _ := json.Marshal(rule)
	return string(encoded)
}

func validRule(rule Rule) bool {
	return strings.TrimSpace(rule.ID) != "" && strings.TrimSpace(rule.SourceType) != "" &&
		strings.TrimSpace(rule.Maintainer) != "" && strings.TrimSpace(rule.Version) != "" &&
		!rule.EffectiveAt.IsZero() && (NormalizeVenueName(rule.VenueName) != "" || len(nonEmpty(rule.VenueIDs)) > 0)
}

func sourceTypeMatches(ruleType, workType string) bool {
	workType = canonicalSourceType(workType)
	return workType == "" || canonicalSourceType(ruleType) == workType
}

func canonicalSourceType(value string) string {
	value = NormalizeVenueName(value)
	// Crossref classifies conference proceedings papers as proceedings-article,
	// while project owners may naturally label the same venue as a conference.
	if value == "conference" || value == "proceedings" {
		return "conference"
	}
	return value
}

func venueMatches(rule Rule, work Work) (idMatched bool, matched bool) {
	ruleIDs := normalizeUnique(rule.VenueIDs, NormalizeVenueID)
	workIDs := normalizeUnique(append(append([]string(nil), work.ISSNs...), work.VenueIDs...), NormalizeVenueID)
	if len(ruleIDs) > 0 && len(workIDs) > 0 {
		if intersects(ruleIDs, workIDs) {
			return true, true
		}
		// A known but conflicting identifier must not fall back to a matching
		// display name, which could otherwise approve a different journal.
		return false, false
	}
	name := NormalizeVenueName(work.Journal)
	if name != "" && name == NormalizeVenueName(rule.VenueName) {
		return false, true
	}
	return false, false
}

func ruleAllowsAnyWorkLicense(rule Rule, workURLs []string) bool {
	allowed := make(map[string]struct{}, len(rule.AllowedLicenseURLs))
	for _, value := range rule.AllowedLicenseURLs {
		if isAbsoluteHTTPURL(value) {
			allowed[NormalizeLicenseURL(value)] = struct{}{}
		}
	}
	for _, value := range workURLs {
		if !isAbsoluteHTTPURL(value) {
			continue
		}
		if _, ok := allowed[NormalizeLicenseURL(value)]; ok {
			return true
		}
	}
	return false
}

func isAbsoluteHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed != nil && parsed.Host != "" && parsed.User == nil &&
		(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}

func normalizeUnique(values []string, normalize func(string) string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := normalize(value); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func nonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func intersects(left, right []string) bool {
	leftSet := make(map[string]struct{}, len(left))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := leftSet[value]; ok {
			return true
		}
	}
	return false
}

func isISSNKey(value string) bool {
	if len(value) != 8 {
		return false
	}
	for index, character := range value {
		if character >= '0' && character <= '9' {
			continue
		}
		if index == 7 && character == 'x' {
			continue
		}
		return false
	}
	return true
}

func fold(value string) string {
	return cases.Fold().String(norm.NFKC.String(value))
}
