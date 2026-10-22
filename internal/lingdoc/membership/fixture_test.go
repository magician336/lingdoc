package membership

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "modernc.org/sqlite" // Pure Go test driver; production retains the existing SQLite driver.
)

// The member and owner context left the aggregate (Spec #47, ticket #268).
// Its tests therefore live here, next to the implementation, and they need two
// objects: the aggregate, which still owns the project, the spec and the
// chapters, and the member and owner service, which owns the members and the
// owner.
//
// The two share one database, so a member write the aggregate reads back is the
// same row. This file builds that pair, and the helpers the migrated tests
// used to borrow from the aggregate's own fixture.

// testWorkspace is the pair a member and owner test needs.
type testWorkspace struct {
	// t is the test that owns this workspace.
	t *testing.T
	// svc is the workspace aggregate: it owns the project, the spec, the
	// chapters and the audits.
	svc *core.Service
	// member is the member and owner service under test.
	member *Service
	// db is the handle the tests use to read and write rows directly, the way
	// the aggregate's own fixture did through its unexported testDB.
	db *gorm.DB
}

// newTestWorkspace opens an isolated SQLite workspace and assembles the
// aggregate and the member and owner service over it.
func newTestWorkspace(t *testing.T, path string) *testWorkspace {
	t.Helper()
	svc, repository, db := testStore(t, path)
	return &testWorkspace{t: t, svc: svc, member: newServiceForTest(t, svc, repository), db: db}
}

// setSources replaces the aggregate's source review with another policy.
//
// The source policy is a constructor argument of the aggregate, not a field a
// caller may reach, so the aggregate is rebuilt over the same database. The
// machine-read fixture needs this: one of its cases must run without a source
// bridge at all.
func (w *testWorkspace) setSources(policy core.SourcePolicy) {
	w.t.Helper()
	repository := core.NewGORMRepository(w.db)
	w.svc = core.NewServiceWithSources(repository, core.ContractDemoTemplate{}, policy)
	w.member = newServiceForTest(w.t, w.svc, repository)
}

// reopenTestWorkspace reopens the workspace over the same database file.
//
// The file is left alone, so what the previous run persisted survives the
// restart: that is what makes a persistence assertion mean something.
func reopenTestWorkspace(t *testing.T, w *testWorkspace, path string) *testWorkspace {
	t.Helper()
	sqlDB, err := w.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close original SQLite connection: %v", err)
	}
	return newTestWorkspace(t, path)
}

// newServiceForTest assembles the member and owner service over the
// workspace's ports.
//
// The ports are the ones the composition root builds (internal/container): the
// operation pipeline that runs the writes, the authorizer that judges the
// reads, the workspace's own operating policy, the repository that opens the
// transaction, and the one storage value that answers every record question
// the member context asks.
func newServiceForTest(t *testing.T, svc *core.Service, repository core.Repository) *Service {
	t.Helper()
	storage := core.NewMemberStorage(svc)
	return NewService(Ports{
		Runner:     core.NewOperationRunner(svc),
		Authorizer: core.NewWorkspaceAuthorizer(svc),
		Policy:     core.NewWorkspacePolicy(svc),
		Repository: repository,
		Records:    storage,
		Directory:  storage,
		Transfers:  storage,
		APIKeys:    storage,
		Resources:  storage,
	})
}

// testStore opens a fixture database carrying the production migration chain
// and returns the aggregate, the repository and the database handle.
//
// The chain is replayed once per package into a snapshot and every test
// receives a private copy of it, for the reason the aggregate's own fixture
// gives: replaying 25 migrations per test dominated the package's runtime.
func testStore(t *testing.T, path string) (*core.Service, core.Repository, *gorm.DB) {
	t.Helper()
	if err := seedSQLiteFromSnapshot(t, path); err != nil {
		t.Fatal(err)
	}
	db, err := openSilentSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(tenantMembersFixtureDDL).Error; err != nil {
		t.Fatal(err)
	}
	repository := core.NewGORMRepository(db)
	svc := core.NewServiceWithSources(repository, core.ContractDemoTemplate{}, &fakeSources{})
	return svc, repository, db
}

// fakeSources is the aggregate's own source-review stand-in: it allows by
// default. The member and owner tests do not exercise the source review; they
// need the aggregate to behave as it does in the aggregate's own fixture.
type fakeSources struct{}

func (f *fakeSources) Validate(context.Context, string, string, []string) error { return nil }

// seedTenantMember writes one active tenant member.
//
// It is a copy, not a move: the aggregate's fixture keeps its own, because
// the aggregate's other tests still need it.
func seedTenantMember(t *testing.T, db *gorm.DB, actor core.Actor) {
	t.Helper()
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status, role) VALUES (?, ?, 'active', ?)",
		actor.TenantID, actor.UserID, seededTenantRole(actor)).Error; err != nil {
		t.Fatal(err)
	}
}

// seededTenantRole is the tenant role a seeded member carries.
func seededTenantRole(actor core.Actor) string {
	if actor.Role != "" {
		return string(actor.Role)
	}
	if actor.TenantRole != "" {
		return actor.TenantRole
	}
	return "owner"
}

