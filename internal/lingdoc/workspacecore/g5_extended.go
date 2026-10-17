package workspacecore

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (s *Service) RequireActivationRead(required bool) { s.requireActivationRead = required }

// Initialization checks the real field bindings, without claiming that unwritten
// chapters have passed the later delivery preflight.
func validateInitializationRules(p Project) error {
	if p.TemplateCopy == nil {
		return ErrInvalidState
	}
	def := p.TemplateCopy.Definition
	input := template.EvaluationInput{ProjectID: p.ID, TargetVersion: strconv.FormatInt(p.ProjectVersion, 10), Fields: p.Spec}
	required := map[string]bool{}
	var results []template.Evaluation
	var err error
	if set := p.TemplateCopy.RuleSetSnapshot; set != nil {
		for _, binding := range set.Bindings {
			if binding.TargetKind == "field" || binding.DefinitionID == "computed/required_fields" {
				required[binding.RuleID] = true
			}
		}
		results, err = template.EvaluateRuleSet(def, *set, "write_check", input)
	} else {
		for _, rule := range def.Rules {
			if rule.Evaluator == "required_fields" || rule.Parameters["target_kind"] == "field" {
				required[rule.ID] = true
			}
		}
		results, err = template.Evaluate(def, input)
	}
	if err != nil {
		return ErrInvalidState
	}
	for _, result := range results {
		if required[result.RuleID] && result.Severity == "blocking" && result.Status != template.EvaluationPass {
			return ErrInvalidState
		}
	}
	return nil
}

type MemberDirectoryEntry struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	TenantRole string `json:"tenant_role"`
}

func (s *Service) ProjectMemberDirectory(ctx context.Context, a Actor, pid string) ([]MemberDirectoryEntry, error) {
	var out []MemberDirectoryEntry
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, a, pid, "manage"); err != nil {
			return err
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return err
		}
		return db.Table("tenant_members AS tm").Select("tm.user_id, u.username AS name, tm.role AS tenant_role").Joins("JOIN users u ON u.id=tm.user_id AND u.deleted_at IS NULL AND u.is_active=?", true).Where("tm.tenant_id=? AND tm.status='active' AND tm.deleted_at IS NULL", a.TenantID).Order("u.username,tm.user_id").Limit(200).Scan(&out).Error
	})
	return out, err
}

type MemberAssignmentInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Assignment             Member `json:"assignment"`
	Reason                 string `json:"reason"`
}

func (s *Service) SetMemberAssignment(ctx context.Context, a Actor, pid, user, key string, input MemberAssignmentInput) (json.RawMessage, int, bool, error) {
	if input.Assignment.UserID != user || input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, a, "memberAssignment", pid+"/"+user, key, input, pid, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		old, exists := projectMember(p, user)
		if !exists || governanceRole(old) == "owner" || old.Status != "" && old.Status != "active" {
			return nil, 0, ErrInvalidState
		}
		nextMember := input.Assignment
		if err := validateInvitation(tx, p, nextMember); err != nil {
			return nil, 0, err
		}
		active, err := tx.ActiveMember(Actor{TenantID: a.TenantID, UserID: user})
		if err != nil {
			return nil, 0, err
		}
		if !active {
			return nil, 0, ErrNotFound
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
		if err := tx.UpdateProject(a.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if err := tx.ReplaceMembers(pid, next.Members); err != nil {
			return nil, 0, err
		}
		if err := g5Audit(tx, a, pid, "member.assignment", user, input.Reason); err != nil {
			return nil, 0, err
		}
		return next, 200, nil
	})
}

