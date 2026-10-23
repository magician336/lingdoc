package membership

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// SaveMembersResult exposes the updated project without transport fields.
func (s *Service) SaveMembersResult(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.SaveMembersInput,
) (core.ProjectMutationResult, error) {
	project, replayed, err := core.AdaptMutationResult[core.Project](func() (json.RawMessage, int, bool, error) {
		return s.SaveMembers(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return core.ProjectMutationResult{Replayed: replayed}, err
	}
	return core.ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

// TransferOwnerResult exposes the updated project without transport fields.
func (s *Service) TransferOwnerResult(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.TransferOwnerInput,
) (core.ProjectMutationResult, error) {
	project, replayed, err := core.AdaptMutationResult[core.Project](func() (json.RawMessage, int, bool, error) {
		return s.TransferOwner(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return core.ProjectMutationResult{Replayed: replayed}, err
	}
	return core.ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

// RequestOwnerTransferResult exposes the transfer request without transport
// fields. The HTTP adapter retains the established 201 status.
func (s *Service) RequestOwnerTransferResult(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.OwnerTransferInput,
) (core.OwnerTransferResult, error) {
	transfer, replayed, err := core.AdaptMutationResult[core.OwnerTransfer](func() (json.RawMessage, int, bool, error) {
		return s.RequestOwnerTransfer(ctx, actor, projectID, key, input)
	}, "owner transfer result")
	if err != nil {
		return core.OwnerTransferResult{Replayed: replayed}, err
	}
	return core.OwnerTransferResult{Transfer: transfer, Replayed: replayed}, nil
}

// AcceptOwnerTransferResult exposes the accepted transfer without transport
// fields.
func (s *Service) AcceptOwnerTransferResult(
	ctx context.Context,
	actor core.Actor,
	projectID, transferID, key string,
) (core.OwnerTransferResult, error) {
	transfer, replayed, err := core.AdaptMutationResult[core.OwnerTransfer](func() (json.RawMessage, int, bool, error) {
		return s.AcceptOwnerTransfer(ctx, actor, projectID, transferID, key)
	}, "owner transfer result")
	if err != nil {
		return core.OwnerTransferResult{Replayed: replayed}, err
	}
	return core.OwnerTransferResult{Transfer: transfer, Replayed: replayed}, nil
}

// SaveMembers replaces the project's collaborators.
//
// The collaborator list is one shape of the same decision as the member
// assignment list: either one is given, never both.
func (s *Service) SaveMembers(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.SaveMembersInput,
) (json.RawMessage, int, bool, error) {
	if s.policy.RequiresActivationRead() {
		return nil, 0, false, core.ErrProjectInvitationRequired
	}
	if input.Members != nil {
		return s.saveMemberPermissions(ctx, actor, projectID, key, input)
	}
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs == nil || len(input.CollaboratorUserIDs) > 20 {
		return nil, 0, false, core.ErrInvalidRequest
	}
	ids := slices.Clone(input.CollaboratorUserIDs)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, 0, false, core.ErrInvalidRequest
	}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 64 || id == actor.UserID {
			return nil, 0, false, core.ErrInvalidRequest
		}
	}
	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationSaveMembers,
		Target:  projectID,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, projectID, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationSaveMembers),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.ProjectMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		for _, id := range ids {
			active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: id})
			if err != nil {
				return core.OperationOutcome{}, err
			}
			if !active {
				return core.OperationOutcome{}, core.ErrInvalidState
			}
		}
		existing := []string{}
		members := []core.Member{}
		for _, m := range p.Members {
			if m.Role == "collaborator" {
				existing = append(existing, m.UserID)
			} else {
				members = append(members, m)
			}
		}
		slices.Sort(existing)
		if !slices.Equal(existing, ids) {
			next := p
			next.ProjectVersion++
			if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
				return core.OperationOutcome{}, err
			}
			if err := port.ReplaceCollaborators(projectID, ids); err != nil {
				return core.OperationOutcome{}, err
			}
			if audit, ok := tx.(core.AuditTransaction); ok {
				if err := audit.RecordAudit(core.AuditEvent{
					ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
					Action: "project.member_permissions.replace", Target: projectID,
					Details: map[string]any{"collaborator_user_ids": ids}, CreatedAt: time.Now().UTC(),
				}); err != nil {
					return core.OperationOutcome{}, err
				}
			}
			for _, id := range ids {
				members = append(members, core.Member{UserID: id, Role: "collaborator"})
			}
			slices.SortFunc(members, func(a, b core.Member) int { return strings.Compare(a.UserID, b.UserID) })
			next.Members = members
			p = next
		}
		return core.OperationOutcome{Value: p, Status: core.OperationStatusOK}, nil
	})
}

