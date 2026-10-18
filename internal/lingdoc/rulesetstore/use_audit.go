package rulesetstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrInvalidRuleSetUseAudit = errors.New("invalid ruleset use audit event")

const (
	RuleSetUseAuditDefaultLimit = 50
	RuleSetUseAuditMaxLimit     = 100
	auditCursorVersion          = 1
	auditCursorMaxBytes         = 1024
)

// RuleSetUseAuditQuery is the bounded, keyset-paginated history query. From is
// inclusive and To is exclusive; both are normalized to UTC before querying.
type RuleSetUseAuditQuery struct {
	Cursor string
	Limit  int
	Action string
	From   *time.Time
	To     *time.Time
}

type RuleSetUseAuditPage struct {
	Items      []template.RuleSetUseAuditEvent `json:"items"`
	NextCursor string                          `json:"next_cursor,omitempty"`
}

type ruleSetUseAuditCursor struct {
	Version   int    `json:"v"`
	TenantID  uint64 `json:"tenant_id"`
	ProjectID string `json:"project_id"`
	Action    string `json:"action,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type ruleSetUseAuditRow struct {
	ID                   string    `gorm:"primaryKey;size:36"`
	TenantID             uint64    `gorm:"not null;index:idx_lingdoc_ruleset_use_audit_scope,priority:1"`
	ActorID              string    `gorm:"not null;size:64"`
	ActorRole            string    `gorm:"not null;size:32"`
	ProjectID            string    `gorm:"not null;size:64;index:idx_lingdoc_ruleset_use_audit_scope,priority:2"`
	Action               string    `gorm:"not null;size:32"`
	RuleSetID            string    `gorm:"column:ruleset_id;not null;size:64;index:idx_lingdoc_ruleset_use_audit_version,priority:2"`
	RuleSetVersion       string    `gorm:"column:ruleset_version;not null;size:64;index:idx_lingdoc_ruleset_use_audit_version,priority:3"`
	RuleSetHash          string    `gorm:"column:ruleset_hash;not null;size:64"`
	EffectiveRulesetHash string    `gorm:"not null;size:64"`
	TemplateID           string    `gorm:"not null;size:80"`
	TemplateVersion      string    `gorm:"not null;size:40"`
	TemplateCopyID       string    `gorm:"not null;size:64"`
	TemplateCopyVersion  int64     `gorm:"not null"`
	ProjectVersion       int64     `gorm:"not null"`
	ResourceID           string    `gorm:"not null;size:80"`
	Outcome              string    `gorm:"not null;size:24"`
	CreatedAt            time.Time `gorm:"not null;index:idx_lingdoc_ruleset_use_audit_scope,priority:3"`
}

func (ruleSetUseAuditRow) TableName() string { return "lingdoc_ruleset_use_audits" }

// RecordRuleSetUse validates the exact persisted source version and appends
// its use event in a transaction. A use of an unknown or hash-mismatched
// RuleSet is never recorded as if it were valid.
func (s *Store) RecordRuleSetUse(ctx context.Context, event template.RuleSetUseAuditEvent) (err error) {
	actor := Actor{TenantID: event.TenantID, UserID: event.ActorID, Role: event.ActorRole}
	defer func() {
		if err != nil {
			s.recordVerificationFailure(ctx, actor, event.ProjectID, err, "record_rule_set_use")
		}
	}()
	if err = s.validateActor(actor); err != nil {
		return err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := loadVersion(tx, event.TenantID, event.RuleSetID, event.RuleSetVersion)
		if err != nil {
			return err
		}
		ruleSet, err := decodeRuleSet(set)
		if err != nil {
			return err
		}
		if ruleSet.RulesetHash == "" || ruleSet.RulesetHash != event.RuleSetHash {
			return fmt.Errorf("%w: source RuleSet hash mismatch", ErrInvalidRuleSetUseAudit)
		}
		if event.Action == "check" || event.Action == "preflight" {
			if err := validateAuditEvidence(event); err != nil {
				return err
			}
		}
		if err := s.authorize(ctx, tx, Actor{TenantID: event.TenantID, UserID: event.ActorID, Role: event.ActorRole}, ActionRead, &ruleSet); err != nil {
			return err
		}
		return RecordRuleSetUseInTransaction(tx, event)
	})
	return err
}

// RecordRuleSetUseInTransaction lets snapshot/export adapters commit the use
// audit and governed artifact atomically in the same database transaction.
func RecordRuleSetUseInTransaction(tx *gorm.DB, event template.RuleSetUseAuditEvent) error {
	if tx == nil {
		return errors.New("ruleset use audit transaction is unavailable")
	}
	event, err := normalizeRuleSetUseAudit(event)
	if err != nil {
		return err
	}
	if event.Action == "check" || event.Action == "preflight" {
		if err := validateAuditEvidence(event); err != nil {
			return err
		}
	}
	// The transaction boundary must not trust a caller-stamped source hash.
	// Re-read the immutable source row while the artifact transaction is open;
	// ValidateRuleSet recomputes its canonical hash and rejects tampering.
	var source template.RuleSet
	if tx.Migrator().HasTable("lingdoc_rule_sets") {
		sourceRow, err := loadVersion(tx, event.TenantID, event.RuleSetID, event.RuleSetVersion)
		if err != nil {
			return fmt.Errorf("validate ruleset use source: %w", err)
		}
		source, err = decodeRuleSet(sourceRow)
		if err != nil {
			return fmt.Errorf("validate ruleset use source: %w", err)
		}
		if source.RulesetHash != event.RuleSetHash {
			return fmt.Errorf("%w: source RuleSet hash mismatch", ErrInvalidRuleSetUseAudit)
		}
	}
	if err := validateDurableAuditBinding(tx, event, source); err != nil {
		return err
	}
	row := ruleSetUseAuditRow{
		ID: event.ID, TenantID: event.TenantID, ActorID: event.ActorID, ActorRole: event.ActorRole,
		ProjectID: event.ProjectID, Action: event.Action, RuleSetID: event.RuleSetID,
		RuleSetVersion: event.RuleSetVersion, RuleSetHash: event.RuleSetHash,
		EffectiveRulesetHash: event.EffectiveRulesetHash, TemplateID: event.TemplateID,
		TemplateVersion: event.TemplateVersion, TemplateCopyID: event.TemplateCopyID,
		TemplateCopyVersion: event.TemplateCopyVersion, ProjectVersion: event.ProjectVersion,
		ResourceID: event.ResourceID, Outcome: event.Outcome, CreatedAt: event.CreatedAt.UTC(),
	}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("persist ruleset use audit: %w", err)
	}
	return nil
}

func validateAuditEvidence(event template.RuleSetUseAuditEvent) error {
	if event.VerifiedTemplate == nil || event.VerifiedBinding == nil {
		return fmt.Errorf("%w: check/preflight audit requires verified binding evidence", ErrInvalidRuleSetUseAudit)
	}
	if err := template.ValidateVerifiedRuleSetBinding(*event.VerifiedTemplate, *event.VerifiedBinding); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRuleSetUseAudit, err)
	}
	binding := event.VerifiedBinding
	ref := binding.Reference
	if ref.ID != event.RuleSetID || ref.Version != event.RuleSetVersion || ref.Hash != event.RuleSetHash || binding.EffectiveHash != event.EffectiveRulesetHash || event.VerifiedTemplate.ID != event.TemplateID || event.VerifiedTemplate.Version != event.TemplateVersion {
		return fmt.Errorf("%w: verified binding does not match audit identity", ErrInvalidRuleSetUseAudit)
	}
	return nil
}

type auditSnapshotPayload struct {
	ProjectID string            `json:"project_id"`
	Template  template.Template `json:"template"`
}

type auditSnapshotRow struct {
	FrozenInputJSON string `gorm:"column:frozen_input_json"`
}

type auditExportSnapshotRow struct {
	SnapshotID string `gorm:"column:snapshot_id"`
}

// validateDurableAuditBinding links the audit event to the frozen template
// payload written by the same transaction. This closes the effective-hash
// forgery gap: the event cannot claim a different template or override set.
func validateDurableAuditBinding(tx *gorm.DB, event template.RuleSetUseAuditEvent, source template.RuleSet) error {
	if event.Action != "release_snapshot" && event.Action != "export" {
		return nil
	}
	// Older standalone RuleSet-store deployments do not own delivery tables;
	// their non-transactional audit path still validates the source row above.
	// The production delivery schema has this table and therefore takes the
	// durable payload checks below.
	if !tx.Migrator().HasTable("lingdoc_release_snapshots") {
		return nil
	}
	snapshotID := event.ResourceID
	if event.Action == "export" {
		var artifact auditExportSnapshotRow
		err := tx.Table("lingdoc_export_artifacts").Select("snapshot_id").Where("id = ? AND project_id = ?", event.ResourceID, event.ProjectID).Take(&artifact).Error
		if err == nil {
			snapshotID = artifact.SnapshotID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: read governed export artifact: %v", ErrInvalidRuleSetUseAudit, err)
		}
		// Failed exports are audited before an artifact exists; in that path
		// ResourceID is the durable snapshot ID itself.
	}
	var row auditSnapshotRow
	if err := tx.Table("lingdoc_release_snapshots").Select("frozen_input_json").Where("id = ? AND project_id = ?", snapshotID, event.ProjectID).Take(&row).Error; err != nil {
		return fmt.Errorf("%w: governed action has no durable snapshot", ErrInvalidRuleSetUseAudit)
	}
	var payload auditSnapshotPayload
	if err := json.Unmarshal([]byte(row.FrozenInputJSON), &payload); err != nil {
		return fmt.Errorf("%w: decode durable snapshot: %v", ErrInvalidRuleSetUseAudit, err)
	}
	if payload.ProjectID != event.ProjectID {
		return fmt.Errorf("%w: durable snapshot project mismatch", ErrInvalidRuleSetUseAudit)
	}
	t := payload.Template
	ref := template.RuleSetReferenceFor(t)
	if ref == nil || (source.ID != "" && (ref.ID != source.ID || ref.Version != source.Version || ref.Hash != source.RulesetHash)) || ref.ID != event.RuleSetID || ref.Version != event.RuleSetVersion || ref.Hash != event.RuleSetHash {
		return fmt.Errorf("%w: durable snapshot RuleSet identity mismatch", ErrInvalidRuleSetUseAudit)
	}
	contentHash, effectiveHash, err := template.Hashes(t)
	if err != nil || t.ContentHash != contentHash || t.RulesetHash != effectiveHash || event.EffectiveRulesetHash != effectiveHash {
		return fmt.Errorf("%w: durable snapshot effective hash mismatch", ErrInvalidRuleSetUseAudit)
	}
	if t.ID != event.TemplateID || t.Version != event.TemplateVersion {
		return fmt.Errorf("%w: durable snapshot template identity mismatch", ErrInvalidRuleSetUseAudit)
	}
	return nil
}

func (s *Store) ListRuleSetUseAudits(ctx context.Context, actor Actor, projectID string) ([]template.RuleSetUseAuditEvent, error) {
	page, err := s.ListRuleSetUseAuditsPage(ctx, actor, projectID, RuleSetUseAuditQuery{Limit: RuleSetUseAuditDefaultLimit})
	return page.Items, err
}

// ListRuleSetUseAuditsPage returns a stable newest-first page. Authorization
// runs before the history query, including when the page is empty. The audit
// rows are fetched once and their referenced RuleSet versions are batch-loaded
// before any historical state is checked, avoiding one query per event.
func (s *Store) ListRuleSetUseAuditsPage(ctx context.Context, actor Actor, projectID string, query RuleSetUseAuditQuery) (page RuleSetUseAuditPage, err error) {
	started := time.Now()
	defer func() {
		s.recordAuditQueryMetrics(ctx, actor, projectID, len(page.Items), time.Since(started), err)
	}()
	if err = s.validateActor(actor); err != nil {
		return RuleSetUseAuditPage{}, err
	}
	if strings.TrimSpace(projectID) == "" {
		return RuleSetUseAuditPage{}, ErrInvalidRuleSetUseAudit
	}
	normalized, cursor, err := normalizeRuleSetUseAuditQuery(actor.TenantID, projectID, query)
	if err != nil {
		return RuleSetUseAuditPage{}, err
	}
	var events []template.RuleSetUseAuditEvent
	var nextCursor string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Authorize the project before querying history. This check is required
		// even for an empty page so a non-member cannot probe project existence.
		if err := authorizeAuditProject(tx, actor, projectID); err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, ActionRead, nil); err != nil {
			return err
		}
		queryDB := tx.Where("tenant_id = ? AND project_id = ?", actor.TenantID, projectID)
		if normalized.Action != "" {
			queryDB = queryDB.Where("action = ?", normalized.Action)
		}
		if normalized.From != nil {
			queryDB = queryDB.Where("created_at >= ?", normalized.From.UTC())
		}
		if normalized.To != nil {
			queryDB = queryDB.Where("created_at < ?", normalized.To.UTC())
		}
		if cursor != nil {
			queryDB = queryDB.Where("(created_at < ? OR (created_at = ? AND id < ?))", cursor.createdAt(), cursor.createdAt(), cursor.ID)
		}
		var rows []ruleSetUseAuditRow
		if err := queryDB.Order("created_at DESC, id DESC").Limit(normalized.Limit + 1).Find(&rows).Error; err != nil {
			return err
		}
		hasMore := len(rows) > normalized.Limit
		if hasMore {
			rows = rows[:normalized.Limit]
		}
		sets, err := batchLoadRuleSetVersions(tx, actor.TenantID, rows)
		if err != nil {
			return err
		}
		events = make([]template.RuleSetUseAuditEvent, 0, len(rows))
		authorized := make(map[string]struct{}, len(sets))
		for _, row := range rows {
			set, ok := sets[auditRuleSetKey(row.RuleSetID, row.RuleSetVersion)]
			if !ok {
				return ErrRuleSetNotFound
			}
			if set.RulesetHash != row.RuleSetHash {
				return fmt.Errorf("%w: source RuleSet hash mismatch", ErrInvalidRuleSetUseAudit)
			}
			key := auditRuleSetKey(row.RuleSetID, row.RuleSetVersion)
			if _, ok := authorized[key]; !ok {
				if err := s.authorize(ctx, tx, actor, ActionRead, &set); err != nil {
					return err
				}
				authorized[key] = struct{}{}
			}
			events = append(events, ruleSetUseAuditEventFromRow(row))
		}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			nextCursor, err = encodeRuleSetUseAuditCursor(ruleSetUseAuditCursor{
				Version: auditCursorVersion, TenantID: actor.TenantID, ProjectID: projectID,
				Action: normalized.Action, From: normalized.fromString(), To: normalized.toString(),
				CreatedAt: last.CreatedAt.UTC().Format(time.RFC3339Nano), ID: last.ID,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return RuleSetUseAuditPage{}, err
	}
	return RuleSetUseAuditPage{Items: events, NextCursor: nextCursor}, nil
}

// authorizeAuditProject validates both the project tenant and current project
// membership before any audit rows are read. Project-scoped membership is
// deliberately independent from the RuleSet authorizer, whose contract only
// knows about tenant RuleSet access.
func authorizeAuditProject(tx *gorm.DB, actor Actor, projectID string) error {
	var project struct {
		TenantID uint64 `gorm:"column:tenant_id"`
	}
	if err := tx.Table("lingdoc_projects").Select("tenant_id").Where("id = ?", projectID).Take(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRuleSetNotFound
		}
		return err
	}
	if project.TenantID != actor.TenantID {
		return ErrRuleSetNotFound
	}
	var members int64
	if err := tx.Table("lingdoc_members").Where("project_id = ? AND user_id = ?", projectID, actor.UserID).Count(&members).Error; err != nil {
		return err
	}
	if members == 0 {
		return ErrRuleSetAccessDenied
	}
	return nil
}

func (c ruleSetUseAuditCursor) createdAt() time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, c.CreatedAt)
	return parsed.UTC()
}

func (q RuleSetUseAuditQuery) fromString() string {
	if q.From == nil {
		return ""
	}
	return q.From.UTC().Format(time.RFC3339Nano)
}

func (q RuleSetUseAuditQuery) toString() string {
	if q.To == nil {
		return ""
	}
	return q.To.UTC().Format(time.RFC3339Nano)
}

func normalizeRuleSetUseAuditQuery(tenantID uint64, projectID string, query RuleSetUseAuditQuery) (RuleSetUseAuditQuery, *ruleSetUseAuditCursor, error) {
	if query.Limit == 0 {
		query.Limit = RuleSetUseAuditDefaultLimit
	}
	if query.Limit < 1 || query.Limit > RuleSetUseAuditMaxLimit || (query.Action != "" && !validUseAction(query.Action)) {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	if query.From != nil {
		from := query.From.UTC()
		query.From = &from
	}
	if query.To != nil {
		to := query.To.UTC()
		query.To = &to
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	if query.Cursor == "" {
		return query, nil, nil
	}
	if len(query.Cursor) > auditCursorMaxBytes {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
	if err != nil {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	var cursor ruleSetUseAuditCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.Version != auditCursorVersion || cursor.TenantID != tenantID || cursor.ProjectID != projectID || cursor.Action != query.Action || cursor.From != query.fromString() || cursor.To != query.toString() || cursor.ID == "" {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt); err != nil {
		return RuleSetUseAuditQuery{}, nil, ErrInvalidRuleSetUseAudit
	}
	return query, &cursor, nil
}

func encodeRuleSetUseAuditCursor(cursor ruleSetUseAuditCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func auditRuleSetKey(id, version string) string { return id + "\x00" + version }

func batchLoadRuleSetVersions(tx *gorm.DB, tenantID uint64, rows []ruleSetUseAuditRow) (map[string]template.RuleSet, error) {
	sets := make(map[string]template.RuleSet)
	if len(rows) == 0 {
		return sets, nil
	}
	clauses := make([]string, 0, len(rows))
	args := []any{tenantID}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key := auditRuleSetKey(row.RuleSetID, row.RuleSetVersion)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clauses = append(clauses, "(ruleset_id = ? AND version = ?)")
		args = append(args, row.RuleSetID, row.RuleSetVersion)
	}
	var versions []ruleSetVersionRow
	if err := tx.Where("tenant_id = ? AND ("+strings.Join(clauses, " OR ")+")", args...).Find(&versions).Error; err != nil {
		return nil, err
	}
	for _, row := range versions {
		set, err := decodeRuleSet(row)
		if err != nil {
			return nil, err
		}
		sets[auditRuleSetKey(row.RuleSetID, row.Version)] = set
	}
	return sets, nil
}

func ruleSetUseAuditEventFromRow(row ruleSetUseAuditRow) template.RuleSetUseAuditEvent {
	return template.RuleSetUseAuditEvent{
		ID: row.ID, TenantID: row.TenantID, ActorID: row.ActorID, ActorRole: row.ActorRole,
		ProjectID: row.ProjectID, Action: row.Action, RuleSetID: row.RuleSetID,
		RuleSetVersion: row.RuleSetVersion, RuleSetHash: row.RuleSetHash,
		EffectiveRulesetHash: row.EffectiveRulesetHash, TemplateID: row.TemplateID,
		TemplateVersion: row.TemplateVersion, TemplateCopyID: row.TemplateCopyID,
		TemplateCopyVersion: row.TemplateCopyVersion, ProjectVersion: row.ProjectVersion,
		ResourceID: row.ResourceID, Outcome: row.Outcome, CreatedAt: row.CreatedAt.UTC(),
	}
}

func normalizeRuleSetUseAudit(event template.RuleSetUseAuditEvent) (template.RuleSetUseAuditEvent, error) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.TenantID == 0 || strings.TrimSpace(event.ActorID) == "" || strings.TrimSpace(event.ActorRole) == "" ||
		strings.TrimSpace(event.ProjectID) == "" || !validUseAction(event.Action) ||
		strings.TrimSpace(event.RuleSetID) == "" || strings.TrimSpace(event.RuleSetVersion) == "" || len(event.RuleSetHash) != 64 ||
		len(event.EffectiveRulesetHash) != 64 || strings.TrimSpace(event.TemplateID) == "" || strings.TrimSpace(event.TemplateVersion) == "" ||
		strings.TrimSpace(event.Outcome) == "" {
		return template.RuleSetUseAuditEvent{}, ErrInvalidRuleSetUseAudit
	}
	return event, nil
}

func validUseAction(action string) bool {
	switch action {
	case "check", "preflight", "release_snapshot", "export":
		return true
	default:
		return false
	}
}
