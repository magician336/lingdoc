package workspacecore

import (
	"github.com/Tencent/WeKnora/internal/types"
)

func ownerCurrentlyAvailable(tx Transaction, p Project, tenant uint64, user string) bool {
	active, err := tx.ActiveMember(Actor{TenantID: tenant, UserID: user})
	if err != nil || !active {
		return false
	}
	role, err := currentTenantRole(tx, Actor{TenantID: tenant, UserID: user})
	if err != nil || role != "" && !role.HasPermission(types.TenantRoleContributor) {
		return false
	}
	m, ok := projectMember(p, user)
	return ok && governanceRole(m) == "owner" && (m.Status == "" || m.Status == "active")
}
