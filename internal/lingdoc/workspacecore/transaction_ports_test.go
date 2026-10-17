package workspacecore

import "testing"

// These compile-time contracts keep the expand phase honest: a persistence
// transaction can be passed through each cohesive port without changing the
// legacy Transaction seam.
var (
	_ Transaction                      = gormTransaction{}
	_ ProjectMembershipTransaction     = gormTransaction{}
	_ ChapterWorkspaceTransaction      = gormTransaction{}
	_ OperationTransaction             = gormTransaction{}
	_ AuditTransaction                 = gormTransaction{}
	_ ProjectMutationTransaction       = gormTransaction{}
	_ ChapterMutationTransaction       = gormTransaction{}
	_ ChangeSetMutationTransaction     = gormTransaction{}
	_ CandidateMutationTransaction     = gormTransaction{}
	_ AuditMutationTransaction         = gormTransaction{}
	_ OwnerTransferMutationTransaction = gormTransaction{}
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
		{name: "project-mutation", port: focusedPort[ProjectMutationTransaction](legacy)},
		{name: "chapter-mutation", port: focusedPort[ChapterMutationTransaction](legacy)},
		{name: "change-set-mutation", port: focusedPort[ChangeSetMutationTransaction](legacy)},
		{name: "candidate-mutation", port: focusedPort[CandidateMutationTransaction](legacy)},
		{name: "audit-mutation", port: focusedPort[AuditMutationTransaction](legacy)},
		{name: "owner-transfer-mutation", port: focusedPort[OwnerTransferMutationTransaction](legacy)},
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
