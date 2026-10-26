package database

// The row-shape guard keeps the registered gorm row structures and the real
// migration chains honest about the same tables (Spec #707, tickets #708 and
// #709: the SQLite side; ticket #710: the PostgreSQL twin in
// row_shape_guard_postgres_test.go, which runs these same rules against the
// versioned chain). Everything in this block is the single source of the
// comparison semantics: there is deliberately no second place that restates
// these rules — the dialects differ only in the artifact-side type tier and
// rule 3's agreement rule, both tiered below.
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
//     declared gorm DataType and the artifact's decl type are each mapped to
//     one canonical family and the families must agree. The agreement rule is
//     dialect-tiered (typeFamiliesAgreeOnDialect): SQLite keeps one documented
//     equivalence (typeFamiliesAgree) — a declared bool accepts an INTEGER
//     artifact column, because SQLite has no separate boolean storage class
//     and stores booleans as integers — directionally, so an integer
//     declaration cannot hide behind it; PostgreSQL has a real boolean type,
//     so its twin compares the families directly and that equivalence does
//     not carry over (#710, per #709's risk note). A declared or artifact
//     type that no rule covers is itself a violation telling the contributor
//     to extend the table here — never silently accepted, never downgraded to
//     report-only.
//  4. Length — a declared `size` is compared whenever the artifact side
//     exposes a length. SQLite decl types carry no enforced length (SQLite
//     accepts any string into a varchar column regardless of its declared
//     width), so on this dialect the comparison stays a documented no-op.
//     PostgreSQL's information seam exposes character_maximum_length and the
//     PostgreSQL reader feeds it into the same comparison: #710's first full
//     run proved it end-to-end by flagging two true declaration-side width
//     drifts on the delivery structs, and #713 fixed those declarations and
//     turned the rule on. Silence here means the declared width matches the
//     migrated column; it is never a skipped comparison.
//
// What is NOT compared (v1): default values, foreign keys, indexes, unique
// constraints, autoincrement, primary-key semantics.
//
// Registration and exclusions: the checklist (guardedRowShapes) is explicit —
// one (package, type, table) entry per row structure, currently the domain's
// full set. The AST scan in row_shape_registry_ast_test.go enforces the other
// direction: a TableName row structure missing from this list is red, and a
// list entry no type answers to is red. This ticket needs no exclusions; if
// one ever is needed it must be added next to the checklist with its own
// reason comment (Spec #707: exclusions are explicit and reasoned, and the
// target state is zero).
//
// Failure messages must carry the table name, the column name and both
// sides' concrete shapes (declared vs artifact), so a mismatch is fixable
// without opening a database by hand.
//
// The row structures are unexported in their owning packages, so the
// declaration side is captured through one exported door per package
// (RowShapesForSchemaGuard, keyed by type name; the precedent is
// workspacecore.AuditEventRowColumns). The doors are test-support only —
// nothing in production calls them — and the guard parses what they return
// with gorm's own parser, the same knowledge gorm writes rows with.

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// rowShapeRegistration is one explicit entry of the shape guard's checklist:
// the owning package, the row structure's type name, the table it maps to,
// and the structure itself (reached through the owning package's
// RowShapesForSchemaGuard door; see the block at the top of this file).
type rowShapeRegistration struct {
	PackagePath string
	TypeName    string
	TableName   string
	model       any
}

// registration builds one checklist entry: the explicit (package, type,
// table) triple, with the structure resolved from the owning package's door
// under the very same names — so a typo in any of the three names fails
// loudly at init, before the guard even runs.
func registration(pkg, typeName, table string) rowShapeRegistration {
	return rowShapeRegistration{
		PackagePath: lingdocDomainModulePath + "/" + pkg,
		TypeName:    typeName,
		TableName:   table,
		model:       rowShapeModel(pkg, typeName),
	}
}

// rowShapeDoors maps the checklist's package shorthand to the owning
// package's RowShapesForSchemaGuard door.
var rowShapeDoors = map[string]func() map[string]any{
	"candidateadoption":  candidateadoption.RowShapesForSchemaGuard,
	"delivery":           delivery.RowShapesForSchemaGuard,
	"generation":         generation.RowShapesForSchemaGuard,
	"rulesetstore":       rulesetstore.RowShapesForSchemaGuard,
	"templategovernance": templategovernance.RowShapesForSchemaGuard,
	"workspace":          workspace.RowShapesForSchemaGuard,
	"workspacecore":      workspacecore.RowShapesForSchemaGuard,
}

