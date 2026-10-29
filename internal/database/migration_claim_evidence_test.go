package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func checkClaimEvidenceMigration(t *testing.T, db *sql.DB, chain string) {
	t.Helper()
	root := sqliteRepoRoot(t)
	up, err := os.ReadFile(filepath.Join(root, "migrations", chain, map[string]string{"sqlite": "000060_lingdoc_candidate_claim_evidence.up.sql", "versioned": "000139_lingdoc_candidate_claim_evidence.up.sql"}[chain]))
	require.NoError(t, err)
	down, err := os.ReadFile(filepath.Join(root, "migrations", chain, map[string]string{"sqlite": "000060_lingdoc_candidate_claim_evidence.down.sql", "versioned": "000139_lingdoc_candidate_claim_evidence.down.sql"}[chain]))
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE lingdoc_candidates(id TEXT PRIMARY KEY, body_markdown TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE lingdoc_selected_rewrites(id TEXT PRIMARY KEY, body_markdown TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO lingdoc_candidates(id,body_markdown) VALUES('old','original')`)
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	var body, claims string
	require.NoError(t, db.QueryRow(`SELECT body_markdown,claim_evidence_json FROM lingdoc_candidates WHERE id='old'`).Scan(&body, &claims))
	require.Equal(t, "original", body)
	require.Equal(t, "[]", claims)
	_, err = db.Exec(`UPDATE lingdoc_candidates SET claim_evidence_json='[{"text":"claim"}]' WHERE id='old'`)
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.Error(t, err, "rollback must not erase new evidence")
	require.NoError(t, db.QueryRow(`SELECT body_markdown,claim_evidence_json FROM lingdoc_candidates WHERE id='old'`).Scan(&body, &claims))
	require.Contains(t, claims, "claim")
	_, err = db.Exec(`UPDATE lingdoc_candidates SET claim_evidence_json='[]' WHERE id='old'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO lingdoc_selected_rewrites(id,body_markdown,claim_evidence_json) VALUES('rewrite','text','[{"text":"claim"}]')`)
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.Error(t, err, "rollback must preserve rewrite evidence as well")
	_, err = db.Exec(`UPDATE lingdoc_selected_rewrites SET claim_evidence_json='[]' WHERE id='rewrite'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO lingdoc_claim_provenance(id,project_id,chapter_id,chapter_version_id,origin_kind,candidate_id,run_id,working_copy_revision,start_utf16,end_utf16,body_hash,status,created_at) VALUES('trace','p','c','','generation','candidate','run',1,0,1,'hash','needs_revalidation',CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.Error(t, err, "rollback must preserve provenance")
	_, err = db.Exec(`DELETE FROM lingdoc_claim_provenance WHERE id='trace'`)
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT body_markdown FROM lingdoc_candidates WHERE id='old'`).Scan(&body))
	require.Equal(t, "original", body)
}
func TestSQLiteClaimEvidenceMigrationPreservesLegacyAndRefusesLoss(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "claim-evidence.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)
	checkClaimEvidenceMigration(t, db, "sqlite")
}
func TestPostgresClaimEvidenceMigrationPreservesLegacyAndRefusesLoss(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: isolated PostgreSQL DSN is unset")
	}
	schema := newPostgresRehearsalSchema(t, dsn)
	db := openPostgresSchemaDB(t, dsn, schema)
	checkClaimEvidenceMigration(t, db, "versioned")
}
