package workspacecore

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// #647：把 #619 留下的三条承重属性与两条时序断言钉进仓内自带测试。
//
// 这里只加测试，不改任何判定式。独立复核用反向变异证明过：在并入之前，
// 下面这些性质没有被仓内自带测试钉住 —— 把「无声明也豁免」改回去、把豁免
// 范围放宽到非资料对象、把审计闸门的调用点整段删掉，三处变异都能让整包测试
// 全绿通过。每条用例都在注释里说清它钉住的是哪一条性质，以及它的反向变异
// 是什么。
// TestStabilizationFailsClosedWhenTheChangeSetCannotProveItsOwnAdvance
// 钉住：fail closed。变更集读不出自己的声明（语义计划缺失）时，稳定化不得
// 把它自己那次推进当成「非漂移」而放行。
// 反向变异：把「无声明也豁免」改回去（impact_self_attributed_advance.go 的
// projectAssetVersionBlocker）。并入前这条变异整包仍全绿。
func TestStabilizationFailsClosedWhenTheChangeSetCannotProveItsOwnAdvance(t *testing.T) {
	f := newAssetLicenseFixture(t, "residual-fail-closed")
	closeBlockReleaseTask(t, f, "residual-fail-closed")

	// 声明没了：这份变更集再也证明不了任何事。
	require.NoError(t, f.svc.testDB().Exec(
		"DELETE FROM lingdoc_g3_records WHERE kind = ? AND record_key = ?",
		"semantic_plan", f.change.ID).Error,
		"remove the change set's own declaration")

	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "residual-fail-closed-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status,
		"a change set that cannot prove its own advance must not stabilize: %+v", result.Blockers)
	blocker := blockerFor(t, result, "impact_object_stale")
	require.Equal(t, f.asset, blocker.TargetID,
		"the material must be named as the object that is still stale: %+v", result.Blockers)
}

// TestStabilizationFailsClosedWhenTheSemanticPlanCannotBeRead
// 钉住：fail closed。声明读不出来（坏 JSON）时，稳定化必须失败，而且不得
// 留下一条「稳定化过」的审计。
// 反向变异：把「读不到声明也豁免」改回去。这条与上一条是同一性质的两个
// 入口：声明缺失与声明不可读。
func TestStabilizationFailsClosedWhenTheSemanticPlanCannotBeRead(t *testing.T) {
	f := newAssetLicenseFixture(t, "residual-unreadable-plan")
	closeBlockReleaseTask(t, f, "residual-unreadable-plan")

	require.NoError(t, f.svc.testDB().Exec(
		"UPDATE lingdoc_g3_records SET body_json = ? WHERE kind = ? AND record_key = ?",
		"{not-json", "semantic_plan", f.change.ID).Error,
		"corrupt the change set's own declaration")

	_, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "residual-unreadable-plan-stabilize")
	require.Error(t, err, "an unreadable declaration must not stabilize")
	require.Equal(t, int64(0), countAuditEvents(t, f.svc, "revision.stabilize"),
		"a refused stabilization must not leave a stabilization audit row")
}

// TestStabilizationKeepsTheSelfAttributedAdvanceOffTheOtherImpactedObjects
// 钉住：窄口径。自身推进的豁免只覆盖变更集自己动过的那份资料；研究主张
// （以及证据卡、路线节点）不在豁免范围内，它们被自己的 apply 推进之后仍然
// 判漂移、仍然阻断。
// 反向变异：把豁免范围放宽到非资料对象，并让通用分支也查豁免表
// （impact_self_attributed_advance.go 的 semanticSelfAdvanceTarget 与
// impact_service.go 的 impactConfirmationBlockers）。并入前这条变异整包仍全绿。
func TestStabilizationKeepsTheSelfAttributedAdvanceOffTheOtherImpactedObjects(t *testing.T) {
	f := newAgentFixture(t, false)
	claim := f.claim(t)
	change := semanticDraft(t, f.svc, f.owner, f.project, "residual-scope-draft", SemanticDelta{
		Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version,
		ProposedClaim: &SaveResearchClaimInput{Text: "合成主张：范围声明只覆盖资料本身", Scope: "合成范围", Status: "active", Confirmed: true}})
	semanticApply(t, f.svc, f.owner, f.project, change, "residual-scope")

	// 变更集自己的 apply 把主张推进了一版。豁免不覆盖主张，所以它仍然是漂移。
	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, change.ID, "residual-scope-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status,
		"an impacted claim that moved on must still block: %+v", result.Blockers)
	blocker := blockerFor(t, result, "impact_object_stale")
	require.Equal(t, claim.ID, blocker.TargetID,
		"the claim must be named as the object that is still stale: %+v", result.Blockers)
}

