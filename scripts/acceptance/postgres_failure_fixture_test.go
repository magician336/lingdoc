package main

import "testing"

func TestValidateAdminTargetUsesDriverEffectiveValues(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		ok   bool
	}{
		{
			name: "key value target",
			dsn:  "host=127.0.0.1 port=5432 user=lingdoc_test dbname=lingdoc_test",
			ok:   true,
		},
		{
			name: "url target",
			dsn:  "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?sslmode=disable",
			ok:   true,
		},
		{
			name: "query overrides host",
			dsn:  "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?host=remote.example",
			ok:   false,
		},
		{
			name: "query overrides port",
			dsn:  "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?port=5433",
			ok:   false,
		},
		{
			name: "query overrides identity",
			dsn:  "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?user=postgres",
			ok:   false,
		},
		{
			name: "query overrides database",
			dsn:  "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?dbname=production",
			ok:   false,
		},
		{
			name: "remote host",
			dsn:  "host=db.example port=5432 user=lingdoc_test dbname=lingdoc_test",
			ok:   false,
		},
		{
			name: "invalid port",
			dsn:  "host=127.0.0.1 port=not-a-port user=lingdoc_test dbname=lingdoc_test",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdminTarget(tt.dsn)
			if (err == nil) != tt.ok {
				t.Fatalf("validateAdminTarget(%q) error = %v, want success = %t", tt.dsn, err, tt.ok)
			}
		})
	}
}

func TestPostgresPortUsesDriverEffectiveValue(t *testing.T) {
	if got := postgresPort("host=127.0.0.1 port=5433 user=lingdoc_test dbname=lingdoc_test"); got != "5433" {
		t.Fatalf("postgresPort key/value target = %q, want 5433", got)
	}
	if got := postgresPort("host=127.0.0.1 user=lingdoc_test dbname=lingdoc_test"); got != "5432" {
		t.Fatalf("postgresPort default = %q, want 5432", got)
	}
}
