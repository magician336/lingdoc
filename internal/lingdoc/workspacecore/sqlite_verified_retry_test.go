package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVerifiedSQLiteRetryNeverRetriesUnverifiedBusy(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"commit or preflight busy", errors.New("database is locked"), 1},
		{"confirmed rollback busy", verifiedSQLiteRollback{cause: errors.New("database is locked")}, 2},
		{"confirmed rollback non-lock", verifiedSQLiteRollback{cause: errors.New("audit denied")}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, _, _, err := retryVerifiedSQLiteOperation(context.Background(), func(context.Context) (json.RawMessage, int, bool, error) {
				calls++
				if calls == 1 {
					return nil, 0, false, tc.err
				}
				return nil, 200, false, nil
			})
			if calls != tc.want {
				t.Fatalf("attempt count=%d want %d", calls, tc.want)
			}
			if tc.want == 1 && err == nil {
				t.Fatal("unverified failure was hidden")
			}
			if tc.want == 2 && err != nil {
				t.Fatalf("verified rollback did not recover: %v", err)
			}
		})
	}
}

func TestSQLiteProductionDriverFailedCommitDiscardsConnection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "production-driver.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	pool.SetMaxOpenConns(1)
	for _, query := range []string{
		"PRAGMA foreign_keys = ON",
		"CREATE TABLE retry_parent (id INTEGER PRIMARY KEY)",
		"CREATE TABLE retry_child (parent_id INTEGER, FOREIGN KEY(parent_id) REFERENCES retry_parent(id) DEFERRABLE INITIALLY DEFERRED)",
	} {
		if err = db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewGORMRepository(db)
	calls := 0
	_, _, _, err = retryVerifiedSQLiteOperation(context.Background(), func(ctx context.Context) (json.RawMessage, int, bool, error) {
		calls++
		err := repo.Transaction(ctx, TransactionOptions{VerifyRollback: true}, func(tx Transaction) error {
			dbtx, e := TransactionDatabase(tx)
			if e != nil {
				return e
			}
			return dbtx.Exec("INSERT INTO retry_child(parent_id) VALUES (404)").Error
		})
		return nil, 0, false, err
	})
	var verified verifiedSQLiteRollback
	if err == nil || errors.As(err, &verified) || calls != 1 {
		t.Fatalf("failed commit incorrectly authorized a retry: calls=%d err=%v", calls, err)
	}
	var count int64
	if err = db.Raw("SELECT count(*) FROM retry_child").Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed commit leaked rows into pooled connection: count=%d err=%v", count, err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec("INSERT INTO retry_parent(id) VALUES (1)").Error }); err != nil {
		t.Fatalf("pooled connection retains failed transaction: %v", err)
	}
}

func TestSQLiteVerifiedRetryHonorsCancellation(t *testing.T) {
	t.Run("before retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, _, _, err := retryVerifiedSQLiteOperation(ctx, func(context.Context) (json.RawMessage, int, bool, error) {
			calls++
			cancel()
			return nil, 0, false, verifiedSQLiteRollback{cause: errors.New("database is locked")}
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled operation retried or returned wrong error: calls=%d err=%v", calls, err)
		}
	})
	t.Run("during transaction", func(t *testing.T) {
		f := newAgentFixture(t, false)
		db := f.svc.testDB()
		if err := db.Exec("CREATE TABLE retry_canceled (value INTEGER)").Error; err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		_, _, _, err := retryVerifiedSQLiteOperation(ctx, func(ctx context.Context) (json.RawMessage, int, bool, error) {
			calls++
			err := f.svc.repository.Transaction(ctx, TransactionOptions{VerifyRollback: true}, func(tx Transaction) error {
				dbtx, e := TransactionDatabase(tx)
				if e != nil {
					return e
				}
				if e = dbtx.Exec("INSERT INTO retry_canceled(value) VALUES (1)").Error; e != nil {
					return e
				}
				cancel()
				return context.Canceled
			})
			return nil, 0, false, err
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled transaction retried or returned wrong error: calls=%d err=%v", calls, err)
		}
		var count int64
		if err = db.Raw("SELECT count(*) FROM retry_canceled").Scan(&count).Error; err != nil || count != 0 {
			t.Fatalf("canceled transaction leaked rows: count=%d err=%v", count, err)
		}
		if err = db.Exec("INSERT INTO retry_canceled(value) VALUES (2)").Error; err != nil {
			t.Fatalf("connection unavailable after cancellation: %v", err)
		}
	})
}

