package workspace

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// FullWorkflowRoutePorts 是全流程路由这条面依赖的三个上下文窄口。
//
// 成员与 Owner 的十一个操作属于 membership 那一门语言，章节复核的四个操作与
// 项目生命周期的那两个操作属于 workspacecore 那一门语言。三者都不提供聚合体，
// 所以这条面不再持有聚合体具体类型。
type FullWorkflowRoutePorts struct {
	Members   membership.MemberLifecycleApplication
	Reviews   core.ChapterReviewApplication
	Lifecycle core.ProjectLifecycleApplication
}

// wired 报告这束窄口是否全部接上了实现。
//
// 迁移前这条面持有聚合体指针，守卫只有「指针为 nil」一种状态。束里有三个口，
// 少接一个就会在请求里空指针崩溃，所以守卫改成整束检查：只要有一个口没接上，
// 传输层就不注册这条面的路由，而不是解引用一个空的窄口。
//
// nilPort 同时覆盖「口是 nil」与「口里装的是 nil 指针」两种情况，
// 与迁移前 `h.full.Core == nil` 的拒绝范围一致。
func (p FullWorkflowRoutePorts) wired() bool {
	return !nilPort(p.Members) && !nilPort(p.Reviews) && !nilPort(p.Lifecycle)
}

// FullWorkflowGuardPorts 是全流程守卫依赖的窄口。
//
// 前四个是守卫**读**的能力；Actions 是它构造智能体行动执行器时需要的
// 那七种原始操作；Compose 是它把自己装进运行时（预检复核器、恢复复核器、
// 行动执行器）时需要的三个装配操作——守卫今天就是自己往运行时里装的，
// 这一点不因端口收窄而改变。
type FullWorkflowGuardPorts struct {
	Delivery   core.DeliveryGuardApplication
	Provenance core.SourceProvenanceApplication
	Impact     core.ImpactGuardApplication
	Runtime    core.RuntimeGuardApplication
	Actions    core.AgentActionApplication
	Compose    core.RuntimeCompositionApplication
}

// wired 报告这束窄口是否全部接上了实现。
//
// 迁移前守卫持有聚合体指针，只有「指针为 nil」一种状态；束里有六个口，
// 少接一个就会在请求里空指针崩溃，所以守卫改成整束检查：只要有一个口没接上，
// 守卫就按迁移前的形态拒绝，而不是解引用一个空的窄口。
func (p FullWorkflowGuardPorts) wired() bool {
	return !nilPort(p.Delivery) && !nilPort(p.Provenance) && !nilPort(p.Impact) &&
		!nilPort(p.Runtime) && !nilPort(p.Actions) && !nilPort(p.Compose)
}
