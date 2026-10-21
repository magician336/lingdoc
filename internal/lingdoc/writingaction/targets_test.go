package writingaction_test

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// TargetsFor 是采纳端唯一的许可位查询入口。
//
// 采纳端只知道「动作」与「采纳目标」，不知道产出参数，所以调不了 PlanFor；
// 而 Describe 又只覆盖某个目标范围下的动作（Describe(ScopeSection) 里没有
// outline）。这个入口补上这个缺口，但**不新建第二份许可表**：它必须与
// PlanFor 解析出来的 Targets 逐字一致。
func TestTargetsForMatchesThePlanOfTheSameAction(t *testing.T) {
	actions := []writingaction.Action{
		writingaction.ActionOutline, writingaction.ActionGenerate, writingaction.ActionContinue,
		writingaction.ActionRewrite, writingaction.ActionCompress, writingaction.ActionExpand,
		writingaction.ActionTransform, writingaction.ActionReview,
	}
	for _, action := range actions {
		targets, err := writingaction.TargetsFor(action)
		if err != nil {
			t.Fatalf("TargetsFor(%q) 返回错误：%v", action, err)
		}
		if len(targets) == 0 && action != writingaction.ActionReview {
			t.Errorf("TargetsFor(%q) 没有给出任何采纳目标", action)
		}
		// 许可位必须与同一份规格表解析出来的结果一致：多写一份表就会漂。
		for _, scope := range []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection} {
			plan, planErr := writingaction.PlanFor(action, scope, canonicalParamsFor(action))
			if planErr != nil {
				continue
			}
			if len(plan.Targets) != len(targets) {
				t.Fatalf("TargetsFor(%q) = %v，与 PlanFor 的 %v 不一致", action, targets, plan.Targets)
			}
			for i := range plan.Targets {
				if plan.Targets[i] != targets[i] {
					t.Fatalf("TargetsFor(%q) = %v，与 PlanFor 的 %v 不一致", action, targets, plan.Targets)
				}
			}
		}
	}
}

func TestTargetsForRefusesAnUnknownAction(t *testing.T) {
	_, err := writingaction.TargetsFor(writingaction.Action("not-an-action"))
	if !errors.Is(err, writingaction.ErrUnknownAction) {
		t.Fatalf("未知动作的错误 = %v，期望 ErrUnknownAction", err)
	}
}

// TestTargetsForTellsTheOutlineApartFromTheProseActions 钉住许可位表的形状：
// 提纲只写工作副本，语言评审没有可采纳物。
func TestTargetsForTellsTheOutlineApartFromTheProseActions(t *testing.T) {
	outline, err := writingaction.TargetsFor(writingaction.ActionOutline)
	if err != nil {
		t.Fatalf("TargetsFor(outline) 返回错误：%v", err)
	}
	if len(outline) != 1 || outline[0] != writingaction.TargetWorkingCopyWhole {
		t.Errorf("提纲的许可位 = %v，期望只有工作副本整段", outline)
	}
	review, err := writingaction.TargetsFor(writingaction.ActionReview)
	if err != nil {
		t.Fatalf("TargetsFor(review) 返回错误：%v", err)
	}
	if len(review) != 0 {
		t.Errorf("语言评审的许可位 = %v，期望空集", review)
	}
	rewrite, err := writingaction.TargetsFor(writingaction.ActionRewrite)
	if err != nil {
		t.Fatalf("TargetsFor(rewrite) 返回错误：%v", err)
	}
	if len(rewrite) != 3 {
		t.Errorf("改写的许可位 = %v，期望三个目标全开", rewrite)
	}
}

// canonicalParamsFor 给一个动作造一份合法的产出参数。
//
// 参数取值抄自词汇包本身：动作的产出参数维度由词汇包拥有，测试不另抄一份
// 取值表。
func canonicalParamsFor(action writingaction.Action) writingaction.OutputParams {
	params := writingaction.OutputParams{}
	switch action {
	case writingaction.ActionGenerate, writingaction.ActionContinue, writingaction.ActionRewrite:
		params.Length = writingaction.LengthShort
		params.Tone = writingaction.ToneNeutral
	case writingaction.ActionCompress, writingaction.ActionExpand:
		params.Ratio = 50
		params.Tone = writingaction.ToneNeutral
	case writingaction.ActionTransform:
		params.Length = writingaction.LengthShort
		language := writingaction.LangZH
		params.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate, TargetLanguage: &language}
	}
	return params
}
