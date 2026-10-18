package rulesetstore

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	MetricAuditQueryLatency       = "audit_query_latency"
	MetricAuditPageSize           = "audit_page_size"
	MetricVerificationFailure     = "verification_failure"
	MetricTransactionalCapability = "transactional_capability"
)

var ErrRuleSetMetricsUnavailable = errors.New("ruleset metrics storage unavailable")

// RuleSetMetricsQuery bounds the persisted operational evidence read. The
// project authorization check is performed before any metric row is queried.
type RuleSetMetricsQuery struct {
	From *time.Time
	To   *time.Time
}

// RuleSetMetricsSnapshot is the application-facing aggregate returned by the
// metrics HTTP seam. Latency is reported in milliseconds; page-size quantiles
// are item counts. Failure maps use stable, low-cardinality reason keys.
type RuleSetMetricsSnapshot struct {
	AuditQueryCount               int64            `json:"audit_query_count"`
	AuditQueryP95Ms               float64          `json:"audit_query_p95_ms"`
	AuditQueryP99Ms               float64          `json:"audit_query_p99_ms"`
	AuditPageSizeCount            int64            `json:"audit_page_size_count"`
	AuditPageSizeP95              float64          `json:"audit_page_size_p95"`
	AuditPageSizeP99              float64          `json:"audit_page_size_p99"`
	AuditPageSizeDistribution     map[string]int64 `json:"audit_page_size_distribution"`
	VerificationFailures          map[string]int64 `json:"verification_failures"`
	TransactionalCapabilityErrors map[string]int64 `json:"transactional_capability_errors"`
}

type ruleSetMetricRow struct {
	ID            string    `gorm:"primaryKey;size:36"`
	TenantID      uint64    `gorm:"not null;index:idx_lingdoc_ruleset_metrics_scope,priority:1"`
	ProjectID     string    `gorm:"not null;size:64;index:idx_lingdoc_ruleset_metrics_scope,priority:2"`
	Metric        string    `gorm:"not null;size:48;index:idx_lingdoc_ruleset_metrics_scope,priority:3"`
	Operation     string    `gorm:"not null;size:64"`
	Value         int64     `gorm:"not null;default:0"`
	FailureReason string    `gorm:"not null;size:64"`
	Capability    string    `gorm:"not null;size:64"`
	CreatedAt     time.Time `gorm:"not null;index:idx_lingdoc_ruleset_metrics_scope,priority:4"`
}

func (ruleSetMetricRow) TableName() string { return "lingdoc_ruleset_metrics" }

// RecordTransactionalCapabilityError is the observer used by the delivery
// composition root. Tenant 0 denotes a global wiring capability, so it is not
// mixed with tenant-owned audit evidence and can be reported to an authorized
// project metrics reader without exposing another tenant's data.
func (s *Store) RecordTransactionalCapabilityError(ctx context.Context, capability, reason string) {
	s.recordMetric(ctx, 0, "", MetricTransactionalCapability, "composition", 1, reason, capability)
}

func (s *Store) recordMetric(ctx context.Context, tenantID uint64, projectID, metric, operation string, value int64, failureReason, capability string) {
	if s == nil || s.db == nil || ctx == nil || !s.db.Migrator().HasTable((ruleSetMetricRow{}).TableName()) {
		return
	}
	_ = s.db.WithContext(ctx).Create(&ruleSetMetricRow{
		ID: uuid.NewString(), TenantID: tenantID, ProjectID: projectID,
		Metric: metric, Operation: operation, Value: value,
		FailureReason: failureReason, Capability: capability, CreatedAt: time.Now().UTC(),
	}).Error
}

func (s *Store) recordAuditQueryMetrics(ctx context.Context, actor Actor, projectID string, pageSize int, elapsed time.Duration, err error) {
	if err != nil {
		s.recordMetric(ctx, actor.TenantID, projectID, MetricVerificationFailure, "list_rule_set_use_audits", 1, verificationFailureReason(err), "")
	}
	latency := elapsed.Microseconds()
	if latency < 1 {
		latency = 1
	}
	s.recordMetric(ctx, actor.TenantID, projectID, MetricAuditQueryLatency, "list_rule_set_use_audits", latency, "", "")
	s.recordMetric(ctx, actor.TenantID, projectID, MetricAuditPageSize, "list_rule_set_use_audits", int64(pageSize), "", "")
}