// rowShapeModel resolves one row structure through its package's door.
func rowShapeModel(pkg, typeName string) any {
	door, ok := rowShapeDoors[pkg]
	if !ok {
		panic("row-shape guard: 清单登记的包没有声明门（RowShapesForSchemaGuard）：" + pkg)
	}
	model, ok := door()[typeName]
	if !ok {
		panic("row-shape guard: 行结构 " + pkg + "." + typeName + " 未由其包的门提供——清单与门的键名必须一致")
	}
	return model
}

// hostRowShapeDoors maps one full import path to the owning package's
// RowShapesForSchemaGuard door, for row structures that live outside
// internal/lingdoc but belong to LingDoc tables the guard watches.
var hostRowShapeDoors = map[string]func() map[string]any{
	"github.com/Tencent/WeKnora/internal/evidence": evidence.RowShapesForSchemaGuard,
}

// hostRegistration registers one out-of-domain row structure: its owning
// package sits outside internal/lingdoc, but its table is a LingDoc table.
// #713 registered the first one — lingdoc_project_assets' full row lives in
// internal/evidence while the domain only holds a narrow projection — so the
// reverse coverage check unions the real owner's columns, not just the
// projection's. The identity assertions treat it exactly like a domain
// entry; the AST scan does not demand it, because that scan only patrols the
// domain (host entries are extra coverage, never scan obligations).
func hostRegistration(pkgPath, typeName, table string) rowShapeRegistration {
	door, ok := hostRowShapeDoors[pkgPath]
	if !ok {
		panic("row-shape guard: 域外包没有声明门（RowShapesForSchemaGuard）：" + pkgPath)
	}
	model, ok := door()[typeName]
	if !ok {
		panic("row-shape guard: 行结构 " + pkgPath + "." + typeName + " 未由其包的门提供——清单与门的键名必须一致")
	}
	return rowShapeRegistration{
		PackagePath: pkgPath,
		TypeName:    typeName,
		TableName:   table,
		model:       model,
	}
}

// rowShapeExclusion is one explicitly deferred checklist entry. The target
// state is zero exclusions; an entry is only legal with all three hard
// requirements — a per-item reason, a written unlock condition, and a
// follow-up issue — and the registry cross-check fails when any is missing
// (Spec #707: exclusions are explicit and reasoned, never a silent hole).
type rowShapeExclusion struct {
	RelPackage      string // package dir relative to internal/lingdoc
	TypeName        string
	TableName       string
	Reason          string
	UnlockCondition string
	FollowUpIssue   int
}

// deferredRowShapeExclusions holds the explicitly deferred entries. #709
// ships with none: every discovered row structure is registered and compared.
var deferredRowShapeExclusions = []rowShapeExclusion{}

