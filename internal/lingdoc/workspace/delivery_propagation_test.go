package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

func TestIndependentCopySidecarReachesActualFrozenCheckAndDocumentValidator(t *testing.T) {
	_, projects, db, actor, p, ctx := governanceAdapterFixtureWithProjects(t)
	p = activateProjectFixture(t, projects, actor, p, ctx)
	version := runtimeDemoVersionFixture(t)
	if p.TemplateCopy == nil {
		t.Fatal("actual project copy unavailable")
	}
	copy := *p.TemplateCopy
	copy.RuleSetSnapshot = &version.RuleSetSnapshot
	copy.SourceRuleSetID = version.RuleSet.ID
	copy.SourceRuleSetVersion = version.RuleSet.Version
	copy.SourceRuleSetHash = version.RuleSet.Hash
	copy.BindingPolicies = []governance.BindingPolicy{}
	copy.SourcePolicyHash = version.PolicyHash
	copy.Definition = version.Definition
	copy.ContentHash = version.ContentHash
	copy.RulesetHash = version.RulesetHash
	raw, _ := json.Marshal(copy)
	definition, _ := json.Marshal(copy.Definition)
	if err := db.Exec("UPDATE lingdoc_project_template_copies SET definition_json=?,content_hash=?,ruleset_hash=? WHERE id=?", string(definition), copy.ContentHash, copy.RulesetHash, copy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_g5_records(project_id,kind,id,version,payload_json) VALUES(?,'template_binding',?,1,?)", p.ID, copy.ID, string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	writeConfirmedChaptersFixture(t, projects, db, actor, p, ctx)
	reader := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	input, err := reader.ReadDeliveryInput(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if input.RuleSetSnapshot == nil || input.TemplateCopyRulesetHash == input.TemplateDefinition.RulesetHash || input.SourcePolicyHash != version.PolicyHash {
		t.Fatal("candidate reader collapsed independent source identity")
	}
	sourceRuntime := NewGORMSourceRuntime(db, ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects}, nil, nil, delivery.NewFixedTemplateReader())
	connectAggregateCapabilities(sourceRuntime, projects)
	assembler := sourceRuntime.DeliveryInputBuilder()
	frozen, err := assembler.Build(governanceCallerContext(ctx, actor), actor.UserID, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := delivery.NewReleaseService(delivery.NewMemorySnapshotStore()).Freeze(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Check.Status != delivery.CheckPassed || snapshot.Check.RulesetHash != version.RulesetHash {
		t.Fatalf("real freeze = %+v", snapshot.Check)
	}
	document := DeliveryDocument{}
	file, err := document.RenderFrozen(snapshot.FrozenInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.ValidateFrozen(snapshot.FrozenInput, file); err != nil {
		t.Fatal(err)
	}
	// A pure source catalog state change does not rewrite this project copy;
	// explicit copy-policy or binding identity changes do make it stale.
	current, err := reader.ReadDeliveryInput(context.WithoutCancel(ctx), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDeliveryState(snapshot.FrozenInput, current) {
		t.Fatal("unchanged exact copy no longer current")
	}
	current.SourcePolicyHash = "unreviewed"
	if sameDeliveryState(snapshot.FrozenInput, current) {
		t.Fatal("policy identity change left delivery current")
	}
	malformed := snapshot.FrozenInput
	malformed.TemplateCopyRulesetHash = malformed.Template.RulesetHash
	if err := document.ValidateFrozen(malformed, file); err == nil {
		t.Fatal("document validator accepted compiled/independent hash substitution")
	}
}
