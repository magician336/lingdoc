package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 上下文选择被拒时的两个对外形态。
//
// 它们是请求错误：客户端要求纳入的东西这个动作根本不读，或者用户要把这个
// 动作必须用的那一类拿掉——服务端不会替它猜一个意思。
var (
	// ErrContextNotAllowed 说清「这个动作不读你选的那一类上下文」。
	ErrContextNotAllowed = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request",
		Message: "这个动作不读所选的那一类上下文，请调整上下文选择后再试。", Retryable: false},
		"generation_context_not_allowed")
	// ErrContextRequired 说清「这一类上下文这个动作必须用」。
	ErrContextRequired = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request",
		Message: "这一类上下文是本次动作必须使用的，不能剔除。", Retryable: false},
		"generation_context_required")
)

// 可选上下文被丢掉时的原因。
const (
	// contextDroppedExcluded 表示这条上下文是用户自己剔除的。
	//
	// 生成侧的授权判定是「全有或全无」：请求里有未获授权的资料，整个请求被拒
	// 并点名（既有契约），不会有一半资料悄悄进模型请求。所以生成侧的丢弃只有
	// 一种来源——用户自己不要的那一条。
	contextDroppedExcluded = "excluded_by_user"
)

// isLegacyPath 判定这次请求是不是旧路径。
//
// 四个声明字段全部缺省 ⇒ 旧路径：自由文本说明驱动，行为逐字不变。
// 任一字段出现 ⇒ 动作路径。
func (r Request) isLegacyPath() bool {
	return r.WritingAction == nil && r.OutputParams == nil && r.Scope == nil && r.Context == nil
}

// resolveGenerationPlan 判定这次请求走哪条路径，并解析动作路径的执行计划。
//
// 四个新字段全部缺省 ⇒ 旧路径（plan 为 nil）：自由文本说明驱动。
// 任一字段出现 ⇒ 动作路径：动作必填、产出参数按闭合清单校验、范围只能是这个
// 动作允许的那两种。
//
// 动作语义一律由 writingaction.PlanFor 解析：执行方不按动作名分支，也不自己
// 解释产出参数。动作身份同样由词汇包给出——执行方引用管道，不写动作常量。
func resolveGenerationPlan(request Request) (*writingaction.Plan, error) {
	if request.isLegacyPath() {
		return nil, nil
	}
	if request.WritingAction == nil {
		return nil, ErrInvalidRequest
	}
	scope := writingaction.ScopeChapter
	if request.Scope != nil {
		scope = *request.Scope
	}
	params := writingaction.OutputParams{}
	if request.OutputParams != nil {
		params = *request.OutputParams
	}
	plan, err := writingaction.PlanFor(*request.WritingAction, scope, params)
	if err != nil {
		return nil, err
	}
	// 这个端点只实现生成管道声明的那几个动作（生成与转写）。别的已知动作由它们
	// 自己的端点负责，动作身份引用管道常量，不写动作字面量。
	//
	// 判定的是「这条管道做不做这个动作」而不是「这条管道做哪个动作」：管道可以
	// 实现多个动作，问错了问题就没有答案（开发计划 A33）。
	pipelineActions, err := writingaction.ActionsFor(writingaction.PipelineGeneration)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pipelineActions, plan.Action) {
		return nil, ErrInvalidRequest
	}
	return &plan, nil
}

// generationRenderContext 组装这次执行交给词汇包渲染的上下文。
//
// 它只填该动作 Plan.Context 声明的类别：目标范围那一类填目标内容，另一类
// 范围留空——那是这个动作在当前范围下**禁止**的类别。资料与证据卡是**授权
// 解析项**：它们按既有机制进入模型输入，不进 RenderContext，也不参与判定。
func generationRenderContext(plan writingaction.Plan, input Input) writingaction.RenderContext {
	var ctx writingaction.RenderContext
	chapter := input.Workspace.Chapter
	for _, item := range plan.Context.Required {
		switch item {
		case writingaction.ContextChapter:
			ctx.Chapter = generationChapterContext(chapter)
		case writingaction.ContextSection:
			ctx.Section = generationSectionContext(chapter, generationSectionTitle(input, chapter))
		case writingaction.ContextTemplate:
			ctx.Template = generationTemplateContext(input.TemplateDefinition)
		}
	}
	return ctx
}

// generationChapterContext 渲染「目标章节」这一类上下文。
//
// 「这一章还没有正文」也是一个事实——它告诉模型这次是整章撰写——所以这一槽
// 永远非空：否则「缺少必需上下文」与「没有正文」会分不开。
func generationChapterContext(chapter candidateadoption.Chapter) string {
	title := strings.TrimSpace(chapter.Title)
	if title == "" {
		title = "（未命名章节）"
	}
	body := strings.TrimSpace(chapter.BodyMarkdown)
	if body == "" {
		return fmt.Sprintf("章节 %s 本章节尚无正文，请整章撰写。", title)
	}
	return fmt.Sprintf("章节 %s 已有正文：\n%s", title, body)
}

