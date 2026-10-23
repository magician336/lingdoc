package workspacecore

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantPermissionAuthorizer distinguishes an ordinary tenant-role ceiling from
// unavailable membership/storage in the caller's current transaction.
type TenantPermissionAuthorizer interface {
	TenantPermissionInTransaction(context.Context, Transaction, Actor, types.TenantRole) (bool, error)
}

var _ TenantPermissionAuthorizer = (*Service)(nil)
