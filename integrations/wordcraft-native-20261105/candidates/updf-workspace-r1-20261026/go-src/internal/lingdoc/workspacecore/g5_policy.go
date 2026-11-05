package workspacecore

import (
	"context"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// CurrentTenantRole is resolved inside the transaction; token claims never
// restore a role that has been downgraded in the authoritative membership.
type tenantRoleTransaction interface {
	CurrentTenantRole(Actor) (types.TenantRole, error)
}

func (t gormTransaction) CurrentTenantRole(actor Actor) (types.TenantRole, error) {
	var row struct{ Role types.TenantRole }
	err := t.db.Table("tenant_members").Select("role").Where("tenant_id = ? AND user_id = ? AND status = 'active' AND deleted_at IS NULL", actor.TenantID, actor.UserID).Take(&row).Error
	if err != nil {
		return "", storageError(err)
	}
	if !row.Role.IsValid() {
		return "", ErrNotFound
	}
	return row.Role, nil
}
func currentTenantRole(tx Transaction, actor Actor) (types.TenantRole, error) {
	if port, ok := tx.(tenantRoleTransaction); ok {
		role, err := port.CurrentTenantRole(actor)
		if err != nil {
			return "", err
		}
		claim := actor.Role
		if claim == "" {
			claim = types.TenantRole(actor.TenantRole)
		}
		if claim != "" && (!claim.IsValid() || claim.Level() < role.Level()) {
			return claim, nil
		}
		return role, nil
	}
	// Domain fixtures can explicitly supply a role without a production DB.
	// The production GORM implementation above always uses current membership.
	role := actor.Role
	if role == "" {
		role = types.TenantRole(actor.TenantRole)
	}
	return role, nil
}

var permissionRoles = map[string]types.TenantRole{
	"read": types.TenantRoleViewer, "delivery-read": types.TenantRoleContributor, "create": types.TenantRoleContributor, "write": types.TenantRoleContributor,
	"manage": types.TenantRoleContributor, "tenant-manage": types.TenantRoleAdmin, "research": types.TenantRoleContributor, "agent": types.TenantRoleContributor,
	"evidence-write": types.TenantRoleContributor, "evidence-confirm": types.TenantRoleContributor,
	"confirm": types.TenantRoleContributor, "review": types.TenantRoleContributor,
	"change-assess": types.TenantRoleContributor, "impact-resolve": types.TenantRoleContributor,
	"ops-control": types.TenantRoleContributor, "issue-resolve": types.TenantRoleContributor,
	"issue-dismiss": types.TenantRoleContributor, "issue-waive": types.TenantRoleContributor,
	"issue-dismiss-blocking": types.TenantRoleContributor,
}

func authorizeCurrentTenant(tx Transaction, request AuthorizationRequest) error {
	actor := request.Actor
	if err := legacyAuthorizeTenant(tx, actor); err != nil {
		return err
	}
	base := string(request.Action)
	required, known := permissionRoles[base]
	if !known {
		return ErrInvalidRequest
	}
	role, err := currentTenantRole(tx, actor)
	if err != nil {
		return err
	}
	if role != "" && (!role.IsValid() || !role.HasPermission(required)) {
		return ErrNotFound
	}
	return nil
}

type EffectivePermission struct {
	Allowed         bool   `json:"allowed"`
	Capability      string `json:"capability"`
	Target          string `json:"target"`
	ReasonCode      string `json:"reason_code"`
	ProjectVersion  int64  `json:"project_version"`
	ContextRevision int64  `json:"context_revision"`
}

func projectMember(p Project, userID string) (Member, bool) {
	for _, m := range p.Members {
		if m.UserID == userID {
			return m, true
		}
	}
	return Member{}, false
}
func governanceRole(m Member) string {
	if m.Role == "owner" {
		return "owner"
	}
	if m.GovernanceRole == "" {
		return "member"
	}
	return m.GovernanceRole
}
func memberHasScope(m Member, role, target string) bool {
	return slices.Contains(m.FunctionRoles, role) && target != "" && slices.Contains(m.FunctionScopes[role], target)
}
func authorizeCurrentProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	actor, projectID := request.Actor, request.ProjectID
	if err := authorizeCurrentTenant(tx, request); err != nil {
		return Project{}, err
	}
	base, target := string(request.Action), request.Resource
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	m, ok := projectMember(p, actor.UserID)
	if !ok || m.Status != "" && m.Status != "active" {
		return Project{}, ErrNotFound
	}
	governance := governanceRole(m)
	if p.DiscardedAt != nil && (base != "read" || governance != "owner") {
		return Project{}, ErrInvalidState
	}
	if target != "" && (base == "write" || base == "confirm" || base == "review") {
		if _, err := tx.Chapter(projectID, target); err != nil {
			return Project{}, ErrNotFound
		}
	}
	// Delivery reads retain the contributor tenant floor without granting writes.
	if base == "read" || base == "delivery-read" {
		return p, nil
	}
	if governance == "owner" {
		return p, nil
	}
	switch base {
	case "manage":
		if governance == "admin" {
			return p, nil
		}
	case "ops-control": // Owner-only business control, tenant ceiling still applies.
	case "write":
		if memberHasScope(m, "author", target) {
			return p, nil
		}
	case "confirm", "review":
		if memberHasScope(m, "reviewer", target) {
			return p, nil
		}
	case "research", "evidence-write":
		if target != "" && memberHasScope(m, "author", target) {
			return p, nil
		}
		if slices.Contains(m.FunctionRoles, "researcher") && (target != "" && memberHasScope(m, "researcher", target) || target == "" && (p.Status == "draft" || len(m.FunctionScopes["researcher"]) > 0)) {
			return p, nil
		}
	case "evidence-confirm":
		if memberHasScope(m, "reviewer", target) {
			return p, nil
		}
	case "agent":
		if slices.Contains(m.FunctionRoles, "author") || slices.Contains(m.FunctionRoles, "researcher") || slices.Contains(m.FunctionRoles, "reviewer") {
			return p, nil
		}
	case "change-assess", "impact-resolve":
		if governance == "admin" || slices.Contains(m.FunctionRoles, "author") || slices.Contains(m.FunctionRoles, "researcher") || memberHasScope(m, "reviewer", target) {
			return p, nil
		}
	case "issue-dismiss-blocking":
		if memberHasScope(m, "reviewer", target) || memberHasScope(m, "reviewer", "asset:"+target) || memberHasScope(m, "reviewer", "delivery:"+target) {
			return p, nil
		}
	case "issue-resolve", "issue-dismiss", "issue-waive":
		if governance == "admin" {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// Permission status is visible only after basic project membership has been
// established. A denied stranger receives no object names or version metadata.
func (s *Service) EffectivePermission(ctx context.Context, actor Actor, projectID, capability string) (EffectivePermission, error) {
	request, err := authorizationRequestFromCapability(actor, projectID, capability)
	if err != nil {
		return EffectivePermission{}, err
	}
	var result EffectivePermission
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		result = EffectivePermission{Capability: capability, ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision}
		_, result.Target, _ = strings.Cut(capability, ":")
		_, err = s.authorizeProject(ctx, tx, request)
		if err == nil {
			result.Allowed = true
			result.ReasonCode = "allowed"
			return nil
		}
		result.ReasonCode = "capability_or_scope_denied"
		return nil
	})
	return result, err
}
