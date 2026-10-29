package templategovernance

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

type switchUsageGate struct {
	entered chan struct{}
	release <-chan struct{}
}

type switchNotFoundAuthorizer struct{ *testAuth }

func (a switchNotFoundAuthorizer) AuthorizeTemplate(context.Context, *gorm.DB, Actor, Action, string) error {
	return ErrNotFound
}

func (u switchUsageGate) AffectedProjects(ctx context.Context, _ *gorm.DB, _ Actor, _, _ string) ([]string, error) {
	close(u.entered)
	if u.release != nil {
		select {
		case <-u.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []string{"switched-project"}, nil
}

func TestCatalogSwitchSQLiteSerializesRecallBeforeImpactRead(t *testing.T) {
	t.Run("switch first", func(t *testing.T) {
		s, db, _, actor, draft, _ := seedService(t)
		published := publishTestVersion(t, s, actor, draft)
		pool, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(4)
		entered := make(chan struct{})
		s.ports.Usage = switchUsageGate{entered: entered}
		result := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, err := s.ResolveForSwitch(ctx, tx, actor, published.TemplateID, published.Version, true); err != nil {
				return err
			}
			go func() {
				_, err := s.Recall(ctx, actor, "switch-first-recall", RecallInput{GovernanceInput: GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "verify lock"}, MigrationAdvice: "choose another"})
				result <- err
			}()
			select {
			case <-entered:
				t.Error("recall read impact before the switch transaction released the template lock")
			case err := <-result:
				t.Errorf("recall ended before switch released the template lock: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("recall did not finish after switch lock release")
		}
		select {
		case <-entered:
		default:
			t.Fatal("recall never read its impact")
		}
	})

	t.Run("recall first", func(t *testing.T) {
		s, db, _, actor, draft, _ := seedService(t)
		published := publishTestVersion(t, s, actor, draft)
		pool, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(4)
		entered, release := make(chan struct{}), make(chan struct{})
		s.ports.Usage = switchUsageGate{entered: entered, release: release}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		recalled, switched := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := s.Recall(ctx, actor, "recall-first-lock", RecallInput{GovernanceInput: GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "verify lock"}, MigrationAdvice: "choose another"})
			recalled <- err
		}()
		select {
		case <-entered:
		case err := <-recalled:
			t.Fatalf("recall did not enter impact read: %v", err)
		case <-ctx.Done():
			t.Fatal("recall did not enter impact read")
		}
		go func() {
			switched <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				_, err := s.ResolveForSwitch(ctx, tx, actor, published.TemplateID, published.Version, true)
				return err
			})
		}()
		select {
		case err := <-switched:
			t.Errorf("switch passed the lifecycle lock before recall committed: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		select {
		case err := <-recalled:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("recall failed to finish")
		}
		select {
		case err := <-switched:
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("switch did not observe recalled version: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("switch did not finish")
		}
	})
}

func TestCatalogSwitchPreviewIsReadOnlyAndDistinguishesAuthorization(t *testing.T) {
	s, db, auth, actor, draft, _ := seedService(t)
	published := publishTestVersion(t, s, actor, draft)
	if err := db.Exec("PRAGMA query_only=ON").Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("PRAGMA query_only=OFF")
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ResolveForSwitch(context.Background(), tx, actor, published.TemplateID, published.Version, false)
		return err
	}); err != nil {
		t.Fatalf("preview attempted a write: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ResolveForSwitch(context.Background(), tx, actor, published.TemplateID, published.Version, true)
		return err
	}); err == nil {
		t.Fatal("write resolution did not acquire SQLite's write reservation")
	}
	s.ports.Authorizer = switchNotFoundAuthorizer{auth}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ResolveForSwitch(context.Background(), tx, actor, "template-demo", "1", false)
		return err
	}); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrCatalogVersionAbsent) {
		t.Fatalf("authorization failure was misclassified as an absent catalog row: %v", err)
	}
	s.ports.Authorizer = auth
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ResolveForSwitch(context.Background(), tx, actor, "template-demo", "1", false)
		return err
	}); !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrCatalogVersionAbsent) {
		t.Fatalf("truly absent catalog row was not distinguished: %v", err)
	}
}
