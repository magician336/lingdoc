package membership

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/google/uuid"
)

// InviteProjectMember invites a user to the project as a collaborator.
//
// The invitation is a record of its own: it names the assignment it carries,
// it expires, and the collaborator it names becomes a member of the project
// only when the invitation is accepted.
func (s *Service) InviteProjectMember(ctx context.Context, actor core.Actor, pid, key string, input core.InviteMemberInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, core.ErrInvalidRequest
	}
	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationInviteMember,
		Target:  pid,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, pid, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationInviteMember),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.Transaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		if p.DiscardedAt != nil || p.Status == "archived" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		m := input.Assignment
		if err := s.validateInvitation(ctx, tx, p, m); err != nil {
			return core.OperationOutcome{}, err
		}
		active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: m.UserID})
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if !active {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		id := input.InvitationID
		version := int64(0)
		if id != "" {
			var old core.MemberInvitation
			version, err = s.records.Get(ctx, tx, pid, "invitation", id, &old)
			if err != nil {
				return core.OperationOutcome{}, err
			}
			if version != input.ExpectedInvitationVersion || old.UserID != m.UserID || old.Status != "invited" {
				return core.OperationOutcome{}, core.ErrVersionConflict
			}
		}
		if existing, exists := core.ProjectMember(p, m.UserID); exists && existing.Status != "invited" && existing.Status != "removed" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		if id == "" {
			id = uuid.NewString()
		}
		now := time.Now().UTC()
		m.Role = "collaborator"
		m.Status = "invited"
		invitation := core.MemberInvitation{ID: id, ProjectID: pid, UserID: m.UserID, Version: version + 1, Assignment: m, Status: "invited", Inviter: actor.UserID, Reason: input.Reason, CreatedAt: now, ExpiresAt: now.Add(72 * time.Hour)}
		if err := s.records.Put(ctx, tx, pid, "invitation", id, version, invitation); err != nil {
			return core.OperationOutcome{}, err
		}
		next := p
		next.ProjectVersion++
		next.Members = slices.Clone(p.Members)
		replaced := false
		for i := range next.Members {
			if next.Members[i].UserID == m.UserID {
				next.Members[i] = m
				replaced = true
			}
		}
		if !replaced {
			next.Members = append(next.Members, m)
		}
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(pid, next.Members); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := core.RecordProjectAudit(tx, actor, pid, "member.invite", m.UserID, input.Reason); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: invitation, Status: core.OperationStatusCreated}, nil
	})
}

// AcceptProjectInvitation accepts the actor's own invitation and makes the
// invited assignment the actor's member assignment.
//
// Tenant authorization is enough to view one's own invitation, never content.
func (s *Service) AcceptProjectInvitation(ctx context.Context, actor core.Actor, pid, id, key string, input core.AcceptMemberInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedInvitationVersion < 1 {
		return nil, 0, false, core.ErrInvalidRequest
	}
	var checked core.MemberInvitation
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		if err := s.authorizer.AuthorizeTenant(ctx, tx, core.NewAuthorizationRequest(actor, "", core.AuthorizationRead, "")); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, pid)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return core.ErrInvalidState
		}
		v, err := s.records.Get(ctx, tx, pid, "invitation", id, &checked)
		if err != nil {
			return err
		}
		if checked.UserID != actor.UserID {
			return core.ErrNotFound
		}
		if checked.Status == "accepted" {
			m, ok := core.ProjectMember(p, actor.UserID)
			if !ok || m.Status != "active" || checked.Version != input.ExpectedInvitationVersion || v != input.ExpectedInvitationVersion+1 {
				return core.ErrNotFound
			}
		} else if v != input.ExpectedInvitationVersion {
			return core.ErrVersionConflict
		}
		if checked.Status != "accepted" && (checked.Status != "invited" || !time.Now().UTC().Before(checked.ExpiresAt)) {
			return core.ErrInvalidState
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationAcceptMemberInvitation,
		Target:  pid + "/" + id,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, "", core.AuthorizationRead, ""),
		Output:  core.OperationOutputFor(core.OperationAcceptMemberInvitation),
	}, func(tx core.Transaction, _ core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.Transaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		p, err := port.Project(actor.TenantID, pid)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		var invitation core.MemberInvitation
		version, err := s.records.Get(ctx, tx, pid, "invitation", id, &invitation)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if version != input.ExpectedInvitationVersion || invitation.Status != "invited" || !time.Now().UTC().Before(invitation.ExpiresAt) {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		if err := s.validateInvitation(ctx, tx, p, invitation.Assignment); err != nil {
			return core.OperationOutcome{}, err
		}
		next := p
		next.ProjectVersion++
		next.Members = slices.Clone(p.Members)
		found := false
		for i := range next.Members {
			if next.Members[i].UserID == actor.UserID {
				if next.Members[i].Status != "invited" {
					return core.OperationOutcome{}, core.ErrInvalidState
				}
				next.Members[i] = invitation.Assignment
				next.Members[i].Status = "active"
				found = true
			}
		}
		if !found {
			return core.OperationOutcome{}, core.ErrNotFound
		}
		invitation.Status = "accepted"
		now := time.Now().UTC()
		invitation.AcceptedAt = &now
		if err := s.records.Put(ctx, tx, pid, "invitation", id, version, invitation); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(pid, next.Members); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := core.RecordProjectAudit(tx, actor, pid, "member.accept", actor.UserID, ""); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: invitation, Status: core.OperationStatusOK}, nil
	})
}

