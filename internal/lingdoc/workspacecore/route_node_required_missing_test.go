package workspacecore

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/stretchr/testify/require"
)

// The route aware evaluation is the projection the template evaluator reads,
// and it is the only place where the roadmap speaks for a route node id.
//
// A route node id the template declares as required but the roadmap does not
// hold yet has no value at all: the placeholder the spec may carry under that
// id must not stand in for the missing node, or the template reads the project
// as complete while the required route is not there (issue #319). The three
// preconditions of a required route node - missing, not reviewed, behind its
// baseline - therefore produce the same blocking issue, and they produce it
// from the same rule.
func TestRouteAwareEvaluationReportsADeclaredRouteNodeTheRoadmapDoesNotHold(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		svc, actor, p := routeNodeDeclaredProject(t)

		// The spec still carries the placeholder under the route node id.
		require.Equal(t, "合成路线占位值（真正的值由路线图节点提供）", p.Spec[roadmapRequiredNodeID])

		fields := routeEvaluationFields(t, svc, p)
		require.Equal(t, "", fields[roadmapRequiredNodeID],
			"the spec placeholder must not stand in for a route node the roadmap does not hold")
		// The ordinary spec fields are still owned by the spec.
		require.Equal(t, "旧主题", fields["research_subject"])
		require.Equal(t, "旧目标", fields["research_goal"])

		issue, found := routeBlockingIssue(routeEvaluationIssues(t, svc, actor, p), lingdoctemplate.RuleRouteNodeRequired, roadmapRequiredNodeID)
		require.True(t, found, "a declared route node the roadmap does not hold must produce a blocking issue")
		require.Equal(t, lingdoctemplate.RuleRouteNodeRequired, issue.RuleID,
			"the missing route node is reported by the same rule as the other two preconditions")
		require.Equal(t, lingdoctemplate.SeverityBlocking, issue.Severity)
		require.Equal(t, "field/"+roadmapRequiredNodeID, issue.TargetRef)
	})

	t.Run("pending_review", func(t *testing.T) {
		svc, actor, p := routeNodeDeclaredProject(t)
		appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "pending_review")

		fields := routeEvaluationFields(t, svc, p)
		require.Equal(t, "", fields[roadmapRequiredNodeID],
			"a route node that has not been reviewed carries no value")

		issue, found := routeBlockingIssue(routeEvaluationIssues(t, svc, actor, p), lingdoctemplate.RuleRouteNodeRequired, roadmapRequiredNodeID)
		require.True(t, found, "a pending-review route node must produce a blocking issue")
		require.Equal(t, lingdoctemplate.RuleRouteNodeRequired, issue.RuleID)
		require.Equal(t, lingdoctemplate.SeverityBlocking, issue.Severity)
	})

	t.Run("stale", func(t *testing.T) {
		svc, actor, p := routeNodeDeclaredProject(t)
		// The required node pins a prerequisite version, and the prerequisite
		// moves on afterwards: the required node is then behind its baseline.
		appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
		advanceRouteNode(t, svc, p, "roadmap-node-1", "合成路线 roadmap-node-1 v2")
		appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "ready")
		behind := RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Name: "技术路线 R7", Required: true, Status: "ready",
			Version: 2, ContextRevision: p.CurrentContextRevision, ActorID: actor.UserID,
			Inputs: []ObjectVersionRef{{Kind: "RouteNode", ID: "roadmap-node-1", Version: "1"}},
			Reason: "版本落后", UpdatedAt: time.Now().UTC()}
		require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			return tx.(semanticTransaction).AppendRouteNode(RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Version: 1}, behind)
		}))

		fields := routeEvaluationFields(t, svc, p)
		require.Equal(t, "", fields[roadmapRequiredNodeID],
			"a route node behind its baseline carries no value")

		issue, found := routeBlockingIssue(routeEvaluationIssues(t, svc, actor, p), lingdoctemplate.RuleRouteNodeRequired, roadmapRequiredNodeID)
		require.True(t, found, "a route node behind its baseline must produce a blocking issue")
		require.Equal(t, lingdoctemplate.RuleRouteNodeRequired, issue.RuleID)
		require.Equal(t, lingdoctemplate.SeverityBlocking, issue.Severity)
	})

	t.Run("current", func(t *testing.T) {
		svc, actor, p := routeNodeDeclaredProject(t)
		appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "ready")

		fields := routeEvaluationFields(t, svc, p)
		require.Equal(t, "合成路线 "+roadmapRequiredNodeID, fields[roadmapRequiredNodeID],
			"the roadmap speaks for a route node it holds")

		_, found := routeBlockingIssue(routeEvaluationIssues(t, svc, actor, p), lingdoctemplate.RuleRouteNodeRequired, roadmapRequiredNodeID)
		require.False(t, found, "a current required route node must not raise a blocking issue")
	})
}