// guardedRowShapes is the explicit registration checklist (Spec #707).
// #708 pinned one real entry; #709 registers the domain's full set — every
// row structure the AST scan finds (currently 47 across 7 packages; the scan
// is the authority and this list must keep matching it). Every growth or
// shrinkage lands as an explicit diff here.
var guardedRowShapes = []rowShapeRegistration{
	// candidateadoption —— candidate_adoption_store.go
	registration("candidateadoption", "adoptionIdempotencyRow", "lingdoc_candidate_adoptions"),
	registration("candidateadoption", "adoptionWorkingCopyRow", "lingdoc_working_copies"),
	registration("candidateadoption", "candidateRow", "lingdoc_candidates"),
	registration("candidateadoption", "chapterRow", "lingdoc_chapters"),
	registration("candidateadoption", "chapterVersionRow", "lingdoc_chapter_versions"),
	registration("candidateadoption", "confirmationIdempotencyRow", "lingdoc_chapter_confirmation_requests"),
	registration("candidateadoption", "confirmationRow", "lingdoc_chapter_confirmations"),
	registration("candidateadoption", "projectAssetRow", "lingdoc_project_assets"),
	// internal/evidence —— 宿主侧持有的 LingDoc 行结构（#713）：资料域的
	// 完整行结构落在域外包里（该表的设计对应关系见 ProjectAsset 头注），
	// 域内只登记了窄投影；反向覆盖检查需要完整行的列并集。
	hostRegistration("github.com/Tencent/WeKnora/internal/evidence", "ProjectAsset", "lingdoc_project_assets"),
	registration("candidateadoption", "projectAuditRow", "lingdoc_project_audits"),
	registration("candidateadoption", "projectRow", "lingdoc_projects"),
	registration("candidateadoption", "projectTemplateCopyRow", "lingdoc_project_template_copies"),

	// delivery —— sqlite_export_store.go / sqlite_snapshot_store.go
	registration("delivery", "exportArtifactListRow", "lingdoc_export_artifacts"),
	registration("delivery", "exportArtifactRow", "lingdoc_export_artifacts"),
	registration("delivery", "releaseSnapshotRow", "lingdoc_release_snapshots"),

	// generation —— store.go
	registration("generation", "generationRow", "lingdoc_generation_runs"),

	// rulesetstore —— store.go / metrics.go / use_audit.go
	registration("rulesetstore", "ruleSetMetricRow", "lingdoc_ruleset_metrics"),
	registration("rulesetstore", "ruleSetUseAuditRow", "lingdoc_ruleset_use_audits"),
	registration("rulesetstore", "ruleSetVersionRow", "lingdoc_rule_sets"),

	// templategovernance —— schema.go
	registration("templategovernance", "operationRow", "lingdoc_template_governance_operations"),
	registration("templategovernance", "profileRow", "lingdoc_template_profiles"),
	registration("templategovernance", "suggestionRow", "lingdoc_rule_suggestion_runs"),
	registration("templategovernance", "versionRow", "lingdoc_template_versions"),

	// workspace —— selected_rewrite.go / selected_rewrite_review.go
	registration("workspace", "reviewReportRow", "lingdoc_review_reports"),
	registration("workspace", "selectedRewriteRow", "lingdoc_selected_rewrites"),

	// workspacecore —— agent_object_storage.go / impact_dependency_candidate_store.go /
	// impact_store.go / membership_lifecycle.go / model.go / model_call_ledger.go /
	// roadmap_store.go / runtime_control.go
	registration("workspacecore", "auditEventRow", "lingdoc_project_audits"),
	registration("workspacecore", "chapterConfirmationRow", "lingdoc_chapter_confirmations"),
	registration("workspacecore", "chapterRow", "lingdoc_chapters"),
	registration("workspacecore", "chapterVersionRow", "lingdoc_chapter_versions"),
	registration("workspacecore", "changeSetRow", "lingdoc_change_sets"),
	registration("workspacecore", "dependencyCandidateBatchRow", "lingdoc_g3_dependency_candidate_batches"),
	registration("workspacecore", "dependencyCandidateRow", "lingdoc_g3_dependency_candidates"),
	registration("workspacecore", "draftCandidateRow", "lingdoc_draft_candidates"),
	registration("workspacecore", "memberPermissionRow", "lingdoc_member_permissions"),
	registration("workspacecore", "memberRow", "lingdoc_members"),
	registration("workspacecore", "modelCallLedgerRow", "lingdoc_model_call_ledger"),
	registration("workspacecore", "objectRow", "lingdoc_g2_objects"),
	registration("workspacecore", "objectVersionRow", "lingdoc_g2_object_versions"),
	registration("workspacecore", "operationRow", "lingdoc_operations"),
	registration("workspacecore", "ownerTransferRow", "lingdoc_owner_transfers"),
	registration("workspacecore", "projectRecordRow", "lingdoc_g5_records"),
	registration("workspacecore", "projectRow", "lingdoc_projects"),
	registration("workspacecore", "projectTemplateCopyRow", "lingdoc_project_template_copies"),
	registration("workspacecore", "roadmapEditSessionRow", "lingdoc_g3_roadmap_edit_sessions"),
	registration("workspacecore", "routeEdgeRow", "lingdoc_g3_route_edges"),
	registration("workspacecore", "runtimeRecordRow", "lingdoc_g6_records"),
	registration("workspacecore", "versionedRecordRow", "lingdoc_g3_records"),
	registration("workspacecore", "workingCopyRow", "lingdoc_working_copies"),
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
	rowShapeColumnMissing  = "column-existence"
	rowShapeNullability    = "nullability"
	rowShapeTypeMismatch   = "type-mismatch"
	rowShapeTypeUnknown    = "type-not-in-normalization-table"
	rowShapeLengthMismatch = "length-mismatch"
	rowShapeUnmappedColumn = "artifact-column-not-declared"
)

