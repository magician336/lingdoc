package types

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file" // production migration source
	_ "github.com/jackc/pgx/v5/stdlib"                   // PostgreSQL driver (production dialect)
	_ "github.com/mattn/go-sqlite3"                      // SQLite driver behind gorm.io/driver/sqlite (production dialect)
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // pure-Go SQLite driver used by some tests
)

// ---------------------------------------------------------------------------
// What the driver actually hands to a scanner.
//
// The shared reader (json_column.go) accepts two carriers: []byte and string.
// Which one a column produces is not a property of the column — it depends on
// the dialect and on the storage class of the value that was written:
//
//   - SQLite: the value's storage class decides. TEXT -> string, BLOB -> []byte.
//     The `models.parameters` column is declared TEXT, but this application
//     binds the []byte that json.Marshal returns, and SQLite keeps a BLOB a
//     BLOB, so the same column delivers []byte for application-written rows
//     and string for rows written by a literal SQL INSERT.
//   - PostgreSQL: pgx's database/sql layer decodes a json/jsonb column into
//     []byte, and a text column into string.
//
// These tests measure that instead of asserting it from memory. They are the
// evidence that the shared reader has to accept both carriers.
// ---------------------------------------------------------------------------

// productionSQLiteDBPath builds a SQLite database from the production
// migration chain and returns its path.
//
// The models table — and with it the `parameters` column this file measures —
// belongs to the production migration chain, not to this fixture. A fixture
// that hand-writes the column list drifts away from production the next time
// a column is added, and the drift stays green; the repository's own guard
// says so (scripts/ci/test_lingdoc_fixture_schema_source.py).
func productionSQLiteDBPath(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "carrier.db")
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	migrator, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = migrator.Close() })
	require.NoError(t, migrator.Up())
	return dbPath
}

func TestSQLiteJSONColumnCarrierTypes(t *testing.T) {
	dbPath := productionSQLiteDBPath(t)

	// "sqlite3" is the driver gorm.io/driver/sqlite opens — the production
	// SQLite path. "sqlite" is the pure-Go driver some tests use.
	for _, driverName := range []string{"sqlite3", "sqlite"} {
		t.Run(driverName, func(t *testing.T) {
			db, err := sql.Open(driverName, dbPath)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			// A row written as TEXT — what a literal SQL INSERT binds.
			_, err = db.Exec(
				`INSERT INTO models (id, tenant_id, name, type, source, parameters) VALUES (?, ?, ?, ?, ?, ?)`,
				"text-row-"+driverName, 7, "qwen3.8-flash", "KnowledgeQA", "remote", `{"provider":"aliyun"}`,
			)
			require.NoError(t, err)

			// A row written as BLOB — what this application binds.
			_, err = db.Exec(
				`INSERT INTO models (id, tenant_id, name, type, source, parameters) VALUES (?, ?, ?, ?, ?, ?)`,
				"blob-row-"+driverName, 7, "qwen3.8-flash", "KnowledgeQA", "remote", []byte(`{"provider":"aliyun"}`),
			)
			require.NoError(t, err)

			var textCarrier any
			require.NoError(t, db.QueryRow(
				`SELECT parameters FROM models WHERE id = ?`, "text-row-"+driverName,
			).Scan(&textCarrier))
			var blobCarrier any
			require.NoError(t, db.QueryRow(
				`SELECT parameters FROM models WHERE id = ?`, "blob-row-"+driverName,
			).Scan(&blobCarrier))

			t.Logf("driver=%s TEXT value -> %T, BLOB value -> %T", driverName, textCarrier, blobCarrier)

			assert.IsType(t, "", textCarrier, "a TEXT value must reach the scanner as a string")
			assert.IsType(t, []byte{}, blobCarrier, "a BLOB value must reach the scanner as []byte")
		})
	}
}

func TestPostgresJSONColumnCarrierTypes(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Read-only: the statements select literals, so no table and no row of
	// the connected database is touched.
	var jsonbCarrier any
	require.NoError(t, db.QueryRow(`SELECT '{"provider":"aliyun"}'::jsonb`).Scan(&jsonbCarrier))

	var textCarrier any
	require.NoError(t, db.QueryRow(`SELECT '{"provider":"aliyun"}'::text`).Scan(&textCarrier))

	t.Logf("driver=pgx jsonb value -> %T, text value -> %T", jsonbCarrier, textCarrier)

	assert.IsType(t, []byte{}, jsonbCarrier, "a jsonb value must reach the scanner as []byte")
	assert.IsType(t, "", textCarrier, "a text value must reach the scanner as a string")
}
