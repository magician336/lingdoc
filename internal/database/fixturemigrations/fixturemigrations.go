// Package fixturemigrations derives the production migration subset a
// PostgreSQL test fixture needs from an explicit declaration of the tables the
// fixture uses.
//
// It exists because two fixtures used to carry hand-copied lists of migration
// version numbers (issue #386). Version numbers are a shared sequence: the next
// migration that added a column to one of those tables was silently absent from
// the list, the fixture kept building a schema nobody else had, and no test went
// red. The declaration is now by table name and the version numbers come from
// the directory, so a migration that touches a declared table is always
// discovered, and one that touches a fixture-built stub table is reported
// instead of quietly drifting.
//
// The two fixtures keep their platform stubs (tenants, users, audit_logs, ...)
// because the LingDoc chain only references those tables; it never creates them.
// Selecting only the *_lingdoc_*.up.sql files that touch the declared tables is
// what keeps the fixtures from colliding with those stubs.
package fixturemigrations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// identPart matches one SQL identifier, quoted or bare.
const identPart = `(?:"[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`

// qualified matches an optionally schema-qualified identifier and captures the
// table name itself (the last component).
const qualified = `(?:(?:` + identPart + `)\s*\.\s*)?(` + identPart + `)`

// shapePatterns match the statements that define a table's shape. Matching is
// statement-level on purpose: REFERENCES clauses are excluded so that a
// migration which merely points a new table at lingdoc_projects does not drag
// the whole chain into every fixture.
var shapePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\bcreate\s+(?:or\s+replace\s+)?(?:unique\s+)?table\s+(?:if\s+not\s+exists\s+)?` + qualified),
	regexp.MustCompile(`(?is)\balter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?` + qualified),
	regexp.MustCompile(`(?is)\bcreate\s+(?:unique\s+)?index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?(?:` + identPart + `)\s+on\s+(?:only\s+)?` + qualified),
	regexp.MustCompile(`(?is)\bdrop\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?` + qualified),
	regexp.MustCompile(`(?is)\btruncate\s+(?:table\s+)?(?:only\s+)?` + qualified),
	regexp.MustCompile(`(?is)\bcomment\s+on\s+table\s+` + qualified),
	regexp.MustCompile(`(?is)\bcomment\s+on\s+column\s+(?:(?:` + identPart + `)\s*\.\s*)?(` + identPart + `)\s*\.`),
	regexp.MustCompile(`(?is)\bcreate\s+(?:or\s+replace\s+)?(?:constraint\s+)?trigger\s+(?:` + identPart + `)\s+[^;]*?\bon\s+` + qualified),
}

// writePatterns match statements that change a table's contents or privileges.
// A fixture that reads a table needs its production shape, and a data migration
// on that table is part of that shape.
var writePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\binsert\s+into\s+` + qualified),
	regexp.MustCompile(`(?is)\bupdate\s+(?:only\s+)?` + qualified + `\s+set\b`),
	regexp.MustCompile(`(?is)\bdelete\s+from\s+(?:only\s+)?` + qualified),
	regexp.MustCompile(`(?is)\bgrant\s+[^;]*?\bon\s+(?:table\s+)?` + qualified),
	regexp.MustCompile(`(?is)\brevoke\s+[^;]*?\bon\s+(?:table\s+)?` + qualified),
}

var (
	lineCommentPattern  = regexp.MustCompile(`(?m)--[^\n]*$`)
	blockCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// Selection is the migration subset a fixture applies, in directory order.
type Selection struct {
	// Names are the migration file names, sorted.
	Names []string
}

// Select returns the *_lingdoc_*.up.sql migrations under dir that shape or write
// any of tables. It returns an error when nothing matches: an empty selection
// means the declaration no longer describes anything production builds, and a
// fixture that applies nothing would pass for the wrong reason.
func Select(dir string, tables []string) (Selection, error) {
	declared := setOf(tables)
	matches, err := filepath.Glob(filepath.Join(dir, "*_lingdoc_*.up.sql"))
	if err != nil {
		return Selection{}, fmt.Errorf("discover LingDoc migrations in %s: %w", dir, err)
	}
	sort.Strings(matches)
	var selection Selection
	for _, file := range matches {
		body, err := readSQL(file)
		if err != nil {
			return Selection{}, err
		}
		if !touchesAny(declared, touchedTables(body)) {
			continue
		}
		selection.Names = append(selection.Names, filepath.Base(file))
	}
	if len(selection.Names) == 0 {
		return Selection{}, fmt.Errorf("no migration under %s shapes the declared tables %v", dir, tables)
	}
	return selection, nil
}

// SQL returns the statements of the selected migrations, in order, together
// with the file name each statement came from.
func (s Selection) SQL(dir string) ([]Statement, error) {
	statements := make([]Statement, 0, len(s.Names))
	for _, name := range s.Names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		statements = append(statements, Statement{Name: name, SQL: string(raw)})
	}
	return statements, nil
}