// The dialects the guard runs against. The checklist and the comparison
// semantics are shared; the dialect decides the artifact-side type tier and
// rule 3's agreement rule (see the header block).
const (
	dialectSQLite     = "SQLite"
	dialectPostgreSQL = "PostgreSQL"
)

// migrationColumnShape is one column of a real migration chain's product,
// read straight out of the dialect's schema seam: PRAGMA table_info on
// SQLite, information_schema.columns on PostgreSQL. Length carries the
// column's maximum length when the dialect enforces one; the SQLite reader
// leaves it unset (rule 4 above), which keeps the length comparison a
// documented no-op on that dialect.
type migrationColumnShape struct {
	Name     string
	DeclType string
	NotNull  bool
	Length   sql.NullInt64
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

// parseRowShape parses one row structure with gorm's own parser — the same
// knowledge gorm uses when it writes rows. The declaration side never comes
// from a hand-rolled reading of the tags.
func parseRowShape(model any) (*schema.Schema, error) {
	return schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
}

// rowShapeArtifactReader reads one table's columns out of a real migration
// chain's product on its dialect (PRAGMA table_info on SQLite,
// information_schema.columns on PostgreSQL).
type rowShapeArtifactReader func(t *testing.T, table string) map[string]migrationColumnShape

// runRowShapeGuard compares every registration against the migrated SQLite
// chain's artifact and returns all violations. The PostgreSQL twin
// (row_shape_guard_postgres_test.go) calls runRowShapeGuardAgainst with the
// same checklist and its own reader.
func runRowShapeGuard(t *testing.T, db *sql.DB, registrations []rowShapeRegistration) []rowShapeViolation {
	t.Helper()
	return runRowShapeGuardAgainst(t, dialectSQLite, registrations, func(t *testing.T, table string) map[string]migrationColumnShape {
		return migrationTableShapes(t, db, table)
	})
}

// runRowShapeGuardAgainst is the dialect-neutral engine: one checklist, one
// comparison semantics, any dialect's real migration artifact. The identity
// assertions keep the checklist honest: the registered (package, type, table)
// triple must be the parsed structure's real identity, not just documentation.
func runRowShapeGuardAgainst(t *testing.T, dialect string, registrations []rowShapeRegistration, readShapes rowShapeArtifactReader) []rowShapeViolation {
	t.Helper()
	var violations []rowShapeViolation
	for _, reg := range registrations {
		require.NotNil(t, reg.model, "checklist entry "+reg.PackagePath+"."+reg.TypeName+" resolved to no row structure")
		parsed, err := parseRowShape(reg.model)
		require.NoError(t, err)
		require.Equal(t, reg.PackagePath, parsed.ModelType.PkgPath(),
			"registered package path must agree with the row structure's owning package")
		require.Equal(t, reg.TypeName, parsed.ModelType.Name(),
			"registered type name must agree with the row structure's real type")
		require.Equal(t, reg.TableName, parsed.Table,
			"registered table name must agree with the row structure's TableName()")
		violations = append(violations, compareRowShape(reg, parsed, readShapes(t, reg.TableName), dialect)...)
	}
	return violations
}

// guardedRowShapeEntry returns the checklist entry of one row structure by
// type name — the green twins (SQLite here, PostgreSQL in
// row_shape_guard_postgres_test.go) use it to pin the #694 sample's identity
// on both dialects.
func guardedRowShapeEntry(typeName string) *rowShapeRegistration {
	for i := range guardedRowShapes {
		if guardedRowShapes[i].TypeName == typeName {
			return &guardedRowShapes[i]
		}
	}
	return nil
}

// runRowShapeColumnCoverageAgainst is rule 1's reverse, at table level
// (#713): every column the migration chain builds must be declared by at
// least one registered row structure of the same table. The check unions the
// declared columns across the table's checklist entries — part-projection
// structs are legitimate, so a column is only unmapped when NO structure of
// that table declares it. Such a column is invisible to every forward rule
// and to every gorm reader; it is either a missing declaration or schema
// debt, and either way it deserves a red light instead of silence.
func runRowShapeColumnCoverageAgainst(t *testing.T, dialect string, registrations []rowShapeRegistration, readShapes rowShapeArtifactReader) []rowShapeViolation {
	t.Helper()
	declaredByTable := map[string]map[string]string{}
	for _, reg := range registrations {
		require.NotNil(t, reg.model, "checklist entry "+reg.PackagePath+"."+reg.TypeName+" resolved to no row structure")
		parsed, err := parseRowShape(reg.model)
		require.NoError(t, err)
		if declaredByTable[parsed.Table] == nil {
			declaredByTable[parsed.Table] = map[string]string{}
		}
		for _, field := range parsed.Fields {
			declaredByTable[parsed.Table][field.DBName] = reg.PackagePath + "." + reg.TypeName
		}
	}
	var violations []rowShapeViolation
	seenTables := map[string]bool{}
	for _, reg := range registrations {
		if seenTables[reg.TableName] {
			continue
		}
		seenTables[reg.TableName] = true
		artifact := readShapes(t, reg.TableName)
		declared := declaredByTable[reg.TableName]
		names := make([]string, 0, len(artifact))
		for name := range artifact {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if declared[name] != "" {
				continue
			}
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: name,
				Kind:   rowShapeUnmappedColumn,
				Declared: fmt.Sprintf(
					"no registered row structure declares it (union across %s's %d checklist entries covers %d columns)",
					reg.TableName, countTableRegistrations(registrations, reg.TableName), len(declared),
				),
				Artifact: fmt.Sprintf(
					"decl type %q, built by the real %s migration chain",
					artifact[name].DeclType, dialect,
				),
			})
		}
	}
	return violations
}

