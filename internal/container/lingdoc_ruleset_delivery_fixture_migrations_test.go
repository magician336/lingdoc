package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
)

// TestRuleSetPostgresFixtureCoversEveryMigrationThatTouchesItsTables is the
// guard issue #386 asks for.
//
// The fixture used to carry a hand-copied list of 22 migration version numbers
// that stopped at 000121. A later migration that changed one of the tables the
// scenario uses was simply not in the list, so the fixture kept building its own
// older schema and every test stayed green. Now the fixture declares tables, the
// version numbers come from migrations/versioned, and this test fails when a
// migration touches a declared table (or one of the fixture's platform stubs)
// without the fixture applying it.
//
// It needs no PostgreSQL instance, so it runs in the normal unit test matrix.
func TestRuleSetPostgresFixtureCoversEveryMigrationThatTouchesItsTables(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations", "versioned")
	if err := fixturemigrations.Verify(dir, "internal/container rule-set delivery PostgreSQL fixture",
		ruleSetPostgresFixtureTables, ruleSetPostgresFixtureStubs); err != nil {
		t.Fatal(err)
	}
}

// TestRuleSetPostgresFixturePinsNoMigrationVersion keeps the hand-copied list
// from coming back. The PostgreSQL half of the fixture must name no migration
// file at all: a version number written down here is the defect, not the fix.
func TestRuleSetPostgresFixturePinsNoMigrationVersion(t *testing.T) {
	body := functionBody(t, "lingdoc_ruleset_delivery_e2e_test.go", "applyRuleSetPostgresSchema")
	if strings.Contains(body, ".up.sql") || strings.Contains(body, ".down.sql") {
		t.Fatalf("applyRuleSetPostgresSchema names a migration file again; select by table name instead (issue #386):\n%s", body)
	}
}

func functionBody(t *testing.T, file, function string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, "func "+function+"(")
	if start < 0 {
		t.Fatalf("%s does not define %s", file, function)
	}
	end := strings.Index(source[start+1:], "\nfunc ")
	if end < 0 {
		return source[start:]
	}
	return source[start : start+1+end]
}
