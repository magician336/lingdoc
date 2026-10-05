package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const EvaluatorVersion = "lingdoc-rules/1"

type EvaluationStatus string

const (
	EvaluationPass          EvaluationStatus = "PASS"
	EvaluationIssue         EvaluationStatus = "ISSUE"
	EvaluationUnknown       EvaluationStatus = "UNKNOWN"
	EvaluationNotApplicable EvaluationStatus = "NOT_APPLICABLE"
)

var ErrInvalidDefinition = errors.New("invalid template or rule definition")

type ChapterInput struct {
	ID        string
	Version   string
	Body      string
	Confirmed bool
}

type SourceInput struct {
	ID        string
	Version   string
	Available bool
}

type ReviewItemInput struct {
	ID      string
	Version string
	Decided bool
}

// EvaluationInput is a transport-neutral projection shared by editing checks
// and frozen delivery checks. Only versioned values enter the evaluator.
type EvaluationInput struct {
	ProjectID     string
	TargetVersion string
	Fields        map[string]string
	Chapters      []ChapterInput
	Sources       []SourceInput
	ReviewItems   []ReviewItemInput
}

// Evaluation is an explainable, version-bound result. Severity and hashes are
// copied from the validated server-side definition, never from request input.
type Evaluation struct {
	RuleID           string           `json:"rule_id"`
	TargetRef        string           `json:"target_ref"`
	TargetVersion    string           `json:"target_version"`
	RulesetHash      string           `json:"ruleset_hash"`
	Status           EvaluationStatus `json:"status"`
	Severity         string           `json:"severity"`
	Message          string           `json:"message"`
	Evidence         []string         `json:"evidence"`
	EvaluatorVersion string           `json:"evaluator_version"`
}

var stableID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

