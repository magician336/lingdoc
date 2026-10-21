package demoroute

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "modernc.org/sqlite" // Pure Go test driver; production retains the existing SQLite driver.
)

// The tenant membership table belongs to the platform, not to the LingDoc
// migrations, so the fixture creates it the same way the workspace fixtures do.
const tenantMembersDDL = "CREATE TABLE IF NOT EXISTS tenant_members ( role TEXT NOT NULL DEFAULT 'owner',tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, status TEXT NOT NULL, deleted_at DATETIME, PRIMARY KEY (tenant_id, user_id))"

// fixture is one project on a migrated SQLite database, driven through the
// real application service. The nodes and the reasons carry a run label only:
// they are synthetic material, not research content.
type fixture struct {
	svc     *core.Service
	actor   core.Actor
	db      *gorm.DB
	project core.Project
}

func newFixture(t *testing.T, templateVersion string) *fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite",
		DSN: filepath.Join(t.TempDir(), "demoroute.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open the fixture database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("read the fixture database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "sqlite", "*_lingdoc_*.up.sql"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("find the production SQLite migrations: count=%d error=%v", len(matches), err)
	}
	sort.Strings(matches)
	for _, file := range matches {
		body, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", filepath.Base(file), readErr)
		}
		if execErr := db.Exec(string(body)).Error; execErr != nil {
			t.Fatalf("apply migration %s: %v", filepath.Base(file), execErr)
		}
	}
	if err := db.Exec(tenantMembersDDL).Error; err != nil {
		t.Fatalf("create the tenant membership table: %v", err)
	}
	actor := core.Actor{TenantID: 8311, UserID: "owner"}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status, role) VALUES (?, ?, 'active', 'owner')", actor.TenantID, actor.UserID).Error; err != nil {
		t.Fatalf("seed the tenant membership: %v", err)
	}
	f := &fixture{svc: core.NewService(core.NewGORMRepository(db)), actor: actor, db: db}
	f.project = f.createAndActivate(t, templateVersion)
	return f
}

// createAndActivate builds the project the way the demo console does: create
// it on the demo template, write the spec, activate it.
func (f *fixture) createAndActivate(t *testing.T, templateVersion string) core.Project {
	t.Helper()
	ctx := context.Background()
	raw, _, _, err := f.svc.CreateProject(ctx, f.actor, "demoroute-create", core.CreateProjectInput{
		Name: "合成演示项目", TemplateID: lingdoctemplate.DemoTemplateID, TemplateVersion: templateVersion,
	})
	if err != nil {
		t.Fatalf("create the project: %v", err)
	}
	project := decode[core.Project](t, raw)
	// The template's required fields are the spec's vocabulary, and the
	// activation gate requires every one of them to carry a value
	// (workspacecore/service_facade.go). The route node id is one of them, so
	// the spec carries a placeholder for it.
	//
	// The placeholder is not what the template evaluates. The route aware
	// evaluation lets the roadmap speak for a route node id: it carries the
	// node's value while the node is ready and current, and no value at all
	// while the node is missing, not reviewed, or behind (issue #319). That is
	// what makes the three preconditions of AC-G3-08 observable, and the
	// placeholder left in the spec on purpose shows that the server does not
	// take the spec's word for a route node.
	raw, _, _, err = f.svc.SaveSpec(ctx, f.actor, project.ID, "demoroute-spec", core.SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields: map[string]string{
			"research_subject": "合成研究对象", "research_goal": "合成研究目标",
			lingdoctemplate.DemoRequiredRouteNodeID: "合成路线 R7（占位，由路线图节点提供）",
		},
	})
	if err != nil {
		t.Fatalf("write the spec: %v", err)
	}
	project = decode[core.Project](t, raw)
	raw, _, _, err = f.svc.ActivateProject(ctx, f.actor, project.ID, "demoroute-activate", core.ActivateProjectInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
	})
	if err != nil {
		t.Fatalf("activate the project: %v", err)
	}
	return decode[core.Project](t, raw)
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %T: %v", value, err)
	}
	return value
}

// current reads the project back from the service.
func (f *fixture) current(t *testing.T) core.Project {
	t.Helper()
	project, err := f.svc.GetProject(context.Background(), f.actor, f.project.ID)
	if err != nil {
		t.Fatalf("read the project: %v", err)
	}
	return project
}