// asProject decodes one project out of an operation's answer.
func asProject(t *testing.T, raw json.RawMessage) core.Project {
	t.Helper()
	var p core.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// seedActiveImpactProject creates an active project with its spec, the way the
// aggregate's own fixture did.
func seedActiveImpactProject(t *testing.T, svc *core.Service, db *gorm.DB, actor core.Actor) core.Project {
	t.Helper()
	seedTenantMember(t, db, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "change-create",
		core.CreateProjectInput{Name: "正式课题", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(context.Background(), actor, project.ID, "change-spec-1", core.SaveSpecInput{
		ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "旧主题", "research_goal": "旧目标"},
	})
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.ActivateProject(context.Background(), actor, project.ID, "change-activate",
		core.ActivateProjectInput{
			ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion,
			ReviewedProjectVersion: project.ProjectVersion,
		})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	return asProject(t, raw)
}

// rawOwnerTransfer decodes one owner transfer out of an operation's answer.
func rawOwnerTransfer(t *testing.T, raw []byte) core.OwnerTransfer {
	t.Helper()
	var transfer core.OwnerTransfer
	if err := json.Unmarshal(raw, &transfer); err != nil {
		t.Fatal(err)
	}
	return transfer
}

// installAuditFailureTrigger makes the business audit of one action fail, so
// that the transaction that writes it has to roll back.
func installAuditFailureTrigger(t *testing.T, db *gorm.DB, action string) {
	t.Helper()
	trigger := "fail_lingdoc_audit_" + strings.ReplaceAll(action, ".", "_")
	statement := "CREATE TRIGGER " + trigger + " BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = '" +
		action + "' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END"
	if err := db.Exec(statement).Error; err != nil {
		t.Fatalf("install audit trigger: %v", err)
	}
}

// auditSinkStub collects the audit events the workspace records outside the
// caller's transaction.
type auditSinkStub struct{ events []core.AuditEvent }

func (s *auditSinkStub) Record(_ context.Context, event core.AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

// decodeAgentJSON decodes one operation's answer into the type the test expects.
func decodeAgentJSON[T any](t *testing.T, raw json.RawMessage, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// auditEventRow and ownerTransferRow mirror the workspace's own row shapes.
//
// The member and owner tests read those rows directly to prove what the
// transaction did, and did not, persist.
type auditEventRow struct {
	ID          string `gorm:"primaryKey;size:36"`
	ProjectID   string `gorm:"not null;index;size:36"`
	ActorID     string `gorm:"not null;size:64"`
	Action      string `gorm:"not null;size:64"`
	Target      string `gorm:"not null;size:128"`
	DetailsJSON string `gorm:"column:details_json;not null;type:text;default:'{}'"`
	CreatedAt   time.Time
}

func (auditEventRow) TableName() string { return "lingdoc_project_audits" }

type ownerTransferRow struct {
	ID                     string `gorm:"primaryKey;size:36"`
	ProjectID              string `gorm:"not null;index;size:36"`
	FromUserID             string `gorm:"not null;size:64"`
	ToUserID               string `gorm:"not null;size:64"`
	Status                 string `gorm:"not null;size:16"`
	ExpectedProjectVersion int64  `gorm:"not null"`
	CreatedAt              time.Time
	AcceptedAt             *time.Time
}

func (ownerTransferRow) TableName() string { return "lingdoc_owner_transfers" }

// The SQLite fixture
// ------------------
//
// A package-wide SQLite fixture has to replay all the production LingDoc
// migrations. Doing that once per test is what pushed the aggregate's own
// package past the CI race detector's five-minute limit, so the chain is
// replayed once per package and every test receives a private copy of the
// result instead.
//
// Why VACUUM INTO rather than a file copy
// --------------------------------------
//
// A plain copy of the seed file is an empty database: WAL has not been
// checkpointed, so the schema is still in the -wal sidecar. VACUUM INTO writes
// a self-contained snapshot, after which an ordinary byte copy is correct.
var (
	migratedSnapshotOnce sync.Once
	migratedSnapshotPath string
	migratedSnapshotErr  error
)

// openSilentSQLite opens a fixture database without gorm's query logging. The
// migration replay issues hundreds of statements and logging them dominates the
// fixture cost.
func openSilentSQLite(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Dialector{
		DriverName: "sqlite",
		DSN:        path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)",
	},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// migratedSQLiteSnapshot returns the path of a self-contained SQLite file that
// already carries the full production migration chain, building it on first
// use. The file is owned by the package, not by any test.
func migratedSQLiteSnapshot(tb testing.TB) string {
	tb.Helper()
	migratedSnapshotOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lingdoc-membership-migrated-")
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

// productionLingDocMigrationNames discovers the production LingDoc migration
// chain, so the fixture never drifts from the schema the repository expects.
func productionLingDocMigrationNames(tb testing.TB, flavor string) []string {
	tb.Helper()
	if flavor != "sqlite" && flavor != "versioned" {
		tb.Fatalf("unsupported migration fixture flavor %q", flavor)
	}
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", flavor, "*_lingdoc_*.up.sql"))
	if err != nil || len(matches) == 0 {
		tb.Fatalf("find %s production migrations: count=%d error=%v", flavor, len(matches), err)
	}
	sort.Strings(matches)
	names := make([]string, len(matches))
	for i, file := range matches {
		names[i] = filepath.Base(file)
	}
	tb.Logf("%s fixture applies %d production migrations through %s", flavor, len(names), names[len(names)-1])
	return names
}

const tenantMembersFixtureDDL = "CREATE TABLE IF NOT EXISTS tenant_members ( role TEXT NOT NULL DEFAULT 'owner'," +
	"tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, status TEXT NOT NULL, deleted_at DATETIME," +
	" PRIMARY KEY (tenant_id, user_id))"
