package container

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// The platform audit sink is the seam the AuditSink port declares as
// `Record(context.Context, AuditEvent) error`.
//
// It used to return nil no matter what happened: a serialization failure and a
// storage failure were both written to the log and dropped. The caller could
// not tell a recorded decision from a lost one, so nothing could alert, count
// or degrade on it (#414).
//
// The difference between the two outcomes is the caller's policy, not the
// sink's: the write paths fail closed (a state change must never become
// untraceable) and the ordinary read paths stay fail-open and publish an
// audit-failure alert. This test pins the half the sink owns: it reports the
// failure instead of swallowing it.

// failingAuditLogService is the platform audit log with a broken storage.
type failingAuditLogService struct{ err error }

func (s failingAuditLogService) Log(context.Context, *types.AuditLog) error { return s.err }

func (s failingAuditLogService) LogDenied(
	context.Context, *gin.Context, uint64, string, string, types.TenantRole,
) error {
	return nil
}

func (s failingAuditLogService) List(
	context.Context, uint64, *interfaces.AuditLogQuery,
) ([]*types.AuditLog, error) {
	return nil, nil
}

func (s failingAuditLogService) Purge(context.Context, int) (int64, error) { return 0, nil }

func TestWorkspaceAuditSinkReturnsTheStorageFailure(t *testing.T) {
	boom := errors.New("audit storage unavailable")
	sink := workspaceAuditSink{service: failingAuditLogService{err: boom}}
	err := sink.Record(context.Background(), workspacecore.AuditEvent{
		TenantID: 7, UserID: "member", ProjectID: "project-1",
		Capability: "read", Decision: "allow",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("the audit sink swallowed the storage failure: %v", err)
	}
}

func TestWorkspaceAuditSinkWithoutAPlatformServiceIsANoOp(t *testing.T) {
	// A sink with no platform audit log service has no storage to write to,
	// so there is no write that could fail. It stays a no-op, the same
	// convention the OpenSearch audit sink adapter uses.
	sink := workspaceAuditSink{}
	if err := sink.Record(context.Background(), workspacecore.AuditEvent{
		TenantID: 7, UserID: "member", ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("a sink without a platform service reported a failure: %v", err)
	}
}
