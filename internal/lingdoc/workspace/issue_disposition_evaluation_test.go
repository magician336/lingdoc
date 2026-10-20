package workspace

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// 一条被「误报删除」的规则问题不再阻断当次求值，但影响任务仍 OPEN、项目仍不能稳定。
//
// 这条用例同时是「两个求值器同源」的证据：稳定化聚合器走的是**另一个**求值器
// （模板治理求值，TemplateGovernanceRuntime.Check），它读同一条规则判断。若两个
// 求值器各写一份「还阻不阻断」的判定，误报删除就只会在交付链的检查里生效，
// 稳定化仍会被那条已被误报删除的问题挡住——下面的断言会当场失败。
func TestDismissedRuleIssueStopsBlockingTheEvaluationButNotTheProject(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyChangeForGate(t, f, "dismiss-eval")
	// 交付链要带着处置读侧，否则读不到人工决策。
	inputs := &candidateadoption.DeliveryInputService{Reader: candidateadoption.NewSQLiteCandidateAdoptionStore(f.db), Authorizer: deliveryGuardProjectAuthorizer{f.projects}}
	releases := NewDeliveryReleaseServiceWithDispositions(inputs, f.runtime.DeliveryInputBuilder(), delivery.NewMemorySnapshotStore(), nil, f.projects)

	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	check, err := releases.Check(f.ctx, f.actor.UserID, p.ID, p.ProjectVersion)
	require.NoError(t, err)
	require.Equal(t, delivery.CheckBlocked, check.Status, "the applied change invalidates the chapter confirmation")

	blocking := []delivery.CheckIssue{}
	for _, issue := range check.Issues {
		if issue.Severity == delivery.SeverityBlocking {
			blocking = append(blocking, issue)
		}
	}
	require.NotEmpty(t, blocking, "the applied change must leave a blocking rule issue")
	require.True(t, hasRuleIssue(blocking, "chapter-confirmed"), "expected the stale confirmation to be reported: %+v", blocking)

	// 处置之前：规则阻断与任务阻断都在。
	before, _, _, err := f.projects.StabilizeRevision(f.ctx, f.actor, p.ID, change.ID, "dismiss-stabilize-before")
	require.NoError(t, err)
	var beforeResult core.StabilizationResult
	require.NoError(t, json.Unmarshal(before, &beforeResult))
	require.Equal(t, "BLOCKED", beforeResult.Status)
	require.True(t, hasBlocker(beforeResult.Blockers, "rule:chapter-confirmed"), "the rule must block before the dismissal: %+v", beforeResult.Blockers)
	require.True(t, hasBlocker(beforeResult.Blockers, "impact_task_open"), "the impact task must block: %+v", beforeResult.Blockers)

	// 误报删除：把每一条阻断级问题都删掉，它们就不再阻断当次求值。
	for index, issue := range blocking {
		_, _, err = releases.SetIssueDisposition(f.ctx, f.actor.UserID, p.ID, issue.ID, "dismiss-issue-00"+string(rune('1'+index)),
			IssueDispositionRequest{ExpectedProjectVersion: p.ProjectVersion, Action: delivery.DispositionDismiss, Reason: "经核对属于不适用项"})
		require.NoError(t, err, "dismissing %s", issue.ID)
	}

	after, err := releases.Check(f.ctx, f.actor.UserID, p.ID, p.ProjectVersion)
	require.NoError(t, err)
	require.Equal(t, delivery.CheckPassed, after.Status, "the dismissed findings must not block the evaluation: %+v", after)
	// 求值结果仍要写明「这条问题被误报删除了」这一事实。
	for _, issue := range after.Issues {
		require.NotNil(t, issue.Disposition, "the dismissed finding must stay visible with its disposition: %+v", issue)
	}

	// 处置之后：规则阻断消失，任务阻断仍在——项目仍不能稳定。
	raw, _, _, err := f.projects.StabilizeRevision(f.ctx, f.actor, p.ID, change.ID, "dismiss-stabilize-after")
	require.NoError(t, err)
	var result core.StabilizationResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "BLOCKED", result.Status)
	require.False(t, hasBlocker(result.Blockers, "rule:chapter-confirmed"), "the dismissed rule must not block any more: %+v", result.Blockers)
	require.True(t, hasBlocker(result.Blockers, "impact_task_open"), "the impact task must still block: %+v", result.Blockers)

	// 误报删除不关闭任何影响任务。
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	open := 0
	for _, task := range tasks {
		if task.Status != "DONE" {
			open++
		}
	}
	require.Greater(t, open, 0, "dismissing a rule issue must not close any impact task")
}

func hasBlocker(blockers []core.ImpactBlocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func hasRuleIssue(issues []delivery.CheckIssue, ruleID string) bool {
	for _, issue := range issues {
		if issue.RuleID == ruleID {
			return true
		}
	}
	return false
}
