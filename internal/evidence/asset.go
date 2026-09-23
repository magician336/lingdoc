// Package evidence 承载 LingDoc 资料服务的最小领域接口：谁被允许使用哪些资料
// （AssetGateway），以及一条来源能不能指回原文（SourcePolicy）。
//
// 边界见 docs/08-本轮实施方案/02-接口与Mock约定.md §2：本包只回答"允许集合"
// 和"来源定位"，不写章节、不改确认。
package evidence

// Asset 是项目已绑定资料的对外形状，字段对应契约 contracts/openapi.json 的 Asset。
type Asset struct {
	ID              string
	ProjectID       string
	KnowledgeID     string
	Title           string
	AssetRevision   string
	ProcessingState AssetState
}

// AssetState 对应契约的 processing_state 枚举。
type AssetState string

const (
	AssetStatePending    AssetState = "pending"
	AssetStateProcessing AssetState = "processing"
	AssetStateReady      AssetState = "ready"
	AssetStateFailed     AssetState = "failed"
	AssetStateReplaced   AssetState = "replaced"
)

// Actor 是服务端认证上下文里的操作者身份。调用者不通过参数自报租户。
type Actor struct {
	UserID   string
	TenantID string
}