func countTableRegistrations(registrations []rowShapeRegistration, table string) int {
	n := 0
	for _, reg := range registrations {
		if reg.TableName == table {
			n++
		}
	}
	return n
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

// postgresColumnTypeFamily is the PostgreSQL tier of rule 3's artifact-side
// normalization (#710): it maps the data_type strings information_schema.columns
// reports for the versioned chain's columns onto the same canonical families
// the declared side uses. information_schema reports exact, unambiguous type
// names (no storage-class affinity to second-guess), so this tier is a plain
// table. A type that lands in neither tier is a violation (extend it), never
// a silent pass.
//
// uuid and jsonb normalize into the string family per #710's adjudication:
// the versioned chain writes them deliberately as the dialect-native spelling
// of text-valued columns — 000134_lingdoc_model_call_ledger.up.sql documents
// the convention in its own header ("类型按既有方言约定改写：id TEXT→UUID、
// tenant_id INTEGER→BIGINT、JSON 文本→JSONB、时间 DATETIME→TIMESTAMPTZ"), the
// structs declare string/text, and gorm writes Go strings through both column
// types. This mirrors #709's bool↔INTEGER ruling: each side is correct on its
// own layer, the mismatch is a false positive of the normalization tier. The
// acceptance stays family-equal, so only string-family declarations pass over
// these columns — an integer or bool declaration cannot hide behind it.
// declaredTypeFamily is rule 3's declared-side lookup, over
// declaredTypeFamilies. Beyond exact hits it strips a parenthesized length
// suffix from `type:`-tag spellings ("varchar(36)" → "varchar") before the
// lookup: #713 brought internal/evidence's ProjectAsset under the guard and
// its tags spell lengths inline, the same family the bare spellings already
// map to. The length itself is rule 4's business (size/character_maximum_
// length), never re-tested here.
func declaredTypeFamily(dataType string) (string, bool) {
	if family, ok := declaredTypeFamilies[dataType]; ok {
		return family, true
	}
	if cut := strings.Index(dataType, "("); cut > 0 {
		family, ok := declaredTypeFamilies[dataType[:cut]]
		return family, ok
	}
	return "", false
}

func postgresColumnTypeFamily(dataType string) (string, bool) {
	switch strings.ToLower(dataType) {
	case "boolean":
		return "bool", true
	case "smallint", "integer", "bigint":
		return "integer", true
	case "real", "double precision", "numeric":
		return "float", true
	case "character varying", "character", "text", "uuid", "jsonb":
		return "string", true
	case "bytea":
		return "blob", true
	case "timestamp with time zone", "timestamp without time zone",
		"time with time zone", "time without time zone", "date":
		return "time", true
	}
	return "", false
}

// artifactTypeFamily selects the dialect's tier of the artifact-side
// normalization (rule 3): the mapping rules themselves live only in the two
// functions above.
func artifactTypeFamily(dialect, declType string) (string, bool) {
	if dialect == dialectPostgreSQL {
		return postgresColumnTypeFamily(declType)
	}
	return sqliteDeclTypeFamily(declType)
}

// typeFamiliesAgree is rule 3's final verdict for the SQLite dialect, kept
// beside the two normalization tables so the comparison semantics keep
// exactly one source.
// SQLite has no separate boolean storage class — booleans are integers (0/1)
// by documented convention — so a declared bool over an INTEGER artifact
// column is the same shape, not a drift (#709's full-registration run hit
// exactly this false positive on the confirmation_valid / valid / is_current
// columns, and the Spec prescribes fixing it by extending the normalization
// rules, never by downgrading the guard). The acceptance is directional: an
// integer declaration over a boolean-intended column stays a mismatch, so a
// drifted type cannot hide behind the equivalence.
func typeFamiliesAgree(declared, artifact string) bool {
	if declared == artifact {
		return true
	}
	return declared == "bool" && artifact == "integer"
}

// typeFamiliesAgreeOnDialect dispatches rule 3's final verdict per dialect.
// SQLite keeps the directional bool↔INTEGER equivalence documented above (no
// boolean storage class); PostgreSQL has a real boolean type, so the twin
// compares the families directly — the equivalence does not carry over, and
// an integer declaration over a boolean column is a mismatch there (#710;
// #709's risk note records this dialect split).
func typeFamiliesAgreeOnDialect(dialect, declared, artifact string) bool {
	if dialect == dialectPostgreSQL {
		return declared == artifact
	}
	return typeFamiliesAgree(declared, artifact)
}

// compareRowShape applies the rules at the top of this file to one parsed row
// structure against one table's artifact columns, on the given dialect.
func compareRowShape(reg rowShapeRegistration, parsed *schema.Schema, artifact map[string]migrationColumnShape, dialect string) []rowShapeViolation {
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
		declaredFamily, declaredKnown := declaredTypeFamily(string(field.DataType))
		artifactFamily, artifactKnown := artifactTypeFamily(dialect, shape.DeclType)
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
					"decl type %q is not covered by the guard's %s normalization rules",
					shape.DeclType, dialect,
				),
			})
			continue
		}
		if !typeFamiliesAgreeOnDialect(dialect, declaredFamily, artifactFamily) {
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

		// Rule 4: length. A declared `size` is compared when the artifact side
		// enforces a length: the SQLite reader leaves Length unset, so the
		// comparison never fires on that dialect (rule 4 above), while the
		// PostgreSQL reader feeds character_maximum_length into it (#713).
		if field.Size > 0 && shape.Length.Valid && int64(field.Size) != shape.Length.Int64 {
			violations = append(violations, rowShapeViolation{
				Table:  reg.TableName,
				Column: field.DBName,
				Kind:   rowShapeLengthMismatch,
				Declared: fmt.Sprintf(
					"size %d (field %s %s, gorm tag %q)",
					field.Size, field.Name, field.FieldType.String(), field.Tag.Get("gorm"),
				),
				Artifact: fmt.Sprintf(
					"length %d (data type %q)",
					shape.Length.Int64, shape.DeclType,
				),
			})
		}
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
		model:       &rowShapeGuardMissingColumnFixture{},
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
		model:       &rowShapeGuardNotNullOnNullableFixture{},
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
		model:       &rowShapeGuardPointerOnNotNullFixture{},
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
		model:       &rowShapeGuardTypeMismatchFixture{},
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
		model:       &rowShapeGuardUnknownTypeFixture{},
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

// rowShapeGuardSQLiteBooleanFixture pins rule 3's documented equivalence on
// the real artifact: a declared bool over an INTEGER column is SQLite's
// boolean storage (0/1), not a drift — #709's full-registration run hit this
// false positive on the boolean columns and the Spec prescribes extending
// the normalization rules for it.
type rowShapeGuardSQLiteBooleanFixture struct {
	IsCurrent bool `gorm:"column:is_current;not null"`
}

func (rowShapeGuardSQLiteBooleanFixture) TableName() string { return "lingdoc_release_snapshots" }

func TestRowShapeGuardTreatsADeclaredBoolAsSQLiteIntegerStorage(t *testing.T) {
	// The directionality ratchet: only a declared bool accepts an INTEGER
	// artifact column; no other cross-family pair may hide behind the
	// equivalence.
	require.True(t, typeFamiliesAgree("bool", "integer"))
	require.False(t, typeFamiliesAgree("integer", "bool"))
	require.False(t, typeFamiliesAgree("string", "integer"))
	require.False(t, typeFamiliesAgree("bool", "string"))

	db := migratedSQLiteChainForShapeGuard(t)
	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "rowShapeGuardSQLiteBooleanFixture",
		TableName:   "lingdoc_release_snapshots",
		model:       &rowShapeGuardSQLiteBooleanFixture{},
	}}
	violations := runRowShapeGuard(t, db, registrations)
	require.Empty(t, violations,
		"a declared bool over SQLite's INTEGER boolean storage must not be a violation; got %v", violations)
}

