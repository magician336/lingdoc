package evidence

// RowShapesForSchemaGuard 是表形状互检守护（internal/database/
// row_shape_guard_test.go）的资料域门：返回本包持有的 LingDoc 行结构零值，
// 按类型名键入。先例：workspacecore.AuditEventRowColumns。
//
// 仅测试支撑——生产代码零调用。返回的是行结构本身，不是契约形状：
// Asset（契约对外形状）与本行刻意分离，守护要看的正是会随迁移漂移的那份。
func RowShapesForSchemaGuard() map[string]any {
	return map[string]any{
		"ProjectAsset":         ProjectAsset{},
		"ProjectAssetRevision": ProjectAssetRevision{},
		"operationRow":         operationRow{},
	}
}
