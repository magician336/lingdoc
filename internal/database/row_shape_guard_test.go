package database

// The row-shape guard keeps the registered gorm row structures and the real
// SQLite migration chain honest about the same tables (Spec #707, ticket
// #708). Everything in this block is the single source of the comparison
// semantics: there is deliberately no second place that restates these rules.
//
// What is compared, per registered (package, row structure, table) entry:
//
//  1. Column existence — every column the row structure declares (every
//     parsed gorm field with a DB name) must exist in the migration artifact.
//     One direction only in v1: artifact columns the struct does not declare
//     are not flagged.
//  2. Nullability, both directions:
//     - a value-type field with the `not null` tag declares NOT NULL; the
//       artifact column must be NOT NULL (the #693 shape);
//     - a pointer field without the `not null` tag declares NULL allowed; the
//       artifact column must allow NULL.
//     Not enforced in v1 (deliberately): a value-type field without `not
//     null` (gorm writes zero values, e.g. created_at), and a pointer field
//     with `not null` (a declaration contradiction outside the two directions
//     above). SQLite quirk folded in here: PRIMARY KEY columns report
//     notnull=0 unless NOT NULL is spelled out, so a primary key without a
//     `not null` tag sits in the unenforced bucket and the quirk never fires.
//  3. Type — compared loosely through the normalization tables below: the
//     declared gorm DataType and the artifact's SQLite decl type are each
//     mapped to one canonical family and the families must agree. A declared
//     or artifact type that no rule covers is itself a violation telling the
//     contributor to extend the table here — never silently accepted, never
//     downgraded to report-only.
//  4. Length — a declared `size` is compared only when the artifact side
//     exposes a length. SQLite decl types carry no length, so on this dialect
//     a declared size never mismatches (documented no-op; the PostgreSQL side
//     of #710 will enforce it against the same rules).
//
// What is NOT compared (v1): default values, foreign keys, indexes, unique
// constraints, autoincrement, primary-key semantics.
//
// Registration and exclusions: the checklist (guardedRowShapes) is explicit.
// This ticket needs no exclusions; if one ever is needed it must be added
// next to the checklist with its own reason comment (Spec #707: exclusions
// are explicit and reasoned, and the target state is zero).
//
// Failure messages must carry the table name, the column name and both
// sides' concrete shapes (declared vs artifact), so a mismatch is fixable
// without opening a database by hand.
//
// The real checklist entry's row structure is unexported in its owning
// package and this ticket must not touch production code, so the declaration
// side is captured through gorm itself: the owning store's existing exported
// AutoMigrate runs on a throwaway database, which makes gorm parse the real
// structs, and the parsed *schema.Schema values are read out of gorm's own
// schema cache (Config.cacheStore, a reflect read-only probe). If a gorm
// upgrade moves that field, the guard fails loudly here instead of silently
// passing.

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// rowShapeRegistration is one explicit entry of the shape guard's checklist:
// the owning package, the row structure's type name, the table it maps to,
// and the gorm-parsed schema of that exact structure.
type rowShapeRegistration struct {
	PackagePath string
	TypeName    string
	TableName   string
	schemaOf    func() (*schema.Schema, error)
}

// rowShapeViolation is one mismatch the guard found. Error() renders the
// contract above: table name, column name, both sides' shapes.
type rowShapeViolation struct {
	Table    string
	Column   string
	Kind     string
	Declared string
	Artifact string
}

func (v rowShapeViolation) Error() string {
	return fmt.Sprintf(
		"%s.%s: row shape mismatch (%s). declared side: %s. artifact side: %s.",
		v.Table, v.Column, v.Kind, v.Declared, v.Artifact,
	)
}

const (
	rowShapeColumnMissing = "column-existence"
	rowShapeNullability   = "nullability"
)

// migrationColumnShape is one column of the real migration chain's product,
// read straight out of PRAGMA table_info.
type migrationColumnShape struct {
	Name     string
	DeclType string
	NotNull  bool
}

// migratedSQLiteChainForShapeGuard builds a fresh database through the real
// migration chain and opens it. The artifact side never comes from a
// hand-written schema.
func migratedSQLiteChainForShapeGuard(t *testing.T) *sql.DB {
	t.Helper()
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "shape-guard.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	return openSQLiteDB(t, dbPath)
}

// migrationTableShapes reads every column of one table out of the migrated
// chain's product.
func migrationTableShapes(t *testing.T, db *sql.DB, table string) map[string]migrationColumnShape {
	t.Helper()
	rows, err := db.Query(`SELECT name, type, "notnull" FROM pragma_table_info(?)`, table)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	shapes := map[string]migrationColumnShape{}
	for rows.Next() {
		var shape migrationColumnShape
		require.NoError(t, rows.Scan(&shape.Name, &shape.DeclType, &shape.NotNull))
		shapes[shape.Name] = shape
	}
	require.NoError(t, rows.Err())
	return shapes
}

// parseRowShapeFixture parses a test-owned fixture structure with gorm's own
// parser — the same knowledge gorm uses when it writes rows.
func parseRowShapeFixture(model any) func() (*schema.Schema, error) {
	return func() (*schema.Schema, error) {
		return schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
	}
}

