// Package writingaction 独占拥有灵档的写作动作词汇：八类动作，以及每个动作的
// 目标范围、可用产出参数、产出形状、允许的采纳目标、所需上下文类别与动作专属
// 指令片段。
//
// 这里是这些语义的唯一拥有者：两条 AI 管道（整章生成、选段改写）都通过
// PlanFor 取已解析的执行计划，不得各自解释动作语义。
//
// 产出参数是客户端提交的产出条件；服务端的 AI 安全边界（AgentContextPackage
// 里的输出约束）是另一回事，由服务端组装。两者不得合并。
package writingaction

// Action 是一次 AI 写作请求的种类，说明这次请求要产出什么。
type Action string

// 八类写作动作。动作由请求显式声明，不由自由文本推断。
const (
	// ActionOutline 产出章节骨架。
	ActionOutline Action = "outline"
	// ActionGenerate 产出完整正文。
	ActionGenerate Action = "generate"
	// ActionContinue 接着选区及其前文继续写。
	ActionContinue Action = "continue"
	// ActionRewrite 改写选中的片段。
	ActionRewrite Action = "rewrite"
	// ActionCompress 把选中的片段压缩到指定比例。
	ActionCompress Action = "compress"
	// ActionExpand 把选中的片段扩展到指定比例。
	ActionExpand Action = "expand"
	// ActionTransform 按子模式转写选中的片段或整个章节。
	ActionTransform Action = "transform"
	// ActionReview 对章节或选区做语言评审。
	ActionReview Action = "review"
)

// Scope 是一次动作执行的目标范围。
type Scope string

// 两种目标范围。
const (
	// ScopeChapter 整个章节。
	ScopeChapter Scope = "chapter"
	// ScopeSection 章节里的选段。
	ScopeSection Scope = "section"
)

// AdoptionTarget 是一次采纳把候选写到哪里。
type AdoptionTarget string

// 三种采纳目标。
const (
	// TargetWorkingCopyPartial 写入工作副本的指定片段。
	TargetWorkingCopyPartial AdoptionTarget = "working_copy_partial"
	// TargetWorkingCopyWhole 写入工作副本的整段。
	TargetWorkingCopyWhole AdoptionTarget = "working_copy_whole"
	// TargetFormalVersionWhole 写入正式章节版本的整段。
	TargetFormalVersionWhole AdoptionTarget = "formal_version_whole"
)

// Origin 是一段正文的来源。
type Origin string

// 三种来源。
const (
	// OriginHuman 人工撰写。
	OriginHuman Origin = "human"
	// OriginGenerated AI 生成。
	OriginGenerated Origin = "ai_generated"
	// OriginAssisted 人工在 AI 结果上继续写的。
	OriginAssisted Origin = "ai_assisted_human"
)

// Produce 是一个动作的产出形状。
type Produce string

// 三种产出形状。
const (
	// ProduceProse 候选正文。
	ProduceProse Produce = "prose"
	// ProduceOutline 候选正文（章节骨架）。
	ProduceOutline Produce = "outline"
	// ProduceFindings 评审 findings，确认后应用。
	ProduceFindings Produce = "findings"
)

// ContextItem 是一类上下文项。
type ContextItem string

// 五类上下文项。
const (
	// ContextChapter 目标章节。
	ContextChapter ContextItem = "chapter"
	// ContextSection 目标选区及其上下文。
	ContextSection ContextItem = "section"
	// ContextAssets 已授权资料。
	ContextAssets ContextItem = "assets"
	// ContextCards 证据卡。
	ContextCards ContextItem = "cards"
	// ContextTemplate 模板与规则。
	ContextTemplate ContextItem = "template"
)

// ContextPolicy 是一个动作在某个范围下的上下文策略。
//
// 必需与可选分开：具体资料 ID 仍由服务端依据当前授权解析，客户端不能扩大
// 授权范围。禁止项是这个执行不得读的那一类上下文。
type ContextPolicy struct {
	// Required 是这次执行必须具备的上下文类别。
	Required []ContextItem
	// Optional 是用户可以在授权范围内增删的上下文类别。
	Optional []ContextItem
	// Forbidden 是这次执行不得使用的上下文类别。
	Forbidden []ContextItem
}
