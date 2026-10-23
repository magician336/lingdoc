package templategovernance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ObserveOperationReplay exposes transport metadata only after the stored
// request hash matches. It cannot grant authority or change retry semantics.
type replayObserverKey struct{}

func ObserveOperationReplay(ctx context.Context, onReplay func()) context.Context {
	return context.WithValue(ctx, replayObserverKey{}, onReplay)
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

func (s *Service) ready(actor Actor) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(actor.Role) == "" || s.ports.Authorizer == nil {
		return ErrUnauthorized
	}
	return nil
}
func validKey(key string) bool {
	return len(key) >= 8 && len(key) <= 128 && strings.TrimSpace(key) == key
}
func validReason(reason string) bool { return strings.TrimSpace(reason) != "" && len(reason) <= 4000 }
func hashJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func jsonText(value any) (string, error) { raw, err := json.Marshal(value); return string(raw), err }
func (s *Service) withTx(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}
func operationIdentity(actor Actor, operation, target, key string) operationRow {
	return operationRow{TenantID: actor.TenantID, ActorID: actor.UserID, Operation: operation, Target: target, IdempotencyKey: key}
}
func operationGet(tx *gorm.DB, identity operationRow, request any, out any) (bool, error) {
	var row operationRow
	err := tx.Where("tenant_id=? AND actor_id=? AND operation=? AND target=? AND idempotency_key=?", identity.TenantID, identity.ActorID, identity.Operation, identity.Target, identity.IdempotencyKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hash, err := hashJSON(request)
	if err != nil {
		return false, ErrInvalidRequest
	}
	if hash != row.RequestHash {
		return false, ErrIdempotencyConflict
	}
	if err = json.Unmarshal([]byte(row.ResultJSON), out); err != nil {
		return false, fmt.Errorf("decode governance operation: %w", err)
	}
	if observer, ok := tx.Statement.Context.Value(replayObserverKey{}).(func()); ok && observer != nil {
		observer()
	}
	return true, nil
}
func operationSave(tx *gorm.DB, identity operationRow, request, result, basis any) error {
	var err error
	identity.RequestHash, err = hashJSON(request)
	if err != nil {
		return err
	}
	identity.ResultJSON, err = jsonText(result)
	if err != nil {
		return err
	}
	identity.BasisJSON, err = jsonText(basis)
	if err != nil {
		return err
	}
	identity.CreatedAt = time.Now().UTC()
	r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrIdempotencyConflict
	}
	return nil
}
func recordAudit(tx *gorm.DB, actor Actor, action Action, target, reason string, details any) error {
	return recordAuditOutcome(tx, actor, action, target, reason, "success", details)
}
func recordAuditOutcome(tx *gorm.DB, actor Actor, action Action, target, reason, outcome string, details any) error {
	raw, err := json.Marshal(map[string]any{"reason": reason, "target_ref": target, "details": details})
	if err != nil {
		return err
	}
	query := "INSERT INTO audit_logs (tenant_id,actor_user_id,actor_role,action,target_type,target_id,outcome,details,scope_type,scope_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)"
	if tx.Name() == "postgres" {
		query = "INSERT INTO audit_logs (tenant_id,actor_user_id,actor_role,action,target_type,target_id,outcome,details,scope_type,scope_id,created_at) VALUES (?,?,?,?,?,?,?,?::jsonb,?,?,?)"
	}
	auditTarget := target
	if len(auditTarget) > 64 {
		auditTarget = digestText(target)
	}
	return tx.Exec(query, actor.TenantID, actor.UserID, actor.Role, "lingdoc.template."+string(action), "template_governance", auditTarget, outcome, string(raw), "lingdoc_template", auditTarget, time.Now().UTC()).Error
}

// A rejected catalog mutation has already rolled back when this runs. Its
// receipt is written in a separate bounded transaction so a stale version,
// failed replay, missing source, or recently revoked actor cannot erase the
// denial trail. Current-version metadata is best-effort and is included only
// when the actor can still read it; the minimal receipt uses the authenticated
// request identity and never widens that actor's read access. An unverified
// claimed identity remains an error and cannot author an attributed receipt.
func (s *Service) auditCatalogFailure(ctx context.Context, actor Actor, action Action, id, version, reason string, cause error) error {
	return s.auditCatalogFailureWithDetails(ctx, actor, action, id, version, reason, cause, nil)
}

