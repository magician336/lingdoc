package writingaction_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// validParams 给出一个动作**一份**合法的产出参数。
//
// 这里写的是字面量，不是从实现里推出来的：期望值必须来自独立的事实来源
// （开发计划 §3.2 的 8 条动作规格表），否则断言就成了自证。
func validParams(action writingaction.Action) writingaction.OutputParams {
	switch action {
	case writingaction.ActionGenerate, writingaction.ActionContinue, writingaction.ActionRewrite:
		return writingaction.OutputParams{
			Length: writingaction.LengthShort,
			Tone:   writingaction.ToneFormal,
		}
	case writingaction.ActionCompress, writingaction.ActionExpand:
		return writingaction.OutputParams{
			Ratio: 60,
			Tone:  writingaction.TonePlain,
		}
	case writingaction.ActionTransform:
		lang := writingaction.LangZH
		return writingaction.OutputParams{
			Length: writingaction.LengthMedium,
			Transform: &writingaction.TransformParams{
				Mode:           writingaction.TransformTranslate,
				TargetLanguage: &lang,
			},
		}
	default:
		return writingaction.OutputParams{}
	}
}

// TestPlanForResolvesEveryActionSpec 覆盖 8 条动作规格：允许范围、所需上下文、
// 产出形状与允许的采纳目标。
func TestPlanForResolvesEveryActionSpec(t *testing.T) {
	allTargets := []writingaction.AdoptionTarget{
		writingaction.TargetWorkingCopyPartial,
		writingaction.TargetWorkingCopyWhole,
		writingaction.TargetFormalVersionWhole,
	}

	cases := []struct {
		action    writingaction.Action
		scopes    []writingaction.Scope
		produce   writingaction.Produce
		targets   []writingaction.AdoptionTarget
		required  map[writingaction.Scope][]writingaction.ContextItem
		forbidden map[writingaction.Scope][]writingaction.ContextItem
	}{
		{
			action:  writingaction.ActionOutline,
			scopes:  []writingaction.Scope{writingaction.ScopeChapter},
			produce: writingaction.ProduceOutline,
			targets: []writingaction.AdoptionTarget{writingaction.TargetWorkingCopyWhole},
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextChapter, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextSection},
			},
		},
		{
			action:  writingaction.ActionGenerate,
			scopes:  []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextChapter, writingaction.ContextAssets, writingaction.ContextCards},
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextAssets, writingaction.ContextCards},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextSection},
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionContinue,
			scopes:  []writingaction.Scope{writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionRewrite,
			scopes:  []writingaction.Scope{writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionCompress,
			scopes:  []writingaction.Scope{writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionExpand,
			scopes:  []writingaction.Scope{writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionTransform,
			scopes:  []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection},
			produce: writingaction.ProduceProse,
			targets: allTargets,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextChapter, writingaction.ContextTemplate},
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextSection},
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
		{
			action:  writingaction.ActionReview,
			scopes:  []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection},
			produce: writingaction.ProduceFindings,
			targets: nil,
			required: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextChapter, writingaction.ContextTemplate},
				writingaction.ScopeSection: {writingaction.ContextSection, writingaction.ContextTemplate},
			},
			forbidden: map[writingaction.Scope][]writingaction.ContextItem{
				writingaction.ScopeChapter: {writingaction.ContextSection},
				writingaction.ScopeSection: {writingaction.ContextChapter},
			},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			for _, scope := range tc.scopes {
				plan, err := writingaction.PlanFor(tc.action, scope, validParams(tc.action))
				if err != nil {
					t.Fatalf("PlanFor(%q, %q) 报错：%v", tc.action, scope, err)
				}
				if plan.Action != tc.action {
					t.Errorf("Action = %q，期望 %q", plan.Action, tc.action)
				}
				if plan.Scope != scope {
					t.Errorf("Scope = %q，期望 %q", plan.Scope, scope)
				}
				if plan.Produce != tc.produce {
					t.Errorf("Produce = %q，期望 %q", plan.Produce, tc.produce)
				}
				if !slices.Equal(plan.Targets, tc.targets) {
					t.Errorf("Targets = %v，期望 %v", plan.Targets, tc.targets)
				}
				if !slices.Equal(plan.Context.Required, tc.required[scope]) {
					t.Errorf("Context.Required = %v，期望 %v", plan.Context.Required, tc.required[scope])
				}
				if !slices.Equal(plan.Context.Forbidden, tc.forbidden[scope]) {
					t.Errorf("Context.Forbidden = %v，期望 %v", plan.Context.Forbidden, tc.forbidden[scope])
				}
				if plan.Instruction == "" {
					t.Error("Instruction 为空：动作专属指令片段必须由词汇包提供")
				}
			}
		})
	}
}