// Hashes validates a definition and returns content and ruleset hashes derived
// by the server. Client-supplied hash fields are excluded from the calculation.
func Hashes(t Template) (contentHash, rulesetHash string, err error) {
	if err = Validate(t); err != nil {
		return "", "", err
	}
	rules := append([]Rule(nil), t.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	ruleBytes, err := json.Marshal(rules)
	if err != nil {
		return "", "", fmt.Errorf("marshal rules: %w", err)
	}
	rulesetHash = digest(ruleBytes)
	t.Rules = rules
	t.RequiredFields = append([]string(nil), t.RequiredFields...)
	slices.Sort(t.RequiredFields)
	t.RulesetHash = ""
	t.ContentHash = ""
	contentBytes, err := json.Marshal(t)
	if err != nil {
		return "", "", fmt.Errorf("marshal template: %w", err)
	}
	return digest(contentBytes), rulesetHash, nil
}

// WithHashes returns a validated copy stamped with server-computed hashes.
func WithHashes(t Template) (Template, error) {
	contentHash, rulesetHash, err := Hashes(t)
	if err != nil {
		return Template{}, err
	}
	t.ContentHash, t.RulesetHash = contentHash, rulesetHash
	return t, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Validate rejects unknown executors, parameters, malformed structures, and
// client-authored executable expressions before a definition can be stored.
func Validate(t Template) error {
	if !stableID.MatchString(t.ID) || strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Version) == "" {
		return fmt.Errorf("%w: template identity is incomplete", ErrInvalidDefinition)
	}
	sectionIDs := map[string]bool{}
	for _, section := range t.Sections {
		if !stableID.MatchString(section.ID) || strings.TrimSpace(section.Title) == "" || sectionIDs[section.ID] {
			return fmt.Errorf("%w: invalid or duplicate section %q", ErrInvalidDefinition, section.ID)
		}
		sectionIDs[section.ID] = true
	}
	fieldIDs := map[string]bool{}
	for _, field := range t.Fields {
		if !stableID.MatchString(field.ID) || strings.TrimSpace(field.Label) == "" || fieldIDs[field.ID] || !validFieldType(field.Type) {
			return fmt.Errorf("%w: invalid or duplicate field %q", ErrInvalidDefinition, field.ID)
		}
		fieldIDs[field.ID] = true
		if field.Type == "enum" && len(field.Options) == 0 {
			return fmt.Errorf("%w: enum field %q has no options", ErrInvalidDefinition, field.ID)
		}
	}
	for _, id := range t.RequiredFields {
		if !stableID.MatchString(id) {
			return fmt.Errorf("%w: invalid required field %q", ErrInvalidDefinition, id)
		}
		if len(fieldIDs) > 0 && !fieldIDs[id] {
			return fmt.Errorf("%w: required field %q is not defined", ErrInvalidDefinition, id)
		}
	}
	termIDs := map[string]bool{}
	for _, term := range t.Terms {
		if !stableID.MatchString(term.ID) || strings.TrimSpace(term.Preferred) == "" || termIDs[term.ID] {
			return fmt.Errorf("%w: invalid or duplicate term %q", ErrInvalidDefinition, term.ID)
		}
		termIDs[term.ID] = true
		for _, variant := range term.Variants {
			if strings.TrimSpace(variant) == "" || variant == term.Preferred {
				return fmt.Errorf("%w: invalid variant for term %q", ErrInvalidDefinition, term.ID)
			}
		}
	}
	ruleIDs := map[string]bool{}
	for _, rule := range t.Rules {
		if !stableID.MatchString(rule.ID) || ruleIDs[rule.ID] || !validSeverity(rule.Severity) {
			return fmt.Errorf("%w: invalid or duplicate rule %q", ErrInvalidDefinition, rule.ID)
		}
		ruleIDs[rule.ID] = true
		if err := validateRule(rule); err != nil {
			return err
		}
	}
	return nil
}

func validFieldType(fieldType string) bool {
	switch fieldType {
	case "string", "text", "number", "integer", "boolean", "enum":
		return true
	default:
		return false
	}
}

func validSeverity(severity string) bool {
	return severity == "blocking" || severity == "warning" || severity == "info"
}

func validateRule(rule Rule) error {
	allowed := map[string]bool{}
	switch rule.Kind {
	case "model_assisted":
		allowed = keySet("target_kind", "target_id")
		if !validTextTarget(rule.Parameters) || (rule.Severity != "warning" && rule.Severity != "info") {
			return fmt.Errorf("%w: model-assisted rule %q must be a non-blocking text suggestion", ErrInvalidDefinition, rule.ID)
		}
	case "presence":
		allowed = keySet("target_kind", "target_id")
		if !validTarget(rule.Parameters) {
			return fmt.Errorf("%w: presence rule %q needs a supported target", ErrInvalidDefinition, rule.ID)
		}
	case "pattern":
		allowed = keySet("target_kind", "target_id", "pattern")
		pattern, ok := rule.Parameters["pattern"].(string)
		if !validTarget(rule.Parameters) || !ok || len(pattern) > 512 {
			return fmt.Errorf("%w: pattern rule %q has invalid parameters", ErrInvalidDefinition, rule.ID)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%w: pattern rule %q has invalid regexp", ErrInvalidDefinition, rule.ID)
		}
	case "consistency":
		allowed = keySet("target_kind", "target_id", "preferred", "variants")
		preferred, ok := rule.Parameters["preferred"].(string)
		variants, variantsOK := stringSlice(rule.Parameters["variants"])
		if !validTextTarget(rule.Parameters) || !ok || strings.TrimSpace(preferred) == "" || !variantsOK || len(variants) == 0 {
			return fmt.Errorf("%w: consistency rule %q has invalid parameters", ErrInvalidDefinition, rule.ID)
		}
		for _, variant := range variants {
			if strings.TrimSpace(variant) == "" || variant == preferred {
				return fmt.Errorf("%w: consistency rule %q has invalid variants", ErrInvalidDefinition, rule.ID)
			}
		}
	case "computed":
		allowed = keySet("target_kind", "target_id", "minimum", "maximum")
		switch rule.Evaluator {
		case "required_fields", "nonempty_chapter", "confirmed_chapter", "review_items_decided", "source_available":
			if len(rule.Parameters) != 0 {
				return fmt.Errorf("%w: computed rule %q does not accept parameters", ErrInvalidDefinition, rule.ID)
			}
		case "word_count", "character_count":
			if !validTextTarget(rule.Parameters) || !validCountBounds(rule.Parameters) {
				return fmt.Errorf("%w: computed rule %q has invalid parameters", ErrInvalidDefinition, rule.ID)
			}
		default:
			return fmt.Errorf("%w: unsupported computed evaluator %q", ErrInvalidDefinition, rule.Evaluator)
		}
	default:
		return fmt.Errorf("%w: unsupported rule kind %q", ErrInvalidDefinition, rule.Kind)
	}
	for key := range rule.Parameters {
		if !allowed[key] {
			return fmt.Errorf("%w: unsupported parameter %q in rule %q", ErrInvalidDefinition, key, rule.ID)
		}
	}
	if rule.Kind != "computed" && rule.Evaluator != "" {
		return fmt.Errorf("%w: evaluator is server-selected for %q rules", ErrInvalidDefinition, rule.ID)
	}
	return nil
}

func keySet(keys ...string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

func validTarget(parameters map[string]any) bool {
	kind, kindOK := parameters["target_kind"].(string)
	id, idOK := parameters["target_id"].(string)
	return kindOK && idOK && stableID.MatchString(id) && (kind == "field" || kind == "chapter" || kind == "source" || kind == "review_item")
}

func validTextTarget(parameters map[string]any) bool {
	if !validTarget(parameters) {
		return false
	}
	kind := parameters["target_kind"].(string)
	return kind == "field" || kind == "chapter"
}

func validCountBounds(parameters map[string]any) bool {
	minimum, hasMin := numeric(parameters["minimum"])
	maximum, hasMax := numeric(parameters["maximum"])
	if !hasMin && !hasMax {
		return false
	}
	if (hasMin && (minimum < 0 || minimum > 1000000 || math.Trunc(minimum) != minimum)) || (hasMax && (maximum < 0 || maximum > 1000000 || math.Trunc(maximum) != maximum)) {
		return false
	}
	return !hasMin || !hasMax || minimum <= maximum
}

func numeric(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	default:
		return 0, false
	}
}

func stringSlice(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			value, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, value)
		}
		return result, true
	default:
		return nil, false
	}
}

