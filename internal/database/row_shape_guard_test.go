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
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

// guardedRowShapes is the explicit registration checklist (Spec #707). Ticket
// #708 pins it to exactly one real entry — the #694 adoption idempotency row —
// so the known-good shape is permanently locked. #709's AST scan grows the
// checklist, and every growth lands as an explicit diff here.
var guardedRowShapes = []rowShapeRegistration{
	{
		PackagePath: "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption",
		TypeName:    "adoptionIdempotencyRow",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    candidateAdoptionIdempotencyRowSchema,
	},
}

// candidateAdoptionIdempotencyRowSchema returns gorm's own parse of the real
// adoption idempotency row. The row structure is unexported in its owning
// package and this ticket must not touch production code, so the parse is
// triggered through the store's existing exported AutoMigrate on a throwaway
// database and read back out of gorm's schema cache (see the block at the top
// of this file).
func candidateAdoptionIdempotencyRowSchema() (*schema.Schema, error) {
	dir, err := os.MkdirTemp("", "lingdoc-row-shape-guard-*")
	if err != nil {
		return nil, fmt.Errorf("row-shape guard: could not create the throwaway declaration database: %w", err)
	}
	defer os.RemoveAll(dir)

	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "declaration.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("row-shape guard: could not open the throwaway declaration database: %w", err)
	}
	if sqlDB, sqlErr := db.DB(); sqlErr == nil {
		defer func() { _ = sqlDB.Close() }()
	}

	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	if err := store.AutoMigrate(context.Background()); err != nil {
		return nil, fmt.Errorf("row-shape guard: the candidateadoption store could not parse its row structures: %w", err)
	}

	return gormParsedSchema(db, "lingdoc_candidate_adoptions", "candidateadoption", "adoptionIdempotencyRow")
}

