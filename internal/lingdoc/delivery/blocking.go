package delivery

// The human actions a rule issue can carry. They are workflow decisions about
// one exact finding revision, not evaluator output:
//
//   - 「误报删除」(dismiss)：Owner 判定这条规则判断不该存在于当前规则版本与
//     目标版本下。它只解除该问题对**这一次求值**的阻断，不关闭影响任务、
//     不解除稳定化阻断、也不替代对象的重新确认。
//   - 「修正」(resolve)：这条判断成立且已按其结论处置。
//   - 「豁免」(waive)：接受风险；只适用于非阻断级。
//
// 术语见 GLOSSARY.md；决策见 docs/adr/0004-误报删除进入规则问题求值.md。
const (
	DispositionDismiss = "dismiss"
	DispositionResolve = "resolve"
	DispositionWaive   = "waive"
)

// BlockingIssues returns the rule issues that still block this evaluation.
//
// 这是**唯一**定义「这条规则问题是否还阻断」的地方：两个求值器
// （delivery.Evaluate 与 TemplateGovernanceRuntime.Check）与稳定化聚合器
// 都走它，谁都不许自己再写一份。
//
// 判定只有一条规则：阻断级问题被「误报删除」后不再阻断当次求值；
// 「修正」与「豁免」不解除阻断——判断依然成立，该由谁处置仍由谁处置。
func BlockingIssues(issues []CheckIssue) []CheckIssue {
	blocking := []CheckIssue{}
	for _, issue := range issues {
		if issue.Severity != SeverityBlocking {
			continue
		}
		if issue.Disposition != nil && issue.Disposition.Action == DispositionDismiss {
			continue
		}
		blocking = append(blocking, issue)
	}
	return blocking
}

// ApplyDispositions rewrites the evaluation status from the blocking set.
//
// 求值本身是纯函数（Evaluate 不读处置），因此「误报删除」是在应用层读出处置
// 之后才合入的。合入后：仍然有阻断级问题 → 仍是 blocked；一条都不剩 →
// passed。not_evaluated 不是「没有阻断」，而是「没有可求值的规则集」，它由
// 求值器决定，不受处置影响。
//
// 被误报删除的问题**仍留在 issues 里**并带着处置记录：求值结果必须写明
// 「这条问题被误报删除了」，导出口径不得静默消失。
func ApplyDispositions(result *CheckResult) {
	if result == nil || result.Status != CheckBlocked {
		return
	}
	if len(BlockingIssues(result.Issues)) == 0 {
		result.Status = CheckPassed
	}
}