// TestGuardedRowShapesMatchTheSQLiteMigrationChain is the green sample: the
// #694 adoption idempotency row, parsed by gorm itself, against the real
// migration chain's product (000056 made chapter_version_id nullable) — and,
// since #709, every other registered row structure in the domain. The
// precondition assertions keep this from passing vacuously — they prove the
// pointer/nullable direction of rule 2 really executed on the real row, and
// that the #708 green sample survived the checklist's growth intact.
func TestGuardedRowShapesMatchTheSQLiteMigrationChain(t *testing.T) {
	// The #708 green sample stays pinned by identity: the adoption
	// idempotency row must remain registered on its real table.
	entry := guardedRowShapeEntry("adoptionIdempotencyRow")
	require.NotNil(t, entry, "the #694 green sample must stay on the checklist")
	require.Equal(t, "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption", entry.PackagePath)
	require.Equal(t, "lingdoc_candidate_adoptions", entry.TableName)

	parsed, err := parseRowShape(entry.model)
	require.NoError(t, err)
	field, ok := parsed.FieldsByDBName["chapter_version_id"]
	require.True(t, ok, "the adoption idempotency row must declare chapter_version_id")
	require.Equal(t, reflect.Pointer, field.FieldType.Kind(),
		"the #694 fix made the field a pointer (*string)")
	require.False(t, field.NotNull, "the green sample's pointer must not carry a not null tag")

	db := migratedSQLiteChainForShapeGuard(t)
	violations := runRowShapeGuard(t, db, guardedRowShapes)
	require.Empty(t, violations,
		"every registered row structure and the real migration chain must agree on every guarded column")
}

// TestGuardedTablesCoverEverySQLiteMigrationColumn is rule 1's reverse on the
// SQLite dialect (#713): every column the real migration chain builds must be
// declared by at least one registered row structure of the same table —
// part-projection structs union their columns, and only a column no structure
// declares is a violation.
func TestGuardedTablesCoverEverySQLiteMigrationColumn(t *testing.T) {
	db := migratedSQLiteChainForShapeGuard(t)
	violations := runRowShapeColumnCoverageAgainst(t, dialectSQLite, guardedRowShapes, func(t *testing.T, table string) map[string]migrationColumnShape {
		return migrationTableShapes(t, db, table)
	})
	require.Empty(t, violations,
		"every column the SQLite migration chain builds must be declared by at least one registered row structure")
}
