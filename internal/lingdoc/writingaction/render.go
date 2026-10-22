package writingaction

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// ErrContextNotAllowed 表示执行方交来的上下文越出了 Plan.Context 的声明。
//
// 禁止项被填进来，或必需项没交上来，都不是「这次执行少一点上下文」，而是
// 「执行方自建了动作语义」——所以它是一个请求错误，不是内部错误。
var ErrContextNotAllowed = clienterror.New(clienterror.InvalidRequest, "context_not_allowed")

// RenderContext 是执行方已经按当前授权解析好的上下文，按动作上下文类别分槽。
//
// 只有三类上下文有槽位：目标范围（选段或整章）与模板规则。证据卡与已授权资料
// 是**授权解析项**——执行方按请求里的引用与当前授权解析，既有行为不变，
// 它们不进入 RenderContext，也不参与这里的判定。
type RenderContext struct {
	// Section 是目标选区及其上下文（ContextSection）。
	Section string
	// Chapter 是目标章节（ContextChapter）。
	Chapter string
	// Template 是模板与规则（ContextTemplate）。
	Template string
}

// RenderInstruction 校验上下文是否落在 Plan.Context 的声明内，并给出动作专属指令。
//
//   - 出现 Plan.Context.Forbidden 的类别 → 错误（禁止项不得进入）
//   - 缺少 Plan.Context.Required 的类别 → 错误
//   - 其余类别不参与判定（证据卡与来源是「授权解析项」，按既有机制单独进模型
//     输入，不属于动作上下文类别；执行方不得把它们塞进 RenderContext）
//
// 指令文本由 Plan.Instruction 与 Plan.OutputParams 派生——两者都在词汇包里，
// 执行方不解释产出参数，也不自写动作指令。
func RenderInstruction(plan Plan, ctx RenderContext) (string, error) {
	if err := plan.checkRenderContext(ctx); err != nil {
		return "", err
	}
	return plan.renderInstruction(), nil
}

// value 取某个上下文类别在 RenderContext 里的槽位。
//
// 第二个返回值说这个类别有没有槽位：没有槽位的类别（证据卡、已授权资料）
// 不参与判定。
func (c RenderContext) value(item ContextItem) (string, bool) {
	switch item {
	case ContextSection:
		return c.Section, true
	case ContextChapter:
		return c.Chapter, true
	case ContextTemplate:
		return c.Template, true
	}
	return "", false
}

// checkRenderContext 判定这次执行交来的上下文是否落在计划的声明内。
func (p Plan) checkRenderContext(ctx RenderContext) error {
	for _, item := range p.Context.Forbidden {
		value, ok := ctx.value(item)
		if !ok {
			continue
		}
		if value != "" {
			return contextNotAllowed("forbidden context %q must not enter the execution", item)
		}
	}
	for _, item := range p.Context.Required {
		value, ok := ctx.value(item)
		if !ok {
			continue
		}
		if strings.TrimSpace(value) == "" {
			return contextNotAllowed("required context %q is missing", item)
		}
	}
	return nil
}

// renderInstruction 渲染动作专属指令。
func (p Plan) renderInstruction() string {
	parts := []string{string(p.Instruction)}
	if params := renderOutputParams(p.OutputParams); params != "" {
		parts = append(parts, params)
	}
	return strings.Join(parts, "")
}

// renderOutputParams 把产出参数写进动作指令。
//
// 取值同时给中文说法与原始取值：中文说法是模型读得懂的说法，原始取值是
// 开发计划 D17 的闭合清单里的那一个，两边都对得上。
func renderOutputParams(p OutputParams) string {
	var parts []string
	if p.Length != "" {
		parts = append(parts, fmt.Sprintf("产出长度：%s（%s）。", lengthLabel(p.Length), p.Length))
	}
	if p.Tone != "" {
		parts = append(parts, fmt.Sprintf("产出语气：%s（%s）。", toneLabel(p.Tone), p.Tone))
	}
	if p.Ratio != 0 {
		parts = append(parts, fmt.Sprintf("目标比例：%d%%。", p.Ratio))
	}
	if p.Transform != nil {
		parts = append(parts, renderTransform(p.Transform))
	}
	return strings.Join(parts, "")
}

// renderTransform 渲染转写动作的子参数。
func renderTransform(t *TransformParams) string {
	switch t.Mode {
	case TransformTranslate:
		if t.TargetLanguage != nil {
			return fmt.Sprintf("转写模式：译成 %s。", *t.TargetLanguage)
		}
	case TransformExplain:
		if t.Audience != nil {
			return fmt.Sprintf("转写模式：面向 %s 解释。", *t.Audience)
		}
	}
	return fmt.Sprintf("转写模式：%s。", t.Mode)
}

// lengthLabel 是产出长度的中文说法。
func lengthLabel(l Length) string {
	switch l {
	case LengthShort:
		return "短"
	case LengthMedium:
		return "中"
	case LengthLong:
		return "长"
	}
	return string(l)
}

// toneLabel 是产出语气的中文说法。
func toneLabel(t Tone) string {
	switch t {
	case ToneNeutral:
		return "中性"
	case ToneFormal:
		return "正式"
	case ToneAcademic:
		return "学术"
	case TonePlain:
		return "平实"
	}
	return string(t)
}

// contextNotAllowed 造出带上下文语义的越界错误。
func contextNotAllowed(format string, args ...any) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("context_not_allowed: "+format, args...), ErrContextNotAllowed)
}
