package writingaction

// ContextSelection 是用户对这次执行要纳入的可选上下文的选择。
//
// 它按**类别**说话：键是上下文类别，值是这一类别的条目。类别必须落在动作
// 声明的 Plan.Context.Optional 里——客户端要求纳入一个这个动作根本不读
// 的东西，整个请求被拒；用户要剔除一个这个动作必须用的类别，同样被拒。
//
// 具体条目是否真的进入模型请求，由服务端按当前授权解析：没授权的条目
// 不进模型请求，并在响应里被点名。丢弃是可见的，不是静默的。
//
// 它是这一组形状的**唯一拥有者**：整章生成与选段改写两条管道都用它，请求
// 侧的形状只有一份。
type ContextSelection struct {
	// Include 是用户要纳入的条目，按上下文类别分列。
	Include map[ContextItem][]ContextEntry `json:"include,omitempty"`
	// Exclude 是用户要剔除的条目，按上下文类别分列。
	Exclude map[ContextItem][]ContextEntry `json:"exclude,omitempty"`
}

// ContextEntry 是一个上下文条目。
type ContextEntry struct {
	// ID 是条目 ID。资料、来源与证据卡都是它自己的 ID。
	ID string `json:"id"`
	// Version 是条目版本。证据卡必须带版本——没有版本的证据卡无从复核。
	Version int64 `json:"version,omitempty"`
}

// DroppedContextEntry 是一个没有进入模型请求的可选上下文条目。
//
// 丢弃是可见的：静默丢弃会让用户以为它进去了。
type DroppedContextEntry struct {
	// Category 是这一类上下文。
	Category ContextItem `json:"category"`
	// ID 是被丢弃的条目 ID。
	ID string `json:"id"`
	// Reason 说明它为什么没进去。
	Reason string `json:"reason"`
}
