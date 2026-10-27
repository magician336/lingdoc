package fixturemigrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMigrations(t *testing.T, files map[string]string) string {
	t.Helper()
	// The version prefixes are deliberately not six digits: the repository-wide
	// guard test_lingdoc_fixture_schema_source.py reads any <6 digits>_name.up.sql
	// in a Go test file as a named production migration and fails when it does
	// not exist. These files live in a temporary directory, not in the chain.
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSelectFindsMigrationsByTableNotByVersionNumber(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_unrelated.up.sql":       "CREATE TABLE lingdoc_elsewhere (id varchar(36) PRIMARY KEY);\n",
		"101_lingdoc_rule_sets.up.sql":       "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		"102_lingdoc_rule_sets_ext.up.sql":   "ALTER TABLE lingdoc_rule_sets ADD COLUMN ruleset_hash varchar(64) NOT NULL DEFAULT '';\n",
		"102_lingdoc_rule_sets_ext.down.sql": "ALTER TABLE lingdoc_rule_sets DROP COLUMN ruleset_hash;\n",
	})
	selection, err := Select(dir, []string{"lingdoc_rule_sets"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"101_lingdoc_rule_sets.up.sql", "102_lingdoc_rule_sets_ext.up.sql"}
	if strings.Join(selection.Names, ",") != strings.Join(want, ",") {
		t.Fatalf("selection = %v, want %v", selection.Names, want)
	}

	// Renumbering a migration must not change the selection: the fixture never
	// writes a version number down (issue #386).
	renamed := writeMigrations(t, map[string]string{
		"200_lingdoc_rule_sets.up.sql":     "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		"201_lingdoc_rule_sets_ext.up.sql": "ALTER TABLE lingdoc_rule_sets ADD COLUMN ruleset_hash varchar(64) NOT NULL DEFAULT '';\n",
	})
	renumbered, err := Select(renamed, []string{"lingdoc_rule_sets"})
	if err != nil {
		t.Fatal(err)
	}
	if len(renumbered.Names) != len(want) {
		t.Fatalf("renumbered selection = %v, want %d migrations", renumbered.Names, len(want))
	}
}

func TestSelectIgnoresForeignKeyReferences(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql": "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		"101_lingdoc_other.up.sql":     "CREATE TABLE lingdoc_other (project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE);\n",
	})
	selection, err := Select(dir, []string{"lingdoc_rule_sets", "lingdoc_projects"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range selection.Names {
		if strings.Contains(name, "000011") {
			t.Fatalf("a REFERENCES clause must not pull a migration in: %v", selection.Names)
		}
	}
}

func TestSelectCoversQualifiedNamesAndWrites(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_qualified.up.sql": "ALTER TABLE ONLY public.lingdoc_rule_sets ADD COLUMN probe text;\n",
		"101_lingdoc_write.up.sql":     "UPDATE lingdoc_rule_sets SET probe = 'x' WHERE probe IS NULL;\n",
		"102_lingdoc_insert.up.sql":    "INSERT INTO lingdoc_rule_sets (id) VALUES ('x');\n",
	})
	selection, err := Select(dir, []string{"lingdoc_rule_sets"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Names) != 3 {
		t.Fatalf("selection = %v, want all three statement forms", selection.Names)
	}
}

func TestSelectRefusesAnEmptySelection(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql": "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
	})
	if _, err := Select(dir, []string{"lingdoc_missing"}); err == nil {
		t.Fatal("an empty selection must fail: a fixture that applies nothing would pass for the wrong reason")
	}
}

func TestVerifyPassesWhenEveryMigrationTouchingTheTablesIsSelected(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql":     "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		"101_lingdoc_rule_sets_ext.up.sql": "ALTER TABLE lingdoc_rule_sets ADD COLUMN ruleset_hash varchar(64) NOT NULL DEFAULT '';\n",
		"001_base.up.sql":                  "CREATE TABLE tenants (id bigint PRIMARY KEY);\n",
	})
	if err := Verify(dir, "synthetic", []string{"lingdoc_rule_sets"}, []string{"tenants"}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// The guard has to go red when a new migration changes a table the fixture
// uses but the fixture does not apply it. This is the mutation the issue asks
// for: before the fix the fixture simply kept its old schema and stayed green.
func TestVerifyCatchesMigrationOutsideTheFixtureNameConvention(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql": "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		// Not named *_lingdoc_*, so Select never sees it, but the production
		// migration chain still applies it.
		"101_ruleset_probe.up.sql": "ALTER TABLE lingdoc_rule_sets ADD COLUMN probe text;\n",
	})
	err := Verify(dir, "synthetic", []string{"lingdoc_rule_sets"}, []string{"tenants"})
	if err == nil {
		t.Fatal("verify must fail when a migration outside the LingDoc naming convention touches a declared table")
	}
	if !strings.Contains(err.Error(), "101_ruleset_probe.up.sql") {
		t.Fatalf("error must name the offending migration: %v", err)
	}
}

func TestVerifyCatchesMigrationThatTouchesAFixtureStub(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql": "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
		// The fixture owns audit_logs as a stub; a LingDoc migration that
		// changes it means the stub is now behind production.
		"101_lingdoc_audit_probe.up.sql": "ALTER TABLE audit_logs ADD COLUMN probe text;\n",
	})
	err := Verify(dir, "synthetic", []string{"lingdoc_rule_sets"}, []string{"audit_logs"})
	if err == nil {
		t.Fatal("verify must fail when a LingDoc migration touches a fixture stub table")
	}
	if !strings.Contains(err.Error(), "audit_logs") {
		t.Fatalf("error must name the stub table: %v", err)
	}
}

func TestVerifyCatchesDeclaredTableNobodyBuilds(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"100_lingdoc_rule_sets.up.sql": "CREATE TABLE lingdoc_rule_sets (id varchar(64) PRIMARY KEY);\n",
	})
	err := Verify(dir, "synthetic", []string{"lingdoc_rule_sets", "lingdoc_typo"}, []string{"tenants"})
	if err == nil {
		t.Fatal("verify must fail when a declared table is neither migrated nor a stub")
	}
	if !strings.Contains(err.Error(), "lingdoc_typo") {
		t.Fatalf("error must name the stale declaration: %v", err)
	}
}