// TestStabilizationAuditGateRefusesARecordThatCannotSayWhoActed
// 钉住：审计完整性闸门。稳定化记录缺任何一项必需事实，都必须被这份判定
// 拒绝：一份说不出谁在动作的审计不是更弱的审计，而是不存在的审计。
func TestStabilizationAuditGateRefusesARecordThatCannotSayWhoActed(t *testing.T) {
	base := stabilizationAuditRecord{
		Executor:           stabilizationAuditExecutor{UserID: "owner", TenantID: 1, Role: "owner"},
		Authorization:      stabilizationAuditAuthorization{Method: "m", Capability: "manage", ProjectOwner: true, HumanDecisionGate: "passed"},
		Channel:            stabilizationAuditChannel{Class: "user_session", PrincipalType: "web_user", PrincipalID: "owner"},
		OccurredAt:         time.Now().UTC(),
		Target:             stabilizationAuditTarget{ProjectID: "p", ChangeSetID: "c", ContextRevision: 1},
		Decision:           stabilizationAuditDecision{Verdict: "STABILIZED", VerdictSource: "machine_evaluation"},
		AuditCorrelationID: "cid",
	}
	require.True(t, stabilizationAuditComplete(base), "a complete record must pass the gate")

	missing := map[string]func(*stabilizationAuditRecord){
		"executor.user_id":         func(r *stabilizationAuditRecord) { r.Executor.UserID = "" },
		"authorization.method":     func(r *stabilizationAuditRecord) { r.Authorization.Method = "" },
		"authorization.capability": func(r *stabilizationAuditRecord) { r.Authorization.Capability = "" },
		"human_decision_gate":      func(r *stabilizationAuditRecord) { r.Authorization.HumanDecisionGate = "" },
		"channel.class":            func(r *stabilizationAuditRecord) { r.Channel.Class = "" },
		"channel.principal_type":   func(r *stabilizationAuditRecord) { r.Channel.PrincipalType = "" },
		"occurred_at":              func(r *stabilizationAuditRecord) { r.OccurredAt = time.Time{} },
		"target.project_id":        func(r *stabilizationAuditRecord) { r.Target.ProjectID = "" },
		"target.change_set_id":     func(r *stabilizationAuditRecord) { r.Target.ChangeSetID = "" },
		"decision.verdict":         func(r *stabilizationAuditRecord) { r.Decision.Verdict = "" },
		"decision.verdict_source":  func(r *stabilizationAuditRecord) { r.Decision.VerdictSource = "" },
		"audit_correlation_id":     func(r *stabilizationAuditRecord) { r.AuditCorrelationID = "" },
	}
	for name, mutate := range missing {
		record := base
		mutate(&record)
		require.False(t, stabilizationAuditComplete(record),
			"the audit gate must refuse a record without %s", name)
	}
}

// TestStabilizationAuditGateIsCalledOnTheStabilizationPath
// 钉住：审计完整性闸门的存在性。稳定化这条路必须真的问一次
// stabilizationAuditComplete。
//
// 这条是结构守护而不是行为断言，理由必须写清：今天稳定化记录里的每一个
// 字段都由服务端构造为非空（执行主体来自认证上下文，授权与渠道来自常量与
// 上下文，时间与关联标识在构造时生成），所以公开 API 产不出一条不完整的
// 记录，闸门在公开 API 上不可触发。把整个调用点删掉之后，仓内没有任何行为
// 断言能观察到差别，这正是 #647 独立复核登记的那条残留。要让它可观察只有
// 两种办法：给记录构造留一个可注入的缺口（要改实现），或者用结构守护钉住
// 「这个闸门必须被调用」。本仓已有同风格的守护测试（例如
// change_set_status_ownership_test.go），这里沿用同一条路子。
func TestStabilizationAuditGateIsCalledOnTheStabilizationPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "read the package directory")
	fset := token.NewFileSet()
	called := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "parse %s", name)
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "StabilizeRevision" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if ok && ident.Name == "stabilizationAuditComplete" {
					called = true
				}
				return true
			})
		}
	}
	require.True(t, called,
		"the stabilization path must ask stabilizationAuditComplete whether the record can say who acted")
}

