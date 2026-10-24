package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// modelsTestAESKey is a self-made 32-byte key. No real key and no real
// secret ever enters the repository.
const modelsTestAESKey = "0123456789abcdef0123456789abcdef"

// setupModelsTestDB builds a SQLite database from the production migration
// chain and opens it through GORM.
//
// The models table belongs to the production migration chain, not to this
// fixture: a fixture that hand-writes the column list drifts away from
// production the next time a column is added, and the drift stays green —
// the repository's own guard says so
// (scripts/ci/test_lingdoc_fixture_schema_source.py).
//
// The `parameters TEXT NOT NULL` declaration is the point of this file: the
// column is declared TEXT, while this application binds the []byte that
// json.Marshal returns. SQLite keeps a BLOB a BLOB, so the storage class of a
// row's parameters depends on WHO wrote it — this application (BLOB), or a
// literal SQL INSERT in a migration, an ops script or an import (TEXT).
func setupModelsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "models.db")
	require.NoError(t, database.RunMigrationsWithOptions(
		"sqlite3://unused", database.MigrationOptions{SQLiteDBPath: dbPath},
	))

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// insertTextWrittenModel writes a model row the way a literal SQL INSERT
// does: the parameters value is bound as text, so SQLite stores it with the
// TEXT storage class.
func insertTextWrittenModel(t *testing.T, db *gorm.DB, id string, parameters string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO models (id, tenant_id, name, type, source, parameters, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, 7, "qwen3.8-flash", string(types.ModelTypeKnowledgeQA), string(types.ModelSourceRemote),
		parameters, string(types.ModelStatusActive),
	).Error)
}

// #609 — a model row that was written by something other than this
// application must still carry its provider, its endpoint and its credential.
//
// The scanner used to accept []byte only and returned without a word for any
// other carrier, so a row whose parameters were stored as TEXT loaded with an
// empty configuration. The row was still listed as a configured model (its
// name, type, source and status are ordinary columns) while it had no
// provider, no endpoint and no credential — and was therefore never eligible
// for approval.
func TestModelRepositoryReadsModelParametersWrittenAsText(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", modelsTestAESKey)
	db := setupModelsTestDB(t)
	repo := NewModelRepository(db)

	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(modelsTestAESKey))
	require.NoError(t, err)
	insertTextWrittenModel(t, db, "text-written",
		`{"provider":"aliyun","base_url":"https://models.example.com/v1","api_key":"`+ciphertext+`"}`)

	model, err := repo.GetByID(context.Background(), 7, "text-written")
	require.NoError(t, err)
	require.NotNil(t, model)

	assert.Equal(t, "qwen3.8-flash", model.Name)
	assert.Equal(t, types.ModelTypeKnowledgeQA, model.Type)
	assert.Equal(t, types.ModelSourceRemote, model.Source)
	assert.Equal(t, types.ModelStatusActive, model.Status)

	assert.Equal(t, "aliyun", model.Parameters.Provider)
	assert.Equal(t, "https://models.example.com/v1", model.Parameters.BaseURL)
	assert.Equal(t, "sk-self-made-secret", model.Parameters.APIKey)
	assert.NoError(t, model.Parameters.CredentialDecryptError())
}

// The list path showed the row as "configured" while its configuration was
// empty; it must show the configuration too.
func TestModelRepositoryListsModelParametersWrittenAsText(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", modelsTestAESKey)
	db := setupModelsTestDB(t)
	repo := NewModelRepository(db)

	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(modelsTestAESKey))
	require.NoError(t, err)
	insertTextWrittenModel(t, db, "text-written",
		`{"provider":"aliyun","base_url":"https://models.example.com/v1","api_key":"`+ciphertext+`"}`)

	models, err := repo.List(context.Background(), 7, types.ModelTypeKnowledgeQA, types.ModelSourceRemote)
	require.NoError(t, err)
	require.Len(t, models, 1)

	assert.Equal(t, "aliyun", models[0].Parameters.Provider)
	assert.Equal(t, "https://models.example.com/v1", models[0].Parameters.BaseURL)
	assert.Equal(t, "sk-self-made-secret", models[0].Parameters.APIKey)
}

// The row this application writes itself keeps working: its parameters are
// stored as BLOB, and the shared reader accepts that carrier too.
func TestModelRepositoryReadsModelParametersWrittenByTheApplication(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", modelsTestAESKey)
	db := setupModelsTestDB(t)
	repo := NewModelRepository(db)

	created := &types.Model{
		ID:       "app-written",
		TenantID: 7,
		Name:     "qwen3.8-flash",
		Type:     types.ModelTypeKnowledgeQA,
		Source:   types.ModelSourceRemote,
		Status:   types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: "aliyun",
			BaseURL:  "https://models.example.com/v1",
			APIKey:   "sk-self-made-secret",
		},
	}
	require.NoError(t, repo.Create(context.Background(), created))

	model, err := repo.GetByID(context.Background(), 7, "app-written")
	require.NoError(t, err)
	require.NotNil(t, model)

	assert.Equal(t, "aliyun", model.Parameters.Provider)
	assert.Equal(t, "https://models.example.com/v1", model.Parameters.BaseURL)
	assert.Equal(t, "sk-self-made-secret", model.Parameters.APIKey)
}

// A row whose parameters cannot be read must fail the load instead of
// loading as a model with an empty configuration. Before #609 the reader
// dropped the value and the row came back with no provider at all, which is
// indistinguishable from a model nobody configured.
func TestModelRepositoryFailsOnUnreadableModelParameters(t *testing.T) {
	db := setupModelsTestDB(t)
	repo := NewModelRepository(db)

	insertTextWrittenModel(t, db, "broken", `not json`)

	_, err := repo.GetByID(context.Background(), 7, "broken")
	require.Error(t, err, "an unreadable parameters column must not load as an empty configuration")
	assert.Contains(t, err.Error(), "parameters",
		"the failure must name the column that could not be read")
}
