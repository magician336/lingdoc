// Package rulesetstore persists immutable RuleSet versions and their lifecycle
// audit records. Authorization is deliberately supplied by the application
// layer and is checked inside the same database transaction as each read or
// write.
package rulesetstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The failures of this package state their own outward form.
//
// The rule set storage surface and the rule governance surface report some of
// the same failures, and each surface keeps its own wording. A sentinel that
// another surface words differently is not edited here: that surface wraps it
// in a named scenario value instead.
var (
	// ErrActorRequired is a request without a usable actor.
	ErrActorRequired = clienterror.New(clienterror.Form{Status: 401, Code: "unauthenticated", Message: "需要有效的租户身份。", Retryable: false}, "ruleset actor is required")
	// ErrActorRoleInvalid is an actor whose role is outside the tenant role
	// vocabulary. The role is recorded in the audit column, so a value the
	// vocabulary does not define cannot be attributed to a tenant role.
	ErrActorRoleInvalid = clienterror.New(clienterror.Form{Status: 401, Code: "unauthenticated", Message: "需要有效的租户身份。", Retryable: false}, "ruleset actor role is not a tenant role")
	// ErrRuleSetTenantRequired is a request without a usable tenant.
	ErrRuleSetTenantRequired = clienterror.New(clienterror.Form{Status: 401, Code: "unauthenticated", Message: "需要有效的租户身份。", Retryable: false}, "ruleset tenant is required")
	// ErrAuthorizerUnavailable is a rule set authorization that could not be
	// established because the authorizer is down.
	ErrAuthorizerUnavailable = clienterror.New(clienterror.Form{Status: 503, Code: "authorization_unavailable", Message: "授权服务暂不可用，请稍后重试。", Retryable: true}, "ruleset authorizer is unavailable")
	// ErrRuleSetVersionExists is a rule set version that already exists.
	ErrRuleSetVersionExists = clienterror.New(clienterror.BaselineConflict, "ruleset version already exists")
	// ErrRuleSetStateConflict is a rule set whose state changed concurrently.
	ErrRuleSetStateConflict = clienterror.New(clienterror.BaselineConflict, "ruleset state changed concurrently")
	// ErrRuleSetNotFound is a rule set version that does not exist.
	ErrRuleSetNotFound = clienterror.New(clienterror.NotFound, "ruleset version not found")
	// ErrDraftHashMustBeEmpty is a draft that carries the server-managed hash.
	// The rule set storage surface wraps it in its own scenario value.
	ErrDraftHashMustBeEmpty = clienterror.New(clienterror.InvalidRequest, "draft ruleset hash is server managed")
)

type Action string

const (
	ActionCreateDraft Action = "create_draft"
	ActionRead        Action = "read"
	ActionPublish     Action = "publish"
	ActionWithdraw    Action = "withdraw"
	ActionRecall      Action = "recall"
)

// Actor identifies the tenant member an operation is performed as.
//
// Role is the tenant role the member held when the operation was requested.
// Its value comes from the tenant role vocabulary owned by internal/types
// (types.TenantRole), and IsValid is the only judge of membership in it.
//
// The value is not decoration: every audit row this package writes records
// it in audit_logs.actor_role. The column therefore means "the tenant role
// the operator held at that moment" -- not a project role, not a governance
// role, not a function role. A value outside the vocabulary would put a word
// into that column that no reader can interpret, so the store refuses it
// rather than writing it.
type Actor struct {
	TenantID uint64
	UserID   string
	Role     string
}

// Authorizer must evaluate the actor against current authorization data using
// the supplied transaction. A nil authorizer fails closed.
type Authorizer interface {
	AuthorizeRuleSet(context.Context, *gorm.DB, Actor, Action, *template.RuleSet) error
}

type Store struct {
	db         *gorm.DB
	authorizer Authorizer
}

func NewStore(db *gorm.DB, authorizer Authorizer) *Store {
	return &Store{db: db, authorizer: authorizer}
}

// CreateDraft stores one tenant-owned version. Versions are never replaced;
// editing a draft creates a new version, keeping all reviewed identities
// stable and making retries safe.
func (s *Store) CreateDraft(ctx context.Context, actor Actor, draft template.RuleSet) error {
	if err := s.validateActor(actor); err != nil {
		return err
	}
	if draft.State != template.RuleSetDraft {
		return template.ErrRuleSetNotDraft
	}
	if draft.RulesetHash != "" {
		return ErrDraftHashMustBeEmpty
	}
	if err := template.ValidateRuleSet(draft); err != nil {
		return err
	}

	bindings, err := json.Marshal(draft.Bindings)
	if err != nil {
		return fmt.Errorf("encode ruleset bindings: %w", err)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorize(ctx, tx, actor, ActionCreateDraft, &draft); err != nil {
			return err
		}
		row := ruleSetVersionRow{
			TenantID: actor.TenantID, RuleSetID: draft.ID, Version: draft.Version,
			State: string(template.RuleSetDraft), BindingsJSON: string(bindings),
			CreatedBy: actor.UserID, CreatedAt: time.Now().UTC(),
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "ruleset_id"}, {Name: "version"}},
			DoNothing: true,
		}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrRuleSetVersionExists
		}
		return recordAudit(tx, actor, draft, ActionCreateDraft, "", template.RuleSetDraft)
	})
}