// gormParsedSchema digs one parsed row structure out of gorm's schema cache.
// The cache holds gorm's own knowledge; the read is a reflect probe of the
// unexported Config.cacheStore field and fails loudly when a gorm upgrade
// moves it, instead of letting the guard pass silently.
func gormParsedSchema(db *gorm.DB, table, pkgName, typeName string) (*schema.Schema, error) {
	configValue := reflect.ValueOf(db.Config).Elem()
	cacheField := configValue.FieldByName("cacheStore")
	if !cacheField.IsValid() || cacheField.Kind() != reflect.Pointer || !cacheField.CanAddr() {
		return nil, fmt.Errorf(
			"row-shape guard: gorm's schema cache is no longer reachable at Config.cacheStore; " +
				"rework the declaration-side capture (see the row-shape guard's comment) — failing loudly instead of skipping",
		)
	}
	// The field's storage holds the *sync.Map pointer, so read the pointer
	// out through a double dereference, not by reinterpreting the field
	// address itself as a sync.Map.
	cache := *(**sync.Map)(unsafe.Pointer(cacheField.UnsafeAddr()))
	if cache == nil {
		return nil, fmt.Errorf(
			"row-shape guard: gorm's schema cache is nil; rework the declaration-side capture (see the row-shape guard's comment)",
		)
	}

	var found *schema.Schema
	cache.Range(func(_, value any) bool {
		parsed, ok := value.(*schema.Schema)
		if ok && parsed.Table == table &&
			strings.HasSuffix(parsed.ModelType.PkgPath(), "/lingdoc/"+pkgName) &&
			parsed.ModelType.Name() == typeName {
			found = parsed
			return false
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf(
			"row-shape guard: gorm parsed no schema for %s.%s on table %s; the capture mechanism needs rework instead of a silent pass",
			pkgName, typeName, table,
		)
	}
	return found, nil
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
	rowShapeTypeMismatch  = "type-mismatch"
	rowShapeTypeUnknown   = "type-not-in-normalization-table"
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

// declaredTypeFamilies is one half of rule 3's normalization (single source,
// see the block at the top of this file): it maps gorm's parsed DataType
// strings onto canonical type families. The first group is gorm's own
// constants; the second group is the `type:` tag spellings the registered row
// structures use. A type that lands in neither group is a violation (extend
// this table), never a silent pass.
var declaredTypeFamilies = map[string]string{
	"string": "string", "bytes": "blob", "bool": "bool",
	"int": "integer", "uint": "integer", "float": "float", "time": "time",

	"text": "string", "varchar": "string", "character varying": "string",
	"integer": "integer", "bigint": "integer", "smallint": "integer",
	"boolean":  "bool",
	"datetime": "time", "timestamp": "time", "date": "time",
	"blob": "blob", "binary": "blob", "varbinary": "blob",
	"double": "float", "decimal": "float", "numeric": "float", "real": "float",
}

// sqliteDeclTypeFamily is the other half of rule 3's normalization: it maps
// the decl types the real SQLite chain writes (PRAGMA table_info) onto the
// same canonical families. DATETIME and BOOLEAN are pinned ahead of the
// substring rules because SQLite's affinity rules would otherwise class them
// as NUMERIC; the substring rules otherwise mirror SQLite's affinity order.
func sqliteDeclTypeFamily(declType string) (string, bool) {
	upper := strings.ToUpper(declType)
	switch upper {
	case "":
		return "blob", true // a typeless column has SQLite affinity BLOB
	case "DATETIME", "TIMESTAMP", "DATE":
		return "time", true
	case "BOOLEAN", "BOOL":
		return "bool", true
	}
	switch {
	case strings.Contains(upper, "INT"):
		return "integer", true
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return "string", true
	case strings.Contains(upper, "BLOB"):
		return "blob", true
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"),
		strings.Contains(upper, "NUMERIC"), strings.Contains(upper, "DEC"):
		return "float", true
	}
	return "", false
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

		// Rule 2, second direction: a pointer field without the `not null` tag
		// declares NULL allowed, so the artifact column must allow NULL.
		if field.FieldType.Kind() == reflect.Pointer && !field.NotNull && shape.NotNull {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeNullability,
				Declared: fmt.Sprintf(
					"nullable (field %s %s, gorm tag %q)",
					field.Name, field.FieldType.String(), field.Tag.Get("gorm"),
				),
				Artifact: fmt.Sprintf("NOT NULL (decl type %q)", shape.DeclType),
			})
			continue
		}

		// Rule 3: type, compared loosely through the normalization tables.
		// An uncovered type on either side is its own violation — the fix is
		// extending the table here, never silently accepting the column.
		declaredFamily, declaredKnown := declaredTypeFamilies[string(field.DataType)]
		artifactFamily, artifactKnown := sqliteDeclTypeFamily(shape.DeclType)
		if !declaredKnown || !artifactKnown {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeTypeUnknown,
				Declared: fmt.Sprintf(
					"type %q is not covered by the guard's declared-type normalization table — extend the table (field %s %s, gorm tag %q)",
					field.DataType, field.Name, field.FieldType.String(), field.Tag.Get("gorm"),
				),
				Artifact: fmt.Sprintf(
					"decl type %q is not covered by the guard's SQLite normalization rules",
					shape.DeclType,
				),
			})
			continue
		}
		if declaredFamily != artifactFamily {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeTypeMismatch,
				Declared: fmt.Sprintf(
					"type %q (normalized: %s)",
					field.DataType, declaredFamily,
				),
				Artifact: fmt.Sprintf(
					"decl type %q (normalized: %s)",
					shape.DeclType, artifactFamily,
				),
			})
			continue
		}

		// Rule 4: length. SQLite decl types carry no length, so a declared
		// `size` never mismatches on this dialect (rule 4 above; the
		// PostgreSQL side of #710 enforces it with the same rules).
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

// rowShapeGuardPointerOnNotNullFixture is the reverse nullability direction:
// a pointer field without the `not null` tag declares NULL allowed, pointed
// at a column the real chain builds as NOT NULL (rule 2, second direction).
type rowShapeGuardPointerOnNotNullFixture struct {
	ProjectID *string
}

func (rowShapeGuardPointerOnNotNullFixture) TableName() string { return "lingdoc_candidate_adoptions" }

