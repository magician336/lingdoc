package workspacecore

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

type TransferOwnerInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	NewOwnerUserID         string `json:"new_owner_user_id"`
}

// TransferOwner atomically promotes an existing project member and demotes the
// current owner, clearing the new owner's function axis as required by the
// project permission model.
func (s *Service) TransferOwner(ctx context.Context, actor Actor, projectID, key string, input TransferOwnerInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.NewOwnerUserID) != input.NewOwnerUserID || input.NewOwnerUserID == "" || len(input.NewOwnerUserID) > 64 || input.NewOwnerUserID == actor.UserID {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "transferOwner", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
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
			return nil, 0, ErrNotFound
		}
		active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: recipient.UserID})
		if err != nil {
			return nil, 0, err
		}
		if !active {
			return nil, 0, ErrInvalidState
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
			return nil, 0, err
		}
		if err := tx.ReplaceMembers(projectID, members); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}

// saveMemberPermissions replaces non-owner assignments in the same transaction
// as the project version and replay record. Owner changes use a separate,
// recipient-confirmed transfer workflow.
func (s *Service) saveMemberPermissions(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs != nil || len(input.Members) > 20 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "saveMembers", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		members := make([]Member, 0, len(input.Members)+1)
		ownerID := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				if ownerID != "" {
					return nil, 0, ErrInvalidState
				}
				ownerID = member.UserID
				members = append(members, Member{UserID: member.UserID, Role: "owner", GovernanceRole: "owner", Status: "active"})
			}
		}
		if ownerID == "" {
			return nil, 0, ErrInvalidState
		}
		chapters, err := tx.Chapters(projectID)
		if err != nil {
			return nil, 0, err
		}
		chapterIDs := map[string]bool{}
		for _, chapter := range chapters {
			chapterIDs[chapter.ID] = true
		}
		seen := map[string]bool{ownerID: true}
		for _, member := range input.Members {
			if member.UserID == "" || len(member.UserID) > 64 || strings.TrimSpace(member.UserID) != member.UserID || seen[member.UserID] || (member.GovernanceRole != "admin" && member.GovernanceRole != "member") {
				return nil, 0, ErrInvalidRequest
			}
			seen[member.UserID] = true
			if member.Role != "" && member.Role != "collaborator" {
				return nil, 0, ErrInvalidRequest
			}
			if member.Status != "" && member.Status != "active" && member.Status != "suspended" {
				return nil, 0, ErrInvalidRequest
			}
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: member.UserID})
			if err != nil {
				return nil, 0, err
			}
			if !active {
				return nil, 0, ErrInvalidState
			}
			functions := map[string]bool{}
			for _, role := range member.FunctionRoles {
				if functions[role] || !slices.Contains([]string{"author", "researcher", "reviewer", "observer"}, role) {
					return nil, 0, ErrInvalidRequest
				}
				functions[role] = true
			}
			for role, scopes := range member.FunctionScopes {
				if !functions[role] {
					return nil, 0, ErrInvalidRequest
				}
				seenScopes := map[string]bool{}
				for _, scope := range scopes {
					if scope == "" || seenScopes[scope] {
						return nil, 0, ErrInvalidRequest
					}
					// Chapter author/reviewer scopes must name an existing chapter;
					// asset/delivery identifiers use explicit resource prefixes.
					if !chapterIDs[scope] && !strings.HasPrefix(scope, "asset:") && !strings.HasPrefix(scope, "delivery:") {
						return nil, 0, ErrInvalidRequest
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
			return nil, 0, err
		}
		if err := tx.ReplaceMembers(projectID, members); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}
