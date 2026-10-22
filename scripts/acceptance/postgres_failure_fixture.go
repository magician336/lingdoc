package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const syntheticPassword = "synthetic_ci_only"

var (
	failureDatabasePattern       = regexp.MustCompile(`^lingdoc_fault_[a-f0-9]{12}$`)
	failureUserPattern           = regexp.MustCompile(`^lingdoc_fault_user_[a-f0-9]{12}$`)
	targetOverrideKeywordPattern = regexp.MustCompile(`(?i)(?:^|\s)(?:hostaddr|service|passfile)\s*=`)
)

// lingdocMigrationNames 发现生产用的 LingDoc 迁移链。
//
// 这条链以前是一份手抄的截止清单。迁移版本号是共享序列资源：任何一次重编号
// 都会把文件挪到那份清单的后面，于是这个 fixture 悄悄建出一个比仓库当前契约
// 更旧的 schema —— 生成运行表里没有 digest_version 列，PostgreSQL 故障场景
// 就带着 "column digest_version does not exist" 失败。
//
// 所以这里从目录里发现整条链，和仓内自己的数据库夹具一个做法
// （internal/lingdoc/workspacecore: productionLingDocMigrationNames）。
// 清单不再需要维护，也就没有可能悄悄落后于仓库当前状态。
func lingdocMigrationNames() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join("migrations", "versioned", "*_lingdoc_*.up.sql"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no LingDoc migrations found under migrations/versioned")
	}
	sort.Strings(matches)
	names := make([]string, len(matches))
	for i, match := range matches {
		names[i] = filepath.Base(match)
	}
	return names, nil
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
	if action != "create" && action != "drop" && action != "rollback-roundtrip" {
		return fmt.Errorf("expected create, drop, or rollback-roundtrip action")
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
	if action == "rollback-roundtrip" {
		if err := verifyFixtureOwnership(adminSQL, name, user, marker); err != nil {
			return err
		}
		return rollbackAndReapplyLingDocMigrations(failureTestDSN(adminDSN, name, user))
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
	roleCreated := true
	cleanupComplete := false
	defer func() {
		if cleanupComplete {
			return
		}
		if cleanupErr := cleanupFailedFixture(adminSQL, name, user, marker, roleCreated); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "failed to clean up partial PostgreSQL fixture:", cleanupErr)
		}
	}()
	if err := os.WriteFile(marker, []byte(name+"\n"+user+"\n"), 0o600); err != nil {
		_ = os.Remove(marker)
		_, _ = adminSQL.ExecContext(context.Background(), "DROP ROLE "+user)
		return err
	}
	if _, err := adminSQL.ExecContext(context.Background(),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", name, user)); err != nil {
		return err
	}
	if err := applyLingDocMigrations(failureTestDSN(adminDSN, name, user)); err != nil {
		return err
	}
	cleanupComplete = true
	return nil
}

func failureTestDSN(adminDSN, database, user string) string {
	return fmt.Sprintf("host=127.0.0.1 port=%s user=%s password=%s dbname=%s sslmode=disable",
		postgresPort(adminDSN), user, syntheticPassword, database)
}

func validateAdminTarget(dsn string) error {
	if err := rejectPostgresTargetOverrides(dsn); err != nil {
		return err
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN: %w", err)
	}
	host := strings.ToLower(config.Host)
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("admin PostgreSQL host is not loopback")
	}
	if config.User != "lingdoc_test" || config.Database != "lingdoc_test" {
		return fmt.Errorf("admin PostgreSQL identity/database is not the synthetic test target")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("admin PostgreSQL port is invalid")
	}
	return nil
}

func rejectPostgresTargetOverrides(dsn string) error {
	if targetOverrideKeywordPattern.MatchString(dsn) {
		return fmt.Errorf("PostgreSQL target keyword cannot override hostaddr, service, or passfile")
	}
	if !strings.HasPrefix(strings.ToLower(dsn), "postgres://") && !strings.HasPrefix(strings.ToLower(dsn), "postgresql://") {
		return nil
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN: %w", err)
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN query: %w", err)
	}
	for key := range values {
		switch strings.ToLower(key) {
		case "host", "hostaddr", "port", "user", "dbname", "database", "service", "passfile":
			return fmt.Errorf("PostgreSQL target query cannot override %s", key)
		}
	}
	return nil
}

func postgresPort(dsn string) string {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "0"
	}
	return strconv.Itoa(int(config.Port))
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
	migrations, err := lingdocMigrationNames()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "fixture applies %d production LingDoc migrations through %s\n",
		len(migrations), migrations[len(migrations)-1])
	_, db, err := openPostgres(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS tenants (id BIGINT PRIMARY KEY)"); err != nil {
		return fmt.Errorf("create base tenants table: %w", err)
	}
	for _, migration := range migrations {
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

func rollbackAndReapplyLingDocMigrations(dsn string) error {
	migrations, err := lingdocMigrationNames()
	if err != nil {
		return err
	}
	_, sqlDB, err := openPostgres(dsn)
	if err != nil {
		return err
	}
	for i := len(migrations) - 1; i >= 0; i-- {
		upName := migrations[i]
		downName := strings.Replace(upName, ".up.sql", ".down.sql", 1)
		raw, err := os.ReadFile(filepath.Join("migrations", "versioned", downName))
		if err != nil {
			_ = sqlDB.Close()
			return err
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			_ = sqlDB.Close()
			return fmt.Errorf("%s: %w", downName, err)
		}
	}
	var remaining int
	if err := sqlDB.QueryRowContext(context.Background(),
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND left(table_name, 8) = 'lingdoc_'").Scan(&remaining); err != nil {
		_ = sqlDB.Close()
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}
	if remaining != 0 {
		return fmt.Errorf("rollback left %d LingDoc tables in the disposable schema", remaining)
	}
	if err := applyLingDocMigrations(dsn); err != nil {
		return fmt.Errorf("reapply after rollback: %w", err)
	}
	checkDB, checkSQLDB, err := openPostgres(dsn)
	if err != nil {
		return err
	}
	defer checkSQLDB.Close()
	for _, table := range []string{"lingdoc_projects", "lingdoc_generation_runs", "lingdoc_project_template_copies"} {
		var exists bool
		if err := checkDB.Raw("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?)", table).Scan(&exists).Error; err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("reapplying migrations did not restore %s", table)
		}
	}
	var templateVersionColumn bool
	if err := checkDB.Raw("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'lingdoc_projects' AND column_name = 'template_copy_version')").Scan(&templateVersionColumn).Error; err != nil {
		return err
	}
	if !templateVersionColumn {
		return fmt.Errorf("reapplying migrations did not restore lingdoc_projects.template_copy_version")
	}
	return nil
}

func verifyFixtureOwnership(admin *sql.DB, name, user, marker string) error {
	data, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Errorf("read fixture ownership marker: %w", err)
	}
	if string(data) != name+"\n"+user+"\n" {
		return fmt.Errorf("fixture ownership marker does not match generated target")
	}
	var owned bool
	if err := admin.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba WHERE d.datname = $1 AND r.rolname = $2)",
		name, user).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("generated failure database is not owned by its generated test role")
	}
	return nil
}

func cleanupFailedFixture(admin *sql.DB, name, user, marker string, roleCreated bool) error {
	if _, err := os.Stat(marker); err == nil {
		return dropFixture(admin, name, user, marker)
	} else if !os.IsNotExist(err) {
		return err
	}
	if !roleCreated {
		return nil
	}
	_, err := admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+user)
	return err
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
