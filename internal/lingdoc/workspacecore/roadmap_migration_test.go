package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The roadmap migrations (#250) have to be able to go up and to go down on
// both dialects, and the column definitions have to survive a restart.

const (
	roadmapSessionTable = "lingdoc_g3_roadmap_edit_sessions"
	roadmapEdgeTable    = "lingdoc_g3_route_edges"
)

// roadmapMigrationFile returns the up/down scripts of the roadmap migration
// in one dialect.
func roadmapMigrationFile(t *testing.T, flavor string) (string, string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", flavor, "*_lingdoc_roadmap_edit_sessions.*.sql"))
	if err != nil || len(matches) != 2 {
		t.Fatalf("find roadmap migrations in %s: %v", flavor, matches)
	}
	sort.Strings(matches)
	up, down := "", ""
	for _, file := range matches {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(file, ".up.sql") {
			up = string(raw)
		} else {
			down = string(raw)
		}
	}
	if up == "" || down == "" {
		t.Fatalf("roadmap migration in %s is missing an up or a down script", flavor)
	}
	return up, down
}

// assertRoadmapSchema checks that both tables exist with the columns the
// module writes.
func assertRoadmapSchema(t *testing.T, db *sql.DB, flavor string) {
	t.Helper()
	for table, columns := range map[string][]string{
		roadmapSessionTable: {"id", "project_id", "status", "baseline_json", "node_proposals_json", "edge_proposals_json", "result_json", "actor_id", "reason", "created_at", "updated_at"},
		roadmapEdgeTable:    {"id", "project_id", "edge_id", "version", "source_id", "target_id", "relation", "context_revision", "actor_id", "reason", "created_at"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
		if count != 1 {
			t.Fatalf("%s: table %s does not exist", flavor, table)
		}
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
		found := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, columnType string
			var notNull int
			var defaultValue any
			var primaryKey int
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				t.Fatalf("%s: %v", flavor, err)
			}
			found[name] = true
		}
		rows.Close()
		for _, column := range columns {
			if !found[column] {
				t.Fatalf("%s: table %s is missing the column %s", flavor, table, column)
			}
		}
	}
}

// The SQLite chain goes up, the column definitions survive a restart of the
// reader, and the down script removes the tables again.
func TestRoadmapMigrationGoesUpAndDownOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roadmap-migration.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range productionLingDocMigrationNames(t, "sqlite") {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	assertRoadmapSchema(t, db, "sqlite")

	// Restart: a fresh connection reads the same column definitions.
	restarted, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	assertRoadmapSchema(t, restarted, "sqlite (after restart)")

	_, down := roadmapMigrationFile(t, "sqlite")
	if _, err := restarted.ExecContext(context.Background(), down); err != nil {
		t.Fatalf("roadmap down migration: %v", err)
	}
	for _, table := range []string{roadmapSessionTable, roadmapEdgeTable} {
		var count int
		if err := restarted.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("the down migration left %s behind", table)
		}
	}
}

// The PostgreSQL chain goes up and down as well. The case is skipped when the
// host has no isolated test server, and it says so instead of pretending.
func TestRoadmapMigrationGoesUpAndDownOnPostgres(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}
	up, down := roadmapMigrationFile(t, "versioned")
	root, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	schema := "roadmap_" + filepath.Base(t.TempDir())
	if _, err := root.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer root.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	scoped, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()
	if _, err := scoped.ExecContext(context.Background(), up); err != nil {
		t.Fatalf("roadmap up migration: %v", err)
	}
	for table, columns := range map[string][]string{
		roadmapSessionTable: {"id", "project_id", "status", "baseline_json", "node_proposals_json", "edge_proposals_json", "result_json", "actor_id", "reason", "created_at", "updated_at"},
		roadmapEdgeTable:    {"id", "project_id", "edge_id", "version", "source_id", "target_id", "relation", "context_revision", "actor_id", "reason", "created_at"},
	} {
		rows, err := scoped.QueryContext(context.Background(), "SELECT column_name FROM information_schema.columns WHERE table_schema = '"+schema+"' AND table_name = '"+table+"'")
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			found[name] = true
		}
		rows.Close()
		for _, column := range columns {
			if !found[column] {
				t.Fatalf("table %s is missing the column %s", table, column)
			}
		}
	}
	if _, err := scoped.ExecContext(context.Background(), down); err != nil {
		t.Fatalf("roadmap down migration: %v", err)
	}
}
