package workspace

import core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"

// ProjectPorts 是项目侧的三条窄口。
//
// 三条口各属一个上下文：授权、项目读、生成上下文。把它们装进一个结构体不是为了
// 合成一个更宽的口，而是为了让一次装配把「这三条口交给谁」说清楚——组合根是唯一
// 的注入位（Spec #47 决策 37），这里只是那份清单的载体。
//
// 遗留的 ApplicationService（37 方法）曾经同时扮演这三个角色；它退役后，每条口
// 各自回答自己那一个问题。
type ProjectPorts struct {
	// Authorizer 回答「这个人此刻能不能做这件事」。
	Authorizer core.CapabilityAuthorizer
	// Projects 是项目上下文的那条口。
	Projects core.ProjectApplication
	// Context 回答「章节此刻的生成上下文是什么」。
	Context core.GenerationContextApplication
}
