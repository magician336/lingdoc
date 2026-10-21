package template

import (
	"slices"
	"testing"
)

// The demo template version 2 is the one that declares a required route node.
//
// The declaration has to live in the template rules, because the required flag
// of a route node has exactly one owner: the template. A client may not state
// it, and the server computes it from the template copy's required fields.

// The published version 1 stays exactly as it was: it is frozen into the T13
// wire contract, so adding a requirement to it would silently rewrite a
// published artifact.
func TestDemoTemplateVersionOneDeclaresNoRouteNode(t *testing.T) {
	definition := DemoTemplate()
	if definition.Version != DemoTemplateVersion {
		t.Fatalf("demo template version = %q, want %q", definition.Version, DemoTemplateVersion)
	}
	if slices.Contains(definition.RequiredFields, DemoRequiredRouteNodeID) {
		t.Fatalf("published demo template version 1 must not declare a route node: %+v", definition.RequiredFields)
	}
	for _, field := range definition.Fields {
		if field.ID == DemoRequiredRouteNodeID {
			t.Fatalf("published demo template version 1 must not declare the route node field: %+v", field)
		}
	}
}

// Version 2 declares the route node as a required field and binds a blocking
// presence rule to it.
func TestDemoRouteTemplateDeclaresTheRequiredRouteNode(t *testing.T) {
	definition := DemoRouteTemplate()
	if definition.ID != DemoTemplateID || definition.Version != DemoRouteTemplateVersion {
		t.Fatalf("route template identity = %q@%q, want %q@%q", definition.ID, definition.Version, DemoTemplateID, DemoRouteTemplateVersion)
	}
	if !slices.Contains(definition.RequiredFields, DemoRequiredRouteNodeID) {
		t.Fatalf("required fields = %+v, want the route node %q", definition.RequiredFields, DemoRequiredRouteNodeID)
	}
	declared := false
	for _, field := range definition.Fields {
		if field.ID == DemoRequiredRouteNodeID {
			declared = true
			if !field.Required {
				t.Fatalf("route node field must be required: %+v", field)
			}
		}
	}
	if !declared {
		t.Fatalf("route node field is not declared: %+v", definition.Fields)
	}
	bound := false
	for _, rule := range definition.Rules {
		if rule.ID != RuleRouteNodeRequired {
			continue
		}
		bound = true
		if rule.Kind != "presence" || rule.Severity != SeverityBlocking {
			t.Fatalf("route node rule = %+v, want a blocking presence rule", rule)
		}
		if rule.Parameters["target_kind"] != "field" || rule.Parameters["target_id"] != DemoRequiredRouteNodeID {
			t.Fatalf("route node rule target = %+v, want field/%s", rule.Parameters, DemoRequiredRouteNodeID)
		}
	}
	if !bound {
		t.Fatalf("rule %q is not declared: %+v", RuleRouteNodeRequired, definition.Rules)
	}
}

// The declared ruleset hash is published, so it has to be the server-computed
// one and not a value somebody typed.
func TestDemoRouteTemplatePublishesItsComputedHashes(t *testing.T) {
	definition := DemoRouteTemplate()
	content, ruleset, err := Hashes(definition)
	if err != nil {
		t.Fatalf("hash the route template: %v", err)
	}
	if ruleset != DemoRouteTemplateRulesetHash {
		t.Fatalf("ruleset hash = %q, want the published %q", ruleset, DemoRouteTemplateRulesetHash)
	}
	if definition.RulesetHash != DemoRouteTemplateRulesetHash {
		t.Fatalf("declared ruleset hash = %q, want %q", definition.RulesetHash, DemoRouteTemplateRulesetHash)
	}
	stamped, err := WithHashes(definition)
	if err != nil {
		t.Fatalf("stamp the route template: %v", err)
	}
	if stamped.ContentHash != content {
		t.Fatalf("content hash = %q, want %q", stamped.ContentHash, content)
	}
}

// The catalog serves both immutable demo versions, and only those.
func TestFixedDemoReaderServesBothDemoVersions(t *testing.T) {
	reader := FixedDemoReader{}
	published, err := reader.Get(DemoTemplateID, DemoTemplateVersion)
	if err != nil {
		t.Fatalf("read the published demo template: %v", err)
	}
	if published.Version != DemoTemplateVersion {
		t.Fatalf("published version = %q, want %q", published.Version, DemoTemplateVersion)
	}
	route, err := reader.Get(DemoTemplateID, DemoRouteTemplateVersion)
	if err != nil {
		t.Fatalf("read the route demo template: %v", err)
	}
	if route.Version != DemoRouteTemplateVersion {
		t.Fatalf("route version = %q, want %q", route.Version, DemoRouteTemplateVersion)
	}
	for _, request := range []struct{ id, version string }{
		{id: "unknown", version: DemoTemplateVersion},
		{id: DemoTemplateID, version: ""},
		{id: DemoTemplateID, version: "99"},
	} {
		if _, err := reader.Get(request.id, request.version); err == nil {
			t.Fatalf("Get(%q, %q) must not resolve", request.id, request.version)
		}
	}
}

// A missing required route node is a blocking validation issue: the field
// value is empty, so the presence rule and the required-fields rule both fire.
func TestDemoRouteTemplateBlocksWhenTheRequiredRouteNodeIsMissing(t *testing.T) {
	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "project/project-1/version/1/spec/1/copy/1",
		Fields: map[string]string{"research_subject": "合成研究对象", "research_goal": "合成研究目标"},
	}
	evaluated, err := EvaluateScene(DemoRouteTemplate(), "preflight", input)
	if err != nil {
		t.Fatalf("evaluate the route template: %v", err)
	}
	blocking := false
	for _, result := range evaluated {
		if result.TargetRef == "field/"+DemoRequiredRouteNodeID && result.Status == EvaluationIssue && result.Severity == SeverityBlocking {
			blocking = true
		}
	}
	if !blocking {
		t.Fatalf("a missing required route node must produce a blocking issue: %+v", evaluated)
	}
}

// The published template does not know about route nodes, so it must not
// report one: the two versions are not interchangeable.
func TestDemoTemplateVersionOneDoesNotBlockOnTheRouteNode(t *testing.T) {
	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "project/project-1/version/1/spec/1/copy/1",
		Fields: map[string]string{"research_subject": "合成研究对象", "research_goal": "合成研究目标"},
	}
	evaluated, err := EvaluateScene(DemoTemplate(), "preflight", input)
	if err != nil {
		t.Fatalf("evaluate the published demo template: %v", err)
	}
	for _, result := range evaluated {
		if result.TargetRef == "field/"+DemoRequiredRouteNodeID {
			t.Fatalf("published demo template version 1 reported a route node: %+v", result)
		}
	}
}

// A route node that exists and is current satisfies the requirement: the
// field value is the node's name, so no issue is raised for it.
func TestDemoRouteTemplatePassesWhenTheRequiredRouteNodeIsCurrent(t *testing.T) {
	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "project/project-1/version/1/spec/1/copy/1",
		Fields: map[string]string{
			"research_subject": "合成研究对象", "research_goal": "合成研究目标",
			DemoRequiredRouteNodeID: "合成路线 R7",
		},
	}
	evaluated, err := EvaluateScene(DemoRouteTemplate(), "preflight", input)
	if err != nil {
		t.Fatalf("evaluate the route template: %v", err)
	}
	for _, result := range evaluated {
		if result.TargetRef == "field/"+DemoRequiredRouteNodeID && result.Status == EvaluationIssue {
			t.Fatalf("a current required route node must not raise an issue: %+v", result)
		}
	}
}