// saveMemberPermissions replaces non-owner assignments in the same transaction
// as the project version and replay record. Owner changes use a separate,
// recipient-confirmed transfer workflow.
func (s *Service) saveMemberPermissions(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.SaveMembersInput,
) (json.RawMessage, int, bool, error) {
	if s.policy.AuthorizationMode() == core.AuthorizationModeRollback {
		return nil, 0, false, core.ErrInvalidState
	}
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs != nil || len(input.Members) > 20 {
		return nil, 0, false, core.ErrInvalidRequest
	}
	body, status, replayed, err := s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationSaveMembers,
		Target:  projectID,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, projectID, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationSaveMembers),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.ChapterMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		members := make([]core.Member, 0, len(input.Members)+1)
		ownerID := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				if ownerID != "" {
					return core.OperationOutcome{}, core.ErrInvalidState
				}
				ownerID = member.UserID
				members = append(members, core.Member{
					UserID: member.UserID, Role: "owner", GovernanceRole: "owner", Status: "active",
				})
			}
		}
		if ownerID == "" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		chapters, err := port.Chapters(projectID)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		chapterIDs := map[string]bool{}
		for _, chapter := range chapters {
			chapterIDs[chapter.ID] = true
		}
		seen := map[string]bool{ownerID: true}
		for _, member := range input.Members {
			if member.UserID == "" || len(member.UserID) > 64 ||
				strings.TrimSpace(member.UserID) != member.UserID || seen[member.UserID] ||
				(member.GovernanceRole != "admin" && member.GovernanceRole != "member") {
				return core.OperationOutcome{}, core.ErrInvalidRequest
			}
			seen[member.UserID] = true
			if member.Role != "" && member.Role != "collaborator" {
				return core.OperationOutcome{}, core.ErrInvalidRequest
			}
			if member.Status != "" && member.Status != "active" && member.Status != "suspended" {
				return core.OperationOutcome{}, core.ErrInvalidRequest
			}
			active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: member.UserID})
			if err != nil {
				return core.OperationOutcome{}, err
			}
			if !active {
				return core.OperationOutcome{}, core.ErrInvalidState
			}
			functions := map[string]bool{}
			for _, role := range member.FunctionRoles {
				if functions[role] || !slices.Contains([]string{"author", "researcher", "reviewer", "observer"}, role) {
					return core.OperationOutcome{}, core.ErrInvalidRequest
				}
				functions[role] = true
			}
			for role, scopes := range member.FunctionScopes {
				if !functions[role] {
					return core.OperationOutcome{}, core.ErrInvalidRequest
				}
				seenScopes := map[string]bool{}
				for _, scope := range scopes {
					if scope == "" || seenScopes[scope] {
						return core.OperationOutcome{}, core.ErrInvalidRequest
					}
					// Chapter author/reviewer scopes must name an existing chapter;
					// asset/delivery identifiers use explicit resource prefixes.
					if !chapterIDs[scope] && !strings.HasPrefix(scope, "asset:") &&
						!strings.HasPrefix(scope, "delivery:") {
						return core.OperationOutcome{}, core.ErrInvalidRequest
					}
					seenScopes[scope] = true
				}
			}
			member.Role = "collaborator"
			if member.Status == "" {
				member.Status = "active"
			}
			members = append(members, member)
		}
		slices.SortFunc(members, func(a, b core.Member) int { return strings.Compare(a.UserID, b.UserID) })
		next := p
		next.ProjectVersion++
		next.Members = members
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(projectID, members); err != nil {
			return core.OperationOutcome{}, err
		}
		if audit, ok := tx.(core.AuditTransaction); ok {
			changes := make([]map[string]any, 0, len(input.Members))
			for _, member := range input.Members {
				changes = append(changes, map[string]any{
					"user_id": member.UserID, "governance_role": member.GovernanceRole,
					"function_roles": member.FunctionRoles, "function_scopes": member.FunctionScopes,
					"status": member.Status,
				})
			}
			if err := audit.RecordAudit(core.AuditEvent{
				ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
				Action: "project.member_permissions.replace", Target: projectID,
				Details: map[string]any{"members": changes}, CreatedAt: time.Now().UTC(),
			}); err != nil {
				return core.OperationOutcome{}, err
			}
		}
		return core.OperationOutcome{Value: next, Status: core.OperationStatusOK}, nil
	})
	if err == nil && !replayed {
		changes := make([]map[string]any, 0, len(input.Members))
		for _, member := range input.Members {
			changes = append(changes, map[string]any{
				"user_id": member.UserID, "governance_role": member.GovernanceRole,
				"function_roles": member.FunctionRoles, "function_scopes": member.FunctionScopes,
				"status": member.Status,
			})
		}
		_ = s.policy.RecordAuditEvent(ctx, core.AuditEvent{
			TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID,
			Capability: "manage:members", Decision: "allow",
			Details: map[string]any{"change": "member_permissions_replaced", "members": changes},
		})
	}
	return body, status, replayed, err
}

