package workspacecore

import (
	"context"
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
	if def.RuleSetSnapshot != nil {
		// The canonical definition owns effective overrides. The sidecar is
		// the immutable source identity and must not be evaluated instead.
		for _, rule := range def.Rules {
			if rule.Evaluator == "required_fields" || rule.Parameters["target_kind"] == "field" {
				required[rule.ID] = true
			}
		}
		for _, binding := range def.RuleSetSnapshot.Bindings {
			if binding.TargetKind == "field" || binding.DefinitionID == "computed/required_fields" {
				required[binding.RuleID] = true
			}
		}
		results, err = template.Evaluate(def, input)
	} else if set := p.TemplateCopy.RuleSetSnapshot; set != nil {
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

type MemberAssignmentInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Assignment             Member `json:"assignment"`
	Reason                 string `json:"reason"`
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
		if err := recordProjectAuditEvent(tx, Actor{TenantID: tenantID, UserID: userID}, p.ID, "member.tenant_suspended", userID, "tenant_membership_changed"); err != nil {
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
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrNotFound
	}
	var grant ProjectAPIKeyGrant
	if _, err := store.GetProjectRecord(pid, "api_key_grant", hashImpactValue(scope.KeyID), &grant); err != nil || !grant.Active || grant.KeyID != scope.KeyID || !slices.Contains(grant.Capabilities, base) {
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

type OwnerTransferDecisionInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Reason                 string `json:"reason"`
}

// Immutable baseline confirmation records are produced only by an actual
// Owner activation. This helper intentionally contains no scientific verdict.
func baselineRecord(p Project, a Actor) map[string]any {
	return map[string]any{"id": p.BaselineConfirmationID, "project_id": p.ID, "actor_id": a.UserID, "project_version": p.ProjectVersion, "spec_revision": p.SpecRevision, "template_copy_version": p.TemplateCopyVersion, "spec_fields": p.SpecFields, "spec": p.Spec, "confirmed_at": time.Now().UTC()}
}
