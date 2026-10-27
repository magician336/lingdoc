package rulesetstore

// RowShapesForSchemaGuard returns this package's gorm row structures, keyed
// by type name, so the schema guard in internal/database can hand them to
// gorm's own parser (Spec #707, ticket #709). The row structures are
// unexported; this exported door is test-support only — nothing in
// production calls it (precedent: workspacecore.AuditEventRowColumns).
//
// The row structures live in store.go, whose path is registered to the
// in-flight face PF-14 (#492): this file is new and touches nothing there.
func RowShapesForSchemaGuard() map[string]any {
	return map[string]any{
		"ruleSetMetricRow":   &ruleSetMetricRow{},
		"ruleSetUseAuditRow": &ruleSetUseAuditRow{},
		"ruleSetVersionRow":  &ruleSetVersionRow{},
	}
}
