package workspacecore

import (
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/types"
)

// The owner_unavailable judgement.
//
// Every Owner-only action asks one question: may the Owner act as the Owner
// right now? The answer is derived from the Owner's current tenant membership,
// tenant role and project membership. It is never stored, so a restored tenant
// identity unfreezes the action by itself and there is no "forgot to unfreeze"
// state to repair.
//
// This file holds the judgement and the doorways to it together. A second copy
// of the same conditions is a second answer to the same question, and the two
// answers drift apart -- that is exactly what the inline copy in the transfer
// request used to be.

const (
	// ReasonAllowed is the reason code of an action that may run.
	ReasonAllowed = "allowed"
	// ReasonNotOwner is the reason code of an action the actor may not run
	// because the actor is not the Owner.
	ReasonNotOwner = "not_owner"
	// ReasonOwnerUnavailable is the reason code of an Owner-only action
	// frozen because the Owner's tenant identity lapsed.
	ReasonOwnerUnavailable = "owner_unavailable"
)

// ErrOwnerUnavailable is the refusal an Owner-only action answers with while
// the Owner's tenant identity has lapsed.
//
// The status and the error code stay what a refused Owner-only action has
// always answered with: nothing about the existing codes changes meaning, and
// no status code is added. It still is the same refusal -- it wraps
// ErrNotFound, so every caller that asks "is this the resource I cannot
// reach?" keeps its answer. The reason rides along in the optional details,
// so a caller can also tell "the Owner's tenant identity lapsed" from "you
// are not the Owner" without guessing.
var ErrOwnerUnavailable = clienterror.Wrap(
	clienterror.Form{
		Status: 404, Code: "not_found",
		Message: "资源不存在或不可访问。", Retryable: false,
		Details: map[string]any{"reason_code": ReasonOwnerUnavailable},
	},
	ReasonOwnerUnavailable,
	ErrNotFound,
)

// OwnerAuthority is the derived fact about a project's Owner.
//
// It is derived, not stored: the Owner's tenant membership, the Owner's tenant
// role and the Owner's project membership are read at the moment of the
// question. When the tenant identity comes back, the fact changes back by
// itself.
type OwnerAuthority struct {
	// OwnerUserID is the project's Owner.
	OwnerUserID string
	// Available reports whether the Owner may still act as a tenant member.
	Available bool
}

// OwnerAuthorityOf derives the fact about the project's Owner.
//
// The Owner's tenant membership must be active, the Owner's tenant role must
// still carry contributor rights, and the Owner's project membership must
// still be active. This is the one place that knows what "the Owner may still
// act" means.
func OwnerAuthorityOf(tx Transaction, p Project, tenant uint64) OwnerAuthority {
	owner, ok := projectOwnerUser(p)
	if !ok {
		return OwnerAuthority{}
	}
	return OwnerAuthority{OwnerUserID: owner, Available: ownerCurrentlyAvailable(tx, p, tenant, owner)}
}

// OwnerActionJudgement is the answer for one Owner-only action.
type OwnerActionJudgement struct {
	// Allowed reports whether the action may run.
	Allowed bool
	// ReasonCode names the answer: ReasonAllowed, ReasonNotOwner or
	// ReasonOwnerUnavailable.
	ReasonCode string
	// OwnerUserID is the project's Owner.
	OwnerUserID string
}

// The ways back from a frozen project.
//
// They are identifiers, not sentences: the outlet names the way back, the
// interface writes it down. A path that the product cannot offer yet is
// simply not in the list.
const (
	// RecoveryRestoreTenantIdentity is the way back that changes nothing
	// about the project: the tenant restores the Owner's identity, and the
	// derived fact turns back by itself.
	RecoveryRestoreTenantIdentity = "restore_tenant_identity"
	// RecoveryTenantOwnerNomination is the way back that moves the project:
	// the WeKnora tenant Owner nominates a successor and starts a transfer.
	RecoveryTenantOwnerNomination = "tenant_owner_nomination"
)

// OwnerAvailability is the derived fact about a project's Owner, read at the
// project level.
//
// It is the same judgement the permission read outlet asks about, answered in
// the shape a project page and a members page can use: who the Owner is,
// whether the Owner may still act, why, and the ways back. It is derived, so
// it does not live on the project and it is not stored: the project payload
// does not grow a field when the Owner lapses.
type OwnerAvailability struct {
	// OwnerUserID is the project's Owner.
	OwnerUserID string `json:"owner_user_id"`
	// Available reports whether the Owner may still act as a tenant member.
	Available bool `json:"available"`
	// ReasonCode names the state: ReasonAllowed or ReasonOwnerUnavailable.
	ReasonCode string `json:"reason_code"`
	// RecoveryPaths names the ways back while the Owner is unavailable. It
	// is empty when the Owner may still act.
	RecoveryPaths []string `json:"recovery_paths"`
}

