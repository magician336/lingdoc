package container

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestRuleSetProductionLegacyTemplatePathPersistsAcrossRestartSQLite keeps the
// compatibility path in the same production HTTP/application seam as the
// canonical RuleSet path. The project intentionally has no independent
// RuleSet binding; its historical template rules must still check, freeze,
// export and read back without being relabelled as a canonical binding.
func TestRuleSetProductionLegacyTemplatePathPersistsAcrossRestartSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "g4-legacy-e2e.sqlite")
	db := openG4E2ESQLite(t, dbPath)
	applyG4E2ESQLiteSchema(t, db)
	runG4LegacyTemplateE2E(t, db, func() *gorm.DB { return openG4E2ESQLite(t, dbPath) })
}

func TestRuleSetProductionLegacyTemplatePathPersistsAcrossRestartPostgres(t *testing.T) {
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
	schema := fmt.Sprintf("lingdoc_g4_legacy_%d_%d", os.Getpid(), time.Now().UnixNano())
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
	runG4LegacyTemplateE2E(t, db, open)
}

func runG4LegacyTemplateE2E(t *testing.T, db *gorm.DB, reopen func() *gorm.DB) {
	t.Helper()
	first := buildG4E2EHTTP(t, db)
	request := func(router *gin.Engine, method, path, body, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "g4-legacy-e2e")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, want, res.Body.String())
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

	var project struct {
		ID             string `json:"id"`
		ProjectVersion int64  `json:"project_version"`
		TemplateCopy   struct {
			Definition struct {
				RuleSetRef *struct {
					ID      string `json:"ruleset_id"`
					Version string `json:"version"`
					Hash    string `json:"hash"`
				} `json:"ruleset_ref"`
			} `json:"definition"`
		} `json:"template_copy"`
	}
	decode(request(first.router, http.MethodPost, "/projects", `{"name":"legacy G4 E2E","template_id":"template-demo"}`, "g4-legacy-create", http.StatusCreated), &project)
	if project.ID == "" || project.TemplateCopy.Definition.RuleSetRef != nil {
		t.Fatalf("legacy project unexpectedly has an independent RuleSet binding: %+v", project)
	}

	var afterSpec struct {
		ProjectVersion int64 `json:"project_version"`
	}
	decode(request(first.router, http.MethodPut, "/projects/"+project.ID+"/spec", `{"expected_spec_revision":0,"fields":{"research_subject":"Legacy subject","research_goal":"Legacy goal"}}`, "g4-legacy-spec", http.StatusOK), &afterSpec)
	var activationRead struct {
		ID string `json:"id"`
	}
	readBody := fmt.Sprintf(`{"expected_project_version":%d}`, afterSpec.ProjectVersion)
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/activation-read", readBody, "", http.StatusCreated), &activationRead)
	if activationRead.ID == "" {
		t.Fatal("legacy activation read did not return a receipt")
	}
	activateBody := fmt.Sprintf(`{"expected_spec_revision":1,"expected_project_version":%d,"reviewed_project_version":%d,"activation_read_id":%q}`, afterSpec.ProjectVersion, afterSpec.ProjectVersion, activationRead.ID)
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/activate", activateBody, "g4-legacy-activate", http.StatusOK), &project)

	var chapters []struct {
		ID        string `json:"id"`
		SectionID string `json:"section_id"`
	}
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID+"/chapters", "", "", http.StatusOK), &chapters)
	if len(chapters) != 2 {
		t.Fatalf("legacy project chapters = %d, want 2", len(chapters))
	}
	for _, chapter := range chapters {
		body := "Legacy research question with enough context."
		if chapter.SectionID == "method" {
			body = "Legacy method text."
		}
		versionBody := fmt.Sprintf(`{"expected_chapter_version_id":null,"expected_spec_revision":1,"body_markdown":%q,"source_ids":[]}`, body)
		var version struct {
			CurrentVersionID *string `json:"current_version_id"`
		}
		decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/versions", versionBody, "g4-legacy-save-"+chapter.SectionID, http.StatusCreated), &version)
		if version.CurrentVersionID == nil || strings.TrimSpace(*version.CurrentVersionID) == "" {
			t.Fatalf("legacy chapter %s did not persist a current version", chapter.SectionID)
		}
		confirmBody := fmt.Sprintf(`{"expected_chapter_version_id":%q,"expected_spec_revision":1,"review_decisions":[]}`, *version.CurrentVersionID)
		request(first.router, http.MethodPost, "/projects/"+project.ID+"/chapters/"+chapter.ID+"/confirmations", confirmBody, "g4-legacy-confirm-"+chapter.SectionID, http.StatusCreated)
	}

	var current struct {
		ProjectVersion int64 `json:"project_version"`
	}
	decode(request(first.router, http.MethodGet, "/projects/"+project.ID, "", "", http.StatusOK), &current)
	versionBody := fmt.Sprintf(`{"expected_project_version":%d}`, current.ProjectVersion)
	var check struct {
		Status     string `json:"status"`
		RuleSetRef *struct {
			ID string `json:"ruleset_id"`
		} `json:"ruleset_ref"`
	}
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/checks", versionBody, "", http.StatusOK), &check)
	if check.Status != "passed" || check.RuleSetRef != nil {
		t.Fatalf("legacy check changed semantics: %+v", check)
	}

	var snapshot struct {
		ID         string `json:"id"`
		RuleSetRef *struct {
			ID string `json:"ruleset_id"`
		} `json:"ruleset_ref"`
	}
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/releases", versionBody, "g4-legacy-freeze", http.StatusCreated), &snapshot)
	if snapshot.ID == "" || snapshot.RuleSetRef != nil {
		t.Fatalf("legacy snapshot invented an independent RuleSet reference: %+v", snapshot)
	}
	var artifact struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Download string `json:"download_path"`
	}
	decode(request(first.router, http.MethodPost, "/projects/"+project.ID+"/releases/"+snapshot.ID+"/exports", `{"format":"docx"}`, "g4-legacy-export", http.StatusAccepted), &artifact)
	if artifact.ID == "" || artifact.Status != "verified" || artifact.Download == "" {
		t.Fatalf("legacy export not verified: %+v", artifact)
	}

	closeG4E2EDatabase(t, db)
	second := buildG4E2EHTTP(t, reopen())
	var readBack struct {
		ID         string `json:"id"`
		RuleSetRef *struct {
			ID string `json:"ruleset_id"`
		} `json:"ruleset_ref"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/releases/"+snapshot.ID, "", "", http.StatusOK), &readBack)
	if readBack.ID != snapshot.ID || readBack.RuleSetRef != nil {
		t.Fatalf("legacy snapshot readback changed binding semantics: %+v", readBack)
	}
	var projectAuditEvents []struct {
		Action string `json:"action"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/audit", "", "", http.StatusOK), &projectAuditEvents)
	if len(projectAuditEvents) == 0 {
		t.Fatal("legacy project audit readback is empty")
	}
	var auditEnvelope struct {
		Items []struct {
			Action               string `json:"action"`
			RuleSetID            string `json:"ruleset_id"`
			EffectiveRulesetHash string `json:"effective_ruleset_hash"`
		} `json:"items"`
	}
	decode(request(second.router, http.MethodGet, "/projects/"+project.ID+"/rule-set-use-audits", "", "", http.StatusOK), &auditEnvelope)
	seen := map[string]bool{}
	for _, event := range auditEnvelope.Items {
		if event.EffectiveRulesetHash == "" {
			t.Fatalf("legacy audit lost compiled ruleset identity: %+v", event)
		}
		if event.RuleSetID != "" {
			t.Fatalf("legacy audit invented source RuleSet identity: %+v", event)
		}
		seen[event.Action] = true
	}
	if len(seen) != 0 {
		t.Fatalf("legacy path must not emit governed RuleSet-use audit without a canonical binding: seen=%v", seen)
	}
}
