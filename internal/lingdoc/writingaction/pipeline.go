package writingaction

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// ErrUnknownPipeline 表示请求指向的管道没有实现任何动作。
//
// 它与 ErrUnknownAction 分开：动作名不认识与「这条管道不做这个动作」是两种
// 要分开说的失败——前者是词汇表里没有这一项，后者是这一项归别的端点管。
var ErrUnknownPipeline = clienterror.New(clienterror.InvalidRequest, "unknown_pipeline")

// Pipeline 是一个执行方管道：一条实现某个写作动作的 AI 管道。
//
// 管道按**语义**声明自己实现哪个动作，执行方不自己写动作身份。执行方因此在
// 语法上就没法按动作名分支——它拿到的是动作，不是动作的名字（开发计划 A33）。
type Pipeline string

const (
	// PipelineGeneration 是整章生成管道：它实现「生成」这个动作。
	PipelineGeneration Pipeline = "generation"
)

// pipelineActions 是管道到动作的唯一映射表。
//
// 它与 actionSpecs 不是第二份动作语义：actionSpecs 说「这个动作要什么、允许
// 怎么采纳」，这里说「这条管道做这个动作」。两者是两条不同的轴，词汇包是这两
// 条轴的同一个拥有者。
var pipelineActions = map[Pipeline]Action{
	PipelineGeneration: ActionGenerate,
}

// ActionFor 返回这个管道实现的动作。
//
// 未知管道返回 ErrUnknownPipeline：这条管道没有实现任何动作，不能拿它去解析
// 执行计划。
func ActionFor(p Pipeline) (Action, error) {
	action, ok := pipelineActions[p]
	if !ok {
		return "", clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: unknown pipeline %q", p), ErrUnknownPipeline)
	}
	return action, nil
}
