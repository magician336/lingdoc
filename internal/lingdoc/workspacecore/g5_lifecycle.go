package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"slices"
	"strings"
	"time"
)

type MemberInvitation struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	UserID     string     `json:"user_id"`
	Version    int64      `json:"version"`
	Assignment Member     `json:"assignment"`
	Status     string     `json:"status"`
	Inviter    string     `json:"inviter"`
	Reason     string     `json:"reason"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}
type InviteMemberInput struct {
	ExpectedProjectVersion    int64  `json:"expected_project_version"`
	Assignment                Member `json:"assignment"`
	Reason                    string `json:"reason"`
	InvitationID              string `json:"invitation_id,omitempty"`
	ExpectedInvitationVersion int64  `json:"expected_invitation_version,omitempty"`
}
type AcceptMemberInput struct {
	ExpectedInvitationVersion int64 `json:"expected_invitation_version"`
}
type MemberLifecycleInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Action                 string `json:"action"`
	Reason                 string `json:"reason"`
}
type ActivationReadReceipt struct {
	ID             string    `json:"id"`
	ActorID        string    `json:"actor_id"`
	ProjectID      string    `json:"project_id"`
	ProjectVersion int64     `json:"project_version"`
	SpecRevision   int64     `json:"spec_revision"`
	ReadAt         time.Time `json:"read_at"`
}
type g5RecordRow struct {
	ProjectID   string `gorm:"primaryKey"`
	Kind        string `gorm:"primaryKey"`
	ID          string `gorm:"primaryKey"`
	Version     int64
	PayloadJSON string
}

func (g5RecordRow) TableName() string { return "lingdoc_g5_records" }

type g5Transaction interface {
	G5Get(string, string, string, any) (int64, error)
	G5Put(string, string, string, int64, any) error
	G5List(string, string) ([]g5RecordRow, error)
}

func (t gormTransaction) G5Get(p, k, id string, dst any) (int64, error) {
	var row g5RecordRow
	err := t.db.Where("project_id = ? AND kind = ? AND id = ?", p, k, id).Take(&row).Error
	if err != nil {
		return 0, storageError(err)
	}
	return row.Version, json.Unmarshal([]byte(row.PayloadJSON), dst)
}
func (t gormTransaction) G5Put(p, k, id string, expected int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if expected == 0 {
		return t.db.Create(&g5RecordRow{ProjectID: p, Kind: k, ID: id, Version: 1, PayloadJSON: string(raw)}).Error
	}
	result := t.db.Model(&g5RecordRow{}).Where("project_id = ? AND kind = ? AND id = ? AND version = ?", p, k, id, expected).Updates(map[string]any{"version": expected + 1, "payload_json": string(raw)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}
func (t gormTransaction) G5List(p, k string) ([]g5RecordRow, error) {
	var rows []g5RecordRow
	err := t.db.Where("project_id = ? AND kind = ?", p, k).Order("id").Find(&rows).Error
	return rows, err
}
func g5Audit(tx Transaction, actor Actor, p, action, target, reason string) error {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return ErrInvalidState
	}
	return audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: p, ActorID: actor.UserID, UserID: actor.UserID, TenantID: actor.TenantID, Action: action, Target: target, Reason: reason, Decision: "allow", CreatedAt: time.Now().UTC()})
}
func validateInvitation(tx Transaction, p Project, m Member) error {
	if m.UserID == "" || len(m.UserID) > 64 || strings.TrimSpace(m.UserID) != m.UserID || m.Role == "owner" || m.GovernanceRole != "member" && m.GovernanceRole != "admin" {
		return ErrInvalidRequest
	}
	functions := map[string]bool{}
	for _, role := range m.FunctionRoles {
		if functions[role] || !slices.Contains([]string{"author", "researcher", "reviewer", "observer"}, role) {
			return ErrInvalidRequest
		}
		functions[role] = true
	}
	for role, scopes := range m.FunctionScopes {
		if !functions[role] {
			return ErrInvalidRequest
		}
		seen := map[string]bool{}
		for _, scope := range scopes {
			if scope == "" || seen[scope] {
				return ErrInvalidRequest
			}
			seen[scope] = true
			if strings.HasPrefix(scope, "asset:") || strings.HasPrefix(scope, "knowledge:") || strings.HasPrefix(scope, "template:") || strings.HasPrefix(scope, "delivery:") {
				if err := verifyMemberResourceScope(tx, p, scope); err != nil {
					return err
				}
				continue
			}
			if p.Status == "draft" {
				return ErrInvalidRequest
			}
			if _, err := tx.Chapter(p.ID, scope); err != nil {
				return ErrNotFound
			}
		}
	}
	return nil
}
func (s *Service) InviteProjectMember(ctx context.Context, actor Actor, pid, key string, input InviteMemberInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationInviteMember, pid, key, input, authorizationRequest(actor, pid, AuthorizationManage, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[MemberInvitation], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[MemberInvitation]{}, ErrVersionConflict
		}
		if p.DiscardedAt != nil || p.Status == "archived" {
			return operationResult[MemberInvitation]{}, ErrInvalidState
		}
		m := input.Assignment
		if err := validateInvitation(tx, p, m); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: m.UserID})
		if err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if !active {
			return operationResult[MemberInvitation]{}, ErrNotFound
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return operationResult[MemberInvitation]{}, ErrInvalidState
		}
		id := input.InvitationID
		version := int64(0)
		if id != "" {
			var old MemberInvitation
			version, err = store.G5Get(pid, "invitation", id, &old)
			if err != nil {
				return operationResult[MemberInvitation]{}, err
			}
			if version != input.ExpectedInvitationVersion || old.UserID != m.UserID || old.Status != "invited" {
				return operationResult[MemberInvitation]{}, ErrVersionConflict
			}
		}
		if existing, exists := projectMember(p, m.UserID); exists && existing.Status != "invited" && existing.Status != "removed" {
			return operationResult[MemberInvitation]{}, ErrInvalidState
		}
		if id == "" {
			id = uuid.NewString()
		}
		now := time.Now().UTC()
		m.Role = "collaborator"
		m.Status = "invited"
		invitation := MemberInvitation{ID: id, ProjectID: pid, UserID: m.UserID, Version: version + 1, Assignment: m, Status: "invited", Inviter: actor.UserID, Reason: input.Reason, CreatedAt: now, ExpiresAt: now.Add(72 * time.Hour)}
		if err := store.G5Put(pid, "invitation", id, version, invitation); err != nil {
			return operationResult[MemberInvitation]{}, err
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
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if err := tx.ReplaceMembers(pid, next.Members); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if err := g5Audit(tx, actor, pid, "member.invite", m.UserID, input.Reason); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		return operationResult[MemberInvitation]{Value: invitation, Status: operationStatusCreated}, nil
	})
}
func (s *Service) AcceptProjectInvitation(ctx context.Context, actor Actor, pid, id, key string, input AcceptMemberInput) (json.RawMessage, int, bool, error) {
	// Tenant authorization is enough to view one's own invitation, never content.
	if input.ExpectedInvitationVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	var checked MemberInvitation
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if err := s.authorizeTenant(ctx, tx, authorizationRequest(actor, "", AuthorizationRead, "")); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, pid)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrInvalidState
		}
		v, err := store.G5Get(pid, "invitation", id, &checked)
		if err != nil {
			return err
		}
		if checked.UserID != actor.UserID {
			return ErrNotFound
		}
		if checked.Status == "accepted" {
			m, ok := projectMember(p, actor.UserID)
			if !ok || m.Status != "active" || checked.Version != input.ExpectedInvitationVersion || v != input.ExpectedInvitationVersion+1 {
				return ErrNotFound
			}
		} else if v != input.ExpectedInvitationVersion {
			return ErrVersionConflict
		}
		if checked.Status != "accepted" && (checked.Status != "invited" || !time.Now().UTC().Before(checked.ExpiresAt)) {
			return ErrInvalidState
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, operationInput(operationAcceptMemberInvitation, pid+"/"+id, key, input, authorizationRequest(actor, "", AuthorizationRead, "")), mutationPort[Transaction], func(tx Transaction, _ Project) (operationResult[MemberInvitation], error) {
		p, err := tx.Project(actor.TenantID, pid)
		if err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		store := tx.(g5Transaction)
		var invitation MemberInvitation
		version, err := store.G5Get(pid, "invitation", id, &invitation)
		if err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if version != input.ExpectedInvitationVersion || invitation.Status != "invited" || !time.Now().UTC().Before(invitation.ExpiresAt) {
			return operationResult[MemberInvitation]{}, ErrVersionConflict
		}
		if err := validateInvitation(tx, p, invitation.Assignment); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		next := p
		next.ProjectVersion++
		next.Members = slices.Clone(p.Members)
		found := false
		for i := range next.Members {
			if next.Members[i].UserID == actor.UserID {
				if next.Members[i].Status != "invited" {
					return operationResult[MemberInvitation]{}, ErrInvalidState
				}
				next.Members[i] = invitation.Assignment
				next.Members[i].Status = "active"
				found = true
			}
		}
		if !found {
			return operationResult[MemberInvitation]{}, ErrNotFound
		}
		invitation.Status = "accepted"
		now := time.Now().UTC()
		invitation.AcceptedAt = &now
		if err := store.G5Put(pid, "invitation", id, version, invitation); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if err := tx.ReplaceMembers(pid, next.Members); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		if err := g5Audit(tx, actor, pid, "member.accept", actor.UserID, ""); err != nil {
			return operationResult[MemberInvitation]{}, err
		}
		return operationResult[MemberInvitation]{Value: invitation, Status: operationStatusOK}, nil
	})
}
func (s *Service) ListProjectInvitations(ctx context.Context, actor Actor, pid string) ([]MemberInvitation, error) {
	var result []MemberInvitation
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if err := s.authorizeTenant(ctx, tx, authorizationRequest(actor, "", AuthorizationRead, "")); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, pid)
		if err != nil {
			return err
		}
		m, isMember := projectMember(p, actor.UserID)
		manager := isMember && (governanceRole(m) == "owner" || governanceRole(m) == "admin") && (m.Status == "" || m.Status == "active")
		if manager {
			_, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationManage, ""))
			manager = err == nil
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrInvalidState
		}
		rows, err := store.G5List(pid, "invitation")
		if err != nil {
			return err
		}
		result = []MemberInvitation{}
		for _, row := range rows {
			var item MemberInvitation
			if err := json.Unmarshal([]byte(row.PayloadJSON), &item); err != nil {
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
			return ErrNotFound
		}
		return nil
	})
	return result, err
}
func (s *Service) ChangeMemberState(ctx context.Context, actor Actor, pid, user, key string, input MemberLifecycleInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" || !slices.Contains([]string{"suspend", "remove", "restore", "restore_tenant"}, input.Action) {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationMemberLifecycle, pid+"/"+user, key, input, authorizationRequest(actor, pid, AuthorizationManage, "")), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[Project], error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		m, found := projectMember(p, user)
		if !found || governanceRole(m) == "owner" {
			return operationResult[Project]{}, ErrInvalidState
		}
		switch input.Action {
		case "suspend":
			if m.Status != "active" && m.Status != "" {
				return operationResult[Project]{}, ErrInvalidState
			}
			m.Status = "suspended"
		case "remove":
			if m.Status == "removed" {
				return operationResult[Project]{}, ErrInvalidState
			}
			m.Status = "removed"
			m.FunctionScopes = map[string][]string{}
		case "restore", "restore_tenant":
			if input.Action == "restore" && m.Status != "suspended" || input.Action == "restore_tenant" && m.Status != "suspended_by_tenant" {
				return operationResult[Project]{}, ErrInvalidState
			}
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: user})
			if err != nil {
				return operationResult[Project]{}, err
			}
			if !active {
				return operationResult[Project]{}, ErrNotFound
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
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		if err := tx.ReplaceMembers(pid, next.Members); err != nil {
			return operationResult[Project]{}, err
		}
		if err := g5Audit(tx, actor, pid, "member."+input.Action, user, input.Reason); err != nil {
			return operationResult[Project]{}, err
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// CaptureTenantSuspension is invoked by the tenant membership change adapter.
// It never grants access; recovery requires ChangeMemberState(restore_tenant).
func (s *Service) CaptureTenantSuspension(ctx context.Context, tenantID uint64, userID string) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		projects, err := tx.Projects(Actor{TenantID: tenantID, UserID: userID}, 10000)
		if err != nil {
			return err
		}
		for _, p := range projects {
			m, _ := projectMember(p, userID)
			if governanceRole(m) == "owner" || m.Status != "" && m.Status != "active" {
				continue
			}
			next := p
			next.ProjectVersion++
			next.Members = slices.Clone(p.Members)
			for i := range next.Members {
				if next.Members[i].UserID == userID {
					next.Members[i].Status = "suspended_by_tenant"
				}
			}
			if err := tx.UpdateProject(tenantID, p, next); err != nil {
				return err
			}
			if err := tx.ReplaceMembers(p.ID, next.Members); err != nil {
				return err
			}
			if err := g5Audit(tx, Actor{TenantID: tenantID, UserID: userID}, p.ID, "member.tenant_suspended", userID, "tenant_membership_changed"); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Service) RecordActivationRead(ctx context.Context, actor Actor, pid string, version int64) (ActivationReadReceipt, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return ActivationReadReceipt{}, err
	}
	var result ActivationReadReceipt
	err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if !projectOwner(p, actor.UserID) || p.Status != "draft" {
			return ErrInvalidState
		}
		if p.ProjectVersion != version {
			return ErrVersionConflict
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrInvalidState
		}
		result = ActivationReadReceipt{ID: uuid.NewString(), ActorID: actor.UserID, ProjectID: pid, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, ReadAt: time.Now().UTC()}
		if err := store.G5Put(pid, "activation_read", result.ID, 0, result); err != nil {
			return err
		}
		return g5Audit(tx, actor, pid, "project.activation_diff_read", result.ID, "")
	})
	return result, err
}
func validateActivationRead(tx Transaction, actor Actor, p Project, id string) error {
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrInvalidState
	}
	var receipt ActivationReadReceipt
	_, err := store.G5Get(p.ID, "activation_read", id, &receipt)
	if err != nil {
		return err
	}
	if receipt.ActorID != actor.UserID || receipt.ProjectVersion != p.ProjectVersion || receipt.SpecRevision != p.SpecRevision {
		return ErrVersionConflict
	}
	return nil
}

var _ = errors.Is
