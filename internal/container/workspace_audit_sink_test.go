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

// recordingAuditLogService captures every row the sink tried to write, so a
// test can assert on what reached the platform audit log — or that nothing
// did.
type recordingAuditLogService struct{ entries []*types.AuditLog }

func (s *recordingAuditLogService) Log(_ context.Context, entry *types.AuditLog) error {
	s.entries = append(s.entries, entry)
	return nil
}

func (s *recordingAuditLogService) LogDenied(
	context.Context, *gin.Context, uint64, string, string, types.TenantRole,
) error {
	return nil
}

func (s *recordingAuditLogService) List(
	context.Context, uint64, *interfaces.AuditLogQuery,
) ([]*types.AuditLog, error) {
	return nil, nil
}

func (s *recordingAuditLogService) Purge(context.Context, int) (int64, error) { return 0, nil }

func TestWorkspaceAuditSinkReturnsTheStorageFailure(t *testing.T) {
	boom := errors.New("audit storage unavailable")
	sink := workspaceAuditSink{service: failingAuditLogService{err: boom}}
	err := sink.Record(context.Background(), workspacecore.AuditEvent{
		TenantID: 7, UserID: "member", ProjectID: "project-1",
		Capability: "read", Decision: "allow", Role: types.TenantRoleViewer,
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

// #559: audit_logs.actor_role 空串的含义是「未知」（#535 管理会话裁定），
// 落库口径已经把空串登记为 2026-10-07 前的历史遗留。AuditEvent.Role 的
// 零值就是空串，而上游对 Role 的填充靠的是约定（TenantRoleFromContext 的
// viewer 回退、CallerFromContext 的 Normalize），不是类型保证——一个漏填
// 的构造点就能再写出一批空串。所以 sink 是最后一道闸：事件不带角色 ⇒
// 拒绝写审计并返回可判定错误，一行也不落。写路径的调用方本来就按
// fail-closed 处理 Record 的错误（状态变更不得不可追溯）；读路径的调用方
// 本来就丢弃 Record 的错误（读审计 fail-open），被拒绝只是少一行无法归因
// 的审计，不会改变任何权限结果。
func TestWorkspaceAuditSinkRefusesAnEmptyActorRole(t *testing.T) {
	service := &recordingAuditLogService{}
	sink := workspaceAuditSink{service: service}
	err := sink.Record(context.Background(), workspacecore.AuditEvent{
		TenantID: 7, UserID: "member", ProjectID: "project-1",
		Capability: "read", Decision: "allow",
		// Role 故意不填：这就是零值直达 sink 的路径。
	})
	if err == nil {
		t.Fatal("the audit sink accepted an event whose actor role is the zero value")
	}
	if !errors.Is(err, ErrAuditActorRoleEmpty) {
		t.Fatalf("the refusal is not determinate: %v", err)
	}
	if len(service.entries) != 0 {
		t.Fatalf("the audit sink wrote rows although the actor role was empty: %+v", service.entries)
	}
}
