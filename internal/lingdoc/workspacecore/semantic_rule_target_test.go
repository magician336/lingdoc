package workspacecore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

type sectionTargetTransaction struct {
	Transaction
	semanticTransaction
	chapters map[string][]Chapter
}

func (tx sectionTargetTransaction) Chapters(projectID string) ([]Chapter, error) {
	return tx.chapters[projectID], nil
}

func TestRuleSectionResolvesOnlyUniqueChapterInProject(t *testing.T) {
	tx := sectionTargetTransaction{chapters: map[string][]Chapter{
		"p": {{ID: "chapter-uuid", SectionID: "question", SourceIDs: []string{"source-question"}},
			{ID: "other-uuid", SectionID: "method"}},
		"other-project": {{ID: "other-project-uuid", SectionID: "external"}},
		"duplicate":     {{ID: "one", SectionID: "question"}, {ID: "two", SectionID: "question"}},
	}}
	got, err := chapterForRuleSection(tx, "p", "question")
	if err != nil || got.ID != "chapter-uuid" || !reflect.DeepEqual(got.SourceIDs, []string{"source-question"}) {
		t.Fatalf("section did not resolve its chapter/source: %+v err=%v", got, err)
	}
	if _, err := chapterForRuleSection(tx, "p", "external"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's section was accepted: %v", err)
	}
	if _, err := chapterForRuleSection(tx, "p", "chapter-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chapter UUID was accepted as a section ID: %v", err)
	}
	if _, err := chapterForRuleSection(tx, "duplicate", "question"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ambiguous section was accepted: %v", err)
	}
}

func activeRuleTargetFixture(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	s, actor, project := extendedImpactStore(t)
	set, err := template.PublishRuleSet(template.RuleSet{ID: "target-source", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "project", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"pattern": "."}, Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "owner"},
		{RuleID: "shared-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "project", TargetKind: "field", TargetID: "question", Parameters: map[string]any{"pattern": "."}, Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "owner"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.Clone(project.TemplateCopy.Definition)
	definition.Fields = append(definition.Fields, template.Field{ID: "question", Label: "Question field", Type: "string"})
	definition.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	policies := []governance.BindingPolicy{
		{RuleID: "goal-pattern", AllowedTargetKinds: []string{"chapter"}},
		{RuleID: "shared-pattern", AllowedTargetKinds: []string{"field", "chapter"}},
	}
	if err := governance.ValidateBindingPolicies(definition, set, policies); err != nil {
		t.Fatal(err)
	}
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	value := CatalogTemplateDefinition{Definition: definition, RuleSetSnapshot: &set, SourceRuleSetID: set.ID,
		SourceRuleSetVersion: set.Version, SourceRuleSetHash: set.RulesetHash, BindingPolicies: policies, SourcePolicyHash: policyHash}
	s.SetTemplateCatalogResolver(semanticCatalogFixture{value})
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := project
		copy, err := makeCatalogTemplateCopy(actor, project.ID, project.TemplateCopyVersion+1, value, TemplateCopyBound)
		if err != nil {
			return err
		}
		next.TemplateCopy, next.TemplateCopyVersion = copy, copy.Version
		next.ProjectVersion++
		return tx.UpdateProject(actor.TenantID, project, next)
	}); err != nil {
		t.Fatal(err)
	}
	project, err = s.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, actor, project
}

