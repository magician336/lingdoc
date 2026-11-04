package workspacecore

import (
	"context"
	"testing"
	"time"
)

// The catalog writer and card writer use this same project lock. This proves
// the production PostgreSQL lock serializes both transactions; it does not
// claim external file reads or policy services are transactional.
func TestPostgresAcademicEvidenceWritersSerializeOnProject(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	actor := Actor{TenantID: 8128, UserID: "academic-lock-owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ready, release, first := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		first <- svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			if err := lockAcademicEvidenceProject(tx, actor, project.ID); err != nil {
				return err
			}
			close(ready)
			<-release
			return nil
		})
	}()
	select {
	case <-ready:
	case err := <-first:
		t.Fatalf("first writer lock: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first writer lock timed out")
	}
	second := make(chan error, 1)
	go func() {
		second <- svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
			return lockAcademicEvidenceProject(tx, actor, project.ID)
		})
	}()
	select {
	case err := <-second:
		close(release)
		t.Fatalf("second writer passed before the first committed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second writer did not resume")
	}
}

func TestPostgresAcademicCatalogInvalidationPersistsHistory(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	db, closePool := openSilentPostgres(t, fixture.dsn)
	t.Cleanup(closePool)
	applyVersionedMigrations(t, db)
	if err := db.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithSources(NewGORMRepository(db), ContractDemoTemplate{}, &fakeSources{})
	owner := Actor{TenantID: 8129, UserID: "academic-catalog-owner"}
	project := seedActiveImpactProject(t, svc, owner)
	f := agentFixture{svc: svc, owner: owner, project: project}
	initial, err := svc.GetAcademicSourceCatalog(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := seedAcademicEvidenceInvalidation(t, f, "pg-catalog-card", "10.5555/pg", initial.SourceCatalogVersion)
	input := UpdateAcademicSourceCatalogInput{VersionExpectation: f.expectation(0), Rules: []AcademicSourceRule{{ID: "journal", SourceType: "journal", VenueName: "Example", VenueIDs: []string{"1234-5678"}}}}
	if _, _, _, err = svc.UpdateAcademicSourceCatalog(context.Background(), owner, project.ID, "pg-catalog-change", input); err != nil {
		t.Fatal(err)
	}
	previous := readAcademicCardVersion(t, f, card.ID, 1)
	current := readAcademicCardVersion(t, f, card.ID, 2)
	if previous.Status != "CONFIRMED" || previous.Confirmation == nil || current.Status != "NEEDS_REVIEW" || current.Confirmation != nil {
		t.Fatalf("postgres history or invalidation incorrect: old=%+v new=%+v", previous, current)
	}
}