func (s *Store) GetVersion(ctx context.Context, actor Actor, id, version string) (template.RuleSet, error) {
	if err := s.validateActor(actor); err != nil {
		return template.RuleSet{}, err
	}
	var result template.RuleSet
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := loadVersion(tx, actor.TenantID, id, version)
		if err != nil {
			return err
		}
		result, err = decodeRuleSet(row)
		if err != nil {
			return err
		}
		return s.authorize(ctx, tx, actor, ActionRead, &result)
	}, sqlTxReadOnly)
	return result, err
}

// GetVersionForBinding reads and locks an exact RuleSet version on the caller's
// transaction. This lets a project bind serialize with concurrent withdraw or
// recall transitions instead of persisting a snapshot checked on another
// connection.
func (s *Store) GetVersionForBinding(ctx context.Context, tx *gorm.DB, actor Actor, id, version string) (template.RuleSet, error) {
	if s == nil || tx == nil {
		return template.RuleSet{}, errors.New("ruleset binding transaction is unavailable")
	}
	if actor.TenantID == 0 {
		return template.RuleSet{}, ErrRuleSetTenantRequired
	}
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(actor.Role) == "" {
		return template.RuleSet{}, ErrActorRequired
	}
	if !types.TenantRole(actor.Role).IsValid() {
		return template.RuleSet{}, ErrActorRoleInvalid
	}
	if s.authorizer == nil {
		return template.RuleSet{}, ErrAuthorizerUnavailable
	}
	var result template.RuleSet
	err := tx.WithContext(ctx).Transaction(func(bindingTx *gorm.DB) error {
		// PostgreSQL honors SELECT FOR UPDATE below, but SQLite ignores row
		// locking clauses. A no-op update obtains SQLite's write reservation so
		// a concurrent withdraw/recall cannot commit between the state read and
		// the project's bound snapshot write.
		lock := bindingTx.Model(&ruleSetVersionRow{}).
			Where("tenant_id = ? AND ruleset_id = ? AND version = ?", actor.TenantID, id, version).
			UpdateColumn("state", gorm.Expr("state"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected == 0 {
			return ErrRuleSetNotFound
		}
		var row ruleSetVersionRow
		query := bindingTx.Clauses(clause.Locking{Strength: "UPDATE"})
		if err := query.Where("tenant_id = ? AND ruleset_id = ? AND version = ?", actor.TenantID, id, version).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRuleSetNotFound
			}
			return err
		}
		set, err := decodeRuleSet(row)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, bindingTx, actor, ActionRead, &set); err != nil {
			return err
		}
		result = set
		return nil
	})
	return result, err
}

// ListVersions returns a stable newest-first history for one ruleset identity.
func (s *Store) ListVersions(ctx context.Context, actor Actor, id string) ([]template.RuleSet, error) {
	if err := s.validateActor(actor); err != nil {
		return nil, err
	}
	var sets []template.RuleSet
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []ruleSetVersionRow
		if err := tx.Where("tenant_id = ? AND ruleset_id = ?", actor.TenantID, id).
			Order("created_at DESC, version DESC").Find(&rows).Error; err != nil {
			return err
		}
		sets = make([]template.RuleSet, 0, len(rows))
		for _, row := range rows {
			set, err := decodeRuleSet(row)
			if err != nil {
				return err
			}
			if err := s.authorize(ctx, tx, actor, ActionRead, &set); err != nil {
				return err
			}
			sets = append(sets, set)
		}
		return nil
	}, sqlTxReadOnly)
	return sets, err
}

func (s *Store) Publish(ctx context.Context, actor Actor, id, version string) (template.RuleSet, error) {
	return s.transition(ctx, actor, id, version, ActionPublish, template.PublishRuleSet)
}

func (s *Store) Withdraw(ctx context.Context, actor Actor, id, version string) (template.RuleSet, error) {
	return s.transition(ctx, actor, id, version, ActionWithdraw, template.WithdrawRuleSet)
}

func (s *Store) Recall(ctx context.Context, actor Actor, id, version string) (template.RuleSet, error) {
	return s.transition(ctx, actor, id, version, ActionRecall, template.RecallRuleSet)
}

func (s *Store) transition(
	ctx context.Context,
	actor Actor,
	id, version string,
	action Action,
	apply func(template.RuleSet) (template.RuleSet, error),
) (template.RuleSet, error) {
	if err := s.validateActor(actor); err != nil {
		return template.RuleSet{}, err
	}
	var next template.RuleSet
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := loadVersion(tx, actor.TenantID, id, version)
		if err != nil {
			return err
		}
		current, err := decodeRuleSet(row)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, action, &current); err != nil {
			return err
		}
		next, err = apply(current)
		if err != nil {
			return err
		}
		updates := map[string]any{"state": string(next.State)}
		if action == ActionPublish {
			updates["ruleset_hash"] = next.RulesetHash
		}
		result := tx.Model(&ruleSetVersionRow{}).
			Where("tenant_id = ? AND ruleset_id = ? AND version = ? AND state = ?", actor.TenantID, id, version, string(current.State)).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRuleSetStateConflict
		}
		return recordAudit(tx, actor, next, action, current.State, next.State)
	})
	return next, err
}

