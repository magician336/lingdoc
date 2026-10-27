package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// versionedSQLiteTables is the set of tables that SQLite migrations must
// create to stay in sync with the versioned (PostgreSQL) migrations:
// 000041 task queue, 000053 system settings, 000055 processing spans,
// 000063 knowledge multi-tags, 000093 browser authorization,
// 000097 LingDoc workspace, 000098 LingDoc evidence assets,
// 000099 LingDoc candidate adoption, 000100/000101 LingDoc generation runs,
// 000102 LingDoc confirmation requests, 000103 LingDoc delivery stores,
// 000104 LingDoc member permissions, 000105-000114 LingDoc lifecycle stores,
// 000115 RuleSet base, 000116 full workflow, 000118-000120 RuleSet delivery,
// 000123 roadmap editing sessions and route edges, 000124 dependency
// candidates, 000125 audit action bounds, 000126 candidate storage shape and
// idempotency digest version, 000127 selected rewrite action shape,
// 000128 generation run digest version, 000129 selected rewrite validity,
// 000130 review reports, 000131 audit version fields, 000132 project archive,
// 000134 model call ledger;
// SQLite counterparts are 000036, 000037, and 000039-000053.
// 000122 is not listed: it renames lingdoc_change_sets in place, so it ends
// up under the name the table already had.
var versionedSQLiteTables = []string{
	"memory_extraction_sessions",
	"task_pending_ops",
	"task_dead_letters",
	"system_settings",
	"knowledge_processing_spans",
	"knowledge_tag_relations",
	"browser_devices",
	"browser_pairings",
	"browser_task_interruptions",
	"lingdoc_project_assets",
	"lingdoc_asset_revisions",
	"lingdoc_projects",
	"lingdoc_members",
	"lingdoc_chapters",
	"lingdoc_chapter_versions",
	"lingdoc_operations",
	"lingdoc_candidates",
	"lingdoc_chapter_confirmations",
	"lingdoc_candidate_adoptions",
	"lingdoc_chapter_confirmation_requests",
	"lingdoc_generation_runs",
	"lingdoc_release_snapshots",
	"lingdoc_export_artifacts",
	"lingdoc_working_copies",
	"lingdoc_rule_sets",
	"lingdoc_ruleset_use_audits",
	"lingdoc_template_profiles",
	"lingdoc_template_versions",
	"lingdoc_rule_suggestion_runs",
	"lingdoc_template_governance_operations",
	"lingdoc_g5_records",
	"lingdoc_g2_objects",
	"lingdoc_g2_object_versions",
	"lingdoc_g3_records",
	"lingdoc_g6_records",
	"lingdoc_selected_rewrites",
	"lingdoc_review_reports",
	"lingdoc_ruleset_metrics",
	"lingdoc_g3_roadmap_edit_sessions",
	"lingdoc_g3_route_edges",
	"lingdoc_g3_dependency_candidate_batches",
	"lingdoc_g3_dependency_candidates",
	"lingdoc_model_call_ledger",
}

// versionedSQLiteColumns maps each existing table to the columns that the
// versioned migrations add and the SQLite baseline was missing.
var versionedSQLiteColumns = map[string][]string{
	"lingdoc_export_artifacts":        {"ruleset_ref_json"},                                                                                              // SQLite 000039 / PostgreSQL 000118
	"lingdoc_project_template_copies": {"ruleset_ref_json"},                                                                                              // SQLite 000041 / PostgreSQL 000120
	"lingdoc_selected_rewrites":       {"evidence_card_refs_json", "template_basis_json", "evidence_constraints_json", "writing_action", "origin"},       // SQLite 000037 / 000048
	"memory_subjects":                 {"extraction_state"},                                                                                              // 000094
	"memory_items":                    {"replaces_id"},                                                                                                   // 000094
	"tenants":                         {"api_principal_config"},                                                                                          // 000064
	"users":                           {"is_system_admin"},                                                                                               // 000053
	"knowledges":                      {"pending_subtasks_count", "index_status"},                                                                       // 000056 / 000136 (#599)
	"messages":                        {"attachments", "usage"},                                                                                          // 000034, 000085
	"tenant_invitations":              {"token", "accepted_count"},                                                                                       // 000054
	"embed_channels":                  {"allow_memory"},                                                                                                  // 000060
	"mcp_oauth_tokens":                {"principal_type", "principal_id"},                                                                                // 000064
	"mcp_tool_approvals":              {"enabled"},                                                                                                       // 000091
	"lingdoc_generation_runs":         {"claim_token", "cancel_requested", "digest_version"},                                                             // 000101 / 000049
	"lingdoc_chapter_versions":        {"citation_usages_json"},                                                                                          // 000110
	"lingdoc_projects":                {"current_context_revision", "spec_metadata_json", "discarded_at", "delivery_status", "baseline_confirmation_id"}, // 000105-000109
	"lingdoc_candidates":              {"writing_action", "origin", "run_mode", "model_id"},                                                              // 000126
	"lingdoc_candidate_adoptions":     {"digest_version"},                                                                                                // 000126
	"lingdoc_model_call_ledger":       {"cost_basis"},                                                                                                    // 000058 / 000137 (#527)
}