// evaluationFields are the field values the server hands to the template
// evaluator: the project spec plus the route nodes, where a route node only
// carries a value while it is ready and every version it pinned is current.
func (f *fixture) evaluationFields(t *testing.T, project core.Project) map[string]string {
	t.Helper()
	fields, err := core.RouteEvaluationFields(core.NewTransactionAdapter(f.db), project)
	if err != nil {
		t.Fatalf("read the route evaluation fields: %v", err)
	}
	return fields
}

// routeIssues are the evaluations the template produces for the current
// project state.
func (f *fixture) routeIssues(t *testing.T, project core.Project) []lingdoctemplate.Evaluation {
	t.Helper()
	input := lingdoctemplate.EvaluationInput{
		ProjectID: project.ID, TargetVersion: strconv.FormatInt(project.ProjectVersion, 10),
		Fields: f.evaluationFields(t, project),
	}
	evaluated, err := lingdoctemplate.EvaluateScene(project.TemplateCopy.Definition, "preflight", input)
	if err != nil {
		t.Fatalf("evaluate the template: %v", err)
	}
	return evaluated
}

// hasBlockingRouteIssue reports whether the required route node produced a
// blocking validation issue.
func hasBlockingRouteIssue(evaluated []lingdoctemplate.Evaluation, nodeID string) bool {
	for _, result := range evaluated {
		if result.TargetRef == "field/"+nodeID && result.Status == lingdoctemplate.EvaluationIssue && result.Severity == lingdoctemplate.SeverityBlocking {
			return true
		}
	}
	return false
}

// addRouteNode registers one route node through the roadmap editing session and
// freezes it. A new node is a semantic change, so the session produces a
// ChangeSet that has to be assessed and applied before the node reaches the
// frozen roadmap.
func (f *fixture) addRouteNode(t *testing.T, nodeID, name, status string, inputs []core.ObjectVersionRef) core.RoadmapNodeView {
	t.Helper()
	project := f.current(t)
	session := f.beginSession(t, "demoroute-begin-"+nodeID)
	_, _, _, err := f.svc.ProposeRoadmapNode(context.Background(), f.actor, project.ID, session.ID, "demoroute-propose-"+nodeID, core.RoadmapNodeProposalInput{
		NodeID: nodeID, Kind: core.RoadmapNodeProposalAdd, Name: name, Status: status, Inputs: inputs, Targets: []core.ObjectVersionRef{},
		Reason: "合成材料：登记路线节点 " + nodeID,
	})
	if err != nil {
		t.Fatalf("propose the route node %s: %v", nodeID, err)
	}
	commit := f.commitSession(t, session)
	if commit.ChangeSetID == "" {
		t.Fatalf("adding a route node must produce a change set: %+v", commit)
	}
	if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.actor, project.ID, commit.ChangeSetID, "demoroute-assess-"+nodeID); err != nil {
		t.Fatalf("assess the change set: %v", err)
	}
	if _, _, _, err := f.svc.ApplyChangeSet(context.Background(), f.actor, project.ID, commit.ChangeSetID, "demoroute-apply-"+nodeID); err != nil {
		t.Fatalf("apply the change set: %v", err)
	}
	return f.nodeView(t, nodeID)
}

// updateRouteNode rewrites a frozen node without touching its prerequisite
// relations: that is a text edit, so the new version is frozen right away.
func (f *fixture) updateRouteNode(t *testing.T, nodeID, name, status string) {
	t.Helper()
	project := f.current(t)
	session := f.beginSession(t, "demoroute-begin-update-"+nodeID)
	_, _, _, err := f.svc.ProposeRoadmapNode(context.Background(), f.actor, project.ID, session.ID, "demoroute-propose-update-"+nodeID, core.RoadmapNodeProposalInput{
		NodeID: nodeID, Kind: core.RoadmapNodeProposalUpdate, Name: name, Status: status, Inputs: []core.ObjectVersionRef{}, Targets: []core.ObjectVersionRef{},
		Reason: "合成材料：改写路线节点 " + nodeID,
	})
	if err != nil {
		t.Fatalf("propose the route node update %s: %v", nodeID, err)
	}
	commit := f.commitSession(t, session)
	if commit.ChangeSetID != "" {
		t.Fatalf("a text edit must not produce a change set: %+v", commit)
	}
}

