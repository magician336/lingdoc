package templategovernance

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

type FieldMigrationFact struct {
	FieldID  string `json:"field_id"`
	Status   string `json:"status"` // existing workspace preview: preserved, orphaned, incompatible, added, missing
	Required bool   `json:"required"`
	Resolved bool   `json:"resolved"`
	Mapping  string `json:"mapping,omitempty"`
}
type ChapterFact struct {
	ID                string
	SectionID         string
	VersionID         string
	ConfirmationValid bool
}
type EvidenceFact struct {
	Kind       string // evidence_card or research_claim
	ID         string
	Version    string
	FieldIDs   []string
	SectionIDs []string
	RuleIDs    []string
}
type IssueFact struct {
	ID            string
	TargetVersion string
	RulesetHash   string
}
type ImpactFact struct {
	Category      string `json:"category"`
	TargetRef     string `json:"target_ref"`
	TargetVersion string `json:"target_version"`
	Reason        string `json:"reason"`
	Required      bool   `json:"required"`
}
type UpgradeImpactInput struct {
	ProjectID                 string
	SourceBasis               VersionBasis
	SourceDefinition          template.Template
	SourceRuleSet             template.RuleSet
	Target                    TemplateVersion
	FieldMigrations           []FieldMigrationFact
	Chapters                  []ChapterFact
	Evidence                  []EvidenceFact
	Issues                    []IssueFact
	ExpectedCurrentSnapshotID string
}
type UpgradePlan struct {
	ProjectID                 string               `json:"project_id"`
	SourceBasis               VersionBasis         `json:"source_basis"`
	TargetTemplateID          string               `json:"target_template_id"`
	TargetTemplateVersion     string               `json:"target_template_version"`
	TargetCatalogRevision     int64                `json:"target_catalog_revision"`
	TargetContentHash         string               `json:"target_content_hash"`
	TargetRuleSet             RuleSetRef           `json:"target_ruleset"`
	ExpectedCurrentSnapshotID string               `json:"expected_current_snapshot_id"`
	FieldMigrations           []FieldMigrationFact `json:"field_migrations"`
	Facts                     []ImpactFact         `json:"facts"`
	BlockingReasons           []string             `json:"blocking_reasons"`
	PreviewHash               string               `json:"preview_hash"`
}