// Statement is one migration file's SQL.
type Statement struct {
	Name string
	SQL  string
}

// Verify is the guard. It fails when:
//
//   - a migration touches a declared table but the fixture does not apply it;
//   - a migration touches one of the fixture's platform stub tables, which means
//     the stub is now behind production;
//   - a declared table is neither created by the selected migrations nor listed
//     as a fixture stub.
//
// The first two checks scan every *.up.sql under dir, not only the LingDoc
// chain, so a migration that does not follow the *_lingdoc_* naming convention
// is still caught.
func Verify(dir, fixture string, tables, stubs []string) error {
	declaredNames := normalize(tables)
	declared := setOf(tables)
	stub := setOf(stubs)
	selection, err := Select(dir, tables)
	if err != nil {
		return fmt.Errorf("%s: %w", fixture, err)
	}
	selected := make(map[string]bool, len(selection.Names))
	for _, name := range selection.Names {
		selected[name] = true
	}

	created := map[string]bool{}
	for _, name := range selection.Names {
		body, err := readSQL(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		for _, table := range createdTables(body) {
			created[table] = true
		}
	}
	for _, table := range declaredNames {
		if !created[table] && !stub[table] {
			return fmt.Errorf("%s: declared table %s is neither created by the %d selected migrations nor listed as a fixture stub",
				fixture, table, len(selection.Names))
		}
	}

	all, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return fmt.Errorf("scan %s: %w", dir, err)
	}
	sort.Strings(all)
	for _, file := range all {
		name := filepath.Base(file)
		if selected[name] {
			continue
		}
		body, err := readSQL(file)
		if err != nil {
			return err
		}
		// A REFERENCES clause is a foreign key into the table, not a change to
		// its shape, and a read-only join in a backfill does not change it
		// either: touchedTables only reports shape and write statements.
		touched := touchedTables(body)
		for _, table := range touched {
			if stub[table] && !strings.Contains(name, "_lingdoc_") {
				// Platform migrations legitimately create the stub tables; only
				// the LingDoc chain must stay out of them.
				continue
			}
			if declared[table] || stub[table] {
				return fmt.Errorf("%s: migration %s touches table %s but the fixture does not apply it; declare the table or extend the fixture (issue #386)",
					fixture, name, table)
			}
		}
	}
	return nil
}

// touchedTables returns every table that body shapes or writes.
func touchedTables(body string) []string {
	cleaned := stripComments(body)
	set := map[string]bool{}
	for _, pattern := range append(append([]*regexp.Regexp{}, shapePatterns...), writePatterns...) {
		for _, match := range pattern.FindAllStringSubmatch(cleaned, -1) {
			if len(match) < 2 {
				continue
			}
			if table := cleanIdentifier(match[1]); table != "" {
				set[table] = true
			}
		}
	}
	tables := make([]string, 0, len(set))
	for table := range set {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

// createdTables returns the tables body creates.
func createdTables(body string) []string {
	pattern := regexp.MustCompile(`(?is)\bcreate\s+(?:unique\s+)?table\s+(?:if\s+not\s+exists\s+)?` + qualified)
	cleaned := stripComments(body)
	set := map[string]bool{}
	for _, match := range pattern.FindAllStringSubmatch(cleaned, -1) {
		if table := cleanIdentifier(match[1]); table != "" {
			set[table] = true
		}
	}
	tables := make([]string, 0, len(set))
	for table := range set {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func readSQL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read migration %s: %w", filepath.Base(path), err)
	}
	return string(raw), nil
}

func stripComments(body string) string {
	return blockCommentPattern.ReplaceAllString(lineCommentPattern.ReplaceAllString(body, ""), "")
}

func cleanIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	return strings.ToLower(value)
}

func normalize(tables []string) []string {
	seen := make(map[string]bool, len(tables))
	normalized := make([]string, 0, len(tables))
	for _, table := range tables {
		table = cleanIdentifier(table)
		if table == "" || seen[table] {
			continue
		}
		seen[table] = true
		normalized = append(normalized, table)
	}
	sort.Strings(normalized)
	return normalized
}

func touchesAny(declared map[string]bool, tables []string) bool {
	for _, table := range tables {
		if declared[table] {
			return true
		}
	}
	return false
}

func setOf(tables []string) map[string]bool {
	set := make(map[string]bool, len(tables))
	for _, table := range normalize(tables) {
		set[table] = true
	}
	return set
}