func verifyMemberResourceScope(tx Transaction, p Project, scope string) error {
	kind, id, found := strings.Cut(scope, ":")
	if !found || id == "" {
		return ErrInvalidRequest
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return err
	}
	var count int64
	switch kind {
	case "asset", "knowledge":
		q := db.Model(&evidence.ProjectAsset{}).Where("project_id=?", p.ID)
		if kind == "asset" {
			q = q.Where("id=?", id)
		} else {
			q = q.Where("knowledge_id=?", id)
		}
		if err := q.Count(&count).Error; err != nil {
			return err
		}
	case "template":
		if p.TemplateCopy != nil && (id == p.TemplateCopy.ID || id == p.TemplateID) {
			count = 1
		}
	case "delivery":
		if id == p.ID {
			count = 1
		} else {
			if err := db.Table("lingdoc_release_snapshots").Where("project_id=? AND id=?", p.ID, id).Count(&count).Error; err != nil {
				return err
			}
		}
	default:
		return ErrInvalidRequest
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// Called in the SAME transaction that removes/suspends tenant membership.
// Never regrant project access merely because a tenant membership is restored.
func CaptureTenantSuspensionInTransaction(db *gorm.DB, tenantID uint64, userID string) error {
	if !db.Migrator().HasTable("lingdoc_projects") {
		return nil
	}
	tx := gormTransaction{db: db}
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
}

type ProjectAPIKeyGrant struct {
	KeyID                  uint64    `json:"key_id"`
	Capabilities           []string  `json:"capabilities"`
	ExpectedProjectVersion int64     `json:"expected_project_version"`
	Reason                 string    `json:"reason"`
	Active                 bool      `json:"active"`
	CreatedBy              string    `json:"created_by"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (s *Service) SaveProjectAPIKeyGrant(ctx context.Context, a Actor, pid, key string, input ProjectAPIKeyGrant) (json.RawMessage, int, bool, error) {
	if input.KeyID == 0 || input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	for _, cap := range input.Capabilities {
		if cap != "read" {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	return s.operation(ctx, a, "apiKeyProjectGrant", pid, key, input, pid, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return nil, 0, err
		}
		var count int64
		if err := db.Table("tenant_api_keys").Where("id=? AND tenant_id=?", input.KeyID, a.TenantID).Count(&count).Error; err != nil {
			return nil, 0, err
		}
		if count != 1 {
			return nil, 0, ErrNotFound
		}
		if input.Active {
			if _, err := currentMachineNativeKey(tx, a.TenantID, input.KeyID); err != nil {
				return nil, 0, err
			}
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		id := hashImpactValue(input.KeyID)
		var old ProjectAPIKeyGrant
		version, err := store.G5Get(pid, "api_key_grant", id, &old)
		if err != nil && err != ErrNotFound {
			return nil, 0, err
		}
		if err == ErrNotFound {
			version = 0
		}
		input.CreatedBy = a.UserID
		input.UpdatedAt = time.Now().UTC()
		if err := store.G5Put(pid, "api_key_grant", id, version, input); err != nil {
			return nil, 0, err
		}
		if err := g5Audit(tx, a, pid, "api_key.scope", id, input.Reason); err != nil {
			return nil, 0, err
		}
		return input, 200, nil
	})
}
func authorizeAPIKeyProject(ctx context.Context, tx Transaction, pid, capability string) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		if RequireHumanDecision(ctx) != nil {
			return ErrNotFound
		}
		return nil
	}
	if scope.KeyID == 0 {
		return ErrNotFound
	}
	if pid == "" {
		return ErrNotFound
	}
	base, _, _ := strings.Cut(capability, ":")
	if !slices.Contains([]string{"read", "write", "research", "agent", "evidence-write"}, base) {
		return ErrNotFound
	}
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrNotFound
	}
	var grant ProjectAPIKeyGrant
	if _, err := store.G5Get(pid, "api_key_grant", hashImpactValue(scope.KeyID), &grant); err != nil || !grant.Active || grant.KeyID != scope.KeyID || !slices.Contains(grant.Capabilities, base) {
		return ErrNotFound
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrNotFound
	}
	var row struct {
		ID        uint64
		TenantID  *uint64
		ScopeType string
		ExpiresAt *time.Time
		RevokedAt *time.Time
	}
	if err := db.Table("tenant_api_keys").Select("id,tenant_id,scope_type,expires_at,revoked_at").Where("id=?", scope.KeyID).Take(&row).Error; err != nil {
		return ErrNotFound
	}
	if row.RevokedAt != nil || row.ExpiresAt != nil && !time.Now().UTC().Before(*row.ExpiresAt) {
		return ErrNotFound
	}
	if !scope.IsPlatform() {
		var projectTenant uint64
		if err := db.Table("lingdoc_projects").Select("tenant_id").Where("id=?", pid).Scan(&projectTenant).Error; err != nil || row.TenantID == nil || *row.TenantID != projectTenant {
			return ErrNotFound
		}
	}
	// Retain all native/grant checks above, but never turn auth's legacy
	// tenant-user alias into a permitted Actor. Explicit machine GETs use
	// ReadMachineProjectSummary/ReadMachineChapter with the real key subject.
	return ErrNotFound
}

func (s *Service) ListMyProjectInvitations(ctx context.Context, a Actor) ([]MemberInvitation, error) {
	result := []MemberInvitation{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if err := s.authorizeTenant(ctx, tx, a, "read"); err != nil {
			return err
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return err
		}
		var rows []g5RecordRow
		if err := db.Table("lingdoc_g5_records AS g").Select("g.*").Joins("JOIN lingdoc_projects p ON p.id=g.project_id").Where("p.tenant_id=? AND g.kind='invitation'", a.TenantID).Order("g.id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var item MemberInvitation
			if err := json.Unmarshal([]byte(row.PayloadJSON), &item); err != nil {
				return err
			}
			if item.UserID == a.UserID && item.Status == "invited" && time.Now().UTC().Before(item.ExpiresAt) {
				result = append(result, item)
			}
		}
		return nil
	})
	return result, err
}

func (s *Service) ListOwnerTransfers(ctx context.Context, a Actor, pid string) ([]OwnerTransfer, error) {
	var result []OwnerTransfer
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, a, pid, "read")
		if err != nil {
			return err
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return err
		}
		var rows []ownerTransferRow
		if err := db.Where("project_id=?", pid).Order("created_at DESC,id").Find(&rows).Error; err != nil {
			return err
		}
		result = []OwnerTransfer{}
		for _, row := range rows {
			item := OwnerTransfer(row)
			if !projectOwner(p, a.UserID) && item.FromUserID != a.UserID && item.ToUserID != a.UserID {
				continue
			}
			if item.Status == "pending" && time.Since(item.CreatedAt) > 72*time.Hour {
				item.Status = "expired"
			}
			result = append(result, item)
		}
		return nil
	})
	return result, err
}

type OwnerTransferDecisionInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Reason                 string `json:"reason"`
}

func (s *Service) RejectOwnerTransfer(ctx context.Context, a Actor, pid, id, key string, input OwnerTransferDecisionInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, a, "rejectOwnerTransfer", pid+"/"+id, key, input, pid, "read", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return nil, 0, err
		}
		store, ok := tx.(ownerTransferTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		item, err := store.OwnerTransfer(pid, id)
		if err != nil {
			return nil, 0, err
		}
		if item.Status != "pending" || item.ToUserID != a.UserID && !projectOwner(p, a.UserID) {
			return nil, 0, ErrNotFound
		}
		if time.Since(item.CreatedAt) > 72*time.Hour {
			return nil, 0, ErrInvalidState
		}
		update := db.Model(&ownerTransferRow{}).Where("id=? AND project_id=? AND status='pending'", id, pid).Update("status", "cancelled")
		if err := affected(update); err != nil {
			return nil, 0, err
		}
		item.Status = "cancelled"
		if err := g5Audit(tx, a, pid, "owner.transfer.reject", id, input.Reason); err != nil {
			return nil, 0, err
		}
		return item, 200, nil
	})
}

// Immutable baseline confirmation records are produced only by an actual
// Owner activation. This helper intentionally contains no scientific verdict.
func baselineRecord(p Project, a Actor) map[string]any {
	return map[string]any{"id": p.BaselineConfirmationID, "project_id": p.ID, "actor_id": a.UserID, "project_version": p.ProjectVersion, "spec_revision": p.SpecRevision, "template_copy_version": p.TemplateCopyVersion, "spec_fields": p.SpecFields, "spec": p.Spec, "confirmed_at": time.Now().UTC()}
}
