package writingaction

import (
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// TargetsFor 返回这个动作允许的采纳目标。
//
// 采纳端只查这张许可位表，不按动作名分支。未知动作返回 ErrUnknownAction。
//
// 它由同一份包内规格表派生（内部用 PlanFor + 规范产出参数），不是第二份
// 数据源——与 Describe 同性质。许可位住在 actionSpecs 一张表里；这里若另写
// 一份许可表，A29 守护会红。
//
// 为什么不能只靠 PlanFor：采纳端只知道「动作」与「采纳目标」，不知道产出
// 参数，因此调不了 PlanFor；而 Describe(scope) 只覆盖某个目标范围下的动作
// （Describe(ScopeSection) 里没有 outline），拿不到全部动作的许可位。
func TargetsFor(a Action) ([]AdoptionTarget, error) {
	spec, ok := specFor(a)
	if !ok {
		return nil, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: unknown action %q", a), ErrUnknownAction)
	}
	plan, err := PlanFor(spec.action, spec.canonicalScope(), spec.canonicalParams())
	if err != nil {
		return nil, err
	}
	return slices.Clone(plan.Targets), nil
}
