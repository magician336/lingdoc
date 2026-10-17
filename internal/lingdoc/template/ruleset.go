package template

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// RuleSetState describes whether a ruleset can be edited or bound to a new
// template version. Published content is immutable; withdrawn and recalled
// versions remain available for historical evaluation.
type RuleSetState string

const (
	RuleSetDraft     RuleSetState = "draft"
	RuleSetPublished RuleSetState = "published"
	RuleSetWithdrawn RuleSetState = "withdrawn"
	RuleSetRecalled  RuleSetState = "recalled"
)

// RuleSet lifecycle errors distinguish invalid content from invalid state transitions.
var (
	ErrInvalidRuleSet      = errors.New("invalid rule set")
	ErrRuleSetNotDraft     = errors.New("rule set is not a draft")
	ErrRuleSetNotPublished = errors.New("rule set is not published")
)

// RuleDefinition is a server-owned evaluator contract. Definitions are
// exposed as copies by RuleDefinitions; callers may bind them but cannot add
// evaluators or change their safety limits.
type RuleDefinition struct {
	ID                 string   `json:"definition_id"`
	Version            string   `json:"definition_version"`
	Kind               string   `json:"kind"`
	Evaluator          string   `json:"evaluator,omitempty"`
	AllowedTargetKinds []string `json:"allowed_target_kinds,omitempty"`
}

// RuleBinding configures one server-defined rule for a target. Parameters are
// declarative JSON values only; executable expressions and evaluator names
// are never accepted from a binding.
type RuleBinding struct {
	RuleID              string         `json:"rule_id"`
	DefinitionID        string         `json:"definition_id"`
	DefinitionVersion   string         `json:"definition_version"`
	Origin              string         `json:"origin"`
	TargetKind          string         `json:"target_kind,omitempty"`
	TargetID            string         `json:"target_id,omitempty"`
	Parameters          map[string]any `json:"parameters,omitempty"`
	Severity            string         `json:"severity"`
	Message             string         `json:"message,omitempty"`
	TriggerScenes       []string       `json:"trigger_scenes"`
	PositiveExampleRefs []string       `json:"positive_example_refs,omitempty"`
	NegativeExampleRefs []string       `json:"negative_example_refs,omitempty"`
	Enabled             bool           `json:"enabled"`
	Owner               string         `json:"owner"`
}

// RuleSet is a versioned collection of bindings. RulesetHash is always
// computed by this package and covers the published binding snapshot. The
// application repository must keep published versions append-only.
type RuleSet struct {
	ID          string        `json:"ruleset_id"`
	Version     string        `json:"version"`
	State       RuleSetState  `json:"state"`
	Bindings    []RuleBinding `json:"bindings"`
	RulesetHash string        `json:"ruleset_hash,omitempty"`
}

// RuleSetReference is the exact immutable version a TemplateVersion uses.
// Hash is the RuleSet's source hash, not the combined project-copy hash.
type RuleSetReference struct {
	ID      string `json:"ruleset_id"`
	Version string `json:"version"`
	Hash    string `json:"ruleset_hash"`
}

// RuleSetReferenceFor returns the explicit immutable reference carried by a
// template, or derives it from a verified snapshot for legacy callers.
func RuleSetReferenceFor(t Template) *RuleSetReference {
	if t.RuleSetRef != nil {
		ref := *t.RuleSetRef
		return &ref
	}
	if t.RuleSetSnapshot != nil {
		return &RuleSetReference{ID: t.RuleSetSnapshot.ID, Version: t.RuleSetSnapshot.Version, Hash: t.RuleSetSnapshot.RulesetHash}
	}
	return nil
}

