package container

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

const (
	g4E2ETenantID = uint64(9142)
	g4E2EActorID  = "g4-e2e-admin"
	g4E2ERuleSet  = "g4-e2e-grant-rules"
	g4E2EVersion  = "v1"
)

type g4E2EEnvelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		Replayed bool `json:"replayed"`
	} `json:"meta"`
}

func TestRuleSetProductionHTTPToExportPersistsAcrossRestartSQLite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "g4-e2e.sqlite")
	db := openG4E2ESQLite(t, dbPath)
	applyG4E2ESQLiteSchema(t, db)
	runG4RuleSetDeliveryE2E(t, db, "sqlite", func() *gorm.DB { return openG4E2ESQLite(t, dbPath) })
}

func TestRuleSetProductionHTTPToExportPersistsAcrossRestartPostgres(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires a disposable PostgreSQL test database")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer rootSQL.Close()
	schema := fmt.Sprintf("lingdoc_g4_e2e_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
	}()
	open := func() *gorm.DB {
		db, err := gorm.Open(postgres.Open(g4E2EPostgresDSN(dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		return db
	}
	db := open()
	applyG4E2EPostgresSchema(t, db)
	runG4RuleSetDeliveryE2E(t, db, "postgres", open)
}

func g4E2EPostgresDSN(dsn, schema string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func openG4E2ESQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func applyG4E2ESQLiteSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE tenants (id INTEGER PRIMARY KEY)",
		"CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, is_active INTEGER NOT NULL, deleted_at DATETIME)",
		"INSERT INTO users (id, username, is_active) VALUES ('g4-e2e-admin', 'g4-e2e-admin', 1)",
		"INSERT INTO tenants (id) VALUES (9142)",
		"CREATE TABLE chunks (id VARCHAR(36) PRIMARY KEY, deleted_at DATETIME)",
		`CREATE TABLE tenant_members (
			tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL,
			status TEXT NOT NULL, deleted_at DATETIME, PRIMARY KEY (tenant_id, user_id)
		)`,
		"INSERT INTO tenant_members (tenant_id, user_id, role, status) VALUES (9142, 'g4-e2e-admin', 'admin', 'active')",
		`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL, target_type TEXT NOT NULL DEFAULT '', target_id TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL DEFAULT 'success', details TEXT NOT NULL DEFAULT '{}',
			scope_type TEXT NOT NULL DEFAULT '', scope_id TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create SQLite test prerequisite: %v", err)
		}
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"000018_lingdoc_workspace.up.sql", "000019_lingdoc_evidence_assets.up.sql",
		"000020_lingdoc_candidate_adoption.up.sql", "000023_lingdoc_chapter_confirmation_requests.up.sql",
		"000024_lingdoc_delivery_stores.up.sql", "000025_lingdoc_member_permissions.up.sql",
		"000026_lingdoc_project_context_revision.up.sql", "000027_lingdoc_draft_provenance.up.sql",
		"000028_lingdoc_owner_transfers.up.sql", "000029_lingdoc_project_discard.up.sql",
		"000030_lingdoc_project_baseline.up.sql", "000031_lingdoc_citation_usages.up.sql",
		"000032_lingdoc_change_sets.up.sql", "000033_lingdoc_working_copies.up.sql",
		"000034_lingdoc_selected_rewrites.up.sql", "000035_lingdoc_project_template_copies.up.sql",
		"000036_lingdoc_rule_sets.up.sql", "000037_lingdoc_full_workflow.up.sql",
		"000039_lingdoc_ruleset_export_reference.up.sql",
		"000040_lingdoc_ruleset_use_audits.up.sql", "000041_lingdoc_template_version_ruleset_ref.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("apply production SQLite migration %s: %v", name, err)
		}
	}
}

func applyG4E2EPostgresSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE tenants (id BIGINT PRIMARY KEY)",
		"CREATE TABLE users (id VARCHAR(64) PRIMARY KEY, username TEXT, is_active BOOLEAN NOT NULL, deleted_at TIMESTAMPTZ)",
		"INSERT INTO users (id, username, is_active) VALUES ('g4-e2e-admin', 'g4-e2e-admin', TRUE)",
		"INSERT INTO tenants (id) VALUES (9142)",
		"CREATE TABLE chunks (id VARCHAR(36) PRIMARY KEY, deleted_at TIMESTAMPTZ)",
		`CREATE TABLE tenant_members (
			tenant_id BIGINT NOT NULL, user_id VARCHAR(64) NOT NULL, role VARCHAR(32) NOT NULL,
			status VARCHAR(16) NOT NULL, deleted_at TIMESTAMPTZ, PRIMARY KEY (tenant_id, user_id)
		)`,
		"INSERT INTO tenant_members (tenant_id, user_id, role, status) VALUES (9142, 'g4-e2e-admin', 'admin', 'active')",
		`CREATE TABLE audit_logs (
			id BIGSERIAL PRIMARY KEY, tenant_id BIGINT NOT NULL,
			actor_user_id VARCHAR(36) NOT NULL DEFAULT '', actor_role VARCHAR(32) NOT NULL DEFAULT '',
			action VARCHAR(64) NOT NULL, target_type VARCHAR(32) NOT NULL DEFAULT '', target_id VARCHAR(64) NOT NULL DEFAULT '',
			outcome VARCHAR(16) NOT NULL DEFAULT 'success', details JSONB NOT NULL DEFAULT '{}'::JSONB,
			scope_type VARCHAR(32) NOT NULL DEFAULT '', scope_id VARCHAR(64) NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create PostgreSQL test prerequisite: %v", err)
		}
	}
	for _, name := range []string{
		"000097_lingdoc_workspace.up.sql", "000098_lingdoc_evidence_assets.up.sql",
		"000099_lingdoc_candidate_adoption.up.sql", "000102_lingdoc_chapter_confirmation_requests.up.sql",
		"000103_lingdoc_delivery_stores.up.sql", "000104_lingdoc_member_permissions.up.sql",
		"000105_lingdoc_project_context_revision.up.sql", "000106_lingdoc_draft_provenance.up.sql",
		"000107_lingdoc_owner_transfers.up.sql", "000108_lingdoc_project_discard.up.sql",
		"000109_lingdoc_project_baseline.up.sql", "000110_lingdoc_citation_usages.up.sql",
		"000111_lingdoc_change_sets.up.sql", "000112_lingdoc_working_copies.up.sql",
		"000113_lingdoc_selected_rewrites.up.sql", "000114_lingdoc_project_template_copies.up.sql",
		"000115_lingdoc_rule_sets.up.sql", "000116_lingdoc_full_workflow.up.sql",
		"000118_lingdoc_ruleset_export_reference.up.sql",
		"000119_lingdoc_ruleset_use_audits.up.sql", "000120_lingdoc_template_version_ruleset_ref.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "versioned", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(raw)).Error; err != nil {
			t.Fatalf("apply production PostgreSQL migration %s: %v", name, err)
		}
	}
}

func runG4RuleSetDeliveryE2E(t *testing.T, db *gorm.DB, dialect string, reopen func() *gorm.DB) {
	t.Helper()
	first := buildG4E2EHTTP(t, db)
	request := func(router *gin.Engine, method, path, body, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		var requestBody io.Reader
		if reader != nil {
			requestBody = reader
		}
		requestPath := "/api/v1/lingdoc" + path
		if strings.HasPrefix(path, "/api/v1/lingdoc/") {
			requestPath = path
		}
		req := httptest.NewRequest(method, requestPath, requestBody)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "g4-e2e")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s (body=%q)", method, path, res.Code, want, res.Body.String(), body)
		}
		return res
	}
	decode := func(res *httptest.ResponseRecorder, target any) g4E2EEnvelope {
		t.Helper()
		var envelope g4E2EEnvelope
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode HTTP response %q: %v", res.Body.String(), err)
		}
		if len(envelope.Data) == 0 {
			t.Fatalf("HTTP response has no data envelope: %s", res.Body.String())
		}
		if target != nil {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				t.Fatalf("decode response data %q: %v", envelope.Data, err)
			}
		}
		return envelope
	}

	draftBody := `{"ruleset_id":"` + g4E2ERuleSet + `","version":"` + g4E2EVersion + `","bindings":[{"rule_id":"method-length","definition_id":"computed/word_count","definition_version":"1","origin":"organization","target_kind":"chapter","target_id":"method","parameters":{"minimum":10,"maximum":500},"severity":"warning","trigger_scenes":["preflight"],"enabled":true,"owner":"research-office"}]}`
	createdRuleSet := request(first.router, http.MethodPost, "/rule-sets", draftBody, "", http.StatusCreated)
	var draft lingdoctemplate.RuleSet
	decode(createdRuleSet, &draft)
	if draft.ID != g4E2ERuleSet || draft.State != lingdoctemplate.RuleSetDraft {
		t.Fatalf("created RuleSet = %+v", draft)
	}
	concurrentRuleSetID := "g4-e2e-concurrent-publish"
	concurrentDraftBody := strings.Replace(draftBody, g4E2ERuleSet, concurrentRuleSetID, 1)
	request(first.router, http.MethodPost, "/rule-sets", concurrentDraftBody, "", http.StatusCreated)
	startPublish := make(chan struct{})
	publishResults := make(chan error, 2)
	actor := rulesetstore.Actor{TenantID: g4E2ETenantID, UserID: g4E2EActorID, Role: "admin"}
	for range 2 {
		go func() {
			<-startPublish
			_, err := first.ruleSetStore.Publish(context.Background(), actor, concurrentRuleSetID, g4E2EVersion)
			publishResults <- err
		}()
	}
	close(startPublish)
	publishSuccesses := 0
	var publishErrors []error
	for range 2 {
		if err := <-publishResults; err == nil {
			publishSuccesses++
		} else {
			publishErrors = append(publishErrors, err)
		}
	}
	if publishSuccesses != 1 {
		t.Fatalf("concurrent publish successes = %d, want exactly one; errors=%v", publishSuccesses, publishErrors)
	}
	var concurrentPublishAudits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_id = ? AND action = ?", g4E2ETenantID, concurrentRuleSetID, "lingdoc.ruleset.publish").Count(&concurrentPublishAudits).Error; err != nil || concurrentPublishAudits != 1 {
		t.Fatalf("concurrent publish audit rows = %d, err=%v; want exactly one", concurrentPublishAudits, err)
	}
	setG4AuditFailureTrigger(t, db, dialect, "audit_logs", "lingdoc.ruleset.publish", "g4_fail_publish_audit", true)
	request(first.router, http.MethodPost, "/rule-sets/"+g4E2ERuleSet+"/versions/"+g4E2EVersion+"/publish", `{}`, "", http.StatusInternalServerError)
	setG4AuditFailureTrigger(t, db, dialect, "audit_logs", "lingdoc.ruleset.publish", "g4_fail_publish_audit", false)
	var storedDraft lingdoctemplate.RuleSet
	decode(request(first.router, http.MethodGet, "/rule-sets/"+g4E2ERuleSet+"/versions/"+g4E2EVersion, "", "", http.StatusOK), &storedDraft)
	if storedDraft.State != lingdoctemplate.RuleSetDraft || storedDraft.RulesetHash != "" {
		t.Fatalf("failed publish audit left partial RuleSet transition: %+v", storedDraft)
	}
	var published lingdoctemplate.RuleSet
	decode(request(first.router, http.MethodPost, "/rule-sets/"+g4E2ERuleSet+"/versions/"+g4E2EVersion+"/publish", `{}`, "", http.StatusOK), &published)
	if published.State != lingdoctemplate.RuleSetPublished || len(published.RulesetHash) != 64 {
		t.Fatalf("published RuleSet = %+v", published)
	}

	var project workspacecore.Project
	decode(request(first.router, http.MethodPost, "/projects", `{"name":"synthetic G4 E2E","template_id":"template-demo"}`, "g4-create-project-1", http.StatusCreated), &project)
	if project.ID == "" || project.TemplateCopyVersion != 1 || project.ProjectVersion != 1 {
		t.Fatalf("created project = %+v", project)
	}
	bindPath := "/projects/" + project.ID + "/template-copy/rule-set"
	plainBind := `{"expected_project_version":1,"expected_template_copy_version":1,"ruleset_id":"` + g4E2ERuleSet + `","ruleset_version":"` + g4E2EVersion + `","overrides":[]}`
	var firstBound workspacecore.Project
	decode(request(first.router, http.MethodPut, bindPath, plainBind, "g4-bind-plain-01", http.StatusOK), &firstBound)
	if firstBound.ProjectVersion != 2 || firstBound.TemplateCopyVersion != 2 {
		t.Fatalf("plain bind did not advance project/copy atomically: %+v", firstBound)
	}
	if replay := decode(request(first.router, http.MethodPut, bindPath, plainBind, "g4-bind-plain-01", http.StatusOK), &firstBound); !replay.Meta.Replayed || firstBound.ProjectVersion != 2 || firstBound.TemplateCopyVersion != 2 {
		t.Fatalf("plain binding retry was not idempotent: meta=%+v project=%+v", replay.Meta, firstBound)
	}
	badOverride := `{"expected_project_version":2,"expected_template_copy_version":2,"ruleset_id":"` + g4E2ERuleSet + `","ruleset_version":"` + g4E2EVersion + `","overrides":[{"rule_id":"method-length","parameters":{"unreviewed_parameter":7}}]}`
	request(first.router, http.MethodPut, bindPath, badOverride, "g4-invalid-override", http.StatusBadRequest)
	var unchanged workspacecore.Project
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &unchanged)
	if unchanged.ProjectVersion != 2 || unchanged.TemplateCopyVersion != 2 {
		t.Fatalf("rejected override partially updated project: version=%d copy=%d", unchanged.ProjectVersion, unchanged.TemplateCopyVersion)
	}
	validOverride := `{"expected_project_version":2,"expected_template_copy_version":2,"ruleset_id":"` + g4E2ERuleSet + `","ruleset_version":"` + g4E2EVersion + `","overrides":[{"rule_id":"method-length","parameters":{"minimum":5}}]}`
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_project_audits", "project_template.override_ruleset", "g4_fail_project_audit", true)
	request(first.router, http.MethodPut, bindPath, validOverride, "g4-bind-override", http.StatusInternalServerError)
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_project_audits", "project_template.override_ruleset", "g4_fail_project_audit", false)
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &unchanged)
	if unchanged.ProjectVersion != 2 || unchanged.TemplateCopyVersion != 2 {
		t.Fatalf("failed override audit left partial project/copy: version=%d copy=%d", unchanged.ProjectVersion, unchanged.TemplateCopyVersion)
	}
	boundResponse := request(first.router, http.MethodPut, bindPath, validOverride, "g4-bind-override", http.StatusOK)
	var bound workspacecore.Project
	decode(boundResponse, &bound)
	if bound.ProjectVersion != 3 || bound.TemplateCopyVersion != 3 || bound.TemplateCopy == nil {
		t.Fatalf("bound project/copy version not advanced atomically: %+v", bound)
	}
	wantRef := &lingdoctemplate.RuleSetReference{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}
	if bound.TemplateCopy.TemplateVersionRuleSetRef == nil || *bound.TemplateCopy.TemplateVersionRuleSetRef != *wantRef ||
		bound.TemplateCopy.Definition.RuleSetRef == nil || *bound.TemplateCopy.Definition.RuleSetRef != *wantRef ||
		bound.TemplateCopy.Definition.RuleSetSnapshot == nil || bound.TemplateCopy.Definition.RuleSetSnapshot.RulesetHash != published.RulesetHash {
		t.Fatalf("binding did not pin exact RuleSet reference: %+v", bound.TemplateCopy)
	}
	var replayedProject workspacecore.Project
	replayed := decode(request(first.router, http.MethodPut, bindPath, validOverride, "g4-bind-override", http.StatusOK), &replayedProject)
	if !replayed.Meta.Replayed || replayedProject.ProjectVersion != 3 || replayedProject.TemplateCopyVersion != 3 {
		t.Fatalf("binding retry was not idempotent: meta=%+v project=%+v", replayed.Meta, replayedProject)
	}

	var projectAfterSpec workspacecore.Project
	decode(request(first.router, http.MethodPut, "/projects/"+project.ID+"/spec", `{"expected_spec_revision":0,"fields":{"research_subject":"Synthetic subject","research_goal":"Synthetic goal"}}`, "g4-save-spec-01", http.StatusOK), &projectAfterSpec)
	var activationRead struct {
		ID string `json:"id"`
	}
	readBody := fmt.Sprintf(`{"expected_project_version":%d}`, projectAfterSpec.ProjectVersion)
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/activation-read", readBody, "", http.StatusCreated), &activationRead)
	if activationRead.ID == "" {
		t.Fatal("production activation read did not return a receipt")
	}
	var active workspacecore.Project
	activateBody := fmt.Sprintf(`{"expected_spec_revision":1,"expected_project_version":%d,"reviewed_project_version":%d,"activation_read_id":%q}`, projectAfterSpec.ProjectVersion, projectAfterSpec.ProjectVersion, activationRead.ID)
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/activate", activateBody, "g4-activate-01", http.StatusOK), &active)
	var chapters []workspacecore.Chapter
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID+"/chapters", "", "", http.StatusOK), &chapters)
	if len(chapters) != 2 {
		t.Fatalf("active demo project chapters = %d, want 2", len(chapters))
	}
	for _, chapter := range chapters {
		body := "Synthetic research question with enough context."
		if chapter.SectionID == "method" {
			body = "Short method text."
		}
		bodyJSON, err := json.Marshal(map[string]any{"expected_chapter_version_id": nil, "expected_spec_revision": 1, "body_markdown": body, "source_ids": []string{}})
		if err != nil {
			t.Fatal(err)
		}
		var version workspacecore.Chapter
		decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/versions", string(bodyJSON), "g4-save-"+chapter.SectionID, http.StatusCreated), &version)
		if version.CurrentVersionID == nil || strings.TrimSpace(*version.CurrentVersionID) == "" {
			t.Fatalf("saved chapter has no current version: %+v", version)
		}
		confirmJSON, err := json.Marshal(map[string]any{"expected_chapter_version_id": *version.CurrentVersionID, "expected_spec_revision": 1, "review_decisions": []any{}})
		if err != nil {
			t.Fatal(err)
		}
		request(first.router, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/confirmations", string(confirmJSON), "g4-confirm-"+chapter.SectionID, http.StatusCreated)
	}
	var current workspacecore.Project
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &current)
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, g4E2EActorID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, g4E2ETenantID)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: g4E2ETenantID, UserID: g4E2EActorID, Role: types.TenantRoleAdmin})
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: g4E2EActorID})
	workspaceInput, err := first.reader.ReadDeliveryInput(ctx, project.ID)
	if err != nil {
		t.Fatalf("read workspace input before Template Check: %v", err)
	}
	builtInput, err := first.builder.Build(ctx, g4E2EActorID, workspaceInput)
	if err != nil {
		t.Fatalf("build delivery input before Template Check: %v", err)
	}
	ref := lingdoctemplate.RuleSetReferenceFor(builtInput.Template)
	if ref == nil || ref.ID != published.ID || ref.Version != published.Version || ref.Hash != published.RulesetHash ||
		strings.TrimSpace(builtInput.Template.RulesetHash) == "" || builtInput.TemplateCopyID != bound.TemplateCopy.ID || builtInput.TemplateCopyVersion != bound.TemplateCopyVersion {
		t.Fatalf("delivery builder lost RuleSet/template-copy audit identity: ref=%+v effective_hash=%q template_copy=%s@%d want=%s@%d",
			ref, builtInput.Template.RulesetHash, builtInput.TemplateCopyID, builtInput.TemplateCopyVersion, bound.TemplateCopy.ID, bound.TemplateCopyVersion)
	}
	directTemplateCheck, err := first.releases.TemplateCheck(ctx, g4E2EActorID, project.ID, current.ProjectVersion)
	if err != nil {
		t.Fatalf("direct Template Check failed before HTTP: %v (delivery_invalid_request=%v candidate_invalid_request=%v)",
			err, errors.Is(err, delivery.ErrInvalidRequest), errors.Is(err, candidateadoption.ErrInvalidRequest))
	}
	if directTemplateCheck.TemplateCopyID != bound.TemplateCopy.ID || directTemplateCheck.TemplateCopyVersion != bound.TemplateCopyVersion {
		t.Fatalf("direct Template Check lost template-copy identity: result=%+v want=%s@%d", directTemplateCheck, bound.TemplateCopy.ID, bound.TemplateCopyVersion)
	}
	versionBody := fmt.Sprintf(`{"expected_project_version":%d}`, current.ProjectVersion)
	var templateCheck delivery.TemplateCheckResult
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/template-checks", versionBody, "", http.StatusOK), &templateCheck)
	if templateCheck.RuleSetRef == nil || *templateCheck.RuleSetRef != *wantRef || templateCheck.TemplateCopyID != bound.TemplateCopy.ID || templateCheck.TemplateCopyVersion != bound.TemplateCopyVersion {
		t.Fatalf("Template Check did not expose exact RuleSet reference: %+v", templateCheck)
	}
	var check delivery.CheckResult
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/checks", versionBody, "", http.StatusOK), &check)
	if check.RuleSetRef == nil || *check.RuleSetRef != *wantRef {
		t.Fatalf("Preflight did not expose exact RuleSet reference: %+v", check)
	}
	foundIssue := false
	for _, issue := range check.Issues {
		if issue.RuleID == "method-length" {
			foundIssue = true
			if issue.RuleSetRef == nil || *issue.RuleSetRef != *wantRef {
				t.Fatalf("ValidationIssue did not retain exact RuleSet reference: %+v", issue)
			}
		}
	}
	if !foundIssue || check.Status != delivery.CheckPassed {
		t.Fatalf("expected a non-blocking method-length ValidationIssue and passing preflight, got status=%s issues=%+v", check.Status, check.Issues)
	}
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_ruleset_use_audits", "release_snapshot", "g4_fail_freeze_audit", true)
	request(first.router, http.MethodPost, "/projects/"+project.ID+"/releases", versionBody, "g4-freeze-001", http.StatusInternalServerError)
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_ruleset_use_audits", "release_snapshot", "g4_fail_freeze_audit", false)
	var snapshotCount int64
	if err := db.Table("lingdoc_release_snapshots").Where("project_id = ?", project.ID).Count(&snapshotCount).Error; err != nil || snapshotCount != 0 {
		t.Fatalf("failed freeze audit left persisted snapshot count=%d err=%v", snapshotCount, err)
	}
	var snapshot delivery.ReleaseSnapshot
	freezeResponse := request(first.router, http.MethodPost, "/projects/"+project.ID+"/releases", versionBody, "g4-freeze-001", http.StatusCreated)
	decode(freezeResponse, &snapshot)
	if snapshot.RuleSetRef == nil || *snapshot.RuleSetRef != *wantRef || snapshot.FrozenInput.Template.RuleSetRef == nil || *snapshot.FrozenInput.Template.RuleSetRef != *wantRef ||
		snapshot.FrozenInput.TemplateCopyID != bound.TemplateCopy.ID || snapshot.FrozenInput.TemplateCopyVersion != bound.TemplateCopyVersion {
		t.Fatalf("release snapshot lost exact RuleSet reference: %+v", snapshot)
	}
	var snapshotReplay delivery.ReleaseSnapshot
	if replay := decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/releases", versionBody, "g4-freeze-001", http.StatusCreated), &snapshotReplay); !replay.Meta.Replayed || snapshotReplay.ID != snapshot.ID {
		t.Fatalf("freeze retry not idempotent: replay=%+v first=%s again=%s", replay.Meta, snapshot.ID, snapshotReplay.ID)
	}
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_ruleset_use_audits", "export", "g4_fail_export_audit", true)
	exportPath := "/projects/" + project.ID + "/releases/" + snapshot.ID + "/exports"
	request(first.router, http.MethodPost, exportPath, `{"format":"docx"}`, "g4-export-001", http.StatusInternalServerError)
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_ruleset_use_audits", "export", "g4_fail_export_audit", false)
	var exportCount int64
	if err := db.Table("lingdoc_export_artifacts").Where("project_id = ?", project.ID).Count(&exportCount).Error; err != nil || exportCount != 0 {
		t.Fatalf("failed export audit left artifact count=%d err=%v", exportCount, err)
	}
	var artifact struct {
		ID         string                            `json:"id"`
		Status     string                            `json:"status"`
		RuleSetRef *lingdoctemplate.RuleSetReference `json:"ruleset_ref"`
		Download   *string                           `json:"download_path"`
	}
	decode(request(first.router, http.MethodPost, exportPath, `{"format":"docx"}`, "g4-export-001", http.StatusAccepted), &artifact)
	if artifact.Status != string(delivery.ExportVerified) || artifact.RuleSetRef == nil || *artifact.RuleSetRef != *wantRef || artifact.Download == nil {
		t.Fatalf("verified artifact did not preserve RuleSet reference/download: %+v", artifact)
	}
	var artifactReplay struct {
		ID string `json:"id"`
	}
	if replay := decode(request(first.router, http.MethodPost, exportPath, `{"format":"docx"}`, "g4-export-001", http.StatusAccepted), &artifactReplay); !replay.Meta.Replayed || artifactReplay.ID != artifact.ID {
		t.Fatalf("export retry not idempotent: replay=%+v first=%s again=%s", replay.Meta, artifact.ID, artifactReplay.ID)
	}

	closeG4E2EDatabase(t, db)
	db = reopen()
	second := buildG4E2EHTTP(t, db)
	var readBackCopy workspacecore.ProjectTemplateCopy
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/template-copies/3", "", "", http.StatusOK), &readBackCopy)
	if readBackCopy.TemplateVersionRuleSetRef == nil || *readBackCopy.TemplateVersionRuleSetRef != *wantRef || readBackCopy.Definition.RuleSetRef == nil || *readBackCopy.Definition.RuleSetRef != *wantRef {
		t.Fatalf("TemplateVersion RuleSet identity did not survive %s database restart: %+v", dialect, readBackCopy)
	}
	var releaseReadBack delivery.ReleaseSnapshot
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/releases/"+snapshot.ID, "", "", http.StatusOK), &releaseReadBack)
	if releaseReadBack.RuleSetRef == nil || *releaseReadBack.RuleSetRef != *wantRef || releaseReadBack.SnapshotDigest != snapshot.SnapshotDigest {
		t.Fatalf("release snapshot readback changed after %s restart: %+v", dialect, releaseReadBack)
	}
	var exportReadBack struct {
		ID         string                            `json:"id"`
		Status     string                            `json:"status"`
		RuleSetRef *lingdoctemplate.RuleSetReference `json:"ruleset_ref"`
		Download   *string                           `json:"download_path"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/exports/"+artifact.ID, "", "", http.StatusOK), &exportReadBack)
	if exportReadBack.ID != artifact.ID || exportReadBack.RuleSetRef == nil || *exportReadBack.RuleSetRef != *wantRef || exportReadBack.Status != string(delivery.ExportVerified) || exportReadBack.Download == nil {
		t.Fatalf("export readback lost exact RuleSet identity after %s restart: %+v", dialect, exportReadBack)
	}
	download := request(second.router, http.MethodGet, *exportReadBack.Download, "", "", http.StatusOK)
	if len(download.Body.Bytes()) == 0 || !strings.Contains(download.Header().Get("Content-Type"), "wordprocessingml.document") {
		t.Fatalf("persisted %s DOCX download empty or wrong content type: bytes=%d type=%q", dialect, download.Body.Len(), download.Header().Get("Content-Type"))
	}
	var auditEnvelope struct {
		Items []lingdoctemplate.RuleSetUseAuditEvent `json:"items"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/rule-set-use-audits", "", "", http.StatusOK), &auditEnvelope)
	audits := auditEnvelope.Items
	seen := map[string]lingdoctemplate.RuleSetUseAuditEvent{}
	for _, event := range audits {
		if event.RuleSetID != wantRef.ID || event.RuleSetVersion != wantRef.Version || event.RuleSetHash != wantRef.Hash || event.EffectiveRulesetHash == "" || event.TemplateCopyID == "" {
			t.Fatalf("incomplete exact RuleSet-use audit: %+v", event)
		}
		seen[event.Action] = event
	}
	for _, action := range []string{"check", "preflight", "release_snapshot", "export"} {
		if _, ok := seen[action]; !ok {
			t.Errorf("%s RuleSet-use audit missing after %s restart; seen=%v", action, dialect, mapKeys(seen))
		}
	}
	var lifecycleAudits []struct {
		Action  string `gorm:"column:action"`
		Details []byte `gorm:"column:details"`
	}
	if err := db.Table("audit_logs").Select("action, details").Where("tenant_id = ? AND scope_id = ? AND action IN ?", g4E2ETenantID, g4E2ERuleSet, []string{"lingdoc.ruleset.create_draft", "lingdoc.ruleset.publish"}).Find(&lifecycleAudits).Error; err != nil {
		t.Fatalf("read %s RuleSet lifecycle audits: %v", dialect, err)
	}
	if len(lifecycleAudits) != 2 {
		t.Fatalf("%s RuleSet lifecycle audit rows=%d, want create + publish", dialect, len(lifecycleAudits))
	}
	for _, event := range lifecycleAudits {
		var details map[string]any
		if err := json.Unmarshal(event.Details, &details); err != nil {
			t.Fatalf("decode %s RuleSet lifecycle audit: %v", dialect, err)
		}
		if details["ruleset_id"] != published.ID || details["version"] != published.Version {
			t.Fatalf("%s lifecycle audit lost exact RuleSet identity: action=%s details=%v", dialect, event.Action, details)
		}
		if event.Action == "lingdoc.ruleset.publish" && details["ruleset_hash"] != published.RulesetHash {
			t.Fatalf("%s publish audit lost server-computed RuleSet hash: details=%v want=%s", dialect, details, published.RulesetHash)
		}
		if event.Action == "lingdoc.ruleset.create_draft" && details["ruleset_hash"] != "" {
			t.Fatalf("%s draft creation audit must not claim a published RuleSet hash: details=%v", dialect, details)
		}
	}
	var concurrentAuditCount int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_id = ? AND action = ?", g4E2ETenantID, concurrentRuleSetID, "lingdoc.ruleset.publish").Count(&concurrentAuditCount).Error; err != nil || concurrentAuditCount != 1 {
		t.Fatalf("%s concurrent publish audit rows=%d err=%v, want 1 after restart", dialect, concurrentAuditCount, err)
	}
	type projectAuditRow struct {
		Action      string `gorm:"column:action"`
		DetailsJSON string `gorm:"column:details_json"`
	}
	var projectAudits []projectAuditRow
	if err := db.Table("lingdoc_project_audits").Select("action, details_json").Where("project_id = ? AND action IN ?", project.ID, []string{"project_template.bind_ruleset", "project_template.override_ruleset"}).Find(&projectAudits).Error; err != nil {
		t.Fatalf("read %s project binding/override audits: %v", dialect, err)
	}
	if len(projectAudits) != 2 {
		t.Fatalf("%s project bind/override audit rows=%d, want 2", dialect, len(projectAudits))
	}
	projectAuditActions := map[string]bool{}
	for _, event := range projectAudits {
		var details map[string]any
		if err := json.Unmarshal([]byte(event.DetailsJSON), &details); err != nil {
			t.Fatalf("decode %s project audit: %v", dialect, err)
		}
		if details["ruleset_id"] != published.ID || details["ruleset_version"] != published.Version || details["ruleset_hash"] != published.RulesetHash {
			t.Fatalf("%s project audit lost exact RuleSet identity/hash: action=%s details=%v", dialect, event.Action, details)
		}
		projectAuditActions[event.Action] = true
	}
	for _, action := range []string{"project_template.bind_ruleset", "project_template.override_ruleset"} {
		if !projectAuditActions[action] {
			t.Errorf("%s project audit missing action %s", dialect, action)
		}
	}

	// Active projects migrate by assessing and applying a G3 ChangeSet that pins
	// the exact next immutable RuleSet version. Existing release/export history
	// must continue to point at v1; a fresh evaluation must point at v2.
	v2Body := strings.Replace(draftBody, `"version":"`+g4E2EVersion+`"`, `"version":"v2"`, 1)
	v2Body = strings.Replace(v2Body, `"maximum":500`, `"maximum":400`, 1)
	request(second.router, http.MethodPost, "/rule-sets", v2Body, "", http.StatusCreated)
	var publishedV2 lingdoctemplate.RuleSet
	decode(request(second.router, http.MethodPost, "/rule-sets/"+g4E2ERuleSet+"/versions/v2/publish", `{}`, "", http.StatusOK), &publishedV2)
	var currentProject workspacecore.Project
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &currentProject)
	currentDefinition := currentProject.TemplateCopy.Definition
	migrationInput := workspacecore.CreateChangeSetInput{
		ExpectedContextRevision: currentProject.CurrentContextRevision,
		Reason:                  "迁移到新的不可变 RuleSet 版本",
		TemplateUpgrade: &workspacecore.TemplateUpgradeInput{
			ExpectedProjectVersion: currentProject.ProjectVersion, ExpectedTemplateCopyVersion: currentProject.TemplateCopyVersion,
			RuleSetRef: &lingdoctemplate.RuleSetReference{ID: publishedV2.ID, Version: publishedV2.Version, Hash: publishedV2.RulesetHash},
			Fields:     append([]lingdoctemplate.Field{}, currentDefinition.Fields...), Sections: append([]lingdoctemplate.Section{}, currentDefinition.Sections...),
			Terms: append([]lingdoctemplate.Term{}, currentDefinition.Terms...), RequiredFields: append([]string{}, currentDefinition.RequiredFields...),
			Rules: append([]lingdoctemplate.Rule{}, currentDefinition.Rules...),
		},
	}
	migrationJSON, err := json.Marshal(migrationInput)
	if err != nil {
		t.Fatal(err)
	}
	var assessed workspacecore.ChangeSet
	decode(request(second.router, http.MethodPost, "/projects/"+project.ID+"/change-sets", string(migrationJSON), "g4-migrate-assess-v2", http.StatusCreated), &assessed)
	assessedRef := lingdoctemplate.RuleSetReferenceFor(assessed.TemplateUpgrade.Definition)
	if assessed.Status != "assessed" || assessedRef == nil || *assessedRef != (lingdoctemplate.RuleSetReference{ID: publishedV2.ID, Version: publishedV2.Version, Hash: publishedV2.RulesetHash}) {
		t.Fatalf("active migration did not assess an exact v2 RuleSet snapshot: %+v ref=%+v", assessed, assessedRef)
	}
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_project_audits", "project.change_set.apply", "g4_fail_change_set_apply_audit", true)
	request(second.router, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+assessed.ID+"/apply", `{}`, "g4-migrate-apply-v2", http.StatusInternalServerError)
	setG4AuditFailureTrigger(t, db, dialect, "lingdoc_project_audits", "project.change_set.apply", "g4_fail_change_set_apply_audit", false)
	var rolledBackMigration workspacecore.Project
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &rolledBackMigration)
	if rolledBackMigration.ProjectVersion != currentProject.ProjectVersion || rolledBackMigration.TemplateCopyVersion != currentProject.TemplateCopyVersion {
		t.Fatalf("failed ChangeSet apply audit partially migrated the project: before=%d/%d after=%d/%d", currentProject.ProjectVersion, currentProject.TemplateCopyVersion, rolledBackMigration.ProjectVersion, rolledBackMigration.TemplateCopyVersion)
	}
	var assessedAfterAuditFailure workspacecore.ChangeSet
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/change-sets/"+assessed.ID, "", "", http.StatusOK), &assessedAfterAuditFailure)
	if assessedAfterAuditFailure.Status != "assessed" {
		t.Fatalf("failed ChangeSet apply audit changed the proposal state: %+v", assessedAfterAuditFailure)
	}
	var failedApplyAuditCount int64
	if err := db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ? AND target = ?", project.ID, "project.change_set.apply", assessed.ID).Count(&failedApplyAuditCount).Error; err != nil || failedApplyAuditCount != 0 {
		t.Fatalf("failed ChangeSet apply audit persisted an event: count=%d err=%v", failedApplyAuditCount, err)
	}
	var applied workspacecore.ChangeSet
	decode(request(second.router, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+assessed.ID+"/apply", `{}`, "g4-migrate-apply-v2", http.StatusOK), &applied)
	if applied.Status != "applied" {
		t.Fatalf("active RuleSet migration was not applied: %+v", applied)
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &currentProject)
	newRef := lingdoctemplate.RuleSetReferenceFor(currentProject.TemplateCopy.Definition)
	if newRef == nil || *newRef != *assessedRef || currentProject.TemplateCopyVersion != 4 {
		t.Fatalf("applied project did not advance to RuleSet v2 copy: project=%+v ref=%+v", currentProject.TemplateCopy, newRef)
	}
	var newPreflight delivery.CheckResult
	newVersionBody := fmt.Sprintf(`{"expected_project_version":%d}`, currentProject.ProjectVersion)
	decode(request(second.router, http.MethodPost, "/projects/"+project.ID+"/checks", newVersionBody, "", http.StatusOK), &newPreflight)
	if newPreflight.RuleSetRef == nil || *newPreflight.RuleSetRef != *assessedRef {
		t.Fatalf("new evaluation did not use migrated RuleSet v2: %+v", newPreflight)
	}
	var oldSnapshotAfterMigration delivery.ReleaseSnapshot
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/releases/"+snapshot.ID, "", "", http.StatusOK), &oldSnapshotAfterMigration)
	if oldSnapshotAfterMigration.SnapshotDigest != snapshot.SnapshotDigest || oldSnapshotAfterMigration.RuleSetRef == nil || *oldSnapshotAfterMigration.RuleSetRef != *wantRef {
		t.Fatalf("migrating active project mutated its v1 release history: %+v", oldSnapshotAfterMigration)
	}
	if oldSnapshotAfterMigration.IsCurrent {
		t.Fatalf("v1 release was marked current after active project migrated to v2: %+v", oldSnapshotAfterMigration)
	}
	var oldArtifactAfterMigration struct {
		RuleSetRef *lingdoctemplate.RuleSetReference `json:"ruleset_ref"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/exports/"+artifact.ID, "", "", http.StatusOK), &oldArtifactAfterMigration)
	if oldArtifactAfterMigration.RuleSetRef == nil || *oldArtifactAfterMigration.RuleSetRef != *wantRef {
		t.Fatalf("migrating active project changed its v1 export record: %+v", oldArtifactAfterMigration)
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/template-copies/3", "", "", http.StatusOK), &readBackCopy)
	if readBackCopy.Status != workspacecore.TemplateCopySuperseded || readBackCopy.TemplateVersionRuleSetRef == nil || *readBackCopy.TemplateVersionRuleSetRef != *wantRef {
		t.Fatalf("historical project copy no longer retains exact v1 identity: %+v", readBackCopy)
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/rule-set-use-audits", "", "", http.StatusOK), &auditEnvelope)
	foundV2Preflight := false
	for _, event := range auditEnvelope.Items {
		if event.RuleSetVersion == "v2" && event.RuleSetID == publishedV2.ID && event.RuleSetHash == publishedV2.RulesetHash && event.Action == "preflight" {
			foundV2Preflight = true
		}
	}
	if !foundV2Preflight {
		t.Fatalf("tenant-authorized HTTP audit history did not expose the v2 preflight: %+v", auditEnvelope.Items)
	}

	// Repeat the upgrade with an allowlisted project override. The project audit
	// endpoint must independently expose exact source identity, override and
	// effective hash, migration reason, and both copy versions after persistence.
	v3Body := strings.Replace(draftBody, `"version":"`+g4E2EVersion+`"`, `"version":"v3"`, 1)
	v3Body = strings.Replace(v3Body, `"maximum":500`, `"maximum":350`, 1)
	request(second.router, http.MethodPost, "/rule-sets", v3Body, "", http.StatusCreated)
	var publishedV3 lingdoctemplate.RuleSet
	decode(request(second.router, http.MethodPost, "/rule-sets/"+g4E2ERuleSet+"/versions/v3/publish", `{}`, "", http.StatusOK), &publishedV3)
	var beforeV3 workspacecore.Project
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &beforeV3)
	definitionV3 := beforeV3.TemplateCopy.Definition
	minimumOverride := 8.0 // JSON-decoded numeric parameters use float64.
	migrationV3 := workspacecore.CreateChangeSetInput{
		ExpectedContextRevision: beforeV3.CurrentContextRevision,
		Reason:                  "按项目方法章节要求迁移至规则集 v3 并收紧最小字数",
		TemplateUpgrade: &workspacecore.TemplateUpgradeInput{
			ExpectedProjectVersion: beforeV3.ProjectVersion, ExpectedTemplateCopyVersion: beforeV3.TemplateCopyVersion,
			RuleSetRef:           &lingdoctemplate.RuleSetReference{ID: publishedV3.ID, Version: publishedV3.Version, Hash: publishedV3.RulesetHash},
			RuleBindingOverrides: []lingdoctemplate.RuleBindingOverride{{RuleID: "method-length", Parameters: map[string]any{"minimum": minimumOverride}}},
			Fields:               append([]lingdoctemplate.Field{}, definitionV3.Fields...), Sections: append([]lingdoctemplate.Section{}, definitionV3.Sections...),
			Terms: append([]lingdoctemplate.Term{}, definitionV3.Terms...), RequiredFields: append([]string{}, definitionV3.RequiredFields...),
			Rules: append([]lingdoctemplate.Rule{}, definitionV3.Rules...),
		},
	}
	migrationV3JSON, err := json.Marshal(migrationV3)
	if err != nil {
		t.Fatal(err)
	}
	var assessedV3 workspacecore.ChangeSet
	decode(request(second.router, http.MethodPost, "/projects/"+project.ID+"/change-sets", string(migrationV3JSON), "g4-migrate-assess-v3", http.StatusCreated), &assessedV3)
	var appliedV3 workspacecore.ChangeSet
	decode(request(second.router, http.MethodPost, "/projects/"+project.ID+"/change-sets/"+assessedV3.ID+"/apply", `{}`, "g4-migrate-apply-v3", http.StatusOK), &appliedV3)
	if appliedV3.Status != "applied" {
		t.Fatalf("active RuleSet v3 override migration was not applied: %+v", appliedV3)
	}
	var projectAuditEvents []workspacecore.AuditEvent
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/audit", "", "", http.StatusOK), &projectAuditEvents)
	applyAudits := map[string]workspacecore.AuditEvent{}
	for _, event := range projectAuditEvents {
		if event.Action == "project.change_set.apply" {
			applyAudits[event.Target] = event
		}
	}
	assertUpgradeAudit := func(change workspacecore.ChangeSet, wantRef lingdoctemplate.RuleSetReference, oldCopyVersion, newCopyVersion int64, wantOverride bool) {
		t.Helper()
		event, ok := applyAudits[change.ID]
		if !ok {
			t.Fatalf("GET project audit did not return ChangeSet apply event %s: %+v", change.ID, projectAuditEvents)
		}
		upgrade, ok := event.Details["template_upgrade"].(map[string]any)
		if !ok {
			t.Fatalf("ChangeSet apply event omitted template upgrade details: %+v", event)
		}
		if upgrade["ruleset_id"] != wantRef.ID || upgrade["ruleset_version"] != wantRef.Version || upgrade["ruleset_source_hash"] != wantRef.Hash {
			t.Fatalf("migration audit lost exact RuleSet source identity: %+v want=%+v", upgrade, wantRef)
		}
		if upgrade["old_copy_version"] != float64(oldCopyVersion) || upgrade["new_copy_version"] != float64(newCopyVersion) ||
			upgrade["reason"] != change.Reason || event.Details["reason"] != change.Reason || event.Reason != change.Reason {
			t.Fatalf("migration audit lost reason/copy transition: event=%+v upgrade=%+v want=%d->%d reason=%q", event, upgrade, oldCopyVersion, newCopyVersion, change.Reason)
		}
		if upgrade["effective_ruleset_hash"] == "" || upgrade["ruleset_hash"] != upgrade["effective_ruleset_hash"] {
			t.Fatalf("migration audit lost effective RuleSet hash: %+v", upgrade)
		}
		if wantOverride {
			if upgrade["override_count"] != float64(1) {
				t.Fatalf("override migration audit count=%v, want one: %+v", upgrade["override_count"], upgrade)
			}
			overrides, ok := upgrade["overrides"].([]any)
			if !ok || len(overrides) != 1 {
				t.Fatalf("override migration audit omitted the effective override: %+v", upgrade)
			}
			override, ok := overrides[0].(map[string]any)
			parameters, parametersOK := override["parameters"].(map[string]any)
			if !ok || !parametersOK || override["rule_id"] != "method-length" || parameters["minimum"] != minimumOverride {
				t.Fatalf("override migration audit mismatch: %+v", overrides)
			}
		} else if upgrade["override_count"] != float64(0) {
			t.Fatalf("ordinary migration unexpectedly has overrides: %+v", upgrade)
		}
	}
	assertUpgradeAudit(assessed, lingdoctemplate.RuleSetReference{ID: publishedV2.ID, Version: publishedV2.Version, Hash: publishedV2.RulesetHash}, 3, 4, false)
	assertUpgradeAudit(assessedV3, lingdoctemplate.RuleSetReference{ID: publishedV3.ID, Version: publishedV3.Version, Hash: publishedV3.RulesetHash}, 4, 5, true)
}

func setG4AuditFailureTrigger(t *testing.T, db *gorm.DB, dialect, table, action, trigger string, enabled bool) {
	t.Helper()
	if dialect == "postgres" {
		function := trigger + "_fn"
		if enabled {
			if err := db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = '%s' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END; $$`, function, action)).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, table, function)).Error; err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := db.Exec(fmt.Sprintf(`DROP TRIGGER %s ON %s`, trigger, table)).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(fmt.Sprintf(`DROP FUNCTION %s()`, function)).Error; err != nil {
			t.Fatal(err)
		}
		return
	}
	if enabled {
		statement := fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON %s WHEN NEW.action = '%s' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`, trigger, table, action)
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := db.Exec(fmt.Sprintf("DROP TRIGGER %s", trigger)).Error; err != nil {
		t.Fatal(err)
	}
}

type g4E2EHTTP struct {
	router       *gin.Engine
	ruleSetStore *rulesetstore.Store
	reader       *candidateadoption.SQLiteCandidateAdoptionStore
	builder      workspace.DeliveryInputAssembler
	releases     *workspace.DeliveryReleaseService
}

func buildG4E2EHTTP(t *testing.T, db *gorm.DB) g4E2EHTTP {
	t.Helper()
	parts, err := NewLingDocWorkspaceHandler(db, nil, nil, nil, nil, nil, nil, NewLingDocTemplateCatalog())
	if err != nil {
		t.Fatalf("build production LingDoc workspace composition: %v", err)
	}
	ruleSetStore := rulesetstore.NewStore(db, rulesetstore.TenantMembershipAuthorizer{})
	adoptionStore := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	projectAuthorizer := g4E2EWorkspaceAuthorizer{candidateAdoptionWorkspaceAuthorizer{service: parts.Projects}}
	inputs := &candidateadoption.DeliveryInputService{Reader: adoptionStore, Authorizer: projectAuthorizer}
	policy := workspace.NewAccessPolicy(parts.Projects, parts.Runtime, adoptionStore)
	snapshots := delivery.NewSQLiteSnapshotStore(db)
	releases := workspace.NewDeliveryReleaseServiceWithPolicy(inputs, parts.Integration.DeliveryInputBuilder(), snapshots, parts.Integration.CandidateAdoptionSourcePolicy(), policy, parts.Projects.(workspace.ValidationIssueDispositionApplication)).WithRuleSetUseAuditor(ruleSetStore)
	exports := workspace.NewDeliveryExportServiceWithPolicy(snapshots, delivery.NewSQLiteExportStore(db), workspace.DeliveryDocument{}, workspace.DeliveryDocument{}, inputs, parts.Integration.WorkspaceSourcePolicy(), policy).
		WithRuleSetUseAuditor(ruleSetStore)
	confirmSource, ok := parts.Integration.CandidateAdoptionSourcePolicy().(candidateadoption.ConfirmationSourcePolicy)
	if !ok {
		t.Fatal("production source runtime does not implement confirmation validation")
	}
	confirmations := candidateadoption.NewConfirmationService(adoptionStore, confirmSource, projectAuthorizer)
	confirmHandler := candidateadoption.NewCandidateAdoptionHandler(candidateadoption.NewCandidateAdoptionService(adoptionStore, parts.Integration.CandidateAdoptionSourcePolicy(), projectAuthorizer), confirmations, func(c *gin.Context) (string, bool) {
		return types.UserIDFromContext(c.Request.Context())
	})
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.UserIDContextKey, g4E2EActorID)
		ctx = context.WithValue(ctx, types.TenantIDContextKey, g4E2ETenantID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
		ctx = types.WithCaller(ctx, types.Caller{TenantID: g4E2ETenantID, UserID: g4E2EActorID, Role: types.TenantRoleAdmin})
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: g4E2EActorID})
		c.Request = c.Request.WithContext(ctx)
		c.Set("request_id", "g4-e2e")
		c.Next()
	})
	group := engine.Group("/api/v1/lingdoc")
	parts.Handler.Register(workspace.RouteGroups{Read: group, Write: group})
	parts.Handler.RegisterFullRoutes(group)
	parts.RuleSets.Register(group, group)
	workspace.RegisterDeliveryRoutes(group, workspace.NewDeliveryHandler(workspace.GuardFullWorkflowRelease(releases, parts.Guard)))
	workspace.RegisterDeliveryExportRoutes(group, workspace.NewDeliveryExportHandler(workspace.GuardFullWorkflowExport(exports, parts.Guard)))
	candidateadoption.RegisterRoutes(group, confirmHandler)
	return g4E2EHTTP{router: engine, ruleSetStore: ruleSetStore, reader: adoptionStore, builder: parts.Integration.DeliveryInputBuilder(), releases: releases}
}

// Share the production transaction-aware authorization port with adoption and
// confirmation; a separate pool read would re-enter the one-connection fixture.
type g4E2EWorkspaceAuthorizer struct {
	candidateAdoptionWorkspaceAuthorizer
}

func (a g4E2EWorkspaceAuthorizer) AuthorizeProject(ctx context.Context, actorID, projectID string) error {
	return a.Authorize(ctx, actorID, projectID, "read")
}

func closeG4E2EDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil && err != sql.ErrConnDone {
		t.Fatal(err)
	}
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
