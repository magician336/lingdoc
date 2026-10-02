package workspacecore

import "context"

// SourcePolicy 是「这批已经产出的引用此刻还能不能当证据」在核心域的端口。
//
// 端口声明在核心域而不是装配层，理由有三条：
//
//   - 调用者是核心域自己的写路径。SaveChapter 要在落笔之前核一遍引用，而复核必须跑在
//     幂等重放之前（见 operation 的注释）——那是核心域自己的那笔事务，只有核心域能在
//     那个位置插进去。
//   - 又不能反向 import 具体实现所在的包：那等于让核心域依赖它的一个消费者。
//     装配层同时认识两边，适配器放在那里。
//   - 返回 error 而不是 bool，因为结论不止一种（未授权 / 坐标失效 / 协作方坏了）。
//     核心域只负责把结论原样转交，怎么归类由传输层统一决定；在这里分档就等于把
//     线上语义抄进领域。
//
// 实现方可以返回 nil 之外的任何 error；核心域不解释它，只把它交给调用者。
type SourcePolicy interface {
	Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error
}
