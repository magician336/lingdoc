package rulesetstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRuleSetMetricsP95P99PersistenceAndHTTPReadback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, path := openMetricsDatabase(t)
	actor := Actor{TenantID: 1, UserID: "owner", Role: "tenant_admin"}
	if err := db.Exec("INSERT INTO lingdoc_projects(id, tenant_id) VALUES (?, ?)", "project-metrics", actor.TenantID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members(project_id, user_id, role) VALUES (?, ?, ?)", "project-metrics", actor.UserID, "owner").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store := NewStore(db, testAuthorizer{allow: true})
	for i := int64(1); i <= 100; i++ {
		row := ruleSetMetricRow{
			TenantID: actor.TenantID, ProjectID: "project-metrics",
			Metric: MetricAuditQueryLatency, Operation: "list_rule_set_use_audits", Value: i * 1000, CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}
		row.ID = "latency-" + formatMetricID(i)
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&ruleSetMetricRow{
			ID: "page-" + formatMetricID(i), TenantID: actor.TenantID, ProjectID: "project-metrics",
			Metric: MetricAuditPageSize, Operation: "list_rule_set_use_audits", Value: i, CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.Create(&ruleSetMetricRow{ID: "failure-" + formatMetricID(int64(i)), TenantID: actor.TenantID, ProjectID: "project-metrics", Metric: MetricVerificationFailure, Operation: "record_rule_set_use", Value: 1, FailureReason: "invalid_evidence", CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.RecordTransactionalCapabilityError(context.Background(), "snapshot", "missing_atomic_audit_port")
	snapshot, err := store.ReadRuleSetMetrics(context.Background(), actor, "project-metrics", RuleSetMetricsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	assertMetricSnapshot(t, snapshot)

	router := gin.New()
	group := router.Group("/api/v1/lingdoc")
	NewHTTPHandler(store, testRequestInfo).Register(group, group)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext(actor.UserID, actor.Role))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics HTTP status = %d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data RuleSetMetricsSnapshot `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	assertMetricSnapshot(t, envelope.Data)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=foreign_keys(1)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	reopenedSQL, err := reopened.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedSQL.Close() })
	readBack, err := NewStore(reopened, testAuthorizer{allow: true}).ReadRuleSetMetrics(context.Background(), actor, "project-metrics", RuleSetMetricsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	assertMetricSnapshot(t, readBack)
}

func assertMetricSnapshot(t *testing.T, snapshot RuleSetMetricsSnapshot) {
	t.Helper()
	if snapshot.AuditQueryCount != 100 || snapshot.AuditQueryP95Ms != 95 || snapshot.AuditQueryP99Ms != 99 {
		t.Fatalf("latency snapshot = %+v, want count 100 p95 95ms p99 99ms", snapshot)
	}
	if snapshot.AuditPageSizeCount != 100 || snapshot.AuditPageSizeP95 != 95 || snapshot.AuditPageSizeP99 != 99 {
		t.Fatalf("page snapshot = %+v, want count 100 p95 95 p99 99", snapshot)
	}
	if snapshot.AuditPageSizeDistribution["1-10"] != 10 || snapshot.AuditPageSizeDistribution["11-50"] != 40 || snapshot.AuditPageSizeDistribution["51-100"] != 50 {
		t.Fatalf("page distribution = %+v", snapshot.AuditPageSizeDistribution)
	}
	if snapshot.VerificationFailures["invalid_evidence"] != 2 {
		t.Fatalf("verification failures = %+v", snapshot.VerificationFailures)
	}
	if snapshot.TransactionalCapabilityErrors["snapshot:missing_atomic_audit_port"] != 1 {
		t.Fatalf("capability errors = %+v", snapshot.TransactionalCapabilityErrors)
	}
}

func openMetricsDatabase(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=foreign_keys(1)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		"CREATE TABLE tenants (id INTEGER PRIMARY KEY)",
		"INSERT INTO tenants(id) VALUES (1)",
		"CREATE TABLE lingdoc_projects (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL DEFAULT 1)",
		"CREATE TABLE lingdoc_members (project_id TEXT NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL, PRIMARY KEY(project_id, user_id))",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"000036_lingdoc_rule_sets.up.sql", "000040_lingdoc_ruleset_use_audits.up.sql", "000042_lingdoc_ruleset_metrics.up.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func formatMetricID(value int64) string {
	return fmt.Sprintf("%03d", value)
}