func (s *Store) recordVerificationFailure(ctx context.Context, actor Actor, projectID string, err error, operation string) {
	if err != nil {
		s.recordMetric(ctx, actor.TenantID, projectID, MetricVerificationFailure, operation, 1, verificationFailureReason(err), "")
	}
}

func verificationFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRuleSetUseAudit):
		return "invalid_evidence"
	case errors.Is(err, ErrRuleSetAccessDenied):
		return "authorization_denied"
	case errors.Is(err, ErrRuleSetNotFound):
		return "not_found"
	case errors.Is(err, ErrAuthorizerUnavailable):
		return "authorization_unavailable"
	case errors.Is(err, ErrActorRequired), errors.Is(err, ErrRuleSetTenantRequired):
		return "invalid_actor"
	default:
		return "persistence_error"
	}
}

// ReadRuleSetMetrics returns persisted samples and computes exact nearest-rank
// p95/p99 values. The calculation is intentionally in the application seam so
// HTTP consumers can read the same evidence that SQLite/PostgreSQL persisted.
func (s *Store) ReadRuleSetMetrics(ctx context.Context, actor Actor, projectID string, query RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error) {
	if err := s.validateActor(actor); err != nil {
		return RuleSetMetricsSnapshot{}, err
	}
	if strings.TrimSpace(projectID) == "" {
		return RuleSetMetricsSnapshot{}, ErrInvalidRuleSetUseAudit
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
		return RuleSetMetricsSnapshot{}, ErrInvalidRuleSetUseAudit
	}
	var rows []ruleSetMetricRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := authorizeAuditProject(tx, actor, projectID); err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, ActionRead, nil); err != nil {
			return err
		}
		if !tx.Migrator().HasTable((ruleSetMetricRow{}).TableName()) {
			return ErrRuleSetMetricsUnavailable
		}
		db := tx.Where("((tenant_id = ? AND project_id = ?) OR tenant_id = 0)", actor.TenantID, projectID)
		if query.From != nil {
			db = db.Where("created_at >= ?", query.From.UTC())
		}
		if query.To != nil {
			db = db.Where("created_at < ?", query.To.UTC())
		}
		return db.Order("created_at ASC, id ASC").Find(&rows).Error
	})
	if err != nil {
		s.recordVerificationFailure(ctx, actor, projectID, err, "read_rule_set_metrics")
		return RuleSetMetricsSnapshot{}, err
	}
	return summarizeRuleSetMetrics(rows), nil
}

func summarizeRuleSetMetrics(rows []ruleSetMetricRow) RuleSetMetricsSnapshot {
	snapshot := RuleSetMetricsSnapshot{
		AuditPageSizeDistribution:     map[string]int64{},
		VerificationFailures:          map[string]int64{},
		TransactionalCapabilityErrors: map[string]int64{},
	}
	var latencies, pageSizes []int64
	for _, row := range rows {
		switch row.Metric {
		case MetricAuditQueryLatency:
			snapshot.AuditQueryCount++
			latencies = append(latencies, row.Value)
		case MetricAuditPageSize:
			snapshot.AuditPageSizeCount++
			pageSizes = append(pageSizes, row.Value)
			snapshot.AuditPageSizeDistribution[pageSizeBucket(row.Value)]++
		case MetricVerificationFailure:
			snapshot.VerificationFailures[row.FailureReason]++
		case MetricTransactionalCapability:
			key := row.Capability
			if row.FailureReason != "" {
				key += ":" + row.FailureReason
			}
			snapshot.TransactionalCapabilityErrors[key]++
		}
	}
	snapshot.AuditQueryP95Ms = float64(nearestRank(latencies, 0.95)) / 1000
	snapshot.AuditQueryP99Ms = float64(nearestRank(latencies, 0.99)) / 1000
	snapshot.AuditPageSizeP95 = float64(nearestRank(pageSizes, 0.95))
	snapshot.AuditPageSizeP99 = float64(nearestRank(pageSizes, 0.99))
	return snapshot
}

func nearestRank(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func pageSizeBucket(size int64) string {
	switch {
	case size <= 0:
		return "0"
	case size <= 10:
		return "1-10"
	case size <= 50:
		return "11-50"
	case size <= 100:
		return "51-100"
	default:
		return "101+"
	}
}