func (s *Service) auditCatalogFailureWithDetails(ctx context.Context, actor Actor, action Action, id, version, reason string, cause error, extra map[string]any) error {
	if cause == nil || errors.Is(cause, ErrUnverifiedActor) {
		return nil
	}
	if err := s.ready(actor); err != nil {
		return fmt.Errorf("catalog failure receipt unavailable: %w", err)
	}
	if !identifier.MatchString(id) || (version != "" && !identifier.MatchString(version)) {
		// A malformed target cannot be safely attributed to a catalog object.
		// This is an intentional non-write, not a failed receipt persistence;
		// preserve the original request rejection without reporting a storage error.
		return nil
	}
	ledgerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	details := map[string]any{"error_code": catalogFailureCode(cause), "template_id": id, "version": version}
	for key, value := range extra {
		details[key] = value
	}
	if version != "" {
		metadataErr := s.withTx(ledgerCtx, func(tx *gorm.DB) error {
			if err := s.ports.Authorizer.AuthorizeTemplate(ledgerCtx, tx, actor, ActionRead, id); err != nil {
				return err
			}
			canManage, err := s.ports.Authorizer.CanManageTemplateCatalog(ledgerCtx, tx, actor)
			if err != nil {
				return err
			}
			current, err := loadVersion(tx, actor, id, version)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				return err
			}
			if canManage || current.State != template.RuleSetDraft {
				details["current_revision"] = current.Revision
				details["content_hash"] = current.ContentHash
				details["ruleset_hash"] = current.RulesetHash
			}
			return nil
		})
		if metadataErr != nil {
			details["current_version_metadata_unavailable"] = true
		}
	}
	if !validReason(reason) {
		reason = "rejected request without valid governance reason"
	}
	target := id
	if version != "" {
		target += "/" + version
	}
	if err := s.withTx(ledgerCtx, func(tx *gorm.DB) error {
		return recordAuditOutcome(tx, actor, action, target, reason, "failure", details)
	}); err != nil {
		return fmt.Errorf("persist catalog failure receipt: %w", err)
	}
	return nil
}
func catalogFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return "permission_denied"
	case errors.Is(err, ErrInvalidRequest):
		return "invalid_request"
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict"
	case errors.Is(err, ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, ErrInvalidState):
		return "invalid_state"
	case errors.Is(err, ErrReplayRequired):
		return "replay_required"
	case errors.Is(err, ErrSourceUnavailable):
		return "source_unavailable"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	default:
		return "operation_failed"
	}
}
func loadProfile(tx *gorm.DB, actor Actor, id string) (Profile, error) {
	var row profileRow
	if err := tx.Where("tenant_id=? AND template_id=?", actor.TenantID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, err
	}
	return Profile{TenantID: row.TenantID, ID: row.TemplateID, Name: row.Name, Revision: row.Revision, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}, nil
}
func loadVersion(tx *gorm.DB, actor Actor, id, version string) (TemplateVersion, error) {
	var row versionRow
	if err := tx.Where("tenant_id=? AND template_id=? AND version=?", actor.TenantID, id, version).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return TemplateVersion{}, ErrNotFound
		}
		return TemplateVersion{}, err
	}
	return decodeVersion(row)
}
func decodeVersion(row versionRow) (TemplateVersion, error) {
	v := TemplateVersion{PolicyHash: row.PolicyHash, TemplateID: row.TemplateID, Version: row.Version, Revision: row.Revision, State: template.RuleSetState(row.State), SourceType: row.SourceType, Scope: row.Scope, Curator: row.Curator, ContentHash: row.ContentHash, RulesetHash: row.RulesetHash, PublishedAt: row.PublishedAt, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal([]byte(row.BindingPoliciesJSON), &v.BindingPolicies); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &v.Definition); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(row.RulesetJSON), &v.RuleSetSnapshot); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(row.SourceRefsJSON), &v.Sources); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(row.RecallJSON), &v.Recall); err != nil {
		return v, err
	}
	v.RuleSet = RuleSetRef{ID: v.RuleSetSnapshot.ID, Version: v.RuleSetSnapshot.Version, Hash: v.RuleSetSnapshot.RulesetHash}
	if err := ValidateVersion(v); err != nil {
		return TemplateVersion{}, err
	}
	return v, nil
}

