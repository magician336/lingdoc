package rulesetstore

import (
	"context"
	"errors"
	"testing"
)

func TestTenantMembershipAuthorizerRechecksActiveRole(t *testing.T) {
	db := openStoreTestDB(t, true)
	if err := db.Exec(`CREATE TABLE tenant_members (
		user_id TEXT NOT NULL, tenant_id INTEGER NOT NULL, role TEXT NOT NULL,
		status TEXT NOT NULL, deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_members(user_id,tenant_id,role,status) VALUES
		('owner',1,'owner','active'), ('admin',1,'admin','active'),
		('viewer',1,'viewer','active'), ('suspended',1,'admin','suspended')`).Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, TenantMembershipAuthorizer{})
	ctx := context.Background()
	draft := testDraft("v1")

	// The persisted membership is authoritative even if the request's cached
	// role claims greater privileges.
	spoofed := Actor{TenantID: 1, UserID: "viewer", Role: "owner"}
	if err := store.CreateDraft(ctx, spoofed, draft); !errors.Is(err, ErrRuleSetAccessDenied) {
		t.Fatalf("viewer with spoofed owner role create error = %v, want access denied", err)
	}
	if _, err := store.GetVersion(ctx, Actor{TenantID: 1, UserID: "suspended", Role: "admin"}, draft.ID, draft.Version); !errors.Is(err, ErrRuleSetNotFound) {
		t.Fatalf("suspended member read error = %v, want not found", err)
	}
	if err := store.CreateDraft(ctx, Actor{TenantID: 1, UserID: "admin", Role: "viewer"}, draft); err != nil {
		t.Fatalf("active tenant admin create error = %v", err)
	}
	if _, err := store.GetVersion(ctx, Actor{TenantID: 1, UserID: "viewer", Role: "viewer"}, draft.ID, draft.Version); err != nil {
		t.Fatalf("active viewer read error = %v", err)
	}
	if _, err := store.Publish(ctx, Actor{TenantID: 1, UserID: "viewer", Role: "admin"}, draft.ID, draft.Version); !errors.Is(err, ErrRuleSetAccessDenied) {
		t.Fatalf("viewer publish error = %v, want access denied", err)
	}
	if err := db.Exec("UPDATE tenant_members SET status='suspended' WHERE user_id='admin'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVersion(ctx, Actor{TenantID: 1, UserID: "admin", Role: "admin"}, draft.ID, draft.Version); !errors.Is(err, ErrRuleSetAccessDenied) {
		t.Fatalf("revoked member read error = %v, want access denied", err)
	}
}

func TestTenantMembershipAuthorizerRequiresIdentity(t *testing.T) {
	db := openStoreTestDB(t, true)
	if err := db.Exec(`CREATE TABLE tenant_members (user_id TEXT, tenant_id INTEGER, role TEXT, status TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, TenantMembershipAuthorizer{})
	if err := store.CreateDraft(context.Background(), Actor{TenantID: 1, UserID: " ", Role: "admin"}, testDraft("v1")); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("empty user create error = %v, want ErrActorRequired", err)
	}
	if _, err := store.ListVersions(context.Background(), Actor{TenantID: 0, UserID: "admin", Role: "admin"}, "rules"); !errors.Is(err, ErrRuleSetTenantRequired) {
		t.Fatalf("empty tenant list error = %v, want ErrRuleSetTenantRequired", err)
	}
}
