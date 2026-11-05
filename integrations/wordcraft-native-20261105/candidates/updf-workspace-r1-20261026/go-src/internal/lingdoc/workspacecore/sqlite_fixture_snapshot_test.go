package workspacecore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "modernc.org/sqlite" // Pure Go test driver; production retains the existing SQLite driver.
)

// A package-wide SQLite fixture has to replay all 25 production LingDoc
// migrations. Doing that once per test is what pushed workspacecore past the
// CI race detector's five-minute limit, so the chain is replayed once per
// package and every test receives a private copy of the result instead.
//
// Why this is not "just cache the schema"
// --------------------------------------
// The cost was measured, not guessed. Of 295 tests summing to 59s, no single
// test exceeded 5.3s and the slowest ten accounted for 28% - there is no slow
// test to optimise. The cost was 295 times a fixed ~128ms of setup, of which
// replaying the migrations was 117ms. Under -race that setup is amplified
// enough to exceed the limit even though the assertions themselves are fast.
//
// Why VACUUM INTO rather than a file copy
// --------------------------------------
// A plain copy of the seed file is a 4096-byte empty database: WAL has not been
// checkpointed, so the schema is still in the -wal sidecar. Every test then
// replayed all 25 migrations anyway and the copy appeared to save nothing.
// VACUUM INTO writes a self-contained snapshot, after which an ordinary byte
// copy is correct.
var (
	migratedSnapshotOnce sync.Once
	migratedSnapshotPath string
	migratedSnapshotErr  error
)

// openSilentSQLite opens a fixture database without gorm's query logging. The
// migration replay issues hundreds of statements and logging them dominates the
// fixture cost.
func openSilentSQLite(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Dialector{DriverName: "sqlite",
		DSN: path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// migratedSQLiteSnapshot returns the path of a self-contained SQLite file that
// already carries the full production migration chain, building it on first
// use. The file is owned by the package, not by any test.
func migratedSQLiteSnapshot(tb testing.TB) string {
	tb.Helper()
	migratedSnapshotOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lingdoc-workspacecore-migrated-")
		if err != nil {
			migratedSnapshotErr = fmt.Errorf("create snapshot directory: %w", err)
			return
		}
		seed := filepath.Join(dir, "seed.db")
		snapshot := filepath.Join(dir, "snapshot.db")

		db, openErr := openSilentSQLite(seed)
		if openErr != nil {
			migratedSnapshotErr = fmt.Errorf("open seed database: %w", openErr)
			return
		}
		defer func() {
			if sqlDB, dbErr := db.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		}()

		for _, name := range productionLingDocMigrationNames(tb, "sqlite") {
			body, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
			if readErr != nil {
				migratedSnapshotErr = fmt.Errorf("read production SQLite migration %s: %w", name, readErr)
				return
			}
			if execErr := db.Exec(string(body)).Error; execErr != nil {
				migratedSnapshotErr = fmt.Errorf("apply production SQLite migration %s: %w", name, execErr)
				return
			}
		}
		if execErr := db.Exec(tenantMembersFixtureDDL).Error; execErr != nil {
			migratedSnapshotErr = fmt.Errorf("create tenant_members fixture: %w", execErr)
			return
		}
		if execErr := db.Exec("VACUUM INTO ?", snapshot).Error; execErr != nil {
			migratedSnapshotErr = fmt.Errorf("snapshot migrated database: %w", execErr)
			return
		}
		migratedSnapshotPath = snapshot
	})
	if migratedSnapshotErr != nil {
		tb.Fatalf("build migrated SQLite snapshot: %v", migratedSnapshotErr)
	}
	return migratedSnapshotPath
}

// seedSQLiteFromSnapshot gives the caller its own writable copy of the
// migrated fixture. Copies are independent files, so tests stay isolated from
// each other exactly as they were when each one replayed the chain.
//
// An existing file is left alone. Reopen-style assertions close the store and
// call testStore again on the SAME path precisely to prove that what was
// persisted survives a restart; overwriting that path with a fresh snapshot
// would silently discard it and turn a persistence check into a vacuous one.
func seedSQLiteFromSnapshot(tb testing.TB, path string) error {
	tb.Helper()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing test database %s: %w", path, err)
	}
	body, err := os.ReadFile(migratedSQLiteSnapshot(tb))
	if err != nil {
		return fmt.Errorf("read migrated snapshot: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write test database %s: %w", path, err)
	}
	return nil
}

const tenantMembersFixtureDDL = "CREATE TABLE IF NOT EXISTS tenant_members ( role TEXT NOT NULL DEFAULT 'owner',tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, status TEXT NOT NULL, deleted_at DATETIME, PRIMARY KEY (tenant_id, user_id))"