func TestActiveRuleTargetValidationAndInverse(t *testing.T) {
	s, actor, project := activeRuleTargetFixture(t)
	invalid := SemanticDelta{Kind: "RuleBinding", TargetID: "goal-pattern", ExpectedVersion: project.TemplateCopyVersion,
		ProposedBinding: &governance.BindingOverride{RuleID: "goal-pattern", TargetKind: "chapter"}}
	input := ExtendedChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision, Reason: "invalid target", SemanticDeltas: []SemanticDelta{invalid}}
	if _, _, _, err := s.CreateChangeDraft(context.Background(), actor, project.ID, "target-invalid", input); err == nil {
		t.Fatal("invalid chapter/research_goal target was accepted")
	}
	outsider := actor
	outsider.UserID = "another-user"
	if _, _, _, err := s.CreateChangeDraft(context.Background(), outsider, project.ID, "target-denied", input); err == nil {
		t.Fatal("non-member changed a project rule")
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || after.ProjectVersion != project.ProjectVersion || after.TemplateCopyVersion != project.TemplateCopyVersion {
		t.Fatalf("rejected proposal changed project: %+v err=%v", after, err)
	}
	for _, check := range []struct{ table, where string }{
		{"lingdoc_operations", "key IN ('target-invalid', 'target-denied')"},
		{"lingdoc_project_audits", "action = 'semantic.apply'"},
	} {
		var count int64
		if err := s.testDB().Table(check.table).Where(check.where).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rejected proposal left %s count=%d err=%v", check.table, count, err)
		}
	}
	valid := SemanticDelta{Kind: "RuleBinding", TargetID: "shared-pattern", ExpectedVersion: project.TemplateCopyVersion,
		ProposedBinding: &governance.BindingOverride{RuleID: "shared-pattern", TargetKind: "chapter"}}
	change := semanticDraft(t, s, actor, project, "target-valid", valid)
	semanticApply(t, s, actor, project, change, "target-valid")
	current, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || current.TemplateCopy.RuleSetSnapshot.Bindings[1].TargetKind != "chapter" || current.TemplateCopy.RuleSetSnapshot.Bindings[1].TargetID != "question" {
		t.Fatalf("valid chapter target not applied: %+v err=%v", current.TemplateCopy, err)
	}
	raw, _, _, err := s.ReverseChangeSet(context.Background(), actor, project.ID, change.ID, "target-inverse-draft")
	inverse := decodeAgentJSON[ChangeSet](t, raw, err)
	semanticApply(t, s, actor, current, inverse, "target-inverse")
	restored, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || restored.TemplateCopy.RuleSetSnapshot.Bindings[1].TargetKind != "field" || restored.TemplateCopy.RuleSetSnapshot.Bindings[1].TargetID != "question" {
		t.Fatalf("controlled inverse did not restore the target: %+v err=%v", restored.TemplateCopy, err)
	}
}

func TestRuleSectionSourcesFollowTheEffectivePartialOverride(t *testing.T) {
	s, _, project := activeRuleTargetFixture(t)
	if err := s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		wrapped := sectionTargetTransaction{Transaction: tx, semanticTransaction: tx.(semanticTransaction), chapters: map[string][]Chapter{
			project.ID: {{ID: "uuid-not-section-id", SectionID: "question", SourceIDs: []string{"source-question"}}},
		}}
		delta := SemanticDelta{Kind: "RuleBinding", TargetID: "shared-pattern", ProposedBinding: &governance.BindingOverride{RuleID: "shared-pattern", TargetKind: "chapter"}}
		ids, err := semanticSourceIDs(wrapped, project, []SemanticDelta{delta})
		if err != nil || !reflect.DeepEqual(ids, []string{"source-question"}) {
			t.Fatalf("proposed section source was not included: %v err=%v", ids, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuleSectionOverrideRechecksSourceAccess(t *testing.T) {
	s, actor, project := activeRuleTargetFixture(t)
	chapters, err := s.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var chapter *Chapter
	for i := range chapters {
		if chapters[i].SectionID == "question" {
			chapter = &chapters[i]
			break
		}
	}
	if chapter == nil {
		t.Fatal("question chapter is missing from the active fixture")
	}
	if _, _, _, err := s.SaveChapter(context.Background(), actor, project.ID, chapter.ID, "target-source-chapter", SaveChapterInput{
		ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: project.SpecRevision,
		BodyMarkdown: "A source-backed question [[source:source-question]].", SourceIDs: []string{"source-question"},
	}); err != nil {
		t.Fatal(err)
	}
	policy, ok := s.sources.(*fakeSources)
	if !ok {
		t.Fatal("fixture source policy unavailable")
	}
	policy.calls = nil
	policy.verdict = ErrSourceUnavailable
	input := ExtendedChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision, Reason: "use a chapter target",
		SemanticDeltas: []SemanticDelta{{Kind: "RuleBinding", TargetID: "shared-pattern", ExpectedVersion: project.TemplateCopyVersion,
			ProposedBinding: &governance.BindingOverride{RuleID: "shared-pattern", TargetKind: "chapter"}}}}
	if _, _, _, err := s.CreateChangeDraft(context.Background(), actor, project.ID, "target-source-denied", input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("unavailable chapter source did not block the target change: %v", err)
	}
	if len(policy.calls) == 0 || policy.calls[len(policy.calls)-1].projectID != project.ID || policy.calls[len(policy.calls)-1].actorID != actor.UserID ||
		!reflect.DeepEqual(policy.calls[len(policy.calls)-1].sourceIDs, []string{"source-question"}) {
		t.Fatalf("source policy did not receive the project, actor, and section sources: %+v", policy.calls)
	}
	var operations, changes int64
	if err := s.testDB().Table("lingdoc_operations").Where("key = ?", "target-source-denied").Count(&operations).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Table("lingdoc_change_sets").Where("project_id = ?", project.ID).Count(&changes).Error; err != nil {
		t.Fatal(err)
	}
	if operations != 0 || changes != 0 {
		t.Fatalf("source denial left operations=%d changes=%d", operations, changes)
	}
}
