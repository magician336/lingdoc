package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/stretchr/testify/require"
)

// The versioned route-roadmap editing session (#250).
//
// These cases drive the module through its public service surface, on the real
// migrated SQLite store, so the version counting, the frozen/unfrozen split
// and the cancellation cleanup are observed as domain behaviour rather than
// as store bookkeeping.

// roadmapRequiredNodeID is the route node the template rules declare as
// required. The required flag has exactly one owner - the template rules - so
// the fixture binds a rule set that declares it.
//
// The identifier is lowercase because a rule binding target has to be a stable
// id in this codebase ("r7" is the same route node the acceptance criteria
// call "R7").
const roadmapRequiredNodeID = "r7"

// advanceRouteNode moves a route node to its next frozen version, so a node
// that pinned the previous version is behind.
func advanceRouteNode(t *testing.T, svc *Service, p Project, id, name string) {
	t.Helper()
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).AppendRouteNode(RouteNode{ID: id, ProjectID: p.ID, Version: 1}, RouteNode{
			ID: id, ProjectID: p.ID, Version: 2, Name: name, Status: "ready", Required: false,
			ContextRevision: p.CurrentContextRevision, ActorID: "route-author", Inputs: []ObjectVersionRef{},
			Reason: "版本推进", UpdatedAt: time.Now().UTC(),
		})
	}))
}

