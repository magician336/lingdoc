package workspacecore

import (
	"context"
	"testing"
	"time"
)

// This pins the tenant-membership row serialization used by the association
// operation. Asset-policy, API-key and object-storage races remain separate
// requirements; this test does not claim to cover them.
func TestPostgresAcademicAssociationMembershipRevocationLock(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	actor := Actor{TenantID: 8127, UserID: "association-owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			if _, err := svc.lockAssociationDecisionRows(context.Background(), tx, actor, project.ID); err != nil {
				return err
			}
			close(ready)
			<-release
			return nil
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("lock: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("lock timeout")
	}
	revoke := make(chan error, 1)
	go func() {
		revoke <- db.Exec("UPDATE tenant_members SET status = 'disabled' WHERE tenant_id = ? AND user_id = ?", actor.TenantID, actor.UserID).Error
	}()
	select {
	case err := <-revoke:
		close(release)
		t.Fatalf("revocation finished before lock released: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-revoke:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revocation remained blocked")
	}
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		_, err := svc.lockAssociationDecisionRows(context.Background(), tx, actor, project.ID)
		return err
	}); err == nil {
		t.Fatal("revoked actor authorized after lock release")
	}
}
