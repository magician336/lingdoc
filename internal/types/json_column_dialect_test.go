package types

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver (production dialect)
	_ "github.com/mattn/go-sqlite3"    // SQLite driver behind gorm.io/driver/sqlite (production dialect)
	_ "modernc.org/sqlite"             // pure-Go SQLite driver used by some tests
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSQLiteJSONColumnCarrierTypes(t *testing.T) {
	// "sqlite3" is the driver gorm.io/driver/sqlite opens — the production
	// SQLite path. "sqlite" is the pure-Go driver some tests use.
	for _, driverName := range []string{"sqlite3", "sqlite"} {
		t.Run(driverName, func(t *testing.T) {
			db, err := sql.Open(driverName, ":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			_, err = db.Exec(`CREATE TABLE models (id VARCHAR(64) PRIMARY KEY, parameters TEXT NOT NULL)`)
			require.NoError(t, err)

			// A row written as TEXT — what a literal SQL INSERT binds.
			_, err = db.Exec(
				`INSERT INTO models (id, parameters) VALUES (?, ?)`,
				"text-row", `{"provider":"aliyun"}`,
			)
			require.NoError(t, err)

			// A row written as BLOB — what this application binds.
			_, err = db.Exec(
				`INSERT INTO models (id, parameters) VALUES (?, ?)`,
				"blob-row", []byte(`{"provider":"aliyun"}`),
			)
			require.NoError(t, err)

			var textCarrier any
			require.NoError(t, db.QueryRow(`SELECT parameters FROM models WHERE id = 'text-row'`).Scan(&textCarrier))
			var blobCarrier any
			require.NoError(t, db.QueryRow(`SELECT parameters FROM models WHERE id = 'blob-row'`).Scan(&blobCarrier))

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
