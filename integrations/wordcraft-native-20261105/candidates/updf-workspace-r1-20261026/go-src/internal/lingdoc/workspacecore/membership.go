package workspacecore

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type TransferOwnerInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	NewOwnerUserID         string `json:"new_owner_user_id"`
}

// TransferOwner atomically promotes an existing project member and demotes the
// current owner, clearing the new owner's function axis as required by the
// project permission model.
func (s *Service) TransferOwner(ctx context.Context, actor Actor, projectID, key string, input TransferOwnerInput) (json.RawMessage, int, bool, error) {
	if s.mode == AuthorizationModeRollback {
		return nil, 0, false, ErrInvalidState
	}
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.NewOwnerUserID) != input.NewOwnerUserID || input.NewOwnerUserID == "" || len(input.NewOwnerUserID) > 64 || input.NewOwnerUserID == actor.UserID {
		return nil, 0, false, ErrInvalidRequest
	}
	body, status, replayed, err := runOperation(s, ctx, actor, operationInput(operationTransferOwner, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		var currentOwner, recipient *Member
		members := make([]Member, len(p.Members))
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
			return operationResult[Project]{}, ErrNotFound
		}
		active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: recipient.UserID})
		if err != nil {
			return operationResult[Project]{}, err
		}
		if !active {
			return operationResult[Project]{}, ErrInvalidState
		}
		for i := range members {
			switch members[i].UserID {
			case actor.UserID:
				members[i].Role, members[i].GovernanceRole, members[i].FunctionRoles, members[i].FunctionScopes = "collaborator", "member", nil, nil
			case recipient.UserID:
				members[i].Role, members[i].GovernanceRole, members[i].FunctionRoles, members[i].FunctionScopes = "owner", "owner", nil, nil
			}
		}
		next := p
		next.ProjectVersion++
		next.Members = members
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		if err := tx.ReplaceMembers(projectID, members); err != nil {
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
				Action: "project.owner_transfer", Target: projectID,
				Details: map[string]any{"new_owner_user_id": input.NewOwnerUserID}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
	if err == nil && !replayed {
		_ = s.recordAuditEvent(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: "manage:members", Decision: "allow", Details: map[string]any{"change": "owner_transfer", "new_owner_user_id": input.NewOwnerUserID}})
	}
	return body, status, replayed, err
}

// TransferOwnerResult exposes the updated project without transport fields.
func (s *Service) TransferOwnerResult(ctx context.Context, actor Actor, projectID, key string, input TransferOwnerInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.TransferOwner(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

// saveMemberPermissions replaces non-owner assignments in the same transaction
// as the project version and replay record. Owner changes use a separate,
// recipient-confirmed transfer workflow.
func (s *Service) saveMemberPermissions(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if s.mode == AuthorizationModeRollback {
		return nil, 0, false, ErrInvalidState
	}
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs != nil || len(input.Members) > 20 {
		return nil, 0, false, ErrInvalidRequest
	}
	body, status, replayed, err := runOperation(s, ctx, actor, operationInput(operationSaveMembers, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ChapterMutationTransaction], func(tx ChapterMutationTransaction, p Project) (operationResult[Project], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		members := make([]Member, 0, len(input.Members)+1)
		ownerID := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				if ownerID != "" {
					return operationResult[Project]{}, ErrInvalidState
				}
				ownerID = member.UserID
				members = append(members, Member{UserID: member.UserID, Role: "owner", GovernanceRole: "owner", Status: "active"})
			}
		}
		if ownerID == "" {
			return operationResult[Project]{}, ErrInvalidState
		}
		chapters, err := tx.Chapters(projectID)
		if err != nil {
			return operationResult[Project]{}, err
		}
		chapterIDs := map[string]bool{}
		for _, chapter := range chapters {
			chapterIDs[chapter.ID] = true
		}
		seen := map[string]bool{ownerID: true}
		for _, member := range input.Members {
			if member.UserID == "" || len(member.UserID) > 64 || strings.TrimSpace(member.UserID) != member.UserID || seen[member.UserID] || (member.GovernanceRole != "admin" && member.GovernanceRole != "member") {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			seen[member.UserID] = true
			if member.Role != "" && member.Role != "collaborator" {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			if member.Status != "" && member.Status != "active" && member.Status != "suspended" {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: member.UserID})
			if err != nil {
				return operationResult[Project]{}, err
			}
			if !active {
				return operationResult[Project]{}, ErrInvalidState
			}
			functions := map[string]bool{}
			for _, role := range member.FunctionRoles {
				if functions[role] || !slices.Contains([]string{"author", "researcher", "reviewer", "observer"}, role) {
					return operationResult[Project]{}, ErrInvalidRequest
				}
				functions[role] = true
			}
			for role, scopes := range member.FunctionScopes {
				if !functions[role] {
					return operationResult[Project]{}, ErrInvalidRequest
				}
				seenScopes := map[string]bool{}
				for _, scope := range scopes {
					if scope == "" || seenScopes[scope] {
						return operationResult[Project]{}, ErrInvalidRequest
					}
					// Chapter author/reviewer scopes must name an existing chapter;
					// asset/delivery identifiers use explicit resource prefixes.
					if !chapterIDs[scope] && !strings.HasPrefix(scope, "asset:") && !strings.HasPrefix(scope, "delivery:") {
						return operationResult[Project]{}, ErrInvalidRequest
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
		slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.UserID, b.UserID) })
		next := p
		next.ProjectVersion++
		next.Members = members
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		if err := tx.ReplaceMembers(projectID, members); err != nil {
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			changes := make([]map[string]any, 0, len(input.Members))
			for _, member := range input.Members {
				changes = append(changes, map[string]any{
					"user_id": member.UserID, "governance_role": member.GovernanceRole,
					"function_roles": member.FunctionRoles, "function_scopes": member.FunctionScopes,
					"status": member.Status,
				})
			}
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
				Action: "project.member_permissions.replace", Target: projectID,
				Details: map[string]any{"members": changes}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
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
		_ = s.recordAuditEvent(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: "manage:members", Decision: "allow", Details: map[string]any{"change": "member_permissions_replaced", "members": changes}})
	}
	return body, status, replayed, err
}