func (f *fixture) beginSession(t *testing.T, key string) core.RoadmapEditSession {
	t.Helper()
	project := f.current(t)
	raw, _, _, err := f.svc.BeginRoadmapEdit(context.Background(), f.actor, project.ID, key, core.RoadmapEditBeginInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "合成材料：编辑路线图",
	})
	if err != nil {
		t.Fatalf("begin the roadmap edit session: %v", err)
	}
	return decode[core.RoadmapEditSession](t, raw)
}

func (f *fixture) commitSession(t *testing.T, session core.RoadmapEditSession) core.RoadmapCommitResult {
	t.Helper()
	raw, _, _, err := f.svc.CommitRoadmapEdit(context.Background(), f.actor, f.project.ID, session.ID, "demoroute-commit-"+session.ID, core.RoadmapCommitInput{
		ExpectedBaselineVersion: session.Baseline.Version, Reason: "合成材料：提交路线图编辑",
	})
	if err != nil {
		t.Fatalf("commit the roadmap edit session: %v", err)
	}
	return decode[core.RoadmapCommitResult](t, raw)
}

// nodeView reads one node back from the frozen roadmap.
func (f *fixture) nodeView(t *testing.T, nodeID string) core.RoadmapNodeView {
	t.Helper()
	baseline, err := f.svc.ReadRoadmap(context.Background(), f.actor, f.project.ID)
	if err != nil {
		t.Fatalf("read the roadmap: %v", err)
	}
	for _, node := range baseline.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("route node %q is not in the frozen roadmap: %+v", nodeID, baseline.Nodes)
	return core.RoadmapNodeView{}
}

// The template declaration is the only owner of the required flag.
//
// The flag the server publishes is computed from the template rules, so the
// node reads back as required without anybody asking for it, and a client that
// disagrees with the rules is refused.
func TestDemoRouteTemplateMakesTheRouteNodeRequired(t *testing.T) {
	// A client that states the flag wrongly is refused: the rules own it.
	// The probe runs on its own project because a refused proposal leaves its
	// editing session open, and a project may only have one open session.
	probe := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
	probeSession := probe.beginSession(t, "demoroute-begin-downgrade")
	claimed := false
	_, _, _, err := probe.svc.ProposeRoadmapNode(context.Background(), probe.actor, probe.project.ID, probeSession.ID, "demoroute-propose-downgrade", core.RoadmapNodeProposalInput{
		NodeID: lingdoctemplate.DemoRequiredRouteNodeID, Kind: core.RoadmapNodeProposalAdd, Name: "合成路线 R7", Status: "ready",
		Required: &claimed, Reason: "合成材料：试图把必需节点降级",
	})
	if err == nil {
		t.Fatal("a client must not downgrade a route node the template rules require")
	}

	f := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
	project := f.current(t)
	if project.TemplateCopy == nil || project.TemplateCopy.Definition.Version != lingdoctemplate.DemoRouteTemplateVersion {
		t.Fatalf("the project copy must be bound to the route template version: %+v", project.TemplateCopy)
	}

	node := f.addRouteNode(t, lingdoctemplate.DemoRequiredRouteNodeID, "合成路线 R7", "ready", nil)
	t.Logf("required route node read back: id=%s required=%v state=%s status=%s version=%d name=%s",
		node.ID, node.Required, node.State, node.Status, node.Version, node.Name)
	if !node.Required {
		t.Fatalf("the server must compute the node as required: %+v", node)
	}
	if node.Status == "retired" {
		t.Fatalf("the required node must stay editable: %+v", node)
	}
	if node.State != core.RoadmapNodeStateCurrent {
		t.Fatalf("a ready node without pinned versions must be current: %+v", node)
	}
	if node.Version != 1 {
		t.Fatalf("the frozen node version = %d, want 1: %+v", node.Version, node)
	}

	// A current required route node satisfies the template: no issue is raised
	// for it.
	if hasBlockingRouteIssue(f.routeIssues(t, f.current(t)), lingdoctemplate.DemoRequiredRouteNodeID) {
		t.Fatal("a current required route node must not raise a blocking issue")
	}
}

