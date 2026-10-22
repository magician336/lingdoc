package writingaction

import (
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// 三个动作级错误在模块内定义一次：消费方不再各写各的 400。
var (
	// ErrUnknownAction 表示请求里的动作不在词汇表内。
	ErrUnknownAction = clienterror.New(clienterror.InvalidRequest, "unknown_action")
	// ErrInvalidOutputParams 表示产出参数越界、缺失或混用。
	ErrInvalidOutputParams = clienterror.New(clienterror.InvalidRequest, "invalid_output_params")
	// ErrScopeNotAllowed 表示该动作不允许这个目标范围。
	ErrScopeNotAllowed = clienterror.New(clienterror.InvalidRequest, "scope_not_allowed")
)

// InstructionTemplate 是动作专属的指令片段。
//
// 它只由 writingaction 渲染。执行方只允许把它与管道脚手架拼起来，不得改写
// 它的语义，也不得按动作名另写一段指令。
type InstructionTemplate string

// Plan 是一次动作执行所需的全部动作语义，已由 writingaction 解析完毕。
//
// 消费方只读它，不重新解释其中的任何一项。
type Plan struct {
	// Action 是这次执行的动作。
	Action Action
	// Scope 是这次执行的目标范围。
	Scope Scope
	// Produce 是产出形状：候选正文、章节骨架或评审 findings。
	Produce Produce
	// Context 是上下文策略：必需、可选与禁止的上下文类别。
	// 具体资料 ID 由服务端依据当前授权解析。
	Context ContextPolicy
	// Targets 是这个动作允许的采纳目标。采纳端只查这里，不按动作名分支。
	Targets []AdoptionTarget
	// Instruction 是该动作专属的指令片段。
	Instruction InstructionTemplate
	// OutputParams 是这次执行已经解析好的产出条件。
	//
	// 它挂在计划上而不是让执行方自己留着：执行方只读计划，不解释产出参数
	// （开发计划 D10 / A33）。渲染动作指令时由词汇包把它写进指令文本。
	OutputParams OutputParams
}

// actionSpec 是一个动作的包内规格。
//
// 它不对外导出：消费方拿到的是 PlanFor 解析好的 Plan，而不是一张要自己解读
// 的表。规格表里的每一条都是事实——允许范围、可用产出参数、所需上下文、产出
// 形状、允许的采纳目标、动作专属指令片段。
type actionSpec struct {
	action       Action
	label        string
	scopes       []Scope
	params       []paramKind
	alsoRequired []ContextItem
	// optional 是这个动作允许用户在授权范围内增删的上下文类别。
	//
	// 它是「用户能选什么」，不是「这次执行必须有什么」——后者在
	// alsoRequired 与目标范围里。
	optional    []ContextItem
	produce     Produce
	targets     []AdoptionTarget
	instruction InstructionTemplate
}

// paramKind 是一个产出参数维度。
type paramKind int

// 产出参数的维度。
const (
	paramLength paramKind = iota
	paramTone
	paramRatio
	paramTransform
)

// actionSpecs 是八类动作的唯一规格表。
//
// PlanFor 与 Describe 都从这里读；仓里不存在第二份手写的动作语义。
var actionSpecs = []actionSpec{
	{
		action:       ActionOutline,
		label:        "提纲",
		scopes:       []Scope{ScopeChapter},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceOutline,
		targets:      []AdoptionTarget{TargetWorkingCopyWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "先给出本章的章节骨架：每节的小标题与要点，不展开成完整正文。",
	},
	{
		action:       ActionGenerate,
		label:        "生成",
		scopes:       []Scope{ScopeChapter, ScopeSection},
		params:       []paramKind{paramLength, paramTone},
		alsoRequired: []ContextItem{ContextAssets, ContextCards},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "按给定的长度与语气写出正文，只依据已授权的章节上下文、资料与证据卡。",
	},
	{
		action:       ActionContinue,
		label:        "续写",
		scopes:       []Scope{ScopeSection},
		params:       []paramKind{paramLength, paramTone},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "接着选区及其前文继续写，保持与前文一致的语气与衔接，不重复已有内容。",
	},
	{
		action:       ActionRewrite,
		label:        "改写",
		scopes:       []Scope{ScopeSection},
		params:       []paramKind{paramLength, paramTone},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "只改写选中的片段，保留原有信息与含义，不改动选区之外的文字。",
	},
	{
		action:       ActionCompress,
		label:        "压缩",
		scopes:       []Scope{ScopeSection},
		params:       []paramKind{paramRatio, paramTone},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "把选中的片段压缩到指定比例，保留关键信息与论证链条，不新增未经支持的内容。",
	},
	{
		action:       ActionExpand,
		label:        "扩展",
		scopes:       []Scope{ScopeSection},
		params:       []paramKind{paramRatio, paramTone},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "把选中的片段扩展到指定比例，展开论证与例证，不新增未经支持的内容。",
	},
	{
		action:       ActionTransform,
		label:        "转写",
		scopes:       []Scope{ScopeChapter, ScopeSection},
		params:       []paramKind{paramLength, paramTransform},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceProse,
		targets:      []AdoptionTarget{TargetWorkingCopyPartial, TargetWorkingCopyWhole, TargetFormalVersionWhole},
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "按指定的转写模式改写目标内容：translate 译成目标语言，explain 面向指定读者层次解释。",
	},
	{
		action:       ActionReview,
		label:        "语言评审",
		scopes:       []Scope{ScopeChapter, ScopeSection},
		alsoRequired: []ContextItem{ContextTemplate},
		produce:      ProduceFindings,
		targets:      nil,
		optional:     []ContextItem{ContextAssets, ContextCards},
		instruction:  "对目标内容做语言评审，产出可逐条处置的评审意见；只评审表达，不改动事实与数据。",
	},
}

// PlanFor 是动作语义的唯一决策入口。
//
// 它一次性解析三件事：这次执行需要什么上下文、产出什么形状、允许怎么采纳。
// 未知动作返回 ErrUnknownAction；产出参数越界、缺失或混用返回
// ErrInvalidOutputParams；范围不被该动作允许返回 ErrScopeNotAllowed。
//
// PlanFor 只校验动作参数。服务端还要校验用户选择的具体资料 ID 是否属于
// Plan.Context.Optional 且当前授权有效——具体 ID 的解析不在这里。
func PlanFor(a Action, scope Scope, p OutputParams) (Plan, error) {
	spec, ok := specFor(a)
	if !ok {
		return Plan{}, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: unknown action %q", a), ErrUnknownAction)
	}
	if !slices.Contains(spec.scopes, scope) {
		return Plan{}, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: action %q does not allow scope %q", a, scope), ErrScopeNotAllowed)
	}
	if err := spec.validateParams(p); err != nil {
		return Plan{}, err
	}
	required, optional, forbidden := spec.contextFor(scope)
	return Plan{
		Action:       spec.action,
		Scope:        scope,
		Produce:      spec.produce,
		Context:      ContextPolicy{Required: required, Optional: optional, Forbidden: forbidden},
		Targets:      spec.targets,
		Instruction:  spec.instruction,
		OutputParams: p,
	}, nil
}

// AllowsContext reports whether this action reads the given context category.
//
// 消费方用它判断「客户端要求纳入的东西，这个动作到底读不读」：读的就是
// 可能的增删对象，不读的就是越过策略，越过策略的请求要被拒。
//
// 它查的是计划，不是动作名：消费方不按动作分支。
func (p Plan) AllowsContext(item ContextItem) bool {
	return slices.Contains(p.Context.Required, item) || slices.Contains(p.Context.Optional, item)
}

// specFor 是动作到规格的唯一查找入口。
func specFor(a Action) (actionSpec, bool) {
	for _, spec := range actionSpecs {
		if spec.action == a {
			return spec, true
		}
	}
	return actionSpec{}, false
}

// uses 判断这个动作是否用得上某个产出参数维度。
func (s actionSpec) uses(kind paramKind) bool {
	return slices.Contains(s.params, kind)
}

// contextFor 解析这个范围下的上下文策略。
//
// 目标范围本身（整章或选段）总是必需的；与范围不符的那一类上下文被禁止：
// 整章执行不读选段，选段执行不读整章。
//
// optional 是用户可以在授权范围内增删的类别。已经在 required 里的类别同样
// 列在 optional 里：用户能选它、能点名它，只是不能把它拿掉——「这一类
// 上下文这个动作必须用」是执行方的事实，不该靠「用户没提」来表达。
func (s actionSpec) contextFor(scope Scope) (required, optional, forbidden []ContextItem) {
	target, other := ContextSection, ContextChapter
	if scope == ScopeChapter {
		target, other = ContextChapter, ContextSection
	}
	required = append(required, target)
	required = append(required, s.alsoRequired...)
	optional = append(optional, s.optional...)
	forbidden = append(forbidden, other)
	return required, optional, forbidden
}

// validateParams 校验产出参数是否落在该动作的闭合取值集内。
//
// 三条规则同时成立：动作用得上的参数必须给齐；动作用不上的参数必须没给；
// 给出的取值必须落在闭合枚举内。
func (s actionSpec) validateParams(p OutputParams) error {
	if s.uses(paramLength) {
		if p.Length == "" {
			return invalidParams("action %q requires the length output parameter", s.action)
		}
		if !slices.Contains(lengthValues, p.Length) {
			return invalidParams("action %q got an unknown length %q", s.action, p.Length)
		}
	} else if p.Length != "" {
		return invalidParams("action %q does not use the length output parameter", s.action)
	}

	if s.uses(paramTone) {
		if p.Tone == "" {
			return invalidParams("action %q requires the tone output parameter", s.action)
		}
		if !slices.Contains(toneValues, p.Tone) {
			return invalidParams("action %q got an unknown tone %q", s.action, p.Tone)
		}
	} else if p.Tone != "" {
		return invalidParams("action %q does not use the tone output parameter", s.action)
	}

	if s.uses(paramRatio) {
		if p.Ratio == 0 {
			return invalidParams("action %q requires the ratio output parameter", s.action)
		}
		if p.Ratio < minRatio || p.Ratio > maxRatio {
			return invalidParams("action %q got ratio %d outside %d..%d", s.action, p.Ratio, minRatio, maxRatio)
		}
	} else if p.Ratio != 0 {
		return invalidParams("action %q does not use the ratio output parameter", s.action)
	}

	if s.uses(paramTransform) {
		if p.Transform == nil {
			return invalidParams("action %q requires the transform output parameters", s.action)
		}
		return validateTransform(s.action, p.Transform)
	}
	if p.Transform != nil {
		return invalidParams("action %q does not use the transform output parameters", s.action)
	}
	return nil
}

// validateTransform 校验转写动作的子参数。
//
// translate 必须带目标语言，explain 必须带读者层次，两者互斥；缺一个或两个都
// 带上都被拒。
func validateTransform(action Action, t *TransformParams) error {
	switch t.Mode {
	case TransformTranslate:
		if t.TargetLanguage == nil {
			return invalidParams("action %q in translate mode requires a target language", action)
		}
		if !slices.Contains(targetLanguageValues, *t.TargetLanguage) {
			return invalidParams("action %q got an unknown target language %q", action, *t.TargetLanguage)
		}
		if t.Audience != nil {
			return invalidParams("action %q in translate mode must not carry an audience", action)
		}
	case TransformExplain:
		if t.Audience == nil {
			return invalidParams("action %q in explain mode requires an audience", action)
		}
		if !slices.Contains(audienceValues, *t.Audience) {
			return invalidParams("action %q got an unknown audience %q", action, *t.Audience)
		}
		if t.TargetLanguage != nil {
			return invalidParams("action %q in explain mode must not carry a target language", action)
		}
	default:
		return invalidParams("action %q got an unknown transform mode %q", action, t.Mode)
	}
	return nil
}

// invalidParams 造出带产出参数语义的非法参数错误。
func invalidParams(format string, args ...any) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("invalid_output_params: "+format, args...), ErrInvalidOutputParams)
}