// ValidateVersion verifies both independent binding identity and its compiled
// compatibility projection. Definition's legacy rules hash remains the compiled
// digest; RulesetHash and RuleSetSnapshot are the independent version hash.
func ValidateVersion(v TemplateVersion) error {
	if err := ValidateBindingPolicies(v.Definition, v.RuleSetSnapshot, v.BindingPolicies); err != nil {
		return err
	}
	policyHash, err := HashBindingPolicies(v.BindingPolicies)
	if err != nil || policyHash != v.PolicyHash {
		return ErrInvalidState
	}
	if err := template.ValidateRuleSet(v.RuleSetSnapshot); err != nil {
		return err
	}
	if v.RuleSetSnapshot.State == template.RuleSetDraft {
		return ErrInvalidState
	}
	if v.RuleSet.ID != v.RuleSetSnapshot.ID || v.RuleSet.Version != v.RuleSetSnapshot.Version || v.RuleSet.Hash != v.RuleSetSnapshot.RulesetHash || v.RulesetHash != v.RuleSetSnapshot.RulesetHash {
		return ErrInvalidState
	}
	compiled, err := template.CompileRuleSet(v.RuleSetSnapshot)
	if err != nil {
		return err
	}
	a, _ := hashJSON(compiled)
	b, _ := hashJSON(v.Definition.Rules)
	if a != b {
		return ErrInvalidState
	}
	content, rules, err := template.Hashes(v.Definition)
	if err != nil {
		return err
	}
	if content != v.ContentHash || content != v.Definition.ContentHash || rules != v.Definition.RulesetHash || v.Definition.ID != v.TemplateID || v.Definition.Version != v.Version {
		return ErrInvalidState
	}
	switch v.State {
	case template.RuleSetDraft, template.RuleSetPublished, template.RuleSetWithdrawn, template.RuleSetRecalled:
	default:
		return ErrInvalidState
	}
	return nil
}
func encodeVersion(actor Actor, v TemplateVersion) (versionRow, error) {
	policies, err := jsonText(v.BindingPolicies)
	if err != nil {
		return versionRow{}, err
	}
	definition, err := jsonText(v.Definition)
	if err != nil {
		return versionRow{}, err
	}
	rules, err := jsonText(v.RuleSetSnapshot)
	if err != nil {
		return versionRow{}, err
	}
	sources, err := jsonText(v.Sources)
	if err != nil {
		return versionRow{}, err
	}
	recall, err := jsonText(v.Recall)
	if err != nil {
		return versionRow{}, err
	}
	return versionRow{BindingPoliciesJSON: policies, PolicyHash: v.PolicyHash, TenantID: actor.TenantID, TemplateID: v.TemplateID, Version: v.Version, Revision: v.Revision, State: string(v.State), DefinitionJSON: definition, RulesetJSON: rules, SourceType: v.SourceType, Scope: v.Scope, SourceRefsJSON: sources, Curator: v.Curator, ContentHash: v.ContentHash, RulesetHash: v.RulesetHash, PublishedAt: v.PublishedAt, RecallJSON: recall, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}, nil
}
func saveVersion(tx *gorm.DB, actor Actor, previous, next TemplateVersion) error {
	row, err := encodeVersion(actor, next)
	if err != nil {
		return err
	}
	if previous.Revision == 0 {
		r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrVersionConflict
		}
		return nil
	}
	columns := []string{"binding_policies_json", "policy_hash", "revision", "state", "definition_json", "ruleset_json", "source_type", "scope", "source_refs_json", "curator", "content_hash", "ruleset_hash", "published_at", "recall_json", "updated_at"}
	if previous.State != template.RuleSetDraft {
		if previous.PolicyHash != next.PolicyHash || !reflect.DeepEqual(previous.BindingPolicies, next.BindingPolicies) || !reflect.DeepEqual(previous.Definition, next.Definition) || !reflect.DeepEqual(previous.RuleSetSnapshot, next.RuleSetSnapshot) || previous.ContentHash != next.ContentHash || previous.RulesetHash != next.RulesetHash || previous.SourceType != next.SourceType || previous.Scope != next.Scope || previous.Curator != next.Curator || !reflect.DeepEqual(previous.Sources, next.Sources) {
			return ErrInvalidState
		}
		columns = []string{"revision", "state", "recall_json", "updated_at"}
	}
	r := tx.Model(&versionRow{}).Where("tenant_id=? AND template_id=? AND version=? AND revision=? AND state=?", actor.TenantID, previous.TemplateID, previous.Version, previous.Revision, string(previous.State)).Select(columns).Updates(&row)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

func digestText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