// ListProjectInvitations lists the invitations of one project.
//
// A manager sees every invitation of the project; anyone else sees only the
// invitations addressed to them. A stranger sees nothing at all, and the
// listing says so the same way an absent object does.
func (s *Service) ListProjectInvitations(ctx context.Context, actor core.Actor, pid string) ([]core.MemberInvitation, error) {
	var result []core.MemberInvitation
	err := s.repository.Transaction(ctx, core.TransactionOptions{ReadOnly: true}, func(tx core.Transaction) error {
		if err := s.authorizer.AuthorizeTenant(ctx, tx, core.NewAuthorizationRequest(actor, "", core.AuthorizationRead, "")); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, pid)
		if err != nil {
			return err
		}
		m, isMember := core.ProjectMember(p, actor.UserID)
		manager := isMember && (core.GovernanceRole(m) == "owner" || core.GovernanceRole(m) == "admin") && (m.Status == "" || m.Status == "active")
		if manager {
			_, err := s.authorizer.AuthorizeProject(ctx, tx, core.NewAuthorizationRequest(actor, pid, core.AuthorizationManage, ""))
			manager = err == nil
		}
		rows, err := s.records.ListRecords(ctx, tx, pid, "invitation")
		if err != nil {
			return err
		}
		result = []core.MemberInvitation{}
		for _, record := range rows {
			var item core.MemberInvitation
			if err := json.Unmarshal(record.Payload, &item); err != nil {
				return err
			}
			if manager || item.UserID == actor.UserID {
				if item.Status == "invited" && !time.Now().UTC().Before(item.ExpiresAt) {
					item.Status = "expired"
				}
				result = append(result, item)
			}
		}
		if !manager && len(result) == 0 {
			return core.ErrNotFound
		}
		return nil
	})
	return result, err
}