func (s *Store) validateActor(actor Actor) error {
	if s == nil || s.db == nil {
		return errors.New("ruleset store database is unavailable")
	}
	if actor.TenantID == 0 {
		return ErrRuleSetTenantRequired
	}
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(actor.Role) == "" {
		return ErrActorRequired
	}
	if !types.TenantRole(actor.Role).IsValid() {
		return ErrActorRoleInvalid
	}
	if s.authorizer == nil {
		return ErrAuthorizerUnavailable
	}
	return nil
}

func (s *Store) authorize(ctx context.Context, tx *gorm.DB, actor Actor, action Action, set *template.RuleSet) error {
	if s.authorizer == nil {
		return ErrAuthorizerUnavailable
	}
	return s.authorizer.AuthorizeRuleSet(ctx, tx, actor, action, set)
}

func loadVersion(tx *gorm.DB, tenantID uint64, id, version string) (ruleSetVersionRow, error) {
	var row ruleSetVersionRow
	err := tx.Where("tenant_id = ? AND ruleset_id = ? AND version = ?", tenantID, id, version).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ruleSetVersionRow{}, ErrRuleSetNotFound
	}
	return row, err
}

func decodeRuleSet(row ruleSetVersionRow) (template.RuleSet, error) {
	var bindings []template.RuleBinding
	if err := json.Unmarshal([]byte(row.BindingsJSON), &bindings); err != nil {
		return template.RuleSet{}, fmt.Errorf("decode ruleset %s/%s bindings: %w", row.RuleSetID, row.Version, err)
	}
	set := template.RuleSet{ID: row.RuleSetID, Version: row.Version, State: template.RuleSetState(row.State), Bindings: bindings, RulesetHash: row.RuleSetHash}
	if err := template.ValidateRuleSet(set); err != nil {
		return template.RuleSet{}, fmt.Errorf("validate stored ruleset %s/%s: %w", row.RuleSetID, row.Version, err)
	}
	return set, nil
}

// recordAudit writes the rule set lifecycle audit row.
//
// The row's actor_role column means "the tenant role the operator held at
// that moment". This is the last point before the INSERT, so it also guards
// the callers that reach an audit row without passing through
// validateActor: a role outside the vocabulary must never reach the column.
func recordAudit(tx *gorm.DB, actor Actor, set template.RuleSet, action Action, from, to template.RuleSetState) error {
	if !types.TenantRole(actor.Role).IsValid() {
		return ErrActorRoleInvalid
	}
	details, err := json.Marshal(map[string]any{
		"ruleset_id": set.ID, "version": set.Version,
		"ruleset_hash": set.RulesetHash, "from_state": from, "to_state": to,
	})
	if err != nil {
		return fmt.Errorf("encode ruleset audit details: %w", err)
	}
	now := time.Now().UTC()
	query := "INSERT INTO audit_logs (tenant_id, actor_user_id, actor_role, action, target_type, target_id, outcome, details, scope_type, scope_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	if tx.Dialector.Name() == "postgres" {
		query = "INSERT INTO audit_logs (tenant_id, actor_user_id, actor_role, action, target_type, target_id, outcome, details, scope_type, scope_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?)"
	}
	return tx.Exec(query,
		actor.TenantID, actor.UserID, actor.Role, "lingdoc.ruleset."+string(action),
		"ruleset_version", auditTargetID(set.ID, set.Version), "success", string(details),
		"lingdoc_ruleset", set.ID, now,
	).Error
}

// auditTargetID fits the shared audit_logs.target_id VARCHAR(64) while keeping
// a deterministic reference to the full (ruleset ID, version) pair. The
// unabridged values remain in the JSON details for human-readable lookup.
func auditTargetID(id, version string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + version))
	return hex.EncodeToString(sum[:])
}

type ruleSetVersionRow struct {
	TenantID     uint64    `gorm:"primaryKey;autoIncrement:false"`
	RuleSetID    string    `gorm:"column:ruleset_id;primaryKey;size:64"`
	Version      string    `gorm:"primaryKey;size:64"`
	State        string    `gorm:"not null;size:16"`
	RuleSetHash  string    `gorm:"column:ruleset_hash;not null;size:64;default:''"`
	BindingsJSON string    `gorm:"column:bindings_json;not null;type:text"`
	CreatedBy    string    `gorm:"not null;size:64"`
	CreatedAt    time.Time `gorm:"not null"`
}

func (ruleSetVersionRow) TableName() string { return "lingdoc_rule_sets" }

// A shared read-only option avoids accidentally turning list/read calls into
// writable transactions in database backends that enforce transaction modes.
var sqlTxReadOnly = &sql.TxOptions{ReadOnly: true}
