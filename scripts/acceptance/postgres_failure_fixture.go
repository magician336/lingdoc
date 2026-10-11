package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const syntheticPassword = "synthetic_ci_only"

var (
	failureDatabasePattern = regexp.MustCompile(`^lingdoc_fault_[a-f0-9]{12}$`)
	failureUserPattern     = regexp.MustCompile(`^lingdoc_fault_user_[a-f0-9]{12}$`)
)

var lingdocMigrations = []string{
	"000097_lingdoc_workspace.up.sql", "000098_lingdoc_evidence_assets.up.sql",
	"000099_lingdoc_candidate_adoption.up.sql", "000100_lingdoc_generation_runs.up.sql",
	"000101_lingdoc_generation_claim_token.up.sql", "000102_lingdoc_chapter_confirmation_requests.up.sql",
	"000103_lingdoc_delivery_stores.up.sql", "000104_lingdoc_member_permissions.up.sql",
	"000105_lingdoc_project_context_revision.up.sql", "000106_lingdoc_draft_provenance.up.sql",
	"000107_lingdoc_owner_transfers.up.sql", "000108_lingdoc_project_discard.up.sql",
	"000109_lingdoc_project_baseline.up.sql", "000110_lingdoc_citation_usages.up.sql",
	"000111_lingdoc_change_sets.up.sql", "000112_lingdoc_working_copies.up.sql",
	"000113_lingdoc_selected_rewrites.up.sql", "000114_lingdoc_project_template_copies.up.sql",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated PostgreSQL fixture failed:", err)
		os.Exit(1)
	}
}

func run() error {
	action := ""
	if len(os.Args) == 2 {
		action = os.Args[1]
	}
	if action != "create" && action != "drop" {
		return fmt.Errorf("expected create or drop action")
	}
	adminDSN := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if err := validateAdminTarget(adminDSN); err != nil {
		return err
	}
	name, user := os.Getenv("LINGDOC_FAILURE_TEST_DB"), os.Getenv("LINGDOC_FAILURE_TEST_USER")
	if !failureDatabasePattern.MatchString(name) || !failureUserPattern.MatchString(user) {
		return fmt.Errorf("failure fixture identifiers are not generated test names")
	}
	marker := os.Getenv("LINGDOC_FAILURE_FIXTURE_MARKER")
	if marker == "" {
		return fmt.Errorf("fixture ownership marker path is required")
	}
	admin, adminSQL, err := openPostgres(adminDSN)
	if err != nil {
		return err
	}
	defer adminSQL.Close()
	if action == "drop" {
		return dropFixture(adminSQL, name, user, marker)
	}
	if _, err := os.Stat(marker); err == nil {
		return fmt.Errorf("fixture ownership marker already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	var exists bool
	if err := admin.Raw("SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = ?)", user).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("generated test role already exists; refusing to alter it")
	}
	if _, err := adminSQL.ExecContext(context.Background(),
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", user, syntheticPassword)); err != nil {
		return err
	}
	if err := os.WriteFile(marker, []byte(name+"\n"+user+"\n"), 0o600); err != nil {
		_, _ = adminSQL.ExecContext(context.Background(), "DROP ROLE "+user)
		return err
	}
	if _, err := adminSQL.ExecContext(context.Background(),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", name, user)); err != nil {
		return err
	}
	failureDSN := fmt.Sprintf("host=127.0.0.1 port=%s user=%s password=%s dbname=%s sslmode=disable",
		postgresPort(adminDSN), user, syntheticPassword, name)
	return applyLingDocMigrations(failureDSN)
}

func validateAdminTarget(dsn string) error {
	host, user, database := "", "", ""
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return fmt.Errorf("invalid PostgreSQL DSN")
		}
		host, user, database = strings.ToLower(parsed.Hostname()), parsed.User.Username(), strings.TrimPrefix(parsed.Path, "/")
	} else {
		fields := make(map[string]string)
		for _, part := range strings.Fields(dsn) {
			key, value, ok := strings.Cut(part, "=")
			if ok {
				fields[strings.ToLower(key)] = value
			}
		}
		host, user, database = strings.ToLower(fields["host"]), fields["user"], fields["dbname"]
	}
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("admin PostgreSQL host is not loopback")
	}
	if user != "lingdoc_test" || database != "lingdoc_test" {
		return fmt.Errorf("admin PostgreSQL identity/database is not the synthetic test target")
	}
	port, err := strconv.Atoi(postgresPort(dsn))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("admin PostgreSQL port is invalid")
	}
	return nil
}

func postgresPort(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err == nil && parsed.Port() != "" {
			return parsed.Port()
		}
		return "5432"
	}
	for _, part := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(key, "port") {
			return value
		}
	}
	return "5432"
}

func openPostgres(dsn string) (*gorm.DB, *sql.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	return db, sqlDB, nil
}

func applyLingDocMigrations(dsn string) error {
	_, db, err := openPostgres(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, migration := range lingdocMigrations {
		raw, err := os.ReadFile(filepath.Join("migrations", "versioned", migration))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
			return fmt.Errorf("%s: %w", migration, err)
		}
	}
	return nil
}

func dropFixture(admin *sql.DB, name, user, marker string) error {
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	expected := name + "\n" + user + "\n"
	if string(data) != expected {
		return fmt.Errorf("fixture ownership marker does not match generated target")
	}
	var exists bool
	if err := admin.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			return err
		}
	}
	if _, err := admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+user); err != nil {
		return err
	}
	return os.Remove(marker)
}