func TestSQLiteVerifiedRollbackFailureIsNotRetried(t *testing.T) {
	f := newAgentFixture(t, false)
	calls := 0
	_, _, _, err := retryVerifiedSQLiteOperation(context.Background(), func(ctx context.Context) (json.RawMessage, int, bool, error) {
		calls++
		err := f.svc.repository.Transaction(ctx, TransactionOptions{VerifyRollback: true}, func(tx Transaction) error {
			db, e := TransactionDatabase(tx)
			if e != nil {
				return e
			}
			// Force a rollback failure: the native transaction is already gone.
			if e = db.Exec("ROLLBACK").Error; e != nil {
				return e
			}
			return errors.New("database is locked")
		})
		return nil, 0, false, err
	})
	var verified verifiedSQLiteRollback
	if err == nil || errors.As(err, &verified) || calls != 1 {
		t.Fatalf("rollback failure incorrectly authorized a retry: calls=%d err=%v", calls, err)
	}
}

func TestSQLiteCommitFailureIsNotMarkedOrRetried(t *testing.T) {
	f := newAgentFixture(t, false)
	db := f.svc.testDB()
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	for _, sql := range []string{
		"PRAGMA foreign_keys = ON",
		"CREATE TABLE retry_parent (id INTEGER PRIMARY KEY)",
		"CREATE TABLE retry_child (parent_id INTEGER, FOREIGN KEY(parent_id) REFERENCES retry_parent(id) DEFERRABLE INITIALLY DEFERRED)",
	} {
		if err = db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	_, _, _, err = retryVerifiedSQLiteOperation(context.Background(), func(ctx context.Context) (json.RawMessage, int, bool, error) {
		calls++
		err := f.svc.repository.Transaction(ctx, TransactionOptions{VerifyRollback: true}, func(tx Transaction) error {
			dbtx, e := TransactionDatabase(tx)
			if e != nil {
				return e
			}
			return dbtx.Exec("INSERT INTO retry_child(parent_id) VALUES (404)").Error
		})
		return nil, 0, false, err
	})
	var verified verifiedSQLiteRollback
	if err == nil || errors.As(err, &verified) || calls != 1 {
		t.Fatalf("failed commit incorrectly authorized a retry: calls=%d err=%v", calls, err)
	}
	var count int64
	if err = db.Raw("SELECT count(*) FROM retry_child").Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed commit leaked uncommitted rows into pooled connection: count=%d err=%v", count, err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec("INSERT INTO retry_parent(id) VALUES (1)").Error }); err != nil {
		t.Fatalf("pooled connection retains failed transaction: %v", err)
	}
}

func TestSQLiteCommitBusyDiscardsConnectionWithoutRetry(t *testing.T) {
	f := newAgentFixture(t, false)
	db := f.svc.testDB()
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(2)
	for _, query := range []string{
		"PRAGMA journal_mode = DELETE",
		"CREATE TABLE retry_busy (value INTEGER)",
		"INSERT INTO retry_busy(value) VALUES (1)",
	} {
		if err = db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	reader, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	readTx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readTx.Rollback() }()
	var original int
	if err = readTx.QueryRowContext(ctx, "SELECT value FROM retry_busy").Scan(&original); err != nil || original != 1 {
		t.Fatalf("reader could not establish shared lock: value=%d err=%v", original, err)
	}
	calls := 0
	_, _, _, err = retryVerifiedSQLiteOperation(ctx, func(ctx context.Context) (json.RawMessage, int, bool, error) {
		calls++
		err := f.svc.repository.Transaction(ctx, TransactionOptions{VerifyRollback: true}, func(tx Transaction) error {
			dbtx, e := TransactionDatabase(tx)
			if e != nil {
				return e
			}
			if e = dbtx.Exec("PRAGMA busy_timeout = 0").Error; e != nil {
				return e
			}
			return dbtx.Exec("UPDATE retry_busy SET value = 2").Error
		})
		return nil, 0, false, err
	})
	var verified verifiedSQLiteRollback
	if err == nil || !isSQLiteLockError(err) || errors.As(err, &verified) || calls != 1 {
		t.Fatalf("COMMIT BUSY must not be retried: calls=%d err=%v", calls, err)
	}
	if err = readTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var value int
	if err = db.Raw("SELECT value FROM retry_busy").Scan(&value).Error; err != nil || value != 1 {
		t.Fatalf("failed commit leaked uncommitted value: value=%d err=%v", value, err)
	}
	if err = db.Exec("UPDATE retry_busy SET value = 3").Error; err != nil {
		t.Fatalf("failed commit left a lock behind: %v", err)
	}
}