// requireOwnerDecision refuses an owner transfer while the project's Owner
// may not act, and names the freeze.
//
// The transfer is an Owner-only write like the five decisions in the
// workspace core, so it carries the same freeze. The ordinary authorization
// alone does not say so: it answers a lapsed Owner with a plain "not found",
// which is exactly the answer the caller cannot act on. The gate is asked
// before the operation runs, because the operation's own authorization is
// what refuses first.
//
// It stays silent for anybody but the Owner. The tenant Owner who recovers a
// stranded project is not the project's Owner, so the freeze is not their
// reason for being refused, and the recovery keeps working.
func (s *Service) requireOwnerDecision(ctx context.Context, actor core.Actor, projectID string) error {
	return core.OwnerFreezeRefusal(ctx, s.repository, actor, projectID)
}

// TransferOwner atomically promotes an existing project member and demotes the
// current owner, clearing the new owner's function axis as required by the
// project permission model.
func (s *Service) TransferOwner(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.TransferOwnerInput,
) (json.RawMessage, int, bool, error) {
	if s.policy.AuthorizationMode() == core.AuthorizationModeRollback {
		return nil, 0, false, core.ErrInvalidState
	}
	if input.ExpectedProjectVersion < 1 ||
		strings.TrimSpace(input.NewOwnerUserID) != input.NewOwnerUserID ||
		input.NewOwnerUserID == "" || len(input.NewOwnerUserID) > 64 ||
		input.NewOwnerUserID == actor.UserID {
		return nil, 0, false, core.ErrInvalidRequest
	}
	// The transfer is the Owner's own decision, so it freezes while the
	// Owner's identity has lapsed, and the refusal says why.
	if err := s.requireOwnerDecision(ctx, actor, projectID); err != nil {
		return nil, 0, false, err
	}
	body, status, replayed, err := s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationTransferOwner,
		Target:  projectID,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, projectID, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationTransferOwner),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.ProjectMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		var currentOwner, recipient *core.Member
		members := make([]core.Member, len(p.Members))
		copy(members, p.Members)
		for i := range members {
			if members[i].Role == "owner" {
				currentOwner = &members[i]
			}
			if members[i].UserID == input.NewOwnerUserID {
				recipient = &members[i]
			}
		}
		if currentOwner == nil || currentOwner.UserID != actor.UserID || recipient == nil || recipient.Role == "owner" {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: recipient.UserID})
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if !active {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		for i := range members {
			switch members[i].UserID {
			case actor.UserID:
				members[i].Role, members[i].GovernanceRole,
					members[i].FunctionRoles, members[i].FunctionScopes = "collaborator", "member", nil, nil
			case recipient.UserID:
				members[i].Role, members[i].GovernanceRole,
					members[i].FunctionRoles, members[i].FunctionScopes = "owner", "owner", nil, nil
			}
		}
		next := p
		next.ProjectVersion++
		next.Members = members
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(projectID, members); err != nil {
			return core.OperationOutcome{}, err
		}
		if audit, ok := tx.(core.AuditTransaction); ok {
			if err := audit.RecordAudit(core.AuditEvent{
				ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
				Action: "project.owner_transfer", Target: projectID,
				Details: map[string]any{"new_owner_user_id": input.NewOwnerUserID}, CreatedAt: time.Now().UTC(),
			}); err != nil {
				return core.OperationOutcome{}, err
			}
		}
		return core.OperationOutcome{Value: next, Status: core.OperationStatusOK}, nil
	})
	if err == nil && !replayed {
		_ = s.policy.RecordAuditEvent(ctx, core.AuditEvent{
			TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID,
			Capability: "manage:members", Decision: "allow",
			Details: map[string]any{"change": "owner_transfer", "new_owner_user_id": input.NewOwnerUserID},
		})
	}
	return body, status, replayed, err
}