// RuleBindingOverride contains only project-scoped controls. Executor,
// definition identity, target identity, severity and trigger scenes remain
// server-owned by the published binding.
type RuleBindingOverride struct {
	RuleID     string         `json:"rule_id"`
	Enabled    *bool          `json:"enabled,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

func CloneRuleBindingOverrides(overrides []RuleBindingOverride) []RuleBindingOverride {
	if overrides == nil {
		return nil
	}
	cloned := make([]RuleBindingOverride, len(overrides))
	for i, override := range overrides {
		cloned[i] = override
		if override.Enabled != nil {
			enabled := *override.Enabled
			cloned[i].Enabled = &enabled
		}
		cloned[i].Parameters = cloneParameters(override.Parameters)
	}
	return cloned
}

// ValidateRuleBindingOverrides ensures every change names an existing binding
// and changes only parameter keys explicitly supported by its safe definition.
func ValidateRuleBindingOverrides(set RuleSet, overrides []RuleBindingOverride) error {
	bindings := make(map[string]RuleBinding, len(set.Bindings))
	for _, binding := range set.Bindings {
		bindings[binding.RuleID] = binding
	}
	seen := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		if !stableID.MatchString(override.RuleID) || seen[override.RuleID] {
			return fmt.Errorf("%w: invalid or duplicate RuleSet override %q", ErrInvalidRuleSet, override.RuleID)
		}
		seen[override.RuleID] = true
		binding, ok := bindings[override.RuleID]
		if !ok {
			return fmt.Errorf("%w: override names unknown rule %q", ErrInvalidRuleSet, override.RuleID)
		}
		if override.Enabled == nil && len(override.Parameters) == 0 {
			return fmt.Errorf("%w: empty override for rule %q", ErrInvalidRuleSet, override.RuleID)
		}
		allowed := allowedOverrideParameters(binding.DefinitionID)
		parameters := cloneParameters(binding.Parameters)
		if parameters == nil {
			parameters = map[string]any{}
		}
		for key, value := range override.Parameters {
			if !allowed[key] {
				return fmt.Errorf("%w: parameter %q cannot be overridden for rule %q", ErrInvalidRuleSet, key, override.RuleID)
			}
			parameters[key] = cloneParameterValue(value)
		}
		if len(override.Parameters) > 0 {
			if err := validateOverrideParameters(binding, parameters); err != nil {
				return fmt.Errorf("%w: invalid override for rule %q: %v", ErrInvalidRuleSet, override.RuleID, err)
			}
		}
	}
	return nil
}

func allowedOverrideParameters(definitionID string) map[string]bool {
	switch definitionID {
	case "pattern":
		return keySet("pattern")
	case "consistency":
		return keySet("preferred", "variants")
	case "computed/word_count", "computed/character_count":
		return keySet("minimum", "maximum")
	default:
		return map[string]bool{}
	}
}

func validateOverrideParameters(binding RuleBinding, parameters map[string]any) error {
	definition, ok := ruleDefinition(binding.DefinitionID)
	if !ok {
		return ErrInvalidRuleSet
	}
	if len(definition.AllowedTargetKinds) > 0 {
		parameters["target_kind"] = binding.TargetKind
		parameters["target_id"] = binding.TargetID
	}
	rule := Rule{ID: binding.RuleID, Kind: definition.Kind, Evaluator: definition.Evaluator, Severity: binding.Severity, Message: binding.Message, Parameters: parameters}
	return validateRule(rule)
}

var builtInRuleDefinitions = []RuleDefinition{
	{ID: "presence", Version: "1", Kind: "presence", AllowedTargetKinds: []string{"field", "chapter", "source", "review_item"}},
	{ID: "pattern", Version: "1", Kind: "pattern", AllowedTargetKinds: []string{"field", "chapter"}},
	{ID: "consistency", Version: "1", Kind: "consistency", AllowedTargetKinds: []string{"field", "chapter"}},
	{ID: "model_assisted", Version: "1", Kind: "model_assisted", AllowedTargetKinds: []string{"field", "chapter"}},
	{ID: "computed/required_fields", Version: "1", Kind: "computed", Evaluator: "required_fields"},
	{ID: "computed/nonempty_chapter", Version: "1", Kind: "computed", Evaluator: "nonempty_chapter"},
	{ID: "computed/confirmed_chapter", Version: "1", Kind: "computed", Evaluator: "confirmed_chapter"},
	{ID: "computed/review_items_decided", Version: "1", Kind: "computed", Evaluator: "review_items_decided"},
	{ID: "computed/source_available", Version: "1", Kind: "computed", Evaluator: "source_available"},
	{ID: "computed/word_count", Version: "1", Kind: "computed", Evaluator: "word_count", AllowedTargetKinds: []string{"field", "chapter"}},
	{ID: "computed/character_count", Version: "1", Kind: "computed", Evaluator: "character_count", AllowedTargetKinds: []string{"field", "chapter"}},
}

var allowedRuleScenes = map[string]bool{
	"write_check": true,
	"preflight":   true,
}

// RuleDefinitions returns a defensive copy of the server-owned rule catalog.
func RuleDefinitions() []RuleDefinition {
	definitions := make([]RuleDefinition, len(builtInRuleDefinitions))
	for i, definition := range builtInRuleDefinitions {
		definitions[i] = definition
		definitions[i].AllowedTargetKinds = append([]string(nil), definition.AllowedTargetKinds...)
	}
	return definitions
}

// HashRuleSet validates a draft and returns a deterministic hash over its
// identity, definition versions, and bindings. Client-supplied RulesetHash is
// intentionally excluded. Binding order and set-like references do not
// affect the result.
func HashRuleSet(set RuleSet) (string, error) {
	if err := validateRuleSetContent(set); err != nil {
		return "", err
	}
	bindings := cloneRuleBindings(set.Bindings)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].RuleID < bindings[j].RuleID })
	for i := range bindings {
		slices.Sort(bindings[i].TriggerScenes)
		slices.Sort(bindings[i].PositiveExampleRefs)
		slices.Sort(bindings[i].NegativeExampleRefs)
	}
	canonical := struct {
		ID       string        `json:"ruleset_id"`
		Version  string        `json:"version"`
		Bindings []RuleBinding `json:"bindings"`
	}{ID: set.ID, Version: set.Version, Bindings: bindings}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal ruleset: %w", err)
	}
	return digest(raw), nil
}

// ValidateRuleSet checks the binding contracts and verifies the hash of any
// non-draft version. A draft with a cached hash must also match that hash.
func ValidateRuleSet(set RuleSet) error {
	if err := validateRuleSetContent(set); err != nil {
		return err
	}
	if set.RulesetHash == "" {
		if set.State == RuleSetDraft {
			return nil
		}
		return fmt.Errorf("%w: non-draft ruleset has no server hash", ErrInvalidRuleSet)
	}
	hash, err := HashRuleSet(set)
	if err != nil {
		return err
	}
	if set.RulesetHash != hash {
		return fmt.Errorf("%w: ruleset hash does not match its bindings", ErrInvalidRuleSet)
	}
	return nil
}

// PublishRuleSet freezes a valid draft as a new published value. It always
// replaces a caller-supplied hash with the server-computed value.
func PublishRuleSet(set RuleSet) (RuleSet, error) {
	if set.State != RuleSetDraft {
		return RuleSet{}, ErrRuleSetNotDraft
	}
	if err := validateRuleSetContent(set); err != nil {
		return RuleSet{}, err
	}
	enabled := false
	for _, binding := range set.Bindings {
		enabled = enabled || binding.Enabled
	}
	if !enabled {
		return RuleSet{}, fmt.Errorf("%w: cannot publish a ruleset without an enabled binding", ErrInvalidRuleSet)
	}
	hash, err := HashRuleSet(set)
	if err != nil {
		return RuleSet{}, err
	}
	published := cloneRuleSet(set)
	published.State = RuleSetPublished
	published.RulesetHash = hash
	return published, nil
}

// WithdrawRuleSet prevents a published version from being bound to new
// templates while preserving it for historical reads and replay.
func WithdrawRuleSet(set RuleSet) (RuleSet, error) {
	return transitionRuleSet(set, RuleSetWithdrawn)
}

// RecallRuleSet records a stronger safety stop without deleting the version
// or changing its historical hash.
func RecallRuleSet(set RuleSet) (RuleSet, error) {
	return transitionRuleSet(set, RuleSetRecalled)
}

func transitionRuleSet(set RuleSet, state RuleSetState) (RuleSet, error) {
	if err := ValidateRuleSet(set); err != nil {
		return RuleSet{}, err
	}
	if set.State != RuleSetPublished {
		return RuleSet{}, ErrRuleSetNotPublished
	}
	result := cloneRuleSet(set)
	result.State = state
	return result, nil
}

// CanBindRuleSet is the gate for new template or project bindings. Historical
// withdrawn/recalled versions remain valid values but cannot be newly bound.
func CanBindRuleSet(set RuleSet) error {
	if err := ValidateRuleSet(set); err != nil {
		return err
	}
	if set.State != RuleSetPublished {
		return ErrRuleSetNotPublished
	}
	return nil
}

// CompileRuleSet adapts enabled bindings into the existing Rule contract so
// Template Check and Preflight continue to use the same allowlisted evaluator.
// Drafts may be compiled for preview; persisted historical states may be
// compiled for replay after their immutable hash is verified.
func CompileRuleSet(set RuleSet) ([]Rule, error) {
	if err := ValidateRuleSet(set); err != nil {
		return nil, err
	}
	bindings := cloneRuleBindings(set.Bindings)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].RuleID < bindings[j].RuleID })
	rules := make([]Rule, 0, len(bindings))
	for _, binding := range bindings {
		if !binding.Enabled {
			continue
		}
		definition, ok := ruleDefinition(binding.DefinitionID)
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule definition %q", ErrInvalidRuleSet, binding.DefinitionID)
		}
		parameters := cloneParameters(binding.Parameters)
		if parameters == nil {
			parameters = map[string]any{}
		}
		if len(definition.AllowedTargetKinds) > 0 {
			parameters["target_kind"] = binding.TargetKind
			parameters["target_id"] = binding.TargetID
		}
		rule := Rule{ID: binding.RuleID, Kind: definition.Kind, Evaluator: definition.Evaluator, Severity: binding.Severity, Message: binding.Message, Parameters: parameters}
		if err := validateRule(rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// EvaluateRuleSet adapts a RuleSet to the existing template evaluator while
// honoring the binding's trigger scene and preserving the RuleSet version
// hash in every evaluation and stable issue identity. Drafts can be previewed
// with their computed content hash; published historical versions can be
// replayed after their stored hash is verified.
func EvaluateRuleSet(t Template, set RuleSet, scene string, input EvaluationInput) ([]Evaluation, error) {
	if err := ValidateRuleSet(set); err != nil {
		return nil, err
	}
	if !allowedRuleScenes[scene] {
		return nil, fmt.Errorf("%w: unsupported rule scene %q", ErrInvalidRuleSet, scene)
	}
	rules, err := CompileRuleSet(set)
	if err != nil {
		return nil, err
	}

	activeRuleIDs := make(map[string]bool, len(set.Bindings))
	for _, binding := range set.Bindings {
		if binding.Enabled && slices.Contains(binding.TriggerScenes, scene) {
			activeRuleIDs[binding.RuleID] = true
		}
	}
	filteredRules := make([]Rule, 0, len(activeRuleIDs))
	for _, rule := range rules {
		if activeRuleIDs[rule.ID] {
			filteredRules = append(filteredRules, rule)
		}
	}

	rulesetHash := set.RulesetHash
	if rulesetHash == "" {
		rulesetHash, err = HashRuleSet(set)
		if err != nil {
			return nil, err
		}
	}
	definition := Clone(t)
	definition.Rules = filteredRules
	return evaluateTemplate(definition, input, rulesetHash)
}

// EvaluateBoundRuleSet evaluates the legacy template rules together with the
// scene-matched bindings from the exact RuleSet snapshot embedded in t. The
// combined hash changes when either the legacy rules or the bound RuleSet
// identity/version/hash changes.
func EvaluateBoundRuleSet(t Template, set RuleSet, scene string, input EvaluationInput) ([]Evaluation, error) {
	if t.RuleSetSnapshot == nil || t.RuleSetSnapshot.ID != set.ID || t.RuleSetSnapshot.Version != set.Version || t.RuleSetSnapshot.RulesetHash != set.RulesetHash {
		return nil, fmt.Errorf("%w: evaluation RuleSet does not match the template snapshot", ErrInvalidRuleSet)
	}
	if err := CanBindRuleSet(set); err != nil {
		return nil, err
	}
	if !allowedRuleScenes[scene] {
		return nil, fmt.Errorf("%w: unsupported rule scene %q", ErrInvalidRuleSet, scene)
	}
	_, effectiveHash, err := Hashes(t)
	if err != nil {
		return nil, err
	}
	if err := ValidateRuleBindingOverrides(set, t.RuleBindingOverrides); err != nil {
		return nil, err
	}
	overrides := make(map[string]RuleBindingOverride, len(t.RuleBindingOverrides))
	for _, override := range t.RuleBindingOverrides {
		overrides[override.RuleID] = override
	}
	effectiveSet := cloneRuleSet(set)
	for i := range effectiveSet.Bindings {
		if override, ok := overrides[effectiveSet.Bindings[i].RuleID]; ok && override.Enabled != nil {
			effectiveSet.Bindings[i].Enabled = *override.Enabled
		}
	}
	// This is an ephemeral evaluator input, not a persisted RuleSet version.
	// Applying project-level enable overrides changes the copied bindings, so
	// it must not retain the immutable published hash during compilation.
	effectiveSet.State = RuleSetDraft
	effectiveSet.RulesetHash = ""
	rules, err := CompileRuleSet(effectiveSet)
	if err != nil {
		return nil, err
	}
	activeRuleIDs := make(map[string]bool, len(set.Bindings))
	for _, binding := range set.Bindings {
		enabled := binding.Enabled
		if override, ok := overrides[binding.RuleID]; ok && override.Enabled != nil {
			enabled = *override.Enabled
		}
		if enabled && slices.Contains(binding.TriggerScenes, scene) {
			activeRuleIDs[binding.RuleID] = true
		}
	}
	definition := Clone(t)
	definition.RuleSetSnapshot = nil      // The compiled bindings below replace the snapshot for this evaluation only.
	definition.RuleBindingOverrides = nil // Overrides were applied to the compiled bindings above.
	for _, rule := range rules {
		if activeRuleIDs[rule.ID] {
			if override, ok := overrides[rule.ID]; ok {
				for key, value := range override.Parameters {
					rule.Parameters[key] = cloneParameterValue(value)
				}
				if err := validateRule(rule); err != nil {
					return nil, fmt.Errorf("%w: invalid RuleSet override for %q: %v", ErrInvalidRuleSet, rule.ID, err)
				}
			}
			definition.Rules = append(definition.Rules, rule)
		}
	}
	evaluations, err := evaluateTemplate(definition, input, effectiveHash)
	if err == nil {
		for i := range evaluations {
			evaluations[i].RuleSetID = set.ID
			evaluations[i].RuleSetVersion = set.Version
		}
	}
	return evaluations, err
}

func validateRuleSetContent(set RuleSet) error {
	if !stableID.MatchString(set.ID) || !stableID.MatchString(set.Version) || len(set.ID) > 64 || len(set.Version) > 64 {
		return fmt.Errorf("%w: ruleset id and version must be stable identifiers of at most 64 characters", ErrInvalidRuleSet)
	}
	switch set.State {
	case RuleSetDraft, RuleSetPublished, RuleSetWithdrawn, RuleSetRecalled:
	default:
		return fmt.Errorf("%w: unsupported state %q", ErrInvalidRuleSet, set.State)
	}
	seen := make(map[string]bool, len(set.Bindings))
	for _, binding := range set.Bindings {
		if err := validateRuleBinding(binding); err != nil {
			return err
		}
		if seen[binding.RuleID] {
			return fmt.Errorf("%w: duplicate rule id %q", ErrInvalidRuleSet, binding.RuleID)
		}
		seen[binding.RuleID] = true
	}
	return nil
}

func validateRuleBinding(binding RuleBinding) error {
	if !stableID.MatchString(binding.RuleID) || strings.TrimSpace(binding.Owner) == "" {
		return fmt.Errorf("%w: rule id and owner are required", ErrInvalidRuleSet)
	}
	if binding.Origin != "builtin" && binding.Origin != "organization" && binding.Origin != "project" {
		return fmt.Errorf("%w: unsupported rule origin %q", ErrInvalidRuleSet, binding.Origin)
	}
	definition, ok := ruleDefinition(binding.DefinitionID)
	if !ok || definition.Version != binding.DefinitionVersion {
		return fmt.Errorf("%w: unknown rule definition or version %q@%q", ErrInvalidRuleSet, binding.DefinitionID, binding.DefinitionVersion)
	}
	if len(binding.TriggerScenes) == 0 || !validUniqueValues(binding.TriggerScenes, allowedRuleScenes) {
		return fmt.Errorf("%w: rule %q needs supported trigger scenes", ErrInvalidRuleSet, binding.RuleID)
	}
	if !validStableRefs(binding.PositiveExampleRefs) || !validStableRefs(binding.NegativeExampleRefs) {
		return fmt.Errorf("%w: rule %q has invalid example references", ErrInvalidRuleSet, binding.RuleID)
	}
	needsTarget := len(definition.AllowedTargetKinds) > 0
	if needsTarget {
		if !slices.Contains(definition.AllowedTargetKinds, binding.TargetKind) || !stableID.MatchString(binding.TargetID) {
			return fmt.Errorf("%w: rule %q has an unsupported target", ErrInvalidRuleSet, binding.RuleID)
		}
	} else if binding.TargetKind != "" || binding.TargetID != "" {
		return fmt.Errorf("%w: global rule %q cannot declare a target", ErrInvalidRuleSet, binding.RuleID)
	}
	for _, key := range []string{"target_kind", "target_id", "evaluator", "script", "expression", "code"} {
		if _, ok := binding.Parameters[key]; ok {
			return fmt.Errorf("%w: rule %q cannot set server-owned parameter %q", ErrInvalidRuleSet, binding.RuleID, key)
		}
	}
	parameters := cloneParameters(binding.Parameters)
	if parameters == nil {
		parameters = map[string]any{}
	}
	if needsTarget {
		parameters["target_kind"] = binding.TargetKind
		parameters["target_id"] = binding.TargetID
	}
	rule := Rule{ID: binding.RuleID, Kind: definition.Kind, Evaluator: definition.Evaluator, Severity: binding.Severity, Message: binding.Message, Parameters: parameters}
	if err := validateRule(rule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRuleSet, err)
	}
	return nil
}

func ruleDefinition(id string) (RuleDefinition, bool) {
	for _, definition := range builtInRuleDefinitions {
		if definition.ID == id {
			copy := definition
			copy.AllowedTargetKinds = append([]string(nil), definition.AllowedTargetKinds...)
			return copy, true
		}
	}
	return RuleDefinition{}, false
}

func validUniqueValues(values []string, allowed map[string]bool) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !allowed[value] || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validStableRefs(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !stableID.MatchString(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func cloneRuleSet(set RuleSet) RuleSet {
	copy := set
	copy.Bindings = cloneRuleBindings(set.Bindings)
	return copy
}

func cloneRuleBindings(bindings []RuleBinding) []RuleBinding {
	copy := make([]RuleBinding, len(bindings))
	for i, binding := range bindings {
		copy[i] = binding
		copy[i].Parameters = cloneParameters(binding.Parameters)
		copy[i].TriggerScenes = append([]string(nil), binding.TriggerScenes...)
		copy[i].PositiveExampleRefs = append([]string(nil), binding.PositiveExampleRefs...)
		copy[i].NegativeExampleRefs = append([]string(nil), binding.NegativeExampleRefs...)
	}
	return copy
}