// roadmapTemplateCopy installs a project template copy that declares the route
// node as a required field and carries the blocking presence rule for it.
//
// The required flag of a route node is declared by the template rules, so the
// fixture has to give the project a template that says so. The copy is
// written the same way the production copy is stored, so the definition the
// service reads back is the one the fixture wrote.
func roadmapTemplateCopy(t *testing.T, svc *Service, actor Actor, p Project) Project {
	t.Helper()
	definition, err := (lingdoctemplate.FixedDemoReader{}).Get("template-demo", "1")
	require.NoError(t, err)
	definition.Fields = append(definition.Fields, lingdoctemplate.Field{ID: roadmapRequiredNodeID, Label: "技术路线 " + roadmapRequiredNodeID, Type: "text"})
	definition.RequiredFields = append(definition.RequiredFields, roadmapRequiredNodeID)
	definition.Rules = append(definition.Rules, lingdoctemplate.Rule{ID: lingdoctemplate.RuleRouteNodeRequired, Kind: "presence", Severity: lingdoctemplate.SeverityBlocking,
		Parameters: map[string]any{"target_kind": "field", "target_id": roadmapRequiredNodeID}})
	definition, err = lingdoctemplate.WithHashes(definition)
	require.NoError(t, err)
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	version := p.TemplateCopyVersion + 1
	require.NoError(t, svc.testDB().Exec(`INSERT INTO lingdoc_project_template_copies(id,project_id,version,source_template_id,source_template_version,status,content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"roadmap-copy-"+p.ID, p.ID, version, definition.ID, definition.Version, TemplateCopyBound, definition.ContentHash, definition.RulesetHash, string(raw), actor.UserID, time.Now().UTC()).Error)
	require.NoError(t, svc.testDB().Exec("UPDATE lingdoc_projects SET template_copy_version = ? WHERE id = ?", version, p.ID).Error)
	return roadmapProject(t, svc, actor, p.ID)
}

// roadmapProject reloads the project after a template change.
func roadmapProject(t *testing.T, svc *Service, actor Actor, projectID string) Project {
	t.Helper()
	p, err := svc.GetProject(context.Background(), actor, projectID)
	require.NoError(t, err)
	return p
}

func roadmapDecodeSession(t *testing.T, raw json.RawMessage) RoadmapEditSession {
	t.Helper()
	var session RoadmapEditSession
	require.NoError(t, json.Unmarshal(raw, &session))
	return session
}

func roadmapDecodeResult(t *testing.T, raw json.RawMessage) RoadmapCommitResult {
	t.Helper()
	var result RoadmapCommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

// A proposal lives in the session. The frozen roadmap does not move until the
// session is committed, so the export input cannot read a draft.
func TestRoadmapProposalStaysOutOfTheFrozenRoadmap(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")

	raw, status, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-1", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	require.Equal(t, 201, status)
	session := roadmapDecodeSession(t, raw)
	require.Equal(t, RoadmapEditSessionOpen, session.Status)
	require.Equal(t, int64(1), session.Baseline.Nodes[0].Version)

	raw, status, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-1", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "改写后的路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)
	require.Equal(t, 200, status)
	proposed := roadmapDecodeSession(t, raw)
	require.Len(t, proposed.NodeProposals, 1)
	require.Equal(t, int64(2), proposed.NodeProposals[0].TemporaryVersion)

	// The frozen node is untouched: same version, same name.
	nodes, err := svc.ListRouteNodes(context.Background(), actor, p.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, int64(1), nodes[0].Version)
	require.Equal(t, "合成路线 roadmap-node-1", nodes[0].Name)
}

// A committed text edit freezes a new route node version, and the objects that
// referenced the old version get a review obligation in the same transaction.
func TestRoadmapCommitFreezesANewNodeVersionAndCreatesTheReviewTask(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	appendRouteNode(t, svc, p, "roadmap-node-2", false, "ready")
	// roadmap-node-2 depends on the first version of roadmap-node-1, which is
	// the relation a new version has to re-check.
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return StoreConfirmedDependency(tx, DependencyEdge{
			ProjectID: p.ID,
			Source:    ObjectVersionRef{Kind: "RouteNode", ID: "roadmap-node-1", Version: "1"},
			Target:    ObjectVersionRef{Kind: "RouteNode", ID: "roadmap-node-2", Version: "1"},
			Relation:  "route_input", Action: "REVIEW_REQUIRED", Origin: "user", ActorID: actor.UserID, Reason: "合成前置关系",
		})
	}))

	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-2", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-2", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "改写后的路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)

	raw, status, _, err := svc.CommitRoadmapEdit(context.Background(), actor, p.ID, session.ID, "roadmap-commit-2", RoadmapCommitInput{ExpectedBaselineVersion: session.Baseline.Version, Reason: "提交路线图编辑"})
	require.NoError(t, err)
	require.Equal(t, 200, status)
	result := roadmapDecodeResult(t, raw)
	require.False(t, result.Semantic)
	require.Len(t, result.AppliedNodes, 1)
	require.Equal(t, int64(2), result.AppliedNodes[0].Version)

	nodes, err := svc.ListRouteNodes(context.Background(), actor, p.ID)
	require.NoError(t, err)
	for _, node := range nodes {
		if node.ID == "roadmap-node-1" {
			require.Equal(t, int64(2), node.Version, "the commit must freeze a new version")
			require.Equal(t, "改写后的路线", node.Name)
		}
	}

	tasks, err := svc.ListImpactTasks(context.Background(), actor, p.ID)
	require.NoError(t, err)
	found := false
	for _, task := range tasks {
		if task.Target == (ObjectVersionRef{Kind: "RouteNode", ID: "roadmap-node-2", Version: "1"}) {
			found = true
			require.Equal(t, "OPEN", task.Status)
			require.Equal(t, "REVIEW_REQUIRED", task.Action)
		}
	}
	require.True(t, found, "the dependent node must get a review task: %+v", tasks)
}

// Cancelling an unfrozen session restores the frozen roadmap and releases
// every reference to the temporary version the session proposed.
func TestRoadmapCancelRestoresTheBaselineAndReleasesTemporaryReferences(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")

	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-3", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-3", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "被取消的路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)

	// An obligation that was raised against the temporary version. It can
	// never be discharged, because that version was never frozen.
	temporary := ObjectVersionRef{Kind: "RouteNode", ID: "roadmap-node-1", Version: "2"}
	task := guardTask(t, svc, ImpactTask{ID: "roadmap-cancel-task", ProjectID: p.ID, ChangeSetID: "roadmap-" + session.ID,
		Target: temporary, TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "OPEN",
		ClosureEvidence: []ObjectVersionRef{temporary}})

	raw, status, _, err := svc.CancelRoadmapEdit(context.Background(), actor, p.ID, session.ID, "roadmap-cancel-3", RoadmapCancelInput{ExpectedBaselineVersion: session.Baseline.Version, Reason: "取消编辑"})
	require.NoError(t, err)
	require.Equal(t, 200, status)
	cancelled := roadmapDecodeSession(t, raw)
	require.Equal(t, RoadmapEditSessionCancelled, cancelled.Status)

	// The frozen roadmap is the baseline again.
	nodes, err := svc.ListRouteNodes(context.Background(), actor, p.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, int64(1), nodes[0].Version)
	require.Equal(t, "合成路线 roadmap-node-1", nodes[0].Name)

	// The task that pointed at the cancelled version is invalidated and no
	// longer carries closure evidence that names it.
	tasks, err := svc.ListImpactTasks(context.Background(), actor, p.ID)
	require.NoError(t, err)
	for _, current := range tasks {
		if current.ID != task.ID {
			continue
		}
		require.Equal(t, "STALE", current.Status)
		require.Equal(t, ImpactTaskStaleCancelledVersion, current.StaleReason)
		for _, evidence := range current.ClosureEvidence {
			require.NotEqual(t, temporary, evidence, "closure evidence must not name a cancelled version")
		}
	}
}

// A semantic change does not reach the roadmap: the commit produces a
// ChangeSet instead, and the ChangeSet carries the proposal.
func TestRoadmapSemanticCommitProducesAChangeSetInsteadOfWritingTheRoadmap(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-4", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-4", RoadmapNodeProposalInput{
		NodeID: "roadmap-new-node", Kind: RoadmapNodeProposalAdd, Name: "新增路线节点", Status: "planned", Reason: "新增节点",
	})
	require.NoError(t, err)

	raw, status, _, err := svc.CommitRoadmapEdit(context.Background(), actor, p.ID, session.ID, "roadmap-commit-4", RoadmapCommitInput{ExpectedBaselineVersion: session.Baseline.Version, Reason: "提交路线图编辑"})
	require.NoError(t, err)
	require.Equal(t, 200, status)
	result := roadmapDecodeResult(t, raw)
	require.True(t, result.Semantic)
	require.NotEmpty(t, result.ChangeSetID)

	// The roadmap itself did not move.
	nodes, err := svc.ListRouteNodes(context.Background(), actor, p.ID)
	require.NoError(t, err)
	for _, node := range nodes {
		require.NotEqual(t, "roadmap-new-node", node.ID, "a semantic change must not land in the roadmap directly")
	}

	// The ChangeSet carries the proposal as a route node delta.
	var changes []ChangeSet
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		changes, err = tx.(changeSetTransaction).ChangeSets(p.ID)
		return err
	}))
	found := false
	for _, change := range changes {
		if change.ID != result.ChangeSetID {
			continue
		}
		for _, delta := range change.SemanticDeltas {
			if delta.Kind == "RouteNode" && delta.TargetID == "roadmap-new-node" {
				found = true
				require.NotNil(t, delta.ProposedRoute)
				require.Equal(t, "新增路线节点", delta.ProposedRoute.Name)
			}
		}
	}
	require.True(t, found, "the change set must carry the route node proposal")
}

// The version sequence and the before/after comparison are readable.
func TestRoadmapNodeVersionSequenceAndComparison(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")

	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-5", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-5", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "第二版路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)
	_, _, _, err = svc.CommitRoadmapEdit(context.Background(), actor, p.ID, session.ID, "roadmap-commit-5", RoadmapCommitInput{ExpectedBaselineVersion: session.Baseline.Version, Reason: "提交路线图编辑"})
	require.NoError(t, err)

	entries, err := svc.ReadRoadmapNodeVersions(context.Background(), actor, p.ID, "roadmap-node-1")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, int64(1), entries[0].Version)
	require.Equal(t, int64(2), entries[1].Version)
	require.Equal(t, "第二版路线", entries[1].Name)

	comparison, err := svc.CompareRoadmapNodeVersions(context.Background(), actor, p.ID, "roadmap-node-1", 1, 2)
	require.NoError(t, err)
	require.Equal(t, "RouteNode", comparison.Kind)
	changed := map[string]RoadmapFieldChange{}
	for _, change := range comparison.Changes {
		changed[change.Field] = change
	}
	require.Contains(t, changed, "name")
	require.Equal(t, "合成路线 roadmap-node-1", changed["name"].From)
	require.Equal(t, "第二版路线", changed["name"].To)
}

// The required flag belongs to the template rules. A client cannot state it.
func TestRoadmapRequiredFlagComesFromTheTemplateRules(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	p = roadmapTemplateCopy(t, svc, actor, p)
	p = roadmapProject(t, svc, actor, p.ID)

	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-6", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)

	claimed := true
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-6-claim", RoadmapNodeProposalInput{
		NodeID: roadmapRequiredNodeID, Kind: RoadmapNodeProposalAdd, Name: "技术路线 R7", Status: "ready", Required: &claimed, Reason: "新增必经路线",
	})
	require.NoError(t, err, "the client may state the flag the rules already declare")

	denied := false
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-6-deny", RoadmapNodeProposalInput{
		NodeID: roadmapRequiredNodeID, Kind: RoadmapNodeProposalAdd, Name: "技术路线 R7", Status: "ready", Required: &denied, Reason: "新增必经路线",
	})
	require.ErrorIs(t, err, ErrInvalidRequest, "a client must not downgrade a required route node")

	// A node the rules do not require cannot be declared required either.
	claimedOptional := true
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-6-optional", RoadmapNodeProposalInput{
		NodeID: "roadmap-optional-node", Kind: RoadmapNodeProposalAdd, Name: "可选路线", Status: "planned", Required: &claimedOptional, Reason: "新增可选路线",
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

// The three required-node states of AC-G3-08 are constructible, and each of
// them leaves the required field without a value, which is what the template
// evaluator turns into a blocking validation issue.
func TestRoadmapRequiredNodeStatesProduceBlockingValidationIssues(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	p = roadmapTemplateCopy(t, svc, actor, p)
	p = roadmapProject(t, svc, actor, p.ID)

	evaluate := func(t *testing.T) []lingdoctemplate.Evaluation {
		t.Helper()
		current := roadmapProject(t, svc, actor, p.ID)
		var fields map[string]string
		require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			var err error
			fields, err = RouteEvaluationFields(tx, current)
			return err
		}))
		input := lingdoctemplate.EvaluationInput{ProjectID: p.ID, TargetVersion: strconv.FormatInt(current.ProjectVersion, 10), Fields: fields}
		evaluated, err := lingdoctemplate.EvaluateScene(current.TemplateCopy.Definition, "preflight", input)
		require.NoError(t, err)
		return evaluated
	}
	blocking := func(evaluated []lingdoctemplate.Evaluation) bool {
		for _, result := range evaluated {
			if result.Status == lingdoctemplate.EvaluationIssue && result.Severity == lingdoctemplate.SeverityBlocking && result.TargetRef == "field/"+roadmapRequiredNodeID {
				return true
			}
		}
		return false
	}

	// 1) the required node is missing.
	require.True(t, blocking(evaluate(t)), "a missing required route node must produce a blocking issue")

	// 2) the required node exists but has not been reviewed.
	appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "pending_review")
	require.True(t, blocking(evaluate(t)), "a pending-review route node must produce a blocking issue")

	// 3) the required node is behind: it pinned an input version that moved.
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	advanceRouteNode(t, svc, p, "roadmap-node-1", "合成路线 roadmap-node-1 v2")
	behind := RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Name: "技术路线 R7", Required: true, Status: "ready",
		Version: 2, ContextRevision: p.CurrentContextRevision, ActorID: actor.UserID,
		Inputs: []ObjectVersionRef{{Kind: "RouteNode", ID: "roadmap-node-1", Version: "1"}},
		Reason: "版本落后", UpdatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).AppendRouteNode(RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Version: 1}, behind)
	}))
	require.True(t, blocking(evaluate(t)), "a route node behind its baseline must produce a blocking issue")
}

// The route node state blocks the stabilization of the revision: a required
// route that is not ready, not reviewed or behind cannot be released.
func TestRoadmapRequiredNodeStateBlocksStabilization(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	p = roadmapTemplateCopy(t, svc, actor, p)
	p = roadmapProject(t, svc, actor, p.ID)

	blockers := func(t *testing.T) []ImpactBlocker {
		t.Helper()
		var found []ImpactBlocker
		require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			var err error
			found, err = RouteStabilizationBlockers(tx, p)
			return err
		}))
		return found
	}
	has := func(list []ImpactBlocker, code, target string) bool {
		for _, blocker := range list {
			if blocker.Code == code && blocker.TargetID == target {
				return true
			}
		}
		return false
	}

	// The required route exists but has not been reviewed.
	appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "pending_review")
	list := blockers(t)
	require.True(t, has(list, "required_route_not_current", roadmapRequiredNodeID),
		"a pending-review required route must block stabilization: %+v", list)

	// The required route is behind its baseline.
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	advanceRouteNode(t, svc, p, "roadmap-node-1", "合成路线 roadmap-node-1 v2")
	behind := RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Name: "技术路线 r7", Required: true, Status: "ready",
		Version: 2, ContextRevision: p.CurrentContextRevision, ActorID: actor.UserID,
		Inputs: []ObjectVersionRef{{Kind: "RouteNode", ID: "roadmap-node-1", Version: "1"}},
		Reason: "版本落后", UpdatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).AppendRouteNode(RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Version: 1}, behind)
	}))
	list = blockers(t)
	require.True(t, has(list, "required_route_not_current", roadmapRequiredNodeID),
		"a route node behind its baseline must block stabilization: %+v", list)
}

// The route node state tells the three states apart.
func TestRoadmapNodeStateDistinguishesMissingPendingReviewAndStale(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	p = roadmapTemplateCopy(t, svc, actor, p)
	p = roadmapProject(t, svc, actor, p.ID)

	read := func(t *testing.T, nodeID string) RoadmapNodeView {
		t.Helper()
		baseline, err := svc.ReadRoadmap(context.Background(), actor, p.ID)
		require.NoError(t, err)
		for _, node := range baseline.Nodes {
			if node.ID == nodeID {
				return node
			}
		}
		return RoadmapNodeView{}
	}

	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	advanceRouteNode(t, svc, p, "roadmap-node-1", "合成路线 roadmap-node-1 v2")
	appendRouteNode(t, svc, p, roadmapRequiredNodeID, true, "pending_review")
	require.Equal(t, RoadmapNodeStatePendingReview, read(t, roadmapRequiredNodeID).State)
	require.True(t, read(t, roadmapRequiredNodeID).Required, "the required flag comes from the rules")

	behind := RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Name: "技术路线 R7", Required: true, Status: "ready",
		Version: 2, ContextRevision: p.CurrentContextRevision, ActorID: actor.UserID,
		Inputs: []ObjectVersionRef{{Kind: "RouteNode", ID: "roadmap-node-1", Version: "1"}},
		Reason: "版本落后", UpdatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).AppendRouteNode(RouteNode{ID: roadmapRequiredNodeID, ProjectID: p.ID, Version: 1}, behind)
	}))
	require.Equal(t, RoadmapNodeStateStale, read(t, roadmapRequiredNodeID).State)
}

// A second unfrozen session for the same project is refused.
func TestRoadmapAllowsOnlyOneUnfrozenSessionPerProject(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	_, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-7", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	_, _, _, err = svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-8", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "再次编辑路线图"})
	require.ErrorIs(t, err, ErrInvalidState)
}

// A commit whose baseline moved under the session is refused.
func TestRoadmapCommitRefusesAStaleBaseline(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-9", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-9", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "改写后的路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)

	// Someone else moved the roadmap.
	appendRouteNode(t, svc, p, "roadmap-node-2", false, "ready")
	_, _, _, err = svc.CommitRoadmapEdit(context.Background(), actor, p.ID, session.ID, "roadmap-commit-9", RoadmapCommitInput{ExpectedBaselineVersion: session.Baseline.Version, Reason: "提交路线图编辑"})
	require.ErrorIs(t, err, ErrVersionConflict)
}

// The unfrozen session is not readable as a frozen version: the version
// sequence of a node only contains frozen versions.
func TestRoadmapUnfrozenProposalIsNotInTheVersionSequence(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	appendRouteNode(t, svc, p, "roadmap-node-1", false, "ready")
	raw, _, _, err := svc.BeginRoadmapEdit(context.Background(), actor, p.ID, "roadmap-begin-10", RoadmapEditBeginInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "编辑路线图"})
	require.NoError(t, err)
	session := roadmapDecodeSession(t, raw)
	_, _, _, err = svc.ProposeRoadmapNode(context.Background(), actor, p.ID, session.ID, "roadmap-propose-10", RoadmapNodeProposalInput{
		NodeID: "roadmap-node-1", Kind: RoadmapNodeProposalUpdate, Name: "未冻结的路线", Status: "ready", Reason: "改写节点文字",
	})
	require.NoError(t, err)

	entries, err := svc.ReadRoadmapNodeVersions(context.Background(), actor, p.ID, "roadmap-node-1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, int64(1), entries[0].Version)
	for _, entry := range entries {
		require.NotEqual(t, "未冻结的路线", entry.Name)
	}
	require.False(t, errors.Is(ErrInvalidState, ErrNotFound))
}