func TestRowShapeGuardFlagsPointerDeclarationAgainstANotNullColumn(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardPointerOnNotNullFixture",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    parseRowShapeFixture(&rowShapeGuardPointerOnNotNullFixture{}),
	}}
	violations := runRowShapeGuard(t, db, registrations)

	violation, found := findRowShapeViolation(violations, "project_id", rowShapeNullability)
	require.Truef(t, found,
		"the guard must flag a pointer declaration over a NOT NULL column; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "project_id", "failure must name the column")
	require.Contains(t, message, "nullable", "failure must show the declared nullability")
	require.Contains(t, message, "NOT NULL", "failure must show the artifact's nullability")
}

// rowShapeGuardTypeMismatchFixture declares a string where the real chain
// builds an integer column (rule 3: type families must agree).
type rowShapeGuardTypeMismatchFixture struct {
	DigestVersion string `gorm:"not null"`
}

func (rowShapeGuardTypeMismatchFixture) TableName() string { return "lingdoc_candidate_adoptions" }

func TestRowShapeGuardFlagsDeclaredTypeMismatchingTheArtifact(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardTypeMismatchFixture",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    parseRowShapeFixture(&rowShapeGuardTypeMismatchFixture{}),
	}}
	violations := runRowShapeGuard(t, db, registrations)

	violation, found := findRowShapeViolation(violations, "digest_version", rowShapeTypeMismatch)
	require.Truef(t, found,
		"the guard must flag a declared type whose family differs from the artifact's; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "digest_version", "failure must name the column")
	require.Contains(t, message, `type "string"`, "failure must show the declared type")
	require.Contains(t, message, `"INTEGER"`, "failure must show the artifact's decl type")
}

// rowShapeGuardUnknownTypeFixture declares a type no normalization rule
// covers (rule 3's ratchet: extend the table, never silently accept).
type rowShapeGuardUnknownTypeFixture struct {
	DigestVersion string `gorm:"column:digest_version;not null;type:jsonb"`
}

func (rowShapeGuardUnknownTypeFixture) TableName() string { return "lingdoc_candidate_adoptions" }

func TestRowShapeGuardFlagsDeclaredTypeOutsideTheNormalizationTable(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardUnknownTypeFixture",
		TableName:   "lingdoc_candidate_adoptions",
		schemaOf:    parseRowShapeFixture(&rowShapeGuardUnknownTypeFixture{}),
	}}
	violations := runRowShapeGuard(t, db, registrations)

	violation, found := findRowShapeViolation(violations, "digest_version", rowShapeTypeUnknown)
	require.Truef(t, found,
		"the guard must flag a declared type no normalization rule covers instead of silently passing it; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "digest_version", "failure must name the column")
	require.Contains(t, message, "normalization", "failure must point at the normalization table as the fix")
}

// TestGuardedRowShapesMatchTheSQLiteMigrationChain is the green sample: the
// #694 adoption idempotency row, parsed by gorm itself, against the real
// migration chain's product (000056 made chapter_version_id nullable). The
// precondition assertions keep this from passing vacuously — they prove the
// pointer/nullable direction of rule 2 really executed on the real row.
func TestGuardedRowShapesMatchTheSQLiteMigrationChain(t *testing.T) {
	// The checklist is pinned: exactly one real entry this ticket (#708).
	require.Len(t, guardedRowShapes, 1)
	require.Equal(t, "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption", guardedRowShapes[0].PackagePath)
	require.Equal(t, "adoptionIdempotencyRow", guardedRowShapes[0].TypeName)
	require.Equal(t, "lingdoc_candidate_adoptions", guardedRowShapes[0].TableName)

	parsed, err := guardedRowShapes[0].schemaOf()
	require.NoError(t, err)
	field, ok := parsed.FieldsByDBName["chapter_version_id"]
	require.True(t, ok, "the adoption idempotency row must declare chapter_version_id")
	require.Equal(t, reflect.Pointer, field.FieldType.Kind(),
		"the #694 fix made the field a pointer (*string)")
	require.False(t, field.NotNull, "the green sample's pointer must not carry a not null tag")

	db := migratedSQLiteChainForShapeGuard(t)
	violations := runRowShapeGuard(t, db, guardedRowShapes)
	require.Empty(t, violations,
		"the adoption idempotency row and the real migration chain must agree on every guarded column")
}
