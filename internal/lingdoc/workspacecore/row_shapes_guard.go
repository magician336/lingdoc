package workspacecore

// RowShapesForSchemaGuard returns this package's gorm row structures, keyed
// by type name, so the schema guard in internal/database can hand them to
// gorm's own parser (Spec #707, ticket #709). The row structures are
// unexported; this exported door is test-support only — nothing in
// production calls it (precedent: AuditEventRowColumns in model.go).
func RowShapesForSchemaGuard() map[string]any {
	return map[string]any{
		"academicOriginalSnapshotRow": &academicOriginalSnapshotRow{},
		"auditEventRow":               &auditEventRow{},
		"chapterConfirmationRow":      &chapterConfirmationRow{},
		"chapterRow":                  &chapterRow{},
		"chapterVersionRow":           &chapterVersionRow{},
		"changeSetRow":                &changeSetRow{},
		"dependencyCandidateBatchRow": &dependencyCandidateBatchRow{},
		"dependencyCandidateRow":      &dependencyCandidateRow{},
		"draftCandidateRow":           &draftCandidateRow{},
		"DraftClaimProvenance":        &DraftClaimProvenance{},
		"memberPermissionRow":         &memberPermissionRow{},
		"memberRow":                   &memberRow{},
		"modelCallLedgerRow":          &modelCallLedgerRow{},
		"objectRow":                   &objectRow{},
		"objectVersionRow":            &objectVersionRow{},
		"operationRow":                &operationRow{},
		"ownerTransferRow":            &ownerTransferRow{},
		"projectRecordRow":            &projectRecordRow{},
		"projectRow":                  &projectRow{},
		"projectTemplateCopyRow":      &projectTemplateCopyRow{},
		"roadmapEditSessionRow":       &roadmapEditSessionRow{},
		"routeEdgeRow":                &routeEdgeRow{},
		"runtimeRecordRow":            &runtimeRecordRow{},
		"versionedRecordRow":          &versionedRecordRow{},
		"workingCopyRow":              &workingCopyRow{},
	}
}
