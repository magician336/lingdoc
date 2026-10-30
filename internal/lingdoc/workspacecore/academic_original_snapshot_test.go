package workspacecore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"gorm.io/gorm"
)

func exerciseAcademicSnapshotStore(t *testing.T, svc *Service, owner Actor, project Project) {
	t.Helper()
	ctx := context.Background()
	raw := []byte("synthetic private original")
	var ref AcademicOriginalSnapshotRef
	err := svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		var e error
		ref, e = PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, raw)
		if e != nil {
			return e
		}
		got, e := ReadAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, ref)
		if e != nil || !bytes.Equal(got, raw) {
			t.Fatalf("snapshot read err=%v", e)
		}
		again, e := PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, raw)
		if e != nil || again != ref {
			t.Fatalf("idempotent snapshot err=%v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.SHA256) != 64 || ref.Size != int64(len(raw)) {
		t.Fatalf("unexpected snapshot reference %+v", ref)
	}
	secondBody, _, _, err := svc.CreateProject(ctx, owner, "academic-snapshot-other-project", CreateProjectInput{Name: "snapshot isolation", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	second := asProject(t, secondBody)
	err = svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if _, e := ReadAcademicOriginalSnapshot(tx, owner.TenantID+1, project.ID, ref); !errors.Is(e, ErrNotFound) {
			t.Fatalf("cross-tenant lookup: %v", e)
		}
		if _, e := ReadAcademicOriginalSnapshot(tx, owner.TenantID, second.ID, ref); !errors.Is(e, ErrNotFound) {
			t.Fatalf("cross-project lookup: %v", e)
		}
		invalid := ref
		invalid.Size++
		if _, e := ReadAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, invalid); !errors.Is(e, ErrInvalidState) {
			t.Fatalf("incorrect size accepted: %v", e)
		}
		if _, e := PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, nil); !errors.Is(e, ErrInvalidRequest) {
			t.Fatalf("empty snapshot accepted: %v", e)
		}
		if _, e := PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, make([]byte, evidence.MaxOriginBytes+1)); !errors.Is(e, ErrInvalidRequest) {
			t.Fatalf("oversize snapshot accepted: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("synthetic rollback")
	var rolledBack AcademicOriginalSnapshotRef
	err = svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		var e error
		rolledBack, e = PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, []byte("rollback source"))
		if e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback failed: %v", err)
	}
	err = svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if _, e := ReadAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, rolledBack); !errors.Is(e, ErrNotFound) {
			t.Fatalf("rolled back snapshot survived: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if _, e := PutAcademicOriginalSnapshot(tx, owner.TenantID, project.ID, make([]byte, evidence.MaxOriginBytes)); e != nil {
			t.Fatalf("exactly 8 MiB must be accepted: %v", e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("size boundary rollback: %v", err)
	}
	db := svc.testDB()
	if err := db.Exec("UPDATE lingdoc_academic_original_snapshots SET content = ? WHERE tenant_id = ? AND project_id = ? AND sha256 = ?", []byte("synthetic private changed!"), owner.TenantID, project.ID, ref.SHA256).Error; err == nil {
		t.Fatal("snapshot update accepted")
	}
}

func TestAcademicSnapshotSQLiteIsolationAndRollback(t *testing.T) {
	f := newAgentFixture(t, true)
	exerciseAcademicSnapshotStore(t, f.svc, f.owner, f.project)
	var row struct {
		SHA256  string
		Size    int64
		Content []byte
	}
	db := f.svc.testDB()
	if err := db.Table("lingdoc_academic_original_snapshots").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	changed := bytes.Clone(row.Content)
	changed[0] ^= 1
	if err := db.Exec("INSERT OR REPLACE INTO lingdoc_academic_original_snapshots (tenant_id, project_id, sha256, size, content) VALUES (?, ?, ?, ?, ?)", f.owner.TenantID, f.project.ID, row.SHA256, row.Size, changed).Error; err == nil {
		t.Fatal("SQLite REPLACE changed snapshot bytes")
	}
}

func TestAcademicSnapshotRejectsCorruptContentAndReuse(t *testing.T) {
	f := newAgentFixture(t, true)
	raw := []byte("expected bytes")
	wrong := []byte("tampered bytes")
	sum := sha256.Sum256(raw)
	ref := AcademicOriginalSnapshotRef{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))}
	if err := f.svc.testDB().Exec("INSERT INTO lingdoc_academic_original_snapshots (tenant_id, project_id, sha256, size, content) VALUES (?, ?, ?, ?, ?)", f.owner.TenantID, f.project.ID, ref.SHA256, ref.Size, wrong).Error; err != nil {
		t.Fatal(err)
	}
	err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if _, e := ReadAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, ref); !errors.Is(e, ErrInvalidState) {
			t.Fatalf("corrupt bytes were accepted: %v", e)
		}
		if _, e := PutAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, raw); !errors.Is(e, ErrInvalidState) {
			t.Fatalf("corrupt snapshot was reused: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAcademicSnapshotSQLiteMigrationDownRefusesData(t *testing.T) {
	f := newAgentFixture(t, true)
	ctx := context.Background()
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		_, err := PutAcademicOriginalSnapshot(tx, f.owner.TenantID, f.project.ID, []byte("synthetic"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000063_lingdoc_academic_original_snapshots.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db := f.svc.testDB()
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(down)).Error }); err == nil {
		t.Fatal("downgrade accepted a nonempty snapshot table")
	}
	var count int64
	if err = db.Table("lingdoc_academic_original_snapshots").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed downgrade lost data: count=%d err=%v", count, err)
	}
	if err = db.Exec("DELETE FROM lingdoc_academic_original_snapshots").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(down)).Error }); err != nil {
		t.Fatalf("empty downgrade failed: %v", err)
	}
}

func TestAcademicSnapshotPostgresIsolationAndRollback(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	owner := Actor{TenantID: 309, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	exerciseAcademicSnapshotStore(t, svc, owner, project)
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", "000142_lingdoc_academic_original_snapshots.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(down)).Error }); err == nil {
		t.Fatal("PostgreSQL downgrade accepted a nonempty snapshot table")
	}
	var count int64
	if err = db.Table("lingdoc_academic_original_snapshots").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed PostgreSQL downgrade lost data: count=%d err=%v", count, err)
	}
	if err = db.Exec("DELETE FROM lingdoc_academic_original_snapshots").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(down)).Error }); err != nil {
		t.Fatalf("empty PostgreSQL downgrade failed: %v", err)
	}
}