// 000046 rebuilds lingdoc_project_audits in place to bound its action
// category column (#276). 000052 adds the two audit version columns (#327).
// 000053 rebuilds lingdoc_projects in place to widen its status vocabulary
// (the archived terminal state). 000055 adds the model-call ledger table
// (55-Spec / #453), so the table set grows by one. 000054 is the audit
// decision fields (PF-7), which main already records. 000056 makes the
// adoption idempotency row's chapter_version_id nullable (#693). 000057 adds
// the knowledge row's index_status column (#599): an indexed-but-failed parse
// must stay visible at the knowledge-row level. 000058 adds the ledger's
// cost_basis column (#527): the money columns' 0 must read as
// "not computed" when the profile computed no money, not as "free".
const expectedSQLiteMigrationVersion = 58

func TestSQLiteMigrationsCreateVersionedSchema(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)

	for _, table := range versionedSQLiteTables {
		require.Truef(t, sqliteTableExists(t, db, table), "SQLite migrations must create table %s", table)
	}
	for table, columns := range versionedSQLiteColumns {
		for _, column := range columns {
			require.Truef(
				t,
				sqliteColumnExists(t, db, table, column),
				"SQLite migrations must add column %s.%s",
				table,
				column,
			)
		}
	}

	assertSQLiteShareLinkInvitationsWork(t, db)
	assertSQLiteMCPOAuthPrincipalUpsertWorks(t, db)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"),
		"SQLite migrations must drop legacy knowledges.tag_id after multi-tag migration")
	// The selected rewrite candidate used to carry a status word list of its
	// own. The validity migration replaces it with the candidate layer's
	// validity: the old column must be gone, not merely unused.
	require.True(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "validity"),
		"SQLite migrations must give the selected rewrite candidate the validity column")
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "status"),
		"SQLite migrations must drop the selected rewrite candidate's old status word list")
}

func TestSQLiteMigrationsUpgradeV4PreservesData(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	// Build a legacy v4 migration root (000000_init .. 000004_memory) so we
	// can prove the new migrations upgrade an existing Lite database without
	// replaying the baseline.
	legacyRoot := copySQLiteMigrationsV4(t, repoRoot)
	chdirAndRestore(t, legacyRoot)

	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	require.Equal(t, 4, versionBefore)
	require.False(t, dirtyBefore)
	_, err := db.Exec("INSERT INTO tenants (name, business) VALUES (?, ?)", "upgrade-sentinel", "migration-test")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, tag_id) "+
			"VALUES (?, 1, ?, 'document', 'tagged-doc', 'manual', ?)",
		"legacy-knowledge-1", "legacy-kb-1", "legacy-tag-1",
	)
	require.NoError(t, err)

	// Run the full migration set from the repo root.
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db = openSQLiteDB(t, dbPath)
	versionAfter, dirtyAfter := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, versionAfter)
	require.False(t, dirtyAfter)

	for _, table := range versionedSQLiteTables {
		require.Truef(t, sqliteTableExists(t, db, table), "upgraded SQLite DB must have table %s", table)
	}
	for table, columns := range versionedSQLiteColumns {
		for _, column := range columns {
			require.Truef(
				t,
				sqliteColumnExists(t, db, table, column),
				"upgraded SQLite DB must have column %s.%s",
				table,
				column,
			)
		}
	}

	var sentinelName string
	require.NoError(t, db.QueryRow("SELECT name FROM tenants WHERE business = ?", "migration-test").Scan(&sentinelName))
	require.Equal(t, "upgrade-sentinel", sentinelName)

	var relationCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM knowledge_tag_relations WHERE knowledge_id = ? AND tag_id = ?",
		"legacy-knowledge-1", "legacy-tag-1",
	).Scan(&relationCount))
	require.Equal(t, 1, relationCount)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"))
}