// ChangeMemberState moves one member between the member states.
//
// The owner is not a member state: the owner leaves by transferring the
// ownership, never by a state change.
func (s *Service) ChangeMemberState(ctx context.Context, actor core.Actor, pid, user, key string, input core.MemberLifecycleInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" || !slices.Contains([]string{"suspend", "remove", "restore", "restore_tenant"}, input.Action) {
		return nil, 0, false, core.ErrInvalidRequest
	}
	return s.runner.Run(ctx, actor, core.OperationSpec{
		Name:    core.OperationMemberLifecycle,
		Target:  pid + "/" + user,
		Key:     key,
		Body:    input,
		Request: core.NewAuthorizationRequest(actor, pid, core.AuthorizationManage, ""),
		Output:  core.OperationOutputFor(core.OperationMemberLifecycle),
	}, func(tx core.Transaction, p core.Project) (core.OperationOutcome, error) {
		port, err := core.MutationPort[core.Transaction](tx)
		if err != nil {
			return core.OperationOutcome{}, err
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return core.OperationOutcome{}, core.ErrVersionConflict
		}
		m, found := core.ProjectMember(p, user)
		if !found || core.GovernanceRole(m) == "owner" {
			return core.OperationOutcome{}, core.ErrInvalidState
		}
		switch input.Action {
		case "suspend":
			if m.Status != "active" && m.Status != "" {
				return core.OperationOutcome{}, core.ErrInvalidState
			}
			m.Status = "suspended"
		case "remove":
			if m.Status == "removed" {
				return core.OperationOutcome{}, core.ErrInvalidState
			}
			m.Status = "removed"
			m.FunctionScopes = map[string][]string{}
		case "restore", "restore_tenant":
			if input.Action == "restore" && m.Status != "suspended" || input.Action == "restore_tenant" && m.Status != "suspended_by_tenant" {
				return core.OperationOutcome{}, core.ErrInvalidState
			}
			active, err := port.ActiveMember(core.Actor{TenantID: actor.TenantID, UserID: user})
			if err != nil {
				return core.OperationOutcome{}, err
			}
			if !active {
				return core.OperationOutcome{}, core.ErrNotFound
			}
			m.Status = "active"
		}
		next := p
		next.ProjectVersion++
		next.Members = slices.Clone(p.Members)
		for i := range next.Members {
			if next.Members[i].UserID == user {
				next.Members[i] = m
			}
		}
		if err := port.UpdateProject(actor.TenantID, p, next); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := port.ReplaceMembers(pid, next.Members); err != nil {
			return core.OperationOutcome{}, err
		}
		if err := core.RecordProjectAudit(tx, actor, pid, "member."+input.Action, user, input.Reason); err != nil {
			return core.OperationOutcome{}, err
		}
		return core.OperationOutcome{Value: next, Status: core.OperationStatusOK}, nil
	})
}

// validateInvitation checks that the invited assignment is one the project may
// hold.
//
// A function scope names either a resource the project owns or a chapter the
// project holds. The resource check asks the workspace whether the project
// owns the resource; the chapter check asks the project itself.
func (s *Service) validateInvitation(ctx context.Context, tx core.Transaction, p core.Project, m core.Member) error {
	if m.UserID == "" || len(m.UserID) > 64 || strings.TrimSpace(m.UserID) != m.UserID || m.Role == "owner" || m.GovernanceRole != "member" && m.GovernanceRole != "admin" {
		return core.ErrInvalidRequest
	}
	functions := map[string]bool{}
	for _, role := range m.FunctionRoles {
		if functions[role] || !slices.Contains([]string{"author", "researcher", "reviewer", "observer"}, role) {
			return core.ErrInvalidRequest
		}
		functions[role] = true
	}
	for role, scopes := range m.FunctionScopes {
		if !functions[role] {
			return core.ErrInvalidRequest
		}
		seen := map[string]bool{}
		for _, scope := range scopes {
			if scope == "" || seen[scope] {
				return core.ErrInvalidRequest
			}
			seen[scope] = true
			if strings.HasPrefix(scope, "asset:") || strings.HasPrefix(scope, "knowledge:") || strings.HasPrefix(scope, "template:") || strings.HasPrefix(scope, "delivery:") {
				if err := s.verifyMemberResourceScope(ctx, tx, p, scope); err != nil {
					return err
				}
				continue
			}
			if p.Status == "draft" {
				return core.ErrInvalidRequest
			}
			if _, err := tx.Chapter(p.ID, scope); err != nil {
				return core.ErrNotFound
			}
		}
	}
	return nil
}

// verifyMemberResourceScope checks that a function scope names a resource the
// project holds.
//
// The template scope names the project's own template copy, so that rule
// belongs here, where the project is. The other scopes name a stored
// resource, and the workspace says whether the project holds it.
func (s *Service) verifyMemberResourceScope(ctx context.Context, tx core.Transaction, p core.Project, scope string) error {
	kind, id, found := strings.Cut(scope, ":")
	if !found || id == "" {
		return core.ErrInvalidRequest
	}
	if kind == "template" {
		if p.TemplateCopy != nil && (id == p.TemplateCopy.ID || id == p.TemplateID) {
			return nil
		}
		return core.ErrNotFound
	}
	exists, err := s.resources.ResourceExists(ctx, tx, p.ID, kind, id)
	if err != nil {
		return err
	}
	if !exists {
		return core.ErrNotFound
	}
	return nil
}
