package membership

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// The member and owner operations that read the workspace's records.
//
// Their storage lives behind the workspace's own ports: a caller of this
// package asks for a member directory, an invitation list or an owner
// transfer list, and never learns a table name, a column or a row.

// ProjectMemberDirectory lists the members of the tenant that the project may
// see.
//
// Only a project manager may ask. A stranger receives the same answer the
// workspace gives for an object it may not see.
func (s *Service) ProjectMemberDirectory(
	ctx context.Context,
	a core.Actor,
	pid string,
) ([]core.MemberDirectoryEntry, error) {
	var out []core.MemberDirectoryEntry
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		if _, err := s.authorizer.AuthorizeProject(
			ctx, tx, core.NewAuthorizationRequest(a, pid, core.AuthorizationManage, ""),
		); err != nil {
			return err
		}
		entries, err := s.directory.ListActiveTenantMembers(ctx, tx, a.TenantID)
		out = entries
		if err != nil {
			return err
		}
		return nil
	})
	return out, err
}

// ListMyProjectInvitations lists the invitations addressed to the actor.
func (s *Service) ListMyProjectInvitations(ctx context.Context, a core.Actor) ([]core.MemberInvitation, error) {
	result := []core.MemberInvitation{}
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		if err := s.authorizer.AuthorizeTenant(
			ctx, tx, core.NewAuthorizationRequest(a, "", core.AuthorizationRead, ""),
		); err != nil {
			return err
		}
		invitations, err := s.records.ListTenantInvitations(ctx, tx, a.TenantID, a.UserID)
		result = invitations
		if err != nil {
			return err
		}
		return nil
	})
	return result, err
}

// ListOwnerTransfers lists the owner transfers of the project that the actor
// may see.
//
// A transfer the actor is not a party to is hidden from them, and one that
// has waited too long reads as expired.
func (s *Service) ListOwnerTransfers(ctx context.Context, a core.Actor, pid string) ([]core.OwnerTransfer, error) {
	var result []core.OwnerTransfer
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		p, err := s.authorizer.AuthorizeProject(
			ctx, tx, core.NewAuthorizationRequest(a, pid, core.AuthorizationRead, ""),
		)
		if err != nil {
			return err
		}
		transfers, err := s.transfers.List(ctx, tx, pid, p, a.UserID)
		result = transfers
		if err != nil {
			return err
		}
		return nil
	})
	return result, err
}

// SetMemberAssignment replaces one member's assignment.
//
// The owner is not a member assignment: the owner leaves by transferring the
// ownership, never by an assignment.
func (s *Service) SetMemberAssignment(
	ctx context.Context,
	a core.Actor,
	pid, user, key string,
	input core.MemberAssignmentInput,
) (json.RawMessage, int, bool, error) {
	if input.Assignment.UserID != user || input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, core.ErrInvalidRequest
	}
	return s.runner.Run(ctx, a, core.OperationSpec{
		Name:    core.OperationMemberAssignment,
		Target:  pid + "/" + user,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(a, pid, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationMemberAssignment),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.Transaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		old, exists := core.ProjectMember(p, user)
		if !exists || core.GovernanceRole(old) == "owner" || old.Status != "" && old.Status != "active" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		nextMember := input.Assignment
		if err := s.validateInvitation(ctx, tx, p, nextMember); err != nil {
			return core.OperationOutcome{}, err
		}
		active, err := port.ActiveMember(core.Actor{TenantID: a.TenantID, UserID: user})
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if !active {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		nextMember.Role = "collaborator"
		nextMember.Status = "active"
		next := p
		next.ProjectVersion++
		next.Members = slices.Clone(p.Members)
		for i := range next.Members {
			if next.Members[i].UserID == user {
				next.Members[i] = nextMember
			}
		}
		if err := port.UpdateProject(a.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(pid, next.Members); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := core.RecordProjectAudit(tx, a, pid, "member.assignment", user, input.Reason); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: next, Status: core.OperationStatusOK}, nil
	})
}

// SaveProjectAPIKeyGrant records what a project API key may read.
//
// The grant names a key the tenant owns and the scope it may read; the
// workspace says whether the key may be used.
func (s *Service) SaveProjectAPIKeyGrant(
	ctx context.Context,
	a core.Actor,
	pid, key string,
	input core.ProjectAPIKeyGrant,
) (json.RawMessage, int, bool, error) {
	if input.KeyID == 0 || input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, core.ErrInvalidRequest
	}
	for _, cap := range input.Capabilities {
		if cap != "read" {
			return nil, 0, false, core.ErrInvalidRequest
		}
	}
	return s.runner.Run(ctx, a, core.OperationSpec{
		Name:    core.OperationAPIKeyProjectGrant,
		Target:  pid,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(a, pid, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationAPIKeyProjectGrant),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		has, err := s.apiKeys.Has(ctx, tx, a.TenantID, input.KeyID)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if !has {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		if input.Active {
			if err := s.apiKeys.VerifyMachineNativeKey(ctx, tx, a.TenantID, input.KeyID); err != nil {
				return core.OperationOutcome{}, err
			}
		}
		id := core.ValueHash(input.KeyID)
		var old core.ProjectAPIKeyGrant
		version, err := s.records.Get(ctx, tx, pid, "api_key_grant", id, &old)
		if err != nil && err != core.ErrNotFound {
			return core.OperationOutcome{}, err
		}
		if err == core.ErrNotFound {
			version = 0
		}
		input.CreatedBy = a.UserID
		input.UpdatedAt = time.Now().UTC()
		if err := s.records.Put(ctx, tx, pid, "api_key_grant", id, version, input); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := core.RecordProjectAudit(tx, a, pid, "api_key.scope", id, input.Reason); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: input, Status: core.OperationStatusOK}, nil
	})
}

// RejectOwnerTransfer refuses a pending owner transfer.
//
// Only the recipient, or the project's owner, may refuse it.
func (s *Service) RejectOwnerTransfer(
	ctx context.Context,
	a core.Actor,
	pid, id, key string,
	input core.OwnerTransferDecisionInput,
) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, core.ErrInvalidRequest
	}
	return s.runner.Run(ctx, a, core.OperationSpec{
		Name:    core.OperationRejectOwnerTransfer,
		Target:  pid + "/" + id,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(a, pid, core.AuthorizationRead, ""),
		Output:  core.OperationOutputFor(core.OperationRejectOwnerTransfer),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.OwnerTransferMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		item, err := port.OwnerTransfer(pid, id)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if item.Status != "pending" || item.ToUserID != a.UserID && !core.ProjectOwner(p, a.UserID) {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		if time.Since(item.CreatedAt) > 72*time.Hour {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		if err := s.transfers.Cancel(ctx, tx, pid, id); err != nil {
			return core.OperationOutcome{}, err
		}
		item.Status = "cancelled"
		if err := core.RecordProjectAudit(tx, a, pid, "owner.transfer.reject", id, input.Reason); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: item, Status: core.OperationStatusOK}, nil
	})
}