// TestStabilizationExemptsTheAdvanceOfAnAlreadyAppliedChangeSet
// 钉住：存量形态。声明写在 pin 时刻，apply 在后，稳定化更后；一份早已
// applied 的变更集仍然要能命中自身推进的豁免。存量许可变更集不因为「读不到
// 声明」而阻断，也不因为声明是历史的那一条而阻断。
func TestStabilizationExemptsTheAdvanceOfAnAlreadyAppliedChangeSet(t *testing.T) {
	f := newAssetLicenseFixture(t, "residual-legacy-shape")
	closeBlockReleaseTask(t, f, "residual-legacy-shape")

	// 声明仍然是 pin 时刻写下的那一条，没有被重写成当前版本。
	var plan []SemanticDelta
	require.NoError(t, f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		plan, err = tx.(semanticTransaction).SemanticPlan(f.project.ID, f.change.ID)
		return err
	}))
	require.Len(t, plan, 1, "the declaration must still be the one written at pin time")
	require.Equal(t, int64(1), plan[0].ExpectedVersion,
		"the persisted declaration must still carry the version it was pinned at")

	result, err := runStabilization(t, f.svc, f.owner, f.project.ID, f.change.ID, "residual-legacy-shape-stabilize")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", result.Status,
		"an already-applied change set must still be exempt for the step its own apply produced: %+v", result.Blockers)
}

// TestStabilizationTreatsTheTwoMaterialDriftWindowsDifferently
// 钉住：材料被推进的两个窗口必须是两种不同的拒绝形态，且都是 fail closed。
// 第一个窗口是 pin 与 apply 之间：apply 自己被拒（版本冲突），变更集不落到
// applied，稳定化随后也被拒。第二个窗口是 apply 之后：变更集已经 applied，
// 稳定化必须把材料判成漂移并阻断。
// 两条都不是放行；把它们分开钉住，是为了防止有人把其中一个窗口放宽成放行。
func TestStabilizationTreatsTheTwoMaterialDriftWindowsDifferently(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	f.svc.SetImpactRuleChecker(cleanRuleChecker{})
	change := semanticDraft(t, f.svc, f.owner, f.project, "residual-window-one-draft", SemanticDelta{
		Kind: "ProjectAssetScope", TargetID: "asset-1", ExpectedVersion: 1,
		ProposedAssetPolicy: &ProjectAssetPolicy{Scope: "personal", Purpose: "仅本人复核", ModelUseAllowed: false}})
	_, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, change.ID, "residual-window-one-assess")
	require.NoError(t, err)

	// 第一个窗口：材料在 pin 与 apply 之间被推进。
	advanceAssetRevision(t, f.svc, f.project.ID, "asset-1")
	_, _, _, err = f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, change.ID, "residual-window-one-apply")
	require.Error(t, err, "an apply over a material that moved on must be refused")
	require.True(t, errors.Is(err, ErrVersionConflict), "the refusal must be a version conflict, got %v", err)
	require.Equal(t, "assessed", changeSetStatus(t, f.svc, f.owner, f.project.ID, change.ID),
		"a refused apply must not leave the change set applied")
	_, err = runStabilization(t, f.svc, f.owner, f.project.ID, change.ID, "residual-window-one-stabilize")
	require.Error(t, err, "a change set that never applied cannot be stabilized")
	require.True(t, errors.Is(err, ErrVersionConflict), "the refusal must be a version conflict, got %v", err)

	// 第二个窗口：材料在 apply 之后被推进。
	g := newAssetLicenseFixture(t, "residual-window-two")
	closeBlockReleaseTask(t, g, "residual-window-two")
	advanceAssetRevision(t, g.svc, g.project.ID, g.asset)
	result, err := runStabilization(t, g.svc, g.owner, g.project.ID, g.change.ID, "residual-window-two-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status,
		"a material that moved on after the apply must still block: %+v", result.Blockers)
	blocker := blockerFor(t, result, "impact_object_stale")
	require.Equal(t, g.asset, blocker.TargetID,
		"the material must be named as the object that is still stale: %+v", result.Blockers)
}