// Evaluate applies the same allowlisted deterministic evaluator to either a
// live editing projection or a frozen delivery projection.
func Evaluate(t Template, input EvaluationInput) ([]Evaluation, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if input.ProjectID == "" || input.TargetVersion == "" {
		return nil, fmt.Errorf("%w: project and target version are required", ErrInvalidDefinition)
	}
	_, rulesetHash, err := Hashes(t)
	if err != nil {
		return nil, err
	}
	// T02's public fixed demo hash is part of the already-published T13 wire
	// contract. All project-owned copies use the freshly computed hash above.
	if t.ID == DemoTemplateID && t.Version == DemoTemplateVersion && t.RulesetHash == DemoRulesetHash {
		rulesetHash = DemoRulesetHash
	}
	results := make([]Evaluation, 0)
	for _, rule := range t.Rules {
		items, err := evaluateRule(t, rule, input, rulesetHash)
		if err != nil {
			return nil, err
		}
		results = append(results, items...)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].RuleID != results[j].RuleID {
			return results[i].RuleID < results[j].RuleID
		}
		if results[i].TargetRef != results[j].TargetRef {
			return results[i].TargetRef < results[j].TargetRef
		}
		return results[i].TargetVersion < results[j].TargetVersion
	})
	return results, nil
}

func evaluateRule(t Template, rule Rule, input EvaluationInput, rulesetHash string) ([]Evaluation, error) {
	if rule.Kind == "model_assisted" {
		kind, id := rule.Parameters["target_kind"].(string), rule.Parameters["target_id"].(string)
		version := input.TargetVersion
		if kind == "chapter" {
			if chapter, found := findChapter(input.Chapters, id); found && chapter.Version != "" {
				version = chapter.Version
			}
		}
		message := rule.Message
		if message == "" {
			message = "此项仅供人工核查，不代表通过或阻断"
		}
		return []Evaluation{result(rule, kind+"/"+id, version, rulesetHash, EvaluationUnknown, message, []string{})}, nil
	}
	if rule.Kind == "computed" {
		switch rule.Evaluator {
		case "required_fields":
			ids := append([]string(nil), t.RequiredFields...)
			for _, field := range t.Fields {
				if field.Required && !slices.Contains(ids, field.ID) {
					ids = append(ids, field.ID)
				}
			}
			slices.Sort(ids)
			items := make([]Evaluation, 0)
			for _, id := range ids {
				if strings.TrimSpace(input.Fields[id]) == "" {
					items = append(items, result(rule, "field/"+id, input.TargetVersion, rulesetHash, EvaluationIssue, "required field is missing", []string{"field_id=" + id}))
				}
			}
			if len(items) == 0 {
				items = append(items, result(rule, "project/"+input.ProjectID, input.TargetVersion, rulesetHash, EvaluationPass, "required fields are present", []string{"required_count=" + strconv.Itoa(len(ids))}))
			}
			return items, nil
		case "nonempty_chapter", "confirmed_chapter":
			chapters := append([]ChapterInput(nil), input.Chapters...)
			sort.Slice(chapters, func(i, j int) bool { return chapters[i].ID < chapters[j].ID })
			items := make([]Evaluation, 0)
			for _, section := range t.Sections {
				if !section.Required {
					continue
				}
				chapter, found := findChapter(chapters, section.ID)
				status, message := EvaluationPass, "required chapter is ready"
				evidence := []string{"section_id=" + section.ID}
				version := input.TargetVersion
				if found && chapter.Version != "" {
					version = chapter.Version
				}
				if !found || (rule.Evaluator == "nonempty_chapter" && strings.TrimSpace(chapter.Body) == "") || (rule.Evaluator == "confirmed_chapter" && !chapter.Confirmed) {
					status, message = EvaluationIssue, "required chapter is incomplete"
					if rule.Evaluator == "confirmed_chapter" {
						message = "required chapter is not confirmed"
					}
				}
				items = append(items, result(rule, "chapter/"+section.ID, version, rulesetHash, status, message, evidence))
			}
			if len(items) == 0 {
				items = append(items, result(rule, "project/"+input.ProjectID, input.TargetVersion, rulesetHash, EvaluationNotApplicable, "no required chapters", []string{}))
			}
			return items, nil
		case "review_items_decided":
			items := make([]Evaluation, 0)
			for _, review := range input.ReviewItems {
				status, message := EvaluationPass, "review item has a decision"
				if !review.Decided {
					status, message = EvaluationIssue, "review item is undecided"
				}
				version := review.Version
				if version == "" {
					version = input.TargetVersion
				}
				items = append(items, result(rule, "review_item/"+review.ID, version, rulesetHash, status, message, []string{"review_item_id=" + review.ID}))
			}
			if len(items) == 0 {
				items = append(items, result(rule, "project/"+input.ProjectID, input.TargetVersion, rulesetHash, EvaluationPass, "no undecided review items", []string{}))
			}
			return items, nil
		case "source_available":
			items := make([]Evaluation, 0)
			for _, source := range input.Sources {
				status, message := EvaluationPass, "source is available"
				if !source.Available {
					status, message = EvaluationIssue, "source is unavailable or unauthorized"
				}
				version := source.Version
				if version == "" {
					version = input.TargetVersion
				}
				items = append(items, result(rule, "source/"+source.ID, version, rulesetHash, status, message, []string{"source_id=" + source.ID}))
			}
			if len(items) == 0 {
				items = append(items, result(rule, "project/"+input.ProjectID, input.TargetVersion, rulesetHash, EvaluationNotApplicable, "no cited sources", []string{}))
			}
			return items, nil
		case "word_count", "character_count":
			kind := rule.Parameters["target_kind"].(string)
			id := rule.Parameters["target_id"].(string)
			value, version, found := targetText(input, kind, id)
			if !found {
				return []Evaluation{result(rule, kind+"/"+id, input.TargetVersion, rulesetHash, EvaluationUnknown, "target is unavailable", []string{})}, nil
			}
			count := float64(len([]rune(value)))
			if rule.Evaluator == "word_count" {
				count = float64(len(strings.Fields(value)))
			}
			status, message := countStatus(count, rule.Parameters)
			return []Evaluation{result(rule, kind+"/"+id, version, rulesetHash, status, message, []string{"count=" + strconv.Itoa(int(count))})}, nil
		}
	}

	kind := rule.Parameters["target_kind"].(string)
	id := rule.Parameters["target_id"].(string)
	targetRef := kind + "/" + id
	version := input.TargetVersion
	var status EvaluationStatus
	var message string
	var evidence []string
	switch rule.Kind {
	case "presence":
		present, targetVersion, found := targetPresent(input, kind, id)
		if targetVersion != "" {
			version = targetVersion
		}
		if !found || !present {
			status, message = EvaluationIssue, "required target is absent"
		} else {
			status, message = EvaluationPass, "target is present"
		}
		evidence = []string{"target=" + targetRef}
	case "pattern":
		value, targetVersion, found := targetText(input, kind, id)
		pattern := rule.Parameters["pattern"].(string)
		if targetVersion != "" {
			version = targetVersion
		}
		if !found {
			status, message = EvaluationUnknown, "target is unavailable"
		} else if regexp.MustCompile(pattern).MatchString(value) {
			status, message = EvaluationPass, "target matches the declared pattern"
		} else {
			status, message = EvaluationIssue, "target does not match the declared pattern"
		}
		evidence = []string{"target=" + targetRef, "pattern=" + pattern}
	case "consistency":
		value, targetVersion, found := targetText(input, kind, id)
		if targetVersion != "" {
			version = targetVersion
		}
		if !found {
			status, message = EvaluationUnknown, "target is unavailable"
			evidence = []string{"target=" + targetRef}
		} else {
			status, message, evidence = termConsistency(value, rule.Parameters, targetRef)
		}
	}
	return []Evaluation{result(rule, targetRef, version, rulesetHash, status, message, evidence)}, nil
}