// generationSectionContext 渲染「目标选段」这一类上下文。
//
// 生成管道的写入单位是章节里的一个小节，所以这一槽与整章那一槽读的是同一份
// 现有正文；两者的差别在**目标是什么**（整章 vs 该小节），由词汇包的上下文
// 策略与动作指令表达。
func generationSectionContext(chapter candidateadoption.Chapter, sectionTitle string) string {
	title := strings.TrimSpace(sectionTitle)
	if title == "" {
		title = strings.TrimSpace(chapter.Title)
	}
	if title == "" {
		title = "（未命名小节）"
	}
	body := strings.TrimSpace(chapter.BodyMarkdown)
	if body == "" {
		return fmt.Sprintf("小节 %s 该小节尚无正文，请撰写该小节。", title)
	}
	return fmt.Sprintf("小节 %s 已有正文：\n%s", title, body)
}

// generationSectionTitle 取这个章节对应的小节标题。
//
// 小节标题来自模板：章节按模板的小节划分，标题是模板那一侧的事实。
func generationSectionTitle(input Input, chapter candidateadoption.Chapter) string {
	if input.TemplateDefinition == nil {
		return ""
	}
	for _, section := range input.TemplateDefinition.Sections {
		if section.ID == chapter.SectionID {
			return section.Title
		}
	}
	return ""
}

// generationTemplateContext 渲染「模板与规则」这一类上下文。
//
// 它是**上下文**，不是动作语义：动作说什么由词汇包渲染，这里只回答「有没有模板
// 与规则」。「没有绑定模板」也是一个事实——它告诉模型这次产出不受项目模板约束
// ——所以这一槽永远非空：否则「缺少必需上下文」与「没有模板」会分不开。
func generationTemplateContext(definition *lingdoctemplate.Template) string {
	if definition == nil {
		return "本项目未绑定模板，产出不受项目模板约束。"
	}
	return fmt.Sprintf("模板 %s（%s@%s），内容哈希 %s，规则集哈希 %s。",
		definition.Name, definition.ID, definition.Version, definition.ContentHash, definition.RulesetHash)
}

// actionGenerationInput 填动作路径写给模型的指令。
//
// 指令只来自执行计划：执行方按 Plan.Context 声明的类别组装上下文，把渲染出来
// 的动作指令交给模型。它不按动作名分支，也不自己解释产出参数。客户端的说明
// 单独放一个字段：它不参与动作判定，也不与动作指令混成一段。
func actionGenerationInput(input *Input, plan writingaction.Plan, request Request) error {
	instruction, err := writingaction.RenderInstruction(plan, generationRenderContext(plan, *input))
	if err != nil {
		return err
	}
	input.Instruction = instruction
	input.SupplementaryNote = request.Instruction
	return nil
}

// legacyGenerationInput 填旧路径写给模型的指令。
//
// 客户端的自由文本说明原样作为指令：旧路径的行为逐字不变。
func legacyGenerationInput(input *Input, request Request) {
	input.Instruction = request.Instruction
	input.SupplementaryNote = ""
}

// applyGenerationInstruction 按路径把写给模型的指令放进输入。
//
// 两条路径各自组装，所以写给模型的指令只有一个来源。
func applyGenerationInstruction(input *Input, request Request, plan *writingaction.Plan) error {
	if plan == nil {
		legacyGenerationInput(input, request)
		return nil
	}
	return actionGenerationInput(input, *plan, request)
}

// applyContextSelection 按用户对可选上下文的选择改写这次请求的资料与证据卡。
//
// 它返回改写后的请求与被丢掉的条目。三条规则同时成立：类别必须落在动作声明
// 的可选上下文里，越过策略的请求被拒；这个动作必须用的类别不能被剔除；
// 用户剔除的条目不进模型请求。
func applyContextSelection(plan writingaction.Plan, selection writingaction.ContextSelection,
	request Request) (Request, []writingaction.DroppedContextEntry, error) {
	if err := checkContextPolicy(plan, selection); err != nil {
		return Request{}, nil, err
	}
	excludedAssets := contextItemIDs(selection.Exclude[writingaction.ContextAssets])
	excludedCards := contextItemIDs(selection.Exclude[writingaction.ContextCards])
	assets := append([]string(nil), request.AssetIDs...)
	for _, item := range selection.Include[writingaction.ContextAssets] {
		if item.ID != "" && !slices.Contains(assets, item.ID) {
			assets = append(assets, item.ID)
		}
	}
	cards := append([]EvidenceCardRef(nil), request.EvidenceCardRefs...)
	for _, item := range selection.Include[writingaction.ContextCards] {
		if item.ID == "" {
			continue
		}
		if !slices.ContainsFunc(cards, func(ref EvidenceCardRef) bool { return ref.ID == item.ID }) {
			cards = append(cards, EvidenceCardRef{ID: item.ID, Version: item.Version})
		}
	}
	var dropped []writingaction.DroppedContextEntry
	keptAssets := make([]string, 0, len(assets))
	for _, id := range assets {
		if slices.Contains(excludedAssets, id) {
			dropped = append(dropped, writingaction.DroppedContextEntry{Category: writingaction.ContextAssets, ID: id,
				Reason: contextDroppedExcluded})
			continue
		}
		keptAssets = append(keptAssets, id)
	}
	keptCards := make([]EvidenceCardRef, 0, len(cards))
	for _, ref := range cards {
		if slices.Contains(excludedCards, ref.ID) {
			dropped = append(dropped, writingaction.DroppedContextEntry{Category: writingaction.ContextCards, ID: ref.ID,
				Reason: contextDroppedExcluded})
			continue
		}
		keptCards = append(keptCards, ref)
	}
	// 剔除把一个必须用的类别清空了 ⇒ 400：这次执行缺的就是这一类上下文。
	if requiredContextEmptied(plan, assets, keptAssets, writingaction.ContextAssets) {
		return Request{}, nil, contextRequired(plan.Action, writingaction.ContextAssets)
	}
	if requiredContextEmptied(plan, cardIDs(cards), cardIDs(keptCards), writingaction.ContextCards) {
		return Request{}, nil, contextRequired(plan.Action, writingaction.ContextCards)
	}
	request.AssetIDs = keptAssets
	request.EvidenceCardRefs = keptCards
	return request, dropped, nil
}