func sqliteRepoRoot(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return repoRoot
}

func chdirAndRestore(t *testing.T, dir string) {
	t.Helper()
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
}

func openSQLiteDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqliteMigrationState(t *testing.T, db *sql.DB) (version int, dirty bool) {
	t.Helper()
	require.NoError(t, db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	return version, dirty
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&n))
	return n == 1
}

func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
		table,
		column,
	).Scan(&n))
	return n == 1
}

func assertSQLiteShareLinkInvitationsWork(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec("INSERT INTO tenants (name, business) VALUES (?, ?)", "share-link-tenant", "share-link-test")
	require.NoError(t, err)

	expiresAt := "2099-01-01 00:00:00"
	shareLinkInsert := "INSERT INTO tenant_invitations " +
		"(tenant_id, invitee_user_id, token, role, status, expires_at) " +
		"VALUES (1, '', ?, 'member', 'pending', ?)"
	_, err = db.Exec(shareLinkInsert, "token-a", expiresAt)
	require.NoError(t, err)
	_, err = db.Exec(shareLinkInsert, "token-b", expiresAt)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM tenant_invitations WHERE tenant_id = 1 AND invitee_user_id = '' AND status = 'pending'",
	).Scan(&count))
	require.Equal(t, 2, count)
}

func assertSQLiteMCPOAuthPrincipalUpsertWorks(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO mcp_services (id, tenant_id, name, transport_type) VALUES (?, 1, 'svc', 'http')",
		"svc-migration-1",
	)
	require.NoError(t, err)

	tokenInsertPrefix := "INSERT INTO mcp_oauth_tokens " +
		"(id, tenant_id, user_id, service_id, principal_type, principal_id, access_token) "
	_, err = db.Exec(
		tokenInsertPrefix +
			"VALUES ('tok-1', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', 'token-1')",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		tokenInsertPrefix +
			"VALUES ('tok-2', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', 'token-2') " +
			"ON CONFLICT(tenant_id, principal_type, principal_id, service_id) " +
			"DO UPDATE SET access_token = excluded.access_token",
	)
	require.NoError(t, err)

	var accessToken string
	require.NoError(t, db.QueryRow(
		"SELECT access_token FROM mcp_oauth_tokens "+
			"WHERE tenant_id = 1 AND principal_type = 'web_user' "+
			"AND principal_id = 'u1' AND service_id = 'svc-migration-1'",
	).Scan(&accessToken))
	require.Equal(t, "token-2", accessToken)

	var rowCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM mcp_oauth_tokens WHERE tenant_id = 1 AND service_id = 'svc-migration-1'",
	).Scan(&rowCount))
	require.Equal(t, 1, rowCount)
}

func copySQLiteMigrationsV4(t *testing.T, repoRoot string) string {
	t.Helper()
	dest := t.TempDir()
	srcDir := filepath.Join(repoRoot, "migrations", "sqlite")
	destDir := filepath.Join(dest, "migrations", "sqlite")
	require.NoError(t, os.MkdirAll(destDir, 0o755))

	legacy := []string{
		"000000_init.up.sql",
		"000001_remove_wiki_log.up.sql",
		"000002_knowledge_folder_path.up.sql",
		"000003_knowledge_base_auto_tag_config.up.sql",
		"000004_memory.up.sql",
	}
	for _, name := range legacy {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(destDir, name), data, 0o600))
	}
	return dest
}