// The three preconditions of AC-G3-08: a required route node that is missing,
// one that has not been reviewed, and one that is behind its baseline.
//
// All three leave the required field without a value, which is what the
// template evaluator turns into a blocking validation issue. The missing state
// reaches the evaluation the same way as the other two, because the route
// aware evaluation lets the roadmap speak for a route node id that the
// template declares but the roadmap does not hold yet (issue #319).
func TestDemoRouteTemplateRequiredRouteNodePreconditions(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		// The required node does not exist yet: the roadmap has no node with
		// that identity.
		f := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
		baseline, err := f.svc.ReadRoadmap(context.Background(), f.actor, f.project.ID)
		if err != nil {
			t.Fatalf("read the roadmap: %v", err)
		}
		for _, node := range baseline.Nodes {
			if node.ID == lingdoctemplate.DemoRequiredRouteNodeID {
				t.Fatalf("the required route node must not exist yet: %+v", node)
			}
		}
		// FIXED (issue #319): the missing state now reaches the template
		// evaluation and produces the same blocking issue as the other two
		// preconditions.
		//
		// The gap was that the route aware evaluation started from the spec
		// and only overwrote the value of the route nodes that exist
		// (workspacecore/semantic_delta.go, RouteEvaluationFields), so the
		// activation placeholder under the route node id survived and stood
		// in for a node the roadmap does not hold. The route aware evaluation
		// now lets the roadmap speak for every route node id the template
		// declares as required: a declared node that the roadmap does not hold
		// has no value, so the presence rule of the template reports it.
		evaluated := f.routeIssues(t, f.current(t))
		blocking := hasBlockingRouteIssue(evaluated, lingdoctemplate.DemoRequiredRouteNodeID)
		project := f.current(t)
		t.Logf("missing precondition: id=%s node=absent in the frozen roadmap required-by-the-rules=%v state=absent status=absent version=absent blocking=%v",
			lingdoctemplate.DemoRequiredRouteNodeID,
			slices.Contains(project.TemplateCopy.Definition.RequiredFields, lingdoctemplate.DemoRequiredRouteNodeID),
			blocking)
		if !blocking {
			t.Fatalf("a missing required route node must produce a blocking issue: %+v", evaluated)
		}
	})

	t.Run("pending_review", func(t *testing.T) {
		f := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
		node := f.addRouteNode(t, lingdoctemplate.DemoRequiredRouteNodeID, "合成路线 R7", "pending_review", nil)
		t.Logf("pending_review precondition: id=%s required=%v state=%s status=%s version=%d",
			node.ID, node.Required, node.State, node.Status, node.Version)
		if node.State != core.RoadmapNodeStatePendingReview {
			t.Fatalf("node state = %q, want %q: %+v", node.State, core.RoadmapNodeStatePendingReview, node)
		}
		evaluated := f.routeIssues(t, f.current(t))
		if !hasBlockingRouteIssue(evaluated, lingdoctemplate.DemoRequiredRouteNodeID) {
			t.Fatalf("a pending-review required route node must produce a blocking issue: %+v", evaluated)
		}
	})

	t.Run("stale", func(t *testing.T) {
		f := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
		// The required node pins a prerequisite version, and the prerequisite
		// moves on afterwards: the required node is then behind its baseline.
		f.addRouteNode(t, "demoroute-prerequisite", "合成前置节点", "ready", nil)
		pinned := []core.ObjectVersionRef{{Kind: "RouteNode", ID: "demoroute-prerequisite", Version: "1"}}
		node := f.addRouteNode(t, lingdoctemplate.DemoRequiredRouteNodeID, "合成路线 R7", "ready", pinned)
		if node.State != core.RoadmapNodeStateCurrent {
			t.Fatalf("node state = %q, want %q while the pinned version is current: %+v", node.State, core.RoadmapNodeStateCurrent, node)
		}
		f.updateRouteNode(t, "demoroute-prerequisite", "合成前置节点 第二版", "ready")
		stale := f.nodeView(t, lingdoctemplate.DemoRequiredRouteNodeID)
		t.Logf("stale precondition: id=%s required=%v state=%s status=%s version=%d inputs=%+v",
			stale.ID, stale.Required, stale.State, stale.Status, stale.Version, stale.Inputs)
		if stale.State != core.RoadmapNodeStateStale {
			t.Fatalf("node state = %q, want %q: %+v", stale.State, core.RoadmapNodeStateStale, stale)
		}
		evaluated := f.routeIssues(t, f.current(t))
		if !hasBlockingRouteIssue(evaluated, lingdoctemplate.DemoRequiredRouteNodeID) {
			t.Fatalf("a required route node behind its baseline must produce a blocking issue: %+v", evaluated)
		}
	})
}

