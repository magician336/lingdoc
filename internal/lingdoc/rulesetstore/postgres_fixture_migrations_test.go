package rulesetstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
)

// TestPostgresFixtureCoversEveryMigrationThatTouchesItsTables is the guard
// issue #386 asks for.
//
// The PostgreSQL fixture used to apply a hand-copied list of five migration
// files. Nothing tied that list to the tables the test uses, so the next
// migration that added a column to one of them would have left the fixture
// building a schema the server no longer has while the test stayed green.
func TestPostgresFixtureCoversEveryMigrationThatTouchesItsTables(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "migrations", "versioned")
	if err := fixturemigrations.Verify(dir, "internal/lingdoc/rulesetstore PostgreSQL fixture",
		postgresFixtureTables, postgresFixtureStubs); err != nil {
		t.Fatal(err)
	}
}

// TestPostgresFixturePinsNoMigrationVersion keeps the hand-copied list from
// coming back: this file must not name a migration file at all.
func TestPostgresFixturePinsNoMigrationVersion(t *testing.T) {
	raw, err := os.ReadFile("store_postgres_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if body := string(raw); strings.Contains(body, ".up.sql") || strings.Contains(body, ".down.sql") {
		t.Fatal("store_postgres_test.go names a migration file again; select by table name instead (issue #386)")
	}
}
