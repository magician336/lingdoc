package workspacecore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture adds historical data to the production schema. It neither
// recreates that schema nor asserts that a historical STABILIZED row is a new
// scientific/stabilization decision made by this test.
func pr150SeedMigrationHistory(t *testing.T, s *Service) (Actor, Project, map[string]string) {
	t.Helper()
	ctx := context.Background()
	a := Actor{TenantID: 1, UserID: "migration-owner"}
	p := seedActiveImpactProject(t, s, a)
	chapters, err := s.ListChapters(ctx, a, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("read actual migration-fixture chapters: %v", err)
	}
	raw, _, _, err := s.SaveChapter(ctx, a, p.ID, chapters[0].ID, "migration-history-body-1", SaveChapterInput{
		ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "First historical body.", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var first Chapter
	if err := json.Unmarshal(raw, &first); err != nil || first.CurrentVersionID == nil {
		t.Fatalf("read first chapter version: %+v %v", first, err)
	}
	pr150SeedHistoricalConfirmation(t, s, first)
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "migration-history-change", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields: map[string]ChangeFieldInput{"research_goal": {
			OldValue: p.Spec["research_goal"], NewValue: "Applied historical goal",
		}},
		AffectedChapterIDs: []string{first.ID}, Reason: "Preserve actual historical application.",
	})
	if err != nil {
		t.Fatal(err)
	}
	var applied ChangeSet
	if err := json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ApplyChangeSet(ctx, a, p.ID, applied.ID, "migration-history-apply"); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = s.SaveChapter(ctx, a, p.ID, first.ID, "migration-history-body-2", SaveChapterInput{
		ExpectedChapterVersionID: first.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision,
		BodyMarkdown: "Current body after the historical change.", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var current Chapter
	if err := json.Unmarshal(raw, &current); err != nil || current.CurrentVersionID == nil {
		t.Fatalf("read current chapter version: %+v %v", current, err)
	}
	pr150SeedHistoricalConfirmation(t, s, current)
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{"applied": applied.ID}
	err = s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		store := tx.(gormTransaction)
		var original changeSetRow
		if err := store.db.Where("id = ?", applied.ID).Take(&original).Error; err != nil {
			return err
		}
		decoded, err := decodeChangeSet(original)
		if err != nil {
			return err
		}
		var provenance changeProvenance
		if _, err := store.GetProjectRecord(p.ID, "g1_change_provenance", applied.ID, &provenance); err != nil {
			return err
		}
		for i, status := range []string{"draft", "assessed", "applied", "stabilized", "rejected", "stale"} {
			row := original
			if status != "applied" {
				row.ID = uuid.NewString()
				ids[status] = row.ID
			}
			row.Status, row.Reason = status, "Existing 122 history: "+status
			if status != "applied" && status != "stabilized" {
				row.TargetContextRevision, row.TargetSpecRevision, row.AppliedAt = nil, nil, nil
			}
			var fields []byte
			if i%2 == 0 {
				fields, err = json.Marshal(decoded.Fields)
			} else {
				envelope := map[string]any{"fields": decoded.Fields, "stale_reason": "historical_basis", "reassessed_from_id": applied.ID}
				if status == "stabilized" {
					// a6 stores these additive fields inside the same envelope, not
					// new SQL columns. Preserve them without requiring f8 types to
					// already declare the fused decision/reverse projection fields.
					envelope["decision_by"] = a.UserID
					envelope["decided_at"] = original.AppliedAt
					envelope["reverses_change_set_id"] = applied.ID
				}
				fields, err = json.Marshal(envelope)
			}
			if err != nil {
				return err
			}
			row.FieldsJSON = string(fields)
			if status == "applied" {
				if err := store.db.Model(&changeSetRow{}).Where("id = ?", row.ID).
					Updates(map[string]any{"reason": row.Reason, "fields_json": row.FieldsJSON}).Error; err != nil {
					return err
				}
				continue
			}
			if err := store.db.Create(&row).Error; err != nil {
				return err
			}
			// The copied history has the exact same old values/baseline as the
			// actual applied proposal; retain its genuine immutable source pins.
			if err := store.PutProjectRecord(p.ID, "g1_change_provenance", row.ID, 0, provenance); err != nil {
				return err
			}
		}
		return store.SaveSemanticPlan(p.ID, ids["assessed"], []SemanticDelta{{
			Kind: "RouteNode", TargetID: "migration-route", ExpectedVersion: 0,
			Before: json.RawMessage(`{}`), BeforeHash: "historical-migration-fixture",
			SourceIDs: []string{}, ProposedRoute: &RouteNode{ID: "migration-route", Name: "Unapplied historical route", Status: "planned"},
		}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Exec("INSERT INTO lingdoc_ruleset_metrics (id,tenant_id,project_id,metric,operation,value,created_at) VALUES (?,?,?,?,?,?,?)",
		uuid.NewString(), a.TenantID, p.ID, "migration_sentinel", "status_migration", 17, time.Now().UTC()).Error; err != nil {
		t.Fatalf("main metrics migration was not present before satisfied: %v", err)
	}
	var sidecars []string
	if err := s.testDB().Table("lingdoc_g3_records").Where("project_id = ?", p.ID).Distinct("kind").Pluck("kind", &sidecars).Error; err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"semantic_plan", "assessment", "task", "handoff", "revision"} {
		found := false
		for _, kind := range sidecars {
			found = found || kind == required
		}
		if !found {
			t.Fatalf("missing actual history sidecar %s: %v", required, sidecars)
		}
	}
	for _, valid := range []bool{false, true} {
		var count int64
		if err := s.testDB().Table("lingdoc_chapter_confirmations").Where("valid = ?", valid).Count(&count).Error; err != nil || count == 0 {
			t.Fatalf("missing historical confirmation valid=%v: count=%d err=%v", valid, count, err)
		}
	}
	for _, table := range []string{"lingdoc_project_audits", "lingdoc_operations"} {
		var count int64
		if err := s.testDB().Table(table).Count(&count).Error; err != nil || count == 0 {
			t.Fatalf("missing actual application history %s: count=%d err=%v", table, count, err)
		}
	}
	return a, p, ids
}

func pr150SeedHistoricalConfirmation(t *testing.T, s *Service, chapter Chapter) {
	t.Helper()
	if chapter.CurrentVersionID == nil {
		t.Fatal("confirmation history requires a real chapter version")
	}
	if err := s.testDB().Exec("INSERT INTO lingdoc_chapter_confirmations (id,chapter_id,chapter_version_id,valid,details_json,created_at) VALUES (?,?,?,?,?,?)",
		uuid.NewString(), chapter.ID, *chapter.CurrentVersionID, true, `{"fixture":"old-122-confirmation"}`, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.testDB().Table("lingdoc_chapter_versions").Where("id = ?", *chapter.CurrentVersionID).Update("confirmation_valid", true).Error; err != nil {
		t.Fatal(err)
	}
}

func pr150MigrationRows(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close migration snapshot rows: %v", err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := []string{}
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		item := map[string]any{}
		for i, column := range columns {
			switch value := values[i].(type) {
			case []byte:
				item[column] = string(value)
			case time.Time:
				item[column] = value.UTC().Format(time.RFC3339Nano)
			default:
				item[column] = value
			}
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, string(raw))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(result)
	raw, _ := json.Marshal(result)
	return string(raw)
}

func pr150MigrationHistorySnapshot(t *testing.T, s *Service) string {
	t.Helper()
	db, err := s.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, table := range []string{"lingdoc_projects", "lingdoc_members", "lingdoc_member_permissions", "lingdoc_chapters", "lingdoc_chapter_versions", "lingdoc_chapter_confirmations", "lingdoc_change_sets", "lingdoc_g3_records", "lingdoc_g5_records", "lingdoc_project_audits", "lingdoc_operations", "lingdoc_ruleset_metrics"} {
		result[table] = pr150MigrationRows(t, db, "SELECT * FROM "+table)
	}
	raw, _ := json.Marshal(result)
	return string(raw)
}

func pr150MigrationSchema(t *testing.T, s *Service, flavor string) string {
	t.Helper()
	db, err := s.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if flavor == "sqlite" {
		return pr150MigrationRows(t, db, "SELECT type,name,sql FROM sqlite_master WHERE tbl_name='lingdoc_change_sets'")
	}
	return pr150MigrationRows(t, db, "SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='lingdoc_change_sets'::regclass") +
		pr150MigrationRows(t, db, "SELECT indexname,indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename='lingdoc_change_sets'")
}

func pr150ReadMigrationHistory(t *testing.T, s *Service, a Actor, p Project, ids map[string]string) string {
	t.Helper()
	changes := map[string]ChangeSet{}
	for status, id := range ids {
		change, err := s.GetChangeSet(context.Background(), a, p.ID, id)
		if err != nil || change.Status != status || len(change.Fields) != 1 {
			t.Fatalf("read preserved %s ChangeSet: %+v err=%v", status, change, err)
		}
		if status == "assessed" && len(change.SemanticDeltas) != 1 {
			t.Fatalf("envelope lost separately persisted semantic plan: %+v", change)
		}
		changes[status] = change
	}
	revisions, err := s.ListContextRevisions(context.Background(), a, p.ID)
	if err != nil || len(revisions) < 2 {
		t.Fatalf("actual ContextRevision history missing: %d err=%v", len(revisions), err)
	}
	raw, err := json.Marshal(struct {
		Changes   map[string]ChangeSet    `json:"changes"`
		Revisions []ContextRevisionRecord `json:"revisions"`
	}{changes, revisions})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// run calls the existing production database driver's Run method on a separate
// migration connection. It is not a replacement SQL splitter/transaction runner.
func pr150CheckSatisfiedMigration(t *testing.T, s *Service, flavor string, withSatisfied bool,
	run func([]byte) error, reopen func() *Service) {
	t.Helper()
	// This test rehearses the satisfied-change-set migration on a library that
	// stops before that migration. The current code writes the audit event's
	// version columns, so the library has to carry the audit table's current
	// shape before the service writes to it. The rehearsal touches another
	// table, so bringing the audit table forward does not affect what the
	// rehearsal proves.
	if !s.testDB().Migrator().HasColumn(&auditEventRow{}, "TargetVersion") {
		forward := ""
		for _, file := range productionLingDocMigrationNames(t, flavor) {
			if strings.Contains(file, "audit_version_fields") {
				forward = file
				break
			}
		}
		if forward == "" {
			t.Fatalf("the audit version fields migration is not in the %s production chain", flavor)
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", flavor, forward))
		if err != nil {
			t.Fatal(err)
		}
		if err := run(body); err != nil {
			t.Fatalf("bring the audit table forward: %v", err)
		}
	}
	a, p, ids := pr150SeedMigrationHistory(t, s)
	before := pr150MigrationHistorySnapshot(t, s)
	publicBefore := pr150ReadMigrationHistory(t, s, a, p, ids)
	version := "000043"
	if flavor == "versioned" {
		version = "000122"
	}
	readScript := func(direction string) []byte {
		file := filepath.Join("..", "..", "..", "migrations", flavor, version+"_lingdoc_satisfied_change_sets."+direction+".sql")
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	up, down := readScript("up"), readScript("down")
	if err := run(up); err != nil {
		t.Fatalf("apply unchanged %s satisfied up: %v", flavor, err)
	}
	if pr150MigrationHistorySnapshot(t, s) != before || pr150ReadMigrationHistory(t, s, a, p, ids) != publicBefore {
		t.Fatal("satisfied upgrade changed old 122 rows, sidecars, metrics or application history")
	}
	if withSatisfied {
		satisfied := changeSetRow{ID: uuid.NewString(), ProjectID: p.ID, CreatedBy: a.UserID, Reason: "Already current; no new revision.", Status: "satisfied",
			BaseContextRevision: p.CurrentContextRevision, TargetContextRevision: &p.CurrentContextRevision,
			BaseSpecRevision: p.SpecRevision, TargetSpecRevision: &p.SpecRevision,
			FieldsJSON: `{"fields":[]}`, ImpactsJSON: "[]", CreatedAt: time.Now().UTC()}
		if err := s.testDB().Create(&satisfied).Error; err != nil {
			t.Fatalf("new satisfied status is unavailable: %v", err)
		}
		expected, schema := pr150MigrationHistorySnapshot(t, s), pr150MigrationSchema(t, s, flavor)
		if err := run(down); err == nil {
			t.Fatal("down must refuse a satisfied result instead of rewriting its terminal meaning")
		}
		if pr150MigrationHistorySnapshot(t, s) != expected || pr150MigrationSchema(t, s, flavor) != schema {
			t.Fatal("refused down partially committed application data, constraints or indexes")
		}
		s = reopen()
		if pr150MigrationHistorySnapshot(t, s) != expected || pr150MigrationSchema(t, s, flavor) != schema {
			t.Fatal("refused down left durable partial state after connection restart")
		}
		read, err := s.GetChangeSet(context.Background(), a, p.ID, satisfied.ID)
		if err != nil || read.Status != "satisfied" {
			t.Fatalf("satisfied result unreadable after refused down/reopen: %+v %v", read, err)
		}
		if pr150ReadMigrationHistory(t, s, a, p, ids) != publicBefore {
			t.Fatal("refused down lost old application projections")
		}
	} else {
		if err := run(down); err != nil {
			t.Fatalf("down without satisfied must succeed: %v", err)
		}
		if pr150MigrationHistorySnapshot(t, s) != before || pr150ReadMigrationHistory(t, s, a, p, ids) != publicBefore {
			t.Fatal("successful down lost one of the six old states or linked history")
		}
		if err := run(up); err != nil {
			t.Fatalf("reapply up after clean down: %v", err)
		}
		s = reopen()
		if pr150MigrationHistorySnapshot(t, s) != before || pr150ReadMigrationHistory(t, s, a, p, ids) != publicBefore {
			t.Fatal("clean down/up changed old application history after reopen")
		}
	}
	// A refused/successful roundtrip must not silently remove the CHECK.
	invalid := changeSetRow{ID: uuid.NewString(), ProjectID: p.ID, CreatedBy: a.UserID, Reason: "Invalid status probe", Status: "unknown",
		BaseContextRevision: p.CurrentContextRevision, BaseSpecRevision: p.SpecRevision, FieldsJSON: "[]", ImpactsJSON: "[]", CreatedAt: time.Now().UTC()}
	if err := s.testDB().Create(&invalid).Error; err == nil {
		t.Fatal("status CHECK constraint was lost")
	}
	if flavor == "sqlite" {
		db, err := s.testDB().DB()
		if err != nil {
			t.Fatal(err)
		}
		if temporary := pr150MigrationRows(t, db, "SELECT name FROM sqlite_master WHERE name IN ('lingdoc_change_sets_next','lingdoc_change_sets_previous')"); temporary != "[]" {
			t.Fatalf("migration left a replacement table behind: %s", temporary)
		}
	}
}

func TestPR150SQLiteSatisfiedMigrationPreservesFullWorkflowHistory(t *testing.T) {
	for _, withSatisfied := range []bool{false, true} {
		name := "clean_down_up"
		if withSatisfied {
			name = "refused_down"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old-122.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			foundMetrics := false
			for _, file := range productionLingDocMigrationNames(t, "sqlite") {
				raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
					t.Fatalf("production SQLite prerequisite %s: %v", file, err)
				}
				if file == "000042_lingdoc_ruleset_metrics.up.sql" {
					foundMetrics = true
					break
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if !foundMetrics {
				t.Fatal("expected main SQLite042 metrics before SQLite043 satisfied")
			}
			current := testStore(t, path)
			run := func(raw []byte) error {
				migrationDB, err := sql.Open("sqlite", path)
				if err != nil {
					return err
				}
				driver, err := sqlite3migrate.WithInstance(migrationDB, &sqlite3migrate.Config{})
				if err != nil {
					_ = migrationDB.Close()
					return err
				}
				err = driver.Run(bytes.NewReader(raw))
				if closeErr := driver.Close(); err == nil {
					err = closeErr
				}
				return err
			}
			reopen := func() *Service {
				pool, err := current.testDB().DB()
				if err != nil {
					t.Fatal(err)
				}
				if err := pool.Close(); err != nil {
					t.Fatal(err)
				}
				current = testStore(t, path)
				return current
			}
			pr150CheckSatisfiedMigration(t, current, "sqlite", withSatisfied, run, reopen)
		})
	}
}

func TestPR150PostgresSatisfiedMigrationPreservesFullWorkflowHistory(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	for _, withSatisfied := range []bool{false, true} {
		name := "clean_down_up"
		if withSatisfied {
			name = "refused_down"
		}
		t.Run(name, func(t *testing.T) {
			// Same real isolated-schema harness as postgres_test.go. No SQLite
			// emulation is used for PostgreSQL constraint or transaction behavior.
			root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			rootSQL, err := root.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rootSQL.Close() })
			schema := "pr150_history_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
					t.Error(err)
				}
			})
			schemaDSN := dsn + " search_path=" + schema
			if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
				query := parsed.Query()
				query.Set("search_path", schema)
				parsed.RawQuery = query.Encode()
				schemaDSN = parsed.String()
			}
			open := func() *gorm.DB {
				db, err := gorm.Open(postgres.Open(schemaDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				if err != nil {
					t.Fatal(err)
				}
				pool, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = pool.Close() })
				return db
			}
			db := open()
			if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			foundMetrics := false
			for _, file := range productionLingDocMigrationNames(t, "versioned") {
				raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.ExecContext(context.Background(), string(raw)); err != nil {
					t.Fatalf("production PostgreSQL prerequisite %s: %v", file, err)
				}
				if file == "000121_lingdoc_ruleset_metrics.up.sql" {
					foundMetrics = true
					break
				}
			}
			if !foundMetrics {
				t.Fatal("expected main PostgreSQL121 metrics before PostgreSQL122 satisfied")
			}
			if err := db.Exec("CREATE TABLE tenant_members (role TEXT NOT NULL DEFAULT 'owner',tenant_id BIGINT,user_id TEXT,status TEXT,deleted_at TIMESTAMPTZ)").Error; err != nil {
				t.Fatal(err)
			}
			current := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
			run := func(raw []byte) error {
				migrationDB := open()
				migrationPool, err := migrationDB.DB()
				if err != nil {
					return err
				}
				driver, err := pgmigrate.WithInstance(migrationPool, &pgmigrate.Config{SchemaName: schema})
				if err != nil {
					_ = migrationPool.Close()
					return err
				}
				err = driver.Run(bytes.NewReader(raw))
				if closeErr := driver.Close(); err == nil {
					err = closeErr
				}
				return err
			}
			reopen := func() *Service {
				oldPool, err := current.testDB().DB()
				if err != nil {
					t.Fatal(err)
				}
				if err := oldPool.Close(); err != nil {
					t.Fatal(err)
				}
				current = NewServiceWithSources(NewGORMRepository(open()), ContractDemoTemplate{}, &fakeSources{})
				return current
			}
			pr150CheckSatisfiedMigration(t, current, "versioned", withSatisfied, run, reopen)
		})
	}
}