func result(rule Rule, target, version, ruleset string, status EvaluationStatus, message string, evidence []string) Evaluation {
	if rule.Message != "" {
		message = rule.Message
	}
	return Evaluation{RuleID: rule.ID, TargetRef: target, TargetVersion: version, RulesetHash: ruleset, Status: status, Severity: rule.Severity, Message: message, Evidence: append([]string(nil), evidence...), EvaluatorVersion: EvaluatorVersion}
}

func findChapter(chapters []ChapterInput, id string) (ChapterInput, bool) {
	for _, chapter := range chapters {
		if chapter.ID == id {
			return chapter, true
		}
	}
	return ChapterInput{}, false
}

func targetText(input EvaluationInput, kind, id string) (string, string, bool) {
	switch kind {
	case "field":
		value, ok := input.Fields[id]
		return value, input.TargetVersion, ok
	case "chapter":
		chapter, ok := findChapter(input.Chapters, id)
		return chapter.Body, firstVersion(chapter.Version, input.TargetVersion), ok
	default:
		return "", input.TargetVersion, false
	}
}

func targetPresent(input EvaluationInput, kind, id string) (bool, string, bool) {
	switch kind {
	case "field":
		value, ok := input.Fields[id]
		return ok && strings.TrimSpace(value) != "", input.TargetVersion, true
	case "chapter":
		chapter, ok := findChapter(input.Chapters, id)
		return ok, firstVersion(chapter.Version, input.TargetVersion), true
	case "source":
		for _, source := range input.Sources {
			if source.ID == id {
				return source.Available, firstVersion(source.Version, input.TargetVersion), true
			}
		}
		return false, input.TargetVersion, true
	case "review_item":
		for _, item := range input.ReviewItems {
			if item.ID == id {
				return item.Decided, firstVersion(item.Version, input.TargetVersion), true
			}
		}
		return false, input.TargetVersion, true
	default:
		return false, input.TargetVersion, false
	}
}