// A required route node that is not current keeps the revision from being
// stabilized: the stabilization gate reports the route blocker and refuses.
func TestDemoRouteTemplateRequiredRouteNodeBlocksStabilization(t *testing.T) {
	f := newFixture(t, lingdoctemplate.DemoRouteTemplateVersion)
	f.addRouteNode(t, lingdoctemplate.DemoRequiredRouteNodeID, "合成路线 R7", "pending_review", nil)

	project := f.current(t)
	blockers, err := core.RouteStabilizationBlockers(core.NewTransactionAdapter(f.db), project)
	if err != nil {
		t.Fatalf("read the route stabilization blockers: %v", err)
	}
	found := false
	for _, blocker := range blockers {
		if blocker.Code == "required_route_not_current" && blocker.TargetID == lingdoctemplate.DemoRequiredRouteNodeID {
			found = true
		}
	}
	if !found {
		t.Fatalf("a required route node that is not current must block the stabilization: %+v", blockers)
	}
	t.Logf("route stabilization blockers: %+v", blockers)

	raw, _, _, err := f.svc.StabilizeRevision(context.Background(), f.actor, project.ID, "demoroute-stabilize", "demoroute-stabilize")
	if err == nil {
		result := decode[core.StabilizationResult](t, raw)
		if result.Status == "STABILIZED" {
			t.Fatalf("a required route node that is not current must not produce a STABILIZED revision: %+v", result)
		}
	}
	if err := f.svc.AssertRevisionStabilized(context.Background(), f.actor, project.ID); err == nil {
		t.Fatal("the release assertion accepted a revision whose required route node is not current")
	}

	// The release gate is closed for the same reason: the template check of
	// the frozen input reports the required route node as a blocking issue.
	if project.DeliveryStatus != "NOT_READY" {
		t.Fatalf("delivery status = %q, want NOT_READY", project.DeliveryStatus)
	}
	check, err := delivery.EvaluateTemplate(delivery.DeliveryInput{
		ProjectID: project.ID, ProjectVersion: int(project.ProjectVersion), SpecRevision: int(project.SpecRevision),
		Template: project.TemplateCopy.Definition, Spec: f.evaluationFields(t, project),
		TemplateCopyID: project.TemplateCopy.ID, TemplateCopyVersion: project.TemplateCopy.Version,
		TemplateCopyContentHash: project.TemplateCopy.ContentHash,
	})
	if err != nil {
		t.Fatalf("evaluate the delivery template check: %v", err)
	}
	if check.Status != delivery.TemplateCheckBlocked {
		t.Fatalf("delivery template check = %q, want %q: %+v", check.Status, delivery.TemplateCheckBlocked, check)
	}
	blocked := false
	for _, evaluation := range check.Evaluations {
		if evaluation.TargetRef == "field/"+lingdoctemplate.DemoRequiredRouteNodeID && evaluation.Severity == lingdoctemplate.SeverityBlocking {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("the delivery check must report the required route node: %+v", check.Evaluations)
	}
	t.Logf("delivery template check: status=%q delivery_status=%q", check.Status, project.DeliveryStatus)
	for _, evaluation := range check.Evaluations {
		if evaluation.TargetRef == "field/"+lingdoctemplate.DemoRequiredRouteNodeID {
			t.Logf("delivery evaluation: rule=%s target=%s severity=%s status=%s message=%s",
				evaluation.RuleID, evaluation.TargetRef, evaluation.Severity, evaluation.Status, evaluation.Message)
		}
	}
}