// BuildUpgradePlan consumes server-resolved migration/dependency facts. It
// intentionally doesn't reproduce Workspace's field migration validator or G3's
// task identities/status machine. The caller supplies observed persisted values.
func BuildUpgradePlan(input UpgradeImpactInput) (UpgradePlan, error) {
	if strings.TrimSpace(input.ProjectID) == "" || input.SourceBasis.ProjectVersion < 1 || input.SourceBasis.TemplateCopyVersion < 1 || input.ExpectedCurrentSnapshotID != input.SourceBasis.CurrentSnapshotID {
		return UpgradePlan{}, ErrInvalidRequest
	}
	if err := ValidateVersion(input.Target); err != nil {
		return UpgradePlan{}, err
	}
	if input.Target.State != template.RuleSetPublished {
		return UpgradePlan{}, ErrInvalidState
	}
	if err := template.Validate(input.SourceDefinition); err != nil {
		return UpgradePlan{}, err
	}
	if err := template.ValidateRuleSet(input.SourceRuleSet); err != nil {
		return UpgradePlan{}, err
	}
	if input.SourceBasis.RuleSet.ID != input.SourceRuleSet.ID || input.SourceBasis.RuleSet.Version != input.SourceRuleSet.Version || input.SourceBasis.RuleSet.Hash != input.SourceRuleSet.RulesetHash {
		return UpgradePlan{}, ErrVersionConflict
	}
	plan := UpgradePlan{ProjectID: input.ProjectID, SourceBasis: input.SourceBasis, TargetTemplateID: input.Target.TemplateID, TargetTemplateVersion: input.Target.Version, TargetCatalogRevision: input.Target.Revision, TargetContentHash: input.Target.ContentHash, TargetRuleSet: input.Target.RuleSet, ExpectedCurrentSnapshotID: input.ExpectedCurrentSnapshotID, FieldMigrations: append([]FieldMigrationFact{}, input.FieldMigrations...), Facts: []ImpactFact{}, BlockingReasons: []string{}}
	changedFields := changedFieldIDs(input.SourceDefinition.Fields, input.Target.Definition.Fields)
	changedSections := changedSectionIDs(input.SourceDefinition.Sections, input.Target.Definition.Sections)
	changedRules := changedBindingIDs(input.SourceRuleSet.Bindings, input.Target.RuleSetSnapshot.Bindings)
	rulesChanged := input.SourceRuleSet.RulesetHash != input.Target.RulesetHash
	version := fmt.Sprintf("context/%d/copy/%d", input.SourceBasis.ContextRevision, input.SourceBasis.TemplateCopyVersion)
	add := func(category, ref, targetVersion, reason string) {
		plan.Facts = append(plan.Facts, ImpactFact{Category: category, TargetRef: ref, TargetVersion: targetVersion, Reason: reason, Required: true})
	}
	seen := map[string]bool{}
	for _, fact := range plan.FieldMigrations {
		if !identifier.MatchString(fact.FieldID) || seen[fact.FieldID] {
			return UpgradePlan{}, ErrInvalidRequest
		}
		seen[fact.FieldID] = true
		switch fact.Status {
		case "preserved", "orphaned", "incompatible", "added", "missing":
		default:
			return UpgradePlan{}, ErrInvalidRequest
		}
		if fact.Status != "preserved" || changedFields[fact.FieldID] {
			add("field_migration", "field/"+fact.FieldID, version, "field structure or migration changed")
		}
		if (fact.Status == "incompatible" || (fact.Status == "missing" && fact.Required)) && (!fact.Resolved || strings.TrimSpace(fact.Mapping) == "") {
			plan.BlockingReasons = append(plan.BlockingReasons, "unresolved_migration:"+fact.FieldID)
		}
	}
	for id := range changedSections {
		add("chapter_structure", "section/"+id, version, "chapter structure changed")
	}
	if rulesChanged {
		add("ruleset", "ruleset/"+input.Target.RuleSet.ID, input.Target.RuleSet.Version, "RuleSet version or binding snapshot changed")
	}
	for _, chapter := range input.Chapters {
		if chapter.ID == "" {
			return UpgradePlan{}, ErrInvalidRequest
		}
		if rulesChanged || changedSections[chapter.SectionID] {
			add("chapter_structure", "chapter/"+chapter.ID, chapter.VersionID, "chapter requires review under the new template/ruleset")
			if chapter.ConfirmationValid {
				add("confirmation", "chapter/"+chapter.ID, chapter.VersionID, "the previous confirmation belongs to the old template/ruleset")
			}
		}
	}
	for _, evidence := range input.Evidence {
		if evidence.Kind != "evidence_card" && evidence.Kind != "research_claim" {
			return UpgradePlan{}, ErrInvalidRequest
		}
		if evidence.ID == "" || evidence.Version == "" {
			return UpgradePlan{}, ErrInvalidRequest
		}
		if intersects(evidence.FieldIDs, changedFields) || intersects(evidence.SectionIDs, changedSections) || intersects(evidence.RuleIDs, changedRules) {
			category := "evidence"
			if evidence.Kind == "research_claim" {
				category = "claim"
			}
			add(category, evidence.Kind+"/"+evidence.ID, evidence.Version, "a declared template/rule dependency changed")
		}
	}
	for _, issue := range input.Issues {
		if rulesChanged {
			add("validation", "issue/"+issue.ID, issue.TargetVersion, "old issue dispositions cannot authorize the new RuleSet")
		}
	}
	add("delivery", "project/"+input.ProjectID, version, "new current snapshot/export requires G3 stabilization; existing snapshots remain historical")
	sort.Slice(plan.FieldMigrations, func(i, j int) bool { return plan.FieldMigrations[i].FieldID < plan.FieldMigrations[j].FieldID })
	sort.Slice(plan.Facts, func(i, j int) bool {
		a, b := plan.Facts[i], plan.Facts[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.TargetRef != b.TargetRef {
			return a.TargetRef < b.TargetRef
		}
		return a.TargetVersion < b.TargetVersion
	})
	unique := plan.Facts[:0]
	for _, fact := range plan.Facts {
		if len(unique) == 0 || unique[len(unique)-1] != fact {
			unique = append(unique, fact)
		}
	}
	plan.Facts = unique
	sort.Strings(plan.BlockingReasons)
	hash, err := hashJSON(plan)
	if err != nil {
		return UpgradePlan{}, err
	}
	plan.PreviewHash = hash
	return plan, nil
}
func ValidateUpgradePlan(plan UpgradePlan, actual VersionBasis, target TemplateVersion) error {
	if !reflect.DeepEqual(plan.SourceBasis, actual) || plan.ExpectedCurrentSnapshotID != actual.CurrentSnapshotID {
		return ErrVersionConflict
	}
	if target.TemplateID != plan.TargetTemplateID || target.Version != plan.TargetTemplateVersion || target.Revision != plan.TargetCatalogRevision || target.ContentHash != plan.TargetContentHash || target.RuleSet != plan.TargetRuleSet || target.State != template.RuleSetPublished {
		return ErrVersionConflict
	}
	if err := ValidateVersion(target); err != nil {
		return err
	}
	expected := plan.PreviewHash
	plan.PreviewHash = ""
	hash, err := hashJSON(plan)
	if err != nil {
		return err
	}
	if expected == "" || hash != expected {
		return ErrVersionConflict
	}
	if len(plan.BlockingReasons) > 0 {
		return ErrInvalidState
	}
	return nil
}
func changedFieldIDs(before, after []template.Field) map[string]bool {
	a := map[string]template.Field{}
	b := map[string]template.Field{}
	for _, v := range before {
		a[v.ID] = v
	}
	for _, v := range after {
		b[v.ID] = v
	}
	result := map[string]bool{}
	for id, v := range a {
		if !reflect.DeepEqual(v, b[id]) {
			result[id] = true
		}
	}
	for id, v := range b {
		if !reflect.DeepEqual(v, a[id]) {
			result[id] = true
		}
	}
	return result
}
func changedSectionIDs(before, after []template.Section) map[string]bool {
	a := map[string]template.Section{}
	b := map[string]template.Section{}
	for _, v := range before {
		a[v.ID] = v
	}
	for _, v := range after {
		b[v.ID] = v
	}
	result := map[string]bool{}
	for id, v := range a {
		if !reflect.DeepEqual(v, b[id]) {
			result[id] = true
		}
	}
	for id, v := range b {
		if !reflect.DeepEqual(v, a[id]) {
			result[id] = true
		}
	}
	return result
}
func changedBindingIDs(before, after []template.RuleBinding) map[string]bool {
	a := map[string]template.RuleBinding{}
	b := map[string]template.RuleBinding{}
	for _, v := range before {
		a[v.RuleID] = v
	}
	for _, v := range after {
		b[v.RuleID] = v
	}
	result := map[string]bool{}
	for id, v := range a {
		if !reflect.DeepEqual(v, b[id]) {
			result[id] = true
		}
	}
	for id, v := range b {
		if !reflect.DeepEqual(v, a[id]) {
			result[id] = true
		}
	}
	return result
}
func intersects(values []string, changed map[string]bool) bool {
	for _, value := range values {
		if changed[value] {
			return true
		}
	}
	return false
}
