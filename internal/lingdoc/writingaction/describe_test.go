package writingaction_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// TestDescribeListsEveryActionForItsScope 覆盖动作表按范围给出可用动作。
func TestDescribeListsEveryActionForItsScope(t *testing.T) {
	cases := []struct {
		scope    writingaction.Scope
		expected []writingaction.Action
	}{
		{
			scope: writingaction.ScopeChapter,
			expected: []writingaction.Action{
				writingaction.ActionOutline,
				writingaction.ActionGenerate,
				writingaction.ActionTransform,
				writingaction.ActionReview,
			},
		},
		{
			scope: writingaction.ScopeSection,
			expected: []writingaction.Action{
				writingaction.ActionGenerate,
				writingaction.ActionContinue,
				writingaction.ActionRewrite,
				writingaction.ActionCompress,
				writingaction.ActionExpand,
				writingaction.ActionTransform,
				writingaction.ActionReview,
			},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.scope), func(t *testing.T) {
			descriptions := writingaction.Describe(tc.scope)
			actions := make([]writingaction.Action, 0, len(descriptions))
			for _, description := range descriptions {
				actions = append(actions, description.Action)
			}
			if !slices.Equal(actions, tc.expected) {
				t.Fatalf("Describe(%q) 的动作 = %v，期望 %v", tc.scope, actions, tc.expected)
			}
			for _, description := range descriptions {
				if !slices.Contains(description.Scopes, tc.scope) {
					t.Errorf("%q 的允许范围 = %v，不含 %q", description.Action, description.Scopes, tc.scope)
				}
			}
		})
	}
}

// TestDescribeAgreesWithPlanFor 钉住描述与执行计划的一致：描述不是第二份数据
// 源，它说的产出形状与采纳目标必须与 PlanFor 解析出来的一致。
func TestDescribeAgreesWithPlanFor(t *testing.T) {
	for _, scope := range []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection} {
		for _, description := range writingaction.Describe(scope) {
			plan, err := writingaction.PlanFor(description.Action, scope, validParams(description.Action))
			if err != nil {
				t.Fatalf("PlanFor(%q, %q) 报错：%v", description.Action, scope, err)
			}
			if description.Produce != plan.Produce {
				t.Errorf("%q 的产出形状 = %q，PlanFor 给的是 %q", description.Action, description.Produce, plan.Produce)
			}
			if !slices.Equal(description.Targets, plan.Targets) {
				t.Errorf("%q 的采纳目标 = %v，PlanFor 给的是 %v", description.Action, description.Targets, plan.Targets)
			}
		}
	}
}

// TestDescribeReportsAllowedOutputParams 覆盖动作面板要显示的可用产出参数。
func TestDescribeReportsAllowedOutputParams(t *testing.T) {
	cases := []struct {
		action   writingaction.Action
		scope    writingaction.Scope
		expected []string
	}{
		{writingaction.ActionOutline, writingaction.ScopeChapter, nil},
		{writingaction.ActionGenerate, writingaction.ScopeChapter, []string{"length", "tone"}},
		{writingaction.ActionGenerate, writingaction.ScopeSection, []string{"length", "tone"}},
		{writingaction.ActionContinue, writingaction.ScopeSection, []string{"length", "tone"}},
		{writingaction.ActionRewrite, writingaction.ScopeSection, []string{"length", "tone"}},
		{writingaction.ActionCompress, writingaction.ScopeSection, []string{"ratio", "tone"}},
		{writingaction.ActionExpand, writingaction.ScopeSection, []string{"ratio", "tone"}},
		{writingaction.ActionTransform, writingaction.ScopeChapter, []string{"length", "transform_mode", "target_language", "audience"}},
		{writingaction.ActionTransform, writingaction.ScopeSection, []string{"length", "transform_mode", "target_language", "audience"}},
		{writingaction.ActionReview, writingaction.ScopeChapter, nil},
		{writingaction.ActionReview, writingaction.ScopeSection, nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.action)+"/"+string(tc.scope), func(t *testing.T) {
			var found *writingaction.Description
			for i := range writingaction.Describe(tc.scope) {
				description := writingaction.Describe(tc.scope)[i]
				if description.Action == tc.action {
					found = &description
					break
				}
			}
			if found == nil {
				t.Fatalf("Describe(%q) 里没有 %q", tc.scope, tc.action)
			}
			names := make([]string, 0, len(found.OutputParams))
			for _, option := range found.OutputParams {
				names = append(names, option.Name)
			}
			if !slices.Equal(names, tc.expected) {
				t.Errorf("%q 的可用产出参数 = %v，期望 %v", tc.action, names, tc.expected)
			}
		})
	}
}

// TestDescribeCarriesClosedValueSets 覆盖产出参数的取值集在描述里是闭合的。
func TestDescribeCarriesClosedValueSets(t *testing.T) {
	for _, scope := range []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection} {
		for _, description := range writingaction.Describe(scope) {
			for _, option := range description.OutputParams {
				switch option.Name {
				case "length":
					if !slices.Equal(option.Values, []string{"short", "medium", "long"}) {
						t.Errorf("%q 的长度取值 = %v", description.Action, option.Values)
					}
				case "tone":
					if !slices.Equal(option.Values, []string{"neutral", "formal", "academic", "plain"}) {
						t.Errorf("%q 的语气取值 = %v", description.Action, option.Values)
					}
				case "ratio":
					if option.Min != 10 || option.Max != 90 {
						t.Errorf("%q 的比例区间 = %d..%d，期望 10..90", description.Action, option.Min, option.Max)
					}
				case "transform_mode":
					if !slices.Equal(option.Values, []string{"translate", "explain"}) {
						t.Errorf("%q 的转写模式取值 = %v", description.Action, option.Values)
					}
				case "target_language":
					if !slices.Equal(option.Values, []string{"zh", "en", "ja", "de", "fr"}) {
						t.Errorf("%q 的目标语言取值 = %v", description.Action, option.Values)
					}
					if option.AppliesTo != "translate" {
						t.Errorf("%q 的目标语言适用于 %q，期望 translate", description.Action, option.AppliesTo)
					}
				case "audience":
					if !slices.Equal(option.Values, []string{"peer", "reviewer", "novice"}) {
						t.Errorf("%q 的读者层次取值 = %v", description.Action, option.Values)
					}
					if option.AppliesTo != "explain" {
						t.Errorf("%q 的读者层次适用于 %q，期望 explain", description.Action, option.AppliesTo)
					}
				default:
					t.Errorf("%q 出现了未知的产出参数维度 %q", description.Action, option.Name)
				}
			}
		}
	}
}

// TestDescribeIsSerializable 覆盖描述可以直接进 JSON 给前端。
func TestDescribeIsSerializable(t *testing.T) {
	payload, err := json.Marshal(writingaction.Describe(writingaction.ScopeChapter))
	if err != nil {
		t.Fatalf("描述无法序列化：%v", err)
	}
	for _, key := range []string{`"action"`, `"label"`, `"scopes"`, `"output_params"`, `"produce"`, `"targets"`} {
		if !strings.Contains(string(payload), key) {
			t.Errorf("描述的 JSON 缺少键 %s：%s", key, payload)
		}
	}
}
