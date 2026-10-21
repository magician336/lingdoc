package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// ImpactRoutePorts 是影响中心这条面依赖的六个上下文窄口。
//
// 一条面可以依赖多个上下文；束只是把「这条面需要哪几门语言」写在一处，
// 它不把任何一个口变宽。
type ImpactRoutePorts struct {
	ChangeSets core.ChangeSetDraftApplication
	Impact     core.ImpactApplication
	Dependency core.DependencyApplication
	Runtime    core.RuntimeApplication
	Trials     core.TrialApplication
	Roadmap    core.RoadmapApplication
}

// wired 报告这束窄口是否全部接上了实现。
//
// 迁移前这条面持有聚合体指针，守卫只有「指针为 nil」一种状态。束里有六个口，
// 少接一个就会在请求里空指针崩溃，所以守卫改成整束检查：只要有一个口没接上，
// 传输层就用 ErrInvalidState 拒绝，而不是解引用一个空的窄口。
//
// nilPort 同时覆盖「口是 nil」与「口里装的是 nil 指针」两种情况，
// 与迁移前 `h.service == nil` 的拒绝范围一致。
func (p ImpactRoutePorts) wired() bool {
	return !nilPort(p.ChangeSets) && !nilPort(p.Impact) && !nilPort(p.Dependency) &&
		!nilPort(p.Runtime) && !nilPort(p.Trials) && !nilPort(p.Roadmap)
}
