package writingaction_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// rewritePlan 取一份改写动作的执行计划。
func rewritePlan(t *testing.T) writingaction.Plan {
	t.Helper()
	plan, err := writingaction.PlanFor(writingaction.ActionRewrite, writingaction.ScopeSection,
		writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.ToneFormal})
	if err != nil {
		t.Fatalf("PlanFor(改写) 报错：%v", err)
	}
	return plan
}

// TestRenderInstructionRefusesForbiddenContext 断言执行方不得把动作禁止的
// 上下文类别塞进这次执行。
//
// 改写只针对选区：整章正文是这个动作**禁止**的上下文类别。执行方交了它，
// 说明执行方自建了动作语义，而不是照着执行计划组装。
func TestRenderInstructionRefusesForbiddenContext(t *testing.T) {
	plan := rewritePlan(t)
	_, err := writingaction.RenderInstruction(plan, writingaction.RenderContext{
		Section:  "选中的片段",
		Chapter:  "整章正文",
		Template: "模板与规则",
	})
	if !errors.Is(err, writingaction.ErrContextNotAllowed) {
		t.Fatalf("RenderInstruction 的错误 = %v，期望 ErrContextNotAllowed", err)
	}
}

// TestRenderInstructionRequiresEveryDeclaredContext 断言动作声明的必需上下文
// 缺一不可。
func TestRenderInstructionRequiresEveryDeclaredContext(t *testing.T) {
	plan := rewritePlan(t)
	cases := []struct {
		name string
		ctx  writingaction.RenderContext
	}{
		{"缺少选区上下文", writingaction.RenderContext{Template: "模板与规则"}},
		{"缺少模板规则", writingaction.RenderContext{Section: "选中的片段"}},
		{"只有空白", writingaction.RenderContext{Section: "   ", Template: "\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := writingaction.RenderInstruction(plan, tc.ctx); !errors.Is(err, writingaction.ErrContextNotAllowed) {
				t.Fatalf("RenderInstruction 的错误 = %v，期望 ErrContextNotAllowed", err)
			}
		})
	}
}

// TestRenderInstructionLeavesAuthorizationResolvedItemsUnjudged 断言证据卡与
// 已授权资料不参与上下文判定。
//
// 它们是**授权解析项**：执行方按请求里的引用与当前授权解析，既有行为不变，
// 它们不进入 RenderContext。生成动作要求这两类上下文，但执行方没有槽位
// 交它们——判定不能因此失败。
func TestRenderInstructionLeavesAuthorizationResolvedItemsUnjudged(t *testing.T) {
	plan, err := writingaction.PlanFor(writingaction.ActionGenerate, writingaction.ScopeSection,
		writingaction.OutputParams{Length: writingaction.LengthMedium, Tone: writingaction.ToneNeutral})
	if err != nil {
		t.Fatalf("PlanFor(生成) 报错：%v", err)
	}
	text, err := writingaction.RenderInstruction(plan, writingaction.RenderContext{Section: "选中的片段"})
	if err != nil {
		t.Fatalf("RenderInstruction 报错：%v", err)
	}
	if text == "" {
		t.Fatal("RenderInstruction 返回了空指令")
	}
}

// TestRenderInstructionRendersTheActionInstructionAndTheOutputParams 断言动作
// 指令由计划派生，并把产出参数写进同一段指令。
//
// 期望值来自开发计划 §3.2 的 8 条动作规格表与 D17 的取值清单：改写的动作
// 专属指令是那句固定文本，长度与语气取 short / formal。
func TestRenderInstructionRendersTheActionInstructionAndTheOutputParams(t *testing.T) {
	plan := rewritePlan(t)
	text, err := writingaction.RenderInstruction(plan, writingaction.RenderContext{
		Section:  "选中的片段",
		Template: "模板与规则",
	})
	if err != nil {
		t.Fatalf("RenderInstruction 报错：%v", err)
	}
	if !strings.Contains(text, string(plan.Instruction)) {
		t.Errorf("指令 %q 没有派生自计划的动作指令 %q", text, plan.Instruction)
	}
	if !strings.Contains(text, "只改写选中的片段") {
		t.Errorf("指令 %q 没有动作专属指令的内容", text)
	}
	if !strings.Contains(text, "short") || !strings.Contains(text, "formal") {
		t.Errorf("指令 %q 没有带上产出参数的取值", text)
	}
}

// TestPlanForCarriesTheOutputParams 断言产出参数挂在计划上。
//
// 执行方只读计划，不自己解释产出参数：参数要进模型指令，就必须由词汇包渲染。
func TestPlanForCarriesTheOutputParams(t *testing.T) {
	params := writingaction.OutputParams{Length: writingaction.LengthLong, Tone: writingaction.ToneAcademic}
	plan, err := writingaction.PlanFor(writingaction.ActionRewrite, writingaction.ScopeSection, params)
	if err != nil {
		t.Fatalf("PlanFor 报错：%v", err)
	}
	if plan.OutputParams != params {
		t.Errorf("OutputParams = %+v，期望 %+v", plan.OutputParams, params)
	}
}

// TestOutputParamsJSONShape 断言产出参数的 JSON 形状。
//
// 客户端提交的就是这份形状；取值不在闭合清单内的请求由 PlanFor 拒绝。
func TestOutputParamsJSONShape(t *testing.T) {
	raw, err := json.Marshal(writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.TonePlain})
	if err != nil {
		t.Fatalf("Marshal 报错：%v", err)
	}
	if got := string(raw); got != `{"length":"short","tone":"plain"}` {
		t.Errorf("OutputParams 的 JSON = %s，期望只带被给出的维度", got)
	}
	language := writingaction.LangZH
	raw, err = json.Marshal(writingaction.OutputParams{
		Length:    writingaction.LengthMedium,
		Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate, TargetLanguage: &language},
	})
	if err != nil {
		t.Fatalf("Marshal 报错：%v", err)
	}
	if got := string(raw); got != `{"length":"medium","transform":{"mode":"translate","target_language":"zh"}}` {
		t.Errorf("OutputParams 的 JSON = %s，期望转写子参数带上模式与目标语言", got)
	}
}
