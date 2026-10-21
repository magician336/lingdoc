// Package membership owns the member and owner context: the operations
// that decide who may act on a project and how ownership moves.
//
// The port is declared here, next to the language it describes, rather
// than in the transport that consumes it (Spec #47 decision 33). The
// implementation and the invariants move here in ticket #268; until
// then the aggregate still answers these operations.
package membership

import (
	"context"
	"encoding/json"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Application is the member and owner application port.
//
// Its method set and semantics are unchanged by the move.
type Application interface {
	SaveMembersResult(context.Context, core.Actor, string, string, core.SaveMembersInput) (core.ProjectMutationResult, error)
	TransferOwnerResult(context.Context, core.Actor, string, string, core.TransferOwnerInput) (core.ProjectMutationResult, error)
	RequestOwnerTransferResult(context.Context, core.Actor, string, string, core.OwnerTransferInput) (core.OwnerTransferResult, error)
	AcceptOwnerTransferResult(context.Context, core.Actor, string, string, string) (core.OwnerTransferResult, error)
}

// MemberLifecycleApplication is the member and owner lifecycle: the
// operations the collaboration routes need.
type MemberLifecycleApplication interface {
	AcceptProjectInvitation(ctx context.Context, actor core.Actor, pid, id, key string, input core.AcceptMemberInput) (json.RawMessage, int, bool, error)
	ChangeMemberState(ctx context.Context, actor core.Actor, pid, user, key string, input core.MemberLifecycleInput) (json.RawMessage, int, bool, error)
	EffectivePermission(ctx context.Context, actor core.Actor, projectID, capability string) (core.EffectivePermission, error)
	InviteProjectMember(ctx context.Context, actor core.Actor, pid, key string, input core.InviteMemberInput) (json.RawMessage, int, bool, error)
	ListMyProjectInvitations(ctx context.Context, a core.Actor) ([]core.MemberInvitation, error)
	ListOwnerTransfers(ctx context.Context, a core.Actor, pid string) ([]core.OwnerTransfer, error)
	ListProjectInvitations(ctx context.Context, actor core.Actor, pid string) ([]core.MemberInvitation, error)
	ProjectMemberDirectory(ctx context.Context, a core.Actor, pid string) ([]core.MemberDirectoryEntry, error)
	RejectOwnerTransfer(ctx context.Context, a core.Actor, pid, id, key string, input core.OwnerTransferDecisionInput) (json.RawMessage, int, bool, error)
	SaveProjectAPIKeyGrant(ctx context.Context, a core.Actor, pid, key string, input core.ProjectAPIKeyGrant) (json.RawMessage, int, bool, error)
	SetMemberAssignment(ctx context.Context, a core.Actor, pid, user, key string, input core.MemberAssignmentInput) (json.RawMessage, int, bool, error)
}

// The ports are implemented here, not by the aggregate.
//
// The aggregate used to answer these operations, and the port was declared
// next to the language it describes while the aggregate kept the
// implementation. Now the service in this package holds it, and the
// aggregate no longer implements either port.
//
// The reverse half of that promise - that the aggregate does NOT implement
// these ports any more - cannot be a compile-time assertion, because Go has
// no way to say "this type does not satisfy this interface". It is asserted
// at run time in ports_test.go, where the aggregate is handed to the ports
// as an `any` and refused.
var (
	_ Application                = (*Service)(nil)
	_ MemberLifecycleApplication = (*Service)(nil)
)