// runRowShapeGuard compares every registration against the migrated chain's
// artifact and returns all violations.
func runRowShapeGuard(t *testing.T, db *sql.DB, registrations []rowShapeRegistration) []rowShapeViolation {
	t.Helper()
	var violations []rowShapeViolation
	for _, reg := range registrations {
		parsed, err := reg.schemaOf()
		require.NoError(t, err)
		require.Equal(t, reg.TableName, parsed.Table,
			"registered table name must agree with the row structure's TableName()")
		violations = append(violations, compareRowShape(reg, parsed, migrationTableShapes(t, db, reg.TableName))...)
	}
	return violations
}

// compareRowShape applies the rules at the top of this file to one parsed row
// structure against one table's artifact columns.
func compareRowShape(reg rowShapeRegistration, parsed *schema.Schema, artifact map[string]migrationColumnShape) []rowShapeViolation {
	var violations []rowShapeViolation

	for _, field := range parsed.Fields {
		if field.DBName == "" {
			continue // rule 1 speaks about declared columns; a field without a DB name declares none
		}
		shape, exists := artifact[field.DBName]
		if !exists {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeColumnMissing,
				Declared: fmt.Sprintf(
					"column declared (field %s %s, gorm tag %q)",
					field.Name, field.FieldType.String(), field.Tag.Get("gorm"),
				),
				Artifact: "column absent from the migration chain's table",
			})
			continue
		}

		// Rule 2, first direction: a value-type field with the `not null` tag
		// declares NOT NULL, so the artifact column must be NOT NULL.
		if field.NotNull && field.FieldType.Kind() != reflect.Pointer && !shape.NotNull {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeNullability,
				Declared: fmt.Sprintf(
					"NOT NULL (field %s %s, gorm tag %q)",
					field.Name, field.FieldType.String(), field.Tag.Get("gorm"),
				),
				Artifact: fmt.Sprintf("nullable (decl type %q)", shape.DeclType),
			})
			continue
		}

		// Rule 2, second direction (pointer declares NULL allowed) lands with
		// its red test. Rules 3/4 (type/length) land with theirs.
		_ = shape
	}

	return violations
}

// --- Intentional mismatch fixtures -----------------------------------------
//
// The fixtures are guard self-tests, never checklist entries: each one is a
// deliberately wrong declaration pointed at a real migration-chain artifact,
// proving the guard actually fails instead of passing vacuously.

// rowShapeGuardMissingColumnFixture declares a column the migration chain
// does not build (rule 1: column existence).
type rowShapeGuardMissingColumnFixture struct {
	ProjectID   string `gorm:"not null;size:36"`
	GhostColumn string `gorm:"column:ghost_column;not null"`
}

func (rowShapeGuardMissingColumnFixture) TableName() string { return "lingdoc_candidate_adoptions" }

func TestRowShapeGuardFlagsDeclaredColumnMissingFromTheMigrationArtifact(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardMissingColumnFixture",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    parseRowShapeFixture(&rowShapeGuardMissingColumnFixture{}),
	}}
	violations := runRowShapeGuard(t, db, registrations)

	violation, found := findRowShapeViolation(violations, "ghost_column", rowShapeColumnMissing)
	require.Truef(t, found,
		"the guard must flag a declared column the migration chain does not build; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "ghost_column", "failure must name the column")
	require.Contains(t, message, "declared side", "failure must show the declared shape")
	require.Contains(t, message, "artifact side", "failure must show the artifact shape")
}

// findRowShapeViolation picks the one violation the current test is about.
func findRowShapeViolation(violations []rowShapeViolation, column, kind string) (rowShapeViolation, bool) {
	for _, violation := range violations {
		if violation.Column == column && violation.Kind == kind {
			return violation, true
		}
	}
	return rowShapeViolation{}, false
}

// rowShapeGuardNotNullOnNullableFixture replicates the #693 shape on the real
// migration artifact: a value-type field with the `not null` tag pointed at
// the column 000056 made nullable (rule 2, first direction).
type rowShapeGuardNotNullOnNullableFixture struct {
	ChapterVersionID string `gorm:"not null;size:36"`
}

func (rowShapeGuardNotNullOnNullableFixture) TableName() string { return "lingdoc_candidate_adoptions" }

func TestRowShapeGuardFlagsNotNullDeclarationAgainstANullableColumn(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardNotNullOnNullableFixture",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    parseRowShapeFixture(&rowShapeGuardNotNullOnNullableFixture{}),
	}}
	violations := runRowShapeGuard(t, db, registrations)

	violation, found := findRowShapeViolation(violations, "chapter_version_id", rowShapeNullability)
	require.Truef(t, found,
		"the guard must flag a not-null declaration over a column the chain allows NULL on; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "chapter_version_id", "failure must name the column")
	require.Contains(t, message, "NOT NULL", "failure must show the declared nullability")
	require.Contains(t, message, "nullable", "failure must show the artifact's nullability")
}