// RequestOwnerTransfer asks the owner to hand the ownership over.
//
// The transfer is a request, not a transfer: the recipient confirms it, and
// only then does the ownership move.
func (s *Service) RequestOwnerTransfer(
	ctx context.Context,
	actor core.Actor,
	projectID, key string,
	input core.OwnerTransferInput,
) (json.RawMessage, int, bool, error) {
	input.ToUserID = strings.TrimSpace(input.ToUserID)
	if input.ToUserID == "" || input.ToUserID == actor.UserID || input.ExpectedProjectVersion < 1 {
		return nil, 0, false, core.ErrInvalidRequest
	}
	// The request is the Owner's own decision, so it freezes while the
	// Owner's identity has lapsed, and the refusal says why. A stranded
	// project is recovered by the tenant Owner, not by the unavailable Owner.
	if err := s.requireOwnerDecision(ctx, actor, projectID); err != nil {
		return nil, 0, false, err
	}
	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationRequestOwnerTransfer,
		Target:  projectID,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, "", core.AuthorizationRead, ""),
		Output:  core.OperationOutputFor(core.OperationRequestOwnerTransfer),
	}, func(tx core.Transaction, _ core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.OwnerTransferMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		featureTx, featureErr := core.ExtendedTransaction(port)
		if featureErr != nil {
			return core.OperationOutcome{}, featureErr
		}

		p, err := port.Project(actor.TenantID, projectID)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		currentOwner := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				currentOwner = member.UserID
				break
			}
		}
		if currentOwner == "" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		// The Owner's availability is one fact, and it is derived in one
		// place. This used to be a second copy of the same conditions, and
		// the two copies did not hold the same conditions.
		authority := core.OwnerAuthorityOf(featureTx, p, actor.TenantID)
		ownerAvailable := authority.Available
		role := string(actor.Role)
		if role == "" {
			role = actor.TenantRole
		}
		currentRole, roleErr := core.CurrentTenantRole(featureTx, actor)
		if roleErr != nil {
			return core.OperationOutcome{}, roleErr
		}
		_ = role
		isRecoveryAdmin := currentRole == types.TenantRoleOwner
		if actor.UserID != currentOwner && (!isRecoveryAdmin || ownerAvailable) {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.Status == "archived" {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		if p.DiscardedAt != nil {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: input.ToUserID})
		if err != nil || !active {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		if s.policy.RequiresActivationRead() {
			member, ok := core.ProjectMember(p, input.ToUserID)
			if !ok || member.Status != "" && member.Status != "active" {
				return core.OperationOutcome{}, core.ErrNotFound
			}
		}
		transfer := core.OwnerTransfer{
			ID: uuid.NewString(), ProjectID: projectID, FromUserID: currentOwner, ToUserID: input.ToUserID,
			Status: "pending", ExpectedProjectVersion: p.ProjectVersion, CreatedAt: time.Now().UTC(),
		}
		if err := port.InsertOwnerTransfer(transfer); err != nil {
			return core.OperationOutcome{}, err
		}
		if s.policy.RequiresActivationRead() {
			if err := s.recordOwnerTransferOrigin(ctx, featureTx, actor, p, transfer); err != nil {
				return core.OperationOutcome{}, err
			}
		}
		return core.OperationOutcome{Value: transfer, Status: core.OperationStatusCreated}, nil
	})
}

