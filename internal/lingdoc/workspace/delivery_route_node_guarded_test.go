package workspace

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// #324（guarded 生产 seam）：带必需路线节点的项目在变更集应用之后，交付检查
// （GuardedDeliveryRelease.Check——生产装配走的那条）必须仍能读回规则问题，
// 而不是被来源访问拒绝。
//
// 失配的形状：路线感知投影把必需路线节点读成空值，溯源快照记的却是原始规格里的
// 占位值。检查链上有两处拿 spec 去对快照：Build 里的 ValidateProjectProvenanceSnapshot
// 已按原始规格比对（PR #336）；守卫在求值之后还会拿**冻结输入**再验一次
// （AuthorizeFrozenProvenance）——它拿的如果是投影那份，对必需路线节点必然对不上。
// 本用例把这条 seam 钉住：两次校验必须同口径（都是原始规格），#319 的投影语义不回退。
func TestGuardedDeliveryCheckAfterApplyOnARequiredRouteNodeProjectReadsTheRuleIssues(t *testing.T) {
	f := newDeliveryGuardFixture(t, true)

	// 把项目换成演示模板 v2 副本（声明 route-r7 为必需路线节点），规格里放激活
	// 闸门逼出来的占位值——与 delivery_route_node_test.go 的 seed 同一套事实，
	// 只是这里跑在 guarded 装配与生产迁移表上。
	definition, err := lingdoctemplate.WithHashes(lingdoctemplate.DemoRouteTemplate())
	require.NoError(t, err)
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(`INSERT INTO lingdoc_project_template_copies(
		id,project_id,source_template_id,source_template_version,version,status,
		content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"guarded-route-copy", f.project.ID, definition.ID, definition.Version, 2, core.TemplateCopyBound,
		definition.ContentHash, definition.RulesetHash, string(raw), f.actor.UserID, time.Now().UTC()).Error)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET template_copy_version = 2, template_id = ?, template_version = ? WHERE id = ?",
		definition.ID, definition.Version, f.project.ID).Error)
	spec := map[string]string{
		"research_subject":                      "合成源派生对象",
		"research_goal":                         "合成软件目标",
		lingdoctemplate.DemoRequiredRouteNodeID: "合成路线占位值（真正的值由路线图节点提供）",
	}
	rawSpec, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_projects SET spec_json = ? WHERE id = ?", string(rawSpec), f.project.ID).Error)
	rebindConfirmationTemplateVersion(t, f.db, definition.Version)

	// 应用一个真实变更集改写项目状态（推进 spec_revision / context_revision /
	// project_version）——评论区收窄口径的时间轴：apply 之后检查仍要可用。
	change := applyChangeForGate(t, f, "guarded-route-apply")
	require.Equal(t, core.ChangeSetStatusApplied, change.Status)
	// 重新读项目拿 apply 后的 project_version:checks 带的就是它。
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)

	checked, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, p.ProjectVersion)
	require.NoError(t, err, "guarded POST checks after apply must not be source-denied")

	// 冻结链：apply 之后、稳定化之前，冻结被业务门禁（Owner 稳定化）拒绝——
	// 拒绝原因必须是稳定化要求，**不是**来源访问失败（修复前这里是 403）。
	// 必需路线节点缺失本身是持续阻断（#319），这条场景到不了稳定化成功，
	// 所以冻结链的完成线就是：门禁原因正确。
	_, _, freezeErr := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "guarded-route-freeze", p.ProjectVersion)
	require.ErrorIs(t, freezeErr, core.ErrRevisionNotStable,
		"an unstabilized revision must gate the freeze on stabilization, not on source denial")

	// 求值仍走路线感知投影：已声明但缺失的路线节点必须是阻断级问题（#319 不回退）。
	var route *delivery.CheckIssue
	for index := range checked.Issues {
		if checked.Issues[index].RuleID == lingdoctemplate.RuleRouteNodeRequired {
			route = &checked.Issues[index]
			break
		}
	}
	require.NotNil(t, route, "check issues = %+v, want a blocking route node issue", checked.Issues)
	require.Equal(t, delivery.SeverityBlocking, route.Severity)
	require.Equal(t, lingdoctemplate.DemoRequiredRouteNodeID, route.TargetID)
}