// OwnerAvailabilityOf derives the project level fact about the Owner.
//
// It reads the same judgement the Owner-only actions are judged by, so the
// two read outlets cannot drift apart: the permission outlet says "this entry
// is frozen", and this one says "the Owner is unavailable and here is how the
// project gets back to work".
func OwnerAvailabilityOf(tx Transaction, p Project, tenant uint64) OwnerAvailability {
	authority := OwnerAuthorityOf(tx, p, tenant)
	availability := OwnerAvailability{
		OwnerUserID: authority.OwnerUserID,
		// A project without an Owner has nothing frozen: the Owner-only
		// actions are not blocked, they are simply not this project's shape.
		Available:  true,
		ReasonCode: ReasonAllowed,
		// The list is never nil: a reader can tell "no way back is offered"
		// from "the outlet did not answer".
		RecoveryPaths: []string{},
	}
	if authority.OwnerUserID == "" || authority.Available {
		return availability
	}
	availability.Available = false
	availability.ReasonCode = ReasonOwnerUnavailable
	availability.RecoveryPaths = []string{RecoveryRestoreTenantIdentity, RecoveryTenantOwnerNomination}
	return availability
}

// JudgeOwnerAction decides one Owner-only action for one actor.
//
// An Owner-only action has the Owner as its only authority, so it freezes
// while the Owner is unavailable, and the refusal names the freeze
// (ReasonOwnerUnavailable) instead of the actor's own lack of authority
// (ReasonNotOwner).
func JudgeOwnerAction(tx Transaction, p Project, actor Actor) OwnerActionJudgement {
	authority := OwnerAuthorityOf(tx, p, actor.TenantID)
	if authority.OwnerUserID == "" || actor.UserID != authority.OwnerUserID {
		return OwnerActionJudgement{ReasonCode: ReasonNotOwner, OwnerUserID: authority.OwnerUserID}
	}
	if !authority.Available {
		return OwnerActionJudgement{ReasonCode: ReasonOwnerUnavailable, OwnerUserID: authority.OwnerUserID}
	}
	return OwnerActionJudgement{Allowed: true, ReasonCode: ReasonAllowed, OwnerUserID: authority.OwnerUserID}
}

// ownerDecisionInTransaction is the Owner-only decision taken inside the
// write transaction: the authoritative one, taken against the project the
// write lands on.
func ownerDecisionInTransaction(tx Transaction, p Project, actor Actor) error {
	judgement := JudgeOwnerAction(tx, p, actor)
	if judgement.ReasonCode == ReasonOwnerUnavailable {
		return ErrOwnerUnavailable
	}
	if !judgement.Allowed {
		return ErrNotFound
	}
	return nil
}

// OwnerMayAct reports whether the actor may act because the actor is the
// Owner.
//
// This is the doorway for the uses where somebody else may act as well --
// editing one's own change draft, commenting, reading the transfer record.
// Those uses have another authority beside the Owner, so the Owner's tenant
// identity lapsing removes the Owner's own alternative and nothing else: they
// are never frozen.
func OwnerMayAct(p Project, actor Actor) bool {
	return projectOwner(p, actor.UserID)
}

// OwnerCurrentlyAvailable reports whether the user still owns the project and
// still may act in the tenant.
//
// The owner may have transferred the ownership, or the tenant may have
// suspended them; either way the answer is no.
func OwnerCurrentlyAvailable(tx Transaction, project Project, tenant uint64, user string) bool {
	return ownerCurrentlyAvailable(tx, project, tenant, user)
}

// projectOwnerUser returns the project's Owner.
func projectOwnerUser(p Project) (string, bool) {
	for _, member := range p.Members {
		if member.Role == "owner" {
			return member.UserID, true
		}
	}
	return "", false
}

// ownerCurrentlyAvailable reports whether the user may still act as the
// project's Owner.
//
// The user must still be the Owner, the tenant membership must still be
// active, the tenant role must still carry contributor rights, and the
// project membership must still be active.
func ownerCurrentlyAvailable(tx Transaction, p Project, tenant uint64, user string) bool {
	if !projectOwner(p, user) {
		return false
	}
	active, err := tx.ActiveMember(Actor{TenantID: tenant, UserID: user})
	if err != nil || !active {
		return false
	}
	role, err := currentTenantRole(tx, Actor{TenantID: tenant, UserID: user})
	if err != nil || role != "" && !role.HasPermission(types.TenantRoleContributor) {
		return false
	}
	m, ok := projectMember(p, user)
	return ok && (m.Status == "" || m.Status == "active")
}
