package delivery

// RowShapesForSchemaGuard returns this package's gorm row structures, keyed
// by type name, so the schema guard in internal/database can hand them to
// gorm's own parser (Spec #707, ticket #709). The row structures are
// unexported; this exported door is test-support only — nothing in
// production calls it (precedent: workspacecore.AuditEventRowColumns).
func RowShapesForSchemaGuard() map[string]any {
	return map[string]any{
		"exportArtifactListRow": &exportArtifactListRow{},
		"exportArtifactRow":     &exportArtifactRow{},
		"releaseSnapshotRow":    &releaseSnapshotRow{},
	}
}
