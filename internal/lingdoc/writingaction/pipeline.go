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

// Pipeline 是一个执行方管道：一条实现若干写作动作的 AI 管道。
//
// 管道按**语义**声明它实现哪些动作，执行方不自己写动作身份。执行方因此在
// 语法上就没法按动作名分支——它拿到的是动作，不是动作的名字（开发计划 A33）。
type Pipeline string

const (
	// PipelineGeneration 是整章生成管道：它实现「生成」与「转写」这两个动作。
	PipelineGeneration Pipeline = "generation"
	// PipelineRewrite 是选段管道：它实现「改写」「续写」「压缩」「扩展」与「转写」
	// 这五个动作。
	//
	// 它叫「选段管道」而不是「改写管道」：一条管道可以实现多个动作，动作之间
	// 的差别由词汇包给出，执行方不解释。
	PipelineRewrite Pipeline = "rewrite"
)

// pipelineSpec 是一个管道的声明。
//
// 它说的是**管道**自己的三件事，不是动作的事：这条管道实现哪些动作、它能做哪些
// 目标范围、请求没有显式声明动作时它做哪个动作。三者都是脚手架层面的事实——
// 同一个动作（转写）在选段管道里只做选段，在生成管道里也能做整章。
type pipelineSpec struct {
	// actions 是这条管道实现的动作集合。
	actions []Action
	// scopes 是这条管道能做的目标范围集合。
	scopes []Scope
	// defaultAction 是请求没有显式声明动作时这条管道执行的动作。
	//
	// 它是旧路径的动作身份：旧路径的请求不带动作，候选上记的却是「这次是哪个
	// 动作做的」。没有它，候选就记不下自己的来源，采纳端也无从确认。
	defaultAction Action
}

// pipelineSpecs 是管道到声明的唯一映射表。
//
// 它与 actionSpecs 不是第二份动作语义：actionSpecs 说「这个动作要什么、允许
// 怎么采纳」，这里说「这条管道做这些动作、能做这些范围」。两者是两条不同的轴，
// 词汇包是这两条轴的同一个拥有者。
var pipelineSpecs = map[Pipeline]pipelineSpec{
	PipelineGeneration: {
		actions:       []Action{ActionGenerate, ActionTransform},
		scopes:        []Scope{ScopeChapter, ScopeSection},
		defaultAction: ActionGenerate,
	},
	PipelineRewrite: {
		actions:       []Action{ActionRewrite, ActionContinue, ActionCompress, ActionExpand, ActionTransform},
		scopes:        []Scope{ScopeSection},
		defaultAction: ActionRewrite,
	},
}

// ActionsFor 返回这个管道实现的动作集合。
//
// 未知管道返回 ErrUnknownPipeline：这条管道没有实现任何动作，不能拿它去解析
// 执行计划。集合是拷贝：调用方改不动词汇包。
func ActionsFor(p Pipeline) ([]Action, error) {
	spec, err := specForPipeline(p)
	if err != nil {
		return nil, err
	}
	return slices.Clone(spec.actions), nil
}

// ScopesFor 返回这个管道能做的目标范围集合。
//
// 范围是**管道**的声明，不是动作的声明：动作层面的允许范围说「这个动作可以是
// 整章也可以是选段」，管道层面的允许范围说「这条管道写到哪里」——选段管道写
// 选段，生成管道写整章。执行方据此拒绝「在选段管道里声明整章」这种请求。
func ScopesFor(p Pipeline) ([]Scope, error) {
	spec, err := specForPipeline(p)
	if err != nil {
		return nil, err
	}
	return slices.Clone(spec.scopes), nil
}

// DefaultActionFor 返回这个管道在请求没有显式声明动作时执行的动作。
//
// 它回答的是旧路径的问题：旧请求不带动作，执行方按自己的形状干活，候选上记的
// 却是「这次是哪个动作做的」。管道答不出这个问题，候选就记不下自己的来源。
func DefaultActionFor(p Pipeline) (Action, error) {
	spec, err := specForPipeline(p)
	if err != nil {
		return "", err
	}
	return spec.defaultAction, nil
}

// specForPipeline 是管道到声明的唯一查找入口。
func specForPipeline(p Pipeline) (pipelineSpec, error) {
	spec, ok := pipelineSpecs[p]
	if !ok {
		return pipelineSpec{}, clienterror.Wrap(clienterror.InvalidRequest,
			fmt.Sprintf("writingaction: unknown pipeline %q", p), ErrUnknownPipeline)
	}
	return spec, nil
}
