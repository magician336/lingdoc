package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// The audit event's columns are one fact, not two.
//
// `AuditEvent` declares five decision facts (tenant, user, role, capability,
// decision). They had no columns of their own: they only rode along inside
// details_json, and the read side did not restore them, so the role filter
// fell back to `actor_id` alone (#415).
//
// The table is now the authority for its own shape, and this file keeps the two
// dialects and the row model honest about it:
//
//   - the SQLite chain and the PostgreSQL chain must produce the **same column
//     set** for lingdoc_project_audits. A column added on one side only is a
//     write that fails on the other side;
//   - the five decision columns must be there, so a reader can rebuild a
//     decision without parsing JSON.
//
// The row model itself is checked by the form scan in
// scripts/ci/test_lingdoc_fixture_schema_source.py: the model lives in another
// package, so the same invariant is checked in one place, not two.

// auditEventColumns are the columns the audit event is allowed to have. The
// list is the *result* of the production migration chain, spelled out so a
// change in either dialect has to be spelled out here as well.
var auditEventColumns = []string{
	"id", "project_id", "actor_id", "action", "target", "details_json", "created_at",
	"target_version", "project_revision",
	"tenant_id", "user_id", "role", "capability", "decision",
}

func TestAuditEventColumnsMatchTheProductionMigrations(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "audit-columns.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db := openSQLiteDB(t, dbPath)
	t.Cleanup(func() { _ = db.Close() })

	columns := sqliteTableColumns(t, db, "lingdoc_project_audits")
	sort.Strings(columns)
	expected := append([]string(nil), auditEventColumns...)
	sort.Strings(expected)
	require.Equal(t, expected, columns,
		"the SQLite migration chain does not produce the audit event's declared columns")
}

// TestAuditEventRowModelDeclaresExactlyTheTableColumns is the whole invariant
// in one place: the row model and the table must declare the same columns.
//
// The model answers with GORM's own naming strategy
// (core.AuditEventRowColumns), so the check compares two answers produced by
// the production code itself, not two re-implementations of it. A field added
// to the model without a column, or a column added without a field, is what
// used to hide here (#415).
func TestAuditEventRowModelDeclaresExactlyTheTableColumns(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	columns := migrationChainColumns(t, filepath.Join("migrations", "sqlite"), "lingdoc_project_audits")
	require.NotEmpty(t, columns, "the audit table was not found in the migration chain")

	model := core.AuditEventRowColumns()
	require.NotEmpty(t, model, "the audit event's row model declares no columns")
	require.Equal(t, columns, model,
		"the audit event's row model and its table declare different columns")
}

func TestAuditEventColumnsAreTheSameInBothDialects(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	sqlite := migrationChainColumns(t, filepath.Join("migrations", "sqlite"), "lingdoc_project_audits")
	versioned := migrationChainColumns(t, filepath.Join("migrations", "versioned"), "lingdoc_project_audits")
	expected := append([]string(nil), auditEventColumns...)
	sort.Strings(expected)
	require.Equal(t, expected, sqlite,
		"the SQLite migration chain does not spell out the audit event's declared columns")
	require.Equal(t, sqlite, versioned,
		"the two dialects disagree about the audit event's columns; a write that works on one fails on the other")
}

func TestAuditEventDecisionColumnsExistInBothDialects(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	for _, dialect := range []string{"sqlite", "versioned"} {
		columns := migrationChainColumns(t, filepath.Join("migrations", dialect), "lingdoc_project_audits")
		for _, column := range []string{"tenant_id", "user_id", "role", "capability", "decision"} {
			require.Contains(t, columns, column,
				"the %s dialect does not carry the audit event's %s column", dialect, column)
		}
	}
}

// sqliteTableColumns reads the columns of one table out of a live database.
func sqliteTableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	return columns
}

// createTableColumns reads the table name and the body out of a CREATE TABLE
// statement.
var createTableColumns = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([A-Za-z0-9_]+)"?\s*\((.*)\)`)

// addColumnColumns reads the table and the column out of an ADD COLUMN
// statement. The PostgreSQL chain writes the clause on the next line, so the
// statement is matched as a whole.
var addColumnColumns = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+"?([A-Za-z0-9_]+)"?\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([A-Za-z0-9_]+)"?`)

// columnName reads the column name out of one column definition.
var columnName = regexp.MustCompile(`^["']?([A-Za-z0-9_]+)["']?[\s(]`)

// migrationChainColumns collects the columns one dialect's whole migration
// chain gives one table: the base CREATE TABLE plus every later ADD COLUMN.
//
// The chain is replayed from the files rather than from a database, because the
// PostgreSQL chain cannot be replayed here. The SQLite chain is also replayed
// from the files in the dialect-parity test, so the two are compared on the
// same footing.
func migrationChainColumns(t *testing.T, dir, table string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)

	columns := map[string]bool{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, statement := range strings.Split(string(raw), ";") {
			trimmed := strings.TrimSpace(statement)
			if trimmed == "" {
				continue
			}
			if match := createTableColumns.FindStringSubmatch(trimmed); match != nil && match[1] == table {
				for _, definition := range strings.Split(match[2], ",") {
					definition = strings.TrimSpace(definition)
					if definition == "" || strings.HasPrefix(definition, "--") {
						continue
					}
					if name := columnName.FindStringSubmatch(definition); name != nil {
						columns[name[1]] = true
					}
				}
				continue
			}
			if match := addColumnColumns.FindStringSubmatch(trimmed); match != nil && match[1] == table {
				columns[match[2]] = true
			}
		}
	}
	out := make([]string, 0, len(columns))
	for name := range columns {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