// routeNodeDeclaredProject is a project whose template copy declares the route
// node as required and whose spec carries a placeholder under the route node
// id. The placeholder is what a client that was told the template requires the
// field writes; it must not decide whether the required route is there.
func routeNodeDeclaredProject(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	svc, actor, p := extendedImpactStore(t)
	p = roadmapTemplateCopy(t, svc, actor, p)
	p = roadmapProject(t, svc, actor, p.ID)
	return svc, actor, routeNodeSpecPlaceholder(t, svc, actor, p, roadmapRequiredNodeID)
}

// routeNodeSpecPlaceholder writes a placeholder under the route node id into
// the project spec. An active project refuses an ordinary spec save, so the
// write goes straight to the store, the way the other read-side cases do.
func routeNodeSpecPlaceholder(t *testing.T, svc *Service, actor Actor, p Project, nodeID string) Project {
	t.Helper()
	spec := map[string]string{}
	for key, value := range p.Spec {
		spec[key] = value
	}
	spec[nodeID] = "合成路线占位值（真正的值由路线图节点提供）"
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NoError(t, svc.repository.(*GORMRepository).db.Model(&projectRow{}).Where("id = ?", p.ID).
		Updates(map[string]any{"spec_json": string(raw)}).Error)
	return roadmapProject(t, svc, actor, p.ID)
}

// The route node declaration is the contract's route node rule and nothing
// else. An ordinary presence binding over a spec field says nothing about the
// roadmap, and the template's required fields carry the ordinary spec fields
// as well - a route node id is the field the contract's route node rule binds.
func TestRouteNodeRequiredIDsNamesOnlyTheDeclaredRouteNode(t *testing.T) {
	definition, err := (lingdoctemplate.FixedDemoReader{}).Get(lingdoctemplate.DemoTemplateID, lingdoctemplate.DemoTemplateVersion)
	require.NoError(t, err)
	definition.Fields = append(definition.Fields,
		lingdoctemplate.Field{ID: "route-r7", Label: "技术路线 R7", Type: "text", Required: true},
		lingdoctemplate.Field{ID: "free-node", Label: "可选路线", Type: "text"})
	definition.RequiredFields = append(definition.RequiredFields, "route-r7")
	definition.Rules = append(definition.Rules,
		// An ordinary presence binding over an ordinary spec field.
		lingdoctemplate.Rule{ID: "goal-required", Kind: "presence", Severity: lingdoctemplate.SeverityBlocking,
			Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal"}},
		// The contract's route node rule.
		lingdoctemplate.Rule{ID: lingdoctemplate.RuleRouteNodeRequired, Kind: "presence", Severity: lingdoctemplate.SeverityBlocking,
			Parameters: map[string]any{"target_kind": "field", "target_id": "route-r7"}},
		// A route node rule over a field the template does not require: the
		// field is a free one the spec owns.
		lingdoctemplate.Rule{ID: lingdoctemplate.RuleRouteNodeRequired, Kind: "presence", Severity: lingdoctemplate.SeverityBlocking,
			Parameters: map[string]any{"target_kind": "field", "target_id": "free-node"}},
	)

	project := Project{TemplateCopy: &ProjectTemplateCopy{Definition: definition}}
	require.Equal(t, []string{"route-r7"}, routeNodeRequiredIDs(project),
		"only the field the contract's route node rule binds and the template requires is a route node id")
	require.Nil(t, routeNodeRequiredIDs(Project{}),
		"a project without a template copy declares no route node")
}

// routeEvaluationFields reads the route aware projection of the current
// project state.
func routeEvaluationFields(t *testing.T, svc *Service, p Project) map[string]string {
	t.Helper()
	var fields map[string]string
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		fields, err = RouteEvaluationFields(tx, p)
		return err
	}))
	return fields
}

// routeEvaluationIssues evaluates the project template copy against the route
// aware projection, the same seam the delivery gate reads.
func routeEvaluationIssues(t *testing.T, svc *Service, actor Actor, p Project) []lingdoctemplate.Evaluation {
	t.Helper()
	current, err := svc.GetProject(context.Background(), actor, p.ID)
	require.NoError(t, err)
	input := lingdoctemplate.EvaluationInput{
		ProjectID: current.ID, TargetVersion: strconv.FormatInt(current.ProjectVersion, 10),
		Fields: routeEvaluationFields(t, svc, current),
	}
	evaluated, err := lingdoctemplate.EvaluateScene(current.TemplateCopy.Definition, "preflight", input)
	require.NoError(t, err)
	return evaluated
}

// routeBlockingIssue returns the blocking issue one rule reports for a route
// node id.
func routeBlockingIssue(evaluated []lingdoctemplate.Evaluation, ruleID, nodeID string) (lingdoctemplate.Evaluation, bool) {
	for _, result := range evaluated {
		if result.RuleID == ruleID && result.TargetRef == "field/"+nodeID && result.Status == lingdoctemplate.EvaluationIssue && result.Severity == lingdoctemplate.SeverityBlocking {
			return result, true
		}
	}
	return lingdoctemplate.Evaluation{}, false
}