// AcceptOwnerTransfer accepts an owner transfer and moves the ownership.
func (s *Service) AcceptOwnerTransfer(
	ctx context.Context,
	actor core.Actor,
	projectID, transferID, key string,
) (json.RawMessage, int, bool, error) {
	if s.policy.RequiresActivationRead() {
		if err := core.RequireHumanDecision(ctx); err != nil {
			return nil, 0, false, err
		}
		if err := s.policy.Authorize(ctx, actor, projectID, "read"); err != nil {
			return nil, 0, false, err
		}
	}

	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationAcceptOwnerTransfer,
		Target:  projectID + "/" + transferID,
		Key:     key,
		Body:    map[string]string{"transfer_id": transferID},
		Request: core.NewAuthorizationRequest(actor, "", core.AuthorizationRead, ""),
		Output:  core.OperationOutputFor(core.OperationAcceptOwnerTransfer),
	}, func(tx core.Transaction, _ core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.OwnerTransferMutationTransaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		featureTx, featureErr := core.ExtendedTransaction(port)
		if featureErr != nil {
			return core.OperationOutcome{}, featureErr
		}

		if !core.ValidActor(actor) {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		p, err := port.Project(actor.TenantID, projectID)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.DiscardedAt != nil {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		transfer, err := port.OwnerTransfer(projectID, transferID)
		if err != nil || transfer.Status != "pending" || transfer.ToUserID != actor.UserID {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		if time.Since(transfer.CreatedAt) > 72*time.Hour {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		if s.policy.RequiresActivationRead() {
			m, ok := core.ProjectMember(p, actor.UserID)
			if !ok || m.Status != "" && m.Status != "active" {
				return core.OperationOutcome{}, core.ErrNotFound
			}
		}
		if transfer.ExpectedProjectVersion != p.ProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		if s.policy.RequiresActivationRead() {
			if err := s.verifyOwnerTransferOrigin(ctx, featureTx, actor, p, transfer); err != nil {
				return core.OperationOutcome{}, err
			}
		}

		active, err := port.ActiveMember(actor)
		if err != nil || !active {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		if err := port.EnsureMember(projectID, actor.UserID, "collaborator"); err != nil {
			return core.OperationOutcome{}, err
		}
		next := p
		next.ProjectVersion++
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.SetMemberRole(projectID, transfer.FromUserID, "collaborator"); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.SetMemberRole(projectID, actor.UserID, "owner"); err != nil {
			return core.OperationOutcome{}, err
		}
		acceptedAt := time.Now().UTC()
		if err := port.AcceptOwnerTransfer(
			projectID, transferID, transfer.ExpectedProjectVersion, acceptedAt,
		); err != nil {
			return core.OperationOutcome{}, err
		}
		if s.policy.RequiresActivationRead() {
			if err := core.RecordProjectAudit(
				featureTx, actor, p.ID, "owner.transfer.accept", transfer.ID, "",
			); err != nil {
				return core.OperationOutcome{}, err
			}
		}
		transfer.Status = "accepted"
		transfer.AcceptedAt = &acceptedAt
		return core.OperationOutcome{Value: transfer, Status: core.OperationStatusOK}, nil
	})
}

// ownerTransferOrigin records who asked for an owner transfer, and whether the
// transfer is a recovery of an unavailable owner.
type ownerTransferOrigin struct {
	RequesterID string `json:"requester_id"`
	Recovery    bool   `json:"recovery"`
}

// recordOwnerTransferOrigin writes the origin of an owner transfer request.
func (s *Service) recordOwnerTransferOrigin(
	ctx context.Context,
	tx core.Transaction,
	a core.Actor,
	p core.Project,
	transfer core.OwnerTransfer,
) error {
	origin := ownerTransferOrigin{RequesterID: a.UserID, Recovery: a.UserID != transfer.FromUserID}
	if err := s.records.Put(ctx, tx, p.ID, "owner_transfer_origin", transfer.ID, 0, origin); err != nil {
		return err
	}
	return core.RecordProjectAudit(tx, a, p.ID, "owner.transfer.request", transfer.ID, "")
}

// verifyOwnerTransferOrigin checks that an owner transfer was asked for by the
// owner it names, or recovered by a tenant owner while the owner was
// unavailable.
func (s *Service) verifyOwnerTransferOrigin(
	ctx context.Context,
	tx core.Transaction,
	a core.Actor,
	p core.Project,
	transfer core.OwnerTransfer,
) error {
	if err := core.RequireHumanDecision(ctx); err != nil {
		return err
	}
	var origin ownerTransferOrigin
	if _, err := s.records.Get(ctx, tx, p.ID, "owner_transfer_origin", transfer.ID, &origin); err != nil {
		return core.ErrInvalidState
	}
	if origin.Recovery {
		if core.OwnerCurrentlyAvailable(tx, p, a.TenantID, transfer.FromUserID) {
			return core.ErrInvalidState
		}
		role, err := core.CurrentTenantRole(tx, core.Actor{TenantID: a.TenantID, UserID: origin.RequesterID})
		if err != nil || role != types.TenantRoleOwner {
			return core.ErrNotFound
		}
	} else if origin.RequesterID != transfer.FromUserID ||
		!core.OwnerCurrentlyAvailable(tx, p, a.TenantID, transfer.FromUserID) {
		return core.ErrInvalidState
	}
	role, err := core.CurrentTenantRole(tx, a)
	if err != nil || role != "" && !role.HasPermission(types.TenantRoleContributor) {
		return core.ErrNotFound
	}
	return nil
}
