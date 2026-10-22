package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

// TestSQLiteCandidateStorageShapeMigrationGoesUpAndDown runs the candidate
// storage shape migration up, down and up again on a real SQLite database.
//
// The migration is the expand half of a wide refactor, so it has to be
// reversible: the rows that exist before it must still be there after it, and
// the rows that exist after it must survive its own rollback.
func TestSQLiteCandidateStorageShapeMigrationGoesUpAndDown(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "shape.db")
	migrator := newSQLiteMigrator(t, dbPath)
	// 钉住**这一条**迁移的版本号，不是「最新的那个」：迁移号是共享序列资源，
	// 别的面后面还要加迁移。版本号本身由 expectedSQLiteMigrationVersion 守着。
	require.NoError(t, migrator.Migrate(candidateShapeSQLiteVersion))

	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)
	require.False(t, dirty)

	db := openSQLiteDB(t, dbPath)
	for _, column := range []string{"writing_action", "origin", "run_mode", "model_id"} {
		require.True(t, sqliteColumnExists(t, db, "lingdoc_candidates", column),
			"升级之后候选表必须带 %s 列", column)
	}
	require.True(t, sqliteColumnExists(t, db, "lingdoc_candidate_adoptions", "digest_version"))

	// A candidate and an adoption row written under the old shape.
	insertCandidateRow(t, db, "candidate-1", "fresh")
	insertAdoptionRow(t, db, "adoption-1")

	// A row written before the migration carries the digest version of the
	// rule that had no version prefix.
	var digestVersion int
	require.NoError(t, db.QueryRow(
		"SELECT digest_version FROM lingdoc_candidate_adoptions WHERE id = ?", "adoption-1",
	).Scan(&digestVersion))
	require.Equal(t, 1, digestVersion)

	// The new validity state is accepted by the database.
	insertCandidateRow(t, db, "candidate-2", "not_authorized")

	// The closed origin vocabulary is enforced.
	_, err = db.Exec(
		"INSERT INTO lingdoc_candidates (id, project_id, chapter_id, run_id, body_markdown, basis_json, validity, origin) " +
			"VALUES ('candidate-3', 'project-1', 'chapter-1', 'run-3', 'body', '{}', 'fresh', 'written_by_hand')",
	)
	require.Error(t, err, "来源取值必须落在既有三值词表里")

	// The old shape cannot say "the authorization is gone", so that candidate
	// is taken out again before the rollback runs. The refusal itself is
	// pinned by the guard test below.
	_, err = db.Exec("DELETE FROM lingdoc_candidates WHERE id = ?", "candidate-2")
	require.NoError(t, err)

	// Down: the old shape comes back and the rows survive it.
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(46), version)
	require.False(t, dirty)

	for _, column := range []string{"writing_action", "origin", "run_mode", "model_id"} {
		require.False(t, sqliteColumnExists(t, db, "lingdoc_candidates", column),
			"回滚之后候选表必须不再带 %s 列", column)
	}
	require.False(t, sqliteColumnExists(t, db, "lingdoc_candidate_adoptions", "digest_version"))

	var validity string
	require.NoError(t, db.QueryRow(
		"SELECT validity FROM lingdoc_candidates WHERE id = ?", "candidate-1",
	).Scan(&validity))
	require.Equal(t, "fresh", validity)

	var adoptionCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM lingdoc_candidate_adoptions WHERE id = ?", "adoption-1",
	).Scan(&adoptionCount))
	require.Equal(t, 1, adoptionCount)

	// Up again: the new shape returns and the rows are still there.
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)
	require.False(t, dirty)

	for _, column := range []string{"writing_action", "origin", "run_mode", "model_id"} {
		require.True(t, sqliteColumnExists(t, db, "lingdoc_candidates", column),
			"再次升级之后候选表必须重新带上 %s 列", column)
	}
	require.NoError(t, db.QueryRow(
		"SELECT validity FROM lingdoc_candidates WHERE id = ?", "candidate-1",
	).Scan(&validity))
	require.Equal(t, "fresh", validity)
	require.NoError(t, db.QueryRow(
		"SELECT digest_version FROM lingdoc_candidate_adoptions WHERE id = ?", "adoption-1",
	).Scan(&digestVersion))
	require.Equal(t, 1, digestVersion)
}

// TestSQLiteCandidateStorageShapeMigrationRefusesToDropNotAuthorized
// Candidates pins the rollback guard.
//
// A candidate whose authorization is gone has no truthful representation in
// the old two-value validity state. The rollback refuses instead of quietly
// rewriting that state, and it refuses before it touches the table.
func TestSQLiteCandidateStorageShapeMigrationRefusesToDropNotAuthorizedCandidates(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "guard.db")
	migrator := newSQLiteMigrator(t, dbPath)
	// 同样钉住这一条迁移的版本号：回滚一步才是这一条迁移的回滚。
	require.NoError(t, migrator.Migrate(candidateShapeSQLiteVersion))

	db := openSQLiteDB(t, dbPath)
	insertCandidateRow(t, db, "candidate-1", "not_authorized")

	require.Error(t, migrator.Steps(-1), "旧形状表达不了授权失效的候选，回滚必须被拒绝")

	var validity string
	require.NoError(t, db.QueryRow(
		"SELECT validity FROM lingdoc_candidates WHERE id = ?", "candidate-1",
	).Scan(&validity))
	require.Equal(t, "not_authorized", validity, "被拒绝的回滚不得改动候选状态")
	require.True(t, sqliteColumnExists(t, db, "lingdoc_candidates", "writing_action"),
		"被拒绝的回滚不得改动候选表的形状")
}

// newSQLiteMigrator opens a migrator on one SQLite database file so that a
// test can step the migrations up and down.
func newSQLiteMigrator(t *testing.T, dbPath string) *migrate.Migrate {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	migrator, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.Close()
	})
	return migrator
}

// insertCandidateRow writes one candidate row in the shape both the old and
// the new migration accept.
func insertCandidateRow(t *testing.T, db *sql.DB, id string, validity string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_candidates (id, project_id, chapter_id, run_id, body_markdown, basis_json, validity) "+
			"VALUES (?, 'project-1', 'chapter-1', ?, '候选正文', '{}', ?)",
		id, "run-"+id, validity,
	)
	require.NoError(t, err)
}

// insertAdoptionRow writes one candidate adoption row in the shape both the
// old and the new migration accept.
func insertAdoptionRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_candidate_adoptions "+
			"(id, project_id, chapter_id, actor_id, idempotency_key, request_hash, chapter_version_id, response_json) "+
			"VALUES (?, 'project-1', 'chapter-1', 'user-1', ?, 'digest', 'version-1', '{}')",
		id, "key-"+id,
	)
	require.NoError(t, err)
}