func firstVersion(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func countStatus(count float64, parameters map[string]any) (EvaluationStatus, string) {
	if minimum, ok := numeric(parameters["minimum"]); ok && count < minimum {
		return EvaluationIssue, "target is below the minimum count"
	}
	if maximum, ok := numeric(parameters["maximum"]); ok && count > maximum {
		return EvaluationIssue, "target exceeds the maximum count"
	}
	return EvaluationPass, "target count is within the declared range"
}

func termConsistency(value string, parameters map[string]any, target string) (EvaluationStatus, string, []string) {
	preferred := parameters["preferred"].(string)
	variants, _ := stringSlice(parameters["variants"])
	used := make([]string, 0)
	for _, variant := range variants {
		if strings.Contains(value, variant) {
			used = append(used, variant)
		}
	}
	sort.Strings(used)
	if len(used) > 0 {
		return EvaluationIssue, "non-preferred controlled term is present", []string{"target=" + target, "preferred=" + preferred, "variants=" + strings.Join(used, ",")}
	}
	return EvaluationPass, "controlled term usage is consistent", []string{"target=" + target, "preferred=" + preferred}
}

// String returns a stable human-readable identity for logs and test evidence.
func (e Evaluation) String() string {
	return fmt.Sprintf("%s %s %s %s", e.RuleID, e.TargetRef, e.TargetVersion, e.Status)
}
