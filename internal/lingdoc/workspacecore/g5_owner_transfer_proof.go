package workspacecore

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
)

type ownerTransferOrigin struct {
	RequesterID string `json:"requester_id"`
	Recovery    bool   `json:"recovery"`
}

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
func recordOwnerTransferOrigin(tx Transaction, a Actor, p Project, transfer OwnerTransfer) error {
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrInvalidState
	}
	origin := ownerTransferOrigin{RequesterID: a.UserID, Recovery: a.UserID != transfer.FromUserID}
	if err := store.G5Put(p.ID, "owner_transfer_origin", transfer.ID, 0, origin); err != nil {
		return err
	}
	return g5Audit(tx, a, p.ID, "owner.transfer.request", transfer.ID, "")
}
func verifyOwnerTransferOrigin(ctx context.Context, tx Transaction, a Actor, p Project, transfer OwnerTransfer) error {
	if err := RequireHumanDecision(ctx); err != nil {
		return err
	}
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrInvalidState
	}
	var origin ownerTransferOrigin
	if _, err := store.G5Get(p.ID, "owner_transfer_origin", transfer.ID, &origin); err != nil {
		return ErrInvalidState
	}
	if origin.Recovery {
		if ownerCurrentlyAvailable(tx, p, a.TenantID, transfer.FromUserID) {
			return ErrInvalidState
		}
		role, err := currentTenantRole(tx, Actor{TenantID: a.TenantID, UserID: origin.RequesterID})
		if err != nil || role != types.TenantRoleOwner {
			return ErrNotFound
		}
	} else if origin.RequesterID != transfer.FromUserID || !ownerCurrentlyAvailable(tx, p, a.TenantID, transfer.FromUserID) {
		return ErrInvalidState
	}
	role, err := currentTenantRole(tx, a)
	if err != nil || role != "" && !role.HasPermission(types.TenantRoleContributor) {
		return ErrNotFound
	}
	return nil
}
