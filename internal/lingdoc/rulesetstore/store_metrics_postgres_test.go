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

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// #193 AC1/US-22 的 PostgreSQL 方言变体:US-22 四指标的持久化与 HTTP 读回此前
// 只有 SQLite 证据(metrics_test.go 的 openMetricsDatabase 硬编码 SQLite,
// closeout 在 PG 进程环境重跑时仍落在 SQLite 文件上)。本文件把同一组断言在
// 生产 PG 迁移链的一次性 schema 上重跑。DSN 未配置时按口径跳过(skip 不计
// 通过)。
func TestPostgresRuleSetMetricsP95P99PersistenceAndHTTPReadback(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootSQL.Close() }()

	schema := fmt.Sprintf("rule_set_metrics_%d", time.Now().UnixNano())
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}

	// 生产迁移链落在该 schema:指标表/使用审计表/规则集表由迁移创建
	// (与 TestPostgresRuleSetMigrationAndLifecycle 同一 fixture 约定)。
	// 平台 stub 表先建(tenants 被迁移外键引用、audit_logs 不被迁移链创建):
	// 与 TestPostgresRuleSetMigrationAndLifecycle 的同一约定。
	for _, statement := range []string{
		`CREATE TABLE tenants (id BIGINT PRIMARY KEY)`,
		`INSERT INTO tenants (id) VALUES (1)`,
		`CREATE TABLE audit_logs (
			id BIGSERIAL PRIMARY KEY,
			tenant_id BIGINT NOT NULL,
			actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
			actor_role VARCHAR(32) NOT NULL DEFAULT '',
			action VARCHAR(64) NOT NULL,
			target_type VARCHAR(32) NOT NULL DEFAULT '',
			target_id VARCHAR(64) NOT NULL DEFAULT '',
			target_user_id VARCHAR(36) NOT NULL DEFAULT '',
			request_path VARCHAR(512) NOT NULL DEFAULT '',
			request_method VARCHAR(16) NOT NULL DEFAULT '',
			outcome VARCHAR(16) NOT NULL DEFAULT 'success',
			details JSONB NOT NULL DEFAULT '{}'::JSONB,
			scope_type VARCHAR(32) NOT NULL DEFAULT '',
			scope_id VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := fixturemigrations.Select(filepath.Join("..", "..", "..", "migrations", "versioned"), postgresFixtureTables)
	if err != nil {
		t.Fatal(err)
	}
	statements, err := migrations.SQL(filepath.Join("..", "..", "..", "migrations", "versioned"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement.SQL).Error; err != nil {
			t.Fatalf("apply PostgreSQL migration %s: %v", statement.Name, err)
		}
	}

	actor := Actor{TenantID: 1, UserID: "owner", Role: string(types.TenantRoleAdmin)}
	if err := db.Exec("INSERT INTO lingdoc_projects(id, tenant_id, name, status, project_version, spec_revision, current_context_revision, template_id, template_version) VALUES (?, ?, 'metrics-pg-iso', 'active', 1, 1, 1, 'template-demo', '1')", "project-metrics-pg", actor.TenantID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members(project_id, user_id, role) VALUES (?, ?, ?)", "project-metrics-pg", actor.UserID, "owner").Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	ctx := context.Background()
	store := NewStore(db, testAuthorizer{allow: true})
	draft := testDraft("v-metrics-pg")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	for i := 0; i < 100; i++ {
		row := template.RuleSetUseAuditEvent{
			TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
			ProjectID: "project-metrics-pg", Action: "preflight", Outcome: "allowed",
			RuleSetID: verifiedBinding.Reference.ID, RuleSetVersion: verifiedBinding.Reference.Version,
			RuleSetHash: verifiedBinding.Reference.Hash, EffectiveRulesetHash: verifiedBinding.EffectiveHash,
			TemplateID: verifiedTemplate.ID, TemplateVersion: verifiedTemplate.Version,
			VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}
		if err := RecordRuleSetUseInTransaction(db, row); err != nil {
			t.Fatalf("record use audit %d: %v", i, err)
		}
		if err := db.Exec(
			"INSERT INTO lingdoc_ruleset_metrics (tenant_id, project_id, id, metric, operation, value, created_at) VALUES (?,?,?,?,?,?,?)",
			actor.TenantID, "project-metrics-pg", fmt.Sprintf("latency-pg-%03d", i), MetricAuditQueryLatency, "list_rule_set_use_audits", i*1000, now.Add(time.Duration(i)*time.Millisecond),
		).Error; err != nil {
			t.Fatalf("seed metric %d: %v", i, err)
		}
	}

	group := gin.New()
	NewHTTPHandler(store, testRequestInfo).Register(group, group)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics-pg/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext(actor.UserID, actor.Role))
	group.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			AuditQueryCount int64    `json:"audit_query_count"`
			AuditQueryP95Ms float64  `json:"audit_query_p95_ms"`
			AuditQueryP99Ms float64  `json:"audit_query_p99_ms"`
			Verification    struct{} `json:"verification"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode metrics envelope: %v", err)
	}
	snapshot := envelope.Data
	if snapshot.AuditQueryCount != 100 {
		t.Fatalf("audit query count = %d, want 100", snapshot.AuditQueryCount)
	}
	// 独立真值:样本 value=i(ms),i=0..99,nearest-rank p95=sorted[94]=94、
	// p99=sorted[98]=98(与 SQLite 版样本集不同,期望值各自独立计算)。
	if snapshot.AuditQueryP95Ms != 94 || snapshot.AuditQueryP99Ms != 98 {
		t.Fatalf("latency snapshot = %+v, want p95 94ms p99 98ms", snapshot)
	}
}
