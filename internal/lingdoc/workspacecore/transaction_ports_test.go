package workspacecore

import "testing"

// These compile-time contracts keep the expand phase honest: a persistence
// transaction can be passed through each cohesive port without changing the
// legacy Transaction seam.
var (
	_ Transaction                       = gormTransaction{}
	_ ProjectMembershipTransaction      = gormTransaction{}
	_ ChapterWorkspaceTransaction       = gormTransaction{}
	_ OperationTransaction              = gormTransaction{}
	_ AuditTransaction                  = gormTransaction{}
	_ projectUpdateTransaction          = gormTransaction{}
	_ projectCreateTransaction          = gormTransaction{}
	_ transferOwnerTransaction          = gormTransaction{}
	_ memberPermissionsTransaction      = gormTransaction{}
	_ collaboratorTransaction           = gormTransaction{}
	_ chapterMutationTransaction        = gormTransaction{}
	_ chapterCommitTransaction          = gormTransaction{}
	_ chapterSaveTransaction            = gormTransaction{}
	_ chapterActivationTransaction      = gormTransaction{}
	_ changeSetCreateTransaction        = gormTransaction{}
	_ changeSetApplyTransaction         = gormTransaction{}
	_ changeSetRejectTransaction        = gormTransaction{}
	_ draftCandidateMutationTransaction = gormTransaction{}
	_ issueDispositionTransaction       = gormTransaction{}
	_ ownerTransferRequestTransaction   = gormTransaction{}
	_ ownerTransferAcceptTransaction    = gormTransaction{}
)

func TestGORMTransactionExposesFocusedPortsAlongsideLegacyTransaction(t *testing.T) {
	var legacy Transaction = gormTransaction{}
	ports := []struct {
		name string
		port any
	}{
		{name: "project-membership", port: focusedPort[ProjectMembershipTransaction](legacy)},
		{name: "chapter-workspace", port: focusedPort[ChapterWorkspaceTransaction](legacy)},
		{name: "operation", port: focusedPort[OperationTransaction](legacy)},
		{name: "audit", port: focusedPort[AuditTransaction](legacy)},
	}
	for _, tt := range ports {
		if tt.port == nil {
			t.Fatalf("%s port is nil", tt.name)
		}
	}
}

func TestGORMTransactionExposesMutationCallbackPorts(t *testing.T) {
	var legacy Transaction = gormTransaction{}
	ports := []struct {
		name string
		port any
	}{
		{name: "project-update", port: focusedPort[projectUpdateTransaction](legacy)},
		{name: "project-create", port: focusedPort[projectCreateTransaction](legacy)},
		{name: "transfer-owner", port: focusedPort[transferOwnerTransaction](legacy)},
		{name: "member-permissions", port: focusedPort[memberPermissionsTransaction](legacy)},
		{name: "collaborator", port: focusedPort[collaboratorTransaction](legacy)},
		{name: "chapter-mutation", port: focusedPort[chapterMutationTransaction](legacy)},
		{name: "chapter-commit", port: focusedPort[chapterCommitTransaction](legacy)},
		{name: "chapter-save", port: focusedPort[chapterSaveTransaction](legacy)},
		{name: "chapter-activation", port: focusedPort[chapterActivationTransaction](legacy)},
		{name: "change-set-create", port: focusedPort[changeSetCreateTransaction](legacy)},
		{name: "change-set-apply", port: focusedPort[changeSetApplyTransaction](legacy)},
		{name: "change-set-reject", port: focusedPort[changeSetRejectTransaction](legacy)},
		{name: "draft-candidate", port: focusedPort[draftCandidateMutationTransaction](legacy)},
		{name: "issue-disposition", port: focusedPort[issueDispositionTransaction](legacy)},
		{name: "owner-transfer-request", port: focusedPort[ownerTransferRequestTransaction](legacy)},
		{name: "owner-transfer-accept", port: focusedPort[ownerTransferAcceptTransaction](legacy)},
	}
	for _, tt := range ports {
		if tt.port == nil {
			t.Fatalf("%s port is nil", tt.name)
		}
	}
}

func focusedPort[T any](legacy Transaction) T {
	port, ok := any(legacy).(T)
	if !ok {
		panic("legacy transaction does not expose focused port")
	}
	return port
}