// cardIDs 取出一组证据卡的 ID。
func cardIDs(refs []EvidenceCardRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// checkContextPolicy 校验这次选择落在动作声明的上下文策略内。
//
// 纳入与剔除的类别都必须在这个动作的可选上下文里：客户端要求增删一个这个
// 动作根本不读的东西，整个请求被拒。
func checkContextPolicy(plan writingaction.Plan, selection writingaction.ContextSelection) error {
	for category := range selection.Include {
		if !slices.Contains(plan.Context.Optional, category) {
			return contextNotAllowed(plan.Action, category)
		}
	}
	for category := range selection.Exclude {
		if !slices.Contains(plan.Context.Optional, category) {
			return contextNotAllowed(plan.Action, category)
		}
	}
	return nil
}

// requiredContextEmptied 说这次剔除是不是把一个必须用的类别清空了。
//
// 「这一类上下文这个动作必须用」是执行方的事实：用户可以点名这一类里的条目，
// 也可以剔掉其中几条，但这一类不能被整个拿掉——拿掉之后这次执行缺的就是
// 这一类上下文。
//
// 判据是**效果**而不是类别名：这一类本来就没有条目（用户压根没给证据卡）
// 不算被拿掉，只有剔除把最后几条拿掉了才算。
func requiredContextEmptied(plan writingaction.Plan, before []string, after []string, category writingaction.ContextItem) bool {
	if !slices.Contains(plan.Context.Required, category) {
		return false
	}
	if len(after) > 0 || len(before) == 0 {
		return false
	}
	return true
}

// contextItemIDs 取出一组上下文条目的 ID。
func contextItemIDs(items []writingaction.ContextEntry) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

// contextNotAllowed 造出「这个动作不读这一类上下文」的请求错误。
func contextNotAllowed(action writingaction.Action, item writingaction.ContextItem) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("invalid_request: action %q does not read the %q context", action, item), ErrContextNotAllowed)
}

// contextRequired 造出「这一类上下文这个动作必须用」的请求错误。
func contextRequired(action writingaction.Action, item writingaction.ContextItem) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("invalid_request: action %q requires the %q context", action, item), ErrContextRequired)
}

// candidateAction 是这个候选的动作身份。
//
// 动作路径下是请求显式声明的那一个；旧路径下是这个端点实现的那个动作。两种
// 情况都由词汇包给出——执行方不写动作常量，也就没有第二处动作身份。
//
// 取不到动作时返回空值：候选因此没有动作标注，采纳端会响亮地拒绝它，而不是
// 悄悄按一个猜出来的动作被采纳。
func candidateAction(request Request) writingaction.Action {
	if request.WritingAction != nil {
		return *request.WritingAction
	}
	action, err := writingaction.DefaultActionFor(writingaction.PipelineGeneration)
	if err != nil {
		return ""
	}
	return action
}

// generationRunMode 解析这次运行到模型面前的模式。
//
// 适配器报告的模式必须是三档之一。空值表示「没有 provider」，按 mock 处理：
// 没有 provider 的运行不得冒充真实证据（开发计划 A27）。其它取值是适配器说错了
// 话，直接被拒。
func generationRunMode(reported string) (candidateadoption.RunMode, error) {
	switch strings.TrimSpace(reported) {
	case string(candidateadoption.RunModeReal):
		return candidateadoption.RunModeReal, nil
	case string(candidateadoption.RunModeRealAPIFakeModel):
		return candidateadoption.RunModeRealAPIFakeModel, nil
	case string(candidateadoption.RunModeMock), "":
		return candidateadoption.RunModeMock, nil
	default:
		return "", ErrInvalidRequest
	}
}
