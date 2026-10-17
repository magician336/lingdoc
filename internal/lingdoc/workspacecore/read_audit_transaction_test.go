package workspacecore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type auditedReadRepository struct {
	Repository
	options []TransactionOptions
}

func (r *auditedReadRepository) Transaction(ctx context.Context, options TransactionOptions, fn func(Transaction) error) error {
	r.options = append(r.options, options)
	return r.Repository.Transaction(ctx, options, fn)
}

// Real SQLite persistence verifies the non-nil production audit path. This
// test does not substitute for PostgreSQL's enforcement of read-only writes.
func TestAuditedProjectReadsPersistAndStillRejectRevokedMembers(t *testing.T) {
	ctx := context.Background()
	svc := testStore(t, filepath.Join(t.TempDir(), "audited-reads.db"))
	db := svc.testDB()
	actor := Actor{TenantID: 91, UserID: "audited-owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(ctx, actor, "audited-read-create", CreateProjectInput{Name: "synthetic audited read", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	r := &auditedReadRepository{Repository: svc.repository}
	svc.repository = r
	svc.audit = &auditSinkStub{}
	assertWritableConsistentRead := func(options TransactionOptions) {
		t.Helper()
		if options.ReadOnly || !options.RepeatableRead {
			t.Fatalf("audited read must retain consistency and permit its audit: %+v", options)
		}
	}
	if err := svc.Authorize(ctx, actor, project.ID, "read"); err != nil {
		t.Fatalf("authorize with audit: %v", err)
	}
	assertWritableConsistentRead(r.options[0])
	next := len(r.options)
	got, err := svc.GetProject(ctx, actor, project.ID)
	if err != nil || got.ID != project.ID {
		t.Fatalf("get with audit: id=%q error=%v", got.ID, err)
	}
	assertWritableConsistentRead(r.options[next])
	var persisted int64
	if err := db.Model(&auditEventRow{}).Where("project_id = ? AND action = ?", project.ID, "authorization.read").Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	// GetProject also rechecks G1 provenance. Its additional authorization
	// may add an event; require both entry-point reads without capping audits.
	if persisted < 2 {
		t.Fatalf("authorization and get must persist distinct audit events: %d", persisted)
	}
	var events []auditEventRow
	if err := db.Where("project_id = ? AND action = ?", project.ID, "authorization.read").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		if event.ID == "" || seen[event.ID] || event.ActorID != actor.UserID || event.Target != project.ID {
			t.Fatalf("audit identity or scope lost: %+v", event)
		}
		seen[event.ID] = true
	}
	if err := db.Exec("UPDATE tenant_members SET status = 'revoked' WHERE tenant_id = ? AND user_id = ?", actor.TenantID, actor.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, actor, project.ID, "read"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked authorization: %v", err)
	}
	if got, err := svc.GetProject(ctx, actor, project.ID); !errors.Is(err, ErrNotFound) || got.ID != "" {
		t.Fatalf("revoked get leaked project: id=%q error=%v", got.ID, err)
	}
	var row projectRow
	if err := db.Where("id = ?", project.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ProjectVersion != project.ProjectVersion || row.SpecRevision != project.SpecRevision {
		t.Fatalf("authorization reads mutated project revisions: %+v", row)
	}
}

// A tiny SQL driver observes the options actually passed by GORM to BeginTx.
// It deliberately has no query support and makes no PostgreSQL runtime claim.
type beginOptionsDriver struct{ options *[]driver.TxOptions }
type beginOptionsConn struct{ options *[]driver.TxOptions }
type beginOptionsTx struct{}

func (d beginOptionsDriver) Open(string) (driver.Conn, error) {
	return beginOptionsConn(d), nil
}
func (d beginOptionsDriver) Connect(context.Context) (driver.Conn, error) { return d.Open("") }
func (d beginOptionsDriver) Driver() driver.Driver                        { return d }
func (c beginOptionsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("queries not supported")
}
func (c beginOptionsConn) Close() error { return nil }
func (c beginOptionsConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c beginOptionsConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	*c.options = append(*c.options, options)
	return beginOptionsTx{}, nil
}
func (beginOptionsTx) Commit() error   { return nil }
func (beginOptionsTx) Rollback() error { return nil }

func TestGORMRepositoryPreservesIsolationAndReadOnlyIndependently(t *testing.T) {
	var captured []driver.TxOptions
	sqlDB := sql.OpenDB(beginOptionsDriver{options: &captured})
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewGORMRepository(db)
	for _, test := range []struct {
		name      string
		options   TransactionOptions
		isolation sql.IsolationLevel
		readOnly  bool
	}{
		{name: "audited-consistent-read", options: TransactionOptions{RepeatableRead: true}, isolation: sql.LevelRepeatableRead},
		{name: "genuine-read-only", options: TransactionOptions{ReadOnly: true}, isolation: sql.LevelRepeatableRead, readOnly: true},
		{name: "both-flags-retain-read-only", options: TransactionOptions{ReadOnly: true, RepeatableRead: true}, isolation: sql.LevelRepeatableRead, readOnly: true},
		{name: "ordinary-write", options: TransactionOptions{}, isolation: sql.LevelDefault},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := len(captured)
			if err := repository.Transaction(context.Background(), test.options, func(Transaction) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if len(captured) != before+1 {
				t.Fatalf("missing BeginTx: %+v", captured)
			}
			got := captured[before]
			if got.Isolation != driver.IsolationLevel(test.isolation) || got.ReadOnly != test.readOnly {
				t.Fatalf("SQL transaction options: got %+v, want isolation=%v readOnly=%v", got, test.isolation, test.readOnly)
			}
		})
	}
}