// TestPlanForRejectsUnknownAction 覆盖动作封闭：未知动作与缺动作一样被拒，
// 绝不回退到自由文本。
func TestPlanForRejectsUnknownAction(t *testing.T) {
	for _, action := range []writingaction.Action{"", "unknown", "Rewrite", "polish"} {
		plan, err := writingaction.PlanFor(action, writingaction.ScopeSection, writingaction.OutputParams{})
		if !errors.Is(err, writingaction.ErrUnknownAction) {
			t.Fatalf("PlanFor(%q) 的错误 = %v，期望 ErrUnknownAction", action, err)
		}
		if !reflect.DeepEqual(plan, writingaction.Plan{}) {
			t.Errorf("PlanFor(%q) 失败时仍返回了计划：%+v", action, plan)
		}
	}
}

// TestPlanForRejectsScopeNotAllowed 覆盖范围不被允许的情形。
func TestPlanForRejectsScopeNotAllowed(t *testing.T) {
	cases := []struct {
		action writingaction.Action
		scope  writingaction.Scope
	}{
		{writingaction.ActionOutline, writingaction.ScopeSection},
		{writingaction.ActionContinue, writingaction.ScopeChapter},
		{writingaction.ActionRewrite, writingaction.ScopeChapter},
		{writingaction.ActionCompress, writingaction.ScopeChapter},
		{writingaction.ActionExpand, writingaction.ScopeChapter},
	}
	for _, tc := range cases {
		_, err := writingaction.PlanFor(tc.action, tc.scope, validParams(tc.action))
		if !errors.Is(err, writingaction.ErrScopeNotAllowed) {
			t.Errorf("PlanFor(%q, %q) 的错误 = %v，期望 ErrScopeNotAllowed", tc.action, tc.scope, err)
		}
	}
}

// TestPlanForRejectsInvalidOutputParams 覆盖产出参数的取值边界与用法边界。
func TestPlanForRejectsInvalidOutputParams(t *testing.T) {
	zh := writingaction.LangZH
	peer := writingaction.AudiencePeer
	unknownLength := writingaction.Length("huge")

	cases := []struct {
		name   string
		action writingaction.Action
		scope  writingaction.Scope
		params writingaction.OutputParams
	}{
		{
			name:   "长度取值不在闭合清单内",
			action: writingaction.ActionGenerate,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{Length: unknownLength, Tone: writingaction.ToneNeutral},
		},
		{
			name:   "语气取值不在闭合清单内",
			action: writingaction.ActionGenerate,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.Tone("sarcastic")},
		},
		{
			name:   "比例低于下界",
			action: writingaction.ActionCompress,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{Ratio: 9, Tone: writingaction.ToneNeutral},
		},
		{
			name:   "比例高于上界",
			action: writingaction.ActionCompress,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{Ratio: 91, Tone: writingaction.ToneNeutral},
		},
		{
			name:   "目标语言取值不在闭合清单内",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate, TargetLanguage: pointer(writingaction.TargetLanguage("es"))},
			},
		},
		{
			name:   "读者层次取值不在闭合清单内",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformExplain, Audience: pointer(writingaction.Audience("student"))},
			},
		},
		{
			name:   "转写子模式取值不在闭合清单内",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformMode("summarize")},
			},
		},
		{
			name:   "动作用不上的参数被带上",
			action: writingaction.ActionGenerate,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.ToneNeutral, Ratio: 50},
		},
		{
			name:   "没有产出参数的动作被带上参数",
			action: writingaction.ActionOutline,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{Length: writingaction.LengthShort},
		},
		{
			name:   "转写子参数被带给了别的动作",
			action: writingaction.ActionGenerate,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Tone:      writingaction.ToneNeutral,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate, TargetLanguage: &zh},
			},
		},
		{
			name:   "动作允许的参数没有给齐",
			action: writingaction.ActionGenerate,
			scope:  writingaction.ScopeChapter,
			params: writingaction.OutputParams{Length: writingaction.LengthShort},
		},
		{
			name:   "压缩动作没有给比例",
			action: writingaction.ActionCompress,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{Tone: writingaction.ToneNeutral},
		},
		{
			name:   "转写模式缺少目标语言",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate},
			},
		},
		{
			name:   "转写模式缺少读者层次",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformExplain},
			},
		},
		{
			name:   "转写模式混用目标语言与读者层次",
			action: writingaction.ActionTransform,
			scope:  writingaction.ScopeSection,
			params: writingaction.OutputParams{
				Length:    writingaction.LengthShort,
				Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate, TargetLanguage: &zh, Audience: &peer},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := writingaction.PlanFor(tc.action, tc.scope, tc.params)
			if !errors.Is(err, writingaction.ErrInvalidOutputParams) {
				t.Fatalf("PlanFor(%q, %q) 的错误 = %v，期望 ErrInvalidOutputParams", tc.action, tc.scope, err)
			}
			if !reflect.DeepEqual(plan, writingaction.Plan{}) {
				t.Errorf("PlanFor(%q, %q) 失败时仍返回了计划：%+v", tc.action, tc.scope, plan)
			}
		})
	}
}

// pointer 造一个取址的枚举值，供需要区分「没给」与「给了零值」的用例使用。
func pointer[T any](value T) *T {
	return &value
}
