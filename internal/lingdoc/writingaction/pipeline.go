package writingaction

import (
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// ErrUnknownPipeline 表示请求指向的管道没有实现任何动作。
//
// 它与 ErrUnknownAction 分开：动作名不认识与「这条管道不做这个动作」是两种
// 要分开说的失败——前者是词汇表里没有这一项，后者是这一项归别的端点管。
var ErrUnknownPipeline = clienterror.New(clienterror.InvalidRequest, "unknown_pipeline")

// ErrAmbiguousPipeline 表示这条管道实现了多个动作，「这条管道做哪个动作」
// 没有答案。
//
// 它只在有人问错问题时出现：执行方要问的是「这条管道做不做这个动作」，
// 那个问题由 ActionsFor 回答。
var ErrAmbiguousPipeline = clienterror.New(clienterror.InvalidRequest, "ambiguous_pipeline")

// Pipeline 是一个执行方管道：一条实现若干写作动作的 AI 管道。
//
// 管道按**语义**声明它实现哪些动作，执行方不自己写动作身份。执行方因此在
// 语法上就没法按动作名分支——它拿到的是动作，不是动作的名字（开发计划 A33）。
type Pipeline string

const (
	// PipelineGeneration 是整章生成管道：它实现「生成」这个动作。
	PipelineGeneration Pipeline = "generation"
	// PipelineRewrite 是选段管道：它实现「改写」「续写」与「压缩」这三个动作。
	//
	// 它叫「选段管道」而不是「改写管道」：一条管道可以实现多个动作，动作之间
	// 的差别由词汇包给出，执行方不解释。
	PipelineRewrite Pipeline = "rewrite"
)

// pipelineActions 是管道到动作集合的唯一映射表。
//
// 它与 actionSpecs 不是第二份动作语义：actionSpecs 说「这个动作要什么、允许
// 怎么采纳」，这里说「这条管道做这些动作」。两者是两条不同的轴，词汇包是这
// 两条轴的同一个拥有者。
var pipelineActions = map[Pipeline][]Action{
	PipelineGeneration: {ActionGenerate},
	PipelineRewrite:    {ActionRewrite, ActionContinue, ActionCompress},
}

// ActionsFor 返回这个管道实现的动作集合。
//
// 未知管道返回 ErrUnknownPipeline：这条管道没有实现任何动作，不能拿它去解析
// 执行计划。集合是拷贝：调用方改不动词汇包。
func ActionsFor(p Pipeline) ([]Action, error) {
	actions, ok := pipelineActions[p]
	if !ok {
		return nil, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: unknown pipeline %q", p), ErrUnknownPipeline)
	}
	return slices.Clone(actions), nil
}

// ActionFor 返回这个管道实现的**唯一**动作。
//
// 管道实现多个动作时它没有答案（ErrAmbiguousPipeline）：那时候「这条管道做
// 哪个动作」这句话本身就不成立，调用方要的是集合，用 ActionsFor。
func ActionFor(p Pipeline) (Action, error) {
	actions, err := ActionsFor(p)
	if err != nil {
		return "", err
	}
	if len(actions) != 1 {
		return "", clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: pipeline %q implements %d actions", p, len(actions)),
			ErrAmbiguousPipeline)
	}
	return actions[0], nil
}
